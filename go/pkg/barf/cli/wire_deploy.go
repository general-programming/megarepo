package cli

import (
	"context"
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// The write-side wiring, kept separate from wire.go so the read surface
// and the write surface can be reasoned about (and deleted) apart.

// init registers a cli-level writer factory for exactly the vendors
// whose row in the vendor table has a non-nil NewWriter.
//
// The loop is the point. There used to be a hand-written
// `registerWriter("vyos", wireVyOSWriter)` here — a fourth registry
// keyed by devicetype, in a fourth package, that a maintainer had to
// remember. Now "which vendors can be deployed to" is asked once, of the
// same table that answers "which vendors can be rendered/probed/scoped",
// and the answer cannot disagree with the constructor it names.
//
// writerFactories stays a package var: deploy_test.go swaps it for fakes
// and for the empty map (the "this build cannot deploy anything" case),
// and that seam is untouched.
func init() {
	for _, v := range vendor.All() {
		if v.NewWriter == nil {
			continue
		}
		registerWriter(v.Type, wireWriter)
	}
}

// wireWriter builds the writer for h from the vendor table.
//
// This is still the ONE place in barf that sets Options.AllowWrites.
// Everything upstream — the deploy command's --yes, its per-device
// confirmation, the dry-run default — gates whether this function is
// reached at all; this line is what lets the transport act once it is.
// vendor.NewWriter only chooses the constructor; the constructor still
// refuses if this flag is not set.
func wireWriter(h *model.Host, address string, s SecretSource) (DeviceWriter, error) {
	opts := device.Options{
		Endpoint: address,
		// Fleet devices serve the self-signed default SSL profile.
		InsecureSkipVerify: true,
		// The explicit opt-in. A writer cannot be built without it.
		AllowWrites: true,
		// Timeout is deliberately left zero: the transport then gives
		// each operation its own budget (120s for a commit, 60s for a
		// save), matching vyos_api.py. One client-wide value sized for a
		// read used to abort a slow commit mid-flight while the router
		// applied it anyway.
	}
	if src, ok := s.(*vaultSource); ok {
		opts.Secrets = src
		opts.GlobalSecrets = src
	}
	w, err := vendor.NewWriter(h, opts)
	if err != nil {
		return nil, err
	}
	return writerAdapter{w: w, hostname: h.Hostname}, nil
}

type writerAdapter struct {
	w        device.Writer
	hostname string
}

func (a writerAdapter) Configure(ctx context.Context, ops []ConfigOp) error {
	converted := make([]device.Op, 0, len(ops))
	for _, op := range ops {
		converted = append(converted, device.Op{Command: op.Command, Verb: op.Verb, Path: op.Path})
	}
	if err := a.w.Configure(ctx, converted); err != nil {
		return unsavedConfigError{host: a.hostname, err: err}
	}
	return nil
}

// unsavedConfigError says out loud what a bare "deploy failed" hides: a
// /configure that errors on this side is not proof the commit did not
// land. The HTTP request can be aborted (timeout, reset, proxy hiccup)
// after the router has already applied and committed the ops, and the
// deploy then skips SaveConfig — leaving the device running config that
// will not survive a reboot. The operator has to be told to go look.
type unsavedConfigError struct {
	host string
	err  error
}

func (e unsavedConfigError) Error() string {
	return fmt.Sprintf(
		"%v\n"+
			"    WARNING: %s may have applied this config anyway and it was NOT saved.\n"+
			"    A failed /configure does not prove the commit was rolled back; the boot\n"+
			"    config is untouched either way. Verify the device and re-run the deploy\n"+
			"    (or `save` on the router) before it reboots.",
		e.err, e.host)
}

func (e unsavedConfigError) Unwrap() error { return e.err }

func (a writerAdapter) SaveConfig(ctx context.Context) error {
	return a.w.SaveConfig(ctx)
}
