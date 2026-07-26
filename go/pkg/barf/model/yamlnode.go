package model

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// entry is one key/value pair of a YAML mapping, in document order.
type entry struct {
	key string
	val *yaml.Node
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// mapEntries flattens a mapping into ordered key/value pairs, resolving `<<`
// as PyYAML does: merged entries come first, and an explicit key overrides the
// merged value while keeping the merged key's position.
func mapEntries(n *yaml.Node) []entry {
	n = deref(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}

	var ordered []entry
	index := map[string]int{}
	add := func(e entry) {
		if at, ok := index[e.key]; ok {
			ordered[at] = e
			return
		}
		index[e.key] = len(ordered)
		ordered = append(ordered, e)
	}

	isMerge := func(k *yaml.Node) bool { return k.Tag == "!!merge" || k.Value == "<<" }

	for i := 0; i+1 < len(n.Content); i += 2 {
		if !isMerge(n.Content[i]) {
			continue
		}
		val := deref(n.Content[i+1])
		sources := []*yaml.Node{val}
		if val != nil && val.Kind == yaml.SequenceNode {
			// `<<: [*a, *b]`: the EARLIER alias wins and key order stays
			// *b-first, because PyYAML's flatten_mapping reverses the
			// sequence; walking backwards with overwrite-in-place matches.
			sources = make([]*yaml.Node, 0, len(val.Content))
			for i := len(val.Content) - 1; i >= 0; i-- {
				sources = append(sources, val.Content[i])
			}
		}
		for _, source := range sources {
			for _, merged := range mapEntries(source) {
				add(merged)
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if isMerge(n.Content[i]) {
			continue
		}
		add(entry{key: n.Content[i].Value, val: n.Content[i+1]})
	}
	return ordered
}

func lookup(entries []entry, key string) *yaml.Node {
	for _, e := range entries {
		if e.key == key {
			return deref(e.val)
		}
	}
	return nil
}

// Entry is one key/value pair of a YAML mapping, in document order; the
// exported shape of entry, for callers outside model that need the same
// `<<`-merge semantics without going through Load.
type Entry struct {
	Key string
	Val *yaml.Node
}

// MapEntries flattens a mapping into ordered key/value pairs, resolving `<<`
// exactly as mapEntries/PyYAML does (earlier alias wins). Exported so a
// second walker — cli's validate, which must keep walking a document Load
// would have rejected — does not reimplement merge order and risk drifting
// from it.
func MapEntries(n *yaml.Node) []Entry {
	in := mapEntries(n)
	out := make([]Entry, len(in))
	for i, e := range in {
		out[i] = Entry{Key: e.key, Val: e.val}
	}
	return out
}

// Lookup finds key in entries (as returned by MapEntries), dereferencing its
// value.
func Lookup(entries []Entry, key string) *yaml.Node {
	for _, e := range entries {
		if e.Key == key {
			return deref(e.Val)
		}
	}
	return nil
}

// Deref follows YAML alias nodes to their target; exported for the same
// reason as MapEntries.
func Deref(n *yaml.Node) *yaml.Node { return deref(n) }

func nodeString(n *yaml.Node) string {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// pyyamlBools is PyYAML's YAML 1.1 bool resolver table: plain `yes`/`on` is
// True, `no`/`off` False. Bare `y`/`n` are deliberately absent — PyYAML leaves
// them strings, and a non-empty string is truthy, making BOTH true.
var pyyamlBools = map[string]bool{
	"yes": true, "Yes": true, "YES": true,
	"no": false, "No": false, "NO": false,
	"true": true, "True": true, "TRUE": true,
	"false": false, "False": false, "FALSE": false,
	"on": true, "On": true, "ON": true,
	"off": false, "Off": false, "OFF": false,
}

// nodeBool resolves the scalar as PyYAML (YAML 1.1) would, then applies Python
// truthiness — barf's boolean-ish fields are all `meta.get(...)` in a plain
// `if`. So `management: yes` is true, `enabled: "no"` too (non-empty string),
// `mtu: 0` false. go-yaml is 1.2 and resolves `yes` to !!str, hence redoing it.
func nodeBool(n *yaml.Node) bool {
	n = deref(n)
	if n == nil {
		return false
	}
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		// A container is truthy when it is non-empty, as in Python.
		return len(n.Content) > 0
	case yaml.ScalarNode:
	default:
		return false
	}

	// Quoted and block scalars are strings in PyYAML too, so any non-empty
	// one is truthy (the `enabled: "no"` case).
	const quoted = yaml.SingleQuotedStyle | yaml.DoubleQuotedStyle |
		yaml.LiteralStyle | yaml.FoldedStyle
	if n.Style&quoted != 0 || (n.Style&yaml.TaggedStyle != 0 && n.Tag == "!!str") {
		return n.Value != ""
	}

	// A plain scalar: null (explicit or empty) is falsy.
	if n.Tag == "!!null" {
		return false
	}
	if value, ok := pyyamlBools[n.Value]; ok {
		return value
	}
	if i, err := strconv.ParseInt(n.Value, 0, 64); err == nil {
		return i != 0
	}
	if f, err := strconv.ParseFloat(n.Value, 64); err == nil {
		return f != 0
	}
	return n.Value != ""
}

func nodeInt(n *yaml.Node) (int, bool) {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return 0, false
	}
	value, err := strconv.Atoi(n.Value)
	if err != nil {
		return 0, false
	}
	return value, true
}

func nodeStrings(n *yaml.Node) []string {
	n = deref(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, nodeString(item))
	}
	return out
}

func seqEntries(n *yaml.Node) []*yaml.Node {
	n = deref(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, deref(item))
	}
	return out
}

// nodeAny decodes a node into plain Go values for the Raw / Wireguard / RA
// passthrough fields. A decode failure is returned, never swallowed: Host.Raw
// exists so a partial port never silently drops configuration.
func nodeAny(n *yaml.Node) (any, error) {
	n = deref(n)
	if n == nil {
		return nil, nil
	}
	var out any
	if err := n.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func nodeAnyMap(n *yaml.Node) (map[string]any, error) {
	decoded, err := nodeAny(n)
	if err != nil {
		return nil, err
	}
	value, _ := decoded.(map[string]any)
	return value, nil
}

// anyString reads a string-ish value out of a decoded YAML map.
func anyString(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// anyStrings reads a list-of-strings value out of a decoded YAML map.
func anyStrings(m map[string]any, key string) []string {
	items, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}
