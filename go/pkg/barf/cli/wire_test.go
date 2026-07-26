package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
)

// TestVaultSourceImplementsEverySecretInterface is the regression for
// render.TacacsSource having no production implementation at all: the
// golden tests only ever exercised the test fake, so `barf generate` on
// any Dell (dnos6 / dnos9) device failed 100% of the time with "secret
// source cannot resolve the tacacs key".
//
// The compile-time assertions in wire.go are the real fix — this test
// documents them and fails loudly if one is deleted.
func TestVaultSourceImplementsEverySecretInterface(t *testing.T) {
	var s render.SecretSource = (*vaultSource)(nil)

	if _, ok := s.(render.VaultSource); !ok {
		t.Error("vaultSource does not implement render.VaultSource")
	}
	if _, ok := s.(render.TacacsSource); !ok {
		t.Error("vaultSource does not implement render.TacacsSource")
	}
	if _, ok := s.(render.WireguardSource); !ok {
		t.Error("vaultSource does not implement render.WireguardSource")
	}
}

// fakeVault serves KV v2 reads out of a path -> data map.
func fakeVault(t *testing.T, secrets map[string]map[string]any) *vaultSource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /v1/<mount>/data/<path>
		_, path, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/v1/"), "/data/")
		data, found := secrets[path]
		if !ok || !found {
			http.Error(w, "no such path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"data": data, "metadata": map[string]any{"version": 1}},
		})
	}))
	t.Cleanup(srv.Close)

	c, err := vault.New(vault.Options{Address: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	return &vaultSource{c: c}
}

// TestVaultSourceTacacsKey pins the Python shape: cluster-secrets mount,
// path `tacacs-keys`, FIELD <hostname>, subkey "key" of the object
// stored there (BaseHost.tacacs_key in projects/barf).
func TestVaultSourceTacacsKey(t *testing.T) {
	v := fakeVault(t, map[string]map[string]any{"tacacs-keys": {
		"sea1-sw-0": map[string]any{"address": "10.0.0.1/32", "key": "TACACSKEY0"},
		// The same object written as a JSON string, which is how the
		// Vault CLI's `k=@file` spelling round-trips.
		"sea1-sw-1": `{"address": "10.0.0.2/32", "key": "TACACSKEY1"}`,
		"broken":    map[string]any{"address": "10.0.0.3/32"},
	}})

	for _, tc := range []struct{ host, want string }{
		{"sea1-sw-0", "TACACSKEY0"},
		{"sea1-sw-1", "TACACSKEY1"},
	} {
		got, err := v.TacacsKey(tc.host)
		if err != nil {
			t.Fatalf("TacacsKey(%q): %v", tc.host, err)
		}
		if got != tc.want {
			t.Errorf("TacacsKey(%q) = %q, want %q", tc.host, got, tc.want)
		}
	}

	for _, host := range []string{"missing-host", "broken"} {
		if _, err := v.TacacsKey(host); err == nil {
			t.Errorf("TacacsKey(%q) succeeded; want an error", host)
		}
	}
}

// TestVaultSourceRendersDNOS is the end-to-end version: the real Dell
// renderer (render/dell.go calls tacacsKey unconditionally) against the
// real vaultSource. This whole call failed in production, and only the
// test fake's TacacsKey kept the golden tests green.
func TestVaultSourceRendersDNOS(t *testing.T) {
	v := fakeVault(t, map[string]map[string]any{
		"tacacs-keys":    {"sea1-sw-0": map[string]any{"key": "TACACSKEY0"}},
		"host-sea1-sw-0": {"admin-password": "hunter2"},
	})

	text, err := render.RenderDNOS(&render.IOSDevice{
		Hostname:      "sea1-sw-0",
		TacacsServers: []string{"10.0.0.9"},
	}, model.GlobalMeta{}, v)
	if err != nil {
		t.Fatalf("RenderDNOS with the production secret source: %v", err)
	}
	if !strings.Contains(text, "tacacs-server key TACACSKEY0") {
		t.Errorf("tacacs key missing from the render:\n%s", text)
	}
}

// TestGenerateWritesCredentialsOwnerOnly is the regression for `barf
// generate` leaving world-readable plaintext credentials in output/.
// The rendered config carries the admin password, the fleet VyOS API key
// and WireGuard private keys, and `output/` is only gitignored under
// projects/barf — so a run from the repo root left a credential tree
// `git add -A` would happily stage.
func TestGenerateWritesCredentialsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	host := &model.Host{Hostname: "sea1-vpn-0", Role: "vpn"}

	path, err := writeRenderedConfig(host, "set system login user supertech "+
		"authentication plaintext-password 'hunter2'\n", filepath.Join(dir, "output"))
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		path,
		filepath.Join(dir, "output", "vpn", "cloud_init", "sea1-vpn-0"),
	} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", p, got)
		}
	}

	for _, p := range []string{
		filepath.Join(dir, "output"),
		filepath.Join(dir, "output", "vpn"),
		filepath.Join(dir, "output", "vpn", "cloud_init"),
	} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("%s mode = %04o, want 0700", p, got)
		}
	}

	// Rewriting an existing, wrongly-permissioned file must tighten it.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRenderedConfig(host, "set system host-name x\n",
		filepath.Join(dir, "output")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("re-render left %s at %04o, want 0600", path, got)
	}
}
