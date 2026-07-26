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
//	comparer, ok := vendor.Comparer(host.DeviceType)
//	result, err := comparer.Compare(ctx, scope.Input{...})
//
// `barf diff` dispatches on the vendor table: a devicetype with a nil
// Comparer gets the generic whole-config diff, a devicetype with one
// gets its scoped comparison. Adding a third vendor is a new file here
// plus one field in that vendor's existing row; no caller changes.
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
	// ShowSecrets disables redaction of secret material in the rendered
	// output. Only an explicit --show-secrets sets it.
	//
	// The polarity is deliberate and matches cli.DiffOptions.ShowSecrets
	// exactly, so no caller has to negate anything on the way in. It used
	// to be a `Redact bool` here against a `ShowSecrets bool` there, with
	// a lone `!` bridging them in cli/scopeddiff.go — two opposite senses
	// for one --show-secrets flag, where dropping the `!` would have
	// leaked secrets silently and read as a simplification. It also means
	// the zero value now redacts: an Input built without thinking about
	// this field is safe, where `Redact: false` was not.
	ShowSecrets bool
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

// The devicetype -> Comparer registry that used to live here has moved
// to ../vendor, so that a vendor's comparer sits in the same row as its
// renderer and its transports. The pattern this package was praised for
// -- a small consumer-side interface plus a registry -- is unchanged;
// only the registry's address is, and it is now shared with the three
// other registries that were keyed by the same string.
//
// A devicetype whose row has a nil Comparer has no managed slice, and
// `barf diff` falls back to comparing the whole config.

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
func buildResult(drift []Change, showSecrets bool) Result {
	var b strings.Builder
	for _, change := range drift {
		if change.Device != "" {
			b.WriteString("- " + maybeRedactHash(change.Device, showSecrets) + "\n")
		}
		b.WriteString("+ " + maybeRedactHash(change.Desired, showSecrets) + "\n")
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

// maybeRedactHash is named for what it hides. cli has its own
// maybeRedact* for VyOS `set` lines that hides the value after a secret
// keyword; the two redact genuinely different things and are not
// interchangeable, so neither is called just "maybeRedact" any more.
func maybeRedactHash(line string, showSecrets bool) string {
	if showSecrets {
		return line
	}
	return redact(line)
}
