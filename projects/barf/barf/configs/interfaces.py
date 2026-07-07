"""Interface blocks: the vendor-neutral interface models per dialect.

Byte-identical to the template stanzas they replace (tests/golden/).
On VyOS every modeled interface renders through one loop
(VyosInterfaces); on RouterOS bridges and static WireGuard tunnels are
their own sections (Bridges, StaticWireGuard).
"""

from typing import TYPE_CHECKING, List, cast

from barf.configs.base import ConfigBlock
from barf.configs.lines import barf_file, ros_kv, ros_line

if TYPE_CHECKING:
    from barf.vendors.vyos import VyOSHost


class LinuxDummies(ConfigBlock):
    """dum* interfaces as owned interfaces.d files (the VyOS dum0 pattern).

    Anchor addresses on a dummy interface: the host's stable fabric
    identity, independent of any physical link.
    """

    def linux(self) -> List[str]:
        lines = []
        for iface in self.host.interfaces:
            if not iface.name.startswith("dum"):
                continue
            content = [
                f"# {iface.name} - {iface.description or 'loopback'}"
                " (barf-managed; deploy overwrites)",
                "# Anchor addresses on a dummy interface (the VyOS dum0 pattern): the",
                "# host's stable fabric identity, independent of any physical link.",
                "# `inet manual` + explicit address adds handles any v4/v6 mix.",
                f"auto {iface.name}",
                f"iface {iface.name} inet manual",
                "        pre-up ip link add $IFACE type dummy",
                "        post-up ip link set $IFACE up",
                *(
                    f"        post-up ip addr add {address} dev $IFACE"
                    for address in iface.addresses
                ),
                "        post-down ip link del $IFACE",
            ]
            lines += barf_file(f"/etc/network/interfaces.d/{iface.name}.conf", content)
            lines.append("")
        return lines


class VyosInterfaces(ConfigBlock):
    """Modeled interfaces in VyOS dialect (the vyatta interface loop).

    Bridges and static WireGuard tunnels render here through
    ``interface_prefix`` (on RouterOS the same models are the Bridges /
    StaticWireGuard blocks); each dynamic-addressing mechanism is an
    independent flag.
    """

    def vyos(self) -> List[str]:
        host = cast("VyOSHost", self.host)
        lines = []
        for iface in host.interfaces:
            prefix = host.interface_prefix(iface)
            if iface.dhcp:
                lines.append(f"{prefix} address dhcp")
            if iface.dhcpv6:
                lines.append(f"{prefix} address dhcpv6")
            if iface.ipv6_autoconf:
                lines.append(f"{prefix} ipv6 address autoconf")
            for addr in iface.addresses:
                lines.append(f"{prefix} address {addr.with_prefixlen}")
            if iface.mtu:
                lines.append(f"{prefix} mtu {iface.mtu}")
            if iface.description:
                lines.append(f"{prefix} description '{iface.description}'")
            for member in iface.members:
                lines.append(
                    f"set interfaces bridge {iface.name} member interface {member}"
                )
            if iface.is_wireguard and iface.wireguard:
                wg = iface.wireguard
                lines.append(
                    f"{prefix} private-key"
                    f" '{self.host.secret(wg['private_key_secret'])}'"
                )
                lines.append(f"{prefix} port {wg['port']}")
                for peer in wg.get("peers", []):
                    peer_prefix = f"{prefix} peer {peer['name']}"
                    lines.append(f"{peer_prefix} public-key '{peer['public_key']}'")
                    for ip in peer.get("allowed_ips", ["0.0.0.0/0", "::/0"]):
                        lines.append(f"{peer_prefix} allowed-ips '{ip}'")
                    if peer.get("endpoint"):
                        lines.append(f"{peer_prefix} address '{peer['endpoint']}'")
                        lines.append(
                            f"{peer_prefix} port {peer.get('port', wg['port'])}"
                        )
                    if peer.get("keepalive"):
                        # VyOS keepalive is a bare number of seconds.
                        keepalive = str(peer["keepalive"]).replace("s", "")
                        lines.append(f"{peer_prefix} persistent-keepalive {keepalive}")
        return lines


class Bridges(ConfigBlock):
    """LAN/L2 bridges modeled in network.yml (vendor-neutral type=bridge).

    barf owns the bridge by name, its L3 addresses, and its member
    ports, so every member of an owned bridge must be modeled or it
    would be removed. The optional ``ra`` block is a real prefix RA for
    attached hosts, distinct from the fabric links' unnumbered RA.
    """

    def mikrotik(self) -> List[str]:
        lines = []
        for iface in self.host.interfaces:
            if not iface.is_bridge:
                continue
            add = f"/interface/bridge add name={iface.name}"
            if iface.description:
                add += f' comment="barf: {iface.description}"'
            lines.append(add)
            for member in iface.members:
                lines.append(
                    f"/interface/bridge/port add bridge={iface.name} interface={member}"
                )
            for addr in iface.addresses:
                lines.append(
                    f"/ip/address add address={addr.with_prefixlen}"
                    f" interface={iface.name}"
                    f' comment="barf: {iface.name} address"'
                )
            if iface.ra:
                nd = f"/ipv6/nd add interface={iface.name}"
                if iface.ra.get("hop_limit"):
                    nd += f" hop-limit={iface.ra['hop_limit']}"
                nd += (
                    f" advertise-dns={'yes' if iface.ra.get('advertise_dns') else 'no'}"
                )
                lines.append(nd)
        return [*lines, ""]


class StaticWireGuard(ConfigBlock):
    """Static WireGuard tunnels (vendor-neutral ``wireguard`` block).

    External peerings, not the Vault-derived fabric mesh: the peer
    key/endpoint are static and our private key is captured into Vault
    off the device, so the interface adopts byte-identical without a
    rekey.
    """

    def mikrotik(self) -> List[str]:
        lines = []
        for iface in self.host.interfaces:
            if not (iface.is_wireguard and iface.wireguard):
                continue
            wg = iface.wireguard
            lines.append(
                ros_line(
                    "/interface/wireguard",
                    "add",
                    ros_kv("name", iface.name),
                    ros_kv("listen-port", wg["port"]),
                    ros_kv("mtu", iface.mtu or 1420),
                    ros_kv(
                        "private-key",
                        self.host.secret(wg["private_key_secret"]),
                        quote=True,
                    ),
                    ros_kv("comment", f"barf: {iface.name} tunnel", quote=True),
                )
            )
            for peer in wg.get("peers", []):
                line = ros_line(
                    "/interface/wireguard/peers",
                    "add",
                    ros_kv("interface", iface.name),
                    ros_kv("name", peer["name"]),
                    ros_kv("public-key", peer["public_key"], quote=True),
                    ros_kv(
                        "allowed-address",
                        ",".join(peer.get("allowed_ips", ["0.0.0.0/0", "::/0"])),
                    ),
                )
                if peer.get("endpoint"):
                    line += (
                        f" endpoint-address={peer['endpoint']}"
                        f" endpoint-port={peer.get('port', wg['port'])}"
                        f" persistent-keepalive={peer.get('keepalive', '10s')}"
                    )
                lines.append(line)
            for addr in iface.addresses:
                lines.append(
                    f"/ip/address add address={addr.with_prefixlen}"
                    f" interface={iface.name}"
                    f' comment="barf: {iface.name} address"'
                )
        return [*lines, ""]
