"""Template-vs-blocks equivalence for the linux port.

The golden pins sea21-hv-egg-irl (unnumbered links, weighting,
local_conf, v4+v6 networks); this matrix pins the template semantics
for the combos it doesn't exercise. Renders every variant through
BOTH the retired-in-place Jinja template (vpn/linux.j2 + its includes)
and the block renderer and requires byte-identical output. Delete
together with the templates.
"""

import ipaddress

import pytest
from conftest import COMMUNITY_ASN, SITES, make_link

from barf.common import render_template
from barf.configs import build_context, render_blocks
from barf.util.sites import SITE_ORIGIN_FUNC
from barf.vendors import HostInterface
from barf.vendors.linux import LinuxBirdHost
from barf.vendors.vyos import VyOSHost

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")

GLOBAL_META = {"ssh_keys": [], "sites": SITES, "community_asn": COMMUNITY_ASN}

LOCAL_CONF = "# hand config\nfunction is_blacklisted()\n{\n\treturn false;\n}\n"


def make_linux(**overrides) -> LinuxBirdHost:
    meta = {
        "hostname": "test-hv-1",
        "role": "vpn",
        "asn": 4280805528,
        "site": "sea",
        "host_id": 6,
        "networks": ["192.168.3.0/24", "2602:fa6d:f:aaaa::f06/128"],
        "interfaces": [
            HostInterface(
                name="dum0",
                type="VPNLink",
                _description="underlay loopback",
                addresses=[ipaddress.IPv6Interface("2602:fa6d:f:aaaa::f06/128")],
                management=True,
            )
        ],
    }
    meta.update(overrides)
    return LinuxBirdHost(**meta)


def make_links(device, numbered: bool = False) -> list:
    spine_fmt2 = VyOSHost(
        hostname="fmt2-vpn-spine-1", role="vpn", asn=4280805525, site="fmt2"
    )
    spine_fmt2.ip6_address = "2a0d:1a43:8008:420::1"
    spine_sea = VyOSHost(
        hostname="sea1-vpn-spine-1", role="vpn", asn=4280805529, site="sea"
    )
    network = "172.31.255.44/31" if numbered else None
    return [
        make_link(51070, spine_fmt2, device, network=network),
        make_link(51399, spine_sea, device),
    ]


def render_both_ways(device, links) -> tuple:
    ctx = build_context(device, links, GLOBAL_META, secrets={})
    template = render_template(
        "vpn/linux.j2",
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


VARIANTS = {
    "baseline_unnumbered": {},
    "no_site": {"site": None},
    "no_networks": {"networks": []},
    "v4_only_networks": {"networks": ["192.168.3.0/24"]},
    "no_links": {"__links__": []},
    "extra_config": {"extra_config": ["echo done", "true"]},
    "local_conf": {"bird": {"local_conf": LOCAL_CONF}},
    "bird_knobs": {
        "bird": {
            "router_id": "192.168.3.1",
            "krt_prefsrc": "192.168.3.1",
            "merge_paths": True,
            "import_check_function": "is_blacklisted",
            "local_conf": LOCAL_CONF,
        }
    },
    "import_filter_no_weighting": {
        "site": None,
        "bird": {"import_filter": "my_filter"},
    },
    "no_dummies": {
        "interfaces": [
            HostInterface(
                name="eth0",
                type="VPNLink",
                address=ipaddress.IPv4Interface("192.168.3.1/24"),
                management=True,
            )
        ]
    },
    "multi_address_dummy": {
        "interfaces": [
            HostInterface(
                name="dum0",
                type="VPNLink",
                addresses=[
                    ipaddress.IPv4Interface("10.99.0.1/32"),
                    ipaddress.IPv6Interface("2602:fa6d:f:aaaa::f06/128"),
                ],
                management=True,
            )
        ]
    },
}


@pytest.mark.parametrize("variant", VARIANTS)
def test_blocks_match_template(variant):
    overrides = dict(VARIANTS[variant])
    forced_links = overrides.pop("__links__", None)
    device = make_linux(**overrides)
    links = forced_links if forced_links is not None else make_links(device)
    template, blocks = render_both_ways(device, links)
    assert blocks == template


def test_numbered_links_match_template():
    # Numbered links flip the ifupdown stanza to inet static and derive
    # the router id from the first numbered link when bird.router_id is
    # unset.
    device = make_linux()
    links = make_links(device, numbered=True)
    template, blocks = render_both_ways(device, links)
    assert "iface wg51070 inet static" in blocks
    assert "router id 172.31.255.45;" in blocks
    assert blocks == template


def test_router_id_falls_back_without_numbered_links():
    device = make_linux()
    template, blocks = render_both_ways(device, make_links(device))
    assert "router id 192.0.2.1;" in blocks
    assert blocks == template
