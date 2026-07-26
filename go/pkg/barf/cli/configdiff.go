package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/vyosconfig"
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

// secretKeywords are the config keywords whose FOLLOWING value must
// never be printed. Diff output is redacted by default (CONTRACT.md
// rule 5).
//
// Matching is on the whole keyword, never on a substring: the old
// substring test made `set service ssh disable-password-authentication`
// look like a secret, and — much worse — made the presence of the word
// anywhere in the line redact whatever token happened to be last. For
// `snmp-server community <SECRET> ro` that blanked `ro` and printed the
// community.
var secretKeywords = map[string]bool{
	"password":           true,
	"passphrase":         true,
	"wpa-passphrase":     true,
	"plaintext-password": true,
	"encrypted-password": true,
	"secret":             true,
	"psk":                true,
	"pre-shared-secret":  true,
	"pre-shared-key":     true,
	"preshared-key":      true,
	"private-key":        true,
	"private_key":        true,
	"auth-key":           true,
	"key":                true,
	"key-id":             true,
	"community":          true,
}

const redacted = "<redacted>"

// redactLine hides the VALUE that follows a secret keyword, in both the
// `keyword value` spelling (IOS/VyOS `set`-style) and the `keyword=value`
// spelling (a Mikrotik one-liner).
//
// It is deliberately key-aware rather than position-aware. A positional
// rule gets both real vendors backwards: `snmp-server community <X> ro`
// ends in `ro`, and `/interface/wireguard add private-key=<K>
// comment="x"` ends in the comment. Whitespace is preserved so a diff
// line still looks like the config it came from.
//
// Reachable today only for readers that are neither VyOS (which diffs as
// a path set, see vyosdiff.go) nor scoped (scope.go) — but a Mikrotik or
// linux reader landing here must not be the moment this is discovered.
func redactLine(line string) string {
	var b strings.Builder
	b.Grow(len(line))

	// `public-keys <name> key <pubkey>` is a PUBLIC key: the bare `key`
	// node in that shape is not a secret and Python has a test pinning
	// that it stays visible.
	sawPublic := false
	redactNext := false

	for i := 0; i < len(line); {
		start := i
		for i < len(line) && isSpace(line[i]) {
			i++
		}
		b.WriteString(line[start:i])
		if i >= len(line) {
			break
		}

		start = i
		for i < len(line) && !isSpace(line[i]) {
			i++
		}
		token := line[start:i]

		if redactNext {
			b.WriteString(redacted)
			redactNext = false
			continue
		}

		name, _, hasValue := strings.Cut(token, "=")
		keyword := normalizeKeyword(name)
		if strings.Contains(keyword, "public") {
			sawPublic = true
		}
		secret := secretKeywords[keyword] && !strings.Contains(keyword, "public") &&
			!(keyword == "key" && sawPublic)

		switch {
		case secret && hasValue:
			b.WriteString(name + "=" + redacted)
		case secret:
			b.WriteString(token)
			redactNext = true
		default:
			b.WriteString(token)
		}
	}
	return b.String()
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

// normalizeKeyword strips the decoration a keyword can pick up in real
// config text — quotes, a trailing colon — and lowercases it, so the
// comparison against secretKeywords is on the bare word.
func normalizeKeyword(token string) string {
	return strings.ToLower(strings.Trim(token, `"'`+"`:,"))
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

// -- VyOS path redaction ----------------------------------------------

// VyOS output is redacted by PATH SHAPE, not by node name.
//
// vyosconfig.RedactPath (and its Python original) decides on the parent
// node alone: if path[-2] is in SecretNodes, hide path[-1]. Two real
// fleet secrets slip through that test, and one of them cannot be fixed
// by adding a name to the set:
//
//	set service https api keys id vaultadmin key '<FLEET API KEY>'
//	set system login user supertech authentication public-keys <n> key <PUBKEY>
//
// Both have `key` as the parent node. The first is the fleet-wide VyOS
// API key and MUST be hidden; the second is an SSH *public* key and must
// stay visible (Python has a test pinning that). Only the shape of the
// path tells them apart. The SNMP community has the same problem from
// the other side: its parent is `authorization`, and the secret is not
// even the last component.
//
// So barf matches a table of path prefixes and hides the component that
// follows the match, falling back to vyosconfig.RedactPath for the
// node-name cases it already gets right.
//
// NOTE FOR RECONCILIATION: the shape table lives here rather than in
// vyosconfig because that package is owned elsewhere. If it moves, the
// natural home is an exported `SecretShapes []Path` consulted by
// RedactPath, and this file should then just call RedactPath again.
var vyosSecretShapes = []vyosconfig.Path{
	// The fleet-wide HTTPS API key. "*" is the key id (vaultadmin).
	{"service", "https", "api", "keys", "id", "*", "key"},
	// The SNMP community string: `set service snmp community <C>
	// authorization 'ro'` — the secret is component 3, not the last one.
	{"service", "snmp", "community"},
}

// matchesShape reports whether path starts with shape (where "*" matches
// any single component) AND has at least one more component after it —
// the value to hide.
func matchesShape(path, shape vyosconfig.Path) bool {
	if len(path) <= len(shape) {
		return false
	}
	for i, component := range shape {
		if component != "*" && path[i] != component {
			return false
		}
	}
	return true
}

// redactVyOSPath hides the secret component of path, by shape first and
// by vyosconfig's node-name table second.
func redactVyOSPath(path vyosconfig.Path) vyosconfig.Path {
	for _, shape := range vyosSecretShapes {
		if matchesShape(path, shape) {
			out := path.Clone()
			out[len(shape)] = vyosconfig.Redacted
			return out
		}
	}
	return vyosconfig.RedactPath(path)
}

// redactVyOSDiff returns a copy of diff with every path redacted, so the
// diff can be handed to vyosconfig.FormatDiff with redaction already
// applied. Formatting and ordering stay that package's business.
func redactVyOSDiff(diff vyosconfig.ConfigDiff) vyosconfig.ConfigDiff {
	out := vyosconfig.ConfigDiff{
		Added:   make([]vyosconfig.Path, len(diff.Added)),
		Removed: make([]vyosconfig.Path, len(diff.Removed)),
	}
	for i, path := range diff.Added {
		out.Added[i] = redactVyOSPath(path)
	}
	for i, path := range diff.Removed {
		out.Removed[i] = redactVyOSPath(path)
	}
	return out
}
