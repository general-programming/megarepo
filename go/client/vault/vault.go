// Package vault is a read-only KV v2 secret reader for the Go side of the
// megarepo.
//
// It wraps github.com/hashicorp/vault/api and honours the standard
// environment configuration (VAULT_ADDR, VAULT_TOKEN, VAULT_NAMESPACE, ...)
// plus the ~/.vault-token fallback the Vault CLI writes. Reads are cached
// per process and the cache is safe for concurrent use.
//
// The package is read-only by default. The Python implementation
// (barf.vendors.BaseHost.secret) auto-mints missing host secrets by
// writing them back to Vault; here that behaviour is opt-in and off
// unless Options.AllowMint is set explicitly — without it a missing key
// is an error (ErrKeyNotFound) and MintHostSecret refuses. See mint.go.
//
// Secret values are never logged and never written to disk.
package vault

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	vaultapi "github.com/hashicorp/vault/api"
)

// Default mount points used across this repo.
const (
	// MountSecret holds infrastructure secrets at paths like
	// "infra/netbox" and "infra/dhcp-webhook".
	MountSecret = "secret"
	// MountClusterSecrets holds per-host secrets at paths like
	// "host-<hostname>".
	MountClusterSecrets = "cluster-secrets"
)

// HostSecretPrefix is prepended to a hostname to build its cluster-secrets
// path, matching Python's f"host-{self.hostname}".
const HostSecretPrefix = "host-"

var (
	// ErrPathNotFound is returned when a KV v2 path does not exist (or the
	// token cannot see it).
	ErrPathNotFound = errors.New("vault: secret path not found")
	// ErrKeyNotFound is returned when the path exists but lacks the key.
	ErrKeyNotFound = errors.New("vault: key not found in secret")
)

// NotFoundError carries which mount/path/key was missing. Compare against
// ErrPathNotFound / ErrKeyNotFound with errors.Is.
type NotFoundError struct {
	Mount string
	Path  string
	Key   string // empty when the whole path was missing
	base  error
}

func (e *NotFoundError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("vault: secret %s/%s not found", e.Mount, e.Path)
	}
	return fmt.Sprintf("vault: key %q not found in %s/%s", e.Key, e.Mount, e.Path)
}

func (e *NotFoundError) Unwrap() error { return e.base }

// Options configures a Client. The zero value reads everything from the
// environment, which is the normal case.
type Options struct {
	// Address overrides VAULT_ADDR.
	Address string
	// Token overrides VAULT_TOKEN and the ~/.vault-token fallback.
	Token string
	// Namespace overrides VAULT_NAMESPACE (Vault Enterprise / HCP).
	Namespace string
	// DefaultMount is the KV v2 mount used by Get when no mount is given.
	// Empty means MountSecret.
	DefaultMount string
	// HostMount is the KV v2 mount used by HostSecret. Empty means
	// MountClusterSecrets.
	HostMount string
	// API lets callers inject a preconfigured *vaultapi.Client (tests, or
	// an alternate auth method). When set, Address/Token/Namespace are
	// only applied if non-empty.
	API *vaultapi.Client
	// AllowMint opts this client in to the ONLY write path in the
	// package: minting a missing per-host secret (see mint.go). It is
	// false by default and must be set explicitly, so a client built the
	// normal way stays strictly read-only.
	AllowMint bool
}

// Client is a read-only Vault KV v2 reader. It is safe for concurrent use
// by multiple goroutines.
type Client struct {
	api          *vaultapi.Client
	defaultMount string
	hostMount    string
	// allowMint gates the minting write path; see Options.AllowMint.
	allowMint bool

	mu    sync.RWMutex
	cache map[string]map[string]any // "mount/path" -> secret data
}

// New builds a Client. With a zero Options it behaves like the Vault CLI:
// VAULT_ADDR for the endpoint, VAULT_TOKEN or ~/.vault-token for auth.
func New(opts Options) (*Client, error) {
	api := opts.API
	if api == nil {
		cfg := vaultapi.DefaultConfig()
		if cfg.Error != nil {
			return nil, fmt.Errorf("vault: reading environment config: %w", cfg.Error)
		}
		if opts.Address != "" {
			cfg.Address = opts.Address
		}
		var err error
		api, err = vaultapi.NewClient(cfg)
		if err != nil {
			return nil, fmt.Errorf("vault: creating client: %w", err)
		}
	} else {
		if opts.Address != "" {
			if err := api.SetAddress(opts.Address); err != nil {
				return nil, fmt.Errorf("vault: setting address: %w", err)
			}
		}
	}

	if opts.Namespace != "" {
		api.SetNamespace(opts.Namespace)
	}

	token := opts.Token
	if token == "" {
		token = api.Token() // picked up from VAULT_TOKEN by DefaultConfig
	}
	if token == "" {
		token = tokenFromHelperFile()
	}
	if token != "" {
		api.SetToken(token)
	}

	c := &Client{
		api:          api,
		defaultMount: firstNonEmpty(opts.DefaultMount, MountSecret),
		hostMount:    firstNonEmpty(opts.HostMount, MountClusterSecrets),
		allowMint:    opts.AllowMint,
		cache:        make(map[string]map[string]any),
	}
	return c, nil
}

// API exposes the underlying Vault client for callers that need something
// this package does not wrap. It is still read-only by contract.
func (c *Client) API() *vaultapi.Client { return c.api }

// tokenFromHelperFile reads ~/.vault-token, the file the Vault CLI writes
// after `vault login`. Returns "" when it is absent or unreadable.
func tokenFromHelperFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(home, ".vault-token"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func cacheKey(mount, path string) string { return mount + "/" + path }

// ReadSecret returns the full data map at mount/path, caching the result
// for the life of the process. An empty mount uses the client's default
// mount.
//
// The returned map is a COPY. Handing out the cached map itself would
// give every caller a reference to the same map: one caller mutating it
// (adding a key, deleting one) is an unsynchronised map write racing all
// the other readers, which Go turns into "fatal error: concurrent map
// writes" — a crash no RWMutex here can prevent, because the mutation
// happens outside this package entirely. cacheMint already copies on
// write for the same reason. The copy is shallow: nested values are still
// shared, but KV v2 secrets are flat string maps in this repo.
func (c *Client) ReadSecret(ctx context.Context, mount, path string) (map[string]any, error) {
	if mount == "" {
		mount = c.defaultMount
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return nil, fmt.Errorf("vault: empty secret path")
	}

	key := cacheKey(mount, path)

	c.mu.RLock()
	cached, ok := c.cache[key]
	c.mu.RUnlock()
	if ok {
		return maps.Clone(cached), nil
	}

	secret, err := c.api.KVv2(mount).Get(ctx, path)
	if err != nil {
		if errors.Is(err, vaultapi.ErrSecretNotFound) {
			return nil, &NotFoundError{Mount: mount, Path: path, base: ErrPathNotFound}
		}
		var respErr *vaultapi.ResponseError
		if errors.As(err, &respErr) && respErr.StatusCode == 404 {
			return nil, &NotFoundError{Mount: mount, Path: path, base: ErrPathNotFound}
		}
		// Deliberately no secret material in the error.
		return nil, fmt.Errorf("vault: reading %s/%s: %w", mount, path, err)
	}
	if secret == nil || secret.Data == nil {
		return nil, &NotFoundError{Mount: mount, Path: path, base: ErrPathNotFound}
	}

	c.mu.Lock()
	c.cache[key] = secret.Data
	c.mu.Unlock()

	return maps.Clone(secret.Data), nil
}

// Get returns a single string key from mount/path. An empty mount uses the
// client's default mount ("secret").
func (c *Client) Get(ctx context.Context, mount, path, key string) (string, error) {
	if mount == "" {
		mount = c.defaultMount
	}
	data, err := c.ReadSecret(ctx, mount, path)
	if err != nil {
		return "", err
	}
	raw, ok := data[key]
	if !ok {
		return "", &NotFoundError{Mount: mount, Path: strings.Trim(path, "/"), Key: key, base: ErrKeyNotFound}
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("vault: key %q in %s/%s is %T, not a string", key, mount, path, raw)
	}
	return value, nil
}

// HostSecretPath returns the cluster-secrets path for a hostname.
func HostSecretPath(hostname string) string { return HostSecretPrefix + hostname }

// HostSecretContext returns a per-host secret from the host mount
// ("cluster-secrets"), i.e. host-<hostname>. It mirrors Python's
// BaseHost.secret(key) minus the auto-minting write: a missing path or key
// is an error (ErrPathNotFound / ErrKeyNotFound), never a silent create.
func (c *Client) HostSecretContext(ctx context.Context, hostname, key string) (string, error) {
	return c.Get(ctx, c.hostMount, HostSecretPath(hostname), key)
}

// HostSecret satisfies barf's render.SecretSource. It is the context-free
// convenience wrapper around HostSecretContext.
func (c *Client) HostSecret(hostname, key string) (string, error) {
	return c.HostSecretContext(context.Background(), hostname, key)
}

// SecretSource is the interface barf's renderer depends on. Declared here
// so callers can assert against it without importing barf; *Client
// implements it.
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

var _ SecretSource = (*Client)(nil)

// InvalidateCache drops every cached secret. Mostly useful in long-running
// processes after a credential rotation.
func (c *Client) InvalidateCache() {
	c.mu.Lock()
	c.cache = make(map[string]map[string]any)
	c.mu.Unlock()
}
