package vault

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// writableVault is an in-memory KV v2 over httptest: reads plus the two
// writes minting uses (JSON merge PATCH, POST create/update). It records every
// write so clobbering is observable.
type writableVault struct {
	mu     sync.Mutex
	data   map[string]map[string]any // "<mount>/<path>" -> secret
	writes []string                  // "<method> <mount>/<path>"

	// patchStatus, when non-zero, is returned for PATCH instead of
	// performing it (403 = the token lacks the patch capability).
	patchStatus int32
}

func newWritableVault(seed map[string]map[string]any) *writableVault {
	if seed == nil {
		seed = map[string]map[string]any{}
	}
	return &writableVault{data: seed}
}

func (w *writableVault) handler() http.HandlerFunc {
	return func(rw http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/v1/")
		mount, rest, ok := strings.Cut(trimmed, "/data/")
		if !ok {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"errors":["unsupported path"]}`))
			return
		}
		key := mount + "/" + rest

		w.mu.Lock()
		defer w.mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			secret, found := w.data[key]
			if !found {
				rw.WriteHeader(http.StatusNotFound)
				_, _ = rw.Write([]byte(`{"errors":[]}`))
				return
			}
			w.writeSecret(rw, secret)

		case http.MethodPatch:
			if status := int(atomic.LoadInt32(&w.patchStatus)); status != 0 {
				rw.WriteHeader(status)
				_, _ = rw.Write([]byte(`{"errors":["permission denied"]}`))
				return
			}
			existing, found := w.data[key]
			if !found {
				rw.WriteHeader(http.StatusNotFound)
				_, _ = rw.Write([]byte(`{"errors":[]}`))
				return
			}
			incoming := w.body(r)
			// Merge, never replace: siblings must survive.
			for k, v := range incoming {
				existing[k] = v
			}
			w.writes = append(w.writes, "PATCH "+key)
			w.writeSecret(rw, existing)

		case http.MethodPost, http.MethodPut:
			incoming := w.body(r)
			w.data[key] = incoming
			w.writes = append(w.writes, "POST "+key)
			w.writeSecret(rw, incoming)

		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func (w *writableVault) body(r *http.Request) map[string]any {
	raw, _ := io.ReadAll(r.Body)
	var wrapper struct {
		Data map[string]any `json:"data"`
	}
	_ = json.Unmarshal(raw, &wrapper)
	if wrapper.Data == nil {
		wrapper.Data = map[string]any{}
	}
	return wrapper.Data
}

func (w *writableVault) writeSecret(rw http.ResponseWriter, secret map[string]any) {
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{
		"data": map[string]any{
			"data":     secret,
			"metadata": map[string]any{"version": 1, "created_time": "2026-07-26T00:00:00Z"},
		},
	})
}

func (w *writableVault) secret(key string) map[string]any {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[string]any{}
	for k, v := range w.data[key] {
		out[k] = v
	}
	return out
}

func (w *writableVault) writeLog() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.writes...)
}

func newMintClient(t *testing.T, wv *writableVault, allowMint bool) *Client {
	t.Helper()
	srv := httptest.NewServer(wv.handler())
	t.Cleanup(srv.Close)

	c, err := New(Options{Address: srv.URL, Token: "test-token", AllowMint: allowMint})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func constGen(value string) func() (string, error) {
	return func() (string, error) { return value, nil }
}

func TestGenerateTokenMatchesPythonTokenUrlsafe16(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		got, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		// secrets.token_urlsafe(16) is 16 bytes of unpadded URL-safe
		// base64, which is always 22 characters.
		if len(got) != 22 {
			t.Fatalf("GenerateToken() = %d chars, want 22", len(got))
		}
		if strings.ContainsAny(got, "=+/") {
			t.Fatalf("token %q is not unpadded URL-safe base64", got)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(got)
		if err != nil {
			t.Fatalf("token %q does not decode: %v", got, err)
		}
		if len(decoded) != tokenBytes {
			t.Fatalf("token decoded to %d bytes, want %d", len(decoded), tokenBytes)
		}
		if seen[got] {
			t.Fatalf("GenerateToken repeated a value after %d draws", i)
		}
		seen[got] = true
	}
}

func TestMintDisabledByDefault(t *testing.T) {
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/host-sea1-core-1": {"admin-password": "existing"},
	})
	c := newMintClient(t, wv, false)

	if c.CanMint() {
		t.Fatal("a client built without AllowMint must not be able to mint")
	}

	_, err := c.MintHostSecret(context.Background(), "sea1-core-1", "enable-password", constGen("nope"))
	if !errors.Is(err, ErrMintNotAllowed) {
		t.Fatalf("err = %v, want ErrMintNotAllowed", err)
	}
	if writes := wv.writeLog(); len(writes) != 0 {
		t.Fatalf("a read-only client wrote to Vault: %v", writes)
	}
}

func TestMintReturnsExistingWithoutWriting(t *testing.T) {
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/host-sea1-core-1": {"admin-password": "existing"},
	})
	c := newMintClient(t, wv, true)

	got, err := c.MintHostSecret(context.Background(), "sea1-core-1", "admin-password", constGen("fresh"))
	if err != nil {
		t.Fatalf("MintHostSecret: %v", err)
	}
	if got != "existing" {
		t.Fatalf("got %q, want the stored value", got)
	}
	if writes := wv.writeLog(); len(writes) != 0 {
		t.Fatalf("an existing key must not be rewritten: %v", writes)
	}
}

func TestMintPatchesWithoutClobberingSiblings(t *testing.T) {
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/host-sea1-core-1": {"admin-password": "existing", "ttl": float64(30)},
	})
	c := newMintClient(t, wv, true)

	got, err := c.MintHostSecret(context.Background(), "sea1-core-1", "enable-password", constGen("minted"))
	if err != nil {
		t.Fatalf("MintHostSecret: %v", err)
	}
	if got != "minted" {
		t.Fatalf("got %q, want the generated value", got)
	}

	stored := wv.secret("cluster-secrets/host-sea1-core-1")
	if stored["enable-password"] != "minted" {
		t.Fatalf("the minted key was not stored: %v", stored)
	}
	if stored["admin-password"] != "existing" || stored["ttl"] != float64(30) {
		t.Fatalf("sibling keys were clobbered: %v", stored)
	}
	if writes := wv.writeLog(); len(writes) != 1 || !strings.HasPrefix(writes[0], "PATCH ") {
		t.Fatalf("writes = %v, want a single PATCH", writes)
	}

	// The mint is folded into the read cache, so a second call is free.
	again, err := c.MintHostSecret(context.Background(), "sea1-core-1", "enable-password", constGen("different"))
	if err != nil {
		t.Fatalf("second MintHostSecret: %v", err)
	}
	if again != "minted" {
		t.Fatalf("second call = %q, want the cached mint", again)
	}
	if len(wv.writeLog()) != 1 {
		t.Fatalf("the second call wrote again: %v", wv.writeLog())
	}
}

func TestMintCreatesMissingPath(t *testing.T) {
	wv := newWritableVault(nil)
	c := newMintClient(t, wv, true)

	got, err := c.MintHostSecret(context.Background(), "fmt2-vpn-leaf-9", "admin-password", constGen("brand-new"))
	if err != nil {
		t.Fatalf("MintHostSecret: %v", err)
	}
	if got != "brand-new" {
		t.Fatalf("got %q", got)
	}
	stored := wv.secret("cluster-secrets/host-fmt2-vpn-leaf-9")
	if stored["admin-password"] != "brand-new" || len(stored) != 1 {
		t.Fatalf("created secret = %v", stored)
	}
	if writes := wv.writeLog(); len(writes) != 1 || !strings.HasPrefix(writes[0], "POST ") {
		t.Fatalf("writes = %v, want a single create", writes)
	}
}

func TestMintFallsBackToReadThenWriteWithoutPatchCapability(t *testing.T) {
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/host-sea1-core-1": {"admin-password": "existing"},
	})
	atomic.StoreInt32(&wv.patchStatus, http.StatusForbidden)
	c := newMintClient(t, wv, true)

	got, err := c.MintHostSecret(context.Background(), "sea1-core-1", "enable-password", constGen("minted"))
	if err != nil {
		t.Fatalf("MintHostSecret: %v", err)
	}
	if got != "minted" {
		t.Fatalf("got %q", got)
	}
	stored := wv.secret("cluster-secrets/host-sea1-core-1")
	if stored["admin-password"] != "existing" || stored["enable-password"] != "minted" {
		t.Fatalf("rw fallback lost data: %v", stored)
	}
}

func TestMintWithoutGeneratorIsANotFound(t *testing.T) {
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/host-sea1-core-1": {"admin-password": "existing"},
	})
	c := newMintClient(t, wv, true)

	_, err := c.MintHostSecret(context.Background(), "sea1-core-1", "enable-password", nil)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
	if writes := wv.writeLog(); len(writes) != 0 {
		t.Fatalf("nil generator must not write: %v", writes)
	}
}

func TestMintRejectsEmptyGeneratedValue(t *testing.T) {
	wv := newWritableVault(nil)
	c := newMintClient(t, wv, true)

	if _, err := c.MintHostSecret(context.Background(), "h", "k", constGen("")); err == nil {
		t.Fatal("expected an error for an empty generated value")
	}
	if writes := wv.writeLog(); len(writes) != 0 {
		t.Fatalf("an empty value must not be written: %v", writes)
	}
}

func TestMintGeneratorErrorPropagates(t *testing.T) {
	wv := newWritableVault(nil)
	c := newMintClient(t, wv, true)

	boom := errors.New("no entropy")
	_, err := c.MintHostSecret(context.Background(), "h", "k", func() (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the generator error", err)
	}
}

func TestMintNeverPutsTheValueInAnError(t *testing.T) {
	wv := newWritableVault(nil)
	atomic.StoreInt32(&wv.patchStatus, http.StatusForbidden)
	// A backend that rejects every write, so the error path is taken.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			rw.WriteHeader(http.StatusNotFound)
			_, _ = rw.Write([]byte(`{"errors":[]}`))
			return
		}
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte(`{"errors":["backend down"]}`))
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{Address: srv.URL, Token: "test-token", AllowMint: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const secretValue = "sup3r-s3cret-value"
	_, err = c.MintHostSecret(context.Background(), "sea1-core-1", "admin-password", constGen(secretValue))
	if err == nil {
		t.Fatal("expected the write failure to surface")
	}
	if strings.Contains(err.Error(), secretValue) {
		t.Fatalf("the secret value leaked into an error: %v", err)
	}
}

func TestMintSecretValidatesInputs(t *testing.T) {
	wv := newWritableVault(nil)
	c := newMintClient(t, wv, true)

	if _, err := c.MintSecret(context.Background(), "", "", "k", constGen("v")); err == nil {
		t.Fatal("expected an error for an empty path")
	}
	if _, err := c.MintSecret(context.Background(), "", "p", "", constGen("v")); err == nil {
		t.Fatal("expected an error for an empty key")
	}
}

func TestMintSecretHonoursExplicitMountAndPath(t *testing.T) {
	// Python reaches the shared "tacacs-keys" secret this way.
	wv := newWritableVault(map[string]map[string]any{
		"cluster-secrets/tacacs-keys": {"other-host": "k"},
	})
	c := newMintClient(t, wv, true)

	if _, err := c.MintSecret(context.Background(), "", "tacacs-keys", "sea1-core-1", constGen("tk")); err != nil {
		t.Fatalf("MintSecret: %v", err)
	}
	stored := wv.secret("cluster-secrets/tacacs-keys")
	if stored["sea1-core-1"] != "tk" || stored["other-host"] != "k" {
		t.Fatalf("stored = %v", stored)
	}
}

func TestClientSatisfiesMinter(t *testing.T) {
	var _ Minter = (*Client)(nil)
}
