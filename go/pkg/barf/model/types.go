// Package model holds the network.yml fabric description: hosts, interfaces,
// the wireguard links between them, and global metadata. These types are the
// contract between render, device, and cli (../CONTRACT.md); names are frozen.
package model

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Site is a geographic location, used for BGP origin tagging and local-pref.
type Site struct {
	Name   string
	ID     int
	Coords [2]float64
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

// Version reports the IP family (4 or 6) of the member address.
func (m FirewallAddressMember) Version() int {
	addr := m.Address
	if i := strings.IndexByte(addr, '/'); i >= 0 {
		addr = addr[:i]
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil || ip.Is4() {
		return 4
	}
	return 6
}

// FirewallAddressGroup is a named set of addresses (v4 and/or v6).
type FirewallAddressGroup struct {
	Name    string
	Members []FirewallAddressMember
}

// MembersV returns the group's members belonging to IP version v.
func (g FirewallAddressGroup) MembersV(v int) []FirewallAddressMember {
	var out []FirewallAddressMember
	for _, m := range g.Members {
		if m.Version() == v {
			out = append(out, m)
		}
	}
	return out
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

// GetIP returns hostname's address on this link (empty when unnumbered):
// side A takes the first usable address, side B the next one.
func (l Link) GetIP(hostname string, withNetmask bool) string {
	if l.Network == "" {
		return ""
	}
	prefix, err := netip.ParsePrefix(l.Network)
	if err != nil {
		return ""
	}
	first := firstHost(prefix)
	if !first.IsValid() {
		return ""
	}
	addr := first
	if hostname == l.B {
		addr = first.Next()
	} else if hostname != l.A {
		return ""
	}
	if withNetmask {
		return fmt.Sprintf("%s/%d", addr.String(), prefix.Bits())
	}
	return addr.String()
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
