package cli

import (
	"errors"
	"strings"
	"testing"
)

func withFakeMint(t *testing.T, f func(hostname, key string) (string, error)) {
	t.Helper()
	old := mintHostSecret
	mintHostSecret = f
	t.Cleanup(func() { mintHostSecret = old })
}

// Regression: barf's Vault wiring was read-only everywhere (deliberately),
// but had no opt-in write path at all, so a brand new host or WireGuard
// link failed every command until its secret was minted by hand. `barf
// secrets mint` is that opt-in path; this pins its plumbing end to end
// (independent of whether a real Vault is reachable, via the mintHostSecret
// seam wire.go points at vault.Client.MintHostSecret).
func TestSecretsMintPrintsTheValue(t *testing.T) {
	h := newHarness(t)
	var gotHost, gotKey string
	withFakeMint(t, func(hostname, key string) (string, error) {
		gotHost, gotKey = hostname, key
		return "freshly-minted-value", nil
	})

	if err := h.run(t, "secrets", "mint", "sea1-vpn-9", "admin-password"); err != nil {
		t.Fatal(err)
	}
	if gotHost != "sea1-vpn-9" || gotKey != "admin-password" {
		t.Errorf("mintHostSecret called with (%q, %q)", gotHost, gotKey)
	}
	if strings.TrimSpace(h.out.String()) != "freshly-minted-value" {
		t.Errorf("stdout = %q, want the minted value", h.out.String())
	}
}

// A failure (e.g. AllowMint refused, or Vault unreachable) must surface as
// the command's error, not be swallowed.
func TestSecretsMintPropagatesErrors(t *testing.T) {
	h := newHarness(t)
	withFakeMint(t, func(string, string) (string, error) {
		return "", errors.New("vault: minting is disabled")
	})

	err := h.run(t, "secrets", "mint", "sea1-vpn-9", "admin-password")
	if err == nil || !strings.Contains(err.Error(), "minting is disabled") {
		t.Fatalf("err = %v", err)
	}
}

// The command requires exactly a host and a key.
func TestSecretsMintRequiresHostAndKey(t *testing.T) {
	h := newHarness(t)
	withFakeMint(t, func(string, string) (string, error) { return "unused", nil })

	if err := h.run(t, "secrets", "mint", "sea1-vpn-9"); err == nil {
		t.Fatal("want an error with only one argument")
	}
}
