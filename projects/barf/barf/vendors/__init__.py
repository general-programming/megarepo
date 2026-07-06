from __future__ import annotations

import concurrent.futures
import functools
import ipaddress
import logging
import secrets
import socket
from dataclasses import dataclass, field
from functools import cache, lru_cache
from typing import TYPE_CHECKING, Callable, Dict, List, Optional

import dns.resolver
import hvac
import hvac.exceptions

from barf.actions import open_connection
from barf.model import get_vault
from barf.util.images import PROVIDERS

if TYPE_CHECKING:
    from barf.util.images import ImageProvider

log = logging.getLogger(__name__)


# ruff: noqa: E402


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
    # All addresses on the interface, any mix of IPv4/IPv6. `address`
    # and `ip6_address` are kept in sync as the first of each family
    # for templates and callers that want one; __post_init__ merges
    # whichever of the three the constructor was given.
    addresses: List[ipaddress.IPv4Interface | ipaddress.IPv6Interface] = field(
        default_factory=list
    )
    address: Optional[ipaddress.IPv4Interface] = None
    ip6_address: Optional[ipaddress.IPv6Interface] = None
    dhcp: bool = False
    dhcpv6: bool = False
    ipv6_autoconf: bool = False
    untagged_vlan: Optional[NetworkVLAN] = None
    tagged_vlans: List[NetworkVLAN] = field(default_factory=list)
    mtu: Optional[int] = None
    lag_id: Optional[str] = None
    cable: Optional[Cable] = None
    vrf: Optional[str] = None
    management: Optional[bool] = False
    # Member port names for a bridge interface (type == "bridge"); a
    # vendor-neutral list each template renders in its own dialect
    # (VyOS ``member interface``, RouterOS ``/interface/bridge/port``).
    members: List[str] = field(default_factory=list)
    # IPv6 router-advertisement knobs for an interface that serves a LAN
    # (e.g. a host-networking bridge). None means barf renders no RA. A
    # small vendor-neutral dict (hop_limit, advertise_dns, ...) each
    # template maps to its own syntax (RouterOS ``/ipv6/nd``).
    ra: Optional[dict] = None
    # Static WireGuard tunnel config for a type == "wireguard" interface
    # (an external peering, unlike the fabric mesh links derived from
    # WGNetworkLink). Vendor-neutral dict -- ``port``,
    # ``private_key_secret`` (a Vault key on the host path), and
    # ``peers`` [{name, public_key, endpoint, port, keepalive,
    # allowed_ips}] -- each template maps to its own WireGuard syntax.
    wireguard: Optional[dict] = None

    def __post_init__(self) -> None:
        merged = []
        for addr in (self.address, self.ip6_address, *self.addresses):
            if addr and addr not in merged:
                merged.append(addr)
        self.addresses = merged
        self.address = next((a for a in merged if a.version == 4), None)
        self.ip6_address = next((a for a in merged if a.version == 6), None)

    @property
    def description(self) -> Optional[str]:
        result = self._description or ""

        if self.cable:
            result += f" ({self.cable})"

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
    def is_bridge(self) -> bool:
        """Whether the interface is an L2 bridge."""
        return self.type == "bridge"

    @property
    def is_wireguard(self) -> bool:
        """Whether the interface is a static WireGuard tunnel."""
        return self.type == "wireguard"

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
class NATMasquerade:
    """One rendered ``nat source`` masquerade rule."""

    rule: int
    interface: str
    source: Optional[str] = None
    destination: Optional[str] = None


@dataclass
class PortForward:
    """One rendered ``nat destination`` port-forward rule."""

    rule: int
    name: str
    interface: str
    protocol: str
    port: int
    to: str
    translation_port: Optional[int] = None

    @property
    def description(self) -> str:
        return f"Port Forward: {self.name}-{self.protocol.upper()} to {self.to}"


def parse_nat_rules(
    nat_meta: dict,
) -> tuple[List[NATMasquerade], List[PortForward]]:
    """Expand a network.yml ``nat:`` block into concrete rules.

    Rule numbers are assigned deterministically from YAML order:
    masquerades from 10, port-forwards from 100, one number per
    fan-out (each entry in ``destinations``, each protocol in
    ``protocols``). An explicit ``rule:`` on an entry restarts the
    counter there.
    """
    masquerades = []
    rule = 10
    for entry in nat_meta.get("masquerade", []):
        rule = entry.get("rule", rule)
        destinations = entry.get("destinations") or [entry.get("destination")]
        for destination in destinations:
            masquerades.append(
                NATMasquerade(
                    rule=rule,
                    interface=entry["interface"],
                    source=entry.get("source"),
                    destination=destination,
                )
            )
            rule += 1

    forwards = []
    rule = 100
    for entry in nat_meta.get("port_forwards", []):
        rule = entry.get("rule", rule)
        for protocol in entry.get("protocols") or [entry.get("protocol", "tcp")]:
            forwards.append(
                PortForward(
                    rule=rule,
                    name=entry["name"],
                    interface=entry["interface"],
                    protocol=protocol,
                    port=entry["port"],
                    to=entry["to"],
                    translation_port=entry.get("translation_port"),
                )
            )
            rule += 1

    return masquerades, forwards


@dataclass
class OSPFNetwork:
    """One network announced into an OSPF area."""

    network: str
    # Spelled as the device has it committed ("0" vs "0.0.0.0"): path
    # diffs are textual.
    area: str


@dataclass
class OSPFInterface:
    """Per-interface OSPF options."""

    name: str
    mtu_ignore: bool = False


@dataclass
class OSPFRedistribute:
    """A protocol redistributed into OSPF."""

    protocol: str
    metric_type: Optional[int] = None


@dataclass
class OSPFConfig:
    networks: List[OSPFNetwork] = field(default_factory=list)
    interfaces: List[OSPFInterface] = field(default_factory=list)
    redistribute: List[OSPFRedistribute] = field(default_factory=list)

    def __bool__(self) -> bool:
        return bool(self.networks or self.interfaces or self.redistribute)


def parse_ospf(ospf_meta: dict) -> OSPFConfig:
    """Parse a network.yml ``ospf:`` block into the typed model.

    Vendor-neutral snake_case keys; each vendor template translates to
    its own dialect.
    """
    return OSPFConfig(
        networks=[
            OSPFNetwork(network=entry["network"], area=str(entry["area"]))
            for entry in ospf_meta.get("networks", [])
        ],
        interfaces=[
            OSPFInterface(
                name=entry["name"],
                mtu_ignore=entry.get("mtu_ignore", False),
            )
            for entry in ospf_meta.get("interfaces", [])
        ],
        redistribute=[
            OSPFRedistribute(
                protocol=entry["protocol"],
                metric_type=entry.get("metric_type"),
            )
            for entry in ospf_meta.get("redistribute", [])
        ],
    )


@dataclass
class DeployDiff:
    """A vendor-agnostic config diff, ready to print.

    Attributes:
        text: The printable diff body (may be empty).
        has_changes: Whether deploying would change the device.
        summary: A one-line human summary for tables.
    """

    text: str
    has_changes: bool
    summary: str


class BaseHost:
    DEVICETYPE = "base"
    TEMPLATABLE = True
    # NAPALM driver name for vendors that support config diff/deploy
    # over the generic NAPALM path; None disables both.
    NAPALM_DRIVER: Optional[str] = None

    def __init__(
        self,
        hostname: str,
        role: str,
        # Addresses may be strs straight from network.yml or ipaddress
        # interfaces from Netbox.
        address: Optional[ipaddress.IPv4Interface | str] = None,
        ip6_address: Optional[ipaddress.IPv6Interface | str] = None,
        asn: Optional[int] = None,
        interfaces: Optional[List[HostInterface]] = None,
        nameservers: Optional[List[str]] = None,
        extra_config: Optional[List[str]] = None,
        snmp_location: Optional[str] = None,
        networks: Optional[List[str]] = None,
        cloud_init: bool = False,
        vlan_map: Optional[Dict[int, NetworkVLAN]] = None,
        config_context: Optional[dict] = None,
        nat: Optional[dict] = None,
        host_id: Optional[int] = None,
        ospf: Optional[dict] = None,
        static_routes: Optional[List[dict]] = None,
        bird: Optional[dict] = None,
        bgp: Optional[dict] = None,
        site: Optional[str] = None,
        **kwargs,
    ):
        self.address = address
        self.ip6_address = ip6_address

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
        # Small stable per-host index used to derive link ports
        # (51000 + min*64 + max). Append-only: never reuse a retired
        # host's id while surviving hosts may have shared a link.
        self.host_id = host_id
        self.nat_masquerades, self.nat_port_forwards = parse_nat_rules(nat or {})
        self.ospf = parse_ospf(ospf or {})
        # static_routes: [{network, interface | next-hop}].
        self.static_routes = static_routes or []
        # bird daemon knobs for linux hosts: router_id, krt_prefsrc,
        # merge_paths, import_filter (a filter name defined by the
        # host's human-owned conf.d drop-ins), import_check_function (a
        # bool function name from the same drop-ins, called first by
        # every generated site-weighted import filter).
        self.bird = bird or {}
        # BGP policy knobs beyond plain fabric membership. transit:
        # [{name, remote_as, link_network}] -- foreign ASes this host
        # reflects into the fabric (the far side of those links is not
        # barf-managed); presence flips the export posture from
        # accept-all to explicit-accepts + reject, mirroring how such
        # hosts are hand-configured.
        self.bgp = bgp or {}
        # Geographic site (network.yml global_meta.sites key) this host
        # lives in, if any. Backs BGP large-community origin tagging
        # and import local-pref weighting; hosts without one are
        # untagged and unweighted, unchanged from before this existed.
        self.site = site

        if not vlan_map:
            vlan_map = {}
        self.vlan_map = vlan_map

        self.vault = get_vault()

    @property
    def devicetype(self):
        return self.DEVICETYPE

    @property
    def can_bfd(self) -> bool:
        return False

    @property
    def is_templatable(self):
        """Whether the host can be templated."""
        return self.TEMPLATABLE

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
            functools.partial(
                tacacs_generator,
                f"{ipaddress.ip_interface(str(self.address)).ip.compressed}/32",
            ),
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

        # HACK: TACACS is broken, lol
        return list(hosts)

        resolver = dns.resolver.Resolver()
        query = resolver.query("tacacs.service.fmt2.consul", "A")
        for host in query:
            hosts.add(host)

        return list(hosts)

    @property
    def wg_endpoint(self) -> Optional[str]:
        """The address peers dial to reach this host's WireGuard.

        The global IPv6 address when there is one (matching what the
        fleet has deployed), else a global IPv4 address. None for hosts
        without a global address (e.g. behind NAT): nobody can dial
        them, so their peers must not render an endpoint and just
        listen.
        """
        for candidate in (self.ip6_address, self.address):
            if not candidate:
                continue
            ip = ipaddress.ip_interface(str(candidate)).ip
            if ip.is_global:
                return ip.compressed
        return None

    @property
    def management_address(
        self,
    ) -> Optional[ipaddress.IPv4Interface | ipaddress.IPv6Interface]:
        """Return the management address for this host."""
        for interface in self.interfaces:
            if interface.management:
                if interface.ip6_address:
                    return interface.ip6_address

                return interface.address

        return None

    def _endpoint_candidates(self) -> List[str]:
        """Addresses to try for reaching this host, most specific first.

        FQDN, management address, and host addresses in order, then
        interface addresses with external (global) ones first,
        deduplicated.
        """
        candidates = [f"{self.hostname}.generalprogramming.org"]
        if self.management_address:
            candidates.append(self.management_address.ip.compressed)
        for host_address in (self.address, self.ip6_address):
            if host_address:
                # May be a str straight from network.yml or an ipaddress
                # interface; normalize and drop any prefix length.
                candidates.append(
                    ipaddress.ip_interface(str(host_address)).ip.compressed
                )

        interface_ips = [
            iface_address.ip
            for interface in self.interfaces
            for iface_address in (interface.ip6_address, interface.address)
            if iface_address
        ]
        interface_ips.sort(key=lambda ip: not ip.is_global)
        candidates.extend(ip.compressed for ip in interface_ips)

        return list(dict.fromkeys(candidates))

    @lru_cache
    def _probe_endpoint(self, port: int) -> Optional[str]:
        """The first candidate answering on ``port``, probed once per host."""
        for address in self._endpoint_candidates():
            try:
                with socket.create_connection((address, port), timeout=2):
                    return address
            except OSError:
                continue

        return None

    @property
    def management_ip(self) -> Optional[str]:
        """The first endpoint answering on the HTTPS API port (443)."""
        return self._probe_endpoint(443)

    @property
    def ssh_ip(self) -> Optional[str]:
        """The first endpoint answering on SSH (22).

        Probed separately from ``management_ip``: sshd and the API can be
        bound to different addresses (as the fleet's stale listen-address
        drift demonstrated).
        """
        return self._probe_endpoint(22)

    def require_management_ip(self) -> str:
        """``management_ip``, raising when nothing is reachable."""
        address = self.management_ip
        if not address:
            raise RuntimeError(f"{self.hostname}: no reachable address")
        return address

    def human_version(self) -> str:
        """The running software version, e.g. ``2026.06.30-0048-rolling``.

        Raises when the device is unreachable. Vendors that can report
        their version override this.
        """
        raise NotImplementedError(
            f"{self.devicetype!r} devices do not report a version"
        )

    def version(self) -> Optional[str]:
        """``human_version``, or None when unsupported or unreachable.

        Doubles as the fleet liveness probe, so placeholder output ("-",
        empty) from a half-booted device also counts as not alive.
        """
        try:
            reported = self.human_version()
        except Exception:  # noqa: BLE001 - unreachable is an expected outcome
            return None

        if reported in ("", "-"):
            return None
        return reported

    def uptime(self) -> str:
        """The device's human-readable uptime.

        Vendors that can report their uptime override this.
        """
        raise NotImplementedError(
            f"{self.devicetype!r} devices do not report an uptime"
        )

    def safe_to_reboot(self, fleet: List[BaseHost]) -> bool:
        """Whether rebooting this host leaves the fleet redundant.

        Probes the other fleet members in parallel and raises
        RuntimeError with the reason if this reboot would take down the
        last live spine or leaf.
        """
        others = [h for h in fleet if h.hostname != self.hostname]
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            alive = dict(
                zip(
                    (other.hostname for other in others),
                    pool.map(lambda h: h.version() is not None, others),
                )
            )

        spines = [o.hostname for o in others if o.is_spine and alive[o.hostname]]
        leaves = [o.hostname for o in others if not o.is_spine and alive[o.hostname]]
        log.debug("Alive spines: %s, alive leaves: %s", spines, leaves)

        if self.is_spine and not spines:
            raise RuntimeError(
                f"refusing to reboot {self.hostname}: no other spine is online"
            )
        if not leaves:
            raise RuntimeError(
                f"refusing to reboot {self.hostname}: no other leaf is alive"
            )
        return True

    @property
    def image_provider(self) -> Optional["ImageProvider"]:
        """The upstream image provider for this devicetype, if any."""
        return PROVIDERS.get(self.devicetype)

    def verify_routing(self) -> Optional[str]:
        """Post-reboot routing health check.

        Returns a human-readable warning, or None when healthy or not
        applicable. Vendors with a routing plane override this.
        """
        return None

    def update_host(
        self,
        filename: str,
        stage: bool,
        drain_wait: int = 5,
        version: Optional[str] = None,
    ) -> str:
        """Stage a new system image onto the device, optionally rebooting.

        Args:
            filename: Local path of the image to push.
            stage: Only preload the image; do not drain or reboot.
            drain_wait: Seconds to wait for traffic to drain before the
                reboot, where the vendor supports draining.
            version: Image version; vendors derive it from the filename
                when not given.

        Returns:
            A short human-readable result.

        Vendors that support in-place image updates override this.
        """
        raise NotImplementedError(
            f"{self.devicetype!r} devices do not support image updates"
        )

    def _napalm_address(self) -> Optional[str]:
        """The address NAPALM should connect to (SSH by default)."""
        return self.ssh_ip

    def _napalm_connection(self):
        """An open NAPALM connection to this host; the caller closes it."""
        address = self._napalm_address()
        if not address:
            raise RuntimeError(f"{self.hostname}: no reachable address")
        return open_connection(self, address)

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff ``rendered`` against the device's running config.

        The base implementation loads a NAPALM merge candidate and asks
        the device to compare it, then discards the candidate. Vendors
        that can diff locally (VyOS) override this.

        Args:
            rendered: The rendered candidate config.
            redact: Hide secret values in the diff text, where the
                vendor supports it.
            show_device_only: Include config that exists only on the
                device, where the vendor supports it.
        """
        if not self.NAPALM_DRIVER:
            raise NotImplementedError(
                f"{self.devicetype!r} devices do not support config diffs"
            )

        device = self._napalm_connection()
        try:
            device.load_merge_candidate(config=rendered)
            text = device.compare_config() or ""
            device.discard_config()
        finally:
            device.close()

        has_changes = bool(text.strip())
        return DeployDiff(
            text=text,
            has_changes=has_changes,
            summary="changes pending" if has_changes else "no changes",
        )

    def push_rendered_config(self, rendered: str) -> None:
        """Merge ``rendered`` into the device config and commit it.

        Confirmation and diff display live in the CLI; this only pushes.
        """
        if not self.NAPALM_DRIVER:
            raise NotImplementedError(
                f"{self.devicetype!r} devices do not support config deploys"
            )

        device = self._napalm_connection()
        try:
            device.load_merge_candidate(config=rendered)
            device.commit_config()
        finally:
            device.close()

    def cleanup_host(self) -> List[str]:
        """Run housekeeping tasks on the device.

        Returns:
            Human-readable descriptions of the actions taken.

        Vendors with housekeeping tasks (e.g. old image removal) override
        this.
        """
        raise NotImplementedError(f"{self.devicetype!r} devices do not support cleanup")

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
            # A single `address` and/or a list of `addresses`, any mix
            # of IPv4/IPv6.
            raw_addresses = [interface.get("address"), *interface.get("addresses", [])]
            addresses = [ipaddress.ip_interface(raw) for raw in raw_addresses if raw]

            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    # Plain fabric interfaces are VPNLinks; a bridge (or
                    # any modeled L2/L3 interface) declares its own type.
                    type=interface.get("type", "VPNLink"),
                    _description=interface.get("description"),
                    enabled=interface.get("enabled", True),
                    addresses=addresses,
                    dhcp=interface.get("dhcp", False),
                    dhcpv6=interface.get("dhcpv6", False),
                    ipv6_autoconf=interface.get("ipv6_autoconf", False),
                    untagged_vlan=NetworkVLAN(vid=interface.get("vlan")),
                    tagged_vlans=[
                        NetworkVLAN(vid=vlan)
                        for vlan in interface.get("tagged_vlans", [])
                    ],
                    mtu=interface.get("mtu", None),
                    management=interface.get("management", False),
                    members=interface.get("members", []),
                    ra=interface.get("ra", None),
                    wireguard=interface.get("wireguard", None),
                )
            )

        return cls(
            hostname=hostname,
            role=meta["role"],
            address=meta.get("address", None),
            ip6_address=meta.get("ip6_address", None),
            asn=meta["asn"],
            nameservers=meta.get("nameservers", []),
            extra_config=meta.get("extra_config", []),
            networks=meta.get("networks", []),
            snmp_location=meta.get("location", None),
            interfaces=interfaces,
            cloud_init=meta.get("cloud_init", False),
            nat=meta.get("nat", None),
            host_id=meta.get("id", None),
            ospf=meta.get("ospf", None),
            static_routes=meta.get("static_routes", None),
            bird=meta.get("bird", None),
            bgp=meta.get("bgp", None),
            site=meta.get("site", None),
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

            addresses = [
                ipaddress.ip_interface(entry["address"])
                for entry in interface.get("ip_addresses") or []
                if entry.get("address")
            ]

            interfaces.append(
                HostInterface(
                    name=interface["name"],
                    type=interface["type"],
                    mode=interface["mode"],
                    _description=interface.get("description", None),
                    addresses=addresses,
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
            ip6_address=(
                ipaddress.IPv6Interface(netbox_meta["primary_ip6"]["address"])
                if netbox_meta["primary_ip6"]
                else None
            ),
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
from barf.vendors.mikrotik import MikroTikHost
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
    "mikrotik": MikroTikHost,
}
