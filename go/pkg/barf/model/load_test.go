package model_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "network.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

const minimalGlobal = `
global_meta:
  search_domain: example.org
  nameservers: [10.0.0.1, 10.0.0.2]
  community_asn: 65000
  sites:
    sea: {id: 1, coords: [47.61, -122.33]}
    fmt2: {id: 2, coords: [37.55, -121.99]}
`

func TestLoadRealNetworkYML(t *testing.T) {
	network := loadReal(t)

	if got := len(network.Hosts); got != 17 {
		t.Errorf("host count = %d, want 17", got)
	}
	if got := network.Global.SearchDomain; got != "generalprogramming.org" {
		t.Errorf("search domain = %q", got)
	}
	if got := len(network.Global.Sites); got != 4 {
		t.Errorf("site count = %d, want 4", got)
	}
	if got := network.Global.CommunityASN; got != 4280805525 {
		t.Errorf("community asn = %d", got)
	}
}

func loadReal(t *testing.T) *model.Network {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(file), "..", "..", "..", "..",
		"projects", "barf", "network.yml")
	network, err := model.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return network
}

// `<<: *fmt2-leaf` inherits every profile key; the host's own keys win.
func TestProfileAnchorsMerge(t *testing.T) {
	network := loadReal(t)

	leaf, ok := network.Host("fmt2-vpn-leaf-1")
	if !ok {
		t.Fatal("fmt2-vpn-leaf-1 missing")
	}
	if leaf.DeviceType != "vyos" || leaf.Role != "vpn" || leaf.Site != "fmt2" {
		t.Errorf("profile keys not merged: %+v", leaf)
	}
	if leaf.ASN != 4280805526 {
		t.Errorf("asn = %d, want 4280805526", leaf.ASN)
	}
	if len(leaf.Networks) != 2 || leaf.Networks[0] != "10.65.67.0/24" {
		t.Errorf("networks = %v", leaf.Networks)
	}
	if len(leaf.ExtraConfig) != 5 {
		t.Errorf("extra_config lines = %d, want 5", len(leaf.ExtraConfig))
	}
	if len(leaf.Interfaces) != 4 {
		t.Errorf("interfaces = %d, want 4", len(leaf.Interfaces))
	}
	if len(leaf.NATMasquerades) != 1 || leaf.NATMasquerades[0].Rule != 10 {
		t.Errorf("nat masquerades = %+v", leaf.NATMasquerades)
	}
	if len(leaf.OSPF.Networks) != 1 || leaf.OSPF.Networks[0].Area != "0" {
		t.Errorf("ospf networks = %+v", leaf.OSPF.Networks)
	}
}

func TestNameserverInheritance(t *testing.T) {
	network := loadReal(t)

	inherits, _ := network.Host("fmt2-vpn-spine-1")
	if want := []string{"10.255.1.9", "1.1.1.1"}; !equal(inherits.Nameservers, want) {
		t.Errorf("inherited nameservers = %v, want %v", inherits.Nameservers, want)
	}

	overrides, _ := network.Host("sea1-vpn-spine-1")
	want := []string{"2606:4700:4700::1111", "2606:4700:4700::1001"}
	if !equal(overrides.Nameservers, want) {
		t.Errorf("own nameservers = %v, want %v", overrides.Nameservers, want)
	}
}

// `address:`, `addresses:`, and both families on one interface.
func TestInterfaceAddressForms(t *testing.T) {
	network := loadReal(t)

	leaf, _ := network.Host("sea1-vpn-leaf-1")
	dum0 := leaf.Interfaces[0]
	if got := len(dum0.Addresses); got != 2 {
		t.Fatalf("dum0 addresses = %d, want 2", got)
	}
	if got := dum0.Addresses[0].String(); got != "2602:fa6d:f:aaaa::f01/128" {
		t.Errorf("dum0 first address = %q", got)
	}
	if got := dum0.Addresses[1].String(); got != "10.255.2.9/32" {
		t.Errorf("dum0 second address = %q", got)
	}
	if got := leaf.ManagementAddress().String(); got != "2602:fa6d:f:aaaa::f01/128" {
		t.Errorf("management address = %q", got)
	}

	spine, _ := network.Host("fmt2-vpn-spine-1")
	eth0 := spine.Interfaces[0]
	if !eth0.DHCP || !eth0.IPv6Autoconf {
		t.Errorf("eth0 dhcp=%v autoconf=%v", eth0.DHCP, eth0.IPv6Autoconf)
	}
	if got := len(eth0.Addresses); got != 1 {
		t.Fatalf("eth0 addresses = %d, want 1", got)
	}
	if got := eth0.Addresses[0].String(); got != "2a0d:1a43:8008:420::1/64" {
		t.Errorf("eth0 address = %q", got)
	}
}

// Derived vs pinned ports, the uplink (inner key) as side A, and key paths.
func TestLinkDerivationAndSides(t *testing.T) {
	network := loadReal(t)

	var derived, pinned *model.Link
	for i := range network.Links {
		link := &network.Links[i]
		if link.A == "fmt2-vpn-spine-1" && link.B == "fmt2-vpn-leaf-1" {
			derived = link
		}
		if link.A == "fmt2-vpn-spine-1" && link.B == "oracle-vpn-1-1" {
			pinned = link
		}
	}
	if derived == nil {
		t.Fatal("derived spine-1 <-> leaf-1 link missing (is the uplink side A?)")
	}
	// ids 1 and 3: 51000 + 1*64 + 3.
	if derived.Port != 51067 {
		t.Errorf("derived port = %d, want 51067", derived.Port)
	}
	if derived.Pinned {
		t.Error("derived link marked pinned")
	}
	if !derived.Unnumbered() {
		t.Error("derived link should be unnumbered")
	}
	want := "wglink-fmt2-vpn-leaf-1--fmt2-vpn-spine-1-fmt2-vpn-spine-1"
	if got := derived.KeyPath("fmt2-vpn-spine-1"); got != want {
		t.Errorf("derived key path = %q, want %q", got, want)
	}

	if pinned == nil {
		t.Fatal("pinned spine-1 <-> oracle-vpn-1-1 link missing")
	}
	if pinned.Port != 51831 || !pinned.Pinned || !pinned.IPsec {
		t.Errorf("pinned link = %+v", pinned)
	}
	if pinned.Secret != "fmt2-oracle-vpn-1" || pinned.Network != "172.31.255.20/31" {
		t.Errorf("pinned link spec = %+v", pinned)
	}
	if got := pinned.KeyPath("fmt2-vpn-spine-1"); got != "wg-51831-fmt2-vpn-spine-1" {
		t.Errorf("pinned key path = %q", got)
	}
	// Side A takes the first usable /31 address, side B the next.
	if got, err := pinned.GetIP("fmt2-vpn-spine-1", true); err != nil || got != "172.31.255.20/31" {
		t.Errorf("side A ip = %q, err = %v", got, err)
	}
	if got, err := pinned.GetIP("oracle-vpn-1-1", false); err != nil || got != "172.31.255.21" {
		t.Errorf("side B ip = %q, err = %v", got, err)
	}
}

// Nothing is silently dropped: sea1-vpn-leaf-1's `management:`/`cloudinit:`
// match no typed field.
func TestRawKeepsUnmappedKeys(t *testing.T) {
	network := loadReal(t)

	leaf, _ := network.Host("sea1-vpn-leaf-1")
	if got, ok := leaf.Raw["management"]; !ok || got != "2602:fa6d:f:aaaa::f01" {
		t.Errorf("Raw[management] = %v (present=%v)", got, ok)
	}
	if got, ok := leaf.Raw["cloudinit"]; !ok || got != true {
		t.Errorf("Raw[cloudinit] = %v (present=%v)", got, ok)
	}
	// `cloudinit` is not the `cloud_init` key the typed field reads.
	if leaf.CloudInit {
		t.Error("CloudInit should be false: network.yml spells it `cloudinit`")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "unknown device type",
			body: minimalGlobal + "hosts:\n  a:\n    type: toaster\n    role: vpn\n",
			want: "Invalid host type toaster",
		},
		{
			name: "unknown site",
			body: minimalGlobal + "hosts:\n  a:\n    type: vyos\n    role: vpn\n    site: mars\n",
			want: "unknown site 'mars'",
		},
		{
			name: "duplicate site id",
			body: `
global_meta:
  community_asn: 65000
  sites:
    sea: {id: 1, coords: [1, 2]}
    dup: {id: 1, coords: [3, 4]}
hosts: {}
`,
			want: "id 1 used by both",
		},
		{
			name: "sites without community_asn",
			body: "global_meta:\n  sites:\n    sea: {id: 1, coords: [1, 2]}\nhosts: {}\n",
			want: "community_asn is missing",
		},
		{
			name: "link to unknown host",
			body: minimalGlobal + `hosts:
  a: {type: vyos, role: vpn, id: 1}
links:
  a:
    ghost: {}
`,
			want: "unknown host 'ghost'",
		},
		{
			name: "derived port needs ids",
			body: minimalGlobal + `hosts:
  a: {type: vyos, role: vpn, id: 1}
  b: {type: vyos, role: vpn}
links:
  a:
    b: {}
`,
			want: "needs an `id`",
		},
		{
			name: "duplicate port",
			body: minimalGlobal + `hosts:
  a: {type: vyos, role: vpn, id: 1}
  b: {type: vyos, role: vpn, id: 2}
  c: {type: vyos, role: vpn, id: 3}
links:
  a:
    b: 51000
  c:
    b: 51000
`,
			want: "used by both",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := model.Load(writeYAML(t, test.body))
			if err == nil {
				t.Fatalf("expected an error containing %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestBareIntLinkSpecIsPinned(t *testing.T) {
	network, err := model.Load(writeYAML(t, minimalGlobal+`hosts:
  a: {type: vyos, role: vpn, id: 1}
  b: {type: vyos, role: vpn, id: 2}
links:
  a:
    b: 51820
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(network.Links) != 1 {
		t.Fatalf("links = %d", len(network.Links))
	}
	link := network.Links[0]
	if link.A != "b" || link.B != "a" {
		t.Errorf("uplink (inner key) should be side A: %+v", link)
	}
	if link.Port != 51820 || !link.Pinned {
		t.Errorf("bare int spec should pin the port: %+v", link)
	}
}

func TestParseAddress(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"10.0.0.1", "10.0.0.1/32"},
		{"10.0.0.1/24", "10.0.0.1/24"},
		{"2602:fa6d:f:aaaa::f06", "2602:fa6d:f:aaaa::f06/128"},
		{"2A0D:1A43::1/64", "2a0d:1a43::1/64"},
	}
	for _, test := range tests {
		got, err := model.ParseAddress(test.in)
		if err != nil {
			t.Errorf("ParseAddress(%q): %v", test.in, err)
			continue
		}
		if got.String() != test.want {
			t.Errorf("ParseAddress(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	if _, err := model.ParseAddress("not-an-ip"); err == nil {
		t.Error("expected an error for a non-address")
	}
}

func TestWGEndpointPrefersGlobalV6(t *testing.T) {
	network := loadReal(t)

	spine, _ := network.Host("fmt2-vpn-spine-1")
	if got := spine.WGEndpoint(); got != "2a0d:1a43:8008:420::1" {
		t.Errorf("spine endpoint = %q, want the global v6", got)
	}

	hv, _ := network.Host("sea21-hv-egg-irl")
	if got := hv.WGEndpoint(); got != "209.251.245.111" {
		t.Errorf("v4-only host endpoint = %q", got)
	}

	// A private management address is not dialable: peers render no endpoint.
	nated, _ := network.Host("sea420-acc-v-hv2")
	if got := nated.WGEndpoint(); got != "" {
		t.Errorf("private-address host endpoint = %q, want empty", got)
	}
}

func TestResolvedInterfacesFlattensIncludes(t *testing.T) {
	network := loadReal(t)
	host, _ := network.Host("sea420-acc-v-hv2")

	got := host.Firewall.ResolvedInterfaces("internal-networks")
	want := []string{"bridge-internal", "linuxgemini1", "wg51078", "wg51142"}
	if !equal(got, want) {
		t.Errorf("resolved = %v, want %v", got, want)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Pins PyYAML's YAML 1.1 booleans plus Python truthiness: BaseHost.from_meta
// feeds `meta.get(field, default)` straight into an `if`, never bool()-cast.
// Regression: strconv.ParseBool rejects `yes`/`on`/`off` and reads the quoted
// string "no" as false — silent wrong-config with exit 0.
func TestYAML11BooleanSpellings(t *testing.T) {
	cases := []struct {
		spelling string
		want     bool
	}{
		{"yes", true},
		{"Yes", true},
		{"YES", true},
		{"on", true},
		{"On", true},
		{"true", true},
		{"True", true},
		{"1", true},
		// PyYAML leaves bare y/n as strings, truthy in Python.
		{"y", true},
		{"n", true},
		{`"no"`, true},
		{`'off'`, true},
		{`"false"`, true},
		{`""`, false},
		{"no", false},
		{"No", false},
		{"off", false},
		{"OFF", false},
		{"false", false},
		{"FALSE", false},
		{"0", false},
		{"null", false},
		{"~", false},
		{"", false},
	}

	for _, tc := range cases {
		t.Run("management_"+tc.spelling, func(t *testing.T) {
			network, err := model.Load(writeYAML(t, minimalGlobal+`hosts:
  a:
    type: vyos
    role: vpn
    interfaces:
      - name: eth0
        management: `+tc.spelling+"\n"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			host, _ := network.Host("a")
			if got := host.Interfaces[0].Management; got != tc.want {
				t.Errorf("management: %s -> %v, want %v", tc.spelling, got, tc.want)
			}
		})
	}

	// `enabled` is the one field defaulting to True in Python.
	for _, tc := range []struct {
		spelling string
		want     bool
	}{{"off", false}, {"no", false}, {`"no"`, true}, {"yes", true}} {
		t.Run("enabled_"+tc.spelling, func(t *testing.T) {
			network, err := model.Load(writeYAML(t, minimalGlobal+`hosts:
  a:
    type: vyos
    role: vpn
    interfaces:
      - name: eth0
        enabled: `+tc.spelling+"\n"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			host, _ := network.Host("a")
			if got := host.Interfaces[0].Enabled; got != tc.want {
				t.Errorf("enabled: %s -> %v, want %v", tc.spelling, got, tc.want)
			}
		})
	}

	// cloud_init sits on the host, not the interface.
	for _, tc := range []struct {
		spelling string
		want     bool
	}{{"yes", true}, {"on", true}, {`"no"`, true}, {"no", false}, {"false", false}} {
		t.Run("cloud_init_"+tc.spelling, func(t *testing.T) {
			network, err := model.Load(writeYAML(t, minimalGlobal+`hosts:
  a:
    type: vyos
    role: vpn
    cloud_init: `+tc.spelling+"\n"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			host, _ := network.Host("a")
			if got := host.CloudInit; got != tc.want {
				t.Errorf("cloud_init: %s -> %v, want %v", tc.spelling, got, tc.want)
			}
		})
	}
}

// For `<<: [*a, *b]` the EARLIER alias wins. Regression: Go let the later one
// win, silently rendering a different fabric.
func TestSequenceMergeEarlierAliasWins(t *testing.T) {
	network, err := model.Load(writeYAML(t, minimalGlobal+`
first: &first
  type: vyos
  role: vpn
  site: sea
  location: from-first

second: &second
  type: linux
  role: core
  site: fmt2
  location: from-second
  eapi_vrf: from-second

hosts:
  a:
    <<: [*first, *second]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	host, ok := network.Host("a")
	if !ok {
		t.Fatal("host a missing")
	}
	if host.DeviceType != "vyos" {
		t.Errorf("type = %q, want vyos (earlier alias wins)", host.DeviceType)
	}
	if host.Role != "vpn" {
		t.Errorf("role = %q, want vpn", host.Role)
	}
	if host.Site != "sea" {
		t.Errorf("site = %q, want sea", host.Site)
	}
	if host.SNMPLocation != "from-first" {
		t.Errorf("location = %q, want from-first", host.SNMPLocation)
	}
	// A key only the later alias has is still merged in.
	if host.EAPIVRF != "from-second" {
		t.Errorf("eapi_vrf = %q, want from-second", host.EAPIVRF)
	}
}

// The other half of the rule: an explicit key overrides everything merged.
func TestExplicitKeyBeatsSequenceMerge(t *testing.T) {
	network, err := model.Load(writeYAML(t, minimalGlobal+`
first: &first
  type: vyos
  role: vpn
  location: from-first

second: &second
  type: linux
  role: core
  location: from-second

hosts:
  a:
    <<: [*first, *second]
    location: from-host
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	host, _ := network.Host("a")
	if host.SNMPLocation != "from-host" {
		t.Errorf("location = %q, want from-host", host.SNMPLocation)
	}
}
