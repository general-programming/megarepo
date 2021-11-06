from dataclasses import dataclass
import ipaddress
import hvac
import subprocess
from typing import Optional, Tuple
import jinja2
from jinja2 import Environment, PackageLoader, make_logging_undefined
from magic import logging
import yaml

from networkautomation.vendors import NetworkLink
from networkautomation.vendors.vyos import VyOSHost
from networkautomation.vendors.edgeos import EdgeOSHost
from networkautomation.vendors.linux import LinuxBirdHost

log = logging.getLogger(__name__)
vault = hvac.Client()
jinja_env = Environment(
    loader=PackageLoader("networkautomation"),
    autoescape=False,
    undefined=make_logging_undefined(log, base=jinja2.Undefined),
)


def get_secret(secret: str) -> str:
    """Vault secret fetcher.

    Args:
        secret (str): The secret's path.

    Returns:
        str: The secret's value.
    """
    response = vault.secrets.kv.v2.read_secret_version(
        path=secret,
    )["data"]["data"]
    return response["secret"]


# def generate_peer(interface_name: str, peer: WGPeer, port: int, source_peer: WGPeer):
#     result = []

#     if source_peer.edgeos:
#         peer_name = peer.pubkey(port)
#         result.extend([
#             f"set interfaces wireguard {interface_name} peer {peer_name} allowed-ips 0.0.0.0/0",
#         ])
#     elif source_peer.linux:
#         peer_name = peer.hostname
#         result.extend([
#             "[Peer]",
#             f"PublicKey = {peer.pubkey(port)}",
#             "AllowedIPs = 0.0.0.0/0"
#         ])
#     else:
#         peer_name = peer.hostname
#         result.extend([
#             f"set interfaces wireguard {interface_name} peer {peer_name} public-key '{peer.pubkey(port)}'",
#             f"set interfaces wireguard {interface_name} peer {peer_name} allowed-ips 0.0.0.0/0",
#         ])

#     if peer.address:
#         if source_peer.edgeos:
#             result.append(f"set interfaces wireguard {interface_name} peer {peer_name} endpoint {peer.address}:{port}")
#         elif source_peer.linux:
#             result.append(f"Endpoint = {peer.address}:{port}")
#             result.append("PersistentKeepalive = 10")
#         else:
#             result.append(f"set interfaces wireguard {interface_name} peer {peer_name} address {peer.address}")
#             result.append(f"set interfaces wireguard {interface_name} peer {peer_name} port {port}",)

#         if not source_peer.linux:
#             result.append(f"set interfaces wireguard {interface_name} peer {peer_name} persistent-keepalive 10")

#     return result


# def create_wg_config(interface_name: str, source_peer: WGPeer, address: str, port: int, dest_peer: WGPeer, **kwargs):
#     private_key, _ = get_wg_keys(source_peer.hostname, port)

#     result = []

#     if source_peer.linux:
#         result.extend([
#             "[Interface]",
#             f"Address = {address}/31",
#             f"ListenPort = {port}",
#             f"PrivateKey = {private_key}",
#             "Table = off"
#         ])
#     else:
#         if source_peer.edgeos:
#             interface_name = "wg" + interface_name[-3:].lstrip("wg")

#         result.extend([
#             f"set interfaces wireguard {interface_name} description '{source_peer.hostname} -> {dest_peer.hostname}'",
#             f"set interfaces wireguard {interface_name} address {address}/31",
#             f"set interfaces wireguard {interface_name} private-key '{private_key}'",
#         ])

#         if source_peer.edgeos:
#             result.append(f"set interfaces wireguard {interface_name} listen-port {port}")
#             result.append(f"set interfaces wireguard {interface_name} route-allowed-ips false")
#         else:
#             result.append(f"set interfaces wireguard {interface_name} port {port}")

#     if isinstance(dest_peer, str):
#         dest_peer = WGPeer(hostname=dest_peer)

#     result.extend(generate_peer(
#         interface_name,
#         dest_peer,
#         port,
#         source_peer=source_peer,
#     ))

#     return "\n".join(result)


# def create_bgp_neighbor(peer: WGPeer, peer_ip: str, port: int, edgeos_asn: int = None):
#     # fuck, handle spine + leaf
#     if not peer.asn:
#         raise ValueError(f"Peer '{peer.hostname}' missing ASN.")

#     if edgeos_asn:
#         set_prefix = f"set protocols bgp {edgeos_asn}"
#     else:
#         set_prefix = "set protocols bgp"

#     result = [
#         f"{set_prefix} neighbor {peer_ip} remote-as {peer.asn}",
#         f"{set_prefix} neighbor {peer_ip} update-source wg{port}",
#     ]

#     if "-spine-" in peer.hostname:
#         result.append(f"{set_prefix} neighbor {peer_ip} peer-group spine")
#     else:
#         result.append(f"{set_prefix} neighbor {peer_ip} peer-group leaf")

#     return "\n".join(result)


# def connect_links(link: dict, host: str):
#     # asn = 4206900000 + link["port"]
#     side_a_ip, side_b_ip = list(ipaddress.IPv4Network(link["network"]).hosts())
#     interface_name = f"wg{link['port']}"

#     if link["side_a"].hostname == host:
#         print(create_wg_config(interface_name, link["side_a"], side_a_ip, link["port"], link["side_b"]))
#         print(create_bgp_neighbor(link["side_b"], side_b_ip, link["port"], link["side_a"].asn if link["side_a"].edgeos else None))
#     elif link["side_b"].hostname == host:
#         print(create_wg_config(interface_name, link["side_b"], side_b_ip, link["port"], link["side_a"]))
#         print(create_bgp_neighbor(link["side_a"], side_a_ip, link["port"], link["side_b"].asn if link["side_b"].edgeos else None))

def load_network(filename: str):
    with open(filename, "r") as f:
        network = yaml.safe_load(f)

    # global meta
    global_meta = network["global_meta"]

    # hosts
    hosts = []
    for hostname, meta in network["hosts"].items():
        if meta["type"] == "vyos":
            hostclass = VyOSHost
        elif meta["type"] == "edgeos":
            hostclass = EdgeOSHost
        elif meta["type"] == "linux":
            hostclass = LinuxBirdHost
        else:
            raise ValueError("Invalid host type " + meta["type"])

        hosts.append(hostclass(
            hostname=hostname,
            address=meta.get("address", None),
            asn=meta["asn"]
        ))

    links = []
    for link_id, link in network["links"].items():
        side_a = next(host for host in hosts if host.hostname == link["side_a"])
        side_b = next(host for host in hosts if host.hostname == link["side_b"])

        links.append(NetworkLink(
            link_id=link_id,
            side_a=side_a,
            side_b=side_b,
            network=link["network"]
        ))

    return hosts, links, global_meta


if __name__ == "__main__":
    devices, links, global_meta = load_network("network.yml")

    secrets = dict(
        password=get_secret("vyos-password"),
    )

    for device in devices:
        template = jinja_env.get_template(f"vpn/{device.devicetype}.j2")
        device_links = [
            link for link in links
            if device == link.side_a or device == link.side_b
        ]
        print(device.hostname)
        print(device.devicetype)
        print(template.render(
            global_meta=global_meta,
            device=device,
            links=device_links,
            secrets=secrets,
        ))
        print()
