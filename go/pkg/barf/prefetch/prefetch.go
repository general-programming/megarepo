// Package prefetch warms the secret cache before a fleet render, porting
// barf.util.render.prefetch_link_keys: a host needs the WireGuard keypairs of
// BOTH sides of each link, and fetching them serially dominates render time.
// Warming is read-only and best-effort — the Prefetcher never creates a secret
// and never reports an error, so a missing keypair falls back to the render.
package prefetch

import (
	"context"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Prefetcher warms a cache of per-host secret paths, declared locally so this
// package does not import the vault client.
type Prefetcher interface {
	PrefetchHostSecrets(ctx context.Context, paths ...string)
}

// LinkKeyPaths is every Vault path holding a WireGuard keypair for a link
// touching targets, both sides, sorted. IPsec links have no keypairs.
func LinkKeyPaths(targets []string, links []model.Link) []string {
	if len(targets) == 0 || len(links) == 0 {
		return nil
	}
	wanted := make(map[string]bool, len(targets))
	for _, name := range targets {
		wanted[name] = true
	}

	seen := make(map[string]bool)
	paths := make([]string, 0, len(links)*2)
	for _, link := range links {
		if link.IPsec {
			continue
		}
		if !wanted[link.A] && !wanted[link.B] {
			continue
		}
		for _, hostname := range [2]string{link.A, link.B} {
			path := link.KeyPath(hostname)
			if seen[path] {
				continue
			}
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// Hostnames is the hostnames of a set of hosts, LinkKeyPaths' usual input.
func Hostnames(hosts []*model.Host) []string {
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != nil {
			names = append(names, h.Hostname)
		}
	}
	return names
}

// LinkKeys warms the keypair cache for every link touching targets; a nil
// Prefetcher is a no-op, so callers may always call it unconditionally.
func LinkKeys(ctx context.Context, p Prefetcher, targets []string, links []model.Link) {
	if p == nil {
		return
	}
	paths := LinkKeyPaths(targets, links)
	if len(paths) == 0 {
		return
	}
	p.PrefetchHostSecrets(ctx, paths...)
}

// LinkKeysFor is LinkKeys taking hosts rather than hostnames.
func LinkKeysFor(ctx context.Context, p Prefetcher, hosts []*model.Host, links []model.Link) {
	LinkKeys(ctx, p, Hostnames(hosts), links)
}
