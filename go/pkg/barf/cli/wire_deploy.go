package cli

import (
	"context"

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
	}
	if src, ok := s.(*vaultSource); ok {
		opts.Secrets = src
		opts.GlobalSecrets = src
	}
	w, err := device.NewVyOSWriter(h, opts)
	if err != nil {
		return nil, err
	}
	return writerAdapter{w}, nil
}

type writerAdapter struct{ w device.Writer }

func (a writerAdapter) Configure(ctx context.Context, ops []ConfigOp) error {
	converted := make([]device.Op, 0, len(ops))
	for _, op := range ops {
		converted = append(converted, device.Op{Command: op.Command, Verb: op.Verb, Path: op.Path})
	}
	return a.w.Configure(ctx, converted)
}

func (a writerAdapter) SaveConfig(ctx context.Context) error {
	return a.w.SaveConfig(ctx)
}
