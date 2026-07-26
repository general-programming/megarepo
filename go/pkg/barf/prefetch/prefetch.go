// Package prefetch warms the secret cache before a fleet render.
//
// It is the Go port of barf.util.render.prefetch_link_keys: rendering a
// host needs the WireGuard keypairs of BOTH sides of each of its links,
// and fetching them one at a time from Vault dominates render time, so
// they are pulled concurrently up front.
//
// Warming is read-only and best-effort by construction: the Prefetcher
// it drives never creates a secret and never reports an error, so a
// genuinely missing keypair is left for the render to handle serially.
package prefetch

import (
	"context"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Prefetcher warms a cache of per-host secret paths. Declared locally so
// this package does not import the vault client; *vault.Client satisfies
// it.
type Prefetcher interface {
	PrefetchHostSecrets(ctx context.Context, paths ...string)
}

// LinkKeyPaths is every Vault path holding a WireGuard keypair for a link
// that touches one of targets, both sides included, sorted so the work is
// deterministic.
//
// IPsec links are skipped: they have no WireGuard keypairs in Vault.
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

// Hostnames is the hostnames of a set of hosts, the usual input to
// LinkKeyPaths.
func Hostnames(hosts []*model.Host) []string {
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		if h != nil {
			names = append(names, h.Hostname)
		}
	}
	return names
}

// LinkKeys warms the keypair cache for every link touching targets.
//
// A nil Prefetcher (a secret source with no cache to warm, e.g. a test
// fake) is a no-op, so callers can always call this unconditionally.
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
