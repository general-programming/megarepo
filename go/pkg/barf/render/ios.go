package render

import (
	"fmt"
	"net"
	"sort"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// The IOS-family vendors (Cisco IOS, Dell DNOS6/DNOS9, and the Arista
// eos.j2 that arista.py's managed slice replaced) share three template
// fragments: common/shared_ios_vlans.j2, shared_ios_interfaces.j2 and
// shared_ios_snmp.j2. This file is the port of those three plus the
// device projection they read from.
//
// Unlike every other renderer these hosts do NOT come from network.yml:
// they are the `managed_netdevice`-tagged devices in NetBox, assembled
// by BaseHost.from_netbox_meta with role "network_devices". IOSDevice is
// that projection, kept as its own type because model.Host (frozen by
// ../CONTRACT.md, and owned by another worker) has nowhere to put the
// NetBox-only per-interface facts: 802.1q mode, LAG membership, and VRF.
//
// Whoever wires the NetBox client in should build an IOSDevice directly.
// Rendering a model.Host still works (IOSDeviceFromHost projects one),
// it just cannot know those three fields.

// iosDomainName is hardcoded in common/shared_ios_common.j2 rather than
// read from global_meta.search_domain. Kept literal for parity.
const iosDomainName = "generalprogramming.org"

// IOSInterface is one interface as the shared IOS fragments see it.
type IOSInterface struct {
	Name string
	// Type is the NetBox interface type: "LAG" and "VIRTUAL" are the
	// two the templates branch on; anything else is a physical port.
	Type string
	// Mode is the NetBox 802.1q mode: "ACCESS", "TAGGED", "TAGGED_ALL",
	// or empty for a routed/unmoded interface.
	Mode        string
	Description string
	// VRF is the VRF name, empty when the interface is in the default one.
	VRF string
	// LagID is the parent LAG's NetBox name, empty when not a member.
	LagID string
	// Enabled decides `no shutdown` vs `shutdown`.
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

// IsTrunk reports whether the interface is a trunk.
func (i *IOSInterface) IsTrunk() bool {
	return i.Mode == "TAGGED" || i.Mode == "TAGGED_ALL"
}

// CiscoName is HostInterface.cisco_name: a LAG's bare NetBox name is a
// number, which IOS spells as a Port-Channel.
func (i *IOSInterface) CiscoName() string {
	if i.IsLAG() {
		return "Port-Channel" + i.Name
	}
	return i.Name
}

// V4 returns the interface's first IPv4 address, if any. The templates
// only ever render an IPv4 `ip address`.
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
	// VLANs are the device's VLANs, already filtered and ordered (see
	// IOSVLANs). One `vlan <vid>` stanza is rendered per entry.
	VLANs []model.VLAN
	// DefaultRoute is config_context["default-route"], empty when unset.
	DefaultRoute string
	// SNMPLocation is the per-device location override; NetBox-sourced
	// hosts never set one, so it is normally empty.
	SNMPLocation string
	// TacacsServers is BaseHost.tacacs_servers. Python hardcodes this
	// to an empty list ("HACK: TACACS is broken, lol" -- the Consul SRV
	// lookup below it is unreachable), so every TACACS block gated on
	// it renders as nothing. Modeled anyway: the DNOS templates emit
	// the tacacs-server KEY unconditionally, so the feature is not
	// dead, only its server list.
	TacacsServers []string
}

// IOSDeviceFromHost projects a network.yml host into the IOS view.
//
// The NetBox-only per-interface facts (802.1q mode, LAG parent, VRF)
// have no network.yml spelling and come out empty, so a host routed
// through here renders every port as unmoded. This exists so
// render.Host works end to end for a `network_devices` host; the real
// input for these vendors is a NetBox-built IOSDevice.
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

// IOSVLANs is BaseHost.vlans: every VLAN any interface references,
// deduplicated. known is the NetBox VLAN map the device is allowed to
// declare (BaseHost.vlan_map), keyed by VID; a nil map accepts every
// referenced VLAN, which is what a network.yml host gets.
//
// Python builds this from a set and so emits the stanzas in an order
// that varies run to run (PYTHONHASHSEED). This orders them by VID
// instead: the goldens are a byte-parity contract, and a renderer whose
// output depends on the interpreter's hash seed cannot honour one.
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
			// The template's native-vlan clause is inline, so the
			// separating space is emitted whether or not it fills in.
			native := ""
			if iface.UntaggedVLAN != nil {
				native = fmt.Sprintf("native vlan %d", iface.UntaggedVLAN.VID)
			}
			lines = append(lines, "    switchport mode trunk "+native)
		}

		if iface.VRF != "" {
			lines = append(lines, "    vrf "+iface.VRF)
		}
		// EOS needs every non-SVI port put into switching mode
		// explicitly; IOS and DNOS default to it.
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
