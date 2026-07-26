package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/prefetch"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// The only file naming the render, device, model and vault implementations;
// everything else talks to the local interfaces in deps.go.

func init() {
	loadNetwork = model.Load
	newSecrets = wireSecrets
	newRenderer = wireRenderer
	newReader = wireReader
	reportsStatus = wireReportsStatus
	isTemplatable = wireTemplatable
	mintHostSecret = wireMintHostSecret
}

// wireMintHostSecret is barf's one Vault WRITE path, deliberately built as
// its own client rather than reusing newSecrets: every other command's
// vaultSource is constructed without AllowMint, and that must stay true
// regardless of what this does. Mirrors Python's BaseHost.secret: an
// existing value comes back unwritten, a missing one is minted with a fresh
// token (vault.GenerateToken, matching secrets.token_urlsafe(16)).
func wireMintHostSecret(hostname, key string) (string, error) {
	c, err := vault.New(vault.Options{AllowMint: true})
	if err != nil {
		return "", err
	}
	return c.MintHostSecret(context.Background(), hostname, key, vault.GenerateToken)
}

func wireRenderer(deviceType string) (Renderer, error) {
	r, ok := vendor.Renderer(deviceType)
	if !ok {
		return nil, fmt.Errorf("no renderer for device type %q", deviceType)
	}
	return rendererAdapter{r}, nil
}

type rendererAdapter struct{ r render.Renderer }

// Render forwards to the real renderer. cli.SecretSource has the same method
// set as render.SecretSource, so the value passes straight through.
func (a rendererAdapter) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	return a.r.Render(h, n, s)
}

// Both helpers below are a single lookup against the vendor table on purpose:
// a hand-written vendor switch here would drift from device.New's.

func wireTemplatable(deviceType string) bool {
	return vendor.Templatable(deviceType)
}

// wireReportsStatus mirrors the Python REPORTS_STATUS flag.
func wireReportsStatus(deviceType string) bool {
	return vendor.ReportsStatus(deviceType)
}

// wireReader builds a read-only device client pinned to the endpoint the CLI
// already probed.
func wireReader(h *model.Host, address string, s SecretSource) (DeviceReader, error) {
	opts := device.Options{
		Endpoint: address,
		// Fleet devices serve the self-signed default SSL profile, as Python
		// also assumes.
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
	// Vendors managing only a slice of the device config (EOS) get a reader
	// that can also answer a scoped comparison; see scopeddiff.go.
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

func wireSecrets() (SecretSource, error) {
	c, err := vault.New(vault.Options{})
	if err != nil {
		return nil, err
	}
	src := &vaultSource{c: c}
	// Runtime half of the compile-time assertion below: fail at startup
	// naming the missing capability, not midway through a fleet render.
	if err := render.CheckSecretSource(src); err != nil {
		return nil, err
	}
	return src, nil
}

// vaultSource satisfies every optional secret interface. It only ever reads;
// secrets stay in memory, never logged or written to disk.
type vaultSource struct{ c *vault.Client }

// render resolves optional interfaces by runtime type assertion, so a missing
// method otherwise only surfaces as a failed production render (DNOS did).
// Assert against the COMPOSITE so a new capability breaks this build for free.
var _ render.CompleteSecretSource = (*vaultSource)(nil)

var (
	_ device.Secrets       = (*vaultSource)(nil)
	_ device.GlobalSecrets = (*vaultSource)(nil)
)

// HostSecret is render.SecretSource / device.Secrets.
func (v *vaultSource) HostSecret(hostname, key string) (string, error) {
	return v.c.HostSecret(hostname, key)
}

// VaultSecret is render.VaultSource: Python's `VaultSecrets().some_key` —
// default mount, path `some-key`, key `secret`, with _ dashed so both work.
func (v *vaultSource) VaultSecret(key string) (string, error) {
	path := strings.ReplaceAll(strings.TrimSpace(key), "_", "-")
	return v.c.Get(context.Background(), "", path, "secret")
}

// GlobalSecret is device.GlobalSecrets; same lookup as VaultSecret.
func (v *vaultSource) GlobalSecret(name string) (string, error) {
	return v.VaultSecret(name)
}

// PrefetchHostSecrets is prefetch.Prefetcher, satisfied so generate/diff/
// deploy can warm the WireGuard-keypair cache for the whole selection
// concurrently instead of paying two serial cold Vault reads per link.
var _ prefetch.Prefetcher = (*vaultSource)(nil)

func (v *vaultSource) PrefetchHostSecrets(ctx context.Context, paths ...string) {
	v.c.PrefetchHostSecrets(ctx, paths...)
}

// tacacsKeysPath holds one field per hostname under cluster-secrets.
const tacacsKeysPath = "tacacs-keys"

// TacacsKey is render.TacacsSource. Per Python's BaseHost.tacacs_key the field
// value is a `{"address": ..., "key": ...}` object; only "key" is rendered.
// Unlike Python this never writes back a missing key: barf-go only reads.
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
	// KV v2 returns the nested object as a decoded map, but a value written
	// as a JSON *string* round-trips as text; accept both.
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

// WireguardKeypair is render.WireguardSource: fabric mesh keypairs, stored in
// cluster-secrets as public_key/private_key.
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
