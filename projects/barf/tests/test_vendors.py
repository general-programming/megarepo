import contextlib
import ipaddress

from barf.vendors import BaseHost, HostInterface


def make_host():
    return BaseHost(
        hostname="testbox",
        role="vpn",
        # A plain string with a prefix, straight-from-network.yml style.
        address="10.0.0.1/24",
        ip6_address="2602:fa6d:10::1",
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                address=ipaddress.IPv4Interface("10.255.2.9/32"),
                management=True,
            ),
            HostInterface(
                name="eth0",
                type="VPNLink",
                ip6_address=ipaddress.IPv6Interface("2602:fa6d:10::1/64"),
            ),
            HostInterface(
                name="eth1",
                type="VPNLink",
                address=ipaddress.IPv4Interface("10.3.2.4/23"),
            ),
        ],
    )


def test_management_ip_probe_order_and_dedup(monkeypatch):
    attempts = []

    def refuse(addr, timeout=None):
        attempts.append(addr[0])
        raise OSError("connection refused")

    monkeypatch.setattr("barf.vendors.socket.create_connection", refuse)

    assert make_host().management_ip is None
    # FQDN, management, host v4 (prefix stripped), host v6, then remaining
    # interface addresses (external/global before private), no duplicates.
    assert attempts == [
        "testbox.generalprogramming.org",
        "10.255.2.9",
        "10.0.0.1",
        "2602:fa6d:10::1",
        "10.3.2.4",
    ]


def test_management_ip_returns_first_reachable(monkeypatch):
    def connect(addr, timeout=None):
        if addr[0] == "10.0.0.1":
            return contextlib.nullcontext()
        raise OSError("connection refused")

    monkeypatch.setattr("barf.vendors.socket.create_connection", connect)

    assert make_host().management_ip == "10.0.0.1"


def test_management_and_ssh_ips_probe_their_own_ports(monkeypatch):
    # sshd and the API can be bound to different addresses (stale
    # listen-address drift), so each service probes its own port.
    ports = []

    def connect(addr, timeout=None):
        ports.append(addr[1])
        return contextlib.nullcontext()

    monkeypatch.setattr("barf.vendors.socket.create_connection", connect)

    host = make_host()
    assert host.management_ip is not None
    assert host.ssh_ip is not None
    assert ports == [443, 22]


class TestWgEndpoint:
    """wg_endpoint picks the address peers dial for WireGuard."""

    def test_prefers_global_ipv6(self):
        host = BaseHost(
            hostname="x",
            role="vpn",
            address="79.110.170.6",
            ip6_address="2a0d:1a43:8008:420::1",
        )
        assert host.wg_endpoint == "2a0d:1a43:8008:420::1"

    def test_falls_back_to_global_ipv4(self):
        host = BaseHost(hostname="x", role="vpn", address="79.110.170.6")
        assert host.wg_endpoint == "79.110.170.6"

    def test_private_address_means_no_endpoint(self):
        # A NATed host (e.g. sea69-acc-v-a-1) cannot be dialed; peers
        # must not render an endpoint for it.
        host = BaseHost(hostname="x", role="vpn", address="10.36.75.234")
        assert host.wg_endpoint is None

    def test_no_addresses_means_no_endpoint(self):
        assert BaseHost(hostname="x", role="vpn").wg_endpoint is None


class TestInterfaceAddresses:
    """HostInterface carries all addresses; first-of-family shortcuts stay."""

    def test_addresses_list_populates_family_shortcuts(self):
        iface = HostInterface(
            name="dum0",
            type="VPNLink",
            addresses=[
                ipaddress.ip_interface("2602:fa6d:f:aaaa::f01/128"),
                ipaddress.ip_interface("10.255.2.9/32"),
            ],
        )
        assert iface.address == ipaddress.IPv4Interface("10.255.2.9/32")
        assert iface.ip6_address == ipaddress.IPv6Interface("2602:fa6d:f:aaaa::f01/128")
        assert len(iface.addresses) == 2

    def test_scalar_kwargs_merge_into_addresses(self):
        iface = HostInterface(
            name="eth0",
            type="VPNLink",
            address=ipaddress.IPv4Interface("10.0.0.1/24"),
        )
        assert iface.addresses == [ipaddress.IPv4Interface("10.0.0.1/24")]

    def test_no_addresses(self):
        iface = HostInterface(name="eth0", type="VPNLink")
        assert iface.addresses == []
        assert iface.address is None
        assert iface.ip6_address is None

    def test_from_meta_accepts_address_and_addresses(self):
        host = BaseHost.from_meta(
            "testbox",
            {
                "role": "vpn",
                "asn": 64512,
                "interfaces": [
                    {
                        "name": "dum0",
                        "addresses": ["2602:fa6d:f:aaaa::f01/128", "10.255.2.9/32"],
                    },
                    {"name": "eth0", "address": "10.3.2.4/23"},
                ],
            },
        )
        dum0, eth0 = host.interfaces
        assert [a.with_prefixlen for a in dum0.addresses] == [
            "2602:fa6d:f:aaaa::f01/128",
            "10.255.2.9/32",
        ]
        assert eth0.addresses == [ipaddress.IPv4Interface("10.3.2.4/23")]


class TestParseNatRules:
    def test_masquerade_fan_out_numbers_from_10(self):
        from barf.vendors import parse_nat_rules

        masq, forwards = parse_nat_rules(
            {
                "masquerade": [
                    {"interface": "eth0", "source": "10.3.2.0/23"},
                    {
                        "interface": "wg51820",
                        "destinations": ["10.213.8.0/21", "10.213.0.0/24"],
                    },
                ]
            }
        )
        assert forwards == []
        assert [(m.rule, m.interface, m.source, m.destination) for m in masq] == [
            (10, "eth0", "10.3.2.0/23", None),
            (11, "wg51820", None, "10.213.8.0/21"),
            (12, "wg51820", None, "10.213.0.0/24"),
        ]

    def test_port_forward_protocol_fan_out_numbers_from_100(self):
        from barf.vendors import parse_nat_rules

        _, forwards = parse_nat_rules(
            {
                "port_forwards": [
                    {
                        "name": "Torrent",
                        "interface": "eth0",
                        "port": 30888,
                        "protocols": ["udp", "tcp"],
                        "to": "10.3.2.10",
                    }
                ]
            }
        )
        assert [(f.rule, f.protocol) for f in forwards] == [(100, "udp"), (101, "tcp")]
        assert forwards[0].description == "Port Forward: Torrent-UDP to 10.3.2.10"

    def test_explicit_rule_restarts_the_counter(self):
        from barf.vendors import parse_nat_rules

        masq, _ = parse_nat_rules(
            {
                "masquerade": [
                    {"interface": "eth0", "source": "10.0.0.0/24"},
                    {"interface": "eth1", "source": "10.1.0.0/24", "rule": 50},
                    {"interface": "eth2", "source": "10.2.0.0/24"},
                ]
            }
        )
        assert [m.rule for m in masq] == [10, 50, 51]

    def test_single_protocol_defaults_to_tcp(self):
        from barf.vendors import parse_nat_rules

        _, forwards = parse_nat_rules(
            {
                "port_forwards": [
                    {"name": "Web", "interface": "eth0", "port": 80, "to": "10.0.0.5"}
                ]
            }
        )
        assert [(f.rule, f.protocol) for f in forwards] == [(100, "tcp")]

    def test_from_meta_parses_nat(self):
        host = BaseHost.from_meta(
            "testbox",
            {
                "role": "vpn",
                "asn": 64512,
                "nat": {"masquerade": [{"interface": "eth0", "source": "10.0.0.0/24"}]},
            },
        )
        assert host.nat_masquerades[0].rule == 10
        assert host.nat_port_forwards == []

    def test_no_nat_block(self):
        host = BaseHost(hostname="x", role="vpn")
        assert host.nat_masquerades == []
        assert host.nat_port_forwards == []
