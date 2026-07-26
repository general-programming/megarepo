package cli

import (
	"fmt"
	"sort"
	"strings"
)

// ConfigDiff is the difference between a rendered config and the config
// a device is running. It mirrors the Python ConfigDiff dataclass.
type ConfigDiff struct {
	// Added are lines the render has that the device does not.
	Added []string
	// Removed are lines the device has that the render does not.
	Removed []string
	// Text is the printable body, "- " then "+ " prefixed.
	Text string
	// HasChanges is whether deploying would change the device.
	HasChanges bool
	// Summary is the one-line table cell, e.g. "+12 -2".
	Summary string
}

// secretTokens are the config keywords whose *value* must never be
// printed. Diff output is redacted by default (CONTRACT.md rule 5).
var secretTokens = []string{
	"private-key",
	"pre-shared-secret",
	"preshared-key",
	"password",
	"secret",
	"psk",
	"key-id",
	"authentication",
	"community",
	"snmp",
}

const redacted = "<redacted>"

// redactLine hides the last whitespace-separated token of a line whose
// body mentions a secret keyword. Set-style configs put the value last
// (`set interfaces wireguard wg0 private-key XXX`), so this is enough to
// keep secrets out of terminals and CI logs.
func redactLine(line string) string {
	lower := strings.ToLower(line)
	for _, token := range secretTokens {
		if !strings.Contains(lower, token) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return line
		}
		// Keep the quoting style simple: replace the trailing value.
		idx := strings.LastIndex(line, fields[len(fields)-1])
		return line[:idx] + redacted
	}
	return line
}

// normalizeConfig splits a config into comparable lines: trailing
// whitespace trimmed, blank lines and comments dropped.
func normalizeConfig(config string) []string {
	var out []string
	for _, raw := range strings.Split(config, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// DiffOptions tunes what the diff body shows.
type DiffOptions struct {
	// ShowDeviceOnly also prints config that only exists on the device.
	ShowDeviceOnly bool
	// ShowSecrets disables redaction of secret values.
	ShowSecrets bool
}

// DiffConfigs compares a rendered config against a device's running
// config.
//
// The comparison is line-set based, matching the Python vyos_config
// implementation: these configs are unordered sets of `set ...` style
// statements, so a positional diff would report spurious moves.
func DiffConfigs(rendered, running string, opts DiffOptions) ConfigDiff {
	want := normalizeConfig(rendered)
	have := normalizeConfig(running)

	haveSet := make(map[string]bool, len(have))
	for _, line := range have {
		haveSet[line] = true
	}
	wantSet := make(map[string]bool, len(want))
	for _, line := range want {
		wantSet[line] = true
	}

	var added, removed []string
	for _, line := range want {
		if !haveSet[line] {
			added = append(added, line)
		}
	}
	for _, line := range have {
		if !wantSet[line] {
			removed = append(removed, line)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	d := ConfigDiff{
		Added:      added,
		Removed:    removed,
		HasChanges: len(added) > 0 || len(removed) > 0,
	}

	var b strings.Builder
	if opts.ShowDeviceOnly {
		for _, line := range removed {
			b.WriteString("- " + maybeRedact(line, opts) + "\n")
		}
	}
	for _, line := range added {
		b.WriteString("+ " + maybeRedact(line, opts) + "\n")
	}
	d.Text = strings.TrimRight(b.String(), "\n")
	d.Summary = summarizeDiff(d)
	return d
}

func maybeRedact(line string, opts DiffOptions) string {
	if opts.ShowSecrets {
		return line
	}
	return redactLine(line)
}

// summarizeDiff is the one-line table cell, e.g. "+12 -2".
func summarizeDiff(d ConfigDiff) string {
	if !d.HasChanges {
		return "no changes"
	}
	parts := []string{fmt.Sprintf("+%d", len(d.Added))}
	if len(d.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("-%d", len(d.Removed)))
	}
	return strings.Join(parts, " ")
}
