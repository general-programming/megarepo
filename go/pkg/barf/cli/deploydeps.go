package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The write-side seams, alongside the read-only ones in deps.go: local
// interfaces only, the device package named nowhere outside wire*.go.

// ConfigOp mirrors device.Op: one configuration operation. EOS reads
// Command; VyOS reads Verb ("set"/"delete") plus Path.
type ConfigOp struct {
	Command string
	Verb    string
	Path    []string
}

// VyOS op verbs, mirroring device.OpSet / device.OpDelete.
const (
	opSet    = "set"
	opDelete = "delete"
)

// DeviceWriter is deliberately separate from DeviceReader so nothing holding
// only a reader can write. Obtaining one goes through newWriterFor, i.e. an
// explicit opt-in in the device package.
type DeviceWriter interface {
	// Configure applies ops as one atomic change.
	Configure(ctx context.Context, ops []ConfigOp) error
	// SaveConfig persists the running config to the boot config.
	SaveConfig(ctx context.Context) error
}

// writerFactory builds a writer for a host already probed to address.
type writerFactory func(h *model.Host, address string, s SecretSource) (DeviceWriter, error)

// writerFactories is empty on purpose: an unwired build can diff and plan but
// physically cannot deploy. wire_deploy.go fills it from the vendor table
// rows whose NewWriter is non-nil.
var writerFactories = map[string]writerFactory{}

func registerWriter(deviceType string, f writerFactory) {
	writerFactories[deviceType] = f
}

func hasWriter(deviceType string) bool {
	_, ok := writerFactories[deviceType]
	return ok
}

func deployableDeviceTypes() []string {
	types := make([]string, 0, len(writerFactories))
	for name := range writerFactories {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

func newWriterFor(h *model.Host, address string, s SecretSource) (DeviceWriter, error) {
	factory, ok := writerFactories[h.DeviceType]
	if !ok {
		return nil, fmt.Errorf("no write implementation for devicetype %q (deployable: %v)",
			h.DeviceType, deployableDeviceTypes())
	}
	return factory(h, address, s)
}
