package render_test

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// The fleet has no edgeos, cisco, dnos6 or dnos9 host, so those vendors get no
// snapshot from TestGoldenSnapshots. These cover them instead: edgeos by
// flipping a host's `type` in memory, the IOS family off a synthetic NetBox
// device.

// Renders each snapshotted host as EdgeOS.
func TestEdgeOSSnapshots(t *testing.T) {
	network := loadFleet(t)
	dir := filepath.Join(testdataDir(t), "edgeos")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read edgeos fixtures: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no edgeos fixtures")
	}

	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".conf")
		t.Run(name, func(t *testing.T) {
			host, ok := network.Host(name)
			if !ok {
				t.Fatalf("fixture %s has no host in network.yml", name)
			}
			asEdgeOS := *host
			asEdgeOS.DeviceType = "edgeos"

			got, err := vendor.Render(&asEdgeOS, network, fakeSecrets{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			checkSnapshot(t, got, "edgeos", entry.Name())
		})
	}
}

// The NetBox-shaped device the IOS fixtures were captured from: one port of
// every shape the templates branch on (access, trunk with/without native vlan,
// a LAG, an SVI in a VRF).
func netboxSwitch(deviceType string) *render.IOSDevice {
	address := func(text string) []model.Address {
		prefix := netip.MustParsePrefix(text)
		return []model.Address{{IP: prefix.Addr(), Prefix: prefix.Bits()}}
	}
	vlan := func(vid int, name string) *model.VLAN { return &model.VLAN{VID: vid, Name: name} }

	device := &render.IOSDevice{
		Hostname:     "fmt2-con-sw-140752-1",
		DeviceType:   deviceType,
		DefaultRoute: "10.65.67.1",
		Interfaces: []render.IOSInterface{
			{
				Name: "GigabitEthernet1/0/1", Type: "1000BASE_T", Mode: "ACCESS",
				Description: "workstation", Enabled: true,
				UntaggedVLAN: vlan(10, "office lan"),
			},
			{
				Name: "GigabitEthernet1/0/2", Type: "1000BASE_T", Mode: "TAGGED",
				Description: "uplink", Enabled: true, LagID: "1",
				UntaggedVLAN: vlan(99, "native"),
				TaggedVLANs:  []model.VLAN{{VID: 10, Name: "office lan"}, {VID: 20, Name: "servers"}},
			},
			{
				Name: "GigabitEthernet1/0/3", Type: "1000BASE_T", Mode: "TAGGED_ALL",
				Enabled: true,
			},
			{Name: "1", Type: "LAG", Description: "port-channel", Enabled: true},
			{
				Name: "Vlan10", Type: "VIRTUAL", Description: "svi", Enabled: true,
				VRF: "internal", Addresses: address("10.65.68.2/24"),
			},
		},
	}
	device.VLANs = render.IOSVLANs(device.Interfaces, map[int]model.VLAN{
		10: {VID: 10}, 20: {VID: 20}, 99: {VID: 99},
	})
	return device
}

func TestIOSFamilySnapshots(t *testing.T) {
	global := loadFleet(t).Global

	cases := []struct {
		fixture    string
		deviceType string
		render     func(*render.IOSDevice, model.GlobalMeta, render.SecretSource) (string, error)
	}{
		{"cisco.conf", "cisco", render.RenderCiscoIOS},
		{"dnos6.conf", "dnos6", render.RenderDNOS},
		{"dnos9.conf", "dnos9", render.RenderDNOS},
	}
	for _, tc := range cases {
		t.Run(tc.deviceType, func(t *testing.T) {
			got, err := tc.render(netboxSwitch(tc.deviceType), global, fakeSecrets{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			checkSnapshot(t, got, "ios", tc.fixture)
		})
	}
}

// network_devices/dnos9.j2 includes common/dnos6.j2, so output is identical.
func TestDNOS9MatchesDNOS6(t *testing.T) {
	global := loadFleet(t).Global
	six, err := render.RenderDNOS(netboxSwitch("dnos6"), global, fakeSecrets{})
	if err != nil {
		t.Fatalf("dnos6: %v", err)
	}
	nine, err := render.RenderDNOS(netboxSwitch("dnos9"), global, fakeSecrets{})
	if err != nil {
		t.Fatalf("dnos9: %v", err)
	}
	if six != nine {
		t.Error("dnos6 and dnos9 renders differ; they share common/dnos6.j2")
	}
}

// An external host is a link's far end that barf does not manage.
func TestExternalIsNotRendered(t *testing.T) {
	if vendor.Templatable("external") {
		t.Error("external should not be templatable")
	}
	if _, ok := vendor.Renderer("external"); ok {
		t.Error("external should have no renderer")
	}

	network := loadFleet(t)
	var found bool
	for i := range network.Hosts {
		host := &network.Hosts[i]
		if host.DeviceType != "external" {
			continue
		}
		found = true
		if _, err := vendor.Render(host, network, fakeSecrets{}); err == nil {
			t.Errorf("%s: rendering an external host should fail", host.Hostname)
		}
	}
	if !found {
		t.Skip("network.yml has no external host")
	}
}

// A (role, devicetype) pair Python has no template for must fail here too.
func TestRolesOutsideTheTemplateMatrix(t *testing.T) {
	network := loadFleet(t)
	host, ok := network.Host("fmt2-vpn-spine-1")
	if !ok {
		t.Fatal("fmt2-vpn-spine-1 missing from network.yml")
	}

	cases := []struct{ deviceType, role string }{
		{"vyos", "network_devices"},
		{"linux", "core"},
		{"mikrotik", "network_devices"},
		{"edgeos", "core"},
		{"cisco", "vpn"},
		{"dnos6", "vpn"},
		{"dnos9", "core"},
	}
	for _, tc := range cases {
		t.Run(tc.deviceType+"/"+tc.role, func(t *testing.T) {
			candidate := *host
			candidate.DeviceType = tc.deviceType
			candidate.Role = tc.role
			if _, err := vendor.Render(&candidate, network, fakeSecrets{}); err == nil {
				t.Errorf("%s in role %s rendered; Python has no template for it",
					tc.deviceType, tc.role)
			}
		})
	}
}

// NetBox platform-slug spellings from VENDOR_MAP hit the same renderers.
func TestAliasesResolve(t *testing.T) {
	for alias, canonical := range map[string]string{
		"cisco-ios": "cisco", "dnos-6": "dnos6", "dnos-9": "dnos9",
	} {
		aliased, ok := vendor.Renderer(alias)
		if !ok {
			t.Errorf("%s: no renderer", alias)
			continue
		}
		direct, _ := vendor.Renderer(canonical)
		if aliased != direct {
			t.Errorf("%s should resolve to the %s renderer", alias, canonical)
		}
	}
}

// Deliberate divergence: BaseHost.vlans is a set, so Python's stanza order
// varies with the hash seed; byte parity needs a stable order.
func TestIOSVLANsAreOrdered(t *testing.T) {
	interfaces := []render.IOSInterface{
		{UntaggedVLAN: &model.VLAN{VID: 99, Name: "native"}},
		{TaggedVLANs: []model.VLAN{{VID: 20}, {VID: 10}, {VID: 20}}},
		{UntaggedVLAN: &model.VLAN{VID: 10}},
	}

	all := render.IOSVLANs(interfaces, nil)
	want := []int{10, 20, 99}
	if len(all) != len(want) {
		t.Fatalf("got %d vlans, want %d: %v", len(all), len(want), all)
	}
	for i, vid := range want {
		if all[i].VID != vid {
			t.Errorf("vlan %d: got vid %d, want %d", i, all[i].VID, vid)
		}
	}

	// A vlan_map filters out VLANs the device may not declare.
	filtered := render.IOSVLANs(interfaces, map[int]model.VLAN{20: {VID: 20}})
	if len(filtered) != 1 || filtered[0].VID != 20 {
		t.Errorf("vlan_map filtering: got %v, want just vid 20", filtered)
	}
}

// The legacy template keeps only the last three digits of the link port.
func TestEdgeOSTunnelNamesTruncate(t *testing.T) {
	network := loadFleet(t)
	host, ok := network.Host("fmt2-vpn-spine-1")
	if !ok {
		t.Fatal("fmt2-vpn-spine-1 missing from network.yml")
	}
	asEdgeOS := *host
	asEdgeOS.DeviceType = "edgeos"

	got, err := vendor.Render(&asEdgeOS, network, fakeSecrets{})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Port 51067 -> wg067, not wg51067.
	if !strings.Contains(got, "set interfaces wireguard wg067 ") {
		t.Error("expected the truncated tunnel name wg067")
	}
	if strings.Contains(got, "wireguard wg51067") {
		t.Error("edgeos must not use the whole port as the tunnel name")
	}
}
