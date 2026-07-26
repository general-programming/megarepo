package netbox

import "strings"

// IPAddress is NetBox's ip-address object; Address includes a prefix length.
type IPAddress struct {
	Address string `json:"address"`
}

// IP is the address minus its prefix length, Python's strip_prefix().
func (a *IPAddress) IP() string {
	if a == nil {
		return ""
	}
	ip, _, _ := strings.Cut(a.Address, "/")
	return ip
}

// MACAddress is NetBox's mac-address object, first-class since 4.2.
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

// NamedRef is the `{ name }` shape used for LAGs, VRFs, platforms, tags.
type NamedRef struct {
	Name string `json:"name"`
	Slug string `json:"slug,omitempty"`
}

// Interface is a device interface; unrequested fields stay zero. Name is a
// plain string so a NetBox null decodes to "" (treat it as absent) rather
// than failing the whole fetch as Python does.
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

	// Exactly one is set: Device on interface_list, VirtualMachine on
	// vm_interface_list results.
	Device         *Owner `json:"device,omitempty"`
	VirtualMachine *Owner `json:"virtual_machine,omitempty"`
}

// OwnerRef returns whichever of Device / VirtualMachine owns the interface.
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

// CableTermination is one end of a cable. Only InterfaceType members of
// NetBox 4.x's union are requested, so power/console/circuit ends decode empty.
type CableTermination struct {
	CableEnd    string       `json:"cable_end"`
	Termination *Termination `json:"termination,omitempty"`
}

// Termination is the interface end of a cable termination.
type Termination struct {
	Name   string    `json:"name"`
	Device *NamedRef `json:"device,omitempty"`
}

// DeviceName returns the device on this end of the cable, or "".
func (t CableTermination) DeviceName() string {
	if t.Termination == nil || t.Termination.Device == nil {
		return ""
	}
	return t.Termination.Device.Name
}

// InterfaceName returns the interface on this end of the cable, or "".
func (t CableTermination) InterfaceName() string {
	if t.Termination == nil {
		return ""
	}
	return t.Termination.Name
}

// Owner is the device/VM reference embedded in interface results.
type Owner struct {
	Name       string     `json:"name"`
	PrimaryIP4 *IPAddress `json:"primary_ip4,omitempty"`
	PrimaryIP6 *IPAddress `json:"primary_ip6,omitempty"`
}

// Host is a device or VM from the DNS query; VMs have no Interfaces.
type Host struct {
	Name       string      `json:"name"`
	PrimaryIP4 *IPAddress  `json:"primary_ip4,omitempty"`
	PrimaryIP6 *IPAddress  `json:"primary_ip6,omitempty"`
	Interfaces []Interface `json:"interfaces,omitempty"`
}

// Device is the richer shape DevicesByTag returns, for config generation.
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

// IPMIInterfaceNames are out-of-band management names, matched case-insensitively.
var IPMIInterfaceNames = map[string]struct{}{
	"ipmi":  {},
	"idrac": {},
	"ilo":   {},
	"drac":  {},
	"bmc":   {},
	"imm":   {},
}

// IsIPMIInterface reports whether name is out-of-band management; an absent
// name ("") never matches, which would attach a BMC record to an unnamed one.
func IsIPMIInterface(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	_, ok := IPMIInterfaceNames[strings.ToLower(name)]
	return ok
}

// IPMIAddress is the first IP on the first IPMI-ish interface (ipmi_ip()).
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

// CleanHostname normalises a NetBox name into a DNS label component,
// byte-identical to Python's clean_hostname().
func CleanHostname(data string) string {
	// Order is load-bearing: earlier substitutions create matches for later ones.
	out := strings.ReplaceAll(data, " ", "_")
	out = strings.ReplaceAll(out, ":", "")
	out = strings.ReplaceAll(out, "_-_", "_")
	return strings.ReplaceAll(out, "/", "_")
}
