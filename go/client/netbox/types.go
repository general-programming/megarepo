package netbox

import "strings"

// IPAddress is NetBox's ip-address object as exposed over GraphQL. Address
// carries a prefix length ("10.0.0.1/24").
type IPAddress struct {
	Address string `json:"address"`
}

// IP returns the address with its prefix length stripped, or "" when the
// address is absent. Mirrors Python's strip_prefix().
func (a *IPAddress) IP() string {
	if a == nil {
		return ""
	}
	ip, _, _ := strings.Cut(a.Address, "/")
	return ip
}

// MACAddress is NetBox's mac-address object (NetBox >= 4.2 models MACs as
// first-class objects hung off an interface as primary_mac_address).
type MACAddress struct {
	MACAddress string `json:"mac_address"`
}

// MAC returns the MAC string, or "" when absent.
func (m *MACAddress) MAC() string {
	if m == nil {
		return ""
	}
	return m.MACAddress
}

// VLAN is a tagged/untagged VLAN reference on an interface.
type VLAN struct {
	Name string `json:"name"`
	VID  int    `json:"vid"`
}

// NamedRef is the common `{ name }` shape used for LAGs, VRFs, platforms
// and tags.
type NamedRef struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

// Interface is a device interface. Not every query populates every field;
// unrequested fields stay at their zero value.
type Interface struct {
	Name              string      `json:"name"`
	Type              string      `json:"type,omitempty"`
	Description       string      `json:"description,omitempty"`
	Mode              string      `json:"mode,omitempty"`
	PrimaryMACAddress *MACAddress `json:"primary_mac_address,omitempty"`
	IPAddresses       []IPAddress `json:"ip_addresses,omitempty"`
	LAG               *NamedRef   `json:"lag,omitempty"`
	VRF               *NamedRef   `json:"vrf,omitempty"`
	TaggedVLANs       []VLAN      `json:"tagged_vlans,omitempty"`
	UntaggedVLAN      *VLAN       `json:"untagged_vlan,omitempty"`
	Cable             *Cable      `json:"cable,omitempty"`

	// Device is set on interface_list results; VirtualMachine is set on
	// vm_interface_list results. Exactly one is non-nil in practice.
	Device         *Owner `json:"device,omitempty"`
	VirtualMachine *Owner `json:"virtual_machine,omitempty"`
}

// Owner returns whichever of Device / VirtualMachine owns the interface,
// or nil when neither was requested or present.
func (i Interface) OwnerRef() *Owner {
	if i.Device != nil {
		return i.Device
	}
	return i.VirtualMachine
}

// Cable is a cable and its terminations, used to derive switch topology.
type Cable struct {
	ID           string             `json:"id"`
	Terminations []CableTermination `json:"terminations"`
}

// CableTermination is one end of a cable. NetBox 4.x models the far side as
// a `termination` union; only the InterfaceType members are requested, so
// non-interface terminations (power, console, circuits) decode as an empty
// Termination.
type CableTermination struct {
	CableEnd    string       `json:"cable_end"`
	Termination *Termination `json:"termination,omitempty"`
}

// Termination is the interface end of a cable termination.
type Termination struct {
	Name   string    `json:"name"`
	Device *NamedRef `json:"device,omitempty"`
}

// DeviceName returns the name of the device on this end of the cable, or ""
// when the termination is not an interface on a device.
func (t CableTermination) DeviceName() string {
	if t.Termination == nil || t.Termination.Device == nil {
		return ""
	}
	return t.Termination.Device.Name
}

// InterfaceName returns the interface name on this end of the cable, or ""
// when the termination is not an interface.
func (t CableTermination) InterfaceName() string {
	if t.Termination == nil {
		return ""
	}
	return t.Termination.Name
}

// Owner is the trimmed device/virtual-machine reference embedded in
// interface results.
type Owner struct {
	Name       string     `json:"name"`
	PrimaryIP4 *IPAddress `json:"primary_ip4,omitempty"`
	PrimaryIP6 *IPAddress `json:"primary_ip6,omitempty"`
}

// Host is a device or virtual machine as returned by the DNS query. The
// same struct is used for both lists; VMs simply have no Interfaces.
type Host struct {
	Name       string      `json:"name"`
	PrimaryIP4 *IPAddress  `json:"primary_ip4,omitempty"`
	PrimaryIP6 *IPAddress  `json:"primary_ip6,omitempty"`
	Interfaces []Interface `json:"interfaces,omitempty"`
}

// Device is the richer device shape returned by DevicesByTag, used for
// switch config generation.
type Device struct {
	Name          string      `json:"name"`
	Serial        string      `json:"serial,omitempty"`
	AssetTag      string      `json:"asset_tag,omitempty"`
	ConfigContext any         `json:"config_context,omitempty"`
	PrimaryIP4    *IPAddress  `json:"primary_ip4,omitempty"`
	Platform      *NamedRef   `json:"platform,omitempty"`
	Tags          []NamedRef  `json:"tags,omitempty"`
	Interfaces    []Interface `json:"interfaces,omitempty"`
}

// IPMIInterfaceNames are the interface names treated as out-of-band
// management. Matched case-insensitively.
var IPMIInterfaceNames = map[string]struct{}{
	"ipmi":  {},
	"idrac": {},
	"ilo":   {},
	"drac":  {},
	"bmc":   {},
	"imm":   {},
}

// IsIPMIInterface reports whether an interface name is an out-of-band
// management interface.
func IsIPMIInterface(name string) bool {
	_, ok := IPMIInterfaceNames[strings.ToLower(name)]
	return ok
}

// IPMIAddress returns the first IP on the host's first IPMI-ish interface,
// or "" when the host has no out-of-band address. Mirrors refresh_dns.py's
// ipmi_ip().
func (h Host) IPMIAddress() string {
	for _, iface := range h.Interfaces {
		if !IsIPMIInterface(iface.Name) {
			continue
		}
		if len(iface.IPAddresses) == 0 {
			continue
		}
		return iface.IPAddresses[0].IP()
	}
	return ""
}

// IPv4 returns the host's primary IPv4 without its prefix length.
func (h Host) IPv4() string { return h.PrimaryIP4.IP() }

// IPv6 returns the host's primary IPv6 without its prefix length.
func (h Host) IPv6() string { return h.PrimaryIP6.IP() }

// CleanHostname normalises a NetBox name into something usable as a DNS
// label component. Byte-identical to Python's clean_hostname().
func CleanHostname(data string) string {
	// Applied in the same order as the Python original; the order matters
	// because earlier substitutions can create matches for later ones.
	out := strings.ReplaceAll(data, " ", "_")
	out = strings.ReplaceAll(out, ":", "")
	out = strings.ReplaceAll(out, "_-_", "_")
	return strings.ReplaceAll(out, "/", "_")
}
