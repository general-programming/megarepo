"""Template-vs-blocks equivalence for the VyOS port.

The goldens pin the seven live VyOS hosts; this matrix pins the
template semantics for combos the fleet doesn't currently exercise.
Renders every variant through BOTH the retired-in-place Jinja template
chain (vpn/vyos.j2 -> common/vyos.j2 -> common/vyatta.j2) and the
block renderer and requires byte-identical output. Delete together
with the templates.
"""

import ipaddress

import pytest
from conftest import COMMUNITY_ASN, SITES, make_link

from barf.common import render_template
from barf.configs import build_context, render_blocks
from barf.util.sites import SITE_ORIGIN_FUNC
from barf.vendors import BaseHost, HostInterface
from barf.vendors.vyos import VyOSHost

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")

GLOBAL_META = {
    "ssh_keys": ["ssh-ed25519 AAAAKEY erin"],
    "sites": SITES,
    "community_asn": COMMUNITY_ASN,
    "search_domain": "example.org",
    "snmp_public": "public",
    "snmp_contact": "noc@example.org",
    "snmp_location": "global location",
}


def make_vyos(**overrides) -> VyOSHost:
    meta = {
        "hostname": "test-vpn-leaf-1",
        "role": "vpn",
        "asn": 4280805526,
        "site": "fmt2",
        "networks": ["10.65.67.0/24"],
        "interfaces": [
            HostInterface(
                name="dum0",
                type="VPNLink",
                address=ipaddress.IPv4Interface("10.255.2.3/32"),
                management=True,
            )
        ],
    }
    meta.update(overrides)
    return VyOSHost(**meta)


def make_links(device) -> list:
    spine_fmt2 = make_vyos(
        hostname="fmt2-vpn-spine-1", asn=4280805525, site="fmt2", interfaces=[]
    )
    spine_fmt2.address = "79.110.170.6"
    spine_sea = make_vyos(
        hostname="sea1-vpn-spine-1", asn=4280805529, site="sea", interfaces=[]
    )
    return [
        make_link(51067, spine_fmt2, device),
        make_link(51207, spine_sea, device, network="172.31.255.8/31"),
    ]


def make_ipsec_link(device):
    oracle = BaseHost(
        hostname="oracle-vpn-1-1",
        role="vpn",
        asn=31898,
        address="129.146.0.1",
        site="fmt2",
    )
    return make_link(51831, oracle, device, network="172.31.255.20/31")


def render_both_ways(device, links) -> tuple:
    device.secret = lambda key, *args, **kwargs: f"SECRET-{key}"
    secrets = {"vyos_api_password": "APIKEY", "oracle-psk": "PSK"}
    ctx = build_context(device, links, GLOBAL_META, secrets)
    template = render_template(
        "vpn/vyos.j2",
        device=device,
        secrets=secrets,
        global_meta=GLOBAL_META,
        vpn_links=ctx.vpn_links,
        site_import_rules=ctx.site_import_rules,
        origin_site=ctx.origin_site,
        community_asn=ctx.community_asn,
        SITE_ORIGIN_FUNC=SITE_ORIGIN_FUNC,
    )
    return template, render_blocks(ctx)


def multi_interface_host() -> VyOSHost:
    return make_vyos(
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                address=ipaddress.IPv4Interface("10.255.2.3/32"),
                management=True,
                _description="underlay loopback",
            ),
            HostInterface(
                name="eth0",
                type="VPNLink",
                dhcp=True,
                dhcpv6=True,
                ipv6_autoconf=True,
                untagged_vlan=None,
                mtu=1500,
            ),
            HostInterface(
                name="br0",
                type="bridge",
                address=ipaddress.IPv4Interface("10.3.0.1/23"),
                members=["eth1", "eth2"],
                _description="internal LAN",
            ),
            HostInterface(
                name="wgext",
                type="wireguard",
                address=ipaddress.IPv4Interface("172.22.255.3/31"),
                wireguard={
                    "port": 63666,
                    "private_key_secret": "tunnel-privkey",
                    "peers": [
                        {
                            "name": "gemini",
                            "public_key": "PUBKEY",
                            "endpoint": "192.0.2.9",
                            "keepalive": "10s",
                        },
                        {"name": "quiet", "public_key": "PUBKEY2"},
                    ],
                },
            ),
        ]
    )


VARIANTS = {
    "baseline": {},
    "no_site": {"site": None},
    "no_networks": {"networks": []},
    "no_links": {"__links__": []},
    "extra_config": {"extra_config": ["set system time-zone UTC"]},
    "snmp_location_override": {"snmp_location": "rack 42"},
    "interfaces_full": {"__host__": multi_interface_host},
    "ospf": {
        "ospf": {
            "networks": [{"network": "10.65.67.0/24", "area": 0}],
            "interfaces": [
                {"name": "eth1", "mtu_ignore": True},
                {"name": "eth2"},
            ],
            "redistribute": [
                {"protocol": "bgp", "metric_type": 2},
                {"protocol": "connected"},
            ],
        }
    },
    "static_routes": {
        "static_routes": [
            {"network": "10.9.0.0/24", "interface": "eth1"},
            {"network": "10.10.0.0/24", "next-hop": "10.65.67.1"},
        ]
    },
    "nat": {
        "nat": {
            "masquerade": [
                {"interface": "eth1", "source": "172.31.255.0/24"},
                {
                    "interface": "eth1",
                    "destinations": ["10.0.0.1/32", "10.0.0.2/32"],
                },
            ],
            "port_forwards": [
                {
                    "name": "web",
                    "interface": "eth1",
                    "protocols": ["tcp", "udp"],
                    "port": 443,
                    "to": "10.3.0.10",
                    "translation_port": 8443,
                }
            ],
        }
    },
    "firewall_groups": {
        "firewall": {
            "groups": {
                "address": {
                    "rfc1918": ["10.0.0.0/8"],
                    "mixed": [
                        {"address": "10.3.6.0/27", "comment": "internal"},
                        "2602:fa6d:10::/48",
                    ],
                },
                "interface": {
                    "plain": ["eth1"],
                    "nested": {"interfaces": ["br0"], "include": ["plain"]},
                },
            }
        }
    },
}


@pytest.mark.parametrize("variant", VARIANTS)
def test_blocks_match_template(variant):
    overrides = dict(VARIANTS[variant])
    host_factory = overrides.pop("__host__", None)
    forced_links = overrides.pop("__links__", None)
    device = host_factory() if host_factory else make_vyos(**overrides)
    links = forced_links if forced_links is not None else make_links(device)
    template, blocks = render_both_ways(device, links)
    assert blocks == template


def test_ipsec_link_matches_template():
    device = make_vyos()
    links = [*make_links(device), make_ipsec_link(device)]
    links[-1].ipsec = True
    links[-1].secret = "oracle-psk"
    template, blocks = render_both_ways(device, links)
    assert blocks == template


def test_ipv4_only_management_network_matches_template():
    device = make_vyos()
    links = make_links(device)
    template, blocks = render_both_ways(device, links)
    # The management /32 is v4 here, so it lands in ipv4-unicast.
    assert (
        "set protocols bgp address-family ipv4-unicast network"
        " 10.255.2.3/32 route-map TAG-SITE-ORIGIN" in blocks
    )
    assert blocks == template


def test_ipv6_management_network_matches_template():
    device = make_vyos(
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                ip6_address=ipaddress.IPv6Interface("2602:fa6d:f:aaaa::1/128"),
                management=True,
            )
        ]
    )
    links = make_links(device)
    template, blocks = render_both_ways(device, links)
    assert (
        "set protocols bgp address-family ipv6-unicast network"
        " 2602:fa6d:f:aaaa::1/128" in blocks
    )
    assert blocks == template
