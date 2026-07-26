package cli

import (
	"context"
	"fmt"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The write-side seams, kept in their own file alongside the read-only
// ones in deps.go. Same rule applies: this package talks to local
// interfaces and never names the device package outside wire*.go, so the
// deploy command is testable against fakes with no transport in sight.

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

// DeviceWriter is the write surface. It is deliberately a separate
// interface from DeviceReader: nothing that only has a reader can write.
// Obtaining one requires newWriterFor, which requires a registered
// vendor implementation, which itself requires an explicit opt-in down in
// the device package.
type DeviceWriter interface {
	// Configure applies ops as one atomic change.
	Configure(ctx context.Context, ops []ConfigOp) error
	// SaveConfig persists the running config to the boot config.
	SaveConfig(ctx context.Context) error
}

// writerFactory builds a writer for a host already probed to address.
type writerFactory func(h *model.Host, address string, s SecretSource) (DeviceWriter, error)

// writerFactories holds the vendors that have a write implementation, by
// devicetype. It starts empty on purpose: a build with no wiring can
// diff and plan but physically cannot deploy. wire_deploy.go fills it
// from the rows of the vendor table whose NewWriter is non-nil, so this
// map is derived from that table rather than being a fourth registry
// maintained beside it.
var writerFactories = map[string]writerFactory{}

// registerWriter records a vendor's write implementation. Called from
// init() in the wiring files.
func registerWriter(deviceType string, f writerFactory) {
	writerFactories[deviceType] = f
}

// hasWriter reports whether deploying deviceType is implemented at all.
func hasWriter(deviceType string) bool {
	_, ok := writerFactories[deviceType]
	return ok
}

// deployableDeviceTypes lists the vendors that can be deployed, for error
// messages.
func deployableDeviceTypes() []string {
	types := make([]string, 0, len(writerFactories))
	for name := range writerFactories {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
}

// newWriterFor builds the writer for h, or explains why it cannot.
func newWriterFor(h *model.Host, address string, s SecretSource) (DeviceWriter, error) {
	factory, ok := writerFactories[h.DeviceType]
	if !ok {
		return nil, fmt.Errorf("no write implementation for devicetype %q (deployable: %v)",
			h.DeviceType, deployableDeviceTypes())
	}
	return factory(h, address, s)
}
