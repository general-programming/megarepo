package model

import (
	"net/netip"
	"testing"
)

// Ground truth captured from CPython:
//
//	python3 -c "import ipaddress; print(ipaddress.ip_address(a).is_global)"
//
// barf/device used to carry a second implementation of this; it was
// deleted in favour of this one after losing on the cases marked below,
// so this table is also the record of why.
func TestIsGlobalMatchesPythonIsGlobal(t *testing.T) {
	tests := []struct {
		addr string
		want bool
		note string
	}{
		{"8.8.8.8", true, ""},
		{"1.1.1.1", true, ""},
		{"23.150.40.5", true, ""},
		{"2602:fd37::1", true, ""},
		{"2001:470:1::5", true, ""},
		{"2600:3c00::1", true, ""},
		{"::ffff:8.8.8.8", true, "v4-mapped unwraps to a global v4"},

		{"0.0.0.0", false, ""},
		{"0.1.2.3", false, "0.0.0.0/8; the old device copy said true"},
		{"10.0.0.1", false, ""},
		{"100.64.0.1", false, "CGNAT"},
		{"127.0.0.1", false, ""},
		{"169.254.1.1", false, ""},
		{"172.16.0.1", false, ""},
		{"192.0.0.1", false, ""},
		{"192.0.2.1", false, "TEST-NET-1"},
		{"192.168.1.1", false, ""},
		{"198.18.0.1", false, "benchmarking"},
		{"198.51.100.1", false, "TEST-NET-2"},
		{"203.0.113.1", false, "TEST-NET-3"},
		{"240.0.0.1", false, "reserved"},
		{"255.255.255.255", false, ""},

		{"::", false, ""},
		{"::1", false, ""},
		{"64:ff9b:1::1", false, "NAT64 local-use; the old device copy said true"},
		{"100::1", false, "discard-only; the old device copy said true"},
		{"2001::1", false, "Teredo/ORCHID; the old device copy said true"},
		{"2001:db8::1", false, "documentation"},
		{"2002::1", false, "6to4; the old device copy said true"},
		{"fc00::1", false, "ULA"},
		{"fd00::1", false, "ULA"},
		{"fe80::1", false, "link-local"},
	}
	for _, tc := range tests {
		if got := IsGlobal(netip.MustParseAddr(tc.addr)); got != tc.want {
			t.Errorf("IsGlobal(%s) = %v, want %v (%s)", tc.addr, got, tc.want, tc.note)
		}
	}
}

// Deliberate divergence from Python, documented on IsGlobal: multicast is
// "global" to ipaddress but is not a dialable endpoint, and both callers
// are picking an address to dial. Pin it so the divergence stays a
// decision rather than becoming an accident.
func TestIsGlobalTreatsMulticastAsNonGlobal(t *testing.T) {
	for _, addr := range []string{"224.0.0.1", "239.1.2.3", "ff02::1"} {
		if IsGlobal(netip.MustParseAddr(addr)) {
			t.Errorf("IsGlobal(%s) = true; multicast must not be selectable as an endpoint", addr)
		}
	}
}

func TestIsGlobalRejectsTheZeroAddr(t *testing.T) {
	if IsGlobal(netip.Addr{}) {
		t.Error("IsGlobal(invalid) = true")
	}
}
