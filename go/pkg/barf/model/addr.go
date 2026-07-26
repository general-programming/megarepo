package model

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseAddress parses a network.yml address, with or without a prefix
// length. A bare address gets its family's host prefix (/32, /128),
// matching Python's ipaddress.ip_interface.
func ParseAddress(text string) (Address, error) {
	text = strings.TrimSpace(text)
	if strings.Contains(text, "/") {
		prefix, err := netip.ParsePrefix(text)
		if err != nil {
			return Address{}, fmt.Errorf("invalid address %q: %w", text, err)
		}
		return Address{IP: prefix.Addr(), Prefix: prefix.Bits()}, nil
	}
	ip, err := netip.ParseAddr(text)
	if err != nil {
		return Address{}, fmt.Errorf("invalid address %q: %w", text, err)
	}
	return Address{IP: ip, Prefix: ip.BitLen()}, nil
}

// Python's ipaddress considers these ranges non-global; barf only ever
// asks about addresses drawn from network.yml, so this is the subset
// that matters for wg_endpoint selection.
var nonGlobalV4 = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)

var nonGlobalV6 = mustPrefixes(
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b:1::/48", "100::/64",
	"2001::/23", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10",
	"ff00::/8",
)

func mustPrefixes(texts ...string) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(texts))
	for _, text := range texts {
		out = append(out, netip.MustParsePrefix(text))
	}
	return out
}

// IsGlobal mirrors ipaddress.IPv4Address.is_global / IPv6Address.is_global
// closely enough for endpoint selection: publicly routable addresses only.
//
// This is the single implementation. barf/device carried a second,
// hand-rolled one built from netip's predicates plus a switch; it was
// checked against CPython's ipaddress over the ranges below and lost, so
// it was deleted and its callers now come here. It disagreed with Python
// on 0.0.0.0/8 and on 64:ff9b:1::/48, 100::/64, 2001::/23 and 2002::/16,
// reporting all of them globally routable. The prefix tables above are
// easier to check against Python's own `_private_networks` lists than a
// switch is, which is why this is the version that survived.
//
// KNOWN DIVERGENCE — multicast. Python returns is_global == True for
// 224.0.0.0/4 and ff00::/8, because its is_global is defined as "not in
// the private-networks tables" and multicast is not in them. This
// function returns false. That is deliberate: both call sites are
// choosing an address for something to *dial* — WGEndpoint picks the
// address peers connect to, and device's probe ordering picks which
// address to try first — and a multicast group is not dialable. Calling
// it "global" would let it be selected as a WireGuard endpoint. Do not
// "fix" this to match Python without checking both call sites.
func IsGlobal(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	ranges := nonGlobalV6
	if ip.Is4() {
		ranges = nonGlobalV4
	}
	for _, prefix := range ranges {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
