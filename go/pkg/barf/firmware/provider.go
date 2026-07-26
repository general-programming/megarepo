package firmware

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Asset is one downloadable release artifact.
type Asset struct {
	Name string
	Size int64
	URL  string
}

// Provider is upstream image metadata and downloads for one vendor.
//
// Mirrors Python's ImageProvider: it is shared between `barf status`
// (which only needs LatestVersion) and the update flow (which needs the
// image, its signature, and the local downloads).
type Provider interface {
	// LatestVersion is the upstream release tag, e.g.
	// "2026.07.21-1151-rolling".
	LatestVersion(ctx context.Context) (string, error)
	// IsCurrent reports whether a device-reported version is the latest.
	IsCurrent(ctx context.Context, version string) (bool, error)
	// Image is the installable image asset for the latest release.
	Image(ctx context.Context) (Asset, error)
	// Signature is the image's detached signature. ok is false when
	// upstream ships none, which is not an error.
	Signature(ctx context.Context) (asset Asset, ok bool, err error)
	// Download fetches an asset into the local cache once and returns
	// its path.
	Download(ctx context.Context, a Asset) (string, error)
}

// providers is the registry keyed by network.yml devicetype. Only VyOS
// has an image provider today; every other vendor reports Unknown.
var providers = map[string]Provider{
	"vyos": NewVyOS(),
}

// For returns the provider for a device type. ok is false for vendors
// with no upstream image feed, matching Python's PROVIDERS.get.
func For(deviceType string) (Provider, bool) {
	p, ok := providers[deviceType]
	return p, ok
}

// DeviceTypes lists the device types that have an image provider.
func DeviceTypes() []string {
	out := make([]string, 0, len(providers))
	for k := range providers {
		out = append(out, k)
	}
	return out
}

// CacheDir is where downloaded images live, matching Python's
// ~/.cache/barf/images.
func CacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "barf", "images")
	}
	return filepath.Join(home, ".cache", "barf", "images")
}

// VyOSReleaseURL is the nightly-build release feed Python reads.
const VyOSReleaseURL = "https://api.github.com/repos/vyos/vyos-nightly-build/releases/latest"

// vyosAssetSuffix selects the installable ISO out of the release's
// assets (the release also carries SBOM json files).
const vyosAssetSuffix = "-generic-amd64.iso"

// VyOS is the image provider for VyOS rolling releases.
//
// The release feed is fetched at most once per process on success;
// failures are not cached, so a transient outage does not poison the
// provider for the life of the command.
type VyOS struct {
	// ReleaseURL overrides VyOSReleaseURL (tests).
	ReleaseURL string
	// HTTP overrides the client used for both metadata and downloads.
	HTTP *http.Client
	// CacheDir overrides CacheDir() (tests).
	CacheDir string

	mu      sync.Mutex
	release *githubRelease
}

// NewVyOS builds a VyOS provider with the process defaults.
func NewVyOS() *VyOS { return &VyOS{} }

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

func (v *VyOS) httpClient() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (v *VyOS) releaseURL() string {
	if v.ReleaseURL != "" {
		return v.ReleaseURL
	}
	return VyOSReleaseURL
}

func (v *VyOS) cacheDir() string {
	if v.CacheDir != "" {
		return v.CacheDir
	}
	return CacheDir()
}

// fetchRelease returns the latest release metadata, fetched once on
// success.
//
// The HTTP round trip deliberately happens OUTSIDE the mutex. firmware.For
// hands out a package-level shared provider, so holding the lock across a
// 30s request makes every other caller — including one whose context is
// already cancelled — block on the first caller's network call. Two
// concurrent cold callers may each do a request; that is a wasted GET,
// which is much cheaper than an unbounded, uncancellable wait. Only a
// successful fetch is cached, and the first one to land wins so every
// caller sees the same release.
func (v *VyOS) fetchRelease(ctx context.Context) (*githubRelease, error) {
	v.mu.Lock()
	cached := v.release
	v.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	rel, err := v.getRelease(ctx)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.release == nil {
		v.release = rel
	}
	return v.release, nil
}

// getRelease does the actual request. It holds no lock and caches
// nothing.
func (v *VyOS) getRelease(ctx context.Context) (*githubRelease, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.releaseURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := v.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("firmware: fetching vyos release feed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("firmware: vyos release feed returned %s", resp.Status)
	}

	var rel githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("firmware: decoding vyos release feed: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("firmware: vyos release feed has no tag_name")
	}
	return &rel, nil
}

// LatestVersion implements Provider.
func (v *VyOS) LatestVersion(ctx context.Context) (string, error) {
	rel, err := v.fetchRelease(ctx)
	if err != nil {
		return "", err
	}
	return rel.TagName, nil
}

// IsCurrent implements Provider.
func (v *VyOS) IsCurrent(ctx context.Context, version string) (bool, error) {
	latest, err := v.LatestVersion(ctx)
	if err != nil {
		return false, err
	}
	return IsCurrent(latest, version), nil
}

// Image implements Provider.
func (v *VyOS) Image(ctx context.Context) (Asset, error) {
	rel, err := v.fetchRelease(ctx)
	if err != nil {
		return Asset{}, err
	}
	for _, a := range rel.Assets {
		if strings.HasSuffix(a.Name, vyosAssetSuffix) {
			return Asset{Name: a.Name, Size: a.Size, URL: a.URL}, nil
		}
	}
	return Asset{}, fmt.Errorf("firmware: no %s asset in release %s", vyosAssetSuffix, rel.TagName)
}

// Signature implements Provider. VyOS ships a minisign signature next to
// the ISO; `add system image` fetches "<url>.minisig", so the mirror must
// publish it under exactly that name.
func (v *VyOS) Signature(ctx context.Context) (Asset, bool, error) {
	rel, err := v.fetchRelease(ctx)
	if err != nil {
		return Asset{}, false, err
	}
	image, err := v.Image(ctx)
	if err != nil {
		return Asset{}, false, err
	}
	wanted := image.Name + ".minisig"
	for _, a := range rel.Assets {
		if a.Name == wanted {
			return Asset{Name: a.Name, Size: a.Size, URL: a.URL}, true, nil
		}
	}
	return Asset{}, false, nil
}

// Download implements Provider: fetch an asset into the local cache
// once. A cached file of the expected size is reused; a short download
// is discarded rather than left behind as a valid-looking cache entry.
func (v *VyOS) Download(ctx context.Context, a Asset) (string, error) {
	if a.URL == "" {
		return "", fmt.Errorf("firmware: asset %q has no download URL", a.Name)
	}
	dir := v.cacheDir()
	target := filepath.Join(dir, a.Name)

	if info, err := os.Stat(target); err == nil && info.Size() == a.Size {
		return target, nil
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("firmware: creating cache dir: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := v.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("firmware: downloading %s: %w", a.Name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("firmware: downloading %s: %s", a.Name, resp.Status)
	}

	// The scratch file MUST be unique. A deterministic "<name>.partial"
	// under a shared cache dir means two concurrent barf processes both
	// create and truncate the same inode and then io.Copy into it at
	// independent offsets. Each writes Size bytes, so the length guard
	// below passes for both, and the rename promotes an interleaved,
	// corrupt ISO — which is then mirrored and installed on a router.
	f, err := os.CreateTemp(dir, a.Name+".*.partial")
	if err != nil {
		return "", fmt.Errorf("firmware: creating a scratch file in %s: %w", dir, err)
	}
	partial := f.Name()
	// CreateTemp makes the file 0600; the cache is readable like any other
	// downloaded artifact (and may be served by the mirror).
	_ = f.Chmod(0o644)
	written, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("firmware: downloading %s: %w", a.Name, copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("firmware: writing %s: %w", partial, closeErr)
	}
	if a.Size > 0 && written != a.Size {
		_ = os.Remove(partial)
		return "", fmt.Errorf("firmware: short download for %s: got %d bytes, want %d", a.Name, written, a.Size)
	}
	// Rename is atomic within the directory, so a concurrent downloader
	// either sees the old file or a complete new one, never a half-written
	// mix. Two racing downloads both promote a complete, correct file.
	if err := os.Rename(partial, target); err != nil {
		_ = os.Remove(partial)
		return "", fmt.Errorf("firmware: finalising %s: %w", target, err)
	}
	return target, nil
}

var _ Provider = (*VyOS)(nil)
