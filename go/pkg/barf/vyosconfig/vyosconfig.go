// Package vyosconfig parses and diffs VyOS configuration as path sets, a
// port of barf/util/vyos_config.py. Rendered templates and the API's
// `/retrieve` JSON tree both normalize to one set of path tuples, so diffing
// is local set arithmetic; nothing here talks to a device. Ownership is
// total: any device path the candidate does not render is a real deletion,
// the sole exception being IgnoredPaths.
package vyosconfig

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/general-programming/megarepo/go/common/pytext"
)

// Path is one config path: the tokens of a `set` command, minus the verb.
type Path []string

// Key is Path's map key. Components never contain NUL, so it is injective
// and byte-ordering matches Python's element-wise tuple order.
func (p Path) Key() string { return strings.Join(p, "\x00") }

// Clone returns an independent copy, so callers cannot alias a Set.
func (p Path) Clone() Path { return slices.Clone(p) }

// String renders the path as a `set` command's arguments, shlex-quoted.
func (p Path) String() string {
	parts := make([]string, len(p))
	for i, component := range p {
		parts[i] = pytext.ShellQuote(component)
	}
	return strings.Join(parts, " ")
}

// HasPrefix reports whether prefix is a leading slice of p.
func (p Path) HasPrefix(prefix Path) bool {
	if len(p) < len(prefix) {
		return false
	}
	return slices.Equal(p[:len(prefix)], prefix)
}

// Set is an unordered set of config paths, the canonical form both sides of
// a diff collapse to.
type Set map[string]Path

// NewSet builds a Set from paths.
func NewSet(paths ...Path) Set {
	s := make(Set, len(paths))
	for _, p := range paths {
		s.Add(p)
	}
	return s
}

// Add inserts p (a copy of it).
func (s Set) Add(p Path) { s[p.Key()] = p.Clone() }

// Has reports membership.
func (s Set) Has(p Path) bool { _, ok := s[p.Key()]; return ok }

// Discard removes p if present; removing an absent path is not an error.
func (s Set) Discard(p Path) { delete(s, p.Key()) }

// Clone returns an independent copy of the set.
func (s Set) Clone() Set {
	out := make(Set, len(s))
	for k, v := range s {
		out[k] = v.Clone()
	}
	return out
}

// Sorted returns the set's paths in Python tuple order.
func (s Set) Sorted() []Path {
	out := make([]Path, 0, len(s))
	for _, p := range s {
		out = append(out, p)
	}
	sortPaths(out)
	return out
}

func sortPaths(paths []Path) {
	slices.SortFunc(paths, func(a, b Path) int { return slices.Compare(a, b) })
}

// SecretNodes are components whose immediate child value is a secret. The
// value is still diffed; only the display is redacted.
var SecretNodes = map[string]bool{
	"private-key":        true,
	"secret":             true,
	"password":           true,
	"passphrase":         true,
	"pre-shared-secret":  true,
	"plaintext-password": true,
	"encrypted-password": true,
}

// Redacted is the placeholder substituted for secret values.
const Redacted = "<redacted>"

// IgnoredPaths mirrors `VyOSHost.IGNORED_PATHS`; "*" matches any single
// element. hw-id is a hardware fact, not intent, so it is never deleted,
// listed or counted.
var IgnoredPaths = []Path{{"interfaces", "ethernet", "*", "hw-id"}}

// ParseSetCommands parses rendered config text into paths. Only `set ...`
// lines count; shlex tokenization normalizes the templates' quoting.
func ParseSetCommands(text string) Set {
	paths := Set{}
	for _, raw := range pytext.SplitLines(text) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "set ") {
			continue
		}
		tokens, err := pytext.ShellSplit(line)
		if err != nil {
			// Unbalanced quotes; keep the raw split rather than dying.
			tokens = strings.Fields(line)
		}
		if len(tokens) < 2 {
			// A bare "set" yields the empty path, as tuple(tokens[1:]) does.
			paths.Add(Path{})
			continue
		}
		paths.Add(Path(tokens[1:]))
	}
	return paths
}

// PathsFromAPIJSON flattens the `/retrieve` showConfig JSON tree into paths.
// Leaves are a string, a list (multi-value) or an empty object (valueless).
func PathsFromAPIJSON(data any) Set {
	paths := Set{}
	var walk func(node any, prefix Path)
	walk = func(node any, prefix Path) {
		switch n := node.(type) {
		case map[string]any:
			if len(n) == 0 {
				if len(prefix) > 0 {
					paths.Add(prefix)
				}
				return
			}
			for key, child := range n {
				walk(child, append(prefix.Clone(), key))
			}
		case []any:
			for _, value := range n {
				paths.Add(append(prefix.Clone(), scalarString(value)))
			}
		default:
			paths.Add(append(prefix.Clone(), scalarString(node)))
		}
	}
	walk(data, Path{})
	return paths
}

// scalarString is Python's str() for encoding/json's scalar types. VyOS
// only ever sends strings; the rest are defensive.
func scalarString(v any) string {
	switch n := v.(type) {
	case string:
		return n
	case json.Number:
		return n.String()
	case float64:
		return strconv.FormatFloat(n, 'g', -1, 64)
	case bool:
		if n {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	default:
		return fmt.Sprint(n)
	}
}

// ConfigDiff is the result of diffing a running config against a candidate.
type ConfigDiff struct {
	Added []Path
	// Removed are device paths the deploy deletes: stale config barf neither
	// rendered nor ignores.
	Removed []Path
}

// HasChanges reports whether deploying would change the device.
func (d ConfigDiff) HasChanges() bool { return len(d.Added) > 0 || len(d.Removed) > 0 }

// isIgnored reports whether path falls under an ignored prefix ("*" matches
// any single component).
func isIgnored(path Path, ignored []Path) bool {
	for _, prefix := range ignored {
		if len(path) < len(prefix) {
			continue
		}
		match := true
		for i, component := range prefix {
			if component != "*" && component != path[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// DiffPaths diffs two path sets. Ownership is total: every device path the
// candidate does not render is a real deletion. Paths under an ignored
// prefix are the sole exception, dropped from the diff entirely.
func DiffPaths(running, candidate Set, ignored []Path) ConfigDiff {
	added := []Path{}
	for key, p := range candidate {
		if _, ok := running[key]; !ok {
			added = append(added, p.Clone())
		}
	}
	stale := []Path{}
	for key, p := range running {
		if _, ok := candidate[key]; !ok {
			stale = append(stale, p.Clone())
		}
	}
	sortPaths(added)
	sortPaths(stale)

	removed := []Path{}
	for _, p := range stale {
		if !isIgnored(p, ignored) {
			removed = append(removed, p)
		}
	}
	return ConfigDiff{Added: added, Removed: removed}
}

// MinimalDeletePaths collapses removed leaves into the fewest delete
// commands, deciding what a live router loses: a prefix is chosen only when
// the whole running subtree under it is being removed (which also clears
// empty parents).
func MinimalDeletePaths(removed []Path, running Set) []Path {
	removedSet := NewSet(removed...)

	deletes := Set{}
	for _, path := range removedSet.Sorted() {
		chosen := path
		for i := 1; i < len(path); i++ {
			prefix := path[:i]

			// Every running path under prefix must be going away, and there
			// must be at least one: an empty subtree names nothing.
			subtreeEmpty := true
			whollyRemoved := true
			for _, r := range running {
				if !r.HasPrefix(prefix) {
					continue
				}
				subtreeEmpty = false
				if !removedSet.Has(r) {
					whollyRemoved = false
					break
				}
			}
			if !subtreeEmpty && whollyRemoved {
				chosen = prefix
				break
			}
		}
		deletes.Add(chosen)
	}
	return deletes.Sorted()
}

// RedactPath copies path, hiding its value when it sits under a SecretNode.
func RedactPath(path Path) Path {
	if len(path) > 1 && SecretNodes[path[len(path)-2]] {
		out := path.Clone()
		out[len(out)-1] = Redacted
		return out
	}
	return path.Clone()
}

func formatPath(path Path, redact bool) string {
	if redact {
		path = RedactPath(path)
	}
	return path.String()
}

// FormatDiff renders a ConfigDiff as +/- `set` lines. redact replaces
// secret values in the output only; the diff itself is unaffected.
func FormatDiff(diff ConfigDiff, redact bool) string {
	lines := make([]string, 0, len(diff.Removed)+len(diff.Added))
	for _, path := range diff.Removed {
		lines = append(lines, "- set "+formatPath(path, redact))
	}
	for _, path := range diff.Added {
		lines = append(lines, "+ set "+formatPath(path, redact))
	}
	return strings.Join(lines, "\n")
}

// SummarizeDiff is a one-line human summary, e.g. "+12 -2".
func SummarizeDiff(diff ConfigDiff) string {
	if !diff.HasChanges() {
		return "no changes"
	}
	parts := []string{fmt.Sprintf("+%d", len(diff.Added))}
	if len(diff.Removed) > 0 {
		parts = append(parts, fmt.Sprintf("-%d", len(diff.Removed)))
	}
	return strings.Join(parts, " ")
}
