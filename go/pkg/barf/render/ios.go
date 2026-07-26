package render

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Port of common/shared_ios_{vlans,interfaces,snmp}.j2, shared by Cisco IOS,
// Dell DNOS6/9 and the replaced Arista eos.j2, plus their device projection.
//
// These hosts do NOT come from network.yml: they are `managed_netdevice`
// NetBox devices. IOSDevice is a separate type because model.Host is frozen
// by ../CONTRACT.md and cannot hold the NetBox-only per-interface facts
// (802.1q mode, LAG membership, VRF), which IOSDeviceFromHost cannot fill.

// iosDomainName is hardcoded in common/shared_ios_common.j2 rather than read
// from global_meta.search_domain. Kept literal for parity.
const iosDomainName = "generalprogramming.org"

// IOSInterface is one interface as the shared IOS fragments see it.
type IOSInterface struct {
	Name string
	// Type is the NetBox interface type; only "LAG" and "VIRTUAL" branch.
	Type string
	// Mode is the NetBox 802.1q mode: ACCESS, TAGGED, TAGGED_ALL, or empty.
	Mode        string
	Description string
	VRF         string
	// LagID is the parent LAG's NetBox name, empty when not a member.
	LagID        string
	Enabled      bool
	Addresses    []model.Address
	UntaggedVLAN *model.VLAN
	TaggedVLANs  []model.VLAN
}

// IsLAG reports whether the interface is a link-aggregation group.
func (i *IOSInterface) IsLAG() bool { return i.Type == "LAG" }

// IsVLAN reports whether the interface is an SVI.
func (i *IOSInterface) IsVLAN() bool { return i.Type == "VIRTUAL" }

// IsAccess reports whether the interface is an access port.
func (i *IOSInterface) IsAccess() bool { return i.Mode == "ACCESS" }

// IsTrunk reports whether the interface is a trunk port.
func (i *IOSInterface) IsTrunk() bool {
	return i.Mode == "TAGGED" || i.Mode == "TAGGED_ALL"
}

// CiscoName is HostInterface.cisco_name: a LAG's name is a bare number, which
// IOS spells as a Port-Channel.
func (i *IOSInterface) CiscoName() string {
	if i.IsLAG() {
		return "Port-Channel" + i.Name
	}
	return i.Name
}

// V4 returns the interface's first IPv4 address; templates only render IPv4.
func (i *IOSInterface) V4() *model.Address {
	for n := range i.Addresses {
		if i.Addresses[n].IP.Is4() {
			return &i.Addresses[n]
		}
	}
	return nil
}

// IOSDevice is a NetBox-sourced switch as the IOS-family templates see it.
type IOSDevice struct {
	Hostname   string
	DeviceType string
	Interfaces []IOSInterface
	// VLANs are already filtered and ordered by IOSVLANs; one stanza each.
	VLANs []model.VLAN
	// DefaultRoute is config_context["default-route"], empty when unset.
	DefaultRoute string
	// SNMPLocation is the per-device override; NetBox hosts never set one.
	SNMPLocation string
	// TacacsServers is BaseHost.tacacs_servers, hardcoded empty upstream
	// ("HACK: TACACS is broken, lol"), so every gated TACACS block renders as
	// nothing. Modeled anyway: DNOS emits the tacacs-server KEY unconditionally.
	TacacsServers []string
}

// IOSDeviceFromHost projects a network.yml host into the IOS view; the
// NetBox-only facts have no network.yml spelling, so ports render unmoded.
func IOSDeviceFromHost(h *model.Host) *IOSDevice {
	device := &IOSDevice{
		Hostname:     h.Hostname,
		DeviceType:   h.DeviceType,
		SNMPLocation: h.SNMPLocation,
	}
	for i := range h.Interfaces {
		iface := &h.Interfaces[i]
		device.Interfaces = append(device.Interfaces, IOSInterface{
			Name:         iface.Name,
			Type:         iface.Type,
			Description:  iface.Description,
			Enabled:      iface.Enabled,
			Addresses:    iface.Addresses,
			UntaggedVLAN: iface.UntaggedVLAN,
			TaggedVLANs:  iface.TaggedVLANs,
		})
	}
	device.VLANs = IOSVLANs(device.Interfaces, nil)
	return device
}

// IOSVLANs is BaseHost.vlans: every VLAN any interface references, deduped;
// known is BaseHost.vlan_map keyed by VID, nil admits all. Python builds it
// from a set, so stanza order varies with PYTHONHASHSEED; ordering by VID
// instead is what makes the goldens stable.
func IOSVLANs(interfaces []IOSInterface, known map[int]model.VLAN) []model.VLAN {
	seen := map[int]model.VLAN{}
	admit := func(vlan model.VLAN) {
		if known != nil {
			if _, ok := known[vlan.VID]; !ok {
				return
			}
		}
		if _, ok := seen[vlan.VID]; !ok {
			seen[vlan.VID] = vlan
		}
	}
	for i := range interfaces {
		if interfaces[i].UntaggedVLAN != nil {
			admit(*interfaces[i].UntaggedVLAN)
		}
		for _, vlan := range interfaces[i].TaggedVLANs {
			admit(vlan)
		}
	}

	out := make([]model.VLAN, 0, len(seen))
	for _, vlan := range seen {
		out = append(out, vlan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VID < out[j].VID })
	return out
}

// iosVLANStanzas is common/shared_ios_vlans.j2.
func iosVLANStanzas(d *IOSDevice) []string {
	var lines []string
	for _, vlan := range d.VLANs {
		lines = append(lines, fmt.Sprintf("vlan %d", vlan.VID))
		if vlan.Name != "" {
			// IOS VLAN names cannot contain spaces.
			lines = append(lines, "    name "+strings.ReplaceAll(vlan.Name, " ", "-"))
		}
		lines = append(lines, "!")
	}
	return lines
}

// iosInterfaceStanzas is common/shared_ios_interfaces.j2.
func iosInterfaceStanzas(d *IOSDevice) []string {
	var lines []string
	for i := range d.Interfaces {
		iface := &d.Interfaces[i]
		lines = append(lines, "interface "+iface.CiscoName())

		switch {
		case iface.IsVLAN():
			// An SVI has no switchport config at all.
		case iface.IsAccess():
			lines = append(lines, "    switchport mode access")
			if iface.UntaggedVLAN != nil {
				lines = append(lines,
					fmt.Sprintf("    switchport access vlan %d", iface.UntaggedVLAN.VID))
			}
		case iface.IsTrunk():
			// The native-vlan clause is inline, so the separating space is
			// emitted whether or not it fills in.
			native := ""
			if iface.UntaggedVLAN != nil {
				native = fmt.Sprintf("native vlan %d", iface.UntaggedVLAN.VID)
			}
			lines = append(lines, "    switchport mode trunk "+native)
		}

		if iface.VRF != "" {
			lines = append(lines, "    vrf "+iface.VRF)
		}
		// EOS needs every non-SVI port switched explicitly; IOS/DNOS default to it.
		if d.DeviceType == "eos" && !iface.IsVLAN() {
			lines = append(lines, "    switchport")
		}
		if iface.LagID != "" {
			lines = append(lines, "    channel-group "+iface.LagID+" mode active")
		}
		if iface.Description != "" {
			// Quoted, but not escaped: the template just wraps it.
			lines = append(lines, `    description "`+iface.Description+`"`)
		}
		if address := iface.V4(); address != nil {
			lines = append(lines, fmt.Sprintf("    ip address %s %s",
				address.IP.String(), dottedNetmask(address.Prefix)))
		}
		if iface.Enabled {
			lines = append(lines, "    no shutdown")
		} else {
			lines = append(lines, "    shutdown")
		}
		lines = append(lines, "!")
	}
	return lines
}

// iosSNMP is common/shared_ios_snmp.j2.
func iosSNMP(d *IOSDevice, global model.GlobalMeta) []string {
	lines := []string{"snmp-server community " + global.SNMPPublic + " ro"}
	if d.SNMPLocation != "" {
		lines = append(lines, "snmp-server location "+d.SNMPLocation)
	}
	if global.SNMPContact != "" {
		lines = append(lines, "snmp-server contact "+global.SNMPContact)
	}
	return lines
}

// dottedNetmask is Python's IPv4Network.netmask.compressed.
func dottedNetmask(prefix int) string {
	mask := net.CIDRMask(prefix, 32)
	if mask == nil {
		return ""
	}
	return net.IP(mask).String()
}
