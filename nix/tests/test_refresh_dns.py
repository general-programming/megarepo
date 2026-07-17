"""Tests for the NetBox -> dnsmasq generator (nix/modules/dns/refresh_dns.py).

The script is intentionally not part of a package (it gets embedded into a
NixOS machine closure), so it is loaded directly from its file path.
"""

import importlib.util
from pathlib import Path
from types import ModuleType

import pytest


def _load_module() -> ModuleType:
    path = Path(__file__).parent.parent / "modules" / "dns" / "refresh_dns.py"
    spec = importlib.util.spec_from_file_location("refresh_dns", path)
    assert spec is not None and spec.loader is not None
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


refresh_dns = _load_module()


def _device(
    name: str, ipv4: str = "", ipv6: str = "", interfaces: list | None = None
) -> dict:
    return {
        "name": name,
        "primary_ip4": {"address": ipv4} if ipv4 else None,
        "primary_ip6": {"address": ipv6} if ipv6 else None,
        "interfaces": interfaces or [],
    }


def test_clean_hostname() -> None:
    assert refresh_dns.clean_hostname("host - name") == "host_name"
    assert refresh_dns.clean_hostname("sw:1/2") == "sw1_2"


def test_reverse_arpa() -> None:
    assert refresh_dns.reverse_arpa("10.65.67.5") == "5.67.65.10.in-addr.arpa"


def test_dns_lines_v4_and_ptr() -> None:
    lines = list(
        refresh_dns.dns_lines(
            [_device("fmt2-core", ipv4="10.65.67.5/24")], "example.org"
        )
    )
    assert lines == [
        "address=/fmt2-core.example.org/10.65.67.5",
        "ptr-record=5.67.65.10.in-addr.arpa,fmt2-core.example.org",
    ]


def test_dns_lines_v6_has_no_ptr() -> None:
    lines = list(
        refresh_dns.dns_lines([_device("box", ipv6="fd00::1/64")], "example.org")
    )
    assert lines == ["address=/box.example.org/fd00::1"]


def test_dns_lines_skips_non_static_and_missing_ips() -> None:
    hosts = [
        _device("site1-ap-closet", ipv4="10.0.0.2/24"),
        _device("das-keyboard"),
        _device("no-ip-host"),
    ]
    assert list(refresh_dns.dns_lines(hosts, "example.org")) == []


def test_dns_lines_ipmi() -> None:
    host = _device(
        "metal1",
        ipv4="10.0.0.5/24",
        interfaces=[
            {"name": "eno1", "ip_addresses": [{"address": "10.0.0.5/24"}]},
            {"name": "iDRAC", "ip_addresses": [{"address": "10.9.0.5/24"}]},
        ],
    )
    lines = list(refresh_dns.dns_lines([host], "example.org"))
    assert "address=/ipmi.metal1.example.org/10.9.0.5" in lines
    assert "ptr-record=5.0.9.10.in-addr.arpa,metal1.example.org" in lines


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


def test_dhcp_lines_basic() -> None:
    lines = list(
        refresh_dns.dhcp_lines(
            [_interface("AA:BB:CC:DD:EE:FF", "host1", "eth0", ["10.0.0.9/24"])]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:FF,10.0.0.9,host1-eth0"]


def test_dhcp_primary_interface_gets_bare_device_name() -> None:
    lines = list(
        refresh_dns.dhcp_lines(
            [
                _interface(
                    "AA:BB:CC:DD:EE:FF",
                    "sea1-k8s-0",
                    "internal",
                    ["10.3.2.10/24"],
                    primary_ip4="10.3.2.10/24",
                )
            ]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:FF,10.3.2.10,sea1-k8s-0"]


def test_dhcp_secondary_interface_keeps_suffix() -> None:
    lines = list(
        refresh_dns.dhcp_lines(
            [
                _interface(
                    "AA:BB:CC:DD:EE:FF",
                    "sea1-k8s-0",
                    "internal",
                    ["10.3.2.10/24"],
                    primary_ip4="10.3.2.10/24",
                ),
                _interface(
                    "AA:BB:CC:DD:EE:00",
                    "sea1-k8s-0",
                    "storage",
                    ["10.3.7.10/24"],
                    primary_ip4="10.3.2.10/24",
                ),
            ]
        )
    )
    assert lines == [
        "dhcp-host=AA:BB:CC:DD:EE:FF,10.3.2.10,sea1-k8s-0",
        "dhcp-host=AA:BB:CC:DD:EE:00,10.3.7.10,sea1-k8s-0-storage",
    ]


def test_dhcp_lines_one_reservation_per_mac() -> None:
    interface = _interface(
        "AA:BB:CC:DD:EE:FF", "host1", "eth0", ["10.0.0.9/24", "10.0.0.10/24"]
    )
    assert len(list(refresh_dns.dhcp_lines([interface]))) == 1


def test_dhcp_lines_skips_missing_mac_and_v6() -> None:
    interfaces = [
        _interface(None, "host1", "eth0", ["10.0.0.9/24"]),
        _interface("AA:BB:CC:DD:EE:00", "host2", "eth0", ["fd00::9/64"]),
    ]
    assert list(refresh_dns.dhcp_lines(interfaces)) == []


def test_dhcp_hostname_sanitized() -> None:
    lines = list(
        refresh_dns.dhcp_lines(
            [_interface("AA:BB:CC:DD:EE:11", "Host One", "Gi1/0/1", ["10.0.0.4/24"])]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:11,10.0.0.4,host-one-gi1-0-1"]


def test_render_refuses_empty_dns() -> None:
    dns_data = {"device_list": [], "virtual_machine_list": []}
    dhcp_data = {"interface_list": [], "vm_interface_list": []}

    with pytest.raises(RuntimeError, match="no usable DNS records"):
        refresh_dns.render(dns_data, dhcp_data, "example.org")


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
        refresh_dns.render(dns_data, dhcp_data, "example.org")


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

    output = refresh_dns.render(dns_data, dhcp_data, "example.org")

    assert output.startswith("# Generated from NetBox")
    assert output.endswith("\n")
    assert "address=/fmt2-core.example.org/10.65.67.5" in output
    assert "address=/vm1.example.org/10.0.0.7" in output
    assert "dhcp-host=AA:BB:CC:DD:EE:FF,10.65.67.5,fmt2-core" in output
