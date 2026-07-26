// Package scope compares a device against ONLY the slice of its config
// that barf manages.
//
// Some vendors are adopted whole (VyOS: barf owns the entire config, so a
// line-set diff of render output against the running config is correct).
// Others are adopted one slice at a time — Arista EOS is the first: barf
// owns the `admin` user, its ssh-keys, the enable password and the eAPI
// block, and nothing else. Diffing a managed *slice* against a full
// running-config dump is meaningless (it reports every unmanaged line as
// a removal), so those vendors need a scoped comparison instead.
//
// This package is that comparison, per vendor, behind one interface:
//
//	comparer, ok := scope.For(host.DeviceType)
//	result, err := comparer.Compare(ctx, scope.Input{...})
//
// `barf diff` dispatches on the registry: a devicetype with no entry gets
// the generic whole-config diff, a devicetype with one gets its scoped
// comparison. Adding a third vendor is a new file plus a registry line;
// no caller changes.
//
// Nothing here writes to a device. The Reader it takes is a read-only
// section fetch, structurally satisfied by *device.EOSReader.
//
// Port of the scoped diff half of projects/barf/barf/vendors/arista.py
// (`_device_managed_state`, `_parse_eapi_section`, `_drift`,
// `_hash_matches`, `diff_config`).
package scope

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// SecretSource resolves per-host secrets. Mirrors render.SecretSource /
// device.Secrets; declared locally so this package depends on neither
// Vault nor the CLI.
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

// SectionReader fetches named `show running-config all section <name>`
// blocks. Read-only by construction: *device.EOSReader satisfies it, and
// its implementation cannot send anything but `show`.
type SectionReader interface {
	RunningConfigSections(ctx context.Context, names ...string) (string, error)
}

// Input is everything a scoped comparison needs: the desired state comes
// from the host plus Secrets, the actual state from Reader.
type Input struct {
	// Host is the device being compared.
	Host *model.Host
	// Network is the parsed network.yml; vendors read global metadata
	// (ssh keys) from it. Mirrors the Python `global_meta` that
	// load_network() attaches to each EosHost.
	Network *model.Network
	// Secrets resolves the managed credentials.
	Secrets SecretSource
	// Reader fetches the device's current managed-scope config.
	Reader SectionReader
	// Redact hides secret material in the rendered output. Callers pass
	// false only for an explicit --show-secrets.
	Redact bool
}

// Change is one out-of-sync managed item.
type Change struct {
	// Device is the line the device currently has, or "" when the item
	// is absent from the device entirely.
	Device string
	// Desired is the line barf would send.
	Desired string
}

// Result is a scoped comparison, shaped like the Python DeployDiff.
type Result struct {
	// Drift is every out-of-sync managed item, in a stable order.
	Drift []Change
	// Text is the printable body: `- device line` then `+ desired line`.
	Text string
	// HasChanges is whether deploying would change the device.
	HasChanges bool
	// Summary is the one-line table cell.
	Summary string
}

// Comparer compares one vendor's managed scope.
type Comparer interface {
	Compare(ctx context.Context, in Input) (Result, error)
}

// comparers is the devicetype -> Comparer registry. A devicetype absent
// here has no managed slice, and `barf diff` falls back to comparing the
// whole config.
var comparers = map[string]Comparer{
	"eos": EOS{},
}

// For returns the scoped comparer registered for a device type.
func For(deviceType string) (Comparer, bool) {
	c, ok := comparers[strings.ToLower(deviceType)]
	return c, ok
}

// Compare runs the scoped comparison registered for in.Host's devicetype.
func Compare(ctx context.Context, in Input) (Result, error) {
	if in.Host == nil {
		return Result{}, fmt.Errorf("scope: nil host")
	}
	comparer, ok := For(in.Host.DeviceType)
	if !ok {
		return Result{}, fmt.Errorf("scope: %s: no scoped comparison for device type %q",
			in.Host.Hostname, in.Host.DeviceType)
	}
	return comparer.Compare(ctx, in)
}

// RedactedHash is what a `$6$...` crypt hash is replaced with in output.
// Matches the Python `_REDACTED`.
const RedactedHash = "<hash-redacted>"

var hashPattern = regexp.MustCompile(`\$6\$\S+`)

// redact hides sha512-crypt material. The managed lines carry exactly one
// kind of secret (the crypt hash); ssh public keys are, by definition,
// public.
func redact(line string) string {
	return hashPattern.ReplaceAllString(line, RedactedHash)
}

// buildResult turns drift into the `- device / + desired` body and summary
// that every scoped comparer returns.
func buildResult(drift []Change, doRedact bool) Result {
	var b strings.Builder
	for _, change := range drift {
		if change.Device != "" {
			b.WriteString("- " + maybeRedact(change.Device, doRedact) + "\n")
		}
		b.WriteString("+ " + maybeRedact(change.Desired, doRedact) + "\n")
	}

	summary := "no changes"
	if len(drift) > 0 {
		summary = fmt.Sprintf("%d managed item(s) drifted", len(drift))
	}
	return Result{
		Drift:      drift,
		Text:       strings.TrimRight(b.String(), "\n"),
		HasChanges: len(drift) > 0,
		Summary:    summary,
	}
}

func maybeRedact(line string, doRedact bool) string {
	if !doRedact {
		return line
	}
	return redact(line)
}
