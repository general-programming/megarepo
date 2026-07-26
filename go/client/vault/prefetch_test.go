package vault

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPrefetchWarmsCacheConcurrently(t *testing.T) {
	fv := &fakeVault{data: map[string]map[string]any{}}
	paths := make([]string, 0, 32)
	for i := 0; i < 32; i++ {
		p := fmt.Sprintf("wglink-a--b-%d", i)
		fv.data["cluster-secrets/"+p] = map[string]any{"private_key": "k", "public_key": "p"}
		paths = append(paths, p)
	}
	c := newTestClient(t, fv)

	c.PrefetchHostSecrets(context.Background(), paths...)
	if got := fv.calls.Load(); got != 32 {
		t.Fatalf("prefetch made %d reads, want 32", got)
	}

	// Every path is now cached: the reads below must not hit the server.
	for _, p := range paths {
		if _, err := c.ReadSecret(context.Background(), "cluster-secrets", p); err != nil {
			t.Fatalf("ReadSecret(%s): %v", p, err)
		}
	}
	if got := fv.calls.Load(); got != 32 {
		t.Fatalf("cached reads hit the server: %d calls", got)
	}
}

func TestPrefetchIsConcurrent(t *testing.T) {
	// The whole point is not serialising on Vault round trips, so assert
	// that more than one read is genuinely in flight at a time.
	var mu sync.Mutex
	inFlight, peak := 0, 0

	fv := &fakeVault{data: map[string]map[string]any{}}
	for i := 0; i < 16; i++ {
		fv.data[fmt.Sprintf("cluster-secrets/p%d", i)] = map[string]any{"k": "v"}
	}
	base := fv.handler(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)
		base(w, r)

		mu.Lock()
		inFlight--
		mu.Unlock()
	}))
	t.Cleanup(srv.Close)

	c, err := New(Options{Address: srv.URL, Token: "test-token"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	paths := make([]string, 0, 16)
	for i := 0; i < 16; i++ {
		paths = append(paths, fmt.Sprintf("p%d", i))
	}
	c.Prefetch(context.Background(), "cluster-secrets", paths, 8)

	mu.Lock()
	defer mu.Unlock()
	if peak < 2 {
		t.Fatalf("prefetch serialised: peak concurrency was %d", peak)
	}
}

func TestPrefetchSkipsCachedAndMissing(t *testing.T) {
	fv := &fakeVault{data: map[string]map[string]any{
		"cluster-secrets/present": {"k": "v"},
	}}
	c := newTestClient(t, fv)

	if _, err := c.ReadSecret(context.Background(), "cluster-secrets", "present"); err != nil {
		t.Fatalf("ReadSecret: %v", err)
	}
	before := fv.calls.Load()

	// "present" is already cached, "gone" does not exist: neither may
	// produce an error, and the cached one must not be re-read.
	c.PrefetchHostSecrets(context.Background(), "present", "present", "gone", "")
	if got := fv.calls.Load(); got != before+1 {
		t.Fatalf("prefetch made %d extra reads, want 1 (the missing path)", got-before)
	}

	// A missing path stays missing: warming never creates anything.
	if _, err := c.ReadSecret(context.Background(), "cluster-secrets", "gone"); err == nil {
		t.Fatal("prefetch must not have created the missing secret")
	}
}

func TestPrefetchEmptyAndCancelled(t *testing.T) {
	fv := &fakeVault{data: map[string]map[string]any{"cluster-secrets/p": {"k": "v"}}}
	c := newTestClient(t, fv)

	c.PrefetchHostSecrets(context.Background())
	if fv.calls.Load() != 0 {
		t.Fatal("an empty prefetch must not call Vault")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Prefetch(ctx, "", []string{"p"}, 4) // must return, not hang
}

func TestClientSatisfiesPrefetcher(t *testing.T) {
	var _ Prefetcher = (*Client)(nil)
}
