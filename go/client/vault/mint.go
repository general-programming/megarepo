package vault

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	vaultapi "github.com/hashicorp/vault/api"
)

// Minting is this package's one write path: Python's BaseHost.secret generates
// a missing per-host secret and writes it back on first render. That is a
// silent write to a production store, so here it is gated on AllowMint.

// ErrMintNotAllowed means the client lacks Options.AllowMint.
var ErrMintNotAllowed = errors.New(
	"vault: minting is disabled; construct the client with Options{AllowMint: true} to allow it")

// Minter creates a missing per-host secret.
type Minter interface {
	MintHostSecret(ctx context.Context, hostname, key string, gen func() (string, error)) (string, error)
}

var _ Minter = (*Client)(nil)

const tokenBytes = 16

// GenerateToken mirrors Python generate_tacacs_key's secrets.token_urlsafe(16):
// 16 random bytes in unpadded URL-safe base64 (22 characters).
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("vault: generating secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MintHostSecret returns the per-host secret at host-<hostname>, creating it
// with gen() when missing, in Python's read-then-mint order: an existing key
// is returned unwritten, an existing path patched so siblings survive, a
// missing path created. A nil gen or no AllowMint errors before any write.
func (c *Client) MintHostSecret(ctx context.Context, hostname, key string, gen func() (string, error)) (string, error) {
	return c.MintSecret(ctx, c.hostMount, HostSecretPath(hostname), key, gen)
}

// MintSecret is MintHostSecret against an arbitrary mount and path, as
// Python's secret(..., secret_path=...) reaches shared "tacacs-keys".
func (c *Client) MintSecret(ctx context.Context, mount, path, key string, gen func() (string, error)) (string, error) {
	if mount == "" {
		mount = c.hostMount
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return "", fmt.Errorf("vault: empty secret path")
	}
	if key == "" {
		return "", fmt.Errorf("vault: empty secret key")
	}

	existing, readErr := c.ReadSecret(ctx, mount, path)
	if readErr == nil {
		if raw, ok := existing[key]; ok {
			value, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("vault: key %q in %s/%s is %T, not a string", key, mount, path, raw)
			}
			return value, nil
		}
	} else if !errors.Is(readErr, ErrPathNotFound) {
		return "", readErr
	}

	// Guards precede generation: a disallowed client never materialises one.
	if gen == nil {
		return "", &NotFoundError{Mount: mount, Path: path, Key: key, base: ErrKeyNotFound}
	}
	if !c.allowMint {
		return "", fmt.Errorf("%w (wanted %s/%s key %q)", ErrMintNotAllowed, mount, path, key)
	}

	value, err := gen()
	if err != nil {
		return "", fmt.Errorf("vault: generating %s/%s key %q: %w", mount, path, key, err)
	}
	if value == "" {
		return "", fmt.Errorf("vault: generator for %s/%s key %q produced an empty value", mount, path, key)
	}

	if readErr != nil {
		if err := c.createSecret(ctx, mount, path, key, value); err != nil {
			return "", err
		}
	} else {
		if err := c.patchSecret(ctx, mount, path, key, value); err != nil {
			return "", err
		}
	}

	c.cacheMint(mount, path, key, value)
	return value, nil
}

// patchSecret adds one key to an existing secret. The KV v2 merge patch is one
// server-side op that cannot lose a concurrent sibling write; tokens lacking
// the "patch" capability get a 403, hence the read-then-write fallback.
func (c *Client) patchSecret(ctx context.Context, mount, path, key, value string) error {
	data := map[string]any{key: value}

	_, err := c.api.KVv2(mount).Patch(ctx, path, data)
	if err == nil {
		return nil
	}
	if errors.Is(err, vaultapi.ErrSecretNotFound) {
		return c.createSecret(ctx, mount, path, key, value)
	}

	var respErr *vaultapi.ResponseError
	if errors.As(err, &respErr) && respErr.StatusCode == 404 {
		return c.createSecret(ctx, mount, path, key, value)
	}

	if _, rwErr := c.api.KVv2(mount).Patch(ctx, path, data, vaultapi.WithMergeMethod("rw")); rwErr != nil {
		// Vault echoes only the key name; value is never formatted in.
		return fmt.Errorf("vault: minting key %q in %s/%s: %w", key, mount, path, rwErr)
	}
	return nil
}

// createSecret writes a brand-new secret holding just this key.
func (c *Client) createSecret(ctx context.Context, mount, path, key, value string) error {
	if _, err := c.api.KVv2(mount).Put(ctx, path, map[string]any{key: value}); err != nil {
		return fmt.Errorf("vault: creating %s/%s with key %q: %w", mount, path, key, err)
	}
	return nil
}

// cacheMint caches a minted key so this process cannot re-mint it.
func (c *Client) cacheMint(mount, path, key, value string) {
	ck := cacheKey(mount, path)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[ck]
	if !ok {
		c.cache[ck] = map[string]any{key: value}
		return
	}
	// ReadSecret hands out this map; copy on write.
	updated := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		updated[k] = v
	}
	updated[key] = value
	c.cache[ck] = updated
}

// CanMint reports whether minting is enabled, so a command can fail early.
func (c *Client) CanMint() bool { return c.allowMint }
