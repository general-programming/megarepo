package device

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every test here talks to an httptest server. NOTHING in this file may
// ever be pointed at a real device.

// vyosWriteServer records the /configure and /config-file requests a
// writer makes and answers them successfully.
type vyosWriteServer struct {
	*httptest.Server
	paths []string
	datas []string
	keys  []string
}

func newVyOSWriteServer(t *testing.T) *vyosWriteServer {
	t.Helper()
	s := &vyosWriteServer{}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing form: %v", err)
			return
		}
		s.paths = append(s.paths, r.URL.Path)
		s.datas = append(s.datas, r.PostFormValue("data"))
		s.keys = append(s.keys, r.PostFormValue("key"))
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":"","error":null}`)
	}))
	t.Cleanup(s.Close)
	return s
}

func writeOptions(t *testing.T, s *vyosWriteServer) Options {
	t.Helper()
	opts := testOptions(t, s.Server)
	opts.AllowWrites = true
	return opts
}

// TestNewVyOSWriterRequiresAllowWrites is the safety interlock: without
// the explicit opt-in there is no writer to call.
func TestNewVyOSWriterRequiresAllowWrites(t *testing.T) {
	server := newVyOSWriteServer(t)
	opts := testOptions(t, server.Server) // AllowWrites deliberately unset

	writer, err := NewVyOSWriter(testHost("spine", "vyos"), opts)
	if err == nil {
		t.Fatalf("NewVyOSWriter returned a writer without AllowWrites: %#v", writer)
	}
	var notAllowed *ErrWritesNotAllowed
	if !errors.As(err, &notAllowed) {
		t.Fatalf("err = %v, want ErrWritesNotAllowed", err)
	}
	if writer != nil {
		t.Fatalf("writer = %#v, want nil", writer)
	}
	if len(server.paths) != 0 {
		t.Fatalf("a refused construction still hit the wire: %v", server.paths)
	}
}

func TestNewVyOSWriterRejections(t *testing.T) {
	server := newVyOSWriteServer(t)

	t.Run("nil host", func(t *testing.T) {
		if _, err := NewVyOSWriter(nil, writeOptions(t, server)); err == nil {
			t.Fatal("want error for nil host")
		}
	})

	t.Run("wrong devicetype", func(t *testing.T) {
		_, err := NewVyOSWriter(testHost("switch", "eos"), writeOptions(t, server))
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("err = %v, want ErrUnsupported", err)
		}
	})

	t.Run("missing global secrets", func(t *testing.T) {
		opts := writeOptions(t, server)
		opts.GlobalSecrets = nil
		if _, err := NewVyOSWriter(testHost("spine", "vyos"), opts); err == nil {
			t.Fatal("want error without GlobalSecrets")
		}
	})

	t.Run("devicetype casing tolerated", func(t *testing.T) {
		if _, err := NewVyOSWriter(testHost("spine", "VyOS"), writeOptions(t, server)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

// TestVyOSConfigureWireFormat pins the exact request the Python
// implementation sends: a POST of `data` (a bare JSON array of
// {op, path}) plus `key`, to /configure.
func TestVyOSConfigureWireFormat(t *testing.T) {
	server := newVyOSWriteServer(t)
	writer, err := NewVyOSWriter(testHost("spine", "vyos"), writeOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}

	ops := []Op{
		{Verb: OpDelete, Path: []string{"system", "name-server", "8.8.8.8"}},
		{Verb: OpSet, Path: []string{"interfaces", "ethernet", "eth0", "description", "uplink a"}},
	}
	if err := writer.Configure(context.Background(), ops); err != nil {
		t.Fatal(err)
	}

	if len(server.paths) != 1 || server.paths[0] != "/configure" {
		t.Fatalf("paths = %v, want one /configure", server.paths)
	}
	if server.keys[0] != "api-key" {
		t.Fatalf("key = %q", server.keys[0])
	}

	var decoded []map[string]any
	if err := json.Unmarshal([]byte(server.datas[0]), &decoded); err != nil {
		t.Fatalf("payload %q is not a JSON array of ops: %v", server.datas[0], err)
	}
	if len(decoded) != 2 {
		t.Fatalf("ops = %v", decoded)
	}
	// Order matters: deletes are sent before sets, in one commit.
	if decoded[0]["op"] != "delete" || decoded[1]["op"] != "set" {
		t.Fatalf("op order = %v", decoded)
	}
	if got := decoded[0]["path"].([]any); len(got) != 3 || got[2] != "8.8.8.8" {
		t.Fatalf("delete path = %v", got)
	}
	// No EOS-shaped "command" key leaks into the VyOS payload.
	for _, op := range decoded {
		if len(op) != 2 {
			t.Fatalf("unexpected keys in op %v", op)
		}
	}
}

// TestVyOSConfigureEmptyIsNoWire: a deploy with nothing to do must not
// open a commit on the device.
func TestVyOSConfigureEmptyIsNoWire(t *testing.T) {
	server := newVyOSWriteServer(t)
	writer, _ := NewVyOSWriter(testHost("spine", "vyos"), writeOptions(t, server))

	if err := writer.Configure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(context.Background(), []Op{}); err != nil {
		t.Fatal(err)
	}
	if len(server.paths) != 0 {
		t.Fatalf("empty configure hit the wire: %v", server.paths)
	}
}

func TestVyOSConfigureRejectsBadOps(t *testing.T) {
	tests := []struct {
		name string
		ops  []Op
	}{
		{"unknown verb", []Op{{Verb: "reboot", Path: []string{"system"}}}},
		{"empty verb", []Op{{Path: []string{"system"}}}},
		{"image delete shape", []Op{{Verb: "image", Path: []string{"delete"}}}},
		{"empty path would target the config root", []Op{{Verb: OpDelete}}},
		{"eos-shaped op", []Op{{Command: "no ip routing", Verb: OpSet, Path: []string{"system"}}}},
		{"one bad op poisons the whole batch", []Op{
			{Verb: OpSet, Path: []string{"system", "host-name", "r1"}},
			{Verb: "wipe", Path: []string{"system"}},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := newVyOSWriteServer(t)
			writer, _ := NewVyOSWriter(testHost("spine", "vyos"), writeOptions(t, server))

			if err := writer.Configure(context.Background(), tc.ops); err == nil {
				t.Fatal("want error")
			}
			if len(server.paths) != 0 {
				t.Fatalf("a rejected op still reached the device: %v", server.paths)
			}
		})
	}
}

func TestVyOSSaveConfig(t *testing.T) {
	server := newVyOSWriteServer(t)
	writer, _ := NewVyOSWriter(testHost("spine", "vyos"), writeOptions(t, server))

	if err := writer.SaveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(server.paths) != 1 || server.paths[0] != "/config-file" {
		t.Fatalf("paths = %v", server.paths)
	}
	if !strings.Contains(server.datas[0], `"save"`) {
		t.Fatalf("data = %q", server.datas[0])
	}
}

// TestVyOSWriterEndpointAllowlist proves /image stays unreachable from
// the writer too: image deletion is not ported.
func TestVyOSWriterEndpointAllowlist(t *testing.T) {
	tests := []struct {
		endpoint, op string
		want         bool
	}{
		{"configure", "", true},
		{"config-file", "save", true},
		{"image", "delete", false},
		{"image", "", false},
		{"config-file", "load", false},
		{"config-file", "", false},
		{"show", "show", false},
		{"retrieve", "showConfig", false},
		{"reset", "", false},
		{"reboot", "", false},
		{"poweroff", "", false},
		{"generate", "", false},
	}
	for _, tc := range tests {
		if got := vyosWriteRequestAllowed(tc.endpoint, tc.op); got != tc.want {
			t.Errorf("vyosWriteRequestAllowed(%q, %q) = %v, want %v", tc.endpoint, tc.op, got, tc.want)
		}
	}
}

// TestVyOSWriterRequestGuard: the writer's own primitive refuses an
// endpoint outside its allowlist even when called internally.
func TestVyOSWriterRequestGuard(t *testing.T) {
	server := newVyOSWriteServer(t)
	writer, _ := NewVyOSWriter(testHost("spine", "vyos"), writeOptions(t, server))

	_, err := writer.request(context.Background(), "image", "delete", map[string]any{"op": "delete"})
	var attempt *ErrWriteAttempt
	if !errors.As(err, &attempt) {
		t.Fatalf("err = %v, want ErrWriteAttempt", err)
	}
	if len(server.paths) != 0 {
		t.Fatalf("guard leaked a request: %v", server.paths)
	}
}

// TestReaderStillCannotWrite: adding a writer must not have loosened the
// read transport's guard.
func TestReaderStillCannotWrite(t *testing.T) {
	for _, tc := range []struct{ endpoint, op string }{
		{"configure", "set"},
		{"configure", ""},
		{"config-file", "save"},
		{"image", "delete"},
	} {
		if vyosRequestAllowed(tc.endpoint, tc.op) {
			t.Errorf("read guard now admits %q/%q", tc.endpoint, tc.op)
		}
	}

	server := newVyOSWriteServer(t)
	// Even with AllowWrites set, a Reader stays a Reader.
	reader, err := NewVyOS(testHost("spine", "vyos"), writeOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.request(context.Background(), "configure", "set", []any{}); err == nil {
		t.Fatal("reader accepted a /configure request")
	}
	if _, ok := any(reader).(Writer); ok {
		t.Fatal("VyOSReader satisfies Writer")
	}
	if len(server.paths) != 0 {
		t.Fatalf("reader reached the wire: %v", server.paths)
	}
}

func TestVyOSWriterSurfacesAPIErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":false,"data":null,"error":"commit failed: interface eth9 does not exist"}`)
	}))
	defer server.Close()

	opts := testOptions(t, server)
	opts.AllowWrites = true
	writer, _ := NewVyOSWriter(testHost("spine", "vyos"), opts)

	err := writer.Configure(context.Background(), []Op{{Verb: OpSet, Path: []string{"a", "b"}}})
	if err == nil || !strings.Contains(err.Error(), "commit failed") {
		t.Fatalf("err = %v, want the API error surfaced", err)
	}
}

// deadlineRecorder observes the budget each outgoing request carries on
// its context. It is a client-side probe on purpose: an HTTP server
// context does not inherit the client's deadline, so the assertion has
// to be made before the request leaves.
type deadlineRecorder struct {
	base http.RoundTripper
	mu   sync.Mutex
	seen map[string]time.Duration
}

func newDeadlineRecorder(base http.RoundTripper) *deadlineRecorder {
	return &deadlineRecorder{base: base, seen: map[string]time.Duration{}}
}

func (d *deadlineRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	var budget time.Duration
	if deadline, ok := req.Context().Deadline(); ok {
		budget = time.Until(deadline).Round(time.Second)
	}
	d.mu.Lock()
	d.seen[req.URL.Path] = budget
	d.mu.Unlock()
	return d.base.RoundTrip(req)
}

func (d *deadlineRecorder) budget(path string) time.Duration {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[path]
}

// recordingOptions returns opts with its client wrapped so every request's
// context budget is captured.
func recordingOptions(opts Options) (Options, *deadlineRecorder) {
	recorder := newDeadlineRecorder(opts.HTTPClient.Transport)
	client := *opts.HTTPClient
	client.Transport = recorder
	opts.HTTPClient = &client
	return opts, recorder
}

// Regression: the writer used one client-wide http.Client.Timeout of 10s
// for everything. Python gives /configure 120s and /config-file 60s
// (vyos_api.py), and a real commit on a busy router regularly takes
// longer than 10s — so barf aborted the request, reported a failed
// deploy, and skipped SaveConfig, leaving the device running config that
// would not survive a reboot. The commit itself had already landed.
func TestVyOSWriterGivesEachOperationItsOwnTimeout(t *testing.T) {
	server := newVyOSWriteServer(t)
	opts, recorder := recordingOptions(writeOptions(t, server))
	opts.Timeout = 0 // the default: per-operation budgets apply

	writer, err := NewVyOSWriter(testHost("spine", "vyos"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Configure(context.Background(), []Op{{Verb: OpSet, Path: []string{"a", "b"}}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.SaveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := recorder.budget("/configure"); got != TimeoutConfigure {
		t.Errorf("/configure budget = %v, want %v (Python: vyos_api_configure=120s)", got, TimeoutConfigure)
	}
	if got := recorder.budget("/config-file"); got != TimeoutSave {
		t.Errorf("/config-file budget = %v, want %v (Python: vyos_api_config_save=60s)", got, TimeoutSave)
	}
}

// Reads keep the read budgets (10s show, 30s retrieve), and an explicit
// Options.Timeout still overrides every operation — the escape hatch must
// not have been lost along the way.
func TestVyOSReaderTimeoutsAndOverride(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/retrieve" {
			io.WriteString(w, `{"success":true,"data":{"system":{}},"error":null}`)
			return
		}
		io.WriteString(w, `{"success":true,"data":"VyOS 1.4.2","error":null}`)
	}))
	defer server.Close()

	opts, recorder := recordingOptions(testOptions(t, server))
	reader, err := NewVyOS(testHost("spine", "vyos"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.RetrieveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Show(context.Background(), "version"); err != nil {
		t.Fatal(err)
	}
	if got := recorder.budget("/retrieve"); got != TimeoutRetrieve {
		t.Errorf("/retrieve budget = %v, want %v (Python: 30s)", got, TimeoutRetrieve)
	}
	if got := recorder.budget("/show"); got != TimeoutShow {
		t.Errorf("/show budget = %v, want %v (Python: 10s)", got, TimeoutShow)
	}

	pinnedOpts, pinnedRecorder := recordingOptions(testOptions(t, server))
	pinnedOpts.Timeout = 5 * time.Second
	pinned, err := NewVyOS(testHost("spine", "vyos"), pinnedOpts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pinned.RetrieveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := pinnedRecorder.budget("/retrieve"); got != 5*time.Second {
		t.Errorf("Options.Timeout override = %v, want 5s", got)
	}
}

// A caller's own, shorter deadline must still win: an operation budget
// may only bound a request, never extend one.
func TestOperationTimeoutNeverExtendsCallerDeadline(t *testing.T) {
	server := newVyOSWriteServer(t)
	opts, recorder := recordingOptions(writeOptions(t, server))
	writer, err := NewVyOSWriter(testHost("spine", "vyos"), opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := writer.Configure(ctx, []Op{{Verb: OpSet, Path: []string{"a", "b"}}}); err != nil {
		t.Fatal(err)
	}
	if got := recorder.budget("/configure"); got != 3*time.Second {
		t.Errorf("budget = %v, want the caller's 3s, not the 120s commit budget", got)
	}
}

func TestVyOSOps(t *testing.T) {
	ops := VyOSOps(OpDelete, [][]string{{"a", "b"}, {"c"}})
	if len(ops) != 2 || ops[0].Verb != OpDelete || ops[1].Path[0] != "c" {
		t.Fatalf("VyOSOps = %#v", ops)
	}
	if VyOSOps(OpSet, nil) == nil {
		t.Fatal("VyOSOps(nil) should return an empty slice, not nil")
	}
}
