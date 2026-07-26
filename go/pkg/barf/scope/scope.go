// Package scope compares a device against ONLY the slice of its config that
// barf manages. Some vendors are adopted whole (VyOS: a line-set diff against
// the running config is correct); others a slice at a time, where diffing
// against a full running-config dump would report every unmanaged line as a
// removal. Those go through vendor.Comparer(deviceType); a nil Comparer in
// the vendor table means the generic whole-config diff.
//
// Nothing here writes to a device: Reader is a read-only section fetch.
//
// Port of the scoped diff half of projects/barf/barf/vendors/arista.py.
package scope

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// SecretSource resolves per-host secrets. Declared locally so this package
// depends on neither Vault nor the CLI.
type SecretSource interface {
	HostSecret(hostname, key string) (string, error)
}

// SectionReader fetches named `show running-config all section <name>` blocks.
// Read-only by construction: implementations cannot send anything but `show`.
type SectionReader interface {
	RunningConfigSections(ctx context.Context, names ...string) (string, error)
}

// Input is everything a scoped comparison needs: the desired state comes
// from the host plus Secrets, the actual state from Reader.
type Input struct {
	Host *model.Host
	// Network is the parsed network.yml; vendors read global metadata (ssh
	// keys) from it. Python's `global_meta`.
	Network *model.Network
	Secrets SecretSource
	Reader  SectionReader
	// ShowSecrets disables redaction. The polarity is deliberate: it matches
	// cli.DiffOptions.ShowSecrets, and the zero value redacts.
	ShowSecrets bool
}

// Change is one out-of-sync managed item.
type Change struct {
	// Device is the line the device currently has, "" when absent entirely.
	Device string
	// Desired is the line barf would send.
	Desired string
}

// Result is a scoped comparison, shaped like the Python DeployDiff.
type Result struct {
	// Drift is every out-of-sync managed item, in a stable order.
	Drift []Change
	// Text is the printable body: `- device line` then `+ desired line`.
	Text       string
	HasChanges bool
	// Summary is the one-line table cell.
	Summary string
}

// Comparer compares one vendor's managed scope.
type Comparer interface {
	Compare(ctx context.Context, in Input) (Result, error)
}

// RedactedHash replaces `$6$...` crypt hashes in output. Python `_REDACTED`.
const RedactedHash = "<hash-redacted>"

var hashPattern = regexp.MustCompile(`\$6\$\S+`)

// redact hides sha512-crypt material, the only secret the managed lines carry.
func redact(line string) string {
	return hashPattern.ReplaceAllString(line, RedactedHash)
}

// buildResult turns drift into the `- device / + desired` body and summary.
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

// maybeRedactHash is named for what it hides: cli's maybeRedact* hides the
// value after a secret keyword in VyOS `set` lines, and is not interchangeable.
func maybeRedactHash(line string, showSecrets bool) string {
	if showSecrets {
		return line
	}
	return redact(line)
}
