package firmware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// VaultSecretPath is where the mirror's S3 API credentials live, matching
// Python's VAULT_SECRET ("r2-firmware" under the default "secret" mount).
const VaultSecretPath = "r2-firmware"

// mirrorRegion is what SigV4 signs with. R2 ignores regions but the
// signature still needs one; boto3 is configured with "auto" for the
// same reason.
const mirrorRegion = "auto"

// SecretReader is the read-only slice of the Vault client this package
// needs. Declared locally so firmware does not import the vault package.
// *vault.Client satisfies it.
type SecretReader interface {
	ReadSecret(ctx context.Context, mount, path string) (map[string]any, error)
}

// MirrorConfig is network.yml's global_meta.firmware block.
type MirrorConfig struct {
	S3Endpoint string `yaml:"s3_endpoint"`
	Bucket     string `yaml:"bucket"`
	PublicBase string `yaml:"public_base"`
	Prefix     string `yaml:"prefix"`
}

// ErrNoMirrorConfig is returned when global_meta has no firmware block.
var ErrNoMirrorConfig = errors.New(
	"global_meta: no firmware mirror configured; add a firmware: block with " +
		"s3_endpoint, bucket, and public_base")

// MirrorConfigFromMeta reads the mirror out of a decoded global_meta map.
//
// The Go model package does not (yet) model the firmware block, so this
// takes the raw map a caller decoded from network.yml rather than
// reaching into model.GlobalMeta.
func MirrorConfigFromMeta(globalMeta map[string]any) (MirrorConfig, error) {
	raw, ok := globalMeta["firmware"].(map[string]any)
	if !ok || len(raw) == 0 {
		return MirrorConfig{}, ErrNoMirrorConfig
	}
	str := func(key string) string {
		s, _ := raw[key].(string)
		return s
	}
	cfg := MirrorConfig{
		S3Endpoint: str("s3_endpoint"),
		Bucket:     str("bucket"),
		PublicBase: str("public_base"),
		Prefix:     str("prefix"),
	}
	for name, value := range map[string]string{
		"s3_endpoint": cfg.S3Endpoint,
		"bucket":      cfg.Bucket,
		"public_base": cfg.PublicBase,
	} {
		if value == "" {
			return MirrorConfig{}, fmt.Errorf("global_meta.firmware: missing %s", name)
		}
	}
	return cfg, nil
}

// Mirror publishes images and their signatures to an S3-compatible
// bucket that is also served publicly.
type Mirror struct {
	endpoint   string
	bucket     string
	publicBase string
	prefix     string

	creds Credentials
	http  *http.Client
	now   func() time.Time
}

// MirrorOptions are the optional knobs on NewMirror.
type MirrorOptions struct {
	// HTTP overrides the client used for S3 calls.
	HTTP *http.Client
	// Now overrides the signing clock (tests).
	Now func() time.Time
}

// NewMirror builds a mirror from its config and credentials. Prefix
// defaults to "firmware", matching Python.
func NewMirror(cfg MirrorConfig, creds Credentials, opts MirrorOptions) (*Mirror, error) {
	if cfg.S3Endpoint == "" || cfg.Bucket == "" || cfg.PublicBase == "" {
		return nil, ErrNoMirrorConfig
	}
	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix == "" {
		prefix = "firmware"
	}
	httpClient := opts.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Minute}
	}
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Mirror{
		endpoint:   strings.TrimRight(cfg.S3Endpoint, "/"),
		bucket:     cfg.Bucket,
		publicBase: strings.TrimRight(cfg.PublicBase, "/"),
		prefix:     prefix,
		creds:      creds,
		http:       httpClient,
		now:        nowFn,
	}, nil
}

// CredentialsFromVault reads the mirror's S3 API credentials from Vault,
// the way Python's get_secret("r2-firmware", key=None) does. The values
// are never logged.
func CredentialsFromVault(ctx context.Context, reader SecretReader) (Credentials, error) {
	if reader == nil {
		return Credentials{}, fmt.Errorf("firmware: no secret source for %s", VaultSecretPath)
	}
	data, err := reader.ReadSecret(ctx, "", VaultSecretPath)
	if err != nil {
		return Credentials{}, fmt.Errorf("firmware: reading mirror credentials: %w", err)
	}
	get := func(key string) string {
		s, _ := data[key].(string)
		return s
	}
	creds := Credentials{
		AccessKeyID:     get("access_key_id"),
		SecretAccessKey: get("secret_access_key"),
	}
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return Credentials{}, fmt.Errorf(
			"firmware: %s is missing access_key_id/secret_access_key", VaultSecretPath)
	}
	return creds, nil
}

// NewMirrorFromVault is the production constructor: mirror endpoints from
// network.yml, API tokens from Vault.
func NewMirrorFromVault(ctx context.Context, globalMeta map[string]any, reader SecretReader) (*Mirror, error) {
	cfg, err := MirrorConfigFromMeta(globalMeta)
	if err != nil {
		return nil, err
	}
	creds, err := CredentialsFromVault(ctx, reader)
	if err != nil {
		return nil, err
	}
	return NewMirror(cfg, creds, MirrorOptions{})
}

// Prefix is the key prefix objects are published under.
func (m *Mirror) Prefix() string { return m.prefix }

// Key is the bucket key an object name is published at.
func (m *Mirror) Key(name string) string { return m.prefix + "/" + name }

// PublicURL is the URL a device downloads an object from.
func (m *Mirror) PublicURL(name string) string {
	return m.publicBase + "/" + m.prefix + "/" + name
}

func (m *Mirror) objectURL(key string) (string, error) {
	base, err := url.Parse(m.endpoint)
	if err != nil {
		return "", fmt.Errorf("firmware: bad s3_endpoint %q: %w", m.endpoint, err)
	}
	base.Path = "/" + m.bucket + "/" + key
	return base.String(), nil
}

// ObjectSize is the mirrored object's size. found is false when the
// object does not exist, which is not an error.
func (m *Mirror) ObjectSize(ctx context.Context, key string) (size int64, found bool, err error) {
	target, err := m.objectURL(key)
	if err != nil {
		return 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
	if err != nil {
		return 0, false, err
	}
	if err := signV4(req, m.creds, mirrorRegion, "s3", unsignedPayload, m.now()); err != nil {
		return 0, false, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("firmware: HEAD %s: %w", key, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		length := resp.Header.Get("Content-Length")
		n, convErr := strconv.ParseInt(length, 10, 64)
		if convErr != nil {
			return 0, true, fmt.Errorf("firmware: HEAD %s: bad Content-Length %q", key, length)
		}
		return n, true, nil
	case http.StatusNotFound:
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("firmware: HEAD %s: %s", key, resp.Status)
	}
}

// Upload publishes a local file under the mirror's prefix, skipping the
// transfer when a same-size copy is already there, and verifying the
// mirrored size afterwards.
func (m *Mirror) Upload(ctx context.Context, local string) error {
	info, err := os.Stat(local)
	if err != nil {
		return fmt.Errorf("firmware: stat %s: %w", local, err)
	}
	size := info.Size()
	key := m.Key(filepath.Base(local))

	if existing, found, err := m.ObjectSize(ctx, key); err != nil {
		return err
	} else if found && existing == size {
		return nil
	}

	f, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("firmware: opening %s: %w", local, err)
	}
	defer func() { _ = f.Close() }()

	target, err := m.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	if err := signV4(req, m.creds, mirrorRegion, "s3", unsignedPayload, m.now()); err != nil {
		return err
	}

	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("firmware: PUT %s: %w", key, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("firmware: PUT %s: %s", key, resp.Status)
	}

	mirrored, found, err := m.ObjectSize(ctx, key)
	if err != nil {
		return err
	}
	if !found || mirrored != size {
		return fmt.Errorf("firmware: mirror upload of %s ended up %d bytes, expected %d",
			key, mirrored, size)
	}
	return nil
}

// Publish mirrors an image and its optional signature, returning the
// image's public URL.
//
// The signature lands first: VyOS fetches "<url>.minisig" during
// `add system image`, so the image URL must never be visible before its
// signature is.
func (m *Mirror) Publish(ctx context.Context, image string, signature string) (string, error) {
	if signature != "" {
		if err := m.Upload(ctx, signature); err != nil {
			return "", err
		}
	}
	if err := m.Upload(ctx, image); err != nil {
		return "", err
	}
	return m.PublicURL(filepath.Base(image)), nil
}

// Staged is the result of staging a release onto the mirror: everything
// the update flow needs to tell a device what to install.
type Staged struct {
	Version string
	URL     string
	Size    int64
	Signed  bool
}

// Stage downloads the latest image for a device type, mirrors it (with
// its signature when upstream ships one), and returns the public URL and
// size the update flow hands to the device.
//
// It never touches a device itself.
func Stage(ctx context.Context, p Provider, m *Mirror) (Staged, error) {
	version, err := p.LatestVersion(ctx)
	if err != nil {
		return Staged{}, err
	}
	image, err := p.Image(ctx)
	if err != nil {
		return Staged{}, err
	}
	imagePath, err := p.Download(ctx, image)
	if err != nil {
		return Staged{}, err
	}

	sigPath := ""
	sig, ok, err := p.Signature(ctx)
	if err != nil {
		return Staged{}, err
	}
	if ok {
		if sigPath, err = p.Download(ctx, sig); err != nil {
			return Staged{}, err
		}
	}

	publicURL, err := m.Publish(ctx, imagePath, sigPath)
	if err != nil {
		return Staged{}, err
	}
	return Staged{Version: version, URL: publicURL, Size: image.Size, Signed: ok}, nil
}
