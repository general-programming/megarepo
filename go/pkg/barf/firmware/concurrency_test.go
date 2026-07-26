package firmware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Concurrency regression tests; all served by httptest.

// -- finding 4: ".partial" was not unique -----------------------------

// Regression: the scratch file was a deterministic "<target>.partial", so two
// processes sharing the cache truncated the same inode and copied into it at
// independent offsets; both passed the length guard and an interleaved image
// got promoted. Each response body is a different repeated byte, so any
// interleaving shows up.
func TestConcurrentDownloadsDoNotCorruptTheImage(t *testing.T) {
	const size = 512 * 1024
	const chunk = 4096

	var mu sync.Mutex
	next := byte('A')

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		fill := next
		next++
		mu.Unlock()

		w.Header().Set("Content-Length", itoa(size))
		buf := bytes.Repeat([]byte{fill}, chunk)
		for written := 0; written < size; written += chunk {
			if _, err := w.Write(buf); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	asset := Asset{Name: "vyos-rolling-generic-amd64.iso", Size: size, URL: srv.URL}

	// Two independent providers sharing one cache directory.
	const downloaders = 2
	paths := make([]string, downloaders)
	errs := make([]error, downloaders)
	var wg sync.WaitGroup
	for i := range downloaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := &VyOS{CacheDir: dir}
			paths[i], errs[i] = v.Download(t.Context(), asset)
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("download %d: %v", i, err)
		}
	}

	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatalf("reading the cached image: %v", err)
	}
	if len(body) != size {
		t.Fatalf("cached image is %d bytes, want %d", len(body), size)
	}
	// A mix of byte values means the two downloads shared a file.
	fill := body[0]
	for i, b := range body {
		if b != fill {
			t.Fatalf("the cached image is corrupt: byte %d is %q, but the file starts with %q "+
				"— two concurrent downloads interleaved into one file", i, b, fill)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the cache dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("scratch file %s left in the cache dir", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != asset.Name {
		t.Errorf("unexpected cache contents: %v", names(entries))
	}
	if filepath.Base(paths[0]) != asset.Name || paths[0] != paths[1] {
		t.Errorf("downloads disagreed about the cache path: %q vs %q", paths[0], paths[1])
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name()
	}
	return out
}

// -- finding 5: the mutex was held across the HTTP round trip ---------

// Regression: fetchRelease held v.mu across Do + decode on the shared
// package-level provider, so a second caller — even with a dead context —
// waited out the first caller's full 30s HTTP timeout.
func TestFetchReleaseDoesNotBlockOtherCallers(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(releaseFixture())
	}))
	t.Cleanup(func() {
		unblock()
		srv.Close()
	})

	v := &VyOS{ReleaseURL: srv.URL}

	// Caller one is parked in the request.
	slow := make(chan error, 1)
	go func() {
		_, err := v.LatestVersion(t.Context())
		slow <- err
	}()
	// Let it get into the round trip (i.e. holding the lock, before the fix).
	time.Sleep(100 * time.Millisecond)

	// Caller two's context is already dead; it must not queue behind caller one.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	returned := make(chan error, 1)
	go func() {
		_, err := v.LatestVersion(cancelled)
		returned <- err
	}()

	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("expected the cancelled caller to fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a second caller with an already-cancelled context blocked on the first " +
			"caller's HTTP round trip; the lock is still held across the request")
	}

	// The first caller still completes normally and its result is cached.
	unblock()
	if err := <-slow; err != nil {
		t.Fatalf("the parked caller failed: %v", err)
	}
	version, err := v.LatestVersion(t.Context())
	if err != nil {
		t.Fatalf("after caching: %v", err)
	}
	if version != fleetLatest {
		t.Fatalf("version = %q, want %q", version, fleetLatest)
	}
}

// -race companion: cold callers must agree on one release, and only a
// successful fetch may be cached.
func TestConcurrentFetchReleaseAgreesOnOneRelease(t *testing.T) {
	srv, calls := releaseServer(t, releaseFixture(), http.StatusOK)
	v := &VyOS{ReleaseURL: srv.URL}

	const callers = 16
	versions := make([]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			version, err := v.LatestVersion(t.Context())
			if err != nil {
				t.Errorf("caller %d: %v", i, err)
				return
			}
			versions[i] = version
		}()
	}
	wg.Wait()

	for i, got := range versions {
		if got != fleetLatest {
			t.Fatalf("caller %d saw %q, want %q", i, got, fleetLatest)
		}
	}
	// A cold stampede may cost duplicate GETs (preferred over an uncancellable
	// wait), but once warm the cache must issue no further requests.
	before := calls.Load()
	if _, err := v.LatestVersion(t.Context()); err != nil {
		t.Fatalf("warm read: %v", err)
	}
	if calls.Load() != before {
		t.Fatalf("a warm read hit the network: %d -> %d requests", before, calls.Load())
	}
}
