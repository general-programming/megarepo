"""Fabric blocks: mesh links, BGP sessions, weighting, transit, IPsec.

The fabric contract every vendor implements: WireGuard (or IPsec)
links, eBGP per link, announced networks, and geographic path
weighting (origin-site large communities out, local-pref in) when the
host has a `site`. Byte-identical to the template stanzas replaced
(tests/golden/).
"""

from typing import TYPE_CHECKING, List

from barf.configs.base import ConfigBlock, secret_value
from barf.configs.lines import barf_file, ros_kv, ros_line
from barf.util.sites import SITE_ORIGIN_FUNC

if TYPE_CHECKING:
    from barf.model.wireguard import WGNetworkLink
    from barf.vendors import BaseHost

# The fabric's numbered-link /31 pool (transit export policy).
FABRIC_LINK_NET = "172.31.255.0/24"


def link_sides(link: "WGNetworkLink", device: "BaseHost"):
    """(our_side, other_peer) for a link touching ``device``."""
    if link.side_a == device:
        return link.side_a, link.side_b
    return link.side_b, link.side_a


def transit_of(host: "BaseHost") -> List[dict]:
    """The host's modeled transit ASes (bgp.transit), if any."""
    return host.bgp.get("transit", [])


def announced_networks(host: "BaseHost") -> List[str]:
    """Own networks plus modeled transit link networks.

    Transit link nets are announced (and tagged) like our own networks
    so every fabric session, adopted or new, advertises identically.
    """
    return host.networks + [t["link_network"] for t in transit_of(host)]


class IpsecDefaults(ConfigBlock):
    """The fleet-standard IPsec ESP/IKE groups (vpn-role VyOS boilerplate)."""

    def vyos(self) -> List[str]:
        return [
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
        ]


class FabricWireGuard(ConfigBlock):
    """The Vault-derived fabric mesh links: WG interfaces + addressing.

    Unnumbered links (RFC 5549) get ND instead of an address: RAs teach
    us the peer's link-local (prefix=none answers without any global
    address; ND multicast rides the tunnel because allowed-address
    covers ::/0 -- live-verified on 7.22.3). /ipv6/nd entries silently
    discard comment= (accepted, never stored), so none is rendered: it
    would read as a forever-pending diff.
    """

    def mikrotik(self) -> List[str]:
        lines = []
        for link in self.ctx.vpn_links:
            interface_name = f"wg{link.link_id}"
            our_side, other_peer = link_sides(link, self.host)

            lines.append(
                ros_line(
                    "/interface/wireguard",
                    "add",
                    ros_kv("name", interface_name),
                    ros_kv("listen-port", link.link_id),
                    ros_kv("mtu", 1420),
                    ros_kv("private-key", link.wg_privkey(our_side), quote=True),
                    ros_kv(
                        "comment",
                        f"barf: {link.side_a.hostname} -> {link.side_b.hostname}",
                        quote=True,
                    ),
                )
            )
            peer = ros_line(
                "/interface/wireguard/peers",
                "add",
                ros_kv("interface", interface_name),
                ros_kv("name", other_peer.hostname),
                ros_kv("public-key", link.wg_pubkey(other_peer), quote=True),
                ros_kv("allowed-address", "0.0.0.0/0,::/0"),
            )
            if other_peer.wg_endpoint:
                peer += (
                    f" endpoint-address={other_peer.wg_endpoint}"
                    f" endpoint-port={link.link_id} persistent-keepalive=10s"
                )
            lines.append(peer)

            if link.unnumbered:
                lines.append(
                    f"/ipv6/nd add interface={interface_name} ra-interval=10s-15s"
                )
                lines.append(
                    "/ipv6/nd/prefix add prefix=none"
                    f" interface={interface_name}"
                    f' comment="barf: {other_peer.hostname} unnumbered BGP"'
                )
            else:
                lines.append(
                    f"/ip/address add address={link.get_ip(our_side, with_netmask=True)}"
                    f" interface={interface_name}"
                    f' comment="barf: {other_peer.hostname} link"'
                )
        return [*lines, ""]

    def vyos(self) -> List[str]:
        # VyOS interleaves each link's interface (WireGuard or
        # IPsec/vti) with its BGP neighbor, so the whole per-link loop
        # lives here; FabricBGP renders the shared tail (networks,
        # system-as, peer-group, timers).
        management = self.host.management_address
        lines = []
        for link in self.ctx.vpn_links:
            if management is None:
                # The template silently rendered a bare `update-source`
                # here; a fabric host without a management interface is
                # a modeling error.
                raise ValueError(
                    f"{self.host.hostname}: fabric BGP needs a management"
                    " address for update-source"
                )
            our_side, other_peer = link_sides(link, self.host)

            if link.ipsec:
                if not link.secret:
                    raise ValueError(
                        f"{self.host.hostname}: ipsec link to"
                        f" {other_peer.hostname} has no secret"
                    )
                interface_name = f"vti{link.link_id}"
                peer_name = "peer_" + other_peer.address.replace(".", "-")
                psk = f"set vpn ipsec authentication psk {peer_name}"
                sts = f"set vpn ipsec site-to-site peer {peer_name}"
                lines += [
                    f"set interfaces vti {interface_name} address"
                    f" '{link.get_ip(our_side)}/31'",
                    "",
                    f"{psk} id '{our_side.address}'",
                    f"{psk} id '{other_peer.address}'",
                    f"{psk} id any",
                    f"{psk} secret '{secret_value(self.ctx.secrets, link.secret)}'",
                    "",
                    f"{sts} description"
                    f" '{link.side_a.hostname} -> {link.side_b.hostname}'",
                    f"{sts} authentication local-id '{our_side.address}'",
                    f"{sts} authentication mode 'pre-shared-secret'",
                    f"{sts} authentication remote-id '{other_peer.address}'",
                    f"{sts} connection-type initiate",
                    f"{sts} default-esp-group 'ESP_DEFAULT'",
                    f"{sts} ike-group 'IKEv2_DEFAULT'",
                    f"{sts} local-address any",
                    f"{sts} remote-address '{other_peer.address}'",
                    f"{sts} vti bind '{interface_name}'",
                    f"{sts} vti esp-group 'ESP_DEFAULT'",
                ]
            else:
                interface_name = f"wg{link.link_id}"
                iw = f"set interfaces wireguard {interface_name}"
                peer = f"{iw} peer {other_peer.hostname}"
                lines.append(
                    f"{iw} description"
                    f" 'wg link ({link.side_a.hostname} -> {link.side_b.hostname})'"
                )
                if not link.unnumbered:
                    lines.append(
                        f"{iw} address {link.get_ip(our_side, with_netmask=True)}"
                    )
                lines += [
                    f"{iw} private-key '{link.wg_privkey(our_side)}'",
                    f"{iw} port {link.link_id}",
                    f"{peer} public-key '{link.wg_pubkey(other_peer)}'",
                    f"{peer} allowed-ips 0.0.0.0/0",
                    f"{peer} allowed-ips ::/0",
                ]
                if other_peer.wg_endpoint:
                    lines += [
                        f"{peer} address {other_peer.wg_endpoint}",
                        f"{peer} port {link.link_id}",
                        f"{peer} persistent-keepalive 10",
                    ]

            bgp_neighbor = (
                interface_name if link.unnumbered else link.get_ip(other_peer)
            )
            neigh = f"set protocols bgp neighbor {bgp_neighbor}"
            if link.unnumbered:
                lines += [
                    f"{neigh} description '{other_peer.hostname}'",
                    f"{neigh} update-source {management.ip}",
                    f"{neigh} interface peer-group fabric",
                    f"{neigh} interface v6only peer-group fabric",
                    f"{neigh} interface v6only remote-as external",
                ]
            else:
                lines += [
                    f"{neigh} description '{other_peer.hostname}'",
                    f"{neigh} remote-as external",
                    f"{neigh} update-source {management.ip}",
                    f"{neigh} peer-group fabric",
                ]
            if other_peer.can_bfd:
                lines.append(f"{neigh} bfd")
            if self.ctx.site_import_rules.get(other_peer.site):
                import_map = f"IMPORT-{other_peer.site.upper()}"
                lines += [
                    f"{neigh} address-family ipv4-unicast route-map"
                    f" import {import_map}",
                    f"{neigh} address-family ipv6-unicast route-map"
                    f" import {import_map}",
                ]
        return lines

    def linux(self) -> List[str]:
        # One wg .conf + one interfaces.d stanza per link; the BGP
        # sessions live in bird's drop-in (BirdFabric).
        lines = []
        for link in self.ctx.vpn_links:
            name = f"wg{link.link_id}"
            our_side, other_peer = link_sides(link, self.host)

            wg_conf = [
                f"# {name} - {link.side_a.hostname} -> {link.side_b.hostname}"
                " (barf-managed; deploy overwrites)",
                "[Interface]",
                f"ListenPort = {link.link_id}",
                f"PrivateKey = {link.wg_privkey(our_side)}",
                "",
                "[Peer]",
                f"PublicKey = {link.wg_pubkey(other_peer)}",
                "# Route anything the fabric learns through the tunnel; BGP decides.",
                "# ::/0 matters even on v4-only networks: unnumbered BGP runs over",
                "# link-local IPv6, which WireGuard drops unless allowed here.",
                "AllowedIPs = 0.0.0.0/0, ::/0",
            ]
            if other_peer.wg_endpoint:
                wg_conf += [
                    f"Endpoint = {other_peer.wg_endpoint}:{link.link_id}",
                    "PersistentKeepalive = 10",
                ]
            lines += barf_file(f"/etc/wireguard/{name}.conf", wg_conf)
            lines.append(f"chmod 600 /etc/wireguard/{name}.conf")
            lines.append("")

            header = (
                f"# {name} - {link.side_a.hostname} -> {link.side_b.hostname}"
                " (barf-managed; deploy overwrites)"
            )
            if link.unnumbered:
                ifupdown = [
                    header,
                    f"auto {name}",
                    "# Unnumbered fabric link (RFC 5549): no global address at"
                    " all, only a",
                    "# deterministic link-local (fe80::<host id>) the BGP session"
                    " runs on.",
                    "# `inet manual` because `inet static` refuses to parse without an",
                    "# address -- but manual applies no mtu and does not bring"
                    " the link up,",
                    "# so both happen explicitly below.",
                    f"iface {name} inet manual",
                    "        pre-up ip link add $IFACE mtu 1420 type wireguard",
                    "        pre-up wg setconf $IFACE /etc/wireguard/$IFACE.conf",
                    "        post-up ip link set $IFACE up",
                    f"        post-up ip addr add fe80::{self.host.host_id}/64"
                    " dev $IFACE",
                    "        post-down ip link del $IFACE",
                ]
            else:
                ifupdown = [
                    header,
                    f"auto {name}",
                    f"iface {name} inet static",
                    f"        address {link.get_ip(our_side)}",
                    "        netmask 255.255.255.254",
                    "        mtu 1420",
                    "        pre-up ip link add $IFACE type wireguard",
                    "        pre-up wg setconf $IFACE /etc/wireguard/$IFACE.conf",
                    "        post-down ip link del $IFACE",
                ]
            lines += barf_file(f"/etc/network/interfaces.d/{name}.conf", ifupdown)
            lines.append("")
        return lines


class AnnouncedNetworks(ConfigBlock):
    """The genprog-networks address-list backing BGP origination.

    Transit ASes are reflected into the fabric; their connected link
    nets are announced (and tagged) like our own networks so every
    fabric session, adopted or new, advertises identically.
    """

    def mikrotik(self) -> List[str]:
        return [
            *(
                f"/ip/firewall/address-list add list=genprog-networks"
                f' address={network} comment="barf: announced to the fabric"'
                for network in announced_networks(self.host)
            ),
            "",
        ]


class SiteWeighting(ConfigBlock):
    """Geographic path weighting filters (origin-site tag out, pref in).

    Tags carry the fabric-wide community_asn (RFC 8195 Global
    Administrator), never the per-host ASN -- see barf/util/sites.py
    for the distance math; only the ready-made integers render here.
    With transit modeled the export chain ends in an explicit reject
    (this host does not re-export fabric routes between fabric peers);
    without transit it stays accept-all like the rest of the fleet.

    The section is gated on origin_site but its trailing separator is
    not (the template's blank line renders unconditionally), so
    applies() stays True and an unweighted host emits just the blank.
    """

    def mikrotik(self) -> List[str]:
        if not self.ctx.origin_site:
            return [""]

        asn = self.ctx.community_asn
        transit = transit_of(self.host)
        lines = []
        for network in announced_networks(self.host):
            lines.append(
                "/routing/filter/rule add chain=genprog-out"
                ' comment="barf: origin-site tag"'
                f' rule="if (dst == {network}) {{ set bgp-large-communities'
                f' {asn}:{SITE_ORIGIN_FUNC}:{self.ctx.origin_site.id}; accept }}"'
            )
        for t in transit:
            lines.append(
                "/routing/filter/rule add chain=genprog-out"
                f' comment="barf: reflect {t["name"]} (AS {t["remote_as"]}) into'
                ' the fabric"'
                f' rule="if (dst-len != 0 && bgp-input-remote-as =='
                f' {t["remote_as"]}) {{ accept }}"'
            )
        if transit:
            lines.append(
                "/routing/filter/rule add chain=genprog-out"
                ' comment="barf: no other re-export" rule="reject"'
            )
        else:
            lines.append('/routing/filter/rule add chain=genprog-out rule="accept"')
        for site_name, rules in self.ctx.site_import_rules.items():
            for rule in rules:
                lines.append(
                    f"/routing/filter/rule add chain=genprog-in-{site_name}"
                    f' comment="barf: origin site {rule.site_id}"'
                    f' rule="if (bgp-large-communities includes'
                    f" {asn}:{SITE_ORIGIN_FUNC}:{rule.site_id})"
                    f' {{ set bgp-local-pref {rule.local_pref}; accept }}"'
                )
            lines.append(
                f"/routing/filter/rule add chain=genprog-in-{site_name}"
                ' comment="barf: untagged fallthrough" rule="accept"'
            )
        return [*lines, ""]

    def vyos(self) -> List[str]:
        # `add` keyword and the `match large-community
        # large-community-list` node verified against a live VyOS 1.4
        # API commit (2026-07-05); the shorter forms 400.
        asn = self.ctx.community_asn
        lines = []
        if self.ctx.origin_site:
            lines += [
                "set policy route-map TAG-SITE-ORIGIN rule 10 action permit",
                "set policy route-map TAG-SITE-ORIGIN rule 10 set large-community"
                f" add '{asn}:{SITE_ORIGIN_FUNC}:{self.ctx.origin_site.id}'",
            ]
        if self.ctx.site_import_rules:
            sites = self.ctx.global_meta.get("sites", {})
            for site in sorted(sites.values(), key=lambda s: s.id):
                origin_list = f"set policy large-community-list SITE-ORIGIN-{site.id}"
                lines += [
                    f"{origin_list} rule 10 action permit",
                    f"{origin_list} rule 10 regex"
                    f" '^{asn}:{SITE_ORIGIN_FUNC}:{site.id}$'",
                ]
            for site_name, rules in self.ctx.site_import_rules.items():
                import_map = f"set policy route-map IMPORT-{site_name.upper()}"
                for index, rule in enumerate(rules, start=1):
                    lines += [
                        f"{import_map} rule {index * 10} action permit",
                        f"{import_map} rule {index * 10} match large-community"
                        f" large-community-list SITE-ORIGIN-{rule.site_id}",
                        f"{import_map} rule {index * 10} set local-preference"
                        f" {rule.local_pref}",
                    ]
                # Trailing catch-all: untagged routes fall through
                # unweighted.
                lines.append(f"{import_map} rule {(len(rules) + 1) * 10} action permit")
        return [*lines, ""]


class FabricBGP(ConfigBlock):
    """The fabric BGP template and one connection per mesh link.

    Unnumbered connections: local.address is the INTERFACE, no
    remote.address (the peer's link-local is discovered via ND), and
    afi=ip,ipv6 is mandatory -- the default afi=ip establishes but
    exchanges zero prefixes (live-learned). IPv4 NLRIs then ride IPv6
    next-hops via the extended-nexthop capability.
    """

    def mikrotik(self) -> List[str]:
        template = (
            f"/routing/bgp/template add name=genprog-fabric as={self.host.asn}"
            " hold-time=30s keepalive-time=10s"
        )
        if announced_networks(self.host):
            template += " output.network=genprog-networks"
        lines = [template]

        for link in self.ctx.vpn_links:
            interface_name = f"wg{link.link_id}"
            our_side, other_peer = link_sides(link, self.host)
            weighted = self.ctx.site_import_rules.get(other_peer.site)
            instance = (
                f" instance={self.host.bgp['instance']}"
                if self.host.bgp.get("instance")
                else ""
            )
            policy = (
                f" input.filter=genprog-in-{other_peer.site}" if weighted else ""
            ) + (" output.filter-chain=genprog-out" if self.ctx.origin_site else "")

            if link.unnumbered:
                lines.append(
                    f"/routing/bgp/connection add name={other_peer.hostname}"
                    f" templates=genprog-fabric{instance}"
                    f" local.address={interface_name} local.role=ebgp"
                    f" remote.as={other_peer.asn} afi=ip,ipv6{policy}"
                    f' comment="barf: {other_peer.hostname}"'
                )
            else:
                lines.append(
                    f"/routing/bgp/connection add name={other_peer.hostname}"
                    f" templates=genprog-fabric{instance}"
                    f" remote.address={link.get_ip(other_peer)}"
                    f" remote.as={other_peer.asn}"
                    f" local.address={link.get_ip(our_side)} local.role=ebgp{policy}"
                    f' comment="barf: {other_peer.hostname}"'
                )
        return [*lines, ""]

    def vyos(self) -> List[str]:
        # One line per network: the route-map suffix rides the same
        # config node, so a separate bare `network X` line would read
        # as a forever-pending diff against the device.
        tag_suffix = " route-map TAG-SITE-ORIGIN" if self.ctx.origin_site else ""
        lines = [
            f"set protocols bgp address-family ipv4-unicast network"
            f" {network}{tag_suffix}"
            for network in self.host.networks
        ]
        management = self.host.management_address
        if management and ":" in management.with_prefixlen:
            lines.append(
                "set protocols bgp address-family ipv6-unicast network"
                f" {management}{tag_suffix}"
            )
        elif management:
            lines.append(
                "set protocols bgp address-family ipv4-unicast network"
                f" {management}{tag_suffix}"
            )
        return [
            *lines,
            "",
            f"set protocols bgp system-as '{self.host.asn}'",
            "set protocols bgp peer-group fabric address-family ipv4-unicast",
            "set protocols bgp peer-group fabric address-family ipv6-unicast",
            "set protocols bgp peer-group fabric capability extended-nexthop",
            # Numbered fabric peers (mikrotik) drop routes from the
            # v6-only sea1 spine without this: it lets FRR set a
            # resolvable IPv4 next-hop instead of an unreachable one.
            # Verified 2026-07-05 to fix sea420<->sea1 receiving 0
            # prefixes.
            "set protocols bgp peer-group fabric disable-connected-check",
            "set protocols bgp timers keepalive 10",
            "set protocols bgp timers holdtime 30",
        ]


def _bird_reject_blacklisted(check_function: str) -> str:
    """The blacklist-reject statement referencing a 00-local function."""
    return (
        f'if {check_function}() then reject "REJECTED ", net, " from ",'
        ' proto, " blacklisted";'
    )


def _bird_channels(import_expr: str) -> List[str]:
    """Both address-family channels of a fabric BGP protocol.

    The import expression is the only thing that varies between the
    shared template and the per-peer site-weighted overrides.
    """
    return [
        "\tipv4 {",
        f"\t\timport {import_expr};",
        "\t\texport filter genprog_export;",
        "\t\tnext hop self;",
        "\t\textended next hop on;",
        "\t};",
        "\tipv6 {",
        f"\t\timport {import_expr};",
        "\t\texport filter genprog_export;",
        "\t\tnext hop self;",
        "\t};",
    ]


class BirdFabric(ConfigBlock):
    """conf.d/genprog.conf: the barf-managed bird fabric + weighting.

    Unnumbered links (RFC 5549): one IPv6 link-local session per
    interface carries BOTH address families; IPv4 routes ride IPv6
    next-hops via the extended-nexthop capability. The radv beacon
    exists because FRR's `neighbor <if> interface v6only` learns its
    peer from received RAs; without it the VyOS side idles forever.
    """

    def linux(self) -> List[str]:
        host = self.host
        ctx = self.ctx
        import_filter = host.bird.get("import_filter")
        import_check_function = host.bird.get("import_check_function")
        reject_blacklisted = (
            _bird_reject_blacklisted(import_check_function)
            if import_check_function
            else None
        )

        content = [
            f"# /etc/bird/conf.d/genprog.conf - barf-managed fabric for"
            f" {host.hostname}.",
            f"# DO NOT HAND-EDIT: `barf config deploy {host.hostname}` overwrites it.",
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
            f"define GENPROG_ASN = {host.asn};",
            "",
            "",
            # Site-origin tags carry the fabric-wide community_asn
            # (RFC 8195 Global Administrator), never the per-host ASN:
            # bird's set grammar cannot wildcard the first field of a
            # large-community set, so importers must be able to match
            # the exact triplet.
            "filter genprog_export {",
            "\tif source = RTS_STATIC then {",
        ]
        if ctx.origin_site:
            content.append(
                f"\t\tbgp_large_community.add(({ctx.community_asn},"
                f" {SITE_ORIGIN_FUNC}, {ctx.origin_site.id}));"
            )
        content += [
            "\t\taccept;",
            "\t}",
            "\tif source = RTS_BGP then accept;",
            "\treject;",
            "}",
        ]

        for site_name, rules in ctx.site_import_rules.items():
            content += ["", f"filter genprog_import_{site_name} {{"]
            if reject_blacklisted:
                content.append(f"\t{reject_blacklisted}")
            for rule in rules:
                content += [
                    f"\tif bgp_large_community ~ [({ctx.community_asn},"
                    f" {SITE_ORIGIN_FUNC}, {rule.site_id})] then {{",
                    f"\t\tbgp_local_pref = {rule.local_pref};",
                    "\t\taccept;",
                    "\t}",
                ]
            content += ["\taccept;", "}"]

        nets_v4 = [net for net in host.networks if ":" not in net]
        nets_v6 = [net for net in host.networks if ":" in net]
        if nets_v4:
            content += [
                "",
                "protocol static genprog_originate {",
                "\tipv4;",
                *(f"\troute {net} reject;" for net in nets_v4),
                "}",
            ]
        if nets_v6:
            content += [
                "",
                "protocol static genprog_originate6 {",
                "\tipv6;",
                *(f"\troute {net} reject;" for net in nets_v6),
                "}",
            ]

        if import_filter:
            default_import = f"filter {import_filter}"
        elif reject_blacklisted:
            default_import = (
                "filter {\n\t\t" + reject_blacklisted + "\n\t\taccept;\n\t}"
            )
        else:
            default_import = "all"

        def channels(import_expr: str) -> List[str]:
            # A multi-line import expression (the inline blacklist
            # filter) rides inside the channel lines, reproducing the
            # template macro's odd-but-committed indentation.
            return "\n".join(_bird_channels(import_expr)).split("\n")

        content += [
            "",
            "template bgp genprog_fabric {",
            "\tlocal as GENPROG_ASN;",
            "\thold time 30;",
            "\tkeepalive time 10;",
            *channels(default_import),
            "}",
        ]

        for link in ctx.vpn_links:
            our_side, other_peer = link_sides(link, host)
            pname = other_peer.hostname.replace("-", "_")
            peer_rules_key = (
                other_peer.site if other_peer.site in ctx.site_import_rules else None
            )
            content += ["", f"protocol bgp {pname} from genprog_fabric {{"]
            if link.unnumbered:
                content += [
                    "\t# Passive: the FRR side dials our link-local once it"
                    " sees an RA.",
                    f'\tinterface "wg{link.link_id}";',
                    f"\tneighbor range fe80::/10 as {other_peer.asn};",
                    f'\tdynamic name "{pname}_";',
                ]
            else:
                content += [
                    f"\tsource address {link.get_ip(our_side)};",
                    f"\tneighbor {link.get_ip(other_peer)} as {other_peer.asn};",
                ]
            if peer_rules_key:
                content += [
                    f"\t# Geographic weighting: this neighbor is in site"
                    f" {peer_rules_key}.",
                    *channels(f"filter genprog_import_{peer_rules_key}"),
                ]
            content.append("}")

        unnumbered = [link for link in ctx.vpn_links if link.unnumbered]
        if unnumbered:
            interfaces = ", ".join(f'"wg{link.link_id}"' for link in unnumbered)
            content += [
                "",
                "protocol radv {",
                f"\tinterface {interfaces} {{",
                "\t\tmax ra interval 15;",
                "\t\tdefault lifetime 0;",
                "\t};",
                "}",
            ]

        content += [
            "",
            "protocol bfd {",
            '\tinterface "wg*" {',
            "\t\tmin rx interval 150 ms;",
            "\t\tmin tx interval 150 ms;",
            "\t\tidle tx interval 1500 ms;",
            "\t};",
            "}",
        ]

        return [
            *barf_file("/etc/bird/conf.d/genprog.conf", content),
            "",
            "birdc configure",
        ]


class TransitBGP(ConfigBlock):
    """Transit BGP sessions barf owns (the retired hand `export` chain).

    genprog-out-transit reproduces that chain's toward-transit rules
    verbatim from the model (the toward-fabric direction is handled by
    genprog-out); the session rides genprog-transit, a template shaped
    like the hand `default` (an AS, no output.network -- origination
    stays connected-matched).
    """

    def applies(self) -> bool:
        return bool(transit_of(self.host))

    def mikrotik(self) -> List[str]:
        transit = transit_of(self.host)
        lines = []
        for t in transit:
            name, remote_as = t["name"], t["remote_as"]
            lines.extend(
                [
                    "/routing/filter/rule add chain=genprog-out-transit"
                    f' comment="barf: export fabric routes to {name} (AS {remote_as})"'
                    f' rule="if (dst-len != 0 && bgp-output-remote-as == {remote_as}'
                    f' && bgp-input-remote-as != {remote_as}) {{accept}}"',
                    "/routing/filter/rule add chain=genprog-out-transit"
                    f' comment="barf: reflect {name} routes to the fabric"'
                    f' rule="if (dst-len != 0 && bgp-output-remote-as != {remote_as}'
                    f' && bgp-input-remote-as == {remote_as}) {{accept}}"',
                    "/routing/filter/rule add chain=genprog-out-transit"
                    f' comment="barf: export {name} link prefix to the fabric"'
                    f' rule="if (dst-len != 0 && protocol connected && dst in'
                    f" {t['export_supernet']} && bgp-output-remote-as !="
                    f' {remote_as}) {{accept}}"',
                    "/routing/filter/rule add chain=genprog-out-transit"
                    f' comment="barf: export fabric link prefixes to {name}"'
                    f' rule="if (dst-len != 0 && protocol connected && dst in'
                    f" {FABRIC_LINK_NET} && bgp-output-remote-as =="
                    f' {remote_as}) {{accept}}"',
                ]
            )
        internal = " or ".join(f"dst in {net}" for net in self.host.networks)
        lines.append(
            "/routing/filter/rule add chain=genprog-out-transit"
            ' comment="barf: announce internal prefixes, drop other connected"'
            f' rule="if (protocol connected && ({internal})) {{accept}} else'
            ' {reject}"'
        )
        lines.append("")
        lines.append(
            f"/routing/bgp/template add name=genprog-transit as={self.host.asn}"
            " routing-table=main"
        )
        for t in transit:
            instance = (
                f" instance={self.host.bgp['instance']}"
                if self.host.bgp.get("instance")
                else ""
            )
            lines.append(
                f"/routing/bgp/connection add name={t['name']}"
                f" templates=genprog-transit{instance}"
                f" remote.address={t['remote_address']} remote.as={t['remote_as']}"
                " local.role=ebgp output.filter-chain=genprog-out-transit"
                f' comment="barf: {t["name"]} transit"'
            )
        return lines
