package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// apiServer is a fake VyOS HTTPS API; no test here contacts a real device.
type apiServer struct {
	*httptest.Server

	mu sync.Mutex
	// requests records (endpoint, decoded payload) in order.
	requests []apiRequest
	// reply maps an endpoint to what it answers with.
	reply map[string]any
}

type apiRequest struct {
	Endpoint string
	Payload  map[string]any
}

func newAPIServer(t *testing.T) *apiServer {
	t.Helper()
	s := &apiServer{reply: map[string]any{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("bad form: %v", err)
		}
		var payload map[string]any
		_ = json.Unmarshal([]byte(r.PostFormValue("data")), &payload)

		endpoint := strings.TrimPrefix(r.URL.Path, "/")
		s.mu.Lock()
		s.requests = append(s.requests, apiRequest{Endpoint: endpoint, Payload: payload})
		reply, ok := s.reply[endpoint]
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "no such endpoint"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": reply})
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *apiServer) recorded() []apiRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]apiRequest(nil), s.requests...)
}

func (s *apiServer) client(t *testing.T, allowWrites bool) *APIClient {
	t.Helper()
	c, err := NewAPIClient("test-device", APIOptions{
		BaseURL:     s.URL,
		Key:         "SECRET-API-KEY",
		AllowWrites: allowWrites,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestShowIsAlwaysAllowed(t *testing.T) {
	server := newAPIServer(t)
	server.reply["show"] = "Version:          VyOS 2026.06.30-0048-rolling\n"

	version, err := server.client(t, false).Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if version != "2026.06.30-0048-rolling" {
		t.Fatalf("version = %q", version)
	}
}

func TestDeleteImageRefusedWithoutAllowWrites(t *testing.T) {
	server := newAPIServer(t)
	server.reply["image"] = "deleted"

	err := server.client(t, false).DeleteImage(context.Background(), "2026.01.01")
	if !errors.Is(err, ErrWritesNotAllowed) {
		t.Fatalf("expected ErrWritesNotAllowed, got %v", err)
	}
	// The refusal happens before the request is built, so the device never
	// hears about it.
	if got := server.recorded(); len(got) != 0 {
		t.Fatalf("a refused write still reached the device: %+v", got)
	}
}

func TestDeleteImageWithAllowWrites(t *testing.T) {
	server := newAPIServer(t)
	server.reply["image"] = "deleted"

	if err := server.client(t, true).DeleteImage(context.Background(), "2026.01.01"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got := server.recorded()
	if len(got) != 1 || got[0].Endpoint != "image" {
		t.Fatalf("unexpected requests: %+v", got)
	}
	if got[0].Payload["op"] != "delete" || got[0].Payload["name"] != "2026.01.01" {
		t.Fatalf("unexpected payload: %+v", got[0].Payload)
	}
}

func TestDeleteImageRefusesAnEmptyName(t *testing.T) {
	server := newAPIServer(t)
	if err := server.client(t, true).DeleteImage(context.Background(), ""); err == nil {
		t.Fatal("expected a refusal for an unnamed image")
	}
	if len(server.recorded()) != 0 {
		t.Fatal("an unnamed delete reached the device")
	}
}

func TestAPIErrorsSurface(t *testing.T) {
	server := newAPIServer(t)
	// No reply registered => success:false.
	if _, err := server.client(t, false).Show(context.Background(), "version"); err == nil {
		t.Fatal("expected an API error")
	}
}

func TestVerifyRouting(t *testing.T) {
	server := newAPIServer(t)

	// No ASN: nothing to verify.
	if warning := server.client(t, false).VerifyRouting(context.Background(), false); warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}

	server.reply["show"] = "Neighbor  V  AS  State\n10.0.0.1  4  65000  Idle (Admin)\n"
	warning := server.client(t, false).VerifyRouting(context.Background(), true)
	if !strings.Contains(warning, "administratively down") {
		t.Fatalf("expected the Idle (Admin) warning, got %q", warning)
	}

	server.reply["show"] = "Neighbor  V  AS  State\n10.0.0.1  4  65000  Established\n"
	if warning := server.client(t, false).VerifyRouting(context.Background(), true); warning != "" {
		t.Fatalf("unexpected warning: %q", warning)
	}
}

func TestAPIKeyIsNotLeakedInErrors(t *testing.T) {
	server := newAPIServer(t)
	_, err := server.client(t, false).Show(context.Background(), "version")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "SECRET-API-KEY") {
		t.Fatalf("the API key leaked into an error: %v", err)
	}
}
