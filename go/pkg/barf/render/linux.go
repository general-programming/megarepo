package render

import (
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Linux renders a bird2 + ifupdown + wg-quick host as a shell script: one
// quoted heredoc per barf-owned file. Port of the ("vpn", "linux") entry of
// the Python BLOCK_REGISTRY.
type Linux struct{}

// Render returns the deploy script for h.
func (Linux) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "vpn" {
		// Not a gap: Python has no linux template outside the vpn role either.
		return "", noTemplateError(h)
	}
	ctx := newRenderCtx(h, n, s)

	blocks := []func(*renderCtx) ([]string, error){
		linuxHeader,
		linuxFabricWireGuard,
		linuxInterfaces,
		linuxBirdBase,
		linuxBirdFabric,
		vyosExtraConfig, // configs.system.ExtraConfig is identical per vendor.
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

// barfFile is configs.lines.barf_file: the BARF_FILE heredoc writing content
// to path. Deploy parses these blocks back into a path -> content map; the
// quoted delimiter means nothing expands if the script is run by hand.
func barfFile(path string, content []string) []string {
	lines := make([]string, 0, len(content)+2)
	lines = append(lines, "cat << 'BARF_FILE' > "+path)
	lines = append(lines, content...)
	return append(lines, "BARF_FILE")
}

// linuxHeader is configs.system.Header's linux method.
func linuxHeader(c *renderCtx) ([]string, error) {
	return []string{
		"#!/bin/sh",
		fmt.Sprintf("# Rendered by barf for %s.", c.host.Hostname),
		"#",
		"# One quoted heredoc per barf-owned file. `barf config diff|deploy`",
		"# parses these BARF_FILE blocks into a path -> content map and manages",
		"# the files over SSH; running this script by hand writes the exact",
		"# same contents (the delimiter is quoted, so nothing expands).",
		"set -e",
		"mkdir -p /etc/wireguard /etc/network/interfaces.d /etc/bird/conf.d",
		"",
	}, nil
}

// linuxInterfaces is configs.interfaces.InterfacesConfig's linux method; only
// dum* identity anchors are modeled, fabric wg comes from the fabric blocks.
func linuxInterfaces(c *renderCtx) ([]string, error) {
	var lines []string
	for i := range c.host.Interfaces {
		iface := &c.host.Interfaces[i]
		if !strings.HasPrefix(iface.Name, "dum") {
			continue
		}
		description := iface.Description
		if description == "" {
			description = "loopback"
		}
		content := []string{
			fmt.Sprintf("# %s - %s (barf-managed; deploy overwrites)", iface.Name, description),
			"# Anchor addresses on a dummy interface (the VyOS dum0 pattern): the",
			"# host's stable fabric identity, independent of any physical link.",
			"# `inet manual` + explicit address adds handles any v4/v6 mix.",
			"auto " + iface.Name,
			fmt.Sprintf("iface %s inet manual", iface.Name),
			"        pre-up ip link add $IFACE type dummy",
			"        post-up ip link set $IFACE up",
		}
		for _, addr := range iface.Addresses {
			content = append(content, "        post-up ip addr add "+addr.String()+" dev $IFACE")
		}
		content = append(content, "        post-down ip link del $IFACE")

		lines = append(lines, barfFile("/etc/network/interfaces.d/"+iface.Name+".conf", content)...)
		lines = append(lines, "")
	}
	return lines, nil
}

// linuxFabricWireGuard is configs.fabric.FabricWireGuard's linux method: one
// wg .conf plus one interfaces.d stanza per link; BGP is in linuxBirdFabric.
func linuxFabricWireGuard(c *renderCtx) ([]string, error) {
	var lines []string
	for _, link := range c.links {
		peer, err := c.peer(link)
		if err != nil {
			return nil, err
		}
		ourKeys, err := wireguardKeypair(c.secrets, link.KeyPath(c.host.Hostname))
		if err != nil {
			return nil, err
		}
		peerKeys, err := wireguardKeypair(c.secrets, link.KeyPath(peer.Hostname))
		if err != nil {
			return nil, err
		}

		name := fmt.Sprintf("wg%d", link.Port)
		header := fmt.Sprintf("# %s - %s -> %s (barf-managed; deploy overwrites)",
			name, link.A, link.B)

		wgConf := []string{
			header,
			"[Interface]",
			fmt.Sprintf("ListenPort = %d", link.Port),
			"PrivateKey = " + ourKeys.Private,
			"",
			"[Peer]",
			"PublicKey = " + peerKeys.Public,
			"# Route anything the fabric learns through the tunnel; BGP decides.",
			"# ::/0 matters even on v4-only networks: unnumbered BGP runs over",
			"# link-local IPv6, which WireGuard drops unless allowed here.",
			"AllowedIPs = 0.0.0.0/0, ::/0",
		}
		if endpoint := peer.WGEndpoint(); endpoint != "" {
			wgConf = append(wgConf,
				fmt.Sprintf("Endpoint = %s:%d", endpoint, link.Port),
				"PersistentKeepalive = 10",
			)
		}
		lines = append(lines, barfFile("/etc/wireguard/"+name+".conf", wgConf)...)
		lines = append(lines, "chmod 600 /etc/wireguard/"+name+".conf", "")

		var ifupdown []string
		if link.Unnumbered() {
			ifupdown = []string{
				header,
				"auto " + name,
				"# Unnumbered fabric link (RFC 5549): no global address at all, only a",
				"# deterministic link-local (fe80::<host id>) the BGP session runs on.",
				"# `inet manual` because `inet static` refuses to parse without an",
				"# address -- but manual applies no mtu and does not bring the link up,",
				"# so both happen explicitly below.",
				fmt.Sprintf("iface %s inet manual", name),
				"        pre-up ip link add $IFACE mtu 1420 type wireguard",
				"        pre-up wg setconf $IFACE /etc/wireguard/$IFACE.conf",
				"        post-up ip link set $IFACE up",
				fmt.Sprintf("        post-up ip addr add fe80::%d/64 dev $IFACE", c.host.ID),
				"        post-down ip link del $IFACE",
			}
		} else {
			ifupdown = []string{
				header,
				"auto " + name,
				fmt.Sprintf("iface %s inet static", name),
				"        address " + link.GetIP(c.host.Hostname, false),
				"        netmask 255.255.255.254",
				"        mtu 1420",
				"        pre-up ip link add $IFACE type wireguard",
				"        pre-up wg setconf $IFACE /etc/wireguard/$IFACE.conf",
				"        post-down ip link del $IFACE",
			}
		}
		lines = append(lines, barfFile("/etc/network/interfaces.d/"+name+".conf", ifupdown)...)
		lines = append(lines, "")
	}
	return lines, nil
}

// linuxBirdRouterID is configs.routing.BirdBase._router_id.
func linuxBirdRouterID(c *renderCtx) string {
	if c.host.Bird.RouterID != "" {
		return c.host.Bird.RouterID
	}
	for _, link := range c.links {
		if link.Unnumbered() {
			continue
		}
		return link.GetIP(c.host.Hostname, false)
	}
	return "192.0.2.1"
}

// linuxBirdBase is configs.routing.BirdBase: bird.conf plus 00-local.conf.
func linuxBirdBase(c *renderCtx) ([]string, error) {
	host := c.host
	content := []string{
		fmt.Sprintf("# /etc/bird/bird.conf - barf-managed bird2 base for %s.", host.Hostname),
		fmt.Sprintf("# DO NOT HAND-EDIT: `barf config deploy %s` overwrites it.", host.Hostname),
		"#",
		"# This file owns only the plumbing: logging, router id, and the",
		"# device/kernel protocols. Routing policy and the BGP fabric live in",
		"# drop-ins, included in GLOB (alphabetical) order:",
		"#   /etc/bird/conf.d/00-local.conf -- this host's hand-written config,",
		"#                                     tracked in network.yml (`bird:",
		"#                                     local_conf`). Sorts first so its",
		"#                                     definitions (e.g. the import",
		"#                                     filter genprog references) parse",
		"#                                     before genprog.conf.",
		"#   /etc/bird/conf.d/genprog.conf  -- the barf-managed fabric.",
		"#   /etc/bird/conf.d/<anything else> -- yours; barf never touches it.",
		"log syslog all;",
		fmt.Sprintf("router id %s;", linuxBirdRouterID(c)),
		"",
		"protocol device {",
		"\tscan time 10;",
		"}",
		"",
		"protocol kernel {",
	}
	if host.Bird.MergePaths {
		content = append(content, "\tmerge paths on;")
	}
	content = append(content,
		"\tipv4 {",
		"\t\texport filter {",
		"\t\t\t# Static routes are genprog_originate's announce anchors",
		"\t\t\t# (reject routes); installing them would blackhole the",
		"\t\t\t# very prefixes this host serves.",
		"\t\t\tif source = RTS_STATIC then reject;",
	)
	if host.Bird.KrtPrefsrc != "" {
		content = append(content,
			"\t\t\t# Pin the source address of fabric-learned routes so",
			"\t\t\t# host-originated traffic uses our announced identity",
			"\t\t\t# (unnumbered links leave nothing else to pick from).",
			"\t\t\tkrt_prefsrc = "+host.Bird.KrtPrefsrc+";",
		)
	}
	content = append(content,
		"\t\t\taccept;",
		"\t\t};",
		"\t};",
		"}",
		"",
		"protocol kernel {",
		"\tipv6 {",
		"\t\texport where source != RTS_STATIC;",
		"\t};",
		"}",
		"",
		`include "/etc/bird/conf.d/*.conf";`,
	)

	lines := append(barfFile("/etc/bird/bird.conf", content), "")
	if local := host.Bird.LocalConf; local != "" {
		// 00- so its definitions parse before genprog.conf.
		lines = append(lines, barfFile("/etc/bird/conf.d/00-local.conf",
			strings.Split(strings.TrimRight(local, "\n"), "\n"))...)
	}
	return append(lines, ""), nil
}

// birdRejectBlacklisted references a 00-local function.
func birdRejectBlacklisted(checkFunction string) string {
	return fmt.Sprintf(`if %s() then reject "REJECTED ", net, " from ", proto, " blacklisted";`,
		checkFunction)
}

// birdChannels renders both address-family channels of a fabric BGP protocol,
// reproducing the template macro's odd-but-committed indentation.
func birdChannels(importExpr string) []string {
	block := []string{
		"\tipv4 {",
		"\t\timport " + importExpr + ";",
		"\t\texport filter genprog_export;",
		"\t\tnext hop self;",
		"\t\textended next hop on;",
		"\t};",
		"\tipv6 {",
		"\t\timport " + importExpr + ";",
		"\t\texport filter genprog_export;",
		"\t\tnext hop self;",
		"\t};",
	}
	return strings.Split(strings.Join(block, "\n"), "\n")
}

// linuxBirdFabric is configs.fabric.BirdFabric: conf.d/genprog.conf.
func linuxBirdFabric(c *renderCtx) ([]string, error) {
	host := c.host
	rejectBlacklisted := ""
	if host.Bird.ImportCheckFunction != "" {
		rejectBlacklisted = birdRejectBlacklisted(host.Bird.ImportCheckFunction)
	}

	content := []string{
		fmt.Sprintf("# /etc/bird/conf.d/genprog.conf - barf-managed fabric for %s.", host.Hostname),
		fmt.Sprintf("# DO NOT HAND-EDIT: `barf config deploy %s` overwrites it.", host.Hostname),
		"#",
		"# Contents:",
		"#   GENPROG_ASN       -- this host's ASN from network.yml.",
		"#   genprog_export    -- what the fabric hears from us: our originated",
		"#                        prefixes plus routes learned over BGP.",
		"#   genprog_originate -- static (reject) anchors for network.yml",
		"#                        `networks:`; bird only exports routes that",
		"#                        exist in a table, so announcing a prefix means",
		"#                        anchoring it here. The base config keeps these",
		"#                        anchors out of the kernel.",
		"#   genprog_fabric    -- shared session settings; every WireGuard",
		"#                        fabric link gets one `protocol bgp` from it.",
		"#   radv              -- RA beacon on unnumbered interfaces. FRR's",
		"#                        `neighbor <if> interface v6only` learns its",
		"#                        peer from received RAs; without this beacon",
		"#                        the VyOS side idles forever. `default",
		"#                        lifetime 0` = never advertise as a gateway.",
		"#   bfd               -- fast liveness on the wg fabric interfaces.",
		"#",
		"# Unnumbered links (RFC 5549): one IPv6 link-local session per",
		"# interface carries BOTH address families; IPv4 routes ride IPv6",
		"# next-hops via the extended-nexthop capability, and the kernel",
		"# installs them as `via inet6 fe80::...`. Numbered links keep the",
		"# classic session on the link /31.",
		"#",
		"# Geographic BGP path weighting: source = RTS_STATIC (our own",
		"# originated prefixes) get a large-community origin tag when this",
		"# host has a `site`; genprog_import_<neighbor site> filters below set",
		"# bgp_local_pref per origin-site tag on the way in. See",
		"# barf/util/sites.py for the distance math -- routers only see the",
		"# already-computed integers.",
		fmt.Sprintf("define GENPROG_ASN = %d;", host.ASN),
		"",
		"",
		// Site-origin tags carry the fabric-wide community_asn, never the
		// per-host ASN: bird's set grammar cannot wildcard the first field, so
		// importers must match the exact triplet.
		"filter genprog_export {",
		"\tif source = RTS_STATIC then {",
	}
	if c.originSite != nil {
		content = append(content, fmt.Sprintf("\t\tbgp_large_community.add((%d, %d, %d));",
			c.communityAS, siteOriginFunc, c.originSite.ID))
	}
	content = append(content,
		"\t\taccept;",
		"\t}",
		"\tif source = RTS_BGP then accept;",
		"\treject;",
		"}",
	)

	for _, entry := range c.importRules {
		content = append(content, "", "filter genprog_import_"+entry.Site+" {")
		if rejectBlacklisted != "" {
			content = append(content, "\t"+rejectBlacklisted)
		}
		for _, rule := range entry.Rules {
			content = append(content,
				fmt.Sprintf("\tif bgp_large_community ~ [(%d, %d, %d)] then {",
					c.communityAS, siteOriginFunc, rule.SiteID),
				fmt.Sprintf("\t\tbgp_local_pref = %d;", rule.LocalPref),
				"\t\taccept;",
				"\t}",
			)
		}
		content = append(content, "\taccept;", "}")
	}

	var netsV4, netsV6 []string
	for _, network := range host.Networks {
		if strings.Contains(network, ":") {
			netsV6 = append(netsV6, network)
		} else {
			netsV4 = append(netsV4, network)
		}
	}
	if len(netsV4) > 0 {
		content = append(content, "", "protocol static genprog_originate {", "\tipv4;")
		for _, network := range netsV4 {
			content = append(content, "\troute "+network+" reject;")
		}
		content = append(content, "}")
	}
	if len(netsV6) > 0 {
		content = append(content, "", "protocol static genprog_originate6 {", "\tipv6;")
		for _, network := range netsV6 {
			content = append(content, "\troute "+network+" reject;")
		}
		content = append(content, "}")
	}

	defaultImport := "all"
	switch {
	case host.Bird.ImportFilter != "":
		defaultImport = "filter " + host.Bird.ImportFilter
	case rejectBlacklisted != "":
		defaultImport = "filter {\n\t\t" + rejectBlacklisted + "\n\t\taccept;\n\t}"
	}

	content = append(content,
		"",
		"template bgp genprog_fabric {",
		"\tlocal as GENPROG_ASN;",
		"\thold time 30;",
		"\tkeepalive time 10;",
	)
	content = append(content, birdChannels(defaultImport)...)
	content = append(content, "}")

	for _, link := range c.links {
		peer, err := c.peer(link)
		if err != nil {
			return nil, err
		}
		pname := strings.ReplaceAll(peer.Hostname, "-", "_")
		weighted := len(rulesFor(c.importRules, peer.Site)) > 0

		content = append(content, "", fmt.Sprintf("protocol bgp %s from genprog_fabric {", pname))
		if link.Unnumbered() {
			content = append(content,
				"\t# Passive: the FRR side dials our link-local once it sees an RA.",
				fmt.Sprintf("\tinterface \"wg%d\";", link.Port),
				fmt.Sprintf("\tneighbor range fe80::/10 as %d;", peer.ASN),
				fmt.Sprintf("\tdynamic name \"%s_\";", pname),
			)
		} else {
			content = append(content,
				fmt.Sprintf("\tsource address %s;", link.GetIP(c.host.Hostname, false)),
				fmt.Sprintf("\tneighbor %s as %d;", link.GetIP(peer.Hostname, false), peer.ASN),
			)
		}
		if weighted {
			content = append(content,
				fmt.Sprintf("\t# Geographic weighting: this neighbor is in site %s.", peer.Site))
			content = append(content, birdChannels("filter genprog_import_"+peer.Site)...)
		}
		content = append(content, "}")
	}

	var unnumbered []string
	for _, link := range c.links {
		if link.Unnumbered() {
			unnumbered = append(unnumbered, fmt.Sprintf("\"wg%d\"", link.Port))
		}
	}
	if len(unnumbered) > 0 {
		content = append(content,
			"",
			"protocol radv {",
			fmt.Sprintf("\tinterface %s {", strings.Join(unnumbered, ", ")),
			"\t\tmax ra interval 15;",
			"\t\tdefault lifetime 0;",
			"\t};",
			"}",
		)
	}

	content = append(content,
		"",
		"protocol bfd {",
		"\tinterface \"wg*\" {",
		"\t\tmin rx interval 150 ms;",
		"\t\tmin tx interval 150 ms;",
		"\t\tidle tx interval 1500 ms;",
		"\t};",
		"}",
	)

	return append(barfFile("/etc/bird/conf.d/genprog.conf", content), "", "birdc configure"), nil
}
