import ipaddress
import secrets
import subprocess
from dataclasses import dataclass, field
from functools import cache, lru_cache
from typing import List, Optional, Tuple

import dns.resolver
import hvac

vault = hvac.Client()


@dataclass
class Cable:
    """A cable between two hosts.

    Attributes:
        name (str): The name of the cable.
        host_a_name (str): The name of host A.
        host_b_name (str): The name of host B.
    """

    def __len__(self) -> int:
        return len(str(self))

    def __str__(self) -> str:
        return f"{self.host_a_name} -> {self.host_b_name}"

    name: str
    host_a_name: str
    host_b_name: str

    @classmethod
    def from_netbox(cls, data: dict):
        """Create a cable from a Netbox cable.

        Attributes:
            data (dict): A dictionary containing the cable data.

        Returns:
            Cable: The cable.

        Throws:
            KeyError: If the cable data is missing any devices.
            ValueError: If the cable data is invalid.
        """

        if not data["_termination_a_device"] or not data["_termination_b_device"]:
            raise ValueError("Cable does not have both device ends.")

        return cls(
            name=data["id"],
            host_a_name=data["_termination_a_device"]["name"],
            host_b_name=data["_termination_b_device"]["name"],
        )


@dataclass
class NetworkVLAN:
    vid: int
    name: Optional[str] = ""

    def __hash__(self) -> int:
        return self.vid ^ hash(self.name)

    def __bool__(self) -> bool:
        return self.vid is not None

    @classmethod
    def from_netbox(cls, data):
        if not data:
            return None

        return cls(
            vid=data["vid"],
            name=data["name"],
        )


@dataclass
class HostInterface:
    name: str
    type: str
    mode: Optional[str] = None
    _description: Optional[str] = ""
    enabled: bool = True
    address: Optional[str] = None
    netmask: Optional[str] = None
    dhcp: bool = False
    untagged_vlan: Optional[NetworkVLAN] = None
    tagged_vlans: List[NetworkVLAN] = field(default_factory=list)
    mtu: Optional[int] = None
    lag_id: Optional[str] = None
    cable: Optional[Cable] = None
    vrf: Optional[str] = None

    @property
    def description(self) -> str:
        result = self._description or ""

        if self.cable:
            result += f" ({ self.cable })"

        result = result.strip()

        if not result:
            return None

        return result.strip()

    @property
    def is_lag(self) -> bool:
        """Whether the interface is a link aggregation group."""
        return self.type == "LAG"

    @property
    def is_trunk(self) -> bool:
        """Whether the interface is a trunk."""
        return self.mode in ["TAGGED", "TAGGED_ALL"]

    @property
    def is_access(self) -> bool:
        """Whether the interface is an access port."""
        return self.mode == "ACCESS"

    @property
    def is_vlan(self) -> bool:
        """Whether the interface is a VLAN."""
        return self.type == "VIRTUAL"

    @property
    def cisco_name(self) -> str:
        """Return the interface name in Cisco format."""
        result = self.name

        if self.is_lag:
            result = "Port-Channel" + result

        return result


class BaseHost:
    DEVICETYPE = "base"
    TEMPLATABLE = True

    def __init__(
        self,
        hostname: str,
        role: str,
        address: str = None,
        asn: int = None,
        interfaces: List[HostInterface] = None,
        nameservers: List[str] = None,
        extra_config: Optional[str] = None,
        snmp_location: Optional[str] = None,
        networks: List[str] = None,
        cloud_init: bool = False,
        vlan_map: Optional[NetworkVLAN] = None,
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
        self.role = role

        if not vlan_map:
            vlan_map = {}
        self.vlan_map = vlan_map

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
    def wg_pubkey(self, port: int):
        _, pubkey = get_wg_keys(self.hostname, port)

        return pubkey

    @cache
    def wg_privkey(self, port: int):
        privkey, _ = get_wg_keys(self.hostname, port)

        return privkey

    @property
    @cache
    def tacacs_key(self):
        return get_tacacs_key(self.hostname, self.address)

    @property
    def is_spine(self):
        return "-spine-" in self.hostname

    @property
    @lru_cache
    def vlans(self):
        all_vlans = set()
        for interface in self.interfaces:
            if interface.untagged_vlan and interface.untagged_vlan in self.vlan_map:
                all_vlans.add(self.vlan_map[interface.untagged_vlan])
            for vlan in interface.tagged_vlans:
                if vlan in self.vlan_map:
                    all_vlans.add(self.vlan_map[vlan])

        return list(all_vlans)

    @property
    @lru_cache
    def tacacs_servers(self):
        hosts = set()

        resolver = dns.resolver.Resolver()
        query = resolver.query("tacacs.service.fmt2.consul", "A")
        for host in query:
            hosts.add(host)

        return list(hosts)

    @classmethod
    def from_meta(cls, hostname: str, meta: dict):
        interfaces = []
        for interface in meta.get("interfaces", []):
            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    type="VPNLink",
                    _description=interface.get("description"),
                    enabled=interface.get("enabled", True),
                    address=interface.get("address"),
                    netmask=interface.get("netmask"),
                    dhcp=interface.get("dhcp", False),
                    untagged_vlan=NetworkVLAN(vid=interface.get("vlan")),
                    tagged_vlans=[
                        NetworkVLAN(vid=vlan)
                        for vlan in interface.get("tagged_vlans", [])
                    ],
                    mtu=interface.get("mtu", None),
                )
            )

        return cls(
            hostname=hostname,
            role=meta["role"],
            address=meta.get("address", None),
            asn=meta["asn"],
            nameservers=meta.get("nameservers", []),
            extra_config=meta.get("extra_config", []),
            networks=meta.get("networks", []),
            snmp_location=meta.get("location", None),
            interfaces=interfaces,
            cloud_init=meta.get("cloud_init", False),
        )

    @classmethod
    def from_netbox_meta(cls, netbox_meta: dict, netbox_vlans: dict):
        interfaces = []

        for interface in netbox_meta["interfaces"]:
            # get lag id
            lag_id = (interface.get("lag") or {}).get("name", None)

            # get cable
            try:
                cable = Cable.from_netbox(interface.get("cable") or {})
            except (ValueError, KeyError):
                cable = None

            # XXX: This will break if there are multiple IPs for this interface.
            ip_address = None
            netmask = None

            if "ip_addresses" in interface and interface["ip_addresses"]:
                ip_address, netmask = interface["ip_addresses"][0]["address"].split(
                    "/", 1
                )

            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    type=interface["type"],
                    mode=interface["mode"],
                    _description=interface.get("description", None),
                    address=ipaddress.IPv4Address(ip_address) if ip_address else None,
                    netmask=netmask,
                    dhcp=False,
                    untagged_vlan=NetworkVLAN.from_netbox(interface["untagged_vlan"]),
                    tagged_vlans=[
                        NetworkVLAN.from_netbox(vlan)
                        for vlan in interface["tagged_vlans"]
                    ],
                    mtu=interface.get("mtu", None),
                    lag_id=lag_id,
                    cable=cable,
                    vrf=interface.get("vrf", None),
                )
            )

        # handle netbox VLANs
        parsed_vlans = {}
        for vlan in netbox_vlans:
            if vlan["vid"] in netbox_vlans:
                continue

            parsed_vlans[vlan["vid"]] = NetworkVLAN(
                vid=vlan["vid"],
                name=vlan.get("name", None),
            )

        return cls(
            hostname=netbox_meta["name"],
            role="network_devices",
            address=netbox_meta["primary_ip4"]["address"],
            interfaces=interfaces,
            vlan_map=parsed_vlans,
        )


@dataclass
class NetworkLink:
    link_id: int

    side_a: BaseHost
    side_b: BaseHost


@dataclass
class WGNetworkLink(NetworkLink):
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
        port (int): The port to get the WireGuard keypair for.
        generate_keys (bool): Whether to generate a new keypair if one doesn't exist.

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


def generate_tacacs_key() -> str:
    """Generate a TACACS+ key.

    Returns:
        str: The TACACS+ key.
    """
    return secrets.token_urlsafe(16)


def get_tacacs_key(host: str, device_address: str, generate_keys: bool = True) -> str:
    """Get the TACACS+ key for a host.

    Args:
        host (str): The host to get the TACACS+ key for.
        generate_keys (bool): Whether to generate a new key if one does not exist.

    Returns:
        str: The TACACS+ key.
    """
    secret_path = "tacacs-keys"

    try:
        response = vault.secrets.kv.v2.read_secret_version(
            mount_point="cluster-secrets",
            path=secret_path,
        )["data"]["data"]

        try:
            tacacs_key = response[host]["key"]
        except KeyError:
            tacacs_key = generate_tacacs_key()

        vault.secrets.kv.v2.patch(
            mount_point="cluster-secrets",
            path=secret_path,
            secret={
                host: {
                    "address": device_address,
                    "key": tacacs_key,
                }
            },
        )
    except hvac.exceptions.InvalidPath:
        if not generate_keys:
            raise ValueError(f"Secret '{secret_path}' does not exist.")

        tacacs_key = generate_tacacs_key()

        vault.secrets.kv.v2.create_or_update_secret(
            mount_point="cluster-secrets",
            path=secret_path,
            secret={
                host: {
                    "address": device_address,
                    "key": tacacs_key,
                }
            },
        )

    return tacacs_key


from barf.vendors.arista import EosHost
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
    "cisco-ios": CiscoHost,
    "eos": EosHost,
}
