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

// mapEntries flattens a mapping node into ordered key/value pairs,
// resolving `<<` merge keys the way PyYAML does: merged entries come
// first and an explicit key of the same name overrides the merged value
// while keeping the merged key's position.
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
			sources = val.Content
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

func nodeString(n *yaml.Node) string {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func nodeBool(n *yaml.Node) bool {
	n = deref(n)
	if n == nil || n.Kind != yaml.ScalarNode {
		return false
	}
	value, err := strconv.ParseBool(n.Value)
	return err == nil && value
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

// nodeAny decodes a node into plain Go values (map[string]any, []any,
// scalars) for the Raw / Wireguard / RA passthrough fields.
func nodeAny(n *yaml.Node) any {
	n = deref(n)
	if n == nil {
		return nil
	}
	var out any
	if err := n.Decode(&out); err != nil {
		return nil
	}
	return out
}

func nodeAnyMap(n *yaml.Node) map[string]any {
	value, _ := nodeAny(n).(map[string]any)
	return value
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
