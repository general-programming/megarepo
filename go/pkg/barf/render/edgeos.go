package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// EdgeOS renders a Ubiquiti EdgeOS/vyatta host in the vpn role.
//
// Port of projects/barf/barf/templates/vpn/edgeos.j2, which is
// `{% include 'common/vyos.j2' %}` plus its own fabric section. The
// shared prefix is byte-identical to the VyOS blocks (the blocks were
// extracted from that very include), so this reuses them; only
// `interface_prefix` (barf/vendors/edgeos.py) and the fabric section
// differ.
//
// This template is legacy and unmaintained, and the port is deliberately
// bug-for-bug faithful to it. Three things it gets wrong on the current
// fabric, reproduced here because parity is the contract:
//
//   - The tunnel interface is named from the LAST THREE DIGITS of the
//     link port ("wg080" for port 51080), so ports sharing a suffix
//     collide. The VyOS blocks use the whole port ("wg51080").
//   - Unnumbered (IPv6 link-local, v6only) fabric links have no /31, so
//     `link.get_ip()` is None and the template interpolates the literal
//     string "None" into the address and neighbor lines. Every modern
//     fabric link is unnumbered, so this output does not work as-is.
//   - Peers are keyed by public key rather than by name, and the tunnel
//     carries only `allowed-ips 0.0.0.0/0` (VyOS 1.3-era syntax, no
//     IPv6). IPsec links are not handled at all.
//
// It is ported rather than fixed because barf has no EdgeOS host today:
// there is nothing to validate a "fixed" version against, and inventing
// a different output would be a behaviour change wearing a port's
// clothes. Anything that grows a real EdgeOS device should move it onto
// the block path the way vyos/linux/mikrotik went.
type EdgeOS struct{}

// edgeosKeepalive is the persistent-keepalive the template hardcodes.
const edgeosKeepalive = 10

// Render returns the full EdgeOS config text for h.
func (EdgeOS) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "vpn" {
		return "", noTemplateError(h)
	}
	ctx := newRenderCtx(h, n, s)

	blocks := []func(*renderCtx) ([]string, error){
		// common/vyos.j2, via the blocks extracted from it.
		vyosSystemConfig,
		edgeosInterfaces,
		vyosSSHConfig,
		vyosPlatform,
		vyosFirewallGroups,
		vyosNTPConfig,
		vyosOSPF,
		vyosStaticRoutes,
		vyosNAT,
		// vpn/edgeos.j2's own body.
		edgeosFabric,
		vyosExtraConfig,
	}

	var lines []string
	for _, block := range blocks {
		emitted, err := block(ctx)
		if err != nil {
			return "", err
		}
		lines = append(lines, emitted...)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// edgeosInterfacePrefix is EdgeOSHost.interface_prefix. Unlike the VyOS
// one it has no bridge or wireguard branch, so modeled bridges and
// static WireGuard tunnels render under `set interfaces ethernet`.
func edgeosInterfacePrefix(iface *model.Interface) string {
	interfaceType := "ethernet"
	if iface.Management {
		interfaceType = "dummy"
	}

	prefix := fmt.Sprintf("set interfaces %s %s", interfaceType, iface.Name)
	if iface.UntaggedVLAN != nil {
		prefix += fmt.Sprintf(" vif %d", iface.UntaggedVLAN.VID)
	}
	return prefix
}

func edgeosInterfaces(c *renderCtx) ([]string, error) {
	return vyattaInterfaces(c, edgeosInterfacePrefix)
}

// edgeosFabric is the vpn/edgeos.j2 body: one WireGuard tunnel and one
// BGP neighbor per fabric link, then the announced networks and the
// peer-group/timer boilerplate.
func edgeosFabric(c *renderCtx) ([]string, error) {
	// The template's blank separator after common/vyos.j2.
	lines := []string{""}
	asn := strconv.Itoa(c.host.ASN)

	for _, link := range c.links {
		peer, err := c.peer(link)
		if err != nil {
			return nil, err
		}
		keys, err := wireguardKeypair(c.secrets, link.KeyPath(c.host.Hostname))
		if err != nil {
			return nil, err
		}
		peerKeys, err := wireguardKeypair(c.secrets, link.KeyPath(peer.Hostname))
		if err != nil {
			return nil, err
		}

		name := edgeosTunnelName(link.Port)
		iface := "set interfaces wireguard " + name
		lines = append(lines,
			fmt.Sprintf("%s description 'wg link (%s -> %s)'", iface, link.A, link.B),
			fmt.Sprintf("%s address %s/31", iface, pythonNone(link.GetIP(c.host.Hostname, false))),
			fmt.Sprintf("%s private-key '%s'", iface, keys.Private),
			fmt.Sprintf("%s listen-port %d", iface, link.Port),
			iface+" route-allowed-ips false",
			fmt.Sprintf("%s peer '%s' allowed-ips 0.0.0.0/0", iface, peerKeys.Public),
		)
		// Only dialable peers get an endpoint; the template uses the
		// raw network.yml `address:` spelling, unquoted here.
		if peer.AddressRaw != "" {
			lines = append(lines,
				fmt.Sprintf("%s peer %s endpoint %s:%d", iface, peerKeys.Public,
					peer.AddressRaw, link.Port),
				fmt.Sprintf("%s peer %s persistent-keepalive %d", iface, peerKeys.Public,
					edgeosKeepalive),
			)
		}

		neighbor := fmt.Sprintf("set protocols bgp %s neighbor %s", asn,
			pythonNone(link.GetIP(peer.Hostname, false)))
		peerGroup := "leaf"
		if peer.IsSpine() {
			peerGroup = "spine"
		}
		lines = append(lines,
			fmt.Sprintf("%s remote-as %d", neighbor, peer.ASN),
			// Rendered from `device.management_address.ip`, which is
			// Undefined (and so empty, leaving the line's trailing
			// space) on a host with no management interface.
			fmt.Sprintf("%s update-source %s", neighbor, managementIP(c.host)),
			fmt.Sprintf("%s peer-group %s", neighbor, peerGroup),
		)
	}

	for _, network := range c.host.Networks {
		lines = append(lines, fmt.Sprintf("set protocols bgp %s network %s", asn, network))
	}
	// The loopback announcement carries its prefix length, and is the
	// literal "None" on a host with no management interface.
	management := "None"
	if address := c.host.ManagementAddress(); address != nil {
		management = address.String()
	}
	lines = append(lines,
		fmt.Sprintf("set protocols bgp %s network %s", asn, management),
		"",
		fmt.Sprintf("set protocols bgp %s peer-group leaf ebgp-multihop '6'", asn),
		fmt.Sprintf("set protocols bgp %s peer-group spine ebgp-multihop '6'", asn),
		fmt.Sprintf("set protocols bgp %s timers keepalive 10", asn),
		fmt.Sprintf("set protocols bgp %s timers holdtime 30", asn),
	)
	return lines, nil
}

// edgeosTunnelName is the template's `"wg" + str(link_id)[-3:]`.
func edgeosTunnelName(port int) string {
	digits := strconv.Itoa(port)
	if len(digits) > 3 {
		digits = digits[len(digits)-3:]
	}
	return "wg" + digits
}

// managementIP is `device.management_address.ip`: the bare address of
// the management interface, empty when there is none.
func managementIP(h *model.Host) string {
	if address := h.ManagementAddress(); address != nil {
		return address.IP.String()
	}
	return ""
}

// pythonNone renders an absent value the way Jinja renders Python's
// None, which is what the legacy template does with unnumbered links.
func pythonNone(value string) string {
	if value == "" {
		return "None"
	}
	return value
}
