package cli

import (
	"context"
	"fmt"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The write-side wiring, kept separate from wire.go so the read surface
// and the write surface can be reasoned about (and deleted) apart.
//
// This is the ONE place where Options.AllowWrites is set. Everything
// upstream of it — the deploy command's --yes, its per-device
// confirmation — gates whether this code is reached at all; this line is
// what lets the transport act once it is.

func init() {
	registerWriter("vyos", wireVyOSWriter)
}

func wireVyOSWriter(h *model.Host, address string, s SecretSource) (DeviceWriter, error) {
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
	w, err := device.NewVyOSWriter(h, opts)
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
