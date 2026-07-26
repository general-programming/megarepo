package vault

import (
	"context"
	"sync"
)

// DefaultPrefetchWorkers matches Python's prefetch_keypairs(max_workers=16).
const DefaultPrefetchWorkers = 16

// Prefetch warms the read cache for paths on one mount (empty = host mount).
// KV v2 has no batch read, so this is concurrent per-secret reads. It never
// errors: as in Python's prefetch, swallowing it is what keeps a cache warm
// from ever creating a secret.
func (c *Client) Prefetch(ctx context.Context, mount string, paths []string, workers int) {
	if mount == "" {
		mount = c.hostMount
	}
	if workers <= 0 {
		workers = DefaultPrefetchWorkers
	}

	// Deduplicate and skip cached paths, preserving order.
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
				// Errors dropped: this is a cache warm, not a fetch.
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

// PrefetchHostSecrets warms per-host paths with DefaultPrefetchWorkers.
func (c *Client) PrefetchHostSecrets(ctx context.Context, paths ...string) {
	c.Prefetch(ctx, c.hostMount, paths, DefaultPrefetchWorkers)
}

// Prefetcher is the cache-warming surface, for callers avoiding this import.
type Prefetcher interface {
	PrefetchHostSecrets(ctx context.Context, paths ...string)
}

var _ Prefetcher = (*Client)(nil)
