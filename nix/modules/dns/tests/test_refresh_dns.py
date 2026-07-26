"""Tests for the NetBox -> dnsmasq generator (nix/modules/dns/refresh_dns.py).

The script is not part of a package (it gets embedded into a NixOS machine
closure); conftest.py puts its directory on sys.path so it imports by name.

dhcp_lines accepts two owner shapes — `device` for physical interfaces and
`virtual_machine` for VM ones — so both are exercised below.
"""

import pytest
from refresh_dns import clean_hostname, dhcp_lines, dns_lines, render, reverse_arpa


def _device(
    name: str, ipv4: str = "", ipv6: str = "", interfaces: list | None = None
) -> dict:
    return {
        "name": name,
        "primary_ip4": {"address": ipv4} if ipv4 else None,
        "primary_ip6": {"address": ipv6} if ipv6 else None,
        "interfaces": interfaces or [],
    }


def _interface(
    mac: str | None,
    device: str,
    name: str = "eth0",
    addresses: list[str] | None = None,
    primary_ip4: str = "",
) -> dict:
    return {
        "primary_mac_address": {"mac_address": mac} if mac else None,
        "name": name,
        "device": {
            "name": device,
            "primary_ip4": {"address": primary_ip4} if primary_ip4 else None,
        },
        "ip_addresses": [{"address": address} for address in (addresses or [])],
    }


def iface(
    mac: str | None,
    addresses: list[str],
    name: str = "internal",
    vm: str | None = "sea1-k8s-0",
    primary_ip4: str | None = None,
) -> dict:
    owner: dict | None = None
    if vm:
        owner = {"name": vm}
        if primary_ip4:
            owner["primary_ip4"] = {"address": primary_ip4}
    return {
        "primary_mac_address": {"mac_address": mac} if mac else None,
        "name": name,
        "virtual_machine": owner,
        "ip_addresses": [{"address": a} for a in addresses],
    }


# --- DNS records ---


def test_clean_hostname() -> None:
    assert clean_hostname("host - name") == "host_name"
    assert clean_hostname("sw:1/2") == "sw1_2"


def test_reverse_arpa() -> None:
    assert reverse_arpa("10.65.67.5") == "5.67.65.10.in-addr.arpa"


def test_dns_lines_v4_and_ptr() -> None:
    lines = list(dns_lines([_device("fmt2-core", ipv4="10.65.67.5/24")], "example.org"))
    assert lines == [
        "address=/fmt2-core.example.org/10.65.67.5",
        "ptr-record=5.67.65.10.in-addr.arpa,fmt2-core.example.org",
    ]


def test_dns_lines_v6_has_no_ptr() -> None:
    lines = list(dns_lines([_device("box", ipv6="fd00::1/64")], "example.org"))
    assert lines == ["address=/box.example.org/fd00::1"]


def test_dns_lines_skips_non_static_and_missing_ips() -> None:
    hosts = [
        _device("site1-ap-closet", ipv4="10.0.0.2/24"),
        _device("das-keyboard"),
        _device("no-ip-host"),
    ]
    assert list(dns_lines(hosts, "example.org")) == []


def test_dns_lines_ipmi() -> None:
    host = _device(
        "metal1",
        ipv4="10.0.0.5/24",
        interfaces=[
            {"name": "eno1", "ip_addresses": [{"address": "10.0.0.5/24"}]},
            {"name": "iDRAC", "ip_addresses": [{"address": "10.9.0.5/24"}]},
        ],
    )
    lines = list(dns_lines([host], "example.org"))
    assert "address=/ipmi.metal1.example.org/10.9.0.5" in lines
    assert "ptr-record=5.0.9.10.in-addr.arpa,metal1.example.org" in lines


# --- DHCP reservations, device-owned interfaces ---


def test_dhcp_lines_basic() -> None:
    lines = list(
        dhcp_lines([_interface("AA:BB:CC:DD:EE:FF", "host1", "eth0", ["10.0.0.9/24"])])
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:FF,10.0.0.9,host1-eth0"]


def test_dhcp_lines_skips_missing_mac() -> None:
    # A v6-only interface is a valid reservation since dual-stack support
    # landed, so only the MAC-less interface drops out here.
    interfaces = [
        _interface(None, "host1", "eth0", ["10.0.0.9/24"]),
        _interface("AA:BB:CC:DD:EE:00", "host2", "eth0", ["fd00::9/64"]),
    ]
    assert list(dhcp_lines(interfaces)) == [
        "dhcp-host=AA:BB:CC:DD:EE:00,[fd00::9],host2-eth0"
    ]


def test_dhcp_hostname_sanitized() -> None:
    lines = list(
        dhcp_lines(
            [_interface("AA:BB:CC:DD:EE:11", "Host One", "Gi1/0/1", ["10.0.0.4/24"])]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:11,10.0.0.4,host-one-gi1-0-1"]


# --- DHCP reservations, VM-owned interfaces ---


def test_dual_stack_reservation() -> None:
    lines = list(
        dhcp_lines(
            [iface("BC:24:11:6A:62:B3", ["10.3.2.10/23", "2602:fa6d:10:ffff::110/64"])]
        )
    )
    assert lines == [
        "dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0-internal"
    ]


def test_v4_only_reservation() -> None:
    lines = list(dhcp_lines([iface("AA:BB:CC:DD:EE:01", ["10.3.2.50/23"])]))
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:01,10.3.2.50,sea1-k8s-0-internal"]


def test_v6_only_reservation() -> None:
    lines = list(
        dhcp_lines([iface("AA:BB:CC:DD:EE:02", ["2602:fa6d:10:ffff::120/64"])])
    )
    assert lines == [
        "dhcp-host=AA:BB:CC:DD:EE:02,[2602:fa6d:10:ffff::120],sea1-k8s-0-internal"
    ]


def test_first_address_of_each_family_wins() -> None:
    lines = list(
        dhcp_lines(
            [
                iface(
                    "AA:BB:CC:DD:EE:03",
                    [
                        "10.3.2.60/23",
                        "10.3.2.61/23",
                        "2602:fa6d:10:ffff::130/64",
                        "2602:fa6d:10:ffff::131/64",
                    ],
                )
            ]
        )
    )
    assert lines == [
        "dhcp-host=AA:BB:CC:DD:EE:03,10.3.2.60,[2602:fa6d:10:ffff::130],sea1-k8s-0-internal"
    ]


def test_duplicate_mac_first_interface_wins() -> None:
    lines = list(
        dhcp_lines(
            [
                iface("AA:BB:CC:DD:EE:04", ["10.3.2.70/23"], name="eth0"),
                iface("aa:bb:cc:dd:ee:04", ["10.3.2.71/23"], name="eth1"),
            ]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:04,10.3.2.70,sea1-k8s-0-eth0"]


def test_interface_without_addresses_does_not_consume_mac() -> None:
    lines = list(
        dhcp_lines(
            [
                iface("AA:BB:CC:DD:EE:05", [], name="eth0"),
                iface("AA:BB:CC:DD:EE:05", ["10.3.2.80/23"], name="eth1"),
            ]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:05,10.3.2.80,sea1-k8s-0-eth1"]


def test_missing_mac_or_owner_skipped() -> None:
    lines = list(
        dhcp_lines(
            [
                iface(None, ["10.3.2.90/23"]),
                iface("AA:BB:CC:DD:EE:06", ["10.3.2.91/23"], vm=None),
            ]
        )
    )
    assert lines == []


def test_primary_interface_gets_bare_device_hostname() -> None:
    lines = list(
        dhcp_lines(
            [
                iface(
                    "BC:24:11:6A:62:B3",
                    ["10.3.2.10/23", "2602:fa6d:10:ffff::110/64"],
                    primary_ip4="10.3.2.10/23",
                )
            ]
        )
    )
    assert lines == [
        "dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0"
    ]


def test_secondary_interface_keeps_suffix() -> None:
    lines = list(
        dhcp_lines(
            [
                iface(
                    "AA:BB:CC:DD:EE:07",
                    ["10.3.2.99/23"],
                    name="eth1",
                    primary_ip4="10.3.2.10/23",
                )
            ]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:07,10.3.2.99,sea1-k8s-0-eth1"]


# --- Whole-config render ---


def test_render_refuses_empty_dns() -> None:
    dns_data = {"device_list": [], "virtual_machine_list": []}
    dhcp_data = {"interface_list": [], "vm_interface_list": []}

    with pytest.raises(RuntimeError, match="no usable DNS records"):
        render(dns_data, dhcp_data, "example.org")


def test_render_refuses_all_skipped_hosts() -> None:
    dns_data = {
        "device_list": [
            _device("no-ip-host"),
            _device("site1-ap-closet", ipv4="10.0.0.2/24"),
        ],
        "virtual_machine_list": [],
    }
    dhcp_data = {"interface_list": [], "vm_interface_list": []}

    with pytest.raises(RuntimeError, match="no usable DNS records"):
        render(dns_data, dhcp_data, "example.org")


def test_render_full_config() -> None:
    dns_data = {
        "device_list": [_device("fmt2-core", ipv4="10.65.67.5/24")],
        "virtual_machine_list": [_device("vm1", ipv4="10.0.0.7/24")],
    }
    dhcp_data = {
        "interface_list": [
            _interface(
                "AA:BB:CC:DD:EE:FF",
                "fmt2-core",
                "eno1",
                ["10.65.67.5/24"],
                primary_ip4="10.65.67.5/24",
            )
        ],
        "vm_interface_list": [],
    }

    output = render(dns_data, dhcp_data, "example.org")

    assert output.startswith("# Generated from NetBox")
    assert output.endswith("\n")
    assert "address=/fmt2-core.example.org/10.65.67.5" in output
    assert "address=/vm1.example.org/10.0.0.7" in output
    assert "dhcp-host=AA:BB:CC:DD:EE:FF,10.65.67.5,fmt2-core" in output
