package cli

import (
	"context"
	"errors"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The interfaces below mirror the frozen contract in ../CONTRACT.md but
// are declared locally on purpose: Go interfaces are structural, so this
// package compiles and tests against fakes without importing the render,
// device or vault packages at all. The one file that names those
// packages is wire.go, which adapts them onto these types in init().

// SecretSource resolves per-host secrets. Mirrors render.SecretSource.
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

// Renderer turns a host into device config text. Mirrors render.Renderer.
type Renderer interface {
	Render(h *model.Host, n *model.Network, s SecretSource) (string, error)
}

// DeviceStatus mirrors device.Status.
type DeviceStatus struct {
	Version string
	Uptime  string
	Model   string
}

// DeviceReader is the read-only device surface. Mirrors device.Reader;
// nothing in this package may ever write to a device.
type DeviceReader interface {
	Status(ctx context.Context) (DeviceStatus, error)
	RunningConfig(ctx context.Context) (string, error)
}

// errNotWired is what the unwired defaults below return. It is a plain
// sentinel so `barf status` degrades to a per-device error cell instead
// of crashing when a backend is missing.
var errNotWired = errors.New("not wired: this barf build has no backend for it")

// The seams the commands actually call. wire.go replaces them with the
// real implementations in init(); tests replace them with fakes. Keeping
// them as vars means a missing backend is a link-time no-op rather than
// a compile error across the whole package.
var (
	// loadNetwork parses a network.yml.
	loadNetwork func(path string) (*model.Network, error) = func(string) (*model.Network, error) {
		return nil, errNotWired
	}

	// newSecrets builds the secret source (Vault in production).
	newSecrets func() (SecretSource, error) = func() (SecretSource, error) {
		return nil, errNotWired
	}

	// newRenderer returns the renderer for a device type.
	newRenderer func(deviceType string) (Renderer, error) = func(string) (Renderer, error) {
		return nil, errNotWired
	}

	// newReader returns a read-only client for a host at address.
	newReader func(h *model.Host, address string, s SecretSource) (DeviceReader, error) = func(*model.Host, string, SecretSource) (DeviceReader, error) {
		return nil, errNotWired
	}

	// reportsStatus reports whether `barf status` can probe a device
	// type at all (it needs version/uptime plus config retrieval).
	//
	// Unwired it answers false for everything, which is this seam's
	// version of errNotWired: nothing is selected, so nothing is
	// contacted. It used to carry a full copy of wire.go's vendor switch,
	// which init() then overwrote with the identical function — two
	// places to update to add a vendor, and no signal if only one was.
	reportsStatus func(deviceType string) bool = notWiredPredicate

	// isTemplatable reports whether a device type can be rendered.
	//
	// Unwired it answers false, for the same reason. Its old default
	// (`deviceType != "external"`) was not merely a duplicate of
	// wire.go's wireTemplatable but a *different answer*: wire.go asks
	// the render registry whether a renderer actually exists, while this
	// claimed everything except one hard-coded name was renderable. Since
	// init() always replaced it the two never had to agree, so the
	// fallback quietly encoded a rule nothing enforced.
	isTemplatable func(deviceType string) bool = notWiredPredicate
)

// notWiredPredicate is the false-for-everything default shared by the
// boolean seams above. They cannot return errNotWired the way the
// constructor seams do, so they answer "no device type qualifies", which
// makes an unwired binary select nothing rather than act on a guess.
func notWiredPredicate(string) bool { return false }
