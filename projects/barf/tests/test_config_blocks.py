"""The ConfigBlock contract, block renderer, and vendor line builders."""

import ipaddress

import pytest
from conftest import COMMUNITY_ASN, SITES, make_link

import barf.configs as configs
from barf.configs import (
    ConfigBlock,
    RenderContext,
    UnsupportedFeature,
    build_context,
    render_blocks,
)
from barf.configs.lines import ros_kv, ros_line, squote, vyos_set
from barf.model.wireguard import WGNetworkLink
from barf.vendors import HostInterface
from barf.vendors.linux import LinuxBirdHost
from barf.vendors.mikrotik import MikroTikHost
from barf.vendors.vyos import VyOSHost

pytestmark = pytest.mark.usefixtures("fake_vault")

GLOBAL_META = {"ssh_keys": [], "sites": SITES, "community_asn": COMMUNITY_ASN}


def make_router(cls, hostname: str, asn: int, site=None, **kwargs):
    return cls(
        hostname=hostname,
        role="vpn",
        asn=asn,
        site=site,
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                address=ipaddress.IPv4Interface(f"10.0.{asn % 256}.1/32"),
                management=True,
            )
        ],
        **kwargs,
    )


def make_ctx(host, **overrides) -> RenderContext:
    ctx = RenderContext(host=host, global_meta=GLOBAL_META, secrets={})
    for key, value in overrides.items():
        setattr(ctx, key, value)
    return ctx


class Greeting(ConfigBlock):
    def vyos(self):
        return [f"set greeting '{self.host.hostname}'"]

    def mikrotik(self):
        return [f"/greeting add name={self.host.hostname}"]


class Optional(ConfigBlock):
    def applies(self):
        return bool(self.host.networks)

    def vyos(self):
        return ["set optional"]


class VyosOnly(ConfigBlock):
    def vyos(self):
        return ["set vyos-only"]


class TestConfigBlock:
    def test_emit_dispatches_by_devicetype(self):
        host = make_router(VyOSHost, "leaf-1", 300)
        block = Greeting(make_ctx(host))
        assert block.emit("vyos") == ["set greeting 'leaf-1'"]
        assert block.emit("mikrotik") == ["/greeting add name=leaf-1"]

    def test_missing_vendor_method_fails_loudly(self):
        host = make_router(MikroTikHost, "sea420", 4280805525)
        with pytest.raises(UnsupportedFeature) as excinfo:
            VyosOnly(make_ctx(host)).emit("mikrotik")
        assert "sea420" in str(excinfo.value)
        assert "VyosOnly" in str(excinfo.value)
        # UnsupportedFeature stays a NotImplementedError so existing
        # vendor-capability handling keeps catching it.
        assert isinstance(excinfo.value, NotImplementedError)


class TestRenderBlocks:
    def test_registry_order_is_output_order(self, monkeypatch):
        monkeypatch.setitem(
            configs.BLOCK_REGISTRY, ("vpn", "vyos"), [Greeting, VyosOnly]
        )
        host = make_router(VyOSHost, "leaf-1", 300)
        assert render_blocks(make_ctx(host)) == (
            "set greeting 'leaf-1'\nset vyos-only\n"
        )

    def test_inactive_block_is_skipped_silently(self, monkeypatch):
        # Optional has no mikrotik() emitter, but applies() is False for
        # a host without networks: skipped, never UnsupportedFeature.
        monkeypatch.setitem(
            configs.BLOCK_REGISTRY, ("vpn", "mikrotik"), [Greeting, Optional]
        )
        host = make_router(MikroTikHost, "sea420", 4280805525)
        assert render_blocks(make_ctx(host)) == "/greeting add name=sea420\n"

    def test_active_block_without_vendor_method_raises(self, monkeypatch):
        monkeypatch.setitem(configs.BLOCK_REGISTRY, ("vpn", "mikrotik"), [Optional])
        host = make_router(MikroTikHost, "sea420", 4280805525, networks=["10.0.0.0/24"])
        with pytest.raises(UnsupportedFeature):
            render_blocks(make_ctx(host))


class TestBuildContext:
    def _fabric(self):
        leaf = make_router(VyOSHost, "leaf-sea", 300, site="sea")
        spine = make_router(VyOSHost, "spine-fmt2", 100, site="fmt2")
        other = make_router(VyOSHost, "leaf-other", 301, site="sea")
        links = [
            make_link(51070, spine, leaf),
            make_link(51071, spine, other),
        ]
        return leaf, links

    def test_selects_only_own_links_and_weighting(self):
        leaf, links = self._fabric()
        ctx = build_context(leaf, links, GLOBAL_META, secrets={})
        assert [link.link_id for link in ctx.vpn_links] == [51070]
        assert ctx.origin_site is SITES["sea"]
        assert ctx.community_asn == COMMUNITY_ASN
        assert set(ctx.site_import_rules) == {"fmt2"}

    def test_non_vpn_role_gets_bare_context(self):
        host = VyOSHost(hostname="switch-1", role="network_devices", asn=None)
        ctx = build_context(host, [], GLOBAL_META, secrets={})
        assert ctx.vpn_links == []
        assert ctx.site_import_rules == {}
        assert ctx.origin_site is None
        assert ctx.community_asn is None

    def test_bird_import_filter_with_weighting_fails_fast(self):
        leaf, links = self._fabric()
        linux = make_router(
            LinuxBirdHost,
            "hv-1",
            302,
            site="sea",
            bird={"import_filter": "mine"},
        )
        links = [make_link(51072, leaf, linux)]
        with pytest.raises(ValueError, match="import_filter cannot be combined"):
            build_context(linux, links, GLOBAL_META, secrets={})

    def test_mikrotik_ipsec_link_fails_fast(self):
        router = make_router(MikroTikHost, "sea420", 4280805525)
        spine = make_router(VyOSHost, "spine-1", 100)
        link = WGNetworkLink(
            link_id=51831,
            side_a=spine,
            side_b=router,
            network="172.31.255.20/31",
            secret="oracle-psk",
            ipsec=True,
            pinned=True,
        )
        with pytest.raises(ValueError, match="not supported on mikrotik"):
            build_context(router, [link], GLOBAL_META, secrets={})


class TestRosLines:
    def test_kv_plain_and_quoted(self):
        assert ros_kv("name", "wg51078") == "name=wg51078"
        assert ros_kv("comment", "barf: a -> b", quote=True) == (
            'comment="barf: a -> b"'
        )

    def test_kv_none_vanishes_but_zero_renders(self):
        assert ros_kv("port", None) == ""
        assert ros_kv("port", 0) == "port=0"

    def test_line_drops_empty_pairs(self):
        line = ros_line(
            "/interface/wireguard",
            "add",
            ros_kv("name", "wg51078"),
            ros_kv("comment", None, quote=True),
            ros_kv("mtu", 1420),
        )
        assert line == "/interface/wireguard add name=wg51078 mtu=1420"


class TestVyosLines:
    def test_set_joins_tokens_and_drops_none(self):
        interface = None
        assert vyos_set("interfaces", "loopback", "lo", interface) == (
            "set interfaces loopback lo"
        )

    def test_squote(self):
        assert vyos_set("system", "host-name", squote("leaf-1")) == (
            "set system host-name 'leaf-1'"
        )
