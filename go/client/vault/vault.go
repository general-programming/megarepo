// Package vault is a read-only KV v2 secret reader over
// github.com/hashicorp/vault/api, honouring the standard environment plus the
// ~/.vault-token fallback and caching reads per process. Unlike Python's
// BaseHost.secret, which auto-mints missing host secrets, writes are opt-in:
// without Options.AllowMint a missing key is ErrKeyNotFound (see mint.go).
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
	MountSecret         = "secret"          // infra secrets, e.g. "infra/netbox"
	MountClusterSecrets = "cluster-secrets" // per-host, e.g. "host-sea1-core-1"
)

// HostSecretPrefix matches Python's f"host-{self.hostname}".
const HostSecretPrefix = "host-"

var (
	// ErrPathNotFound: no such path, or invisible to this token.
	ErrPathNotFound = errors.New("vault: secret path not found")
	// ErrKeyNotFound: the path exists but lacks the key.
	ErrKeyNotFound = errors.New("vault: key not found in secret")
)

// NotFoundError names the missing mount/path/key. Compare with errors.Is.
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

// Options configures a Client. The zero value reads the environment.
type Options struct {
	Address      string // overrides VAULT_ADDR
	Token        string // overrides VAULT_TOKEN and the ~/.vault-token fallback
	Namespace    string // overrides VAULT_NAMESPACE
	DefaultMount string // Get's mount; empty means MountSecret
	HostMount    string // HostSecret's mount; empty means MountClusterSecrets

	// API injects a preconfigured client (tests, alternate auth methods);
	// Address/Token/Namespace then apply only if non-empty.
	API *vaultapi.Client

	// AllowMint opts in to the package's ONLY write path (mint.go).
	AllowMint bool
}

// Client is a read-only Vault KV v2 reader, safe for concurrent use.
type Client struct {
	api          *vaultapi.Client
	defaultMount string
	hostMount    string
	allowMint    bool

	mu    sync.RWMutex
	cache map[string]map[string]any // "mount/path" -> secret data
}

// New builds a Client; a zero Options behaves like the Vault CLI.
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

// API exposes the underlying client, still read-only by contract.
func (c *Client) API() *vaultapi.Client { return c.api }

// tokenFromHelperFile reads ~/.vault-token, written by `vault login`.
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

// ReadSecret returns the full data map at mount/path, cached for the life of
// the process. The returned map is a shallow COPY: sharing the cached one
// would let a mutating caller race every reader.
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

// Get returns one string key from mount/path.
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

// HostSecretContext returns a per-host secret from host-<hostname>. It is
// Python's BaseHost.secret(key) minus the auto-minting write.
func (c *Client) HostSecretContext(ctx context.Context, hostname, key string) (string, error) {
	return c.Get(ctx, c.hostMount, HostSecretPath(hostname), key)
}

// HostSecret is HostSecretContext without a ctx, for render.SecretSource.
func (c *Client) HostSecret(hostname, key string) (string, error) {
	return c.HostSecretContext(context.Background(), hostname, key)
}

// SecretSource is barf's renderer interface, redeclared to avoid the import.
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

var _ SecretSource = (*Client)(nil)

// InvalidateCache drops every cached secret, e.g. after a rotation.
func (c *Client) InvalidateCache() {
	c.mu.Lock()
	c.cache = make(map[string]map[string]any)
	c.mu.Unlock()
}
