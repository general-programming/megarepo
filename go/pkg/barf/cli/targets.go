package cli

import (
	"context"
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/prefetch"
)

// resolveTargets is the host selection shared by every command, ported from
// barf/cli/common.py: hostnames, or the single word "all". Duplicates are
// dropped, keeping the order given.
//
// No targets is a usage error, matching Python's click.argument(required=True):
// a bare `barf generate`/`diff`/`deploy` must never silently select the whole
// fleet. Commands that legitimately default to fleet-wide (`status`, `list`)
// opt in explicitly by passing "all" themselves rather than relying on this
// function to fill it in.
func resolveTargets(hosts []model.Host, targets []string) ([]*model.Host, error) {
	byIndex := func(i int) *model.Host { return &hosts[i] }

	if len(targets) == 0 {
		return nil, fmt.Errorf(`no hosts given; pass one or more hostnames, or "all"`)
	}
	for _, t := range targets {
		if t != "all" {
			continue
		}
		if len(targets) > 1 {
			return nil, fmt.Errorf(`"all" cannot be combined with other targets`)
		}
		return allHosts(hosts), nil
	}

	seen := make(map[string]bool, len(targets))
	var selected []*model.Host
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true

		found := false
		for i := range hosts {
			if hosts[i].Hostname == target {
				selected = append(selected, byIndex(i))
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown device %q", target)
		}
	}
	return selected, nil
}

// defaultAllTargets is the opt-in resolveTargets itself no longer provides:
// a command whose Python original always ran fleet-wide (status has no
// TARGETS argument at all) calls this so an empty argv still means "all",
// spelled out rather than left implicit.
func defaultAllTargets(targets []string) []string {
	if len(targets) == 0 {
		return []string{"all"}
	}
	return targets
}

func allHosts(hosts []model.Host) []*model.Host {
	out := make([]*model.Host, len(hosts))
	for i := range hosts {
		out[i] = &hosts[i]
	}
	return out
}

// prefetchLinkKeys warms the WireGuard-keypair cache for hosts' links
// concurrently, mirroring Python's prefetch_link_keys: fetching both sides
// of every link serially (a cold Vault read for the local key, another for
// the peer's) otherwise dominates a fleet render. Best-effort and read-only
// — secrets not supporting prefetch.Prefetcher (test fakes; a SecretSource
// with no batching to offer) are silently skipped, never minted, never an
// error.
func prefetchLinkKeys(ctx context.Context, secrets SecretSource, hosts []*model.Host, links []model.Link) {
	if p, ok := secrets.(prefetch.Prefetcher); ok {
		prefetch.LinkKeysFor(ctx, p, hosts, links)
	}
}

func filterHosts(hosts []*model.Host, keep func(*model.Host) bool) []*model.Host {
	var out []*model.Host
	for _, h := range hosts {
		if keep(h) {
			out = append(out, h)
		}
	}
	return out
}

func (o *Options) loadTargets(targets []string) (*model.Network, []*model.Host, error) {
	path, err := o.networkPath()
	if err != nil {
		return nil, nil, err
	}
	net, err := loadNetwork(path)
	if err != nil {
		return nil, nil, fmt.Errorf("loading %s: %w", path, err)
	}
	selected, err := resolveTargets(net.Hosts, targets)
	if err != nil {
		return nil, nil, err
	}
	return net, selected, nil
}
