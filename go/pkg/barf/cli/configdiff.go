package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/vyosconfig"
)

// ConfigDiff mirrors the Python ConfigDiff dataclass.
type ConfigDiff struct {
	Added   []string
	Removed []string
	// Text is the printable body, "- " then "+ " prefixed.
	Text       string
	HasChanges bool
	// Summary is the one-line table cell, e.g. "+12 -2".
	Summary string
}

// secretKeywords are the keywords whose FOLLOWING value must never be printed;
// diff output is redacted by default (CONTRACT.md rule 5). Matching stays on
// the whole keyword: substrings make `disable-password-authentication` look
// like a secret, and blank `ro` in `snmp-server community <SECRET> ro`.
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

// redactLine hides the VALUE after a secret keyword, in both `keyword value`
// (IOS/VyOS) and `keyword=value` (Mikrotik) spellings. Key-aware, not
// position-aware: `snmp-server community <X> ro` ends in `ro`. Reachable only
// for readers that are neither VyOS nor scoped.
func redactLine(line string) string {
	var b strings.Builder
	b.Grow(len(line))

	// In `public-keys <name> key <pubkey>` the bare `key` node is PUBLIC and
	// must stay visible.
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

// normalizeKeyword strips quotes and punctuation so the bare word is compared.
func normalizeKeyword(token string) string {
	return strings.ToLower(strings.Trim(token, `"'`+"`:,"))
}

// normalizeConfig trims trailing whitespace and drops blanks and comments.
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
	ShowDeviceOnly bool
	ShowSecrets    bool
}

// DiffConfigs compares line SETS, as Python's vyos_config does: these configs
// are unordered `set ...` statements, so a positional diff reports spurious
// moves.
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
			b.WriteString("- " + maybeRedactSecretValue(line, opts) + "\n")
		}
	}
	for _, line := range added {
		b.WriteString("+ " + maybeRedactSecretValue(line, opts) + "\n")
	}
	d.Text = strings.TrimRight(b.String(), "\n")
	d.Summary = summarizeDiff(d)
	return d
}

// maybeRedactSecretValue hides secret VALUES. Not interchangeable with
// scope's maybeRedactHash, which hides `$6$` crypt material on EOS lines.
func maybeRedactSecretValue(line string, opts DiffOptions) string {
	if opts.ShowSecrets {
		return line
	}
	return redactLine(line)
}

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

// VyOS output is redacted by PATH SHAPE, not node name. vyosconfig.RedactPath
// decides on the parent alone (path[-2] in SecretNodes => hide path[-1]),
// which no name set can fix for these two:
//
//	set service https api keys id vaultadmin key '<FLEET API KEY>'
//	set system login user supertech authentication public-keys <n> key <PUBKEY>
//
// Both have parent `key`; the first must be hidden, the second is an SSH
// *public* key. The SNMP community fails the other way: parent `authorization`,
// secret not last. So match path prefixes, hide the next component, and fall
// back to vyosconfig.RedactPath for the node-name cases.
var vyosSecretShapes = []vyosconfig.Path{
	// "*" is the key id (vaultadmin).
	{"service", "https", "api", "keys", "id", "*", "key"},
	// `set service snmp community <C> authorization 'ro'`: the secret is
	// component 3, not the last one.
	{"service", "snmp", "community"},
}

// matchesShape reports whether path starts with shape ("*" matches any one
// component) AND has a further component after it: the value to hide.
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

// redactVyOSPath hides path's secret component: shape first, node name second.
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

// redactVyOSDiff copies diff with every path redacted, for FormatDiff.
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
