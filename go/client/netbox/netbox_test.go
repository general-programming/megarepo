package netbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newTestClient wires a Client at a httptest server.
func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := New(Options{Endpoint: srv.URL, Token: "nbt_test.secret"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("expected an error without a token")
	}
	if _, err := New(Options{Token: "   "}); err == nil {
		t.Fatal("expected an error for a blank token")
	}
}

func TestNewDefaults(t *testing.T) {
	c, err := New(Options{Token: "t"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.Endpoint() != DefaultEndpoint {
		t.Errorf("endpoint = %q, want %q", c.Endpoint(), DefaultEndpoint)
	}
	if c.userAgent != DefaultUserAgent {
		t.Errorf("user agent = %q, want %q", c.userAgent, DefaultUserAgent)
	}
	if c.http.Timeout != DefaultTimeout {
		t.Errorf("timeout = %v, want %v", c.http.Timeout, DefaultTimeout)
	}
}

func TestNewFromEnv(t *testing.T) {
	t.Setenv("NETBOX_API_KEY", "")
	t.Setenv("NETBOX_URL", "")
	if _, err := NewFromEnv(Options{}); err == nil {
		t.Fatal("expected an error when NETBOX_API_KEY is unset")
	}

	t.Setenv("NETBOX_API_KEY", "nbt_env.key")
	t.Setenv("NETBOX_URL", "https://example.invalid/graphql/")
	c, err := NewFromEnv(Options{})
	if err != nil {
		t.Fatalf("NewFromEnv: %v", err)
	}
	if c.Endpoint() != "https://example.invalid/graphql/" {
		t.Errorf("endpoint = %q", c.Endpoint())
	}
	if c.token != "nbt_env.key" {
		t.Error("token not read from the environment")
	}
}

func TestRequestHeadersAndBody(t *testing.T) {
	var (
		gotAuth  string
		gotUA    string
		gotCT    string
		gotQuery string
		gotVars  map[string]any
	)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotCT = r.Header.Get("Content-Type")

		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		gotQuery = payload.Query
		gotVars = payload.Variables

		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	})

	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.Query(context.Background(), "query { ok }", map[string]any{"tag": []string{"a"}}, &out); err != nil {
		t.Fatalf("Query: %v", err)
	}

	if gotAuth != "Bearer nbt_test.secret" {
		t.Errorf("Authorization = %q, want Bearer auth", gotAuth)
	}
	if gotUA != DefaultUserAgent || gotUA == "" {
		t.Errorf("User-Agent = %q, want a custom agent", gotUA)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotQuery != "query { ok }" {
		t.Errorf("query = %q", gotQuery)
	}
	if gotVars == nil || gotVars["tag"] == nil {
		t.Errorf("variables not sent: %#v", gotVars)
	}
	if !out.OK {
		t.Error("data was not unmarshalled")
	}
}

func TestQueryOmitsEmptyVariables(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "variables") {
			t.Errorf("variables key should be omitted, got %s", body)
		}
		_, _ = w.Write([]byte(`{"data":{}}`))
	})
	if err := c.Query(context.Background(), "query { ok }", nil, nil); err != nil {
		t.Fatalf("Query: %v", err)
	}
}

func TestQuerySurfacesGraphQLErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"boom"},{"message":"bang"}]}`))
	})

	err := c.Query(context.Background(), "query { ok }", nil, nil)
	if err == nil {
		t.Fatal("expected an error")
	}

	var gqlErrs GraphQLErrors
	if !errors.As(err, &gqlErrs) {
		t.Fatalf("error is %T, want GraphQLErrors", err)
	}
	if len(gqlErrs) != 2 || gqlErrs[0].Message != "boom" {
		t.Errorf("unexpected errors: %#v", gqlErrs)
	}
	if !strings.Contains(err.Error(), "boom; bang") {
		t.Errorf("error text = %q", err.Error())
	}
}

func TestQueryHTTPError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("error code: 1010"))
	})

	err := c.Query(context.Background(), "query { ok }", nil, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error is %T, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d", httpErr.StatusCode)
	}
	if !strings.Contains(httpErr.Error(), "1010") {
		t.Errorf("body not surfaced: %q", httpErr.Error())
	}
}

func TestQueryNullData(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null}`))
	})
	if err := c.Query(context.Background(), "query { ok }", nil, nil); err == nil {
		t.Fatal("expected an error for a null data block")
	}
}

func TestQueryRespectsContext(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"data":{}}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := c.Query(ctx, "query { ok }", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

const dnsResponse = `{"data":{
  "device_list": [
    {"name":"sea1-core-1",
     "primary_ip4":{"address":"10.3.0.1/24"},
     "primary_ip6":{"address":"2001:db8::1/64"},
     "interfaces":[
       {"name":"eth0","ip_addresses":[{"address":"10.3.0.1/24"}]},
       {"name":"IPMI","ip_addresses":[{"address":"10.3.9.1/24"}]}
     ]},
    {"name":"no-ipmi","primary_ip4":{"address":"10.3.0.2/24"},"primary_ip6":null,"interfaces":[
       {"name":"bmc","ip_addresses":[]}
    ]}
  ],
  "virtual_machine_list": [
    {"name":"vm-1","primary_ip4":{"address":"10.4.0.1/24"},"primary_ip6":null}
  ]
}}`

func TestFetchDNS(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "virtual_machine_list") {
			t.Errorf("DNS query not sent: %s", body)
		}
		_, _ = w.Write([]byte(dnsResponse))
	})

	res, err := c.FetchDNS(context.Background())
	if err != nil {
		t.Fatalf("FetchDNS: %v", err)
	}
	if len(res.Devices) != 2 || len(res.VirtualMachines) != 1 {
		t.Fatalf("got %d devices / %d vms", len(res.Devices), len(res.VirtualMachines))
	}

	hosts := res.Hosts()
	if len(hosts) != 3 || hosts[0].Name != "sea1-core-1" || hosts[2].Name != "vm-1" {
		t.Fatalf("Hosts() = %#v", hosts)
	}

	core := hosts[0]
	if got := core.IPv4(); got != "10.3.0.1" {
		t.Errorf("IPv4 = %q", got)
	}
	if got := core.IPv6(); got != "2001:db8::1" {
		t.Errorf("IPv6 = %q", got)
	}
	if got := core.IPMIAddress(); got != "10.3.9.1" {
		t.Errorf("IPMIAddress = %q", got)
	}
	if got := hosts[1].IPMIAddress(); got != "" {
		t.Errorf("IPMIAddress for an addressless bmc = %q, want empty", got)
	}
	if got := hosts[2].IPv6(); got != "" {
		t.Errorf("nil primary_ip6 should yield empty, got %q", got)
	}
}

const dhcpResponse = `{"data":{
  "interface_list": [
    {"name":"eth0",
     "primary_mac_address":{"mac_address":"AA:BB:CC:DD:EE:FF"},
     "device":{"name":"sea1-core-1","primary_ip4":{"address":"10.3.0.1/24"}},
     "ip_addresses":[{"address":"10.3.0.1/24"},{"address":"2001:db8::1/64"}]},
    {"name":"eth1","primary_mac_address":null,
     "device":{"name":"sea1-core-1","primary_ip4":{"address":"10.3.0.1/24"}},
     "ip_addresses":[]}
  ],
  "vm_interface_list": [
    {"name":"ens18","primary_mac_address":{"mac_address":"11:22:33:44:55:66"},
     "virtual_machine":{"name":"vm-1","primary_ip4":{"address":"10.4.0.1/24"}},
     "ip_addresses":[{"address":"10.4.0.1/24"}]}
  ]
}}`

func TestFetchDHCP(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(dhcpResponse))
	})

	res, err := c.FetchDHCP(context.Background())
	if err != nil {
		t.Fatalf("FetchDHCP: %v", err)
	}

	all := res.AllInterfaces()
	if len(all) != 3 {
		t.Fatalf("got %d interfaces, want 3", len(all))
	}

	eth0 := all[0]
	if got := eth0.PrimaryMACAddress.MAC(); got != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("MAC = %q", got)
	}
	owner := eth0.OwnerRef()
	if owner == nil || owner.Name != "sea1-core-1" {
		t.Fatalf("OwnerRef = %#v", owner)
	}
	if got := owner.PrimaryIP4.IP(); got != "10.3.0.1" {
		t.Errorf("owner primary ip4 = %q", got)
	}
	if got := eth0.IPAddresses[1].IP(); got != "2001:db8::1" {
		t.Errorf("second address = %q", got)
	}

	if got := all[1].PrimaryMACAddress.MAC(); got != "" {
		t.Errorf("nil MAC should be empty, got %q", got)
	}

	ens18 := all[2]
	if ens18.Device != nil {
		t.Error("vm interface should not carry a device")
	}
	if o := ens18.OwnerRef(); o == nil || o.Name != "vm-1" {
		t.Errorf("vm OwnerRef = %#v", ens18.OwnerRef())
	}
}

func TestDevicesByTag(t *testing.T) {
	var gotVars map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &payload)
		gotVars = payload.Variables

		_, _ = w.Write([]byte(`{"data":{"device_list":[{
		  "name":"sw-1","serial":"ABC","asset_tag":"A1",
		  "platform":{"slug":"eos","name":"Arista EOS"},
		  "tags":[{"name":"switch"}],
		  "primary_ip4":{"address":"10.0.0.5/24"},
		  "config_context":{"foo":"bar"},
		  "interfaces":[{
		    "name":"Ethernet1","type":"A_10GBASE_T","description":"uplink","mode":"TAGGED",
		    "lag":{"name":"Port-Channel1"},
		    "untagged_vlan":{"name":"mgmt","vid":10},
		    "tagged_vlans":[{"name":"prod","vid":20}],
		    "vrf":{"name":"MGMT"},
		    "ip_addresses":[{"address":"10.0.0.5/24"}],
		    "cable":{"id":"7","terminations":[
		      {"cable_end":"A","termination":{"name":"Ethernet1","device":{"name":"sw-1"}}},
		      {"cable_end":"B","termination":{"name":"Ethernet9","device":{"name":"sw-2"}}},
		      {"cable_end":"B","termination":{}}
		    ]}
		  }]
		}]}}`))
	})

	devices, err := c.DevicesByTag(context.Background(), "switch")
	if err != nil {
		t.Fatalf("DevicesByTag: %v", err)
	}
	filters, _ := gotVars["filters"].(map[string]any)
	if filters == nil {
		t.Fatalf("filters variable not sent: %#v", gotVars)
	}
	tags, _ := filters["tags"].(map[string]any)
	slug, _ := tags["slug"].(map[string]any)
	list, _ := slug["in_list"].([]any)
	if len(list) != 1 || list[0] != "switch" {
		t.Errorf("tag filter = %#v", filters)
	}
	if len(devices) != 1 {
		t.Fatalf("got %d devices", len(devices))
	}

	d := devices[0]
	if d.Name != "sw-1" || d.Serial != "ABC" || d.AssetTag != "A1" {
		t.Errorf("device = %#v", d)
	}
	if d.Platform == nil || d.Platform.Slug != "eos" {
		t.Errorf("platform = %#v", d.Platform)
	}
	if len(d.Interfaces) != 1 {
		t.Fatalf("got %d interfaces", len(d.Interfaces))
	}

	iface := d.Interfaces[0]
	if iface.LAG == nil || iface.LAG.Name != "Port-Channel1" {
		t.Errorf("lag = %#v", iface.LAG)
	}
	if iface.UntaggedVLAN == nil || iface.UntaggedVLAN.VID != 10 {
		t.Errorf("untagged vlan = %#v", iface.UntaggedVLAN)
	}
	if len(iface.TaggedVLANs) != 1 || iface.TaggedVLANs[0].VID != 20 {
		t.Errorf("tagged vlans = %#v", iface.TaggedVLANs)
	}
	if iface.VRF == nil || iface.VRF.Name != "MGMT" {
		t.Errorf("vrf = %#v", iface.VRF)
	}
	if iface.Cable == nil || len(iface.Cable.Terminations) != 3 {
		t.Fatalf("cable = %#v", iface.Cable)
	}
	if got := iface.Cable.Terminations[1].DeviceName(); got != "sw-2" {
		t.Errorf("far end device = %q", got)
	}
	if got := iface.Cable.Terminations[1].InterfaceName(); got != "Ethernet9" {
		t.Errorf("far end interface = %q", got)
	}
	// A non-interface termination decodes without a device.
	if got := iface.Cable.Terminations[2].DeviceName(); got != "" {
		t.Errorf("non-interface termination device = %q, want empty", got)
	}
}

func TestCableTerminationNilSafety(t *testing.T) {
	var term CableTermination
	if term.DeviceName() != "" || term.InterfaceName() != "" {
		t.Error("a zero CableTermination should yield empty strings")
	}
}

func TestCleanHostname(t *testing.T) {
	cases := []struct{ in, want string }{
		{"sea1-core-1", "sea1-core-1"},
		{"my host", "my_host"},
		{"a:b", "ab"},
		{"a - b", "a_b"},
		{"a/b", "a_b"},
		{"switch 1 - port:2/3", "switch_1_port2_3"},
	}
	for _, tc := range cases {
		if got := CleanHostname(tc.in); got != tc.want {
			t.Errorf("CleanHostname(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsIPMIInterface(t *testing.T) {
	for _, name := range []string{"ipmi", "IPMI", "iDRAC", "ilo", "drac", "BMC", "imm"} {
		if !IsIPMIInterface(name) {
			t.Errorf("IsIPMIInterface(%q) = false", name)
		}
	}
	for _, name := range []string{"eth0", "", "ipmi0", "mgmt"} {
		if IsIPMIInterface(name) {
			t.Errorf("IsIPMIInterface(%q) = true", name)
		}
	}
}

func TestIPAddressAndMACNilSafety(t *testing.T) {
	var addr *IPAddress
	if got := addr.IP(); got != "" {
		t.Errorf("nil IPAddress.IP() = %q", got)
	}
	var mac *MACAddress
	if got := mac.MAC(); got != "" {
		t.Errorf("nil MACAddress.MAC() = %q", got)
	}
	bare := &IPAddress{Address: "10.0.0.1"}
	if got := bare.IP(); got != "10.0.0.1" {
		t.Errorf("prefixless address = %q", got)
	}
}

// TestNullNamesDecodeToEmptyNotGarbage pins that a NetBox null `name`
// (which NetBox permits on both devices and interfaces) decodes to ""
// and never produces a record. The callers in cli/generatedns.go treat
// "" as "absent"; this is the half of the contract that lives here.
func TestNullNamesDecodeToEmptyNotGarbage(t *testing.T) {
	var host Host
	if err := json.Unmarshal([]byte(`{
	  "name": null,
	  "primary_ip4": {"address": "10.0.0.5/24"},
	  "interfaces": [{"name": null, "ip_addresses": [{"address": "10.0.1.5/24"}]}]
	}`), &host); err != nil {
		t.Fatal(err)
	}
	if host.Name != "" {
		t.Errorf("null device name = %q, want empty", host.Name)
	}
	if host.Interfaces[0].Name != "" {
		t.Errorf("null interface name = %q, want empty", host.Interfaces[0].Name)
	}
	// An unnamed interface must not be mistaken for out-of-band kit.
	if IsIPMIInterface("") || IsIPMIInterface("  ") {
		t.Error("an unnamed interface was treated as IPMI")
	}
	if got := host.IPMIAddress(); got != "" {
		t.Errorf("IPMIAddress from an unnamed interface = %q, want empty", got)
	}
}
