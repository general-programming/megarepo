import ipaddress

import pytest
from conftest import COMMUNITY_ASN, FMT2, SEA, SITES, make_link

from barf.model.wireguard import WGNetworkLink
from barf.util.render import render_host_config
from barf.util.sites import BASE_LOCAL_PREF, haversine_km
from barf.vendors import HostInterface
from barf.vendors.external import ExternalHost
from barf.vendors.vyos import VyOSHost

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")


def make_router(hostname: str, asn: int, site=None, networks=None) -> VyOSHost:
    return VyOSHost(
        hostname=hostname,
        role="vpn",
        asn=asn,
        site=site,
        networks=networks or [],
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                address=ipaddress.IPv4Interface(f"10.0.{asn % 256}.1/32"),
                management=True,
            )
        ],
    )


def render(device, links, sites=None) -> str:
    sites = SITES if sites is None else sites
    return render_host_config(
        device,
        links,
        global_meta={"ssh_keys": [], "sites": sites, "community_asn": COMMUNITY_ASN},
        secrets={"vyos_api_password": "x"},
    )


class TestOriginTagging:
    def test_tag_route_map_defined_and_attached_when_site_set(self):
        device = make_router("leaf-sea", 300, site="sea", networks=["10.9.0.0/24"])
        conf = render(device, [])
        # `add` keyword and the `large-community large-community-list`
        # match node verified against a live VyOS 1.4 API commit
        # (2026-07-05); the shorter forms 400.
        assert (
            "set policy route-map TAG-SITE-ORIGIN rule 10 set large-community"
            f" add '{COMMUNITY_ASN}:1:1'" in conf
        )
        assert (
            "set protocols bgp address-family ipv4-unicast network 10.9.0.0/24"
            " route-map TAG-SITE-ORIGIN" in conf
        )
        # Management prefix is tagged too.
        assert (
            "set protocols bgp address-family ipv4-unicast network 10.0.44.1/32"
            " route-map TAG-SITE-ORIGIN" in conf
        )

    def test_no_tag_without_a_site(self):
        device = make_router("leaf-none", 301, site=None, networks=["10.9.0.0/24"])
        conf = render(device, [])
        assert "TAG-SITE-ORIGIN" not in conf


class TestImportWeighting:
    def _fabric(self):
        leaf = make_router("leaf-sea", 300, site="sea")
        spine_fmt2 = make_router("spine-fmt2", 100, site="fmt2")
        spine_sea = make_router("spine-sea", 200, site="sea")
        links = [
            make_link(51070, spine_fmt2, leaf),
            make_link(51134, spine_sea, leaf),
        ]
        return leaf, links

    def test_large_community_lists_defined_for_every_known_site(self):
        leaf, links = self._fabric()
        conf = render(leaf, links)
        assert (
            "set policy large-community-list SITE-ORIGIN-1 rule 10 regex"
            f" '^{COMMUNITY_ASN}:1:1$'" in conf
        )
        assert (
            "set policy large-community-list SITE-ORIGIN-2 rule 10 regex"
            f" '^{COMMUNITY_ASN}:1:2$'" in conf
        )
        # The match node is `match large-community large-community-list`
        # (live-verified); the shorter `match large-community-list` 400s.
        assert "match large-community large-community-list SITE-ORIGIN-1" in conf

    def test_import_route_map_exact_local_prefs(self):
        leaf, links = self._fabric()
        conf = render(leaf, links)
        d = haversine_km(SEA.coords, FMT2.coords)

        # Heard from spine-fmt2 (base = dist(sea, fmt2) = d).
        assert (
            f"set policy route-map IMPORT-FMT2 rule 10 set local-preference {BASE_LOCAL_PREF - 2 * d}"
            in conf
        )
        assert (
            f"set policy route-map IMPORT-FMT2 rule 20 set local-preference {BASE_LOCAL_PREF - d}"
            in conf
        )
        # Heard from spine-sea (base = dist(sea, sea) = 0).
        assert (
            f"set policy route-map IMPORT-SEA rule 10 set local-preference {BASE_LOCAL_PREF}"
            in conf
        )
        assert (
            f"set policy route-map IMPORT-SEA rule 20 set local-preference {BASE_LOCAL_PREF - d}"
            in conf
        )
        # Trailing catch-all: untagged routes fall through unweighted.
        assert "set policy route-map IMPORT-FMT2 rule 30 action permit" in conf
        assert "IMPORT-FMT2 rule 30 set local-preference" not in conf

    def test_import_route_map_attached_to_unnumbered_neighbor(self):
        leaf, links = self._fabric()
        conf = render(leaf, links)
        # address-family sits directly under the neighbor, never under
        # the `interface v6only` node (matches the eth0.5 extra_config
        # precedent in network.yml).
        assert (
            "set protocols bgp neighbor wg51070 address-family"
            " ipv4-unicast route-map import IMPORT-FMT2" in conf
        )
        assert (
            "set protocols bgp neighbor wg51070 address-family"
            " ipv6-unicast route-map import IMPORT-FMT2" in conf
        )
        assert (
            "set protocols bgp neighbor wg51134 address-family"
            " ipv4-unicast route-map import IMPORT-SEA" in conf
        )
        assert "v6only address-family" not in conf

    def test_import_route_map_attached_to_numbered_neighbor(self):
        leaf = make_router("leaf-sea", 300, site="sea")
        spine_fmt2 = make_router("spine-fmt2", 100, site="fmt2")
        link = make_link(51070, spine_fmt2, leaf, network="172.31.255.8/31")
        conf = render(leaf, [link])
        # other_peer (spine_fmt2) is side_a of the link, so it gets the
        # link's first address.
        assert (
            "set protocols bgp neighbor 172.31.255.8 address-family ipv4-unicast"
            " route-map import IMPORT-FMT2" in conf
        )
        assert (
            "set protocols bgp neighbor 172.31.255.8 address-family ipv6-unicast"
            " route-map import IMPORT-FMT2" in conf
        )

    def test_no_import_weighting_toward_external_peers(self):
        leaf = make_router("leaf-sea", 300, site="sea")
        oracle = ExternalHost(
            hostname="oracle-1", role="vpn", asn=31898, site="fmt2", address="9.9.9.9"
        )
        link = WGNetworkLink(
            link_id=51831,
            side_a=oracle,
            side_b=leaf,
            network="172.31.255.20/31",
            secret="oracle-psk",
            ipsec=False,
            pinned=True,
        )
        conf = render(leaf, [link])
        assert "route-map import" not in conf

    def test_no_weighting_at_all_when_device_has_no_site(self):
        leaf = make_router("leaf-none", 301, site=None)
        spine_fmt2 = make_router("spine-fmt2", 100, site="fmt2")
        link = make_link(51070, spine_fmt2, leaf)
        conf = render(leaf, [link])
        assert "route-map import" not in conf
        assert "large-community-list" not in conf


class TestBridges:
    """The same vendor-neutral bridge interface renders as VyOS."""

    def _router(self):
        router = make_router("sea-lan-1", 4280805540, site="sea")
        router.interfaces.append(
            HostInterface(
                name="br0",
                type="bridge",
                _description="internal LAN",
                address=ipaddress.IPv4Interface("10.3.0.1/23"),
                members=["eth1", "eth2"],
            )
        )
        return router

    def test_bridge_address_and_description(self):
        conf = render(self._router(), [])
        # Interface addresses render unquoted (as the fleet has them);
        # descriptions are quoted.
        assert "set interfaces bridge br0 address 10.3.0.1/23" in conf
        assert "set interfaces bridge br0 description 'internal LAN'" in conf

    def test_bridge_members(self):
        conf = render(self._router(), [])
        assert "set interfaces bridge br0 member interface eth1" in conf
        assert "set interfaces bridge br0 member interface eth2" in conf
