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

// renderers is the devicetype -> Renderer registry. Vendors appear here
// as their port lands; an absent vendor is reported, never guessed at.
var renderers = map[string]Renderer{
	"eos":      EOS{},
	"vyos":     VyOS{},
	"linux":    Linux{},
	"mikrotik": MikroTik{},
}

// For returns the renderer registered for a device type.
func For(deviceType string) (Renderer, bool) {
	renderer, ok := renderers[deviceType]
	return renderer, ok
}

// Host renders one host with the renderer registered for its device type.
func Host(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	renderer, ok := For(h.DeviceType)
	if !ok {
		return "", fmt.Errorf("%s: no renderer for device type %q", h.Hostname, h.DeviceType)
	}
	return renderer.Render(h, n, s)
}

func vaultSecret(s SecretSource, key string) (string, error) {
	source, ok := s.(VaultSource)
	if !ok {
		return "", fmt.Errorf("secret source cannot resolve vault key %q", key)
	}
	return source.VaultSecret(key)
}

func wireguardKeypair(s SecretSource, path string) (Keypair, error) {
	source, ok := s.(WireguardSource)
	if !ok {
		return Keypair{}, fmt.Errorf("secret source cannot resolve wireguard keypair %q", path)
	}
	return source.WireguardKeypair(path)
}
