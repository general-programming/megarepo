package device

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// captured `show version` output from a VyOS rolling release.
const vyosShowVersion = `Version:          VyOS 2026.06.30-0048-rolling
Release train:    current
Release flavor:   generic

Built by:         autobuild@vyos.net
Built on:         Mon 30 Jun 2026 00:48 UTC
Build UUID:       0dbe4e1d-63cf-4e7f-9f2e-3fa2b8d3f0aa
Build commit ID:  6a1f0f1b3f9

Architecture:     x86_64
Boot via:         installed image
System type:      KVM guest

Hardware vendor:  QEMU
Hardware model:   Standard PC (Q35 + ICH9, 2009)
Hardware S/N:
Hardware UUID:    4f8b6a30-1c1e-4e21-8a6f-2f9b0c1d2e3f

Copyright:        VyOS maintainers and contributors
`

func TestVyOSStatus(t *testing.T) {
	var seen []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if key := r.PostFormValue("key"); key != "api-key" {
			t.Errorf("key = %q", key)
		}
		data := r.PostFormValue("data")
		seen = append(seen, r.URL.Path+" "+data)
		switch {
		case strings.Contains(data, `"version"`):
			writeVyOSData(w, vyosShowVersion)
		case strings.Contains(data, `"uptime"`):
			writeVyOSData(w, "Uptime:  17 days, 4 hours, 9 minutes\n")
		default:
			t.Fatalf("unexpected request %q", data)
		}
	}))
	defer server.Close()

	reader, err := NewVyOS(testHost("fmt2-vpn-spine-1", "vyos"), testOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}
	status, err := reader.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if status.Version != "2026.06.30-0048-rolling" {
		t.Errorf("version = %q", status.Version)
	}
	if status.Model != "QEMU Standard PC (Q35 + ICH9, 2009)" {
		t.Errorf("model = %q", status.Model)
	}
	if status.Uptime != "17 days, 4 hours, 9 minutes" {
		t.Errorf("uptime = %q", status.Uptime)
	}
	if len(seen) != 2 {
		t.Fatalf("made %d requests, want 2 (version cached for version+model): %v", len(seen), seen)
	}
	if !strings.HasPrefix(seen[0], "/show ") {
		t.Errorf("first request = %q", seen[0])
	}
}

func writeVyOSData(w http.ResponseWriter, data string) {
	w.Header().Set("Content-Type", "application/json")
	body, _ := json.Marshal(map[string]any{"success": true, "data": data, "error": nil})
	w.Write(body)
}

func TestVyOSRetrieveConfig(t *testing.T) {
	var gotPath, gotData string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		gotPath, gotData = r.URL.Path, r.PostFormValue("data")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":{"system":{"host-name":"spine-1"}},"error":null}`)
	}))
	defer server.Close()

	reader, _ := NewVyOS(testHost("spine", "vyos"), testOptions(t, server))
	tree, err := reader.RetrieveConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/retrieve" {
		t.Errorf("path = %q", gotPath)
	}
	if !strings.Contains(gotData, `"showConfig"`) {
		t.Errorf("data = %q", gotData)
	}
	system := tree["system"].(map[string]any)
	if system["host-name"] != "spine-1" {
		t.Errorf("tree = %v", tree)
	}

	config, err := reader.RunningConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(config, `"host-name": "spine-1"`) {
		t.Errorf("config = %q", config)
	}
}

func TestVyOSAPIError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":false,"data":null,"error":"invalid API key"}`)
	}))
	defer server.Close()

	reader, _ := NewVyOS(testHost("spine", "vyos"), testOptions(t, server))
	_, err := reader.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid API key") {
		t.Errorf("err = %v", err)
	}
}

// The write guard: no write endpoint or op can be reached.
func TestVyOSRequestAllowed(t *testing.T) {
	allowed := [][2]string{{"show", "show"}, {"retrieve", "showConfig"}}
	for _, pair := range allowed {
		if !vyosRequestAllowed(pair[0], pair[1]) {
			t.Errorf("vyosRequestAllowed(%q,%q) = false, want true", pair[0], pair[1])
		}
	}

	refused := [][2]string{
		{"configure", "set"},
		{"configure", "delete"},
		{"configure", "comment"},
		{"config-file", "save"},
		{"config-file", "load"},
		{"image", "delete"},
		{"image", "add"},
		{"reset", "reset"},
		{"reboot", "reboot"},
		{"generate", "generate"},
		{"show", "set"},     // right endpoint, write op
		{"retrieve", "set"}, // ditto
		{"retrieve", "returnValue"},
		{"", ""},
	}
	for _, pair := range refused {
		if vyosRequestAllowed(pair[0], pair[1]) {
			t.Errorf("vyosRequestAllowed(%q,%q) = true, want false", pair[0], pair[1])
		}
	}
}

func TestVyOSRequestRefusesWrites(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a write request reached the wire")
	}))
	defer server.Close()

	reader, _ := NewVyOS(testHost("spine", "vyos"), testOptions(t, server))
	_, err := reader.request(context.Background(), "configure", "set",
		[]any{map[string]any{"op": "set", "path": []string{"system", "host-name", "pwned"}}})
	var writeErr *ErrWriteAttempt
	if !errors.As(err, &writeErr) {
		t.Fatalf("err = %v, want ErrWriteAttempt", err)
	}
}

func TestParseVyOSVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{vyosShowVersion, "2026.06.30-0048-rolling"},
		{"Version:  VyOS 1.4.2\n", "1.4.2"},
		{"VyOS 1.3.0\nsomething\n", "1.3.0"},
		{"", "-"},
		{"   \n", "-"},
	}
	for _, tc := range cases {
		if got := ParseVyOSVersion(tc.in); got != tc.want {
			t.Errorf("ParseVyOSVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseVyOSModel(t *testing.T) {
	cases := []struct{ in, want string }{
		{vyosShowVersion, "QEMU Standard PC (Q35 + ICH9, 2009)"},
		// Model already repeats the vendor: no prefix.
		{"Hardware vendor:  Supermicro\nHardware model:   Supermicro SYS-5018D\n", "Supermicro SYS-5018D"},
		{"Hardware vendor:  Dell Inc.\n", "Dell Inc."},
		{"Hardware model:   PowerEdge R240\n", "PowerEdge R240"},
		{"nothing here\n", "?"},
	}
	for _, tc := range cases {
		if got := ParseVyOSModel(tc.in); got != tc.want {
			t.Errorf("ParseVyOSModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseVyOSUptime(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Uptime:  17 days, 4 hours, 9 minutes\n", "17 days, 4 hours, 9 minutes"},
		{"\n\n  3 days\n", "3 days"},
		{"", "-"},
	}
	for _, tc := range cases {
		if got := ParseVyOSUptime(tc.in); got != tc.want {
			t.Errorf("ParseVyOSUptime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestParseSystemImages(t *testing.T) {
	table := `Name                            Default boot    Running
------------------------------  --------------  --------
2026.06.30-0048-rolling         Yes             Yes
1.4.2                           No              No
`
	images := ParseSystemImages(table)
	if len(images) != 2 {
		t.Fatalf("images = %+v", images)
	}
	if images[0] != (SystemImage{Name: "2026.06.30-0048-rolling", DefaultBoot: true, Running: true}) {
		t.Errorf("images[0] = %+v", images[0])
	}
	if images[1] != (SystemImage{Name: "1.4.2"}) {
		t.Errorf("images[1] = %+v", images[1])
	}

	legacy := `The system currently has the following image(s) installed:

   1: 1.4.2 (default boot) (running image)
   2: 1.4.1
`
	images = ParseSystemImages(legacy)
	if len(images) != 2 {
		t.Fatalf("images = %+v", images)
	}
	if images[0] != (SystemImage{Name: "1.4.2", DefaultBoot: true, Running: true}) {
		t.Errorf("images[0] = %+v", images[0])
	}
	if images[1] != (SystemImage{Name: "1.4.1"}) {
		t.Errorf("images[1] = %+v", images[1])
	}

	if got := ParseSystemImages(""); len(got) != 0 {
		t.Errorf("empty -> %+v", got)
	}
}

// Regression: `json.Unmarshal("null", &map)` succeeds and leaves a nil
// map, so a /retrieve that answered `{"success":true,"data":null}` was
// accepted as a valid — and empty — running config. The running set then
// collapsed to nothing and the operator was shown a full-reconfigure diff
// for what was really a failed read. Python refuses it outright:
// vyos_api.py `if not isinstance(data, dict): raise`.
func TestVyOSRetrieveConfigRejectsNonObjectPayload(t *testing.T) {
	for _, payload := range []string{`null`, `[]`, `"text"`, `42`, `true`} {
		t.Run(payload, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"success":true,"data":`+payload+`,"error":null}`)
			}))
			defer server.Close()

			reader, err := NewVyOS(testHost("fmt2-vpn-spine-1", "vyos"), testOptions(t, server))
			if err != nil {
				t.Fatal(err)
			}
			tree, err := reader.RetrieveConfig(context.Background())
			if err == nil {
				t.Fatalf("payload %s accepted as a config tree: %#v", payload, tree)
			}
			if tree != nil {
				t.Errorf("a tree came back alongside the error: %#v", tree)
			}
			if !strings.Contains(err.Error(), "/retrieve payload") {
				t.Errorf("err = %v, want it to name the payload", err)
			}
		})
	}
}

// RunningConfig must fail for the same reason: it is RetrieveConfig's
// only caller here and must not print `{}` for a failed read.
func TestVyOSRunningConfigRejectsNullPayload(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"success":true,"data":null,"error":null}`)
	}))
	defer server.Close()

	reader, err := NewVyOS(testHost("fmt2-vpn-spine-1", "vyos"), testOptions(t, server))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reader.RunningConfig(context.Background()); err == nil {
		t.Fatalf("RunningConfig accepted a null payload: %q", got)
	}
}

// Regression: strings.Split(_, "\n") leaves CRLF's "\r" on the end of
// every line. Most parsers TrimSpace it away afterwards, but the version
// *fallback* returns its line verbatim — so a device answering with CRLF
// yielded "1.4.2\r" where Python's splitlines() yields "1.4.2", and that
// value goes straight into the fleet table and version comparisons.
func TestParseVyOSHandlesPythonLineBoundaries(t *testing.T) {
	if got := ParseVyOSVersion("VyOS 1.4.2\r\nsomething else\r\n"); got != "1.4.2" {
		t.Errorf("CRLF fallback = %q, want %q", got, "1.4.2")
	}
	if got := ParseVyOSVersion("Version:  VyOS 1.4.2\r\nRelease train: current\r\n"); got != "1.4.2" {
		t.Errorf("CRLF labelled = %q, want %q", got, "1.4.2")
	}
	// Python's splitlines() also breaks on \f, \v and U+0085 (NEL).
	if got := ParseVyOSVersion("VyOS 1.4.2\fsomething else"); got != "1.4.2" {
		t.Errorf("form-feed fallback = %q, want %q", got, "1.4.2")
	}
	if got := ParseVyOSVersion("Version: VyOS 1.4.2\vRelease train: current"); got != "1.4.2" {
		t.Errorf("vertical-tab labelled = %q, want %q", got, "1.4.2")
	}
	if got := ParseVyOSModel("Hardware vendor: QEMU\r\nHardware model: Standard PC\r\n"); got != "QEMU Standard PC" {
		t.Errorf("CRLF model = %q", got)
	}
	if got := ParseVyOSUptime("Uptime:  17 days\r\n"); got != "17 days" {
		t.Errorf("CRLF uptime = %q", got)
	}
	images := ParseSystemImages("   1: 1.4.2 (default boot) (running image)\r\n   2: 1.4.1\r\n")
	if len(images) != 2 || images[0].Name != "1.4.2" || !images[0].DefaultBoot || images[1].Name != "1.4.1" {
		t.Errorf("CRLF images = %+v", images)
	}
}

// splitLines is the shared helper those parsers rely on; pin its contract
// against Python's str.splitlines() directly.
func TestSplitLinesMatchesPythonSplitlines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\rb", []string{"a", "b"}},
		{"a\r\n", []string{"a"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"a\vb", []string{"a", "b"}},
		{"a\fb", []string{"a", "b"}},
		{"a\x1cb", []string{"a", "b"}},
		{"a\x1db", []string{"a", "b"}},
		{"a\x1eb", []string{"a", "b"}},
		{"a\u0085b", []string{"a", "b"}},
		{"a\u2028b", []string{"a", "b"}},
		{"a\u2029b", []string{"a", "b"}},
		// Not boundaries in Python: tab, space, NBSP.
		{"a\tb", []string{"a\tb"}},
		{"a b", []string{"a b"}},
		{"a\u00a0b", []string{"a\u00a0b"}},
	}
	for _, tc := range tests {
		got := splitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitLines(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitLines(%q) = %q, want %q", tc.in, got, tc.want)
				break
			}
		}
	}
}
