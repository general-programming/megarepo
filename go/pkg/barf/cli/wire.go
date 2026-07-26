package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// This is the only file in the package that names the render, device,
// model-loading and vault implementations. Everything else talks to the
// local interfaces in deps.go, so a change to a constructor over there is
// a change here and nowhere else. Deleting this file leaves the package
// compiling (the seams fall back to "not wired" errors) and its tests
// passing against their fakes.

func init() {
	loadNetwork = model.Load
	newSecrets = wireSecrets
	newRenderer = wireRenderer
	newReader = wireReader
	reportsStatus = wireReportsStatus
	isTemplatable = wireTemplatable
}

// wireRenderer resolves the renderer registered for a device type.
func wireRenderer(deviceType string) (Renderer, error) {
	r, ok := vendor.Renderer(deviceType)
	if !ok {
		return nil, fmt.Errorf("no renderer for device type %q", deviceType)
	}
	return rendererAdapter{r}, nil
}

type rendererAdapter struct{ r render.Renderer }

// Render forwards to the real renderer. cli.SecretSource has the same
// method set as render.SecretSource, so the value passes straight
// through, extra optional interfaces (VaultSource, WireguardSource)
// included.
func (a rendererAdapter) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	return a.r.Render(h, n, s)
}

// wireTemplatable and wireReportsStatus are one lookup each against the
// vendor table now. wireReportsStatus in particular used to be a
// hand-written `case "vyos", "eos"` — a second, independently editable
// copy of device.New's vendor switch, with nothing checking that the two
// agreed. Adding a third readable vendor to one and not the other gave a
// device barf refused to probe (or probed and then could not construct a
// client for). `v.NewReader != nil` cannot drift from the constructor it
// is asking about, because it IS the constructor.

// wireTemplatable reports whether a device type has a renderer at all.
func wireTemplatable(deviceType string) bool {
	return vendor.Templatable(deviceType)
}

// wireReportsStatus mirrors the Python REPORTS_STATUS flag: the vendors
// the table has a reader constructor for.
func wireReportsStatus(deviceType string) bool {
	return vendor.ReportsStatus(deviceType)
}

// wireReader builds a read-only device client pinned to the endpoint the
// CLI already probed.
func wireReader(h *model.Host, address string, s SecretSource) (DeviceReader, error) {
	opts := device.Options{
		Endpoint: address,
		// Fleet devices serve the self-signed default SSL profile; the
		// Python implementation uses an unverified context here too.
		InsecureSkipVerify: true,
	}
	if src, ok := s.(*vaultSource); ok {
		opts.Secrets = src
		opts.GlobalSecrets = src
	}
	r, err := vendor.NewReader(h, opts)
	if err != nil {
		return nil, err
	}
	// Vendors that manage only a slice of the device config (EOS) get a
	// reader that can also answer a scoped comparison; see scopeddiff.go.
	return wrapScoped(h, readerAdapter{r}, r), nil
}

type readerAdapter struct{ r device.Reader }

func (a readerAdapter) Status(ctx context.Context) (DeviceStatus, error) {
	st, err := a.r.Status(ctx)
	return DeviceStatus{Version: st.Version, Uptime: st.Uptime, Model: st.Model}, err
}

func (a readerAdapter) RunningConfig(ctx context.Context) (string, error) {
	return a.r.RunningConfig(ctx)
}

// wireSecrets builds the Vault-backed secret source.
func wireSecrets() (SecretSource, error) {
	c, err := vault.New(vault.Options{})
	if err != nil {
		return nil, err
	}
	src := &vaultSource{c: c}
	// The runtime half of the compile-time assertion below. It cannot
	// fail for *vaultSource — that is what the assertion guarantees — but
	// it is the check any OTHER secret source has to pass to get in here,
	// and it fails at startup naming the missing capability instead of
	// midway through a fleet render on one unlucky vendor.
	if err := render.CheckSecretSource(src); err != nil {
		return nil, err
	}
	return src, nil
}

// vaultSource satisfies every secret interface the render and device
// packages optionally ask for. It only ever reads; secrets stay in
// memory and are never logged or written to disk.
type vaultSource struct{ c *vault.Client }

// The production secret source must implement EVERY optional interface
// the render package asks for, and this line is what enforces it.
//
// render resolves those interfaces with runtime type assertions, so a
// missing method is otherwise only discovered when a real render of that
// vendor fails in production — DNOS rendering did exactly that while
// TacacsKey was unimplemented, and the goldens stayed green because the
// test fake had it.
//
// This is asserted against the COMPOSITE, not against the four members
// individually. Four separate assertions still had the original defect
// in miniature: adding a fifth optional interface to render meant
// remembering to add a fifth line here, and forgetting was silent.
// Against the composite, a new optional capability breaks this build the
// moment it is embedded in render.CompleteSecretSource.
var _ render.CompleteSecretSource = (*vaultSource)(nil)

// The device package's optional interfaces get the same treatment.
var (
	_ device.Secrets       = (*vaultSource)(nil)
	_ device.GlobalSecrets = (*vaultSource)(nil)
)

// HostSecret is render.SecretSource / device.Secrets.
func (v *vaultSource) HostSecret(hostname, key string) (string, error) {
	return v.c.HostSecret(hostname, key)
}

// VaultSecret is render.VaultSource: the Python
// `VaultSecrets().some_key` attribute lookup, i.e. the default mount's
// `some-key` path, `secret` key. Python dashes the attribute name
// (util/secrets.py `key.replace("_", "-")`), so callers may pass either
// spelling.
func (v *vaultSource) VaultSecret(key string) (string, error) {
	path := strings.ReplaceAll(strings.TrimSpace(key), "_", "-")
	return v.c.Get(context.Background(), "", path, "secret")
}

// GlobalSecret is device.GlobalSecrets; same lookup as VaultSecret.
func (v *vaultSource) GlobalSecret(name string) (string, error) {
	return v.VaultSecret(name)
}

// tacacsKeysPath is the fleet-wide secret every device's TACACS+ key
// lives in: cluster-secrets/tacacs-keys, one field per hostname.
const tacacsKeysPath = "tacacs-keys"

// TacacsKey is render.TacacsSource. Python's BaseHost.tacacs_key reads
// `self.secret(self.hostname, ..., secret_path="tacacs-keys")["key"]`,
// i.e. the cluster-secrets mount, path `tacacs-keys`, FIELD <hostname>,
// whose value is a `{"address": ..., "key": ...}` object; only "key" is
// rendered.
//
// Unlike Python this never mints and writes back a missing key: barf-go
// reads secrets and nothing else, so an absent host is an error the
// operator resolves, not a silent Vault write during a render.
func (v *vaultSource) TacacsKey(hostname string) (string, error) {
	data, err := v.c.ReadSecret(context.Background(), vault.MountClusterSecrets, tacacsKeysPath)
	if err != nil {
		return "", err
	}
	entry, ok := data[hostname]
	if !ok {
		return "", fmt.Errorf("no tacacs key for %q in %s/%s",
			hostname, vault.MountClusterSecrets, tacacsKeysPath)
	}
	// KV v2 returns the nested object as a decoded map, but a value
	// written as a JSON *string* (the CLI's `vault kv patch k=@file`
	// spelling) round-trips as text; accept both.
	fields, ok := entry.(map[string]any)
	if !ok {
		text, isText := entry.(string)
		if !isText {
			return "", fmt.Errorf("tacacs key for %q is %T, not an object", hostname, entry)
		}
		if err := json.Unmarshal([]byte(text), &fields); err != nil {
			return "", fmt.Errorf("tacacs key for %q is not a JSON object", hostname)
		}
	}
	key, ok := fields["key"].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("tacacs key for %q has no %q field", hostname, "key")
	}
	return key, nil
}

// WireguardKeypair is render.WireguardSource: the fabric mesh keypairs,
// stored in the cluster-secrets mount as public_key/private_key.
func (v *vaultSource) WireguardKeypair(path string) (render.Keypair, error) {
	ctx := context.Background()
	public, err := v.c.Get(ctx, vault.MountClusterSecrets, path, "public_key")
	if err != nil {
		return render.Keypair{}, err
	}
	private, err := v.c.Get(ctx, vault.MountClusterSecrets, path, "private_key")
	if err != nil {
		return render.Keypair{}, err
	}
	return render.Keypair{Public: public, Private: private}, nil
}
