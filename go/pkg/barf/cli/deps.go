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
	reportsStatus func(deviceType string) bool = func(deviceType string) bool {
		switch deviceType {
		case "vyos", "eos":
			return true
		}
		return false
	}

	// isTemplatable reports whether a device type can be rendered.
	isTemplatable func(deviceType string) bool = func(deviceType string) bool {
		return deviceType != "external"
	}
)
