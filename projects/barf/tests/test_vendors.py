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
