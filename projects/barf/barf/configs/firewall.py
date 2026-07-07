"""Firewall blocks: vendor-neutral groups and the declarative nat model.

Byte-identical to the template stanzas they replace (tests/golden/).
"""

from typing import List

from barf.configs.base import ConfigBlock


class FirewallGroups(ConfigBlock):
    """Vendor-neutral firewall groups (barf/util/firewall.py).

    Address members split by family into the v4/v6 address-list trees;
    interface lists render their nesting via include=.
    """

    def mikrotik(self) -> List[str]:
        lines = []
        for group in self.host.firewall.address:
            for version, path in ((4, "/ip"), (6, "/ipv6")):
                for member in group.members_v(version):
                    line = (
                        f"{path}/firewall/address-list add list={group.name}"
                        f" address={member.address}"
                    )
                    if member.comment:
                        line += f' comment="{member.comment}"'
                    lines.append(line)
        for group in self.host.firewall.interface:
            add = f"/interface/list add name={group.name}"
            if group.include:
                add += f" include={','.join(group.include)}"
            lines.append(add)
            for iface in group.interfaces:
                lines.append(
                    f"/interface/list/member add list={group.name} interface={iface}"
                )
        return [*lines, ""]

    def vyos(self) -> List[str]:
        # VyOS interface-groups are flat, so include= nesting is
        # resolved here (RouterOS renders it natively).
        lines = []
        for group in self.host.firewall.address:
            for member in group.members_v(4):
                lines.append(
                    f"set firewall group network-group {group.name}"
                    f" network {member.address}"
                )
            for member in group.members_v(6):
                lines.append(
                    f"set firewall group ipv6-network-group {group.name}"
                    f" network {member.address}"
                )
        for group in self.host.firewall.interface:
            for iface in self.host.firewall.resolved_interfaces(group.name):
                lines.append(
                    f"set firewall group interface-group {group.name} interface {iface}"
                )
        return [*lines, ""]


class Nat(ConfigBlock):
    """The declarative nat: block (masquerades + port forwards)."""

    def vyos(self) -> List[str]:
        lines = []
        for rule in self.host.nat_masquerades:
            src = f"set nat source rule {rule.rule}"
            lines.append(f"{src} outbound-interface name {rule.interface}")
            if rule.source:
                lines.append(f"{src} source address {rule.source}")
            if rule.destination:
                lines.append(f"{src} destination address {rule.destination}")
            lines.append(f"{src} translation address masquerade")
        for fwd in self.host.nat_port_forwards:
            dst = f"set nat destination rule {fwd.rule}"
            lines += [
                f"{dst} description '{fwd.description}'",
                f"{dst} inbound-interface name {fwd.interface}",
                f"{dst} protocol {fwd.protocol}",
                f"{dst} destination port {fwd.port}",
                f"{dst} translation address {fwd.to}",
            ]
            if fwd.translation_port:
                lines.append(f"{dst} translation port {fwd.translation_port}")
        return lines
