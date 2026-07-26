package cli

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// Ports barf/cli/validate.py. model.Load rejects most of what is checked here
// but dies on the first problem, so validate re-parses the document
// tolerantly, reports everything wrong at once, then runs model.Load too.

// validDeviceTypes mirrors model's unexported deviceTypes (Python's
// VENDOR_MAP) including aliases; a type added there and missing here shows up
// as a false "unsupported type".
var validDeviceTypes = map[string]bool{
	"vyos": true, "edgeos": true, "linux": true, "external": true,
	"cisco": true, "cisco-ios": true, "eos": true,
	"dnos6": true, "dnos-6": true, "dnos9": true, "dnos-9": true,
	"mikrotik": true,
}

// problem is one validation finding.
type problem struct {
	// Where is the offending part of the document ("hosts.sea1-vpn-0").
	Where   string `json:"where"`
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

// validateFile parses path tolerantly and returns every problem found. The
// error is reserved for failing to read or parse the file at all.
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

	// model.Load is the real parser; anything it rejects that the pass above
	// missed must still surface.
	if _, err := loadNetwork(path); err != nil && len(v.report.Problems) == 0 {
		v.add("network.yml", 0, "%s", err.Error())
	}

	v.report.OK = len(v.report.Problems) == 0
	return v.report, nil
}

type validator struct {
	report *validateReport
	// sites are the site names global_meta declared, for host lookups.
	sites map[string]bool
	// hostIDs maps hostname to its `id`, 0 when absent.
	hostIDs   map[string]int
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
		where := "global_meta.sites." + site.Key
		v.sites[site.Key] = true
		fields := ymap(site.Val)

		id, ok := yint(yget(fields, "id"))
		if !ok {
			v.add(where, site.Val.Line, "no id")
		} else if other, dup := byID[id]; dup {
			v.add(where, site.Val.Line, "id %d is already used by site %q", id, other)
		} else {
			byID[id] = site.Key
		}

		coords := yseq(yget(fields, "coords"))
		if len(coords) != 2 {
			v.add(where, site.Val.Line, "needs exactly two coords, got %d", len(coords))
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
		where := "hosts." + host.Key
		v.hostOrder = append(v.hostOrder, host.Key)
		fields := ymap(host.Val)

		deviceType := ystr(yget(fields, "type"))
		switch {
		case deviceType == "":
			v.add(where, host.Val.Line, "missing required field 'type'")
		case !validDeviceTypes[deviceType]:
			v.add(where, host.Val.Line, "unsupported type %q", deviceType)
		}

		if role := ystr(yget(fields, "role")); role == "" {
			v.add(where, host.Val.Line, "missing required field 'role'")
		}

		if site := ystr(yget(fields, "site")); site != "" && !v.sites[site] {
			v.add(where, host.Val.Line, "unknown site %q", site)
		}

		if id, ok := yint(yget(fields, "id")); ok {
			v.hostIDs[host.Key] = id
			if other, dup := seenID[id]; dup {
				v.add(where, host.Val.Line, "id %d is already used by host %q", id, other)
			} else {
				seenID[id] = host.Key
			}
		}

		for _, field := range []string{"address", "ip6_address"} {
			raw := ystr(yget(fields, field))
			if raw == "" {
				continue
			}
			if _, err := netip.ParsePrefix(raw); err != nil {
				if _, plainErr := netip.ParseAddr(raw); plainErr != nil {
					v.add(where, host.Val.Line, "%s %q is not an IP address", field, raw)
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
		// One physical interface appears once per VLAN sub-interface, so a
		// duplicate needs a matching VLAN too.
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

// checkLinks walks the nested {peer: {uplink: spec}} mapping; the inner key is
// side A, matching model.parseLinks.
func (v *validator) checkLinks(node *yaml.Node) {
	known := map[string]bool{}
	for _, name := range v.hostOrder {
		known[name] = true
	}

	// port -> the link that claimed it, for collision reporting.
	byPort := map[int]string{}
	count := 0

	for _, outer := range ymap(node) {
		sideB := outer.Key
		if !known[sideB] {
			v.add("links."+sideB, outer.Val.Line, "%q is not a host", sideB)
		}

		for _, inner := range ymap(outer.Val) {
			sideA := inner.Key
			where := fmt.Sprintf("links.%s.%s", sideB, sideA)
			count++
			if !known[sideA] {
				v.add(where, inner.Val.Line, "%q is not a host", sideA)
			}

			port, pinned := yint(inner.Val)
			if !pinned {
				port, pinned = yint(yget(ymap(inner.Val), "port"))
			}
			if !pinned {
				// Derived ports need both ids; model.Load refuses otherwise.
				missing := []string{}
				for _, name := range []string{sideA, sideB} {
					if known[name] && v.hostIDs[name] == 0 {
						missing = append(missing, name)
					}
				}
				if len(missing) > 0 {
					sort.Strings(missing)
					v.add(where, inner.Val.Line,
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
				v.add(where, inner.Val.Line, "port %d is already used by %s", port, other)
			} else {
				byPort[port] = where
			}

			// A `network:` with host bits set (e.g. "172.31.255.21/31",
			// whose network address is .20) would deploy at a different
			// address than what network.yml names: model.Link.GetIP now
			// refuses it at render time (see model/types.go), but a
			// validate pass should catch the typo before then, not send the
			// operator to a render failure to find it.
			if network := ystr(yget(ymap(inner.Val), "network")); network != "" {
				if prefix, err := netip.ParsePrefix(network); err != nil {
					v.add(where, inner.Val.Line, "network %q: %s", network, err)
				} else if prefix.Masked().Addr() != prefix.Addr() {
					v.add(where, inner.Val.Line, "network %q has host bits set", network)
				}
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
// model's equivalents are unexported, and validate must keep walking a
// document model.Load would have rejected; but the `<<`-merge WALK itself
// (model.MapEntries) is shared, so this file cannot drift from PyYAML's
// "earlier alias wins" semantics the way a second reimplementation did.

func ymap(n *yaml.Node) []model.Entry { return model.MapEntries(n) }

func yget(entries []model.Entry, key string) *yaml.Node { return model.Lookup(entries, key) }

func ystr(n *yaml.Node) string {
	n = model.Deref(n)
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func yint(n *yaml.Node) (int, bool) {
	n = model.Deref(n)
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
	n = model.Deref(n)
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, model.Deref(item))
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
