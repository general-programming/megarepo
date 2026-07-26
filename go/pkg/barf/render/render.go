// Package render turns a parsed network.yml host into device config text.
//
// It is a port of the Python barf render path (barf/util/render.py plus
// barf/configs/): one block per feature, one method per vendor, emitting
// the exact strings the Python implementation emits. The goldens in
// projects/barf/tests/golden/ are the byte-for-byte parity contract.
//
// Nothing here talks to a device or to Vault: secrets arrive through the
// SecretSource interfaces below.
package render

import (
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Renderer produces the device config text for one host.
type Renderer interface {
	Render(h *model.Host, n *model.Network, s SecretSource) (string, error)
}

// SecretSource resolves per-host secrets (Vault in production, a
// deterministic fake in tests). Mirrors Python BaseHost.secret().
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

// VaultSource resolves the fabric-wide Vault attribute lookups the
// Python templates reach through util.secrets.VaultSecrets (the VyOS API
// key, IPsec pre-shared secrets). Optional: a SecretSource that does not
// implement it makes those renders fail loudly rather than silently.
type VaultSource interface {
	VaultSecret(key string) (string, error)
}

// TacacsSource resolves a host's TACACS+ shared key. Python reads it
// from a fleet-wide secret (cluster-secrets/tacacs-keys, field
// <hostname>, subkey "key") rather than the per-host path, so it does
// not fit HostSecret's (hostname, key) shape. Optional, like
// VaultSource: only the IOS-family vendors need it.
type TacacsSource interface {
	TacacsKey(hostname string) (string, error)
}

// Keypair is one WireGuard keypair, base64 as stored in Vault.
type Keypair struct {
	Public  string
	Private string
}

// WireguardSource resolves the fabric mesh keypairs by Vault path
// (model.Link.KeyPath). Optional, like VaultSource.
type WireguardSource interface {
	WireguardKeypair(path string) (Keypair, error)
}

// CompleteSecretSource is EVERY optional interface above, in one name.
//
// It exists because of a production outage. The optional capabilities
// are discovered by type assertion (see vaultSecret and friends below),
// so a secret source missing one compiles, links, passes every test that
// uses a different fake, and then fails on a real render. That is not a
// hypothetical: DNOS rendering failed 100% in production because the
// Vault-backed adapter implemented three of these four and not
// TacacsSource, while the test fake implemented all four.
//
// The rule, which is the whole point of this type:
//
//	EVERY optional SecretSource interface in this file MUST be embedded
//	here, and every production secret source MUST be asserted against
//	this type (not against the members individually).
//
// A `var _ render.CompleteSecretSource = (*yourSource)(nil)` then turns
// that entire bug class into a build error. Adding a fifth optional
// interface and embedding it here breaks the build of every adapter that
// has not caught up -- which is exactly what should happen.
type CompleteSecretSource interface {
	SecretSource
	VaultSource
	TacacsSource
	WireguardSource
}

// SecretCapability names one optional capability, for reporting.
type SecretCapability string

// The optional capabilities, in the order CheckSecretSource reports
// them. Same list as CompleteSecretSource's members; keep them together.
const (
	CapVault     SecretCapability = "VaultSource.VaultSecret"
	CapTacacs    SecretCapability = "TacacsSource.TacacsKey"
	CapWireguard SecretCapability = "WireguardSource.WireguardKeypair"
)

// MissingCapabilities returns the optional interfaces s does not
// implement, in declaration order. Empty means s is a
// CompleteSecretSource.
//
// This is the runtime half of the compile-time assertion above, for
// sources that cannot be checked at build time (a source chosen by flag,
// a fake in someone else's test). It answers WHICH capability is missing
// before any host is rendered, instead of one vendor's render failing
// deep in a fleet run.
func MissingCapabilities(s SecretSource) []SecretCapability {
	var missing []SecretCapability
	if _, ok := s.(VaultSource); !ok {
		missing = append(missing, CapVault)
	}
	if _, ok := s.(TacacsSource); !ok {
		missing = append(missing, CapTacacs)
	}
	if _, ok := s.(WireguardSource); !ok {
		missing = append(missing, CapWireguard)
	}
	return missing
}

// CheckSecretSource reports whether s can serve every render in the
// fleet, naming what it cannot do.
func CheckSecretSource(s SecretSource) error {
	if s == nil {
		return fmt.Errorf("render: nil secret source")
	}
	missing := MissingCapabilities(s)
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, len(missing))
	for i, c := range missing {
		names[i] = string(c)
	}
	return fmt.Errorf("render: secret source %T is incomplete: missing %s", s, strings.Join(names, ", "))
}

// The devicetype -> Renderer registry used to live here, next to the
// three other registries keyed by the same string in three other
// packages. It now lives in one table in ../vendor, with the transport
// and comparison entries for the same devicetype next to it. This
// package deliberately has no dispatch left: it renders, and something
// else decides who renders.

// noTemplateError is the Go spelling of Jinja's TemplateNotFound for a
// (role, devicetype) pair barf has no config for.
func noTemplateError(h *model.Host) error {
	return fmt.Errorf("%s: no config for role %q on device type %q",
		h.Hostname, h.Role, h.DeviceType)
}

func vaultSecret(s SecretSource, key string) (string, error) {
	source, ok := s.(VaultSource)
	if !ok {
		return "", fmt.Errorf("secret source cannot resolve vault key %q", key)
	}
	return source.VaultSecret(key)
}

func tacacsKey(s SecretSource, hostname string) (string, error) {
	source, ok := s.(TacacsSource)
	if !ok {
		return "", fmt.Errorf("secret source cannot resolve the tacacs key for %q", hostname)
	}
	return source.TacacsKey(hostname)
}

func wireguardKeypair(s SecretSource, path string) (Keypair, error) {
	source, ok := s.(WireguardSource)
	if !ok {
		return Keypair{}, fmt.Errorf("secret source cannot resolve wireguard keypair %q", path)
	}
	return source.WireguardKeypair(path)
}
