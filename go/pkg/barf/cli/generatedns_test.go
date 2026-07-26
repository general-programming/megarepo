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

// iface mirrors the fixture helper in nix/modules/dns/tests/test_refresh_dns.py,
// pinning Go and Python to the same cases.
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
	for _, r := range reservations(interfaces, nil) {
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
	// clean_hostname: spaces become underscores, then any non-alphanumeric
	// survivor becomes a dash.
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
	got := dnsLines(hosts, "example.org", func(h, _ string) { warned = append(warned, h) })
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

func reservationsNoWarn(interfaces []netbox.Interface) []reservation {
	return reservations(interfaces, nil)
}

func TestKeaReservations(t *testing.T) {
	hosts4, hosts6 := keaReservations(reservationsNoWarn([]netbox.Interface{
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

// fakeNetboxServer serves the two GraphQL documents from a fixture, so the
// command tests exercise the real client without a live NetBox.
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

// Regression: `--json` returned before `--with-dhcp`'s FetchDHCP call, so
// `generate dns --with-dhcp --json` silently dropped the DHCP reservations
// instead of including or refusing them.
func TestGenerateDNSJSONWithDHCPIncludesReservations(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dns", "--domain", "example.org", "--json", "--with-dhcp"); err != nil {
		t.Fatal(err)
	}
	var report dnsJSON
	if err := json.Unmarshal(h.out.Bytes(), &report); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out.String())
	}
	if len(report.Addresses) != 3 || len(report.PTRRecords) != 2 {
		t.Errorf("dns fields regressed: %+v", report)
	}
	if len(report.DHCPReservations) != 1 {
		t.Fatalf("dhcp_reservations = %+v, want exactly the one reservation sampleData carries", report.DHCPReservations)
	}
	got := report.DHCPReservations[0]
	if got.MAC != "BC:24:11:6A:62:B3" || got.Hostname != "sea1-k8s-0" ||
		got.IPv4 != "10.3.2.10" || got.IPv6 != "2602:fa6d:10:ffff::110" {
		t.Errorf("reservation = %+v", got)
	}
}

// Without --with-dhcp, --json's shape must stay exactly as before: no
// dhcp_reservations key at all (omitempty), not an empty array.
func TestGenerateDNSJSONWithoutDHCPOmitsReservationsKey(t *testing.T) {
	h := newHarness(t)
	dns, dhcp := sampleData()
	useFakeNetbox(t, fakeNetboxServer(t, dns, dhcp))

	if err := h.run(t, "generate", "dns", "--domain", "example.org", "--json"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "dhcp_reservations") {
		t.Errorf("dhcp_reservations present without --with-dhcp:\n%s", h.out.String())
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

// Regression: a NetBox device with a null `name` decoded to "" and the
// renderer emitted a malformed `address=/.domain/10.0.0.5` with exit 0.
func TestUnnamedNetboxDeviceIsSkipped(t *testing.T) {
	hosts := []netbox.Host{
		{Name: "", PrimaryIP4: &netbox.IPAddress{Address: "10.0.0.5/24"}},
		{Name: "   ", PrimaryIP6: &netbox.IPAddress{Address: "2602:fa6d::5/64"}},
		{Name: "sea1-core", PrimaryIP4: &netbox.IPAddress{Address: "10.3.2.6/23"}},
	}

	var warned []string
	got := dnsLines(hosts, "example.org", func(h, reason string) {
		warned = append(warned, h+": "+reason)
	})

	assertLines(t, got, []string{
		"address=/sea1-core.example.org/10.3.2.6",
		"ptr-record=6.2.3.10.in-addr.arpa,sea1-core.example.org",
	})
	for _, line := range got {
		if strings.HasPrefix(line, "address=/.") {
			t.Errorf("malformed record emitted for an unnamed device: %q", line)
		}
	}
	if len(warned) != 2 {
		t.Fatalf("warned = %v, want one entry per unnamed device", warned)
	}
	for _, w := range warned {
		if !strings.Contains(w, "no name") {
			t.Errorf("warning does not explain itself: %q", w)
		}
	}
}

// Same shape one level down: a null interface name became a reservation
// hostname with a dangling "-" suffix.
func TestUnnamedSecondaryInterfaceIsSkipped(t *testing.T) {
	var warned []string
	res := reservations([]netbox.Interface{
		// Secondary (its v4 is not the owner's primary): must skip.
		iface("AA:BB:CC:DD:EE:01", []string{"10.0.0.9/24"}, "", "sea1-k8s-0", "10.0.0.1/24"),
		// Primary: the interface name is never used, so a null one is fine.
		iface("AA:BB:CC:DD:EE:02", []string{"10.0.0.2/24"}, "", "sea1-k8s-1", "10.0.0.2/24"),
	}, func(h, reason string) { warned = append(warned, h+": "+reason) })

	if len(res) != 1 || res[0].Hostname != "sea1-k8s-1" {
		t.Fatalf("reservations = %+v, want only the primary-interface one", res)
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "sea1-k8s-0") {
		t.Errorf("warned = %v, want one warning naming sea1-k8s-0", warned)
	}
	for _, r := range res {
		if strings.HasSuffix(r.Hostname, "-") {
			t.Errorf("reservation hostname has a dangling suffix: %q", r.Hostname)
		}
	}
}

// Pins the two ways Go's JSON drifted from `json.dump(hosts, f, indent=1)`
// in nix/modules/kea/refresh_kea.py: a trailing newline Python omits, and
// HTML-escaping & < > where Python escapes non-ASCII instead. Either makes
// a cmp-based "did reservations change?" gate flap every run.
func TestKeaFilesAreBytewiseIdenticalToPython(t *testing.T) {
	dir := t.TempDir()
	path4 := filepath.Join(dir, "hosts4.json")
	path6 := filepath.Join(dir, "hosts6.json")

	h := newHarness(t)
	dhcp := netbox.DHCPResult{VMInterfaces: []netbox.Interface{
		iface("AA:BB:CC:DD:EE:01", []string{"10.0.0.2/24", "2602:fa6d::2/64"},
			"eth0", "r&d-lab-caf\u00e9", "10.0.0.2/24"),
	}}
	useFakeNetbox(t, fakeNetboxServer(t, netbox.DNSResult{}, dhcp))

	if err := h.run(t, "generate", "dhcp", "--format", "kea",
		"--output4", path4, "--output6", path6); err != nil {
		t.Fatal(err)
	}

	body4, err := os.ReadFile(path4)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(string(body4), "\n") {
		t.Errorf("kea file ends in a newline; refresh_kea.py's json.dump does not:\n%q", body4)
	}
	// Python does not HTML-escape; hostnames are sanitized to [a-z0-9-]
	// first, so TestPythonJSONMatchesJSONDumps covers escaping directly.
	if strings.ContainsAny(string(body4), "&<>") {
		t.Errorf("unexpected raw HTML character in the fixture:\n%s", body4)
	}
	if strings.Contains(string(body4), "é") {
		t.Errorf("non-ASCII emitted raw; Python's ensure_ascii=True escapes it:\n%s", body4)
	}
	if !strings.Contains(string(body4), `caf\u00e9`) {
		t.Errorf("non-ASCII was not \\u-escaped:\n%s", body4)
	}
	// The exact bytes json.dump(..., indent=1) produces.
	want4 := "[\n {\n  \"hw-address\": \"AA:BB:CC:DD:EE:01\",\n  \"ip-address\": \"10.0.0.2\"," +
		"\n  \"hostname\": \"r-d-lab-caf\\u00e9\"\n }\n]"
	if string(body4) != want4 {
		t.Errorf("hosts4.json\n got %q\nwant %q", body4, want4)
	}

	body6, err := os.ReadFile(path6)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(string(body6), "\n") {
		t.Errorf("hosts6.json ends in a newline:\n%q", body6)
	}
}

func TestPythonJSONMatchesJSONDumps(t *testing.T) {
	cases := []struct {
		name   string
		value  any
		indent string
		want   string
	}{
		{"html characters stay literal", []string{"a&b", "<c>"}, " ",
			"[\n \"a&b\",\n \"<c>\"\n]"},
		{"non-ascii is \\u-escaped", []string{"café"}, " ", "[\n \"caf\\u00e9\"\n]"},
		{"astral planes use surrogate pairs", []string{"\U0001F408"}, " ",
			"[\n \"\\ud83d\\udc08\"\n]"},
		{"empty array", []string{}, " ", "[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pythonJSON(tc.value, tc.indent)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("pythonJSON\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}
