package cli

import (
	"context"
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// The write-side wiring, kept separate from wire.go so the read and write
// surfaces can be reasoned about (and deleted) apart.

// init registers a writer factory for exactly the vendor table rows with a
// non-nil NewWriter. Derived, not hand-listed, so "which vendors can be
// deployed to" cannot disagree with the constructor it names.
func init() {
	for _, v := range vendor.All() {
		if v.NewWriter == nil {
			continue
		}
		registerWriter(v.Type, wireWriter)
	}
}

// wireWriter builds the writer for h from the vendor table. This is the ONE
// place in barf that sets Options.AllowWrites; everything upstream gates
// whether it is reached at all, and the constructor still refuses without it.
func wireWriter(h *model.Host, address string, s SecretSource) (DeviceWriter, error) {
	opts := device.Options{
		Endpoint: address,
		// Fleet devices serve the self-signed default SSL profile.
		InsecureSkipVerify: true,
		// The explicit opt-in. A writer cannot be built without it.
		AllowWrites: true,
		// Zero so the transport gives each operation its own budget (120s
		// commit, 60s save) as vyos_api.py does: one client-wide value sized
		// for a read aborts a slow commit while the router applies it anyway.
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

// unsavedConfigError says what a bare "deploy failed" hides: a /configure
// erroring on this side is not proof the commit did not land, and the deploy
// then skips SaveConfig, leaving config that will not survive a reboot.
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
