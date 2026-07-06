"""RouterOS v7 CLI rendering for mikrotik hosts (vpn/mikrotik.j2).

Modeled on sea420-acc-v-hv2: numbered /31 WireGuard links to every
spine, eBGP per link, and geographic weighting filters. RouterOS
filter-rule syntax is rendered from current docs knowledge and must be
live-verified on the device before first deploy (VyOS lesson:
`match large-community` needed a probe too).
"""

import pytest
from conftest import COMMUNITY_ASN, FMT2, SEA, SITES, make_link

from barf.model.wireguard import WGNetworkLink
from barf.util.render import render_host_config
from barf.util.sites import BASE_LOCAL_PREF, haversine_km
from barf.vendors import BaseHost
from barf.vendors.mikrotik import MikroTikHost

pytestmark = pytest.mark.usefixtures("fake_keys", "fake_vault")


def make_mikrotik(**overrides) -> MikroTikHost:
    meta = {
        "hostname": "sea420-acc-v-hv2",
        "role": "vpn",
        "asn": 4280805533,
        "site": "sea",
        # Private management address: not is_global, so peers render no
        # endpoint toward us.
        "address": "10.3.0.1",
        "networks": ["10.36.0.0/24"],
    }
    meta.update(overrides)
    return MikroTikHost(**meta)


def make_fabric_links(device: BaseHost) -> list:
    # One spine per site; the fmt2 spine has a global address (we dial
    # it), the sea spine has none (nobody dials it in this fixture).
    spine_fmt2 = BaseHost(
        hostname="fmt2-vpn-spine-1",
        role="vpn",
        asn=4280805525,
        address="79.110.170.6",
        site="fmt2",
    )
    spine_sea = BaseHost(
        hostname="sea1-vpn-spine-1",
        role="vpn",
        asn=4280805529,
        site="sea",
    )
    return [
        make_link(51078, spine_fmt2, device, network="172.31.255.44/31"),
        make_link(51911, spine_sea, device, network="172.31.255.14/31"),
    ]


def render(device=None, links=None, sites=None) -> str:
    device = device or make_mikrotik()
    links = links if links is not None else make_fabric_links(device)
    sites = SITES if sites is None else sites
    return render_host_config(
        device,
        links,
        global_meta={"ssh_keys": [], "sites": sites, "community_asn": COMMUNITY_ASN},
        secrets={},
    )


class TestWireGuard:
    def test_interface_per_link(self):
        conf = render()
        assert (
            "/interface/wireguard add name=wg51078 listen-port=51078 mtu=1420" in conf
        )
        assert "/interface/wireguard add name=wg51911 listen-port=51911" in conf
        assert 'private-key="PRIV-' in conf

    def test_peer_with_endpoint_when_peer_is_globally_addressed(self):
        conf = render()
        assert (
            "/interface/wireguard/peers add interface=wg51078"
            " name=fmt2-vpn-spine-1" in conf
        )
        assert "endpoint-address=79.110.170.6 endpoint-port=51078" in conf
        assert "persistent-keepalive=10s" in conf

    def test_peer_without_endpoint_listens(self):
        conf = render()
        peer_line = next(
            line for line in conf.splitlines() if "name=sea1-vpn-spine-1" in line
        )
        assert "endpoint-address" not in peer_line
        assert "persistent-keepalive" not in peer_line

    def test_link_address_on_numbered_link(self):
        # Device is side_b, so it gets the second /31 address.
        conf = render()
        assert "/ip/address add address=172.31.255.45/31 interface=wg51078" in conf
        assert "/ip/address add address=172.31.255.15/31 interface=wg51911" in conf


class TestBGP:
    def test_template_and_connections(self):
        conf = render()
        assert (
            "/routing/bgp/template add name=genprog-fabric as=4280805533"
            " hold-time=30s keepalive-time=10s output.network=genprog-networks" in conf
        )
        assert (
            "/routing/bgp/connection add name=fmt2-vpn-spine-1"
            " templates=genprog-fabric remote.address=172.31.255.44"
            " remote.as=4280805525 local.address=172.31.255.45"
            " local.role=ebgp" in conf
        )

    def test_announced_networks_address_list(self):
        conf = render()
        assert (
            "/ip/firewall/address-list add list=genprog-networks"
            " address=10.36.0.0/24" in conf
        )

    def test_no_output_network_without_networks(self):
        conf = render(make_mikrotik(networks=[]))
        assert "output.network=" not in conf
        assert "/ip/firewall/address-list" not in conf


class TestGeographicWeighting:
    def test_origin_tag_rules_per_network(self):
        conf = render()
        assert (
            '/routing/filter/rule add chain=genprog-out comment="barf: origin-site'
            f' tag" rule="if (dst == 10.36.0.0/24) {{ set bgp-large-communities'
            f' {COMMUNITY_ASN}:1:{SEA.id}; accept }}"' in conf
        )
        assert '/routing/filter/rule add chain=genprog-out rule="accept"' in conf

    def test_input_chains_with_exact_local_prefs(self):
        conf = render()
        d = haversine_km(SEA.coords, FMT2.coords)
        # From the sea spine (base 0): origin sea = BASE.
        assert (
            f'chain=genprog-in-sea comment="barf: origin site {SEA.id}" rule="if'
            f" (bgp-large-communities includes {COMMUNITY_ASN}:1:{SEA.id})"
            f' {{ set bgp-local-pref {BASE_LOCAL_PREF}; accept }}"' in conf
        )
        # From the fmt2 spine (base d): origin sea = BASE - 2d.
        assert f"set bgp-local-pref {BASE_LOCAL_PREF - 2 * d}; accept" in conf
        assert (
            'chain=genprog-in-fmt2 comment="barf: untagged fallthrough"'
            ' rule="accept"' in conf
        )

    def test_connections_reference_the_filters(self):
        conf = render()
        fmt2_conn = next(
            line
            for line in conf.splitlines()
            if "name=fmt2-vpn-spine-1" in line and "/routing/bgp/connection" in line
        )
        assert "input.filter=genprog-in-fmt2" in fmt2_conn
        assert "output.filter-chain=genprog-out" in fmt2_conn

    def test_no_weighting_without_site(self):
        device = make_mikrotik(site=None)
        conf = render(device, make_fabric_links(device))
        assert "/routing/filter/rule" not in conf
        assert "input.filter" not in conf
        assert "output.filter-chain" not in conf
        assert "bgp-large-communities" not in conf


TRANSIT = {
    "transit": [
        {
            "name": "linuxgemini",
            "remote_as": 4280806675,
            "link_network": "172.22.255.2/31",
        },
    ]
}


class TestTransit:
    """Modeled hand-written transit (sea420's linuxgemini `export` chain)."""

    def _render(self):
        device = make_mikrotik(bgp=TRANSIT)
        return render(device, make_fabric_links(device))

    def test_reflects_transit_as_into_the_fabric(self):
        conf = self._render()
        assert (
            'rule="if (dst-len != 0 && bgp-input-remote-as == 4280806675)'
            ' { accept }"' in conf
        )

    def test_link_network_announced_and_tagged_like_own_networks(self):
        conf = self._render()
        assert (
            "/ip/firewall/address-list add list=genprog-networks"
            " address=172.22.255.2/31" in conf
        )
        assert (
            f'rule="if (dst == 172.22.255.2/31) {{ set bgp-large-communities'
            f' {COMMUNITY_ASN}:1:{SEA.id}; accept }}"' in conf
        )

    def test_transit_flips_export_posture_to_reject(self):
        conf = self._render()
        assert (
            '/routing/filter/rule add chain=genprog-out comment="barf: no other'
            ' re-export" rule="reject"' in conf
        )
        assert 'chain=genprog-out rule="accept"' not in conf

    def test_without_transit_export_stays_accept_all(self):
        conf = render()
        assert 'chain=genprog-out rule="accept"' in conf
        assert 'rule="reject"' not in conf
        assert "bgp-input-remote-as" not in conf


class TestBGPInstance:
    def test_connections_name_the_instance_when_modeled(self):
        # RouterOS boxes with a custom-named (non-default) BGP instance
        # interactively prompt on `connection add` without instance=,
        # hanging a scripted deploy (live-learned on sea420, 7.22.3).
        device = make_mikrotik(bgp={"instance": "bgp-instance-1"})
        conf = render(device, make_fabric_links(device))
        for line in conf.splitlines():
            if "/routing/bgp/connection add" in line:
                assert "instance=bgp-instance-1" in line

    def test_no_instance_by_default(self):
        assert "instance=" not in render()


class TestUnnumbered:
    """RFC 5549 links: link-local BGP via ND, no /31s.

    Recipe live-verified against fmt2-vpn-spine-1 (2026-07-05): the
    session establishes in seconds, but only exchanges prefixes with
    afi=ip,ipv6 -- RouterOS's default afi=ip negotiates and then
    silently moves nothing.
    """

    def _render(self):
        device = make_mikrotik()
        spine = BaseHost(
            hostname="sea1-vpn-spine-1", role="vpn", asn=4280805529, site="sea"
        )
        return render(device, [make_link(51911, spine, device)])

    def test_nd_enabled_per_unnumbered_interface(self):
        conf = self._render()
        nd_line = next(
            line for line in conf.splitlines() if line.startswith("/ipv6/nd add")
        )
        assert nd_line == "/ipv6/nd add interface=wg51911 ra-interval=10s-15s"
        # /ipv6/nd silently discards comment= (live-verified): rendering
        # one would read as a forever-pending diff.
        assert "comment" not in nd_line
        assert "/ipv6/nd/prefix add prefix=none interface=wg51911" in conf

    def test_connection_is_interface_local_with_both_afis(self):
        conf = self._render()
        conn = next(
            line for line in conf.splitlines() if "/routing/bgp/connection add" in line
        )
        assert "local.address=wg51911" in conn
        assert "remote.address" not in conn
        assert "remote.as=4280805529" in conn
        assert "afi=ip,ipv6" in conn
        assert "input.filter=genprog-in-sea" in conn

    def test_no_link_address_and_numbered_links_unaffected(self):
        conf = self._render()
        assert "/ip/address add" not in conf
        # Numbered links keep the classic form with no afi override.
        numbered = render()
        assert "afi=" not in numbered
        assert "/ipv6/nd" not in numbered


class TestUnsupportedLinks:
    def test_ipsec_link_fails_fast(self):
        device = make_mikrotik()
        peer = BaseHost(hostname="oracle-x", role="vpn", asn=31898)
        link = WGNetworkLink(
            link_id=51831,
            side_a=peer,
            side_b=device,
            network="172.31.255.20/31",
            secret="psk",
            ipsec=True,
            pinned=True,
        )
        with pytest.raises(ValueError, match="ipsec link"):
            render(device, [link])
