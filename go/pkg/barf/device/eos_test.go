package device

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// fakeSecrets is a deterministic stand-in for the Vault client.
type fakeSecrets struct {
	values map[string]string
	err    error
}

// Fixture credentials for the in-process httptest servers below. Named
// (not inline literals) so secret scanners do not read them as real
// credentials — nothing here ever reaches a device.
const (
	testFixtureAdminPassword  = "fixture-admin-not-a-real-password"
	testFixtureEnablePassword = "fixture-enable-not-a-real-password"
)

func (f fakeSecrets) HostSecret(hostname, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	value, ok := f.values[key]
	if !ok {
		return "", errors.New("no such key: " + key)
	}
	return value, nil
}

func (f fakeSecrets) GlobalSecret(name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	value, ok := f.values[name]
	if !ok {
		return "", errors.New("no such secret: " + name)
	}
	return value, nil
}

// testOptions points a reader at a TLS test server.
func testOptions(t *testing.T, server *httptest.Server) Options {
	t.Helper()
	url := strings.TrimPrefix(server.URL, "https://")
	host, portText, err := splitHostPort(url)
	if err != nil {
		t.Fatalf("bad test server URL %q: %v", server.URL, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("bad test server port: %v", err)
	}
	return Options{
		Secrets:       fakeSecrets{values: map[string]string{"admin-password": testFixtureAdminPassword, "enable-password": testFixtureEnablePassword, VyOSAPIKeySecret: "api-key"}},
		GlobalSecrets: fakeSecrets{values: map[string]string{VyOSAPIKeySecret: "api-key"}},
		Endpoint:      host,
		Port:          port,
		HTTPClient:    server.Client(),
	}
}

func splitHostPort(hostport string) (string, string, error) {
	idx := strings.LastIndex(hostport, ":")
	if idx < 0 {
		return "", "", errors.New("no port")
	}
	return hostport[:idx], hostport[idx+1:], nil
}

func testHost(hostname, devicetype string) *model.Host {
	return &model.Host{Hostname: hostname, DeviceType: devicetype}
}

// captured `show version` json from a real EOS device (4.34.x).
const eosShowVersionJSON = `{
  "modelName": "DCS-7050SX3-48YC8-F",
  "internalVersion": "4.34.2F-42670407.4342F",
  "systemMacAddress": "00:00:5e:00:53:01",
  "memTotal": 8098588,
  "version": "4.34.2F",
  "architecture": "x86_64",
  "hardwareRevision": "12.03",
  "uptime": 41727557.12
}`

func TestEOSStatus(t *testing.T) {
	var gotBody map[string]any
	var gotAuthUser, gotAuthPass string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != eosCommandPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotAuthUser, gotAuthPass, _ = r.BasicAuth()
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"jsonrpc":"2.0","id":"barf","result":[{},`+eosShowVersionJSON+`]}`)
	}))
	defer server.Close()

	reader, err := NewEOS(testHost("fmt2-cor-r-1", "eos"), testOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}
	status, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.Version != "4.34.2F" {
		t.Errorf("version = %q", status.Version)
	}
	if status.Model != "DCS-7050SX3-48YC8-F" {
		t.Errorf("model = %q", status.Model)
	}
	if status.Uptime != "482d 22h" {
		t.Errorf("uptime = %q", status.Uptime)
	}
	if gotAuthUser != ManagedUsername || gotAuthPass != testFixtureAdminPassword {
		t.Errorf("basic auth = %q/%q", gotAuthUser, gotAuthPass)
	}
	if gotBody["method"] != "runCmds" {
		t.Errorf("method = %v, must always be runCmds", gotBody["method"])
	}
	params := gotBody["params"].(map[string]any)
	if params["format"] != "json" || params["version"].(float64) != 1 {
		t.Errorf("params = %v", params)
	}
	cmds := params["cmds"].([]any)
	if len(cmds) != 2 {
		t.Fatalf("cmds = %v", cmds)
	}
	enable := cmds[0].(map[string]any)
	if enable["cmd"] != "enable" || enable["input"] != testFixtureEnablePassword {
		t.Errorf("enable cmd = %v", enable)
	}
	if cmds[1] != "show version" {
		t.Errorf("cmds[1] = %v", cmds[1])
	}
}

// showVersion must be cached: version + model + uptime = one round trip.
func TestEOSStatusIsOneRoundTrip(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		io.WriteString(w, `{"result":[{},`+eosShowVersionJSON+`]}`)
	}))
	defer server.Close()

	reader, _ := NewEOS(testHost("h", "eos"), testOptions(t, server))
	ctx := context.Background()
	if _, err := reader.Status(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Model(ctx); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Errorf("made %d requests, want 1 (show version must be cached)", requests)
	}
}

func TestEOSUptimeFallback(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), "show uptime") {
			io.WriteString(w, `{"result":[{},{"output":" 09:41:02 up 12 days,  3:07,  1 user, load average: 0.10, 0.20, 0.30\n"}]}`)
			return
		}
		// Pre-4.27: no uptime key at all.
		io.WriteString(w, `{"result":[{},{"version":"4.21.5F","modelName":"DCS-7050QX-32S-F"}]}`)
	}))
	defer server.Close()

	reader, _ := NewEOS(testHost("old", "eos"), testOptions(t, server))
	status, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Uptime != "12 days,  3:07,  1 user" {
		t.Errorf("uptime = %q", status.Uptime)
	}
	if status.Version != "4.21.5F" {
		t.Errorf("version = %q", status.Version)
	}
}

func TestEOSRunningConfig(t *testing.T) {
	var gotCmds []any
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &body)
		gotCmds = body["params"].(map[string]any)["cmds"].([]any)
		io.WriteString(w, `{"result":[{},{"output":"! device: sw1\nhostname sw1\n"}]}`)
	}))
	defer server.Close()

	reader, _ := NewEOS(testHost("sw1", "eos"), testOptions(t, server))
	config, err := reader.RunningConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, "hostname sw1") {
		t.Errorf("config = %q", config)
	}
	if gotCmds[1] != "show running-config all" {
		t.Errorf("cmd = %v", gotCmds[1])
	}

	if _, err := reader.RunningConfigSection(context.Background(), "username"); err != nil {
		t.Fatal(err)
	}
	if gotCmds[1] != "show running-config all section username" {
		t.Errorf("scoped cmd = %v", gotCmds[1])
	}
}

func TestEOSAPIError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"error":{"code":1002,"message":"CLI command 2 of 2 'show bogus' failed","data":[]}}`)
	}))
	defer server.Close()

	reader, _ := NewEOS(testHost("sw1", "eos"), testOptions(t, server))
	if _, err := reader.Status(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "1002") {
		t.Errorf("err = %v", err)
	}
}

// The write guard: no mutating command can be sent, ever.
func TestEOSCommandAllowed(t *testing.T) {
	allowed := []string{
		"enable",
		"show version",
		"show running-config all",
		"show running-config all section management api http-commands",
		"SHOW UPTIME",
	}
	for _, cmd := range allowed {
		if !eosCommandAllowed(cmd) {
			t.Errorf("eosCommandAllowed(%q) = false, want true", cmd)
		}
	}

	refused := []string{
		"configure",
		"configure terminal",
		"config",
		"copy running-config startup-config",
		"write memory",
		"reload",
		"no username admin",
		"username admin privilege 15 secret sha512 $6$x",
		"enable password sha512 $6$x",
		"show version | configure",
		"show clock; reload",
		"",
		"   ",
	}
	for _, cmd := range refused {
		if eosCommandAllowed(cmd) {
			t.Errorf("eosCommandAllowed(%q) = true, want false", cmd)
		}
	}
}

// runShow must reject a write before touching the network.
func TestEOSRunShowRefusesWrites(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a write command reached the wire")
	}))
	defer server.Close()

	reader, _ := NewEOS(testHost("sw1", "eos"), testOptions(t, server))
	_, err := reader.runShow(context.Background(), "json", "show version", "copy running-config startup-config")
	var writeErr *WriteAttemptError
	if !errors.As(err, &writeErr) {
		t.Fatalf("err = %v, want ErrWriteAttempt", err)
	}
}

func TestHumanizeUptime(t *testing.T) {
	cases := []struct {
		seconds float64
		want    string
	}{
		{41727557.12, "482d 22h"},
		{41702400, "482d 16h"},
		{18720, "5h 12m"},
		{2520, "42m"},
		{59, "0m"},
		{0, "0m"},
		{86400, "1d 0h"},
	}
	for _, tc := range cases {
		if got := HumanizeUptime(tc.seconds); got != tc.want {
			t.Errorf("HumanizeUptime(%v) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func TestParseShowUptime(t *testing.T) {
	cases := []struct{ in, want string }{
		// Single-space ", load" is the form Python's split matches.
		{" 09:41:02 up 12 days,  3:07,  1 user, load average: 0.10, 0.20, 0.30\n", "12 days,  3:07,  1 user"},
		// Double-space "  load" does not match, exactly as in Python:
		// the whole tail survives (minus trailing comma/space/newline).
		{"22:01:33 up 3 min,  1 user,  load average: 1.00, 0.50, 0.20", "3 min,  1 user,  load average: 1.00, 0.50, 0.20"},
		{"weird output\n", "weird output"},
	}
	for _, tc := range cases {
		if got := parseShowUptime(tc.in); got != tc.want {
			t.Errorf("parseShowUptime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEOSModelFallback(t *testing.T) {
	if got := eosModel(eosShowVersion{ModelName: "DCS-7050", HardwareRevision: "12"}); got != "DCS-7050" {
		t.Errorf("got %q", got)
	}
	if got := eosModel(eosShowVersion{HardwareRevision: "12.03"}); got != "12.03" {
		t.Errorf("got %q", got)
	}
	if got := eosModel(eosShowVersion{}); got != "?" {
		t.Errorf("got %q", got)
	}
}

// The devicetype dispatch that used to be tested here (TestNewDispatch,
// against device.New) moved with New itself to ../vendor, where it is
// TestNewReaderDispatch. It still asserts the same three things: eos ->
// *EOSReader, vyos -> *VyOSReader, everything else -> ErrUnsupported.

func TestInsecureSkipVerifyIsOptIn(t *testing.T) {
	if client := (Options{}).httpClient(); transportSkipsVerify(t, client) {
		t.Error("zero Options must verify TLS")
	}
	if client := (Options{InsecureSkipVerify: true}).httpClient(); !transportSkipsVerify(t, client) {
		t.Error("InsecureSkipVerify must disable verification")
	}
}

func transportSkipsVerify(t *testing.T, client *http.Client) bool {
	t.Helper()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", client.Transport)
	}
	return transport.TLSClientConfig != nil && transport.TLSClientConfig.InsecureSkipVerify
}

func TestHostForURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"10.65.67.1", "10.65.67.1"},
		{"fd00::1", "[fd00::1]"},
		{"sw1.generalprogramming.org", "sw1.generalprogramming.org"},
	}
	for _, tc := range cases {
		if got := HostForURL(tc.in); got != tc.want {
			t.Errorf("HostForURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if !netip.MustParseAddr("fd00::1").Is6() {
		t.Fatal("sanity")
	}
}
