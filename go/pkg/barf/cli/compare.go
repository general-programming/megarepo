package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// compareConfig is the single place that decides how a device's running
// config is compared against the rendered one.
//
// Every command that answers "does this device match network.yml?" —
// `status`, `diff` and `deploy` — must give the same answer, so they all
// come through here. They did not always: `status` used to line-diff
// every vendor, which for VyOS compares `set` commands against a JSON
// tree and reported hundreds of phantom changes on a device `diff` and
// `deploy` both called clean.
//
// The comparison used is the one the vendor actually owns:
//   - a reader advertising ScopedDiffer owns only a slice of the device
//     config (EOS: the admin user, its keys, the enable secret and the
//     eAPI block) and compares that slice;
//   - VyOS owns the whole tree and is diffed as a path set — the same
//     computation `deploy` plans from, so the two cannot disagree about
//     what a deploy would do;
//   - anything else falls back to a plain line set.
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
