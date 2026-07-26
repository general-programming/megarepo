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

// renderers is the devicetype -> Renderer registry, mirroring the
// Python VENDOR_MAP. Every vendor Python renders config for is here;
// `external` deliberately is not (see Templatable).
//
// Which ROLES each vendor supports is the renderer's own business, and
// the answer is narrow, because Python's role dispatch is just
// `templates/<role>/<devicetype>.j2` existing on disk:
//
//	vyos, linux, mikrotik   vpn only (barf.configs.BLOCK_REGISTRY)
//	edgeos                  vpn only (templates/vpn/edgeos.j2)
//	cisco, dnos6, dnos9     network_devices only
//	eos                     any role -- arista.py renders a scoped
//	                        managed slice, which render_host_config
//	                        dispatches to before role is consulted
//
// Those are all the (role, devicetype) pairs that exist: there is no
// templates/vpn/vyos.j2, no templates/core/anything. A pair outside the
// table fails in Python with a Jinja TemplateNotFound and fails here
// with noTemplateError, which is the same answer said politely.
var renderers = map[string]Renderer{
	"eos":      EOS{},
	"vyos":     VyOS{},
	"linux":    Linux{},
	"mikrotik": MikroTik{},
	"edgeos":   EdgeOS{},
	"cisco":    Cisco{},
	"dnos6":    DNOS6{},
	"dnos9":    DNOS9{},
}

// aliases are the extra VENDOR_MAP spellings, which come from NetBox
// platform slugs rather than from network.yml.
var aliases = map[string]string{
	"cisco-ios": "cisco",
	"dnos-6":    "dnos6",
	"dnos-9":    "dnos9",
}

// Templatable reports whether barf generates config for a device type at
// all (Python BaseHost.TEMPLATABLE).
//
// Only `external` is not: it marks the far side of a fabric link that
// barf does not manage (a cloud VPN endpoint, a peer's router). Such
// hosts exist in network.yml so links can name them and so their ASN
// and endpoint address are known, but nothing is ever rendered FOR
// them. Callers walking the fleet should skip them by asking this,
// rather than treating them as a vendor whose port has not landed.
func Templatable(deviceType string) bool {
	return deviceType != "external"
}

// For returns the renderer registered for a device type.
func For(deviceType string) (Renderer, bool) {
	if alias, ok := aliases[deviceType]; ok {
		deviceType = alias
	}
	renderer, ok := renderers[deviceType]
	return renderer, ok
}

// Host renders one host with the renderer registered for its device type.
func Host(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if !Templatable(h.DeviceType) {
		return "", fmt.Errorf("%s: device type %q is unmanaged; barf renders no config for it",
			h.Hostname, h.DeviceType)
	}
	renderer, ok := For(h.DeviceType)
	if !ok {
		return "", fmt.Errorf("%s: no renderer for device type %q", h.Hostname, h.DeviceType)
	}
	return renderer.Render(h, n, s)
}

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
