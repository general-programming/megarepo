package model

import "testing"

// Regression: FirewallAddressMember.Version() silently defaulted to 4 for
// any unparseable member ("10.0.0./24", a bad prefix length like
// "10.0.0.0/33", or v6 garbage like "zz::/64"), so a typo in a firewall
// group's address list would sort into the v4 group (or wherever) instead
// of failing the render. Python's ipaddress.ip_network(strict=False).version
// raises ValueError on all three; Version must now error too.
func TestFirewallAddressMemberVersionRejectsInvalidInput(t *testing.T) {
	valid := []struct {
		address string
		want    int
	}{
		{"10.0.0.0/24", 4},
		{"10.0.0.5", 4},
		{"2001:db8::/32", 6},
		{"2001:db8::1", 6},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.address, func(t *testing.T) {
			m := FirewallAddressMember{Address: tc.address}
			got, err := m.Version()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Version() = %d, want %d", got, tc.want)
			}
		})
	}

	invalid := []string{
		"10.0.0./24",     // unparseable address
		"10.0.0.0/33",    // prefix length past a v4 address's 32 bits
		"zz::/64",        // unparseable v6 address
		"not-an-ip",      // not an address at all
		"2001:db8::/999", // prefix length past a v6 address's 128 bits
	}
	for _, address := range invalid {
		t.Run("invalid/"+address, func(t *testing.T) {
			m := FirewallAddressMember{Address: address}
			if _, err := m.Version(); err == nil {
				t.Errorf("Version() on %q: want an error, got none", address)
			}
		})
	}
}

// MembersV must propagate an invalid member's error rather than silently
// dropping or misclassifying it.
func TestFirewallAddressGroupMembersVPropagatesError(t *testing.T) {
	g := FirewallAddressGroup{
		Name: "bad-group",
		Members: []FirewallAddressMember{
			{Address: "10.0.0.0/24"},
			{Address: "10.0.0./24"}, // invalid
		},
	}
	if _, err := g.MembersV(4); err == nil {
		t.Error("MembersV: want an error for the invalid member, got none")
	}
}

// Regression: Link.GetIP silently masked host bits ("172.31.255.21/31" ->
// .20/.21) instead of erroring, so a typo'd network.yml link network would
// deploy at a different address than what was written. Python's get_ip
// constructs ipaddress.IPv4Network/IPv6Network with the default
// strict=True, which raises ValueError on host bits set.
func TestLinkGetIPRejectsHostBitsSet(t *testing.T) {
	link := Link{A: "a", B: "b", Network: "172.31.255.21/31"}
	if _, err := link.GetIP("a", false); err == nil {
		t.Error("GetIP: want an error for a network with host bits set, got none")
	}
	if _, err := link.GetIP("b", true); err == nil {
		t.Error("GetIP: want an error for a network with host bits set, got none")
	}
}

// The aligned network address must keep working exactly as before.
func TestLinkGetIPAcceptsAlignedNetwork(t *testing.T) {
	link := Link{A: "a", B: "b", Network: "172.31.255.20/31"}
	got, err := link.GetIP("a", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "172.31.255.20" {
		t.Errorf("side A ip = %q, want 172.31.255.20", got)
	}
	got, err = link.GetIP("b", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "172.31.255.21/31" {
		t.Errorf("side B ip = %q, want 172.31.255.21/31", got)
	}
}

// GetIP on an unparseable network must also error, not return "".
func TestLinkGetIPRejectsUnparseableNetwork(t *testing.T) {
	link := Link{A: "a", B: "b", Network: "not-a-network"}
	if _, err := link.GetIP("a", false); err == nil {
		t.Error("GetIP: want an error for an unparseable network, got none")
	}
}

// An unnumbered link (no Network) is not an error: GetIP returns "" with a
// nil error, same as before.
func TestLinkGetIPUnnumbered(t *testing.T) {
	link := Link{A: "a", B: "b"}
	got, err := link.GetIP("a", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("GetIP on an unnumbered link = %q, want empty", got)
	}
}
