package render

import (
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// VyOS renders a whole VyOS config as `set` commands. Port of the
// ("vpn", "vyos") Python BLOCK_REGISTRY entry: block order IS output order
// and, like the blank separator lines each block carries, byte-parity contract.
type VyOS struct{}

// renderCtx is the Go twin of barf.configs.base.RenderContext.
type renderCtx struct {
	host    *model.Host
	network *model.Network
	global  model.GlobalMeta
	secrets SecretSource

	// links are the host's own fabric links, in network.yml order.
	links []model.Link
	// importRules are the geographic weighting inputs, empty when unweighted.
	importRules []SiteRules
	originSite  *model.Site
	communityAS int
}

// newRenderCtx builds the shared render context, or fails fast on a
// configuration bird cannot express. Port of build_context
// (barf/configs/__init__.py): bird.import_filter is a standalone filter, and
// bird filters cannot call other filters, so combining it with the
// generated site-weighted filters (linux.go's genprog_import_<site>) would
// silently discard the host's own filter on every weighted peer — the
// override happens unconditionally in birdChannels' caller. Catching it here
// means every vendor sharing this context (linux, vyos, mikrotik, edgeos)
// refuses the same way, rather than one of them quietly losing the filter.
func newRenderCtx(h *model.Host, n *model.Network, s SecretSource) (*renderCtx, error) {
	ctx := &renderCtx{host: h, network: n, global: n.Global, secrets: s}
	if h.Role != "vpn" {
		return ctx, nil
	}
	ctx.links = n.LinksFor(h.Hostname)
	ctx.importRules = siteImportRules(h, ctx.links, n)
	if site, ok := n.Global.Sites[h.Site]; ok && h.Site != "" {
		ctx.originSite = &site
	}
	ctx.communityAS = n.Global.CommunityASN

	if len(ctx.importRules) > 0 && h.Bird.ImportFilter != "" {
		return nil, fmt.Errorf(
			"%s: bird.import_filter cannot be combined with site weighting; "+
				"expose the check as a function and set bird.import_check_function instead",
			h.Hostname)
	}
	return ctx, nil
}

// peer returns the host on the far side of a link.
func (c *renderCtx) peer(link model.Link) (*model.Host, error) {
	other := link.Other(c.host.Hostname)
	host, ok := c.network.Host(other)
	if !ok {
		return nil, fmt.Errorf("%s: link peer %q is not a known host", c.host.Hostname, other)
	}
	return host, nil
}

// Render returns the full VyOS config text for h.
func (VyOS) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "vpn" {
		// Not a gap: Python has no vyos template outside the vpn role either.
		return "", noTemplateError(h)
	}
	ctx, err := newRenderCtx(h, n, s)
	if err != nil {
		return "", err
	}

	blocks := []func(*renderCtx) ([]string, error){
		vyosSystemConfig,
		vyosInterfaces,
		vyosSSHConfig,
		vyosPlatform,
		vyosFirewallGroups,
		vyosNTPConfig,
		vyosOSPF,
		vyosStaticRoutes,
		vyosNAT,
		vyosIPsecDefaults,
		vyosSiteWeighting,
		vyosFabricWireGuard,
		vyosFabricBGP,
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

// -- system -----------------------------------------------------------

// vyosSystemConfig is configs.system.SystemConfig.
func vyosSystemConfig(c *renderCtx) ([]string, error) {
	adminPassword, err := c.secrets.HostSecret(c.host.Hostname, "admin-password")
	if err != nil {
		return nil, err
	}

	lines := []string{
		fmt.Sprintf("set system host-name '%s'", c.host.Hostname),
		fmt.Sprintf("set system domain-name '%s'", c.global.SearchDomain),
		fmt.Sprintf("set system login user supertech authentication plaintext-password '%s'",
			adminPassword),
		fmt.Sprintf("set service snmp community '%s' authorization 'ro'", c.global.SNMPPublic),
	}
	location := c.host.SNMPLocation
	if location == "" {
		location = c.global.SNMPLocation
	}
	if location != "" {
		lines = append(lines, fmt.Sprintf("set service snmp location '%s'", location))
	}
	lines = append(lines, fmt.Sprintf("set service snmp contact '%s'", c.global.SNMPContact))
	for _, server := range c.host.Nameservers {
		lines = append(lines, fmt.Sprintf("set system name-server '%s'", server))
	}
	lines = append(lines,
		"",
		// Factory-default empty loopback node; rendering it keeps the diff clean.
		"set interfaces loopback lo",
		"",
		"set system syslog local facility all level info",
		"set system syslog local facility local7 level debug",
		"set system console device ttyS0 speed 115200",
		"set system config-management commit-revisions 100",
		"",
	)
	// Conntrack helpers: the vyatta-era defaults, set explicitly.
	for _, module := range []string{"ftp", "h323", "nfs", "pptp", "sip", "sqlnet", "tftp"} {
		lines = append(lines, "set system conntrack modules "+module)
	}
	return lines, nil
}

// vyosSSHConfig is configs.system.SshConfig.
func vyosSSHConfig(c *renderCtx) ([]string, error) {
	lines := []string{"", "set service ssh disable-password-authentication"}
	for _, sshKey := range c.global.SSHKeys {
		parts := strings.SplitN(sshKey, " ", 4)
		if len(parts) < 3 {
			return nil, fmt.Errorf("%s: ssh key %q has no comment to name it on",
				c.host.Hostname, sshKey)
		}
		keyType, key, name := parts[0], parts[1], parts[2]
		lines = append(lines,
			fmt.Sprintf("set system login user supertech authentication public-keys %s key %s",
				name, key),
			fmt.Sprintf("set system login user supertech authentication public-keys %s type %s",
				name, keyType),
		)
	}
	return append(lines, ""), nil
}

// vyosPlatform is configs.system.VyosPlatform.
func vyosPlatform(c *renderCtx) ([]string, error) {
	apiKey, err := vaultSecret(c.secrets, "vyos_api_password")
	if err != nil {
		return nil, err
	}
	return []string{
		"set system update-check url https://raw.githubusercontent.com/vyos/" +
			"vyos-nightly-build/refs/heads/rolling/version.json",
		"set service ssh port 22",
		"set protocols bfd profile bgp echo-mode",
		"set protocols bfd profile bgp interval echo-interval 50",
		"set service https api rest",
		fmt.Sprintf("set service https api keys id vaultadmin key '%s'", apiKey),
		"",
		"set firewall global-options all-ping enable",
		"set firewall global-options broadcast-ping enable",
		"",
	}, nil
}

// ntpServers and ntpAllowClients mirror configs.system.NtpConfig.
var ntpServers = []string{"time1.vyos.net", "time2.vyos.net", "time3.vyos.net"}

var ntpAllowClients = []string{
	"10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.168.0.0/16", "::1/128", "fc00::/7", "fe80::/10",
}

func vyosNTPConfig(c *renderCtx) ([]string, error) {
	var lines []string
	for _, server := range ntpServers {
		lines = append(lines, "set service ntp server "+server)
	}
	// The allow-client syntax is chrony-era VyOS, not shared vyatta.
	for _, network := range ntpAllowClients {
		lines = append(lines, "set service ntp allow-client address "+network)
	}
	return append(lines, ""), nil
}

// vyosExtraConfig is configs.system.ExtraConfig; the leading blank is the
// template's final unconditional separator.
func vyosExtraConfig(c *renderCtx) ([]string, error) {
	return append([]string{""}, c.host.ExtraConfig...), nil
}

// -- interfaces -------------------------------------------------------

// vyosInterfacePrefix is VyOSHost.interface_prefix, the stem lines hang off.
func vyosInterfacePrefix(iface *model.Interface) string {
	interfaceType := "ethernet"
	switch {
	case iface.IsBridge():
		interfaceType = "bridge"
	case iface.IsWireguard():
		interfaceType = "wireguard"
	case iface.Management:
		interfaceType = "dummy"
	}

	prefix := fmt.Sprintf("set interfaces %s %s", interfaceType, iface.Name)
	if iface.UntaggedVLAN != nil {
		prefix += fmt.Sprintf(" vif %d", iface.UntaggedVLAN.VID)
	}
	return prefix
}

// vyosInterfaces is configs.interfaces.InterfacesConfig.
func vyosInterfaces(c *renderCtx) ([]string, error) {
	return vyattaInterfaces(c, vyosInterfacePrefix)
}

// vyattaInterfaces is the shared common/vyatta.j2 loop; interface_prefix is
// the only thing that varies between vyatta vendors.
func vyattaInterfaces(c *renderCtx, interfacePrefix func(*model.Interface) string) ([]string, error) {
	var lines []string
	for i := range c.host.Interfaces {
		iface := &c.host.Interfaces[i]
		prefix := interfacePrefix(iface)

		if iface.DHCP {
			lines = append(lines, prefix+" address dhcp")
		}
		if iface.DHCPv6 {
			lines = append(lines, prefix+" address dhcpv6")
		}
		if iface.IPv6Autoconf {
			lines = append(lines, prefix+" ipv6 address autoconf")
		}
		for _, addr := range iface.Addresses {
			lines = append(lines, prefix+" address "+addr.String())
		}
		if iface.MTU != 0 {
			lines = append(lines, fmt.Sprintf("%s mtu %d", prefix, iface.MTU))
		}
		if iface.Description != "" {
			lines = append(lines, fmt.Sprintf("%s description '%s'", prefix, iface.Description))
		}
		for _, member := range iface.Members {
			lines = append(lines,
				fmt.Sprintf("set interfaces bridge %s member interface %s", iface.Name, member))
		}

		if iface.IsWireguard() && len(iface.Wireguard) > 0 {
			wgLines, err := vyosStaticWireguard(c, iface, prefix)
			if err != nil {
				return nil, err
			}
			lines = append(lines, wgLines...)
		}
	}
	return lines, nil
}

// vyosStaticWireguard renders a static (non-fabric) WireGuard tunnel.
func vyosStaticWireguard(c *renderCtx, iface *model.Interface, prefix string) ([]string, error) {
	wg := iface.Wireguard
	privateKey, err := c.secrets.HostSecret(c.host.Hostname, wgString(wg, "private_key_secret"))
	if err != nil {
		return nil, err
	}
	port := wgString(wg, "port")

	lines := []string{
		fmt.Sprintf("%s private-key '%s'", prefix, privateKey),
		fmt.Sprintf("%s port %s", prefix, port),
	}
	peers, _ := wg["peers"].([]any)
	for _, raw := range peers {
		peer, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		peerPrefix := fmt.Sprintf("%s peer %s", prefix, wgString(peer, "name"))
		lines = append(lines,
			fmt.Sprintf("%s public-key '%s'", peerPrefix, wgString(peer, "public_key")))

		allowed := wgStrings(peer, "allowed_ips")
		if allowed == nil {
			allowed = []string{"0.0.0.0/0", "::/0"}
		}
		for _, ip := range allowed {
			lines = append(lines, fmt.Sprintf("%s allowed-ips '%s'", peerPrefix, ip))
		}
		if endpoint := wgString(peer, "endpoint"); endpoint != "" {
			peerPort := wgString(peer, "port")
			if peerPort == "" {
				peerPort = port
			}
			lines = append(lines,
				fmt.Sprintf("%s address '%s'", peerPrefix, endpoint),
				fmt.Sprintf("%s port %s", peerPrefix, peerPort),
			)
		}
		if keepalive := wgString(peer, "keepalive"); keepalive != "" {
			// VyOS keepalive is a bare number of seconds.
			lines = append(lines, fmt.Sprintf("%s persistent-keepalive %s",
				peerPrefix, strings.ReplaceAll(keepalive, "s", "")))
		}
	}
	return lines, nil
}

func wgString(m map[string]any, key string) string {
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func wgStrings(m map[string]any, key string) []string {
	items, ok := m[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

// -- firewall / nat ---------------------------------------------------

// vyosFirewallGroups is configs.firewall.FirewallGroups; interface-groups are
// flat, so include= nesting is resolved here.
func vyosFirewallGroups(c *renderCtx) ([]string, error) {
	var lines []string
	for _, group := range c.host.Firewall.Address {
		v4, err := group.MembersV(4)
		if err != nil {
			return nil, err
		}
		for _, member := range v4 {
			lines = append(lines, fmt.Sprintf(
				"set firewall group network-group %s network %s", group.Name, member.Address))
		}
		v6, err := group.MembersV(6)
		if err != nil {
			return nil, err
		}
		for _, member := range v6 {
			lines = append(lines, fmt.Sprintf(
				"set firewall group ipv6-network-group %s network %s", group.Name, member.Address))
		}
	}
	for _, group := range c.host.Firewall.Interface {
		for _, iface := range c.host.Firewall.ResolvedInterfaces(group.Name) {
			lines = append(lines, fmt.Sprintf(
				"set firewall group interface-group %s interface %s", group.Name, iface))
		}
	}
	return append(lines, ""), nil
}

// vyosNAT is configs.firewall.Nat: the declarative nat: block.
func vyosNAT(c *renderCtx) ([]string, error) {
	var lines []string
	for _, rule := range c.host.NATMasquerades {
		src := fmt.Sprintf("set nat source rule %d", rule.Rule)
		lines = append(lines, fmt.Sprintf("%s outbound-interface name %s", src, rule.Interface))
		if rule.Source != "" {
			lines = append(lines, fmt.Sprintf("%s source address %s", src, rule.Source))
		}
		if rule.Destination != "" {
			lines = append(lines, fmt.Sprintf("%s destination address %s", src, rule.Destination))
		}
		lines = append(lines, src+" translation address masquerade")
	}
	for _, fwd := range c.host.NATPortForwards {
		dst := fmt.Sprintf("set nat destination rule %d", fwd.Rule)
		lines = append(lines,
			fmt.Sprintf("%s description '%s'", dst, fwd.Description()),
			fmt.Sprintf("%s inbound-interface name %s", dst, fwd.Interface),
			fmt.Sprintf("%s protocol %s", dst, fwd.Protocol),
			fmt.Sprintf("%s destination port %d", dst, fwd.Port),
			fmt.Sprintf("%s translation address %s", dst, fwd.To),
		)
		if fwd.TranslationPort != 0 {
			lines = append(lines, fmt.Sprintf("%s translation port %d", dst, fwd.TranslationPort))
		}
	}
	return lines, nil
}

// -- routing ----------------------------------------------------------

// vyosOSPF is configs.routing.Ospf.
func vyosOSPF(c *renderCtx) ([]string, error) {
	ospf := c.host.OSPF
	var lines []string
	for _, network := range ospf.Networks {
		lines = append(lines, fmt.Sprintf("set protocols ospf area %s network %s",
			network.Area, network.Network))
	}
	for _, iface := range ospf.Interfaces {
		if iface.MTUIgnore {
			lines = append(lines, "set protocols ospf interface "+iface.Name+" mtu-ignore")
		}
	}
	for _, redist := range ospf.Redistribute {
		line := "set protocols ospf redistribute " + redist.Protocol
		if redist.MetricType != 0 {
			line += fmt.Sprintf(" metric-type %d", redist.MetricType)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// vyosStaticRoutes is configs.routing.StaticRoutes.
func vyosStaticRoutes(c *renderCtx) ([]string, error) {
	var lines []string
	for _, route := range c.host.StaticRoutes {
		if route.Interface != "" {
			lines = append(lines, fmt.Sprintf("set protocols static route %s interface %s",
				route.Network, route.Interface))
		}
		if route.NextHop != "" {
			lines = append(lines, fmt.Sprintf("set protocols static route %s next-hop %s",
				route.Network, route.NextHop))
		}
	}
	return append(lines, ""), nil
}

// -- fabric -----------------------------------------------------------

// vyosIPsecDefaults is configs.fabric.IpsecDefaults.
func vyosIPsecDefaults(c *renderCtx) ([]string, error) {
	return []string{
		"",
		"set vpn ipsec esp-group ESP_DEFAULT lifetime '3600'",
		"set vpn ipsec esp-group ESP_DEFAULT mode 'tunnel'",
		"set vpn ipsec esp-group ESP_DEFAULT pfs enable",
		"set vpn ipsec esp-group ESP_DEFAULT proposal 1 encryption 'aes256'",
		"set vpn ipsec esp-group ESP_DEFAULT proposal 1 hash 'sha256'",
		"",
		"set vpn ipsec ike-group IKEv2_DEFAULT key-exchange 'ikev2'",
		"set vpn ipsec ike-group IKEv2_DEFAULT lifetime 28800",
		"set vpn ipsec ike-group IKEv2_DEFAULT disable-mobike",
		"set vpn ipsec ike-group IKEv2_DEFAULT proposal 1 dh-group 5",
		"set vpn ipsec ike-group IKEv2_DEFAULT proposal 1 encryption aes256",
		"set vpn ipsec ike-group IKEv2_DEFAULT proposal 1 hash sha256",
		"",
		"set vpn ipsec interface eth0",
		"",
	}, nil
}

// vyosSiteWeighting is configs.fabric.SiteWeighting. The section is gated on
// originSite but its trailing separator is not, so unweighted hosts still
// emit the blank line.
func vyosSiteWeighting(c *renderCtx) ([]string, error) {
	var lines []string
	if c.originSite != nil {
		lines = append(lines,
			"set policy route-map TAG-SITE-ORIGIN rule 10 action permit",
			fmt.Sprintf("set policy route-map TAG-SITE-ORIGIN rule 10 set large-community"+
				" add '%d:%d:%d'", c.communityAS, siteOriginFunc, c.originSite.ID),
		)
	}
	if len(c.importRules) > 0 {
		for _, site := range sitesByID(c.global.Sites) {
			originList := fmt.Sprintf("set policy large-community-list SITE-ORIGIN-%d", site.ID)
			lines = append(lines,
				originList+" rule 10 action permit",
				fmt.Sprintf("%s rule 10 regex '^%d:%d:%d$'",
					originList, c.communityAS, siteOriginFunc, site.ID),
			)
		}
		for _, entry := range c.importRules {
			importMap := "set policy route-map IMPORT-" + strings.ToUpper(entry.Site)
			for index, rule := range entry.Rules {
				number := (index + 1) * 10
				lines = append(lines,
					fmt.Sprintf("%s rule %d action permit", importMap, number),
					fmt.Sprintf("%s rule %d match large-community large-community-list"+
						" SITE-ORIGIN-%d", importMap, number, rule.SiteID),
					fmt.Sprintf("%s rule %d set local-preference %d",
						importMap, number, rule.LocalPref),
				)
			}
			// Trailing catch-all: untagged routes fall through unweighted.
			lines = append(lines, fmt.Sprintf("%s rule %d action permit",
				importMap, (len(entry.Rules)+1)*10))
		}
	}
	return append(lines, ""), nil
}

// vyosFabricWireGuard is configs.fabric.FabricWireGuard's vyos method. VyOS
// interleaves each link's interface with its BGP neighbor, so the whole
// per-link loop lives here; the shared BGP tail is vyosFabricBGP.
func vyosFabricWireGuard(c *renderCtx) ([]string, error) {
	management := c.host.ManagementAddress()
	var lines []string

	for _, link := range c.links {
		if management == nil {
			// The template silently rendered a bare `update-source` here; a
			// fabric host without a management interface is a modeling error.
			return nil, fmt.Errorf("%s: fabric BGP needs a management address for update-source",
				c.host.Hostname)
		}
		peer, err := c.peer(link)
		if err != nil {
			return nil, err
		}

		var interfaceName string
		if link.IPsec {
			ipsecLines, name, err := vyosIPsecLink(c, link, peer)
			if err != nil {
				return nil, err
			}
			lines = append(lines, ipsecLines...)
			interfaceName = name
		} else {
			wgLines, name, err := vyosWireguardLink(c, link, peer)
			if err != nil {
				return nil, err
			}
			lines = append(lines, wgLines...)
			interfaceName = name
		}

		bgpNeighbor := interfaceName
		if !link.Unnumbered() {
			bgpNeighbor, err = link.GetIP(peer.Hostname, false)
			if err != nil {
				return nil, err
			}
		}
		neigh := "set protocols bgp neighbor " + bgpNeighbor
		if link.Unnumbered() {
			lines = append(lines,
				fmt.Sprintf("%s description '%s'", neigh, peer.Hostname),
				fmt.Sprintf("%s update-source %s", neigh, management.IP.String()),
				neigh+" interface peer-group fabric",
				neigh+" interface v6only peer-group fabric",
				neigh+" interface v6only remote-as external",
			)
		} else {
			lines = append(lines,
				fmt.Sprintf("%s description '%s'", neigh, peer.Hostname),
				neigh+" remote-as external",
				fmt.Sprintf("%s update-source %s", neigh, management.IP.String()),
				neigh+" peer-group fabric",
			)
		}
		if peer.CanBFD() {
			lines = append(lines, neigh+" bfd")
		}
		if len(rulesFor(c.importRules, peer.Site)) > 0 {
			importMap := "IMPORT-" + strings.ToUpper(peer.Site)
			lines = append(lines,
				fmt.Sprintf("%s address-family ipv4-unicast route-map import %s", neigh, importMap),
				fmt.Sprintf("%s address-family ipv6-unicast route-map import %s", neigh, importMap),
			)
		}
	}
	return lines, nil
}

func vyosIPsecLink(c *renderCtx, link model.Link, peer *model.Host) ([]string, string, error) {
	if link.Secret == "" {
		return nil, "", fmt.Errorf("%s: ipsec link to %s has no secret",
			c.host.Hostname, peer.Hostname)
	}
	secret, err := vaultSecret(c.secrets, link.Secret)
	if err != nil {
		return nil, "", err
	}

	interfaceName := fmt.Sprintf("vti%d", link.Port)
	peerName := "peer_" + strings.ReplaceAll(peer.AddressRaw, ".", "-")
	psk := "set vpn ipsec authentication psk " + peerName
	sts := "set vpn ipsec site-to-site peer " + peerName

	ourIP, err := link.GetIP(c.host.Hostname, false)
	if err != nil {
		return nil, "", err
	}

	return []string{
		fmt.Sprintf("set interfaces vti %s address '%s/31'",
			interfaceName, ourIP),
		"",
		fmt.Sprintf("%s id '%s'", psk, c.host.AddressRaw),
		fmt.Sprintf("%s id '%s'", psk, peer.AddressRaw),
		psk + " id any",
		fmt.Sprintf("%s secret '%s'", psk, secret),
		"",
		fmt.Sprintf("%s description '%s -> %s'", sts, link.A, link.B),
		fmt.Sprintf("%s authentication local-id '%s'", sts, c.host.AddressRaw),
		sts + " authentication mode 'pre-shared-secret'",
		fmt.Sprintf("%s authentication remote-id '%s'", sts, peer.AddressRaw),
		sts + " connection-type initiate",
		sts + " default-esp-group 'ESP_DEFAULT'",
		sts + " ike-group 'IKEv2_DEFAULT'",
		sts + " local-address any",
		fmt.Sprintf("%s remote-address '%s'", sts, peer.AddressRaw),
		fmt.Sprintf("%s vti bind '%s'", sts, interfaceName),
		sts + " vti esp-group 'ESP_DEFAULT'",
	}, interfaceName, nil
}

func vyosWireguardLink(c *renderCtx, link model.Link, peer *model.Host) ([]string, string, error) {
	ourKeys, err := wireguardKeypair(c.secrets, link.KeyPath(c.host.Hostname))
	if err != nil {
		return nil, "", err
	}
	peerKeys, err := wireguardKeypair(c.secrets, link.KeyPath(peer.Hostname))
	if err != nil {
		return nil, "", err
	}

	interfaceName := fmt.Sprintf("wg%d", link.Port)
	iw := "set interfaces wireguard " + interfaceName
	peerPrefix := fmt.Sprintf("%s peer %s", iw, peer.Hostname)

	lines := []string{
		fmt.Sprintf("%s description 'wg link (%s -> %s)'", iw, link.A, link.B),
	}
	if !link.Unnumbered() {
		ourIP, err := link.GetIP(c.host.Hostname, true)
		if err != nil {
			return nil, "", err
		}
		lines = append(lines, fmt.Sprintf("%s address %s", iw, ourIP))
	}
	lines = append(lines,
		fmt.Sprintf("%s private-key '%s'", iw, ourKeys.Private),
		fmt.Sprintf("%s port %d", iw, link.Port),
		fmt.Sprintf("%s public-key '%s'", peerPrefix, peerKeys.Public),
		peerPrefix+" allowed-ips 0.0.0.0/0",
		peerPrefix+" allowed-ips ::/0",
	)
	if endpoint := peer.WGEndpoint(); endpoint != "" {
		lines = append(lines,
			fmt.Sprintf("%s address %s", peerPrefix, endpoint),
			fmt.Sprintf("%s port %d", peerPrefix, link.Port),
			peerPrefix+" persistent-keepalive 10",
		)
	}
	return lines, interfaceName, nil
}

// vyosFabricBGP is configs.fabric.FabricBGP's vyos method.
func vyosFabricBGP(c *renderCtx) ([]string, error) {
	// The route-map suffix rides the same config node; a separate bare
	// `network X` line would read as a forever-pending diff.
	tagSuffix := ""
	if c.originSite != nil {
		tagSuffix = " route-map TAG-SITE-ORIGIN"
	}

	var lines []string
	for _, network := range c.host.Networks {
		lines = append(lines, fmt.Sprintf(
			"set protocols bgp address-family ipv4-unicast network %s%s", network, tagSuffix))
	}
	if management := c.host.ManagementAddress(); management != nil {
		family := "ipv4"
		if management.IsV6() {
			family = "ipv6"
		}
		lines = append(lines, fmt.Sprintf(
			"set protocols bgp address-family %s-unicast network %s%s",
			family, management.String(), tagSuffix))
	}

	return append(lines,
		"",
		fmt.Sprintf("set protocols bgp system-as '%d'", c.host.ASN),
		"set protocols bgp peer-group fabric address-family ipv4-unicast",
		"set protocols bgp peer-group fabric address-family ipv6-unicast",
		"set protocols bgp peer-group fabric capability extended-nexthop",
		// Without this, numbered fabric peers drop routes from the v6-only
		// sea1 spine: it lets FRR set a resolvable IPv4 next-hop.
		"set protocols bgp peer-group fabric disable-connected-check",
		"set protocols bgp timers keepalive 10",
		"set protocols bgp timers holdtime 30",
	), nil
}
