package firmware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Real values captured from the live VyOS nightly-build release feed; every
// version assertion below is pinned to them.
const (
	fleetRunning = "2026.07.11-0033-rolling"
	fleetLatest  = "2026.07.21-1151-rolling"
	fleetImage   = "vyos-2026.07.21-1151-rolling-generic-amd64.iso"
	fleetSig     = fleetImage + ".minisig"
	fleetSize    = int64(617611264)
)

func TestIsCurrentMatchesPythonContainment(t *testing.T) {
	cases := []struct {
		name    string
		latest  string
		version string
		want    bool
	}{
		{"fleet behind", fleetLatest, fleetRunning, false},
		{"fleet current", fleetLatest, fleetLatest, true},
		// Python's check is `latest in version`, so a decorated tag counts.
		{"decorated", fleetLatest, "VyOS " + fleetLatest + " (amd64)", true},
		{"empty version", fleetLatest, "", false},
		{"unknown latest", "", fleetRunning, false},
		// Containment is not a prefix test: a differing day must not match.
		{"other day", fleetLatest, "2026.07.20-1151-rolling", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCurrent(tc.latest, tc.version); got != tc.want {
				t.Fatalf("IsCurrent(%q, %q) = %v, want %v", tc.latest, tc.version, got, tc.want)
			}
		})
	}
}

func TestCompareVersionsOnRealFleetValues(t *testing.T) {
	if got := CompareVersions(fleetRunning, fleetLatest); got != -1 {
		t.Fatalf("running should sort older than latest, got %d", got)
	}
	if got := CompareVersions(fleetLatest, fleetRunning); got != 1 {
		t.Fatalf("latest should sort newer than running, got %d", got)
	}
	if got := CompareVersions(fleetLatest, fleetLatest); got != 0 {
		t.Fatalf("equal versions should compare 0, got %d", got)
	}
	ordered := []string{
		"2025.12.31-2359-rolling",
		"2026.01.01-0000-rolling",
		"2026.07.11-0033-rolling",
		"2026.07.11-1151-rolling",
		"2026.07.21-1151-rolling",
	}
	for i := 1; i < len(ordered); i++ {
		if CompareVersions(ordered[i-1], ordered[i]) != -1 {
			t.Fatalf("%s should sort before %s", ordered[i-1], ordered[i])
		}
	}
}

// releaseFixture: the live GitHub release, trimmed to the fields read.
func releaseFixture() map[string]any {
	return map[string]any{
		"tag_name": fleetLatest,
		"assets": []map[string]any{
			{"name": "vyos-2026.07.21-1151-rolling-generic-amd64.cdx.json", "size": 16867621,
				"browser_download_url": "https://example.invalid/cdx.json"},
			{"name": fleetImage, "size": fleetSize,
				"browser_download_url": "https://example.invalid/" + fleetImage},
			{"name": fleetSig, "size": 341,
				"browser_download_url": "https://example.invalid/" + fleetSig},
		},
	}
}

func releaseServer(t *testing.T, body any, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestVyOSProviderReadsRelease(t *testing.T) {
	srv, calls := releaseServer(t, releaseFixture(), http.StatusOK)
	p := &VyOS{ReleaseURL: srv.URL}
	ctx := context.Background()

	got, err := p.LatestVersion(ctx)
	if err != nil {
		t.Fatalf("LatestVersion: %v", err)
	}
	if got != fleetLatest {
		t.Fatalf("LatestVersion = %q, want %q", got, fleetLatest)
	}

	image, err := p.Image(ctx)
	if err != nil {
		t.Fatalf("Image: %v", err)
	}
	if image.Name != fleetImage || image.Size != fleetSize {
		t.Fatalf("Image = %+v, want the generic-amd64 iso", image)
	}

	sig, ok, err := p.Signature(ctx)
	if err != nil || !ok {
		t.Fatalf("Signature: ok=%v err=%v", ok, err)
	}
	if sig.Name != fleetSig {
		t.Fatalf("Signature = %q, want %q", sig.Name, fleetSig)
	}

	current, err := p.IsCurrent(ctx, fleetRunning)
	if err != nil {
		t.Fatalf("IsCurrent: %v", err)
	}
	if current {
		t.Fatalf("the fleet's %s must not read as current against %s", fleetRunning, fleetLatest)
	}

	if n := calls.Load(); n != 1 {
		t.Fatalf("release feed fetched %d times, want 1", n)
	}
}

func TestVyOSSignatureAbsentIsNotAnError(t *testing.T) {
	body := releaseFixture()
	assets := body["assets"].([]map[string]any)
	body["assets"] = assets[:2] // drop the .minisig

	srv, _ := releaseServer(t, body, http.StatusOK)
	p := &VyOS{ReleaseURL: srv.URL}

	_, ok, err := p.Signature(context.Background())
	if err != nil {
		t.Fatalf("Signature: %v", err)
	}
	if ok {
		t.Fatal("expected no signature asset")
	}
}

func TestVyOSMissingImageAsset(t *testing.T) {
	body := releaseFixture()
	body["assets"] = []map[string]any{{"name": "notes.txt", "size": 1,
		"browser_download_url": "https://example.invalid/notes.txt"}}
	srv, _ := releaseServer(t, body, http.StatusOK)

	if _, err := (&VyOS{ReleaseURL: srv.URL}).Image(context.Background()); err == nil {
		t.Fatal("expected an error when the release has no iso asset")
	}
}

func TestVyOSFailureIsNotCached(t *testing.T) {
	var calls atomic.Int64
	var ok atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !ok.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(releaseFixture())
	}))
	t.Cleanup(srv.Close)

	p := &VyOS{ReleaseURL: srv.URL}
	if _, err := p.LatestVersion(context.Background()); err == nil {
		t.Fatal("expected the 503 to surface")
	}
	ok.Store(true)
	got, err := p.LatestVersion(context.Background())
	if err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
	if got != fleetLatest {
		t.Fatalf("LatestVersion = %q after recovery", got)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("wanted a retry after failure, got %d calls", n)
	}
}

func TestVyOSDownloadCachesAndRejectsShortReads(t *testing.T) {
	payload := []byte("pretend this is an iso")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	p := &VyOS{CacheDir: dir}
	asset := Asset{Name: "image.iso", Size: int64(len(payload)), URL: srv.URL}

	path, err := p.Download(context.Background(), asset)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if path != filepath.Join(dir, "image.iso") {
		t.Fatalf("Download path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(payload) {
		t.Fatalf("cached file wrong: %v %q", err, data)
	}

	// A size mismatch must not leave a valid-looking cache entry behind.
	short := Asset{Name: "short.iso", Size: int64(len(payload)) + 100, URL: srv.URL}
	if _, err := p.Download(context.Background(), short); err == nil {
		t.Fatal("expected a short-download error")
	}
	if _, err := os.Stat(filepath.Join(dir, "short.iso")); !os.IsNotExist(err) {
		t.Fatal("a short download must not be left in the cache")
	}
	if _, err := os.Stat(filepath.Join(dir, "short.iso.partial")); !os.IsNotExist(err) {
		t.Fatal("the partial file must be cleaned up")
	}
}

func TestRegistry(t *testing.T) {
	if _, ok := For("vyos"); !ok {
		t.Fatal("vyos must have an image provider")
	}
	for _, dt := range []string{"eos", "mikrotik", "linux", "external"} {
		if _, ok := For(dt); ok {
			t.Fatalf("%s must not have an image provider", dt)
		}
	}
}

func TestCheckerCells(t *testing.T) {
	srv, _ := releaseServer(t, releaseFixture(), http.StatusOK)
	restore := providers["vyos"]
	providers["vyos"] = &VyOS{ReleaseURL: srv.URL}
	t.Cleanup(func() { providers["vyos"] = restore })

	c := NewChecker(context.Background(), nil, "vyos", "eos", "vyos")

	if got := c.Latest("vyos"); got != fleetLatest {
		t.Fatalf("Latest(vyos) = %q", got)
	}
	if got := c.Cell("vyos", fleetRunning); got != "no ("+fleetLatest+")" {
		t.Fatalf("behind cell = %q", got)
	}
	if got := c.Cell("vyos", fleetLatest); got != "yes" {
		t.Fatalf("current cell = %q", got)
	}
	// Vendors with no provider show "?", matching Python.
	if got := c.Cell("eos", "4.34.1F"); got != Unknown {
		t.Fatalf("eos cell = %q, want %q", got, Unknown)
	}
}

type recordingLogger struct{ msgs []string }

func (r *recordingLogger) Warn(msg string, args ...any) { r.msgs = append(r.msgs, msg) }

func TestCheckerDegradesWhenMirrorFeedUnreachable(t *testing.T) {
	srv, _ := releaseServer(t, nil, http.StatusInternalServerError)
	restore := providers["vyos"]
	providers["vyos"] = &VyOS{ReleaseURL: srv.URL}
	t.Cleanup(func() { providers["vyos"] = restore })

	log := &recordingLogger{}
	c := NewChecker(context.Background(), log, "vyos")

	if got := c.Cell("vyos", fleetRunning); got != Unknown {
		t.Fatalf("an unreachable feed must yield %q, got %q", Unknown, got)
	}
	if len(log.msgs) != 1 {
		t.Fatalf("expected one warning, got %v", log.msgs)
	}
	// A nil Checker must work too: callers may skip priming.
	var nilChecker *Checker
	if got := nilChecker.Cell("vyos", fleetRunning); got != Unknown {
		t.Fatalf("nil Checker cell = %q", got)
	}
}

func TestMirrorConfigFromMeta(t *testing.T) {
	meta := map[string]any{"firmware": map[string]any{
		"s3_endpoint": "https://acct.r2.cloudflarestorage.com",
		"bucket":      "public",
		"prefix":      "firmware",
		"public_base": "https://files.owo.me",
	}}
	cfg, err := MirrorConfigFromMeta(meta)
	if err != nil {
		t.Fatalf("MirrorConfigFromMeta: %v", err)
	}
	if cfg.Bucket != "public" || cfg.Prefix != "firmware" {
		t.Fatalf("cfg = %+v", cfg)
	}

	if _, err := MirrorConfigFromMeta(map[string]any{}); !errors.Is(err, ErrNoMirrorConfig) {
		t.Fatalf("missing block should be ErrNoMirrorConfig, got %v", err)
	}
	incomplete := map[string]any{"firmware": map[string]any{"bucket": "public"}}
	if _, err := MirrorConfigFromMeta(incomplete); err == nil {
		t.Fatal("expected an error for an incomplete firmware block")
	}
}

func testMirror(t *testing.T, h http.Handler) *Mirror {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	m, err := NewMirror(
		MirrorConfig{S3Endpoint: srv.URL, Bucket: "public", PublicBase: "https://files.owo.me"},
		Credentials{AccessKeyID: "AKIA-test", SecretAccessKey: "secret-test"},
		MirrorOptions{HTTP: srv.Client(), Now: func() time.Time {
			return time.Date(2026, 7, 21, 11, 51, 0, 0, time.UTC)
		}},
	)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	return m
}

func TestMirrorPublicURLAndPrefixDefault(t *testing.T) {
	m := testMirror(t, http.NotFoundHandler())
	if got := m.PublicURL(fleetImage); got != "https://files.owo.me/firmware/"+fleetImage {
		t.Fatalf("PublicURL = %q", got)
	}
	if got := m.Key(fleetImage); got != "firmware/"+fleetImage {
		t.Fatalf("Key = %q", got)
	}
}

// fakeS3 is an in-memory bucket that rejects unsigned requests.
type fakeS3 struct {
	t       *testing.T
	objects map[string][]byte
	puts    []string
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential=AKIA-test/") {
		f.t.Errorf("request %s %s is not SigV4-signed: %q", r.Method, r.URL.Path, auth)
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.Header.Get("X-Amz-Date") == "" || r.Header.Get("X-Amz-Content-Sha256") == "" {
		f.t.Errorf("missing x-amz signing headers on %s %s", r.Method, r.URL.Path)
	}
	key := strings.TrimPrefix(r.URL.Path, "/public/")

	switch r.Method {
	case http.MethodHead:
		body, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", itoa(len(body)))
		w.WriteHeader(http.StatusOK)
	case http.MethodPut:
		buf := make([]byte, r.ContentLength)
		if _, err := readFull(r.Body, buf); err != nil {
			f.t.Errorf("reading PUT body: %v", err)
		}
		f.objects[key] = buf
		f.puts = append(f.puts, key)
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func readFull(r interface{ Read([]byte) (int, error) }, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			if total == len(buf) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}

func TestMirrorPublishSignatureFirst(t *testing.T) {
	s3 := &fakeS3{t: t, objects: map[string][]byte{}}
	m := testMirror(t, s3)

	dir := t.TempDir()
	image := filepath.Join(dir, fleetImage)
	sig := filepath.Join(dir, fleetSig)
	if err := os.WriteFile(image, []byte("iso-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sig, []byte("sig"), 0o644); err != nil {
		t.Fatal(err)
	}

	url, err := m.Publish(context.Background(), image, sig)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if url != "https://files.owo.me/firmware/"+fleetImage {
		t.Fatalf("Publish URL = %q", url)
	}
	// VyOS fetches <url>.minisig during install, so the signature must land first.
	if len(s3.puts) != 2 || s3.puts[0] != "firmware/"+fleetSig || s3.puts[1] != "firmware/"+fleetImage {
		t.Fatalf("upload order = %v, want signature then image", s3.puts)
	}

	// Republishing the same bytes is a no-op: same size, no PUT.
	if _, err := m.Publish(context.Background(), image, sig); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if len(s3.puts) != 2 {
		t.Fatalf("a same-size copy must not be re-uploaded, puts = %v", s3.puts)
	}
}

func TestMirrorObjectSizeMissing(t *testing.T) {
	s3 := &fakeS3{t: t, objects: map[string][]byte{}}
	m := testMirror(t, s3)
	size, found, err := m.ObjectSize(context.Background(), "firmware/nope.iso")
	if err != nil {
		t.Fatalf("ObjectSize: %v", err)
	}
	if found || size != 0 {
		t.Fatalf("missing object reported size=%d found=%v", size, found)
	}
}

func TestMirrorRejectsIncompleteCredentials(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	m, err := NewMirror(
		MirrorConfig{S3Endpoint: srv.URL, Bucket: "public", PublicBase: "https://files.owo.me"},
		Credentials{}, MirrorOptions{HTTP: srv.Client()},
	)
	if err != nil {
		t.Fatalf("NewMirror: %v", err)
	}
	if _, _, err := m.ObjectSize(context.Background(), "firmware/x"); err == nil {
		t.Fatal("expected unsigned requests to be refused locally")
	}
}

// fakeSecrets stands in for the Vault client.
type fakeSecrets struct{ data map[string]map[string]any }

func (f fakeSecrets) ReadSecret(_ context.Context, mount, path string) (map[string]any, error) {
	d, ok := f.data[path]
	if !ok {
		return nil, errors.New("not found")
	}
	return d, nil
}

func TestCredentialsFromVault(t *testing.T) {
	reader := fakeSecrets{data: map[string]map[string]any{
		VaultSecretPath: {"access_key_id": "id", "secret_access_key": "key"},
	}}
	creds, err := CredentialsFromVault(context.Background(), reader)
	if err != nil {
		t.Fatalf("CredentialsFromVault: %v", err)
	}
	if creds.AccessKeyID != "id" || creds.SecretAccessKey != "key" {
		t.Fatal("credentials did not round-trip")
	}

	partial := fakeSecrets{data: map[string]map[string]any{VaultSecretPath: {"access_key_id": "id"}}}
	if _, err := CredentialsFromVault(context.Background(), partial); err == nil {
		t.Fatal("expected an error for a half-populated secret")
	}
	if _, err := CredentialsFromVault(context.Background(), nil); err == nil {
		t.Fatal("expected an error with no secret source")
	}
}

func TestSigV4CanonicalURI(t *testing.T) {
	cases := map[string]string{
		"":                           "/",
		"/public/firmware/a.iso":     "/public/firmware/a.iso",
		"/public/firmware/a%20b.iso": "/public/firmware/a%20b.iso",
		"/public/firmware/a+b.iso":   "/public/firmware/a%2Bb.iso",
	}
	for in, want := range cases {
		if got := canonicalURI(in); got != want {
			t.Fatalf("canonicalURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSigV4Structure(t *testing.T) {
	// fakeS3 covers signing end-to-end; this pins header shape, scope, determinism.
	req, err := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.amazonaws.com"
	creds := Credentials{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
	}
	when := time.Date(2015, 8, 30, 12, 36, 0, 0, time.UTC)
	if err := signV4(req, creds, "us-east-1", "service", hexSHA256(nil), when); err != nil {
		t.Fatalf("signV4: %v", err)
	}
	auth := req.Header.Get("Authorization")
	const want = "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20150830/us-east-1/service/aws4_request, " +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature="
	if !strings.HasPrefix(auth, want) {
		t.Fatalf("Authorization = %q, want prefix %q", auth, want)
	}
	// Deterministic for a fixed clock; a different secret must differ.
	req2, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	req2.Host = "example.amazonaws.com"
	if err := signV4(req2, creds, "us-east-1", "service", hexSHA256(nil), when); err != nil {
		t.Fatal(err)
	}
	if req2.Header.Get("Authorization") != auth {
		t.Fatal("signing is not deterministic for a fixed clock")
	}
	req3, _ := http.NewRequest(http.MethodGet, "https://example.amazonaws.com/", nil)
	req3.Host = "example.amazonaws.com"
	other := creds
	other.SecretAccessKey = "different"
	if err := signV4(req3, other, "us-east-1", "service", hexSHA256(nil), when); err != nil {
		t.Fatal(err)
	}
	if req3.Header.Get("Authorization") == auth {
		t.Fatal("a different secret produced the same signature")
	}
}

// TestDownloadClientHasNoTimeout pins the split between the two clients.
// http.Client.Timeout bounds the whole request including reading the body, so
// reusing the 30s metadata client for image bodies capped downloads at
// whatever fits in 30 seconds -- about 185 Mbit/s sustained for a 700MB VyOS
// ISO. It surfaced as "context deadline exceeded ... while reading body",
// which reads like a hung server rather than a client-side cap, and only on
// links slow enough to miss the window.
func TestDownloadClientHasNoTimeout(t *testing.T) {
	v := NewVyOS()

	if got := v.httpClient().Timeout; got != 30*time.Second {
		t.Errorf("metadata client timeout = %v, want 30s: short deadlines are the point for the release feed", got)
	}
	if got := v.downloadClient().Timeout; got != 0 {
		t.Errorf("download client timeout = %v, want 0: image size must not race a fixed deadline; the caller's context bounds it", got)
	}
}

// TestDownloadClientHonoursInjection keeps both clients overridable together,
// so tests and callers cannot end up exercising a different client than the
// one they configured.
func TestDownloadClientHonoursInjection(t *testing.T) {
	custom := &http.Client{Timeout: 7 * time.Second}
	v := &VyOS{HTTP: custom}

	if v.downloadClient() != custom {
		t.Error("downloadClient ignored the injected client")
	}
	if v.httpClient() != custom {
		t.Error("httpClient ignored the injected client")
	}
}
