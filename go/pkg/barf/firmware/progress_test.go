package firmware

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/progress"
)

// captureSink records what a transfer reported, so the tests below assert on
// the events rather than on a rendering.
type captureSink struct {
	mu       sync.Mutex
	started  []progress.Transfer
	updates  []int64
	finished []int64
	err      error
	skipped  []progress.Transfer
	reasons  []string
}

func (s *captureSink) Start(t progress.Transfer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = append(s.started, t)
}

func (s *captureSink) Update(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, n)
}

func (s *captureSink) Finish(n int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = append(s.finished, n)
	if err != nil {
		s.err = err
	}
}

func (s *captureSink) Skip(t progress.Transfer, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.skipped = append(s.skipped, t)
	s.reasons = append(s.reasons, reason)
}

// isoPayload is big enough that io.Copy makes several writes, so progress has
// something to report mid-transfer.
func isoPayload() []byte { return bytes.Repeat([]byte("vyos-iso-"), 40000) }

// TestDownloadReportsProgressAndStaysByteCorrect is the load-bearing pair:
// progress must be observed AND the cached file must be exactly what the
// server sent.
func TestDownloadReportsProgressAndStaysByteCorrect(t *testing.T) {
	payload := isoPayload()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	p := &VyOS{CacheDir: dir}
	asset := Asset{Name: "image.iso", Size: int64(len(payload)), URL: srv.URL + "/image.iso"}

	sink := &captureSink{}
	ctx := progress.WithSink(context.Background(), sink)
	path, err := p.Download(ctx, asset)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- a path this test just created
	if err != nil {
		t.Fatalf("reading the cached image: %v", err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("progress reporting changed the downloaded bytes")
	}

	if len(sink.started) != 1 || sink.started[0].Name != "image.iso" ||
		sink.started[0].Total != asset.Size || sink.started[0].URL != asset.URL {
		t.Fatalf("Start events = %+v", sink.started)
	}
	if len(sink.updates) < 2 {
		t.Fatalf("expected several progress updates, got %v", sink.updates)
	}
	// Updates are cumulative and monotonic, ending at the whole file.
	for i := 1; i < len(sink.updates); i++ {
		if sink.updates[i] < sink.updates[i-1] {
			t.Fatalf("updates went backwards: %v", sink.updates)
		}
	}
	if last := sink.updates[len(sink.updates)-1]; last != asset.Size {
		t.Fatalf("last update = %d, want %d", last, asset.Size)
	}
	if len(sink.finished) != 1 || sink.finished[0] != asset.Size || sink.err != nil {
		t.Fatalf("Finish = %v, err = %v", sink.finished, sink.err)
	}
	if len(sink.skipped) != 0 {
		t.Fatalf("a real download must not report a skip: %v", sink.reasons)
	}
}

// TestDownloadReportsACacheHitAsASkip: reusing a cached image used to look
// exactly like an instant download.
func TestDownloadReportsACacheHitAsASkip(t *testing.T) {
	payload := []byte("pretend this is an iso")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	p := &VyOS{CacheDir: dir}
	asset := Asset{Name: "image.iso", Size: int64(len(payload)), URL: srv.URL}

	if _, err := p.Download(context.Background(), asset); err != nil {
		t.Fatalf("first Download: %v", err)
	}

	sink := &captureSink{}
	if _, err := p.Download(progress.WithSink(context.Background(), sink), asset); err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if hits != 1 {
		t.Fatalf("the cached image was re-fetched (%d requests)", hits)
	}
	if len(sink.started) != 0 {
		t.Fatalf("a cache hit must not start a transfer: %+v", sink.started)
	}
	if len(sink.skipped) != 1 || !strings.Contains(sink.reasons[0], "already in the local cache") {
		t.Fatalf("skip events = %+v %v", sink.skipped, sink.reasons)
	}
	if sink.skipped[0].Total != asset.Size {
		t.Fatalf("the skip must carry the size, got %d", sink.skipped[0].Total)
	}
}

// TestDownloadFailureNamesTheURLAndTheBytes: the silent-download bug was
// misread as a network fault, so every failure has to say what it was doing.
func TestDownloadFailureNamesTheURLAndTheBytes(t *testing.T) {
	payload := []byte("half an iso")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	p := &VyOS{CacheDir: dir}
	// Claiming a larger size makes this a short download, the same shape as a
	// truncated transfer.
	asset := Asset{Name: "image.iso", Size: int64(len(payload)) + 4096, URL: srv.URL + "/image.iso"}

	sink := &captureSink{}
	_, err := p.Download(progress.WithSink(context.Background(), sink), asset)
	if err == nil {
		t.Fatal("expected a short-download error")
	}
	for _, want := range []string{asset.URL, "got 11 bytes, want 4107"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err, want)
		}
	}
	if len(sink.finished) != 1 || sink.err == nil {
		t.Fatalf("a failed download must be reported to the sink: %v %v", sink.finished, sink.err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "image.iso")); !os.IsNotExist(statErr) {
		t.Fatal("a short download must not be left in the cache")
	}
}

// TestUploadReportsProgressAndSkipsAMirroredCopy covers the other half of the
// same ~700MB: the mirror PUT.
func TestUploadReportsProgressAndSkipsAMirroredCopy(t *testing.T) {
	s3 := &fakeS3{t: t, objects: map[string][]byte{}}
	m := testMirror(t, s3)

	dir := t.TempDir()
	image := filepath.Join(dir, fleetImage)
	payload := isoPayload()
	if err := os.WriteFile(image, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	sink := &captureSink{}
	ctx := progress.WithSink(context.Background(), sink)
	if err := m.Upload(ctx, image); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !bytes.Equal(s3.objects["firmware/"+fleetImage], payload) {
		t.Fatal("progress reporting changed the uploaded bytes")
	}
	if len(sink.started) != 1 || sink.started[0].Op != progress.OpUpload ||
		sink.started[0].Total != int64(len(payload)) {
		t.Fatalf("Start events = %+v", sink.started)
	}
	if len(sink.finished) != 1 || sink.finished[0] != int64(len(payload)) || sink.err != nil {
		t.Fatalf("Finish = %v, err = %v", sink.finished, sink.err)
	}

	// Re-uploading the same bytes is a skip, not a silent no-op.
	again := &captureSink{}
	if err := m.Upload(progress.WithSink(context.Background(), again), image); err != nil {
		t.Fatalf("re-Upload: %v", err)
	}
	if len(again.started) != 0 || len(again.skipped) != 1 {
		t.Fatalf("a same-size copy must report a skip: %+v %+v", again.started, again.skipped)
	}
}
