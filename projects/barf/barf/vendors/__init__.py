from __future__ import annotations

import functools
import ipaddress
import json
import secrets
from dataclasses import dataclass, field
from functools import cache, lru_cache
from typing import TYPE_CHECKING, Callable, List, Optional

import dns.resolver
import hvac
from barf.model import get_vault
from barf.model.wireguard import get_wg_keys

if TYPE_CHECKING:
    from barf.model.wireguard import WGKeypair


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
    def from_netbox(cls, data: dict) -> Cable:
        """Create a cable from a Netbox cable.

        :param data: A dictionary containing the cable data.
        :type data: dict

        :return: A new cable object.
        :rtype: Cable

        :raises KeyError: If the cable data is missing any devices.
        :raises ValueError: If the cable data is invalid.
        """

        if not data["cable"]:
            raise ValueError("Cable does not exist.")
        cable: List[dict] = data["cable"]

        # find cable_end in cables list that matches "a"
        termination_a, termination_b = None, None

        for termination in cable:
            if termination["cable_end"] == "a":
                termination_a = termination
            elif termination["cable_end"] == "b":
                termination_b = termination

        if not termination_a:
            raise KeyError("Cable termination A not found.")

        if not termination_b:
            raise KeyError("Cable termination B not found.")

        return cls(
            name=data["id"],
            host_a_name=termination_a["_device"]["name"],
            host_b_name=termination_b["_device"]["name"],
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
    # TODO: IPv6 support should be added.
    address: Optional[ipaddress.IPv4Interface] = None
    dhcp: bool = False
    untagged_vlan: Optional[NetworkVLAN] = None
    tagged_vlans: List[NetworkVLAN] = field(default_factory=list)
    mtu: Optional[int] = None
    lag_id: Optional[str] = None
    cable: Optional[Cable] = None
    vrf: Optional[str] = None
    management: Optional[bool] = False

    @property
    def description(self) -> Optional[str]:
        result = self._description or ""

        if self.cable:
            result += f" ({ self.cable })"

        # Remove trailing whitespace due to the cable text.
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
    def is_port_channel(self) -> bool:
        """Alias for is_lag."""
        return self.is_lag

    @property
    def cisco_name(self) -> str:
        """Return the interface name in Cisco format."""
        result = self.name

        # We assume that the interface name is a number here.
        # TODO: For that case, we really should check that the name is a number.
        if self.is_lag:
            result = "Port-Channel" + result

        return result


@dataclass
class OSPFNetwork:
    network: ipaddress.IPv4Network
    area: int

    @classmethod
    def from_dict(cls, data: dict) -> OSPFNetwork:
        return cls(
            network=ipaddress.IPv4Network(data["network"]),
            area=data["area"],
        )

class BaseHost:
    DEVICETYPE = "base"
    TEMPLATABLE = True

    def __init__(
        self,
        hostname: str,
        role: str,
        address: ipaddress.IPv4Interface = None,
        asn: int = None,
        interfaces: List[HostInterface] = None,
        nameservers: List[str] = None,
        extra_config: Optional[str] = None,
        snmp_location: Optional[str] = None,
        networks: List[str] = None,
        cloud_init: bool = False,
        vlan_map: Optional[NetworkVLAN] = None,
        config_context: Optional[dict] = None,
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
        self.config_context = config_context or {}

        if not vlan_map:
            vlan_map = {}
        self.vlan_map = vlan_map

        self.vault = get_vault()

    @property
    def devicetype(self):
        return self.DEVICETYPE

    @staticmethod
    @property
    def can_bfd():
        return False

    @property
    def device_username(self):
        return "admin"

    @property
    def is_templatable(self):
        """Whether the host can be templated."""
        return self.TEMPLATABLE

    @cache
    def wg_keys(self, port: int) -> "WGKeypair":
        """Return the WG keys for this host."""
        return get_wg_keys(self.hostname, port)

    @cache
    def wg_pubkey(self, port: int):
        """Return the WireGuard public key for this host."""
        return self.wg_keys(port).public_key

    @cache
    def wg_privkey(self, port: int):
        """Return the WireGuard private key for this host."""
        return self.wg_keys(port).private_key

    @property
    def enable_password(self):
        """Return the enable password for this host."""
        return self.secret("enable-password", generate_tacacs_key)

    @property
    def admin_password(self):
        """Return the admin password for this host."""
        return self.secret("admin-password", generate_tacacs_key)

    @property
    @cache
    def tacacs_key(self):
        """Return the TACACS+ key for this host."""

        def tacacs_generator(address: str):
            return {
                "address": address,
                "key": generate_tacacs_key(),
            }

        return self.secret(
            self.hostname,
            functools.partial(tacacs_generator, f"{self.address.compressed}/32"),
            secret_path="tacacs-keys",
        )["key"]

    @property
    def is_spine(self):
        """Whether the host is a spine."""
        return "-spine-" in self.hostname

    @property
    @lru_cache
    def vlans(self):
        """Return the VLANs for all interfaces this host."""
        all_vlans = set()
        for interface in self.interfaces:
            if interface.untagged_vlan and interface.untagged_vlan.vid in self.vlan_map:
                all_vlans.add(interface.untagged_vlan)
            for vlan in interface.tagged_vlans:
                if vlan.vid in self.vlan_map:
                    all_vlans.add(vlan)

        return list(all_vlans)

    @property
    @lru_cache
    def default_route(self):
        """Return the default route for this host."""
        if "default-route" not in self.config_context:
            return None

        return ipaddress.IPv4Address(self.config_context.get("default-route"))

    @property
    @lru_cache
    def tacacs_servers(self):
        """Return the TACACS+ servers from Consul."""
        hosts = set()

        resolver = dns.resolver.Resolver()
        query = resolver.query("tacacs.service.fmt2.consul", "A")
        for host in query:
            hosts.add(host)

        return list(hosts)

    @property
    def management_address(self) -> Optional[ipaddress.IPv4Interface]:
        """Return the management address for this host."""
        for interface in self.interfaces:
            if interface.management:
                return interface.address

        return None

    def secret(
        self,
        key: str,
        default_value: Optional[Callable[[], str]] = None,
        secret_path: Optional[str] = None,
    ):
        """Get a host specific secret.

        Args:
            key (str): The key to get.
            default_value (Optional[Callable]): A function that returns a default value if not found.
            secret_path (str): The path to the secret.

        Returns:
            str: The secret value.
        """
        if not secret_path:
            secret_path = f"host-{self.hostname}"

        try:
            response = self.vault.secrets.kv.v2.read_secret_version(
                mount_point="cluster-secrets",
                path=secret_path,
            )["data"]["data"]

            try:
                result = response[key]
            except KeyError:
                if not default_value:
                    raise KeyError(f"Key {key} not found in {secret_path}")

                result = default_value()

                self.vault.secrets.kv.v2.patch(
                    mount_point="cluster-secrets",
                    path=secret_path,
                    secret={key: result},
                )
        except hvac.exceptions.InvalidPath:
            if not default_value:
                raise KeyError(f"Key {key} not found in {secret_path}")

            result = default_value()

            self.vault.secrets.kv.v2.create_or_update_secret(
                mount_point="cluster-secrets",
                path=secret_path,
                secret={key: result},
            )

        return result

    @classmethod
    def from_meta(cls, hostname: str, meta: dict):
        """Create a host from a VPN network.yaml metadata entry."""
        interfaces = []
        for interface in meta.get("interfaces", []):
            address = None

            if "address" in interface:
                address = ipaddress.IPv4Interface(interface.get("address"))

            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    type="VPNLink",
                    _description=interface.get("description"),
                    enabled=interface.get("enabled", True),
                    address=address,
                    dhcp=interface.get("dhcp", False),
                    untagged_vlan=NetworkVLAN(vid=interface.get("vlan")),
                    tagged_vlans=[
                        NetworkVLAN(vid=vlan)
                        for vlan in interface.get("tagged_vlans", [])
                    ],
                    mtu=interface.get("mtu", None),
                    management=interface.get("management", False),
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
        """Create a host from a Netbox metadata entry."""
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

            if "ip_addresses" in interface and interface["ip_addresses"]:
                ip_address = interface["ip_addresses"][0]["address"]

            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    type=interface["type"],
                    mode=interface["mode"],
                    _description=interface.get("description", None),
                    address=ipaddress.IPv4Interface(ip_address) if ip_address else None,
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
            address=ipaddress.IPv4Interface(netbox_meta["primary_ip4"]["address"]),
            interfaces=interfaces,
            vlan_map=parsed_vlans,
            config_context=netbox_meta["config_context"] or {},
        )


def generate_tacacs_key() -> str:
    """Generate a TACACS+ key.

    Returns:
        str: The TACACS+ key.
    """
    return secrets.token_urlsafe(16)


from barf.vendors.arista import EosHost
from barf.vendors.cisco import CiscoHost
from barf.vendors.dell import DNOS6Host, DNOS9Host
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
    "dnos6": DNOS6Host,
    "dnos-6": DNOS6Host,
    "dnos9": DNOS9Host,
    "dnos-9": DNOS9Host,
}
