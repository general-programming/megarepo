package cli

import (
	"context"
	"errors"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The interfaces below mirror ../CONTRACT.md but are declared locally so this
// package compiles and tests against fakes without importing render, device
// or vault. wire.go adapts the real types onto these in init().

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

// DeviceReader mirrors device.Reader; nothing here may ever write to a device.
type DeviceReader interface {
	Status(ctx context.Context) (DeviceStatus, error)
	RunningConfig(ctx context.Context) (string, error)
}

// errNotWired is a plain sentinel, so a missing backend degrades to a
// per-device error cell instead of crashing.
var errNotWired = errors.New("not wired: this barf build has no backend for it")

// The seams the commands call. wire.go replaces them with the real
// implementations in init(); tests replace them with fakes.
var (
	loadNetwork func(path string) (*model.Network, error) = func(string) (*model.Network, error) {
		return nil, errNotWired
	}

	// newSecrets builds the secret source (Vault in production).
	newSecrets func() (SecretSource, error) = func() (SecretSource, error) {
		return nil, errNotWired
	}

	newRenderer func(deviceType string) (Renderer, error) = func(string) (Renderer, error) {
		return nil, errNotWired
	}

	newReader func(h *model.Host, address string, s SecretSource) (DeviceReader, error) = func(*model.Host, string, SecretSource) (DeviceReader, error) {
		return nil, errNotWired
	}

	// reportsStatus needs version/uptime plus config retrieval.
	reportsStatus func(deviceType string) bool = notWiredPredicate

	isTemplatable func(deviceType string) bool = notWiredPredicate

	// mintHostSecret is barf's one opt-in Vault WRITE path: `barf secrets
	// mint` (secrets.go) is the only caller. Every other command's
	// SecretSource (newSecrets) is read-only on purpose (see wireSecrets);
	// this seam exists so that stays true even though something still has
	// to create a brand new host's or WG link's first secret.
	mintHostSecret func(hostname, key string) (string, error) = func(string, string) (string, error) {
		return "", errNotWired
	}
)

// notWiredPredicate is errNotWired for the boolean seams: an unwired binary
// selects nothing rather than guessing.
func notWiredPredicate(string) bool { return false }
