"""Template-vs-blocks equivalence for the mikrotik port.

The golden snapshot pins sea420's exact feature combo; this matrix
pins the template's semantics for the combos sea420 doesn't exercise
(numbered links, no site, empty sections, ...). Renders every variant
through BOTH the retired-in-place Jinja template and the block
renderer and requires byte-identical output. Delete together with
vpn/mikrotik.j2 once the template is removed.
"""

import ipaddress

import pytest
from conftest import COMMUNITY_ASN, SITES
from test_mikrotik_render import TRANSIT, make_fabric_links, make_mikrotik

from barf.common import render_template
from barf.configs import build_context, render_blocks
from barf.util.sites import SITE_ORIGIN_FUNC
from barf.vendors import HostInterface

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")

GLOBAL_META = {"ssh_keys": [], "sites": SITES, "community_asn": COMMUNITY_ASN}


def render_both_ways(device, links) -> tuple:
    # Both paths read host secrets (static WG private keys); serve a
    # deterministic fake so they compare equal.
    device.secret = lambda key, *args, **kwargs: f"SECRET-{key}"
    ctx = build_context(device, links, GLOBAL_META, secrets={})
    template = render_template(
        "vpn/mikrotik.j2",
        device=device,
        secrets={},
        global_meta=GLOBAL_META,
        vpn_links=ctx.vpn_links,
        site_import_rules=ctx.site_import_rules,
        origin_site=ctx.origin_site,
        community_asn=ctx.community_asn,
        SITE_ORIGIN_FUNC=SITE_ORIGIN_FUNC,
    )
    return template, render_blocks(ctx)


def bridge(name: str, **overrides) -> HostInterface:
    fields = {
        "name": name,
        "type": "bridge",
        "address": ipaddress.IPv4Interface("10.3.0.1/23"),
    }
    fields.update(overrides)
    return HostInterface(**fields)


def static_wg(mtu=None, **wg_overrides) -> HostInterface:
    wg = {
        "port": 63666,
        "private_key_secret": "tunnel-privkey",
        "peers": [{"name": "gemini", "public_key": "PUBKEY"}],
    }
    wg.update(wg_overrides)
    return HostInterface(
        name="wgext",
        type="wireguard",
        address=ipaddress.IPv4Interface("172.22.255.3/31"),
        mtu=mtu,
        wireguard=wg,
    )


VARIANTS = {
    "baseline_numbered_links": {},
    "no_site": {"site": None},
    "no_networks": {"networks": []},
    "no_links": {"__links__": []},
    "extra_config": {"extra_config": ["/system/note set note=hi", "/log info x"]},
    "bgp_instance": {"bgp": {"instance": "bgp-instance-1"}},
    "transit": {"bgp": TRANSIT, "networks": ["10.36.0.0/24", "10.37.0.0/24"]},
    "transit_without_site": {"bgp": TRANSIT, "site": None},
    "bare_bridge": {"interfaces": [bridge("br0", address=None, addresses=[])]},
    "bridge_full": {
        "interfaces": [
            bridge(
                "br0",
                _description="internal LAN",
                members=["ether1", "ether2"],
                ra={"hop_limit": 255, "advertise_dns": True},
            )
        ]
    },
    "bridge_ra_no_hop_limit": {
        "interfaces": [bridge("br0", ra={"advertise_dns": False})]
    },
    "static_wg_minimal_peer": {"interfaces": [static_wg()]},
    "static_wg_full_peer": {
        "interfaces": [
            static_wg(
                peers=[
                    {
                        "name": "gemini",
                        "public_key": "PUBKEY",
                        "endpoint": "192.0.2.9",
                        "port": 63667,
                        "keepalive": "25s",
                        "allowed_ips": ["10.0.0.0/8"],
                    }
                ]
            )
        ]
    },
    "static_wg_mtu": {"interfaces": [static_wg(mtu=9000)]},
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
                    "plain": ["ether1"],
                    "nested": {"interfaces": ["br0"], "include": ["plain"]},
                },
            }
        }
    },
}


@pytest.mark.parametrize("variant", VARIANTS)
def test_blocks_match_template(variant):
    overrides = dict(VARIANTS[variant])
    forced_links = overrides.pop("__links__", None)
    device = make_mikrotik(**overrides)
    links = forced_links if forced_links is not None else make_fabric_links(device)
    template, blocks = render_both_ways(device, links)
    assert blocks == template


def test_unnumbered_links_match_template():
    device = make_mikrotik()
    links = make_fabric_links(device)
    for link in links:
        link.network = None
    template, blocks = render_both_ways(device, links)
    assert blocks == template
