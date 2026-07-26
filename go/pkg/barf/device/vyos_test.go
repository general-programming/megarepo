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
