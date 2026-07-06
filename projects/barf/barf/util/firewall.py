"""Vendor-neutral firewall model.

A single ``firewall:`` block in network.yml describes address and
interface groups in vendor-neutral terms; each vendor template
translates them to its own dialect:

* address group  -> RouterOS ``/ip[v6]/firewall/address-list``,
                    VyOS ``firewall group [ipv6-]network-group``
* interface group -> RouterOS ``/interface/list`` (+ members),
                    VyOS ``firewall group interface-group``

Address members carry their own family; a group holding both v4 and v6
prefixes is split at render time (two RouterOS lists / two VyOS group
types sharing the group name). RouterOS interface lists may nest via
``include``; VyOS interface-groups are flat, so ``resolved_interfaces``
flattens the include graph for the VyOS renderer.

Only groups are modelled here — filter rules are a later slice.
"""

import ipaddress
from dataclasses import dataclass, field
from typing import Dict, List, Optional


@dataclass
class FirewallAddressMember:
    """One address (with optional comment) in an address group."""

    address: str
    comment: Optional[str] = None

    @property
    def version(self) -> int:
        return ipaddress.ip_network(self.address, strict=False).version


@dataclass
class FirewallAddressGroup:
    """A named set of addresses (v4 and/or v6)."""

    name: str
    members: List[FirewallAddressMember] = field(default_factory=list)

    def members_v(self, version: int) -> List[FirewallAddressMember]:
        """The members belonging to IP ``version`` (4 or 6)."""
        return [m for m in self.members if m.version == version]


@dataclass
class FirewallInterfaceGroup:
    """A named set of interfaces, optionally including other groups.

    ``include`` names other interface groups whose members are folded
    in (RouterOS interface-list nesting). VyOS has no nesting, so the
    renderer flattens via ``FirewallGroups.resolved_interfaces``.
    """

    name: str
    interfaces: List[str] = field(default_factory=list)
    include: List[str] = field(default_factory=list)


@dataclass
class FirewallGroups:
    address: List[FirewallAddressGroup] = field(default_factory=list)
    interface: List[FirewallInterfaceGroup] = field(default_factory=list)

    def __bool__(self) -> bool:
        return bool(self.address or self.interface)

    @property
    def address_names(self) -> List[str]:
        return [g.name for g in self.address]

    @property
    def interface_names(self) -> List[str]:
        return [g.name for g in self.interface]

    def resolved_interfaces(self, name: str) -> List[str]:
        """Every interface in group ``name``, resolving ``include``.

        Interfaces are returned in first-seen order and de-duplicated;
        include cycles terminate (each group is visited once). An
        include of an unknown group contributes nothing.
        """
        by_name = {g.name: g for g in self.interface}
        seen_groups: set = set()
        interfaces: List[str] = []
        seen_ifaces: set = set()
        stack = [name]
        while stack:
            current = stack.pop(0)
            group = by_name.get(current)
            if group is None or group.name in seen_groups:
                continue
            seen_groups.add(group.name)
            for iface in group.interfaces:
                if iface not in seen_ifaces:
                    seen_ifaces.add(iface)
                    interfaces.append(iface)
            stack.extend(group.include)
        return interfaces


def _parse_address_members(members: list) -> List[FirewallAddressMember]:
    parsed = []
    for member in members:
        if isinstance(member, dict):
            parsed.append(
                FirewallAddressMember(
                    address=member["address"], comment=member.get("comment")
                )
            )
        else:
            parsed.append(FirewallAddressMember(address=member))
    return parsed


def parse_firewall(firewall_meta: dict) -> FirewallGroups:
    """Parse a network.yml ``firewall:`` block into the typed model.

    Group order follows YAML order; membership within a group is a set
    on every vendor, so order there is cosmetic.
    """
    groups: Dict = (firewall_meta or {}).get("groups", {}) or {}

    address = [
        FirewallAddressGroup(name=name, members=_parse_address_members(members or []))
        for name, members in (groups.get("address") or {}).items()
    ]

    interface = []
    for name, spec in (groups.get("interface") or {}).items():
        if isinstance(spec, dict):
            interface.append(
                FirewallInterfaceGroup(
                    name=name,
                    interfaces=list(spec.get("interfaces", [])),
                    include=list(spec.get("include", [])),
                )
            )
        else:
            interface.append(
                FirewallInterfaceGroup(name=name, interfaces=list(spec or []))
            )

    return FirewallGroups(address=address, interface=interface)
