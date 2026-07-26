package cli

import (
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// resolveTargets is the host selection shared by every command, ported from
// barf/cli/common.py: hostnames, or the single word "all". No targets also
// means "all". Duplicates are dropped, keeping the order given.
func resolveTargets(hosts []model.Host, targets []string) ([]*model.Host, error) {
	byIndex := func(i int) *model.Host { return &hosts[i] }

	if len(targets) == 0 {
		return allHosts(hosts), nil
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

func allHosts(hosts []model.Host) []*model.Host {
	out := make([]*model.Host, len(hosts))
	for i := range hosts {
		out[i] = &hosts[i]
	}
	return out
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
