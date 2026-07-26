package render

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// EdgeOS renders a Ubiquiti EdgeOS/vyatta host in the vpn role. Port of
// vpn/edgeos.j2, whose shared prefix is byte-identical to the VyOS blocks
// extracted from the same include; only interface_prefix and fabric differ.
//
// The legacy template is ported bug-for-bug because parity is the contract.
// Three bugs reproduced here, unfixed because barf has no EdgeOS host to
// validate against:
//
//   - Tunnels are named from the LAST THREE DIGITS of the link port
//     ("wg080" for 51080), so ports sharing a suffix collide.
//   - Unnumbered links have no /31, so `link.get_ip()` is None and the
//     literal "None" lands in the address and neighbor lines; every modern
//     fabric link is unnumbered, so this output does not work as-is.
//   - Peers are keyed by public key, tunnels carry only
//     `allowed-ips 0.0.0.0/0` (no IPv6), and IPsec links are not handled.
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

// edgeosInterfacePrefix is EdgeOSHost.interface_prefix; with no bridge or
// wireguard branch, both render under `set interfaces ethernet`.
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

// edgeosFabric is the vpn/edgeos.j2 body: a tunnel and BGP neighbor per link,
// then announced networks and boilerplate.
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
		// Only dialable peers get an endpoint, in the raw network.yml spelling.
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
			// `device.management_address.ip` is Undefined without a management
			// interface, which is why this line keeps its trailing space.
			fmt.Sprintf("%s update-source %s", neighbor, managementIP(c.host)),
			fmt.Sprintf("%s peer-group %s", neighbor, peerGroup),
		)
	}

	for _, network := range c.host.Networks {
		lines = append(lines, fmt.Sprintf("set protocols bgp %s network %s", asn, network))
	}
	// The loopback announcement carries its prefix length, and is literally
	// "None" without a management interface.
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

// managementIP is `device.management_address.ip`, empty when there is none.
func managementIP(h *model.Host) string {
	if address := h.ManagementAddress(); address != nil {
		return address.IP.String()
	}
	return ""
}

// pythonNone renders an absent value as Jinja renders None -- what the legacy
// template does with unnumbered links.
func pythonNone(value string) string {
	if value == "" {
		return "None"
	}
	return value
}
