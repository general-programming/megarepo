package model

import (
	"fmt"
	"net/netip"
	"strings"
)

// ParseAddress parses a network.yml address; a bare one gets its family's
// host prefix (/32, /128), matching Python's ipaddress.ip_interface.
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

// The subset of Python's non-global ranges that matters for wg_endpoint.
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

// IsGlobal mirrors ipaddress.is_global closely enough for endpoint selection;
// the prefix tables above are easier to diff against Python's own
// `_private_networks` lists than netip predicates would be.
//
// KNOWN DIVERGENCE — multicast: Python returns True for 224.0.0.0/4 and
// ff00::/8, this false, because both call sites pick an address to *dial*.
// Do not "fix" this without checking both.
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
