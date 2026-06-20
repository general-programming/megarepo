from typing import List, Tuple

import yaml

from barf.model.wireguard import WGNetworkLink
from barf.vendors import VENDOR_MAP, BaseHost


def load_network(filename: str) -> Tuple[List[BaseHost], List[WGNetworkLink], dict]:
    """Load a network.yml file into hosts, links, and global metadata."""
    with open(filename, "r") as f:
        network = yaml.safe_load(f)

    global_meta = network["global_meta"]

    hosts = []
    for hostname, meta in network["hosts"].items():
        if meta["type"] not in VENDOR_MAP:
            raise ValueError("Invalid host type " + meta["type"])

        hostclass = VENDOR_MAP[meta["type"]]
        hosts.append(hostclass.from_meta(hostname=hostname, meta=meta))

    links = []
    for link_id, link in network.get("links", {}).items():
        side_a = next(host for host in hosts if host.hostname == link["side_a"])
        side_b = next(host for host in hosts if host.hostname == link["side_b"])

        links.append(
            WGNetworkLink(
                link_id=link_id,
                side_a=side_a,
                side_b=side_b,
                network=link.get("network", None),
                secret=link.get("secret", None),
                ipsec=link.get("ipsec", False),
            )
        )

    return hosts, links, global_meta
