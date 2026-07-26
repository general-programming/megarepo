package cli

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// This file ports barf/cli/validate.py. model.Load already rejects most of
// what is checked here, but it dies on the first problem; validate does a
// tolerant re-parse of the same document so one run reports everything
// wrong with a network.yml at once. model.Load is then run as a
// belt-and-braces check, and anything only it caught is reported too.

// validDeviceTypes mirrors model's unexported deviceTypes (the Python
// VENDOR_MAP), including the aliases. Keep the two in step: a type added
// there and missing here shows up as a false "unsupported type".
var validDeviceTypes = map[string]bool{
	"vyos": true, "edgeos": true, "linux": true, "external": true,
	"cisco": true, "cisco-ios": true, "eos": true,
	"dnos6": true, "dnos-6": true, "dnos9": true, "dnos-9": true,
	"mikrotik": true,
}

// problem is one validation finding.
type problem struct {
	// Where is the offending part of the document ("hosts.sea1-vpn-0").
	Where string `json:"where"`
	// Message says what is wrong with it.
	Message string `json:"message"`
	// Line is the 1-based network.yml line, or 0 when unknown.
	Line int `json:"line,omitempty"`
}

func (p problem) String() string {
	if p.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", p.Where, p.Line, p.Message)
	}
	return fmt.Sprintf("%s: %s", p.Where, p.Message)
}

// validateReport is the whole outcome of a validate run.
type validateReport struct {
	File     string    `json:"file"`
	OK       bool      `json:"ok"`
	Hosts    int       `json:"hosts"`
	Links    int       `json:"links"`
	Problems []problem `json:"problems"`
}

func newValidateCmd(o *Options) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "validate [network.yml]",
		Short: "Check network.yml for problems",
		Long: "validate reports everything wrong with a network.yml in one pass:\n" +
			"unknown device types and sites, duplicate site ids, unparseable\n" +
			"addresses, links pointing at hosts that do not exist, and links whose\n" +
			"derived ports collide. Exits non-zero when anything is found.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return runValidate(o, path, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit the report as JSON")
	return cmd
}

func runValidate(o *Options, path string, jsonOut bool) error {
	if path == "" {
		discovered, err := o.networkPath()
		if err != nil {
			return err
		}
		path = discovered
	}

	report, err := validateFile(path)
	if err != nil {
		return err
	}

	if jsonOut {
		enc := json.NewEncoder(o.Out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else if report.OK {
		o.printf("OK: %s is valid (%d hosts, %d links).\n", report.File, report.Hosts, report.Links)
	} else {
		for _, p := range report.Problems {
			o.printf("ERROR: %s\n", p)
		}
	}

	if !report.OK {
		return fmt.Errorf("%s: %d problem(s)", report.File, len(report.Problems))
	}
	return nil
}

// validateFile parses path tolerantly and returns every problem found.
// The returned error is reserved for problems reading or parsing the file
// at all — a document that parses but is wrong is a report, not an error.
func validateFile(path string) (*validateReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", path)
	}

	v := &validator{report: &validateReport{File: path, Problems: []problem{}}}
	v.run(root.Content[0])

	// model.Load is the real parser; anything it rejects that the pass
	// above missed still has to surface, or validate would bless a file
	// every other command refuses.
	if _, err := loadNetwork(path); err != nil && len(v.report.Problems) == 0 {
		v.add("network.yml", 0, "%s", err.Error())
	}

	v.report.OK = len(v.report.Problems) == 0
	return v.report, nil
}

// validator accumulates findings over one document.
type validator struct {
	report *validateReport
	// sites are the site names global_meta declared, for host lookups.
	sites map[string]bool
	// hostIDs maps hostname to its `id`, 0 when absent.
	hostIDs map[string]int
	// hostOrder keeps hostnames in document order.
	hostOrder []string
}

func (v *validator) add(where string, line int, format string, args ...any) {
	v.report.Problems = append(v.report.Problems, problem{
		Where:   where,
		Line:    line,
		Message: fmt.Sprintf(format, args...),
	})
}

func (v *validator) run(doc *yaml.Node) {
	top := ymap(doc)

	globalMeta := yget(top, "global_meta")
	if globalMeta == nil {
		v.add("network.yml", 0, "missing 'global_meta' section")
	}
	v.checkGlobalMeta(globalMeta)

	hosts := yget(top, "hosts")
	if hosts == nil {
		v.add("network.yml", 0, "missing 'hosts' section")
	}
	v.checkHosts(hosts)

	v.checkLinks(yget(top, "links"))
}

func (v *validator) checkGlobalMeta(node *yaml.Node) {
	v.sites = map[string]bool{}
	entries := ymap(node)

	sites := ymap(yget(entries, "sites"))
	byID := map[int]string{}
	for _, site := range sites {
		where := "global_meta.sites." + site.key
		v.sites[site.key] = true
		fields := ymap(site.val)

		id, ok := yint(yget(fields, "id"))
		if !ok {
			v.add(where, site.val.Line, "no id")
		} else if other, dup := byID[id]; dup {
			v.add(where, site.val.Line, "id %d is already used by site %q", id, other)
		} else {
			byID[id] = site.key
		}

		coords := yseq(yget(fields, "coords"))
		if len(coords) != 2 {
			v.add(where, site.val.Line, "needs exactly two coords, got %d", len(coords))
		} else {
			for i, name := range []string{"latitude", "longitude"} {
				if _, err := strconv.ParseFloat(coords[i].Value, 64); err != nil {
					v.add(where, coords[i].Line, "%s %q is not a number", name, coords[i].Value)
				}
			}
		}
	}

	if len(sites) > 0 {
		if _, ok := yint(yget(entries, "community_asn")); !ok {
			v.add("global_meta", 0, "sites are defined but community_asn is missing;"+
				" site-origin large communities need a fabric-wide Global Administrator value")
		}
	}
}

func (v *validator) checkHosts(node *yaml.Node) {
	v.hostIDs = map[string]int{}
	entries := ymap(node)
	v.report.Hosts = len(entries)

	seenID := map[int]string{}
	for _, host := range entries {
		where := "hosts." + host.key
		v.hostOrder = append(v.hostOrder, host.key)
		fields := ymap(host.val)

		deviceType := ystr(yget(fields, "type"))
		switch {
		case deviceType == "":
			v.add(where, host.val.Line, "missing required field 'type'")
		case !validDeviceTypes[deviceType]:
			v.add(where, host.val.Line, "unsupported type %q", deviceType)
		}

		if role := ystr(yget(fields, "role")); role == "" {
			v.add(where, host.val.Line, "missing required field 'role'")
		}

		if site := ystr(yget(fields, "site")); site != "" && !v.sites[site] {
			v.add(where, host.val.Line, "unknown site %q", site)
		}

		if id, ok := yint(yget(fields, "id")); ok {
			v.hostIDs[host.key] = id
			if other, dup := seenID[id]; dup {
				v.add(where, host.val.Line, "id %d is already used by host %q", id, other)
			} else {
				seenID[id] = host.key
			}
		}

		for _, field := range []string{"address", "ip6_address"} {
			raw := ystr(yget(fields, field))
			if raw == "" {
				continue
			}
			if _, err := netip.ParsePrefix(raw); err != nil {
				if _, plainErr := netip.ParseAddr(raw); plainErr != nil {
					v.add(where, host.val.Line, "%s %q is not an IP address", field, raw)
				}
			}
		}

		v.checkInterfaces(where, yget(fields, "interfaces"))
	}
}

func (v *validator) checkInterfaces(where string, node *yaml.Node) {
	seen := map[string]bool{}
	for _, item := range yseq(node) {
		fields := ymap(item)
		name := ystr(yget(fields, "name"))
		if name == "" {
			v.add(where, item.Line, "an interface has no name")
			continue
		}
		// The same physical interface legitimately appears once per
		// VLAN sub-interface, so a duplicate is only a duplicate when
		// the VLAN matches too.
		key := name
		if vlan, ok := yint(yget(fields, "vlan")); ok {
			key = fmt.Sprintf("%s.%d", name, vlan)
		}
		if seen[key] {
			v.add(where, item.Line, "interface %q is defined twice", key)
		}
		seen[key] = true

		addresses := append([]string{ystr(yget(fields, "address"))}, ystrs(yget(fields, "addresses"))...)
		for _, raw := range addresses {
			if raw == "" {
				continue
			}
			if _, err := netip.ParsePrefix(raw); err != nil {
				if _, plainErr := netip.ParseAddr(raw); plainErr != nil {
					v.add(where, item.Line, "interface %s: %q is not an IP address", name, raw)
				}
			}
		}
	}
}

// checkLinks walks the nested links: {peer: {uplink: spec}} mapping. The
// inner key is side A, matching model.parseLinks.
func (v *validator) checkLinks(node *yaml.Node) {
	known := map[string]bool{}
	for _, name := range v.hostOrder {
		known[name] = true
	}

	// port -> the link that claimed it, for collision reporting.
	byPort := map[int]string{}
	count := 0

	for _, outer := range ymap(node) {
		sideB := outer.key
		if !known[sideB] {
			v.add("links."+sideB, outer.val.Line, "%q is not a host", sideB)
		}

		for _, inner := range ymap(outer.val) {
			sideA := inner.key
			where := fmt.Sprintf("links.%s.%s", sideB, sideA)
			count++
			if !known[sideA] {
				v.add(where, inner.val.Line, "%q is not a host", sideA)
			}

			port, pinned := yint(inner.val)
			if !pinned {
				port, pinned = yint(yget(ymap(inner.val), "port"))
			}
			if !pinned {
				// Derived ports need both ids; model.Load refuses
				// otherwise.
				missing := []string{}
				for _, name := range []string{sideA, sideB} {
					if known[name] && v.hostIDs[name] == 0 {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					sort.Strings(missing)
					v.add(where, inner.val.Line,
						"cannot derive a port: %s has no 'id' (pin 'port' instead)",
						strings.Join(missing, " and "))
					continue
				}
				if !known[sideA] || !known[sideB] {
					continue
				}
				port = derivedLinkPort(v.hostIDs[sideA], v.hostIDs[sideB])
			}

			if other, dup := byPort[port]; dup {
				v.add(where, inner.val.Line, "port %d is already used by %s", port, other)
			} else {
				byPort[port] = where
			}
		}
	}
	v.report.Links = count
}

// derivedLinkPort mirrors model.derivedPort: 51000 + min*64 + max.
func derivedLinkPort(a, b int) int {
	if a > b {
		a, b = b, a
	}
	return 51000 + a*64 + b
}

// --- tolerant YAML helpers -------------------------------------------
//
// model has equivalents, but they are unexported and validate must keep
// walking a document model.Load would have rejected outright.

type yentry struct {
	key string
	val *yaml.Node
}

func yderef(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// ymap flattens a mapping into ordered entries, resolving `<<` merge keys
// the way PyYAML does: merged entries first, an explicit key of the same
// name overriding in place.
func ymap(n *yaml.Node) []yentry {
	n = yderef(n)
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}

	var ordered []yentry
	index := map[string]int{}
	add := func(e yentry) {
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
		val := yderef(n.Content[i+1])
		sources := []*yaml.Node{val}
		if val != nil && val.Kind == yaml.SequenceNode {
			sources = val.Content
		}
		for _, source := range sources {
			for _, merged := range ymap(source) {
				add(merged)
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if isMerge(n.Content[i]) {
			continue
		}
		add(yentry{key: n.Content[i].Value, val: n.Content[i+1]})
	}
	return ordered
}

func yget(entries []yentry, key string) *yaml.Node {
	for _, e := range entries {
		if e.key == key {
			return yderef(e.val)
		}
	}
	return nil
}

func ystr(n *yaml.Node) string {
	n = yderef(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func yint(n *yaml.Node) (int, bool) {
	n = yderef(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return 0, false
	}
	value, err := strconv.Atoi(n.Value)
	if err != nil {
		return 0, false
	}
	return value, true
}

func yseq(n *yaml.Node) []*yaml.Node {
	n = yderef(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, yderef(item))
	}
	return out
}

func ystrs(n *yaml.Node) []string {
	var out []string
	for _, item := range yseq(n) {
		out = append(out, ystr(item))
	}
	return out
}
