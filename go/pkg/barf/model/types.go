// Package model holds the network.yml fabric description: hosts, interfaces,
// the wireguard links between them, and global metadata. These types are the
// contract between render, device, and cli (../CONTRACT.md); names are frozen.
package model

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Site is a geographic location, used for BGP origin tagging and local-pref.
type Site struct {
	Name   string
	ID     int
	Coords [2]float64
}

// FlowCollector is one NetFlow/IPFIX collector.
type FlowCollector struct {
	Address string
	Port    int
	Version int // 5 | 9 | 10
}

// FlowExport is the network.yml `flow_export` / `global_meta.flow_export` block.
type FlowExport struct {
	Collectors   []FlowCollector
	SamplingRate *int
	Interfaces   []string
	DisableIMT   *bool
}

// EffectiveFlow returns the flow export config for host: per-host overrides
// global; nil when neither is set. Collectors from the host replace the
// global list when the host defines any.
func (n *Network) EffectiveFlow(host *Host) *FlowExport {
	var base *FlowExport
	if n.Global.FlowExport != nil {
		cp := *n.Global.FlowExport
		base = &cp
	}
	if host.FlowExport == nil {
		return base
	}
	if base == nil {
		cp := *host.FlowExport
		return &cp
	}
	out := *base
	if len(host.FlowExport.Collectors) > 0 {
		out.Collectors = append([]FlowCollector(nil), host.FlowExport.Collectors...)
	}
	if host.FlowExport.SamplingRate != nil {
		v := *host.FlowExport.SamplingRate
		out.SamplingRate = &v
	}
	if len(host.FlowExport.Interfaces) > 0 {
		out.Interfaces = append([]string(nil), host.FlowExport.Interfaces...)
	}
	if host.FlowExport.DisableIMT != nil {
		v := *host.FlowExport.DisableIMT
		out.DisableIMT = &v
	}
	return &out
}

// GlobalMeta is network.yml's global_meta block.
type GlobalMeta struct {
	SearchDomain string
	Nameservers  []string
	SSHKeys      []string
	SNMPPublic   string
	SNMPContact  string
	SNMPLocation string
	CommunityASN int
	Sites        map[string]Site
	FlowExport   *FlowExport
}

// Address is an IP address with its prefix length.
type Address struct {
	IP     netip.Addr
	Prefix int
}

// String renders as Python ip_interface.with_prefixlen (host bits preserved).
func (a Address) String() string {
	return fmt.Sprintf("%s/%d", a.IP.String(), a.Prefix)
}

// IsV6 reports whether the address is a real IPv6 address.
func (a Address) IsV6() bool { return a.IP.Is6() && !a.IP.Is4In6() }

// VLAN is an 802.1q VLAN referenced by an interface.
type VLAN struct {
	VID  int
	Name string
}

// Interface is one interface on a Host.
type Interface struct {
	Name         string
	Description  string
	Addresses    []Address
	DHCP         bool
	IPv6Autoconf bool
	Management   bool
	MTU          int
	Members      []string
	UntaggedVLAN *VLAN
	TaggedVLANs  []VLAN
	Wireguard    map[string]any
	RA           map[string]any

	Type    string // "VPNLink" (network.yml default), "bridge", "wireguard", ...
	DHCPv6  bool   // network.yml `dhcpv6`, distinct from DHCP
	Enabled bool   // defaults to true
}

// IsBridge reports whether the interface is a modeled L2 bridge.
func (i *Interface) IsBridge() bool { return i.Type == "bridge" }

// IsWireguard reports whether the interface is a static WireGuard tunnel.
func (i *Interface) IsWireguard() bool { return i.Type == "wireguard" }

// V4 returns the first IPv4 address on the interface, if any.
func (i *Interface) V4() *Address {
	for n := range i.Addresses {
		if i.Addresses[n].IP.Is4() {
			return &i.Addresses[n]
		}
	}
	return nil
}

// V6 returns the first IPv6 address on the interface, if any.
func (i *Interface) V6() *Address {
	for n := range i.Addresses {
		if i.Addresses[n].IP.Is6() && !i.Addresses[n].IP.Is4In6() {
			return &i.Addresses[n]
		}
	}
	return nil
}

// NATMasquerade is one rendered `nat source` masquerade rule.
type NATMasquerade struct {
	Rule        int
	Interface   string
	Source      string
	Destination string
}

// PortForward is one rendered `nat destination` port-forward rule.
type PortForward struct {
	Rule            int
	Name            string
	Interface       string
	Protocol        string
	Port            int
	To              string
	TranslationPort int
}

// Description mirrors the Python PortForward.description property.
func (p PortForward) Description() string {
	return fmt.Sprintf("Port Forward: %s-%s to %s", p.Name, strings.ToUpper(p.Protocol), p.To)
}

// OSPFNetwork is one network announced into an OSPF area.
type OSPFNetwork struct {
	Network string
	Area    string // spelled as the device has it ("0" vs "0.0.0.0")
}

// OSPFInterface holds per-interface OSPF options.
type OSPFInterface struct {
	Name      string
	MTUIgnore bool
}

// OSPFRedistribute is a protocol redistributed into OSPF.
type OSPFRedistribute struct {
	Protocol   string
	MetricType int // 0 when absent
}

// OSPFConfig is the parsed network.yml `ospf:` block.
type OSPFConfig struct {
	Networks     []OSPFNetwork
	Interfaces   []OSPFInterface
	Redistribute []OSPFRedistribute
}

// StaticRoute is one declarative static route.
type StaticRoute struct {
	Network   string
	Interface string
	NextHop   string
}

// BirdConfig holds the bird daemon knobs for linux hosts.
type BirdConfig struct {
	RouterID            string
	KrtPrefsrc          string
	MergePaths          bool
	ImportFilter        string
	ImportCheckFunction string
	LocalConf           string
}

// TransitPeer is one foreign AS this host reflects into the fabric.
type TransitPeer struct {
	Name           string
	RemoteAS       int
	LinkNetwork    string
	RemoteAddress  string
	ExportSupernet string
}

// BGPConfig holds BGP policy knobs beyond plain fabric membership.
type BGPConfig struct {
	Instance string
	Transit  []TransitPeer
}

// FirewallAddressMember is one address (with optional comment) in a group.
type FirewallAddressMember struct {
	Address string
	Comment string
}

// Version reports the IP family (4 or 6) of the member address, erroring on
// anything ipaddress.ip_network(strict=False).version would reject: an
// unparseable address ("10.0.0./24"), or a prefix length outside the
// family's bit width ("10.0.0.0/33"). The previous behaviour defaulted both
// to 4, which would file a v6 address (or garbage) into the v4 group
// instead of failing the render.
func (m FirewallAddressMember) Version() (int, error) {
	addr := m.Address
	prefixLen := -1
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		var err error
		prefixLen, err = strconv.Atoi(addr[i+1:])
		if err != nil {
			return 0, fmt.Errorf("firewall address %q: invalid prefix length", m.Address)
		}
		addr = addr[:i]
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return 0, fmt.Errorf("firewall address %q: %w", m.Address, err)
	}
	if prefixLen != -1 && (prefixLen < 0 || prefixLen > ip.BitLen()) {
		return 0, fmt.Errorf("firewall address %q: prefix length %d out of range for a %d-bit address",
			m.Address, prefixLen, ip.BitLen())
	}
	if ip.Is4() {
		return 4, nil
	}
	return 6, nil
}

// FirewallAddressGroup is a named set of addresses (v4 and/or v6).
type FirewallAddressGroup struct {
	Name    string
	Members []FirewallAddressMember
}

// MembersV returns the group's members belonging to IP version v, erroring
// on the first member whose address is invalid rather than silently sorting
// it into a family it may not belong to.
func (g FirewallAddressGroup) MembersV(v int) ([]FirewallAddressMember, error) {
	var out []FirewallAddressMember
	for _, m := range g.Members {
		version, err := m.Version()
		if err != nil {
			return nil, fmt.Errorf("firewall group %q: %w", g.Name, err)
		}
		if version == v {
			out = append(out, m)
		}
	}
	return out, nil
}

// FirewallInterfaceGroup is a named set of interfaces, optionally including
// other groups (RouterOS interface-list nesting).
type FirewallInterfaceGroup struct {
	Name       string
	Interfaces []string
	Include    []string
}

// FirewallGroups is the vendor-neutral firewall group model.
type FirewallGroups struct {
	Address   []FirewallAddressGroup
	Interface []FirewallInterfaceGroup
}

// ResolvedInterfaces returns every interface in group name, resolving include
// nesting: first-seen order, de-duplicated, cycle-safe.
func (f FirewallGroups) ResolvedInterfaces(name string) []string {
	byName := map[string]FirewallInterfaceGroup{}
	for _, g := range f.Interface {
		byName[g.Name] = g
	}
	seenGroups := map[string]bool{}
	seenIfaces := map[string]bool{}
	var out []string
	stack := []string{name}
	for len(stack) > 0 {
		current := stack[0]
		stack = stack[1:]
		group, ok := byName[current]
		if !ok || seenGroups[group.Name] {
			continue
		}
		seenGroups[group.Name] = true
		for _, iface := range group.Interfaces {
			if !seenIfaces[iface] {
				seenIfaces[iface] = true
				out = append(out, iface)
			}
		}
		stack = append(stack, group.Include...)
	}
	return out
}

// Host is one device in the fabric.
type Host struct {
	Hostname    string
	DeviceType  string // "vyos" | "eos" | "linux" | ...
	Role        string // "vpn" | "core" | ...
	Site        string
	ID          int // network.yml `id`; 0 when absent
	ASN         int // 0 when absent
	Address     *Address
	IP6Address  *Address
	Interfaces  []Interface
	Nameservers []string // inherits GlobalMeta when unset in yaml
	Networks    []string
	ExtraConfig []string
	CloudInit   bool
	EAPIVRF     string // eos only: network.yml `eapi_vrf`

	// AddressRaw / IP6AddressRaw keep the network.yml spelling of `address:` /
	// `ip6_address:`; renders interpolate the literal string (IPsec peer ids).
	AddressRaw    string
	IP6AddressRaw string

	SNMPLocation string // host-level `location:`; empty inherits GlobalMeta

	NATMasquerades  []NATMasquerade
	NATPortForwards []PortForward
	OSPF            OSPFConfig
	StaticRoutes    []StaticRoute
	Bird            BirdConfig
	BGP             BGPConfig
	Firewall        FirewallGroups
	FlowExport      *FlowExport

	// Raw keeps untranslated yaml so a partial port never drops configuration.
	Raw map[string]any
}

// CanBFD reports whether the device supports BFD (VyOS only, as in Python).
func (h *Host) CanBFD() bool { return h.DeviceType == "vyos" }

// IsSpine mirrors the Python BaseHost.is_spine heuristic.
func (h *Host) IsSpine() bool { return strings.Contains(h.Hostname, "-spine-") }

// ManagementAddress returns the first `management: true` interface's address,
// preferring IPv6 (BaseHost.management_address).
func (h *Host) ManagementAddress() *Address {
	for i := range h.Interfaces {
		if !h.Interfaces[i].Management {
			continue
		}
		if v6 := h.Interfaces[i].V6(); v6 != nil {
			return v6
		}
		return h.Interfaces[i].V4()
	}
	return nil
}

// WGEndpoint is the address peers dial for WireGuard, preferring global IPv6;
// empty for hosts nobody can dial (BaseHost.wg_endpoint).
func (h *Host) WGEndpoint() string {
	for _, candidate := range []*Address{h.IP6Address, h.Address} {
		if candidate == nil {
			continue
		}
		if IsGlobal(candidate.IP) {
			return candidate.IP.String()
		}
	}
	return ""
}

// Link is a wireguard fabric link. A is the uplink side (Python side_a),
// which decides numbered-link IP assignment.
type Link struct {
	A, B    string
	Port    int
	Network string
	Secret  string
	IPsec   bool

	// Pinned links (explicit network.yml port) keep the legacy port-based
	// Vault key paths; derived links use pair-based paths, so unpinning is
	// the whole per-link migration.
	Pinned bool
}

// Unnumbered reports whether the link has no /31 (interface-based BGP).
func (l Link) Unnumbered() bool { return l.Network == "" }

// Pair is the canonical, order-free name for the two endpoints.
func (l Link) Pair() string {
	names := []string{l.A, l.B}
	sort.Strings(names)
	return names[0] + "--" + names[1]
}

// KeyPath is the Vault path holding hostname's keypair for this link.
func (l Link) KeyPath(hostname string) string {
	if l.Pinned {
		return fmt.Sprintf("wg-%d-%s", l.Port, hostname)
	}
	return fmt.Sprintf("wglink-%s-%s", l.Pair(), hostname)
}

// Other returns the hostname on the far side of the link from hostname.
func (l Link) Other(hostname string) string {
	if l.A == hostname {
		return l.B
	}
	return l.A
}

// GetIP returns hostname's address on this link ("", nil when unnumbered):
// side A takes the first usable address, side B the next one. l.Network with
// host bits set (e.g. "172.31.255.21/31", whose network address is .20) is
// an error, not a silent correction: Python's get_ip constructs
// ipaddress.IPv4Network/IPv6Network with the default strict=True, which
// raises on exactly this rather than masking it down to a different address
// than network.yml named.
func (l Link) GetIP(hostname string, withNetmask bool) (string, error) {
	if l.Network == "" {
		return "", nil
	}
	prefix, err := netip.ParsePrefix(l.Network)
	if err != nil {
		return "", fmt.Errorf("link %s-%s: invalid network %q: %w", l.A, l.B, l.Network, err)
	}
	if prefix.Masked().Addr() != prefix.Addr() {
		return "", fmt.Errorf("link %s-%s: network %q has host bits set", l.A, l.B, l.Network)
	}
	first := firstHost(prefix)
	if !first.IsValid() {
		return "", fmt.Errorf("link %s-%s: network %q has no usable host address", l.A, l.B, l.Network)
	}
	addr := first
	if hostname == l.B {
		addr = first.Next()
	} else if hostname != l.A {
		return "", nil
	}
	if withNetmask {
		return fmt.Sprintf("%s/%d", addr.String(), prefix.Bits()), nil
	}
	return addr.String(), nil
}

// firstHost mirrors next(ipaddress.ip_network(...).hosts()): /31 and /32
// start at the network address, everything else skips it.
func firstHost(p netip.Prefix) netip.Addr {
	network := p.Masked().Addr()
	full := network.BitLen()
	if p.Bits() >= full-1 {
		return network
	}
	return network.Next()
}

// Network is a parsed network.yml.
type Network struct {
	Global GlobalMeta
	Hosts  []Host
	Links  []Link
}

// Host returns the host with the given hostname.
func (n *Network) Host(hostname string) (*Host, bool) {
	for i := range n.Hosts {
		if n.Hosts[i].Hostname == hostname {
			return &n.Hosts[i], true
		}
	}
	return nil, false
}

// LinksFor returns every link touching hostname.
func (n *Network) LinksFor(hostname string) []Link {
	var out []Link
	for _, link := range n.Links {
		if link.A == hostname || link.B == hostname {
			out = append(out, link)
		}
	}
	return out
}
