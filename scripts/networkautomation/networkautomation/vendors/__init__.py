import ipaddress
import subprocess
from dataclasses import dataclass
from functools import cache
from typing import List, Optional, Tuple

import hvac

vault = hvac.Client()


@dataclass
class HostInterface:
    name: str
    address: str
    netmask: str
    dhcp: bool = False
    vlan: Optional[int] = None


class BaseHost:
    DEVICETYPE = "base"

    def __init__(self, hostname: str, address: str = None, asn: int = None, interfaces: List[HostInterface] = None):
        self.address = address
        self.hostname = hostname
        self.asn = asn
        self.snmp_location = "placeholder snmp location"
        self.interfaces = interfaces or []

    @property
    def devicetype(self):
        return self.DEVICETYPE

    @cache
    def pubkey(self, port: int):
        _, pubkey = get_wg_keys(self.hostname, port)

        return pubkey

    @cache
    def privkey(self, port: int):
        privkey, _ = get_wg_keys(self.hostname, port)

        return privkey

    @property
    def is_spine(self):
        return "-spine-" in self.hostname

@dataclass
class NetworkLink:
    link_id: int

    side_a: BaseHost
    side_b: BaseHost
    network: str

    def get_ip(self, host: BaseHost):
        side_a_ip, side_b_ip = list(ipaddress.IPv4Network(self.network).hosts())

        if host == self.side_a:
            return side_a_ip
        else:
            return side_b_ip


def generate_wireguard_keys() -> Tuple[str, str]:
    """Generate a WireGuard private & public key.

    Requires that the 'wg' command is available on PATH

    Returns:
        (str, str): (private_key, public_key)
    """
    privkey = subprocess.check_output("wg genkey", shell=True).decode("utf-8").strip()
    pubkey = subprocess.check_output(f"echo '{privkey}' | wg pubkey", shell=True).decode("utf-8").strip()

    return (privkey, pubkey)


def get_wg_keys(host: str, port: int, generate_keys: bool = False) -> Tuple[str, str]:
    """Get the WireGuard private, public keys for a host.

    Args:
        host (str): The host to get the WireGuard keypair for.

    Returns:
        Tuple[str, str]: Tuple of (private_key, public_key).
    """
    secret_path = f"wg-{port}-{host}"
    try:
        response = vault.secrets.kv.v2.read_secret_version(
            mount_point="cluster-secrets",
            path=secret_path,
        )["data"]["data"]
        private_key = response["private_key"]
        public_key = response["public_key"]
    except hvac.exceptions.InvalidPath:
        if not generate_keys:
            raise ValueError(f"Secret '{secret_path}' does not exist.")
        private_key, public_key = generate_wireguard_keys()
        vault.secrets.kv.v2.create_or_update_secret(
            mount_point="cluster-secrets",
            path=secret_path,
            secret={
                "public_key": public_key,
                "private_key": private_key,
            }
        )

    return (private_key, public_key)
