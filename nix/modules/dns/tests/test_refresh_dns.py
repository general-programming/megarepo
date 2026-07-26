import pytest
from refresh_dns import dhcp_lines, render


def iface(
    mac: str | None,
    addresses: list[str],
    name: str = "internal",
    vm: str | None = "sea1-k8s-0",
    primary_ip4: str | None = None,
) -> dict:
    owner = None
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


def test_dual_stack_reservation() -> None:
    lines = list(
        dhcp_lines([iface("BC:24:11:6A:62:B3", ["10.3.2.10/23", "2602:fa6d:10:ffff::110/64"])])
    )
    assert lines == [
        "dhcp-host=BC:24:11:6A:62:B3,10.3.2.10,[2602:fa6d:10:ffff::110],sea1-k8s-0-internal"
    ]


def test_v4_only_reservation() -> None:
    lines = list(dhcp_lines([iface("AA:BB:CC:DD:EE:01", ["10.3.2.50/23"])]))
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:01,10.3.2.50,sea1-k8s-0-internal"]


def test_v6_only_reservation() -> None:
    lines = list(dhcp_lines([iface("AA:BB:CC:DD:EE:02", ["2602:fa6d:10:ffff::120/64"])]))
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
            [iface("AA:BB:CC:DD:EE:07", ["10.3.2.99/23"], name="eth1", primary_ip4="10.3.2.10/23")]
        )
    )
    assert lines == ["dhcp-host=AA:BB:CC:DD:EE:07,10.3.2.99,sea1-k8s-0-eth1"]


def test_render_refuses_empty_netbox() -> None:
    with pytest.raises(RuntimeError):
        render(
            {"device_list": [], "virtual_machine_list": []},
            {"interface_list": [], "vm_interface_list": []},
            "example.org",
        )
