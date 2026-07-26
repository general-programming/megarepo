package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// compareConfig is the single place deciding how a running config is compared
// against the rendered one; `status`, `diff` and `deploy` all come through
// here so they cannot disagree. The comparison is the one the vendor owns:
// a reader advertising ScopedDiffer compares only its managed slice (EOS: the
// admin user, its keys, the enable secret, the eAPI block); VyOS owns the
// whole tree and is diffed as a path set, the same computation `deploy` plans
// from; anything else falls back to a plain line set.
func compareConfig(
	ctx context.Context,
	reader DeviceReader,
	h *model.Host,
	net *model.Network,
	rendered string,
	secrets SecretSource,
	opts DiffOptions,
) (ConfigDiff, error) {
	if scoped, ok := reader.(ScopedDiffer); ok {
		return scoped.ScopedDiff(ctx, h, net, secrets, opts)
	}

	running, err := reader.RunningConfig(ctx)
	if err != nil {
		return ConfigDiff{}, err
	}

	if h.DeviceType == "vyos" {
		plan, err := planVyOS(rendered, running)
		if err != nil {
			return ConfigDiff{}, err
		}
		return plan.configDiff(opts), nil
	}

	return DiffConfigs(rendered, running, opts), nil
}
