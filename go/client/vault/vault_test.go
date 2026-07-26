package vault

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeVault serves KV v2 reads out of an in-memory map keyed by
// "<mount>/<path>", counting requests so cache behaviour is observable.
type fakeVault struct {
	data  map[string]map[string]any
	calls atomic.Int64
	token atomic.Value // string
}

func (f *fakeVault) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.token.Store(r.Header.Get("X-Vault-Token"))

		// /v1/<mount>/data/<path...>
		trimmed := strings.TrimPrefix(r.URL.Path, "/v1/")
		mount, rest, ok := strings.Cut(trimmed, "/data/")
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":["unsupported path"]}`))
			return
		}

		secret, found := f.data[mount+"/"+rest]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"data":     secret,
				"metadata": map[string]any{"version": 1},
			},
		})
	}
}

func newTestClient(t *testing.T, fv *fakeVault) *Client {
	t.Helper()
	srv := httptest.NewServer(fv.handler(t))
	t.Cleanup(srv.Close)

	c, err := New(Options{Address: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func fixture() *fakeVault {
	return &fakeVault{data: map[string]map[string]any{
		"secret/infra/netbox":              {"api_key": "nbt_abc.def"},
		"secret/infra/dhcp-webhook":        {"token": "hook-token"},
		"cluster-secrets/host-sea1-core-1": {"admin-password": "pw", "enable-password": "en", "ttl": 30},
	}}
}

func TestGetDefaultMount(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)

	// Empty mount falls back to "secret".
	got, err := c.Get(context.Background(), "", "infra/netbox", "api_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "nbt_abc.def" {
		t.Errorf("value mismatch")
	}
	if tok, _ := fv.token.Load().(string); tok != "test-token" {
		t.Errorf("token header = %q", tok)
	}
}

func TestGetExplicitMount(t *testing.T) {
	c := newTestClient(t, fixture())
	got, err := c.Get(context.Background(), MountClusterSecrets, "host-sea1-core-1", "enable-password")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "en" {
		t.Errorf("got %q", got)
	}
}

func TestGetLeadingSlashesTolerated(t *testing.T) {
	c := newTestClient(t, fixture())
	if _, err := c.Get(context.Background(), MountSecret, "/infra/netbox/", "api_key"); err != nil {
		t.Fatalf("Get with slashes: %v", err)
	}
}

func TestGetEmptyPath(t *testing.T) {
	c := newTestClient(t, fixture())
	if _, err := c.ReadSecret(context.Background(), MountSecret, "/"); err == nil {
		t.Fatal("expected an error for an empty path")
	}
}

func TestReadSecretReturnsWholeMap(t *testing.T) {
	c := newTestClient(t, fixture())
	data, err := c.ReadSecret(context.Background(), MountClusterSecrets, "host-sea1-core-1")
	if err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}
	if len(data) != 3 {
		t.Errorf("got %d keys, want 3", len(data))
	}
}

func TestHostSecret(t *testing.T) {
	c := newTestClient(t, fixture())

	got, err := c.HostSecret("sea1-core-1", "admin-password")
	if err != nil {
		t.Fatalf("HostSecret: %v", err)
	}
	if got != "pw" {
		t.Errorf("got %q", got)
	}
}

func TestHostSecretMissingPath(t *testing.T) {
	c := newTestClient(t, fixture())

	_, err := c.HostSecret("does-not-exist", "admin-password")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("err = %v, want ErrPathNotFound", err)
	}

	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err is %T, want *NotFoundError", err)
	}
	if nf.Mount != MountClusterSecrets || nf.Path != "host-does-not-exist" {
		t.Errorf("NotFoundError = %#v", nf)
	}
	if nf.Key != "" {
		t.Errorf("Key should be empty for a missing path, got %q", nf.Key)
	}
}

func TestHostSecretMissingKey(t *testing.T) {
	c := newTestClient(t, fixture())

	_, err := c.HostSecret("sea1-core-1", "tacacs-key")
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("err = %v, want ErrKeyNotFound", err)
	}
	var nf *NotFoundError
	if !errors.As(err, &nf) || nf.Key != "tacacs-key" {
		t.Fatalf("NotFoundError = %#v", nf)
	}
	if !strings.Contains(err.Error(), "host-sea1-core-1") {
		t.Errorf("error should name the path: %q", err.Error())
	}
}

func TestGetNonStringValue(t *testing.T) {
	c := newTestClient(t, fixture())
	_, err := c.Get(context.Background(), MountClusterSecrets, "host-sea1-core-1", "ttl")
	if err == nil {
		t.Fatal("expected an error for a non-string value")
	}
	if errors.Is(err, ErrKeyNotFound) {
		t.Error("a present-but-non-string key is not a missing key")
	}
}

func TestReadIsCached(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := c.Get(ctx, MountSecret, "infra/netbox", "api_key"); err != nil {
			t.Fatalf("Get: %v", err)
		}
	}
	// A second key on the same path must also come from the cache.
	if _, err := c.Get(ctx, MountClusterSecrets, "host-sea1-core-1", "admin-password"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := c.Get(ctx, MountClusterSecrets, "host-sea1-core-1", "enable-password"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := fv.calls.Load(); got != 2 {
		t.Errorf("made %d requests, want 2 (one per distinct path)", got)
	}

	c.InvalidateCache()
	if _, err := c.Get(ctx, MountSecret, "infra/netbox", "api_key"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := fv.calls.Load(); got != 3 {
		t.Errorf("after invalidation made %d requests, want 3", got)
	}
}

func TestConcurrentReads(t *testing.T) {
	fv := fixture()
	c := newTestClient(t, fv)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = c.Get(context.Background(), MountSecret, "infra/netbox", "api_key")
			} else {
				_, err = c.HostSecret("sea1-core-1", "admin-password")
			}
			if err != nil {
				t.Errorf("concurrent read: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

func TestHostSecretPath(t *testing.T) {
	if got := HostSecretPath("sea1-core-1"); got != "host-sea1-core-1" {
		t.Errorf("HostSecretPath = %q", got)
	}
}

func TestClientSatisfiesSecretSource(t *testing.T) {
	// The barf renderer depends on this exact method set.
	var src interface {
		HostSecret(hostname, key string) (string, error)
	} = newTestClient(t, fixture())
	if _, err := src.HostSecret("sea1-core-1", "admin-password"); err != nil {
		t.Fatalf("HostSecret via interface: %v", err)
	}
}

func TestCustomMounts(t *testing.T) {
	fv := &fakeVault{data: map[string]map[string]any{
		"kv/host-a": {"k": "v"},
	}}
	srv := httptest.NewServer(fv.handler(t))
	t.Cleanup(srv.Close)

	c, err := New(Options{Address: srv.URL, Token: "t", DefaultMount: "kv", HostMount: "kv"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.HostSecret("a", "k")
	if err != nil {
		t.Fatalf("HostSecret: %v", err)
	}
	if got != "v" {
		t.Errorf("got %q", got)
	}
}

func TestTokenFromHelperFileFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_TOKEN", "")
	t.Setenv("VAULT_ADDR", "https://vault.example.invalid")

	if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("file-token\n"), 0o600); err != nil {
		t.Fatalf("writing token file: %v", err)
	}

	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.API().Token() != "file-token" {
		t.Errorf("token = %q, want the trimmed ~/.vault-token contents", c.API().Token())
	}
}

func TestExplicitTokenBeatsHelperFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VAULT_TOKEN", "")
	if err := os.WriteFile(filepath.Join(home, ".vault-token"), []byte("file-token"), 0o600); err != nil {
		t.Fatalf("writing token file: %v", err)
	}

	c, err := New(Options{Address: "https://vault.example.invalid", Token: "explicit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.API().Token() != "explicit" {
		t.Errorf("token = %q", c.API().Token())
	}
}

func TestNewHonoursEnvAddress(t *testing.T) {
	t.Setenv("VAULT_ADDR", "https://vault.example.invalid")
	t.Setenv("VAULT_TOKEN", "env-token")

	c, err := New(Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.API().Address() != "https://vault.example.invalid" {
		t.Errorf("address = %q", c.API().Address())
	}
	if c.API().Token() != "env-token" {
		t.Error("token not taken from VAULT_TOKEN")
	}
	if c.defaultMount != MountSecret || c.hostMount != MountClusterSecrets {
		t.Errorf("mount defaults = %q / %q", c.defaultMount, c.hostMount)
	}
}
