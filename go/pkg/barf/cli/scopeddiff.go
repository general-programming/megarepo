package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/scope"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// ScopedDiffer is implemented by device readers for vendors managing only a
// *slice* of the device config. The generic whole-config line diff is right
// for VyOS but nonsense for Arista EOS, where four managed lines against a
// megabyte barf does not own reports as `+4 -32859`. Dispatch is a capability
// of the *reader*, so fakes stay on the generic path and a third vendor slots
// in by registering a scope.Comparer.
type ScopedDiffer interface {
	// The rendered config is deliberately not an argument: scoped comparers
	// recompute desired state so credentials are compared by verifying the
	// secret against the device's hash rather than by hash text.
	ScopedDiff(ctx context.Context, h *model.Host, n *model.Network, s SecretSource, opts DiffOptions) (ConfigDiff, error)
}

// wrapScoped returns base, upgraded to a ScopedDiffer when h's devicetype has
// a scoped comparer registered and r can serve it.
func wrapScoped(h *model.Host, base DeviceReader, r device.Reader) DeviceReader {
	if h == nil {
		return base
	}
	comparer, ok := vendor.Comparer(h.DeviceType)
	if !ok {
		return base
	}
	sections, ok := r.(scope.SectionReader)
	if !ok {
		return base
	}
	return scopedReaderAdapter{DeviceReader: base, comparer: comparer, sections: sections}
}

// scopedReaderAdapter is a DeviceReader that also knows its vendor's managed
// scope. It cannot write: scope.SectionReader is satisfied by the read-only
// eAPI client, whose request primitive rejects non-`show` verbs.
type scopedReaderAdapter struct {
	DeviceReader
	comparer scope.Comparer
	sections scope.SectionReader
}

var _ ScopedDiffer = scopedReaderAdapter{}

func (a scopedReaderAdapter) ScopedDiff(ctx context.Context, h *model.Host, n *model.Network, s SecretSource, opts DiffOptions) (ConfigDiff, error) {
	result, err := a.comparer.Compare(ctx, scope.Input{
		Host:    h,
		Network: n,
		Secrets: s,
		Reader:  a.sections,
		// Same polarity on both sides; nothing to negate.
		ShowSecrets: opts.ShowSecrets,
	})
	if err != nil {
		return ConfigDiff{}, err
	}
	return scopedConfigDiff(result), nil
}

// scopedConfigDiff maps a scope.Result onto ConfigDiff: desired lines are
// "added", the device's current lines "removed".
func scopedConfigDiff(result scope.Result) ConfigDiff {
	d := ConfigDiff{
		Text:       result.Text,
		HasChanges: result.HasChanges,
		Summary:    result.Summary,
	}
	for _, change := range result.Drift {
		d.Added = append(d.Added, change.Desired)
		if change.Device != "" {
			d.Removed = append(d.Removed, change.Device)
		}
	}
	return d
}
