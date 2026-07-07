"""IGP and static routing blocks (typed OSPF model, static routes).

Byte-identical to the template stanzas they replace (tests/golden/).
"""

from typing import List

from barf.configs.base import ConfigBlock
from barf.configs.lines import barf_file


class BirdBase(ConfigBlock):
    """bird.conf plumbing + the optional 00-local.conf drop-in.

    bird.conf owns only logging, router id, and the device/kernel
    protocols; policy and the fabric live in conf.d drop-ins included
    in glob order (00-local sorts first so its definitions parse
    before genprog.conf; other drop-ins are human-owned).
    """

    def _router_id(self) -> str:
        router_id = self.host.bird.get("router_id")
        if router_id:
            return str(router_id)
        for link in self.ctx.vpn_links:
            if link.unnumbered:
                continue
            our_side = link.side_a if link.side_a == self.host else link.side_b
            return str(link.get_ip(our_side))
        return "192.0.2.1"

    def linux(self) -> List[str]:
        host = self.host
        content = [
            f"# /etc/bird/bird.conf - barf-managed bird2 base for {host.hostname}.",
            f"# DO NOT HAND-EDIT: `barf config deploy {host.hostname}` overwrites it.",
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
            f"router id {self._router_id()};",
            "",
            "protocol device {",
            "\tscan time 10;",
            "}",
            "",
            "protocol kernel {",
        ]
        if host.bird.get("merge_paths"):
            content.append("\tmerge paths on;")
        content += [
            "\tipv4 {",
            "\t\texport filter {",
            "\t\t\t# Static routes are genprog_originate's announce anchors",
            "\t\t\t# (reject routes); installing them would blackhole the",
            "\t\t\t# very prefixes this host serves.",
            "\t\t\tif source = RTS_STATIC then reject;",
        ]
        if host.bird.get("krt_prefsrc"):
            content += [
                "\t\t\t# Pin the source address of fabric-learned routes so",
                "\t\t\t# host-originated traffic uses our announced identity",
                "\t\t\t# (unnumbered links leave nothing else to pick from).",
                f"\t\t\tkrt_prefsrc = {host.bird['krt_prefsrc']};",
            ]
        content += [
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
            'include "/etc/bird/conf.d/*.conf";',
        ]

        lines = [*barf_file("/etc/bird/bird.conf", content), ""]
        local_conf = host.bird.get("local_conf")
        if local_conf:
            # Host-specific human bird config, tracked in network.yml
            # (bird: local_conf). 00- so its definitions (e.g. the
            # import filter genprog references) parse before
            # genprog.conf.
            lines += barf_file(
                "/etc/bird/conf.d/00-local.conf",
                local_conf.rstrip("\n").split("\n"),
            )
        lines.append("")
        return lines


class Ospf(ConfigBlock):
    """The typed OSPF model (networks, mtu-ignore interfaces, redistribute)."""

    def vyos(self) -> List[str]:
        ospf = self.host.ospf
        lines = [
            f"set protocols ospf area {net.area} network {net.network}"
            for net in ospf.networks
        ]
        for iface in ospf.interfaces:
            if iface.mtu_ignore:
                lines.append(f"set protocols ospf interface {iface.name} mtu-ignore")
        for redist in ospf.redistribute:
            line = f"set protocols ospf redistribute {redist.protocol}"
            if redist.metric_type:
                line += f" metric-type {redist.metric_type}"
            lines.append(line)
        return lines


class StaticRoutes(ConfigBlock):
    """Declarative static routes: [{network, interface | next-hop}]."""

    def vyos(self) -> List[str]:
        lines = []
        for route in self.host.static_routes:
            if route.get("interface"):
                lines.append(
                    f"set protocols static route {route['network']}"
                    f" interface {route['interface']}"
                )
            if route.get("next-hop"):
                lines.append(
                    f"set protocols static route {route['network']}"
                    f" next-hop {route['next-hop']}"
                )
        return [*lines, ""]
