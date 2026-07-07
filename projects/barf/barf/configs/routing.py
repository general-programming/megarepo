"""IGP and static routing blocks (typed OSPF model, static routes).

Byte-identical to the template stanzas they replace (tests/golden/).
"""

from typing import List

from barf.configs.base import ConfigBlock


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
