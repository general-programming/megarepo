package vault

import (
	"context"
	"sync"
)

// DefaultPrefetchWorkers matches Python's prefetch_keypairs(max_workers=16).
const DefaultPrefetchWorkers = 16

// Prefetch warms the read cache for a set of paths on one mount using
// concurrent reads.
//
// Vault KV v2 has no batch-read API, so "multi fetch" means doing the
// per-secret reads concurrently. Rendering a fleet needs both sides of
// every WireGuard link, and fetching those serially dominates render
// time; warming them up front turns N round trips into N/workers.
//
// It is strictly read-only and never returns an error: a path that is
// missing (or unreadable) is simply left cold for the render to deal
// with serially, exactly like Python's prefetch, which swallows the
// missing-keypair error so a cache warm never creates a secret.
//
// An empty mount uses the client's host mount ("cluster-secrets"), which
// is where WireGuard keypairs live.
func (c *Client) Prefetch(ctx context.Context, mount string, paths []string, workers int) {
	if mount == "" {
		mount = c.hostMount
	}
	if workers <= 0 {
		workers = DefaultPrefetchWorkers
	}

	// Deduplicate, and skip anything already cached, preserving order so
	// the work is deterministic.
	seen := make(map[string]bool, len(paths))
	todo := make([]string, 0, len(paths))
	c.mu.RLock()
	for _, p := range paths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if _, cached := c.cache[cacheKey(mount, p)]; cached {
			continue
		}
		todo = append(todo, p)
	}
	c.mu.RUnlock()

	if len(todo) == 0 {
		return
	}
	if workers > len(todo) {
		workers = len(todo)
	}

	work := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range work {
				// Errors are deliberately dropped: this is a cache warm,
				// not a fetch. ReadSecret populates the cache on success.
				_, _ = c.ReadSecret(ctx, mount, path)
			}
		}()
	}
	for _, path := range todo {
		select {
		case <-ctx.Done():
			close(work)
			wg.Wait()
			return
		case work <- path:
		}
	}
	close(work)
	wg.Wait()
}

// PrefetchHostSecrets warms the cache for per-host secret paths on the
// host mount, using the default worker count.
func (c *Client) PrefetchHostSecrets(ctx context.Context, paths ...string) {
	c.Prefetch(ctx, c.hostMount, paths, DefaultPrefetchWorkers)
}

// Prefetcher is the cache-warming surface, declared so callers can depend
// on it without importing this package. *Client implements it.
type Prefetcher interface {
	PrefetchHostSecrets(ctx context.Context, paths ...string)
}

var _ Prefetcher = (*Client)(nil)
