// Package vyosconfig parses and diffs VyOS configuration as path sets.
//
// It is a direct port of projects/barf/barf/util/vyos_config.py. Both the
// rendered templates (flat `set ...` command lists) and the HTTPS API's
// `/retrieve` JSON tree are normalized into one canonical representation:
// a set of path tuples, one per `set` command. Diffing is then plain set
// arithmetic, done entirely on this machine — no config session is ever
// opened on the device.
//
// Ownership is total: the rendered config is the full truth, and any
// device path the candidate does not render becomes a real deletion. The
// only exception is the "ignored" path prefixes a vendor declares (see
// IgnoredPaths), which are dropped from the diff entirely — pure hardware
// facts like `hw-id` that are neither intent nor deletable.
//
// This package computes; it never talks to a device.
package vyosconfig

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Path is one config path: the tokens of a `set` command, minus the verb.
type Path []string

// Key is Path's map key. Components never contain NUL, so the encoding is
// injective, and byte-ordering it matches Python's element-wise tuple
// ordering (NUL sorts below every other byte).
func (p Path) Key() string { return strings.Join(p, "\x00") }

// Clone returns an independent copy, so callers cannot alias a Set's
// internals.
func (p Path) Clone() Path { return slices.Clone(p) }

// String renders the path as the argument list of a `set` command, with
// Python `shlex.quote` semantics.
func (p Path) String() string {
	parts := make([]string, len(p))
	for i, component := range p {
		parts[i] = shlexQuote(component)
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

// Set is an unordered set of config paths — the canonical form both a
// rendered config and a device's JSON config tree collapse to.
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

// SecretNodes are the path components whose immediate child value is a
// secret. The value is still diffed, only the display is redacted.
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

// IgnoredPaths is the VyOS vendor's ignored prefix list, mirroring
// `VyOSHost.IGNORED_PATHS`. A "*" component matches any single path
// element. hw-id is a recorded hardware fact, not intent — MAC addresses
// change when VMs are reprovisioned — so it is dropped from diffs
// entirely: never deleted, never listed, never counted.
var IgnoredPaths = []Path{{"interfaces", "ethernet", "*", "hw-id"}}

// ParseSetCommands parses rendered config text into a set of paths.
//
// Only `set ...` lines are considered; blanks, comments, and
// `delete`/op-mode lines are ignored. Values are shlex-tokenized so the
// templates' inconsistent quoting normalizes to the same path.
func ParseSetCommands(text string) Set {
	paths := Set{}
	for _, raw := range splitLines(text) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "set ") {
			continue
		}
		tokens, err := shlexSplit(line)
		if err != nil {
			// Unbalanced quotes; keep the raw split rather than dying.
			tokens = strings.Fields(line)
		}
		if len(tokens) < 2 {
			// "set" with no arguments yields the empty path, exactly as
			// Python's tuple(tokens[1:]) does.
			paths.Add(Path{})
			continue
		}
		paths.Add(Path(tokens[1:]))
	}
	return paths
}

// PathsFromAPIJSON flattens the `/retrieve` showConfig JSON tree into
// paths.
//
// Leaves come back as a string (single value), a list of strings
// (multi-value node), or an empty object (valueless node); each becomes
// the same path its `set` command would produce.
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

// scalarString is Python's str() for the scalar types encoding/json can
// produce. VyOS only ever sends strings here; the rest are defensive.
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

// ConfigDiff is the result of diffing a running config against a
// candidate.
type ConfigDiff struct {
	// Added are the paths the deploy would create.
	Added []Path
	// Removed are the device paths the deploy deletes: stale config barf
	// did not render and does not ignore.
	Removed []Path
}

// HasChanges reports whether deploying would change the device.
func (d ConfigDiff) HasChanges() bool { return len(d.Added) > 0 || len(d.Removed) > 0 }

// isIgnored reports whether path falls under an ignored prefix. A "*"
// component in a prefix matches any single path component, e.g.
// ("interfaces","ethernet","*","hw-id") ignores the hw-id of every
// ethernet interface.
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

// DiffPaths diffs two path sets; the candidate owns everything not
// ignored.
//
// Ownership is total: every device path the candidate does not render is
// a real deletion (Removed). Paths under an ignored prefix are the sole
// exception — dropped from the diff entirely, never deleted, never
// listed, never counted. Supports "*" wildcard components.
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

// MinimalDeletePaths collapses removed leaf paths into the fewest delete
// commands.
//
// A prefix is deleted wholly when every running path beneath it is being
// removed (deleting the node also clears empty parents that per-leaf
// deletes would leave behind). This decides what is deleted from a live
// router: a prefix is only ever chosen when the entire running subtree
// under it is in the removal set.
func MinimalDeletePaths(removed []Path, running Set) []Path {
	removedSet := NewSet(removed...)

	deletes := Set{}
	for _, path := range removedSet.Sorted() {
		chosen := path
		for i := 1; i < len(path); i++ {
			prefix := path[:i]

			// Every running path under prefix must be going away, and
			// there must be at least one (an empty subtree means the
			// prefix names nothing the device has).
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

// RedactPath returns a copy of path with its value hidden when it sits
// under a secret node.
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

// FormatDiff renders a ConfigDiff as +/- `set` lines. With redact set,
// secret values (private keys, PSKs, passwords) are replaced by a
// placeholder in the output — the diff itself is unaffected.
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
