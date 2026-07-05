from typing import List, Tuple

import yaml

from barf.model.wireguard import WGNetworkLink
from barf.vendors import VENDOR_MAP, BaseHost


def load_network(filename: str) -> Tuple[List[BaseHost], List[WGNetworkLink], dict]:
    """Load a network.yml file into hosts, links, and global metadata.

    Links are grouped by device; each entry maps an uplink host to a
    port (the link id, WireGuard port, wgNNNNN interface name, and
    Vault key path all in one), either bare or as a dict with options:

        links:
          fmt2-vpn-leaf-1:
            fmt2-vpn-spine-1: 51820
          oracle-vpn-1-1:
            fmt2-vpn-spine-1:
              {port: 51831, network: 172.31.255.20/31,
               secret: fmt2-oracle-vpn-1, ipsec: true}

    The uplink host (the inner key) is side_a, keeping the historical
    spine-first ordering that decides IP assignment on numbered links.
    """
    with open(filename, "r") as f:
        network = yaml.safe_load(f)

    global_meta = network["global_meta"]

    hosts = []
    for hostname, meta in network["hosts"].items():
        if meta["type"] not in VENDOR_MAP:
            raise ValueError("Invalid host type " + meta["type"])

        hostclass = VENDOR_MAP[meta["type"]]
        hosts.append(hostclass.from_meta(hostname=hostname, meta=meta))

    def host_named(name: str, context: str) -> BaseHost:
        match = next((host for host in hosts if host.hostname == name), None)
        if match is None:
            raise ValueError(f"links: unknown host {name!r} (in {context})")
        return match

    links = []
    seen_ports: dict = {}
    for hostname, uplinks in (network.get("links") or {}).items():
        side_b = host_named(hostname, "links")
        for uplink_name, spec in uplinks.items():
            if isinstance(spec, int):
                spec = {"port": spec}
            port = spec["port"]

            if port in seen_ports:
                raise ValueError(
                    f"links: port {port} used by both {seen_ports[port]} and {hostname}"
                )
            seen_ports[port] = hostname

            links.append(
                WGNetworkLink(
                    link_id=port,
                    side_a=host_named(uplink_name, hostname),
                    side_b=side_b,
                    network=spec.get("network", None),
                    secret=spec.get("secret", None),
                    ipsec=spec.get("ipsec", False),
                )
            )

    return hosts, links, global_meta
