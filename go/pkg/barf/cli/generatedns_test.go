package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/client/netbox"
)

// iface mirrors the fixture helper in nix/modules/dns/tests/test_refresh_dns.py
// so the Go and Python implementations are pinned to the same cases.
func iface(mac string, addresses []string, name string, vm string, primaryIP4 string) netbox.Interface {
	out := netbox.Interface{Name: name}
	if mac != "" {
		out.PrimaryMACAddress = &netbox.MACAddress{MACAddress: mac}
	}
	if vm != "" {
		owner := &netbox.Owner{Name: vm}
		if primaryIP4 != "" {
			owner.PrimaryIP4 = &netbox.IPAddress{Address: primaryIP4}
		}
		out.VirtualMachine = owner
	}
	for _, a := range addresses {
		out.IPAddresses = append(out.IPAddresses, netbox.IPAddress{Address: a})
	}
	return out
}

func dhcpLines(interfaces []netbox.Interface) []string {
	out := []string{}
	for _, r := range reservations(interfaces) {
		out = append(out, r.DnsmasqLine())
	}
	return out
}

func assertLines(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestDualStackReservation(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("BC:24:11:6A:62:B3", []string{"10.3.2.10/23", "2602:fa6d:10:ffff::110/64"}, "internal", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{
		"dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0-internal",
	})
}

func TestV4OnlyReservation(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:01", []string{"10.3.2.50/23"}, "internal", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{"dhcp-host=AA:BB:CC:DD:EE:01,10.3.2.50,sea1-k8s-0-internal"})
}

func TestV6OnlyReservation(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:02", []string{"2602:fa6d:10:ffff::120/64"}, "internal", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{
		"dhcp-host=AA:BB:CC:DD:EE:02,[2602:fa6d:10:ffff::120],sea1-k8s-0-internal",
	})
}

func TestFirstAddressOfEachFamilyWins(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:03", []string{
			"10.3.2.60/23", "10.3.2.61/23",
			"2602:fa6d:10:ffff::130/64", "2602:fa6d:10:ffff::131/64",
		}, "internal", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{
		"dhcp-host=AA:BB:CC:DD:EE:03,10.3.2.60,[2602:fa6d:10:ffff::130],sea1-k8s-0-internal",
	})
}

func TestDuplicateMACFirstInterfaceWins(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:04", []string{"10.3.2.70/23"}, "eth0", "sea1-k8s-0", ""),
		iface("aa:bb:cc:dd:ee:04", []string{"10.3.2.71/23"}, "eth1", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{"dhcp-host=AA:BB:CC:DD:EE:04,10.3.2.70,sea1-k8s-0-eth0"})
}

func TestInterfaceWithoutAddressesDoesNotConsumeMAC(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:05", nil, "eth0", "sea1-k8s-0", ""),
		iface("AA:BB:CC:DD:EE:05", []string{"10.3.2.80/23"}, "eth1", "sea1-k8s-0", ""),
	})
	assertLines(t, got, []string{"dhcp-host=AA:BB:CC:DD:EE:05,10.3.2.80,sea1-k8s-0-eth1"})
}

func TestMissingMACOrOwnerSkipped(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("", []string{"10.3.2.90/23"}, "internal", "sea1-k8s-0", ""),
		iface("AA:BB:CC:DD:EE:06", []string{"10.3.2.91/23"}, "internal", "", ""),
	})
	assertLines(t, got, []string{})
}

func TestPrimaryInterfaceGetsBareDeviceHostname(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("BC:24:11:6A:62:B3",
			[]string{"10.3.2.10/23", "2602:fa6d:10:ffff::110/64"},
			"internal", "sea1-k8s-0", "10.3.2.10/23"),
	})
	assertLines(t, got, []string{
		"dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0",
	})
}

func TestSecondaryInterfaceKeepsSuffix(t *testing.T) {
	got := dhcpLines([]netbox.Interface{
		iface("AA:BB:CC:DD:EE:07", []string{"10.3.2.99/23"}, "eth1", "sea1-k8s-0", "10.3.2.10/23"),
	})
	assertLines(t, got, []string{"dhcp-host=AA:BB:CC:DD:EE:07,10.3.2.99,sea1-k8s-0-eth1"})
}

func TestDHCPHostnameSanitisation(t *testing.T) {
	// Spaces become underscores in clean_hostname, then any non
	// alphanumeric survivor becomes a dash.
	if got := dhcpHostname("Rack 3 / Switch", "Ethernet1/1", false); got != "rack-3---switch-ethernet1-1" {
		t.Errorf("hostname = %q", got)
	}
	if got := dhcpHostname("SEA1-Core", "eth0", true); got != "sea1-core" {
		t.Errorf("primary hostname = %q", got)
	}
}

func TestDNSLines(t *testing.T) {
	hosts := []netbox.Host{
		{
			Name:       "sea1-core",
			PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.6/23"},
			PrimaryIP6: &netbox.IPAddress{Address: "2602:fa6d:10::6/64"},
			Interfaces: []netbox.Interface{
				{Name: "IPMI", IPAddresses: []netbox.IPAddress{{Address: "10.3.1.6/23"}}},
			},
		},
		{Name: "sea1-ap-0", PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.7/23"}},
		{Name: "das-thing", PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.8/23"}},
		{Name: "no-address"},
	}

	var warned []string
	got := dnsLines(hosts, "example.org", func(h string) { warned = append(warned, h) })
	assertLines(t, got, []string{
		"address=/sea1-core.example.org/2602:fa6d:10::6",
		"address=/sea1-core.example.org/10.3.2.6",
		"ptr-record=6.2.3.10.in-addr.arpa,sea1-core.example.org",
		"address=/ipmi.sea1-core.example.org/10.3.1.6",
		"ptr-record=6.1.3.10.in-addr.arpa,sea1-core.example.org",
	})
	if len(warned) != 1 || warned[0] != "no-address" {
		t.Errorf("warned = %v, want [no-address]", warned)
	}
}

func TestKeaReservations(t *testing.T) {
	hosts4, hosts6 := keaReservations(reservations([]netbox.Interface{
		iface("BC:24:11:6A:62:B3", []string{"10.3.2.10/23", "2602:fa6d:10:ffff::110/64"},
			"internal", "sea1-k8s-0", "10.3.2.10/23"),
		iface("AA:BB:CC:DD:EE:02", []string{"2602:fa6d:10:ffff::120/64"}, "eth1", "sea1-k8s-1", ""),
	}))

	if len(hosts4) != 1 || hosts4[0] != (keaHost4{
		HWAddress: "BC:24:11:6A:62:B3", IPAddress: "10.3.2.10", Hostname: "sea1-k8s-0",
	}) {
		t.Errorf("hosts4 = %+v", hosts4)
	}
	if len(hosts6) != 2 || hosts6[1].HWAddress != "AA:BB:CC:DD:EE:02" ||
		len(hosts6[1].IPAddresses) != 1 || hosts6[1].IPAddresses[0] != "2602:fa6d:10:ffff::120" ||
		hosts6[1].Hostname != "sea1-k8s-1-eth1" {
		t.Errorf("hosts6 = %+v", hosts6)
	}
}

// fakeNetboxServer serves the two GraphQL documents from a fixture, so
// the command tests exercise the real client without a live NetBox.
func fakeNetboxServer(t *testing.T, dns netbox.DNSResult, dhcp netbox.DHCPResult) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var data any = dns
		if strings.Contains(body.Query, "interface_list") && !strings.Contains(body.Query, "device_list") {
			data = dhcp
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// useFakeNetbox points the generate commands at srv for one test.
func useFakeNetbox(t *testing.T, srv *httptest.Server) {
	t.Helper()
	old := newNetbox
	t.Cleanup(func() { newNetbox = old })
	newNetbox = func(context.Context) (netboxSource, error) {
		return netbox.New(netbox.Options{Endpoint: srv.URL, Token: "test-token", HTTPClient: srv.Client()})
	}
}

func sampleData() (netbox.DNSResult, netbox.DHCPResult) {
	dns := netbox.DNSResult{
		Devices: []netbox.Host{{
			Name:       "sea1-core",
			PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.6/23"},
			PrimaryIP6: &netbox.IPAddress{Address: "2602:fa6d:10::6/64"},
		}},
		VirtualMachines: []netbox.Host{{
			Name:       "sea1-k8s-0",
			PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.10/23"},
		}},
	}
	dhcp := netbox.DHCPResult{
		VMInterfaces: []netbox.Interface{
			iface("BC:24:11:6A:62:B3", []string{"10.3.2.10/23", "2602:fa6d:10:ffff::110/64"},
				"internal", "sea1-k8s-0", "10.3.2.10/23"),
		},
	}
	return dns, dhcp
}

func TestGenerateDNSCommand(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dns", "--domain", "example.org"); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"address=/sea1-core.example.org/2602:fa6d:10::6",
		"address=/sea1-core.example.org/10.3.2.6",
		"ptr-record=6.2.3.10.in-addr.arpa,sea1-core.example.org",
		"address=/sea1-k8s-0.example.org/10.3.2.10",
		"ptr-record=10.2.3.10.in-addr.arpa,sea1-k8s-0.example.org",
		"",
	}, "\n")
	if got := h.out.String(); got != want {
		t.Errorf("dns output mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestGenerateDNSWithDHCPMatchesRefreshDNS(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dns", "--domain", "example.org", "--with-dhcp"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.HasPrefix(out, dnsmasqHeader+"\n") {
		t.Errorf("missing the refresh_dns header:\n%s", out)
	}
	wantLine := "dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0\n"
	if !strings.HasSuffix(out, wantLine) {
		t.Errorf("reservation line missing:\n%s", out)
	}
}

func TestGenerateDNSJSON(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dns", "--domain", "example.org", "--json"); err != nil {
		t.Fatal(err)
	}
	var report dnsJSON
	if err := json.Unmarshal(h.out.Bytes(), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out.String())
	}
	if len(report.Addresses) != 3 || len(report.PTRRecords) != 2 {
		t.Errorf("report = %+v", report)
	}
	if report.Addresses[0].FQDN != "sea1-core.example.org" || report.Addresses[0].IP != "2602:fa6d:10::6" {
		t.Errorf("first address = %+v", report.Addresses[0])
	}
}

func TestGenerateDNSRefusesEmptyNetbox(t *testing.T) {
	h := newHarness(t)
	useFakeNetbox(t, fakeNetboxServer(t, netbox.DNSResult{}, netbox.DHCPResult{}))

	err := h.run(t, "generate", "dns")
	if err == nil || !strings.Contains(err.Error(), "refusing to render") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestGenerateDHCPCommand(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dhcp"); err != nil {
		t.Fatal(err)
	}
	want := "dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0\n"
	if got := h.out.String(); got != want {
		t.Errorf("dhcp output = %q, want %q", got, want)
	}
}

func TestGenerateDHCPKeaFiles(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	dir := t.TempDir()
	p4 := filepath.Join(dir, "hosts4.json")
	p6 := filepath.Join(dir, "hosts6.json")
	if err := h.run(t, "generate", "dhcp", "--format", "kea", "--output4", p4, "--output6", p6); err != nil {
		t.Fatal(err)
	}

	body4, err := os.ReadFile(p4)
	if err != nil {
		t.Fatal(err)
	}
	var hosts4 []keaHost4
	if err := json.Unmarshal(body4, &hosts4); err != nil {
		t.Fatalf("hosts4 not JSON: %v\n%s", err, body4)
	}
	if len(hosts4) != 1 || hosts4[0].IPAddress != "10.3.2.10" || hosts4[0].Hostname != "sea1-k8s-0" {
		t.Errorf("hosts4 = %+v", hosts4)
	}

	body6, err := os.ReadFile(p6)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body6), `"ip-addresses"`) ||
		!strings.Contains(string(body6), "2602:fa6d:10:ffff::110") {
		t.Errorf("hosts6 = %s", body6)
	}
}

func TestGenerateDHCPRejectsUnknownFormat(t *testing.T) {
	h := newHarness(t)
	err := h.run(t, "generate", "dhcp", "--format", "bind")
	if err == nil || !strings.Contains(err.Error(), "unknown --format") {
		t.Fatalf("err = %v, want an unknown-format error", err)
	}
}

func TestGenerateDHCPRefusesEmptyNetbox(t *testing.T) {
	h := newHarness(t)
	useFakeNetbox(t, fakeNetboxServer(t, netbox.DNSResult{}, netbox.DHCPResult{}))

	err := h.run(t, "generate", "dhcp")
	if err == nil || !strings.Contains(err.Error(), "refusing to render") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestDNSDomainPrecedence(t *testing.T) {
	t.Setenv("DNS_DOMAIN", "env.example")
	if got := dnsDomain("flag.example"); got != "flag.example" {
		t.Errorf("flag should win, got %q", got)
	}
	if got := dnsDomain(""); got != "env.example" {
		t.Errorf("env should be used, got %q", got)
	}
	t.Setenv("DNS_DOMAIN", "")
	if got := dnsDomain(""); got != defaultDNSDomain {
		t.Errorf("default = %q", got)
	}
}
