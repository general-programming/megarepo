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

// Minting: the one write path in this package.
//
// Python's BaseHost.secret(key, default_value=...) generates a missing
// per-host secret and writes it back to Vault on first render, which is
// how a brand-new device ever gets its first admin/enable password. That
// is a genuine requirement for deploying to a NEW device, but it is also
// a silent write to a production secret store, so here it is opt-in:
//
//	c, _ := vault.New(vault.Options{AllowMint: true})
//	pw, _ := c.MintHostSecret(ctx, "sea1-core-1", "admin-password", vault.GenerateToken)
//
// A client built without AllowMint returns ErrMintNotAllowed and writes
// nothing. Secret values are never logged, never returned in errors, and
// never written to disk.

// ErrMintNotAllowed is returned by MintHostSecret on a client that was
// not constructed with Options.AllowMint.
var ErrMintNotAllowed = errors.New(
	"vault: minting is disabled; construct the client with Options{AllowMint: true} to allow it")

// Minter is the write surface a caller needs in order to create a
// missing per-host secret. *Client implements it, but only mints when it
// was constructed with AllowMint.
type Minter interface {
	MintHostSecret(ctx context.Context, hostname, key string, gen func() (string, error)) (string, error)
}

var _ Minter = (*Client)(nil)

// tokenBytes is how many random bytes back a generated credential. It
// matches Python's secrets.token_urlsafe(16), which is 16 random bytes
// rendered as unpadded URL-safe base64 (22 characters).
const tokenBytes = 16

// GenerateToken returns a fresh credential shaped exactly like Python's
// generate_tacacs_key: secrets.token_urlsafe(16), i.e. 16 cryptographically
// random bytes in unpadded URL-safe base64.
func GenerateToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("vault: generating secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MintHostSecret returns the per-host secret at cluster-secrets
// host-<hostname>, creating it with gen() when it is missing.
//
// It mirrors Python's read-then-mint order exactly:
//
//   - the key already exists: return it, write nothing;
//   - the path exists but the key does not: KV v2 patch, which adds the
//     key without touching its siblings;
//   - the path does not exist at all: create it with just this key.
//
// gen may be nil, in which case a missing key is an error and nothing is
// written. Minting on a client without AllowMint returns
// ErrMintNotAllowed before any write is attempted.
func (c *Client) MintHostSecret(ctx context.Context, hostname, key string, gen func() (string, error)) (string, error) {
	return c.MintSecret(ctx, c.hostMount, HostSecretPath(hostname), key, gen)
}

// MintSecret is MintHostSecret against an arbitrary mount and path, the
// way Python's secret(..., secret_path=...) reaches the shared
// "tacacs-keys" secret. An empty mount uses the host mount.
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

	// From here on the key is genuinely missing and we would have to
	// write. Both guards come before any generation so a disallowed
	// client never even materialises a credential.
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
		// The whole path is missing: create it with this key only.
		if err := c.createSecret(ctx, mount, path, key, value); err != nil {
			return "", err
		}
	} else {
		// The path exists: patch so sibling keys survive.
		if err := c.patchSecret(ctx, mount, path, key, value); err != nil {
			return "", err
		}
	}

	c.cacheMint(mount, path, key, value)
	return value, nil
}

// patchSecret adds one key to an existing secret without rewriting the
// keys already there.
//
// The preferred form is a KV v2 merge patch, which is a single
// server-side operation and so cannot lose a concurrent sibling write.
// A token whose policy lacks the "patch" capability gets a 403; the
// fallback is the older read-then-write patch, which still merges rather
// than replaces. A missing path falls through to create.
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
		// Neither error can contain the value: only the key name is sent
		// back by Vault on failure, and we never format value here.
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

// cacheMint folds a freshly minted key into the read cache so the same
// process does not re-read (or, worse, re-mint) it.
func (c *Client) cacheMint(mount, path, key, value string) {
	ck := cacheKey(mount, path)
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[ck]
	if !ok {
		c.cache[ck] = map[string]any{key: value}
		return
	}
	// The cached map is handed out by ReadSecret, so copy on write
	// rather than mutating a map a caller may be reading.
	updated := make(map[string]any, len(entry)+1)
	for k, v := range entry {
		updated[k] = v
	}
	updated[key] = value
	c.cache[ck] = updated
}

// CanMint reports whether this client was constructed with minting
// enabled. Useful for a command that wants to fail early with a clear
// message before doing a long render.
func (c *Client) CanMint() bool { return c.allowMint }
