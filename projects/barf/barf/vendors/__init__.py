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
    description: Optional[str] = ""
    address: Optional[str] = None
    netmask: Optional[str] = None
    dhcp: bool = False
    vlan: Optional[int] = None
    mtu: Optional[int] = None


class BaseHost:
    DEVICETYPE = "base"
    TEMPLATABLE = True

    def __init__(
        self,
        hostname: str,
        address: str = None,
        asn: int = None,
        interfaces: List[HostInterface] = None,
        nameservers: List[str] = None,
        extra_config: Optional[str] = None,
        snmp_location: Optional[str] = None,
        networks: List[str] = None,
        cloud_init: bool = False,
        **kwargs,
    ):
        self.address = address
        self.hostname = hostname
        self.asn = asn
        self.snmp_location = snmp_location
        self.interfaces = interfaces or []
        self.nameservers = nameservers or []
        self.networks = networks or []
        self.extra_config = extra_config or []
        self.cloud_init = cloud_init

    @property
    def devicetype(self):
        return self.DEVICETYPE

    @staticmethod
    @property
    def can_bfd():
        return False

    @property
    def is_templatable(self):
        return self.TEMPLATABLE

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

    @classmethod
    def from_meta(cls, hostname: str, meta: dict):
        interfaces = []
        for interface in meta.get("interfaces", []):
            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    description=interface.get("description"),
                    address=interface.get("address"),
                    netmask=interface.get("netmask"),
                    dhcp=interface.get("dhcp", False),
                    vlan=interface.get("vlan"),
                    mtu=interface.get("mtu", None),
                )
            )

        return cls(
            hostname=hostname,
            address=meta.get("address", None),
            asn=meta["asn"],
            nameservers=meta.get("nameservers", []),
            extra_config=meta.get("extra_config", []),
            networks=meta.get("networks", []),
            snmp_location=meta.get("location", None),
            interfaces=interfaces,
            cloud_init=meta.get("cloud_init", False),
        )


@dataclass
class NetworkLink:
    link_id: int

    side_a: BaseHost
    side_b: BaseHost
    network: str
    secret: Optional[str] = None
    ipsec: bool = False

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
    pubkey = (
        subprocess.check_output(f"echo '{privkey}' | wg pubkey", shell=True)
        .decode("utf-8")
        .strip()
    )

    return (privkey, pubkey)


def get_wg_keys(host: str, port: int, generate_keys: bool = True) -> Tuple[str, str]:
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
            },
        )

    return (private_key, public_key)


from barf.vendors.cisco import CiscoHost
from barf.vendors.edgeos import EdgeOSHost
from barf.vendors.external import ExternalHost
from barf.vendors.linux import LinuxBirdHost
from barf.vendors.vyos import VyOSHost

VENDOR_MAP = {
    "vyos": VyOSHost,
    "edgeos": EdgeOSHost,
    "linux": LinuxBirdHost,
    "external": ExternalHost,
    "cisco": CiscoHost,
}
