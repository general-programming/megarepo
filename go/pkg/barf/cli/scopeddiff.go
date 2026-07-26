package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/scope"
)

// ScopedDiffer is implemented by device readers for vendors that manage
// only a *slice* of the device config.
//
// `barf diff` normally renders a host and line-set diffs that render
// against the device's whole running config. That is right for VyOS,
// where barf owns everything, and nonsense for Arista EOS, where the
// render is four managed lines and the running config is a megabyte of
// config barf does not own — the generic path reported that as
// `+4 -32859`.
//
// Dispatch is a capability of the *reader*, not a switch on devicetype in
// the diff command: a reader that can answer a scoped comparison advertises
// it by implementing this interface, and diff uses it when present. That
// keeps fakes (which implement only DeviceReader) on the generic path, and
// means a third vendor slots in by registering a scope.Comparer — no
// change to this file or to diff.go.
type ScopedDiffer interface {
	// ScopedDiff compares only the vendor's managed scope. The rendered
	// config is deliberately not an argument: scoped comparers recompute
	// the desired state so credentials can be compared by verifying the
	// secret against the device's hash rather than by hash text.
	ScopedDiff(ctx context.Context, h *model.Host, n *model.Network, s SecretSource, opts DiffOptions) (ConfigDiff, error)
}

// wrapScoped returns base, upgraded to a ScopedDiffer when h's devicetype
// has a scoped comparer registered and r can serve it. Called from
// wire.go, which is the only place that builds real readers.
func wrapScoped(h *model.Host, base DeviceReader, r device.Reader) DeviceReader {
	if h == nil {
		return base
	}
	comparer, ok := scope.For(h.DeviceType)
	if !ok {
		return base
	}
	sections, ok := r.(scope.SectionReader)
	if !ok {
		return base
	}
	return scopedReaderAdapter{DeviceReader: base, comparer: comparer, sections: sections}
}

// scopedReaderAdapter is a DeviceReader that also knows its vendor's
// managed scope. It embeds the plain reader, so Status and RunningConfig
// are unchanged — and, like everything else reachable from this package,
// it cannot write: scope.SectionReader is satisfied by the read-only eAPI
// client, whose only request primitive rejects non-`show` verbs.
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

// scopedConfigDiff maps a scope.Result onto the ConfigDiff the diff
// command and TUI already speak. Desired lines are "added", the device's
// current lines "removed", which is exactly the `- device / + desired`
// shape the body already carries.
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
