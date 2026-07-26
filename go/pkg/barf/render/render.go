// Package render turns a parsed network.yml host into device config text.
// Originally a port of the Python render path (barf/util/render.py,
// barf/configs/); the snapshots in render/testdata/ pin the exact bytes it
// emits, so any change to a device's config shows up as a diff first.
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

// SecretSource resolves per-host secrets. Python BaseHost.secret().
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

// VaultSource resolves fabric-wide Vault lookups (VyOS API key, IPsec PSKs).
// Optional: a SecretSource without it fails those renders loudly.
type VaultSource interface {
	VaultSecret(key string) (string, error)
}

// TacacsSource resolves a host's TACACS+ shared key. Separate from
// SecretSource because it is a fleet-wide secret keyed by hostname, which
// does not fit HostSecret's (hostname, key) shape. IOS-family only.
type TacacsSource interface {
	TacacsKey(hostname string) (string, error)
}

// Keypair is one WireGuard keypair, base64 as stored in Vault.
type Keypair struct {
	Public  string
	Private string
}

// WireguardSource resolves fabric mesh keypairs by model.Link.KeyPath.
type WireguardSource interface {
	WireguardKeypair(path string) (Keypair, error)
}

// CompleteSecretSource is EVERY optional interface above, in one name.
// Capabilities are discovered by type assertion, so a source missing one
// compiles and fails only on a real render — this is how DNOS rendering once
// failed 100% in production. EVERY optional interface here MUST be embedded,
// and every production source MUST assert against this type, not its members:
// `var _ render.CompleteSecretSource = (*yourSource)(nil)`.
type CompleteSecretSource interface {
	SecretSource
	VaultSource
	TacacsSource
	WireguardSource
}

// SecretCapability names one optional capability, for reporting.
type SecretCapability string

// Optional capabilities in CheckSecretSource's report order. Keep in sync
// with CompleteSecretSource's members.
const (
	CapVault     SecretCapability = "VaultSource.VaultSecret"
	CapTacacs    SecretCapability = "TacacsSource.TacacsKey"
	CapWireguard SecretCapability = "WireguardSource.WireguardKeypair"
)

// MissingCapabilities returns the optional interfaces s does not implement,
// in declaration order; empty means s is a CompleteSecretSource. Runtime half
// of the assertion above, so a fleet run fails before its first host.
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

// CheckSecretSource reports whether s can serve every render in the fleet.
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

// The devicetype -> Renderer registry lives in ../vendor; this package
// deliberately has no dispatch.

// noTemplateError is the Go spelling of Jinja's TemplateNotFound.
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
