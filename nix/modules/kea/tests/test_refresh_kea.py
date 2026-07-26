from refresh_kea import reservations


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
    hosts4, hosts6 = reservations(
        [
            iface(
                "BC:24:11:6A:62:B3",
                ["10.3.2.10/23", "2602:fa6d:10:ffff::110/64"],
                primary_ip4="10.3.2.10/23",
            )
        ]
    )
    assert hosts4 == [
        {
            "hw-address": "BC:24:11:6A:62:B3",
            "ip-address": "10.3.2.10",
            "hostname": "sea1-k8s-0",
        }
    ]
    assert hosts6 == [
        {
            "hw-address": "BC:24:11:6A:62:B3",
            "ip-addresses": ["2602:fa6d:10:ffff::110"],
            "hostname": "sea1-k8s-0",
        }
    ]


def test_v4_only_and_v6_only() -> None:
    hosts4, hosts6 = reservations(
        [
            iface("AA:BB:CC:DD:EE:01", ["10.3.2.50/23"]),
            iface("AA:BB:CC:DD:EE:02", ["2602:fa6d:10:ffff::120/64"], name="eth1"),
        ]
    )
    assert [h["hw-address"] for h in hosts4] == ["AA:BB:CC:DD:EE:01"]
    assert [h["hw-address"] for h in hosts6] == ["AA:BB:CC:DD:EE:02"]


def test_secondary_interface_keeps_suffix() -> None:
    hosts4, _ = reservations(
        [iface("AA:BB:CC:DD:EE:03", ["10.3.2.99/23"], name="eth1", primary_ip4="10.3.2.10/23")]
    )
    assert hosts4[0]["hostname"] == "sea1-k8s-0-eth1"


def test_first_address_of_each_family_wins() -> None:
    hosts4, hosts6 = reservations(
        [
            iface(
                "AA:BB:CC:DD:EE:04",
                [
                    "10.3.2.60/23",
                    "10.3.2.61/23",
                    "2602:fa6d:10:ffff::130/64",
                    "2602:fa6d:10:ffff::131/64",
                ],
            )
        ]
    )
    assert hosts4[0]["ip-address"] == "10.3.2.60"
    assert hosts6[0]["ip-addresses"] == ["2602:fa6d:10:ffff::130"]


def test_duplicate_mac_first_interface_wins() -> None:
    hosts4, _ = reservations(
        [
            iface("AA:BB:CC:DD:EE:05", ["10.3.2.70/23"], name="eth0"),
            iface("aa:bb:cc:dd:ee:05", ["10.3.2.71/23"], name="eth1"),
        ]
    )
    assert len(hosts4) == 1
    assert hosts4[0]["ip-address"] == "10.3.2.70"


def test_missing_mac_owner_or_addresses_skipped() -> None:
    hosts4, hosts6 = reservations(
        [
            iface(None, ["10.3.2.90/23"]),
            iface("AA:BB:CC:DD:EE:06", ["10.3.2.91/23"], vm=None),
            iface("AA:BB:CC:DD:EE:07", []),
        ]
    )
    assert hosts4 == []
    assert hosts6 == []
