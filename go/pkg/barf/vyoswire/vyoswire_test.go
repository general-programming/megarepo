package vyoswire

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestFormBodyCarriesDataAndKey(t *testing.T) {
	body, err := FormBody("s3cret", map[string]any{"op": "show", "path": []string{"version"}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(body)
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Get("key"); got != "s3cret" {
		t.Errorf("key = %q", got)
	}
	if got := values.Get("data"); got != `{"op":"show","path":["version"]}` {
		t.Errorf("data = %q", got)
	}
}

func TestDecodeSurfacesDeviceError(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string error", `{"success":false,"error":"no such path"}`, "dev: vyos api: no such path"},
		{"empty error", `{"success":false,"error":""}`, "dev: vyos api: unknown API error"},
		{"null error", `{"success":false}`, "dev: vyos api: unknown API error"},
		{"non-string error", `{"success":false,"error":{"code":7}}`, "dev: vyos api: map[code:7]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode("dev", 200, []byte(tc.raw))
			if err == nil || err.Error() != tc.want {
				t.Errorf("Decode = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDecodeRejectsMalformedBody(t *testing.T) {
	_, err := Decode("dev", 503, []byte("<html>gateway timeout</html>"))
	if err == nil || !strings.Contains(err.Error(), "malformed vyos api response (HTTP 503)") {
		t.Errorf("Decode = %v", err)
	}
}

func TestTextInterpretsOpModeData(t *testing.T) {
	if got := Text(nil); got != "" {
		t.Errorf("nil -> %q", got)
	}
	if got := Text(json.RawMessage(`"Version: 1.4.2"`)); got != "Version: 1.4.2" {
		t.Errorf("string -> %q", got)
	}
	// Non-string payloads are surfaced raw rather than dropped.
	if got := Text(json.RawMessage(`{"a":1}`)); got != `{"a":1}` {
		t.Errorf("object -> %q", got)
	}
}

// Regression: the copies this package replaced capped the response body at
// 16 MiB (lifecycle) vs 64 MiB (device), so a large /retrieve truncated on one
// path as "malformed vyos api response". Pin the surviving limit.
func TestMaxResponseBytesIsTheLargerOldLimit(t *testing.T) {
	if MaxResponseBytes != 64<<20 {
		t.Fatalf("MaxResponseBytes = %d, want 64 MiB", MaxResponseBytes)
	}
}

// A body larger than the old 16 MiB cap must still decode.
func TestPostAcceptsBodyOverTheOldSixteenMiBCap(t *testing.T) {
	payload := strings.Repeat("x", 20<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"success":true,"data":%q}`, payload)
	}))
	defer server.Close()

	data, err := Post(context.Background(), server.Client(), "dev", server.URL, "k", map[string]any{"op": "showConfig"})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got := Text(data); got != payload {
		t.Errorf("body truncated: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestPostSendsFormEncodedPost(t *testing.T) {
	var gotMethod, gotType, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotKey = r.PostForm.Get("key")
		fmt.Fprint(w, `{"success":true,"data":"ok"}`)
	}))
	defer server.Close()

	if _, err := Post(context.Background(), server.Client(), "dev", server.URL, "s3cret", nil); err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %s", gotMethod)
	}
	if gotType != "application/x-www-form-urlencoded" {
		t.Errorf("content-type = %s", gotType)
	}
	if gotKey != "s3cret" {
		t.Errorf("key = %q", gotKey)
	}
}

// The API key travels in the request body and must never reach an error
// message, which is what gets logged on a fleet-wide run.
func TestErrorsDoNotLeakTheAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"error":"denied"}`)
	}))
	defer server.Close()

	_, err := Post(context.Background(), server.Client(), "dev", server.URL, "s3cret", nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("error leaked the API key: %v", err)
	}
}
