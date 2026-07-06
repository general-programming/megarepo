import ipaddress

import pytest
from conftest import COMMUNITY_ASN, FMT2, SEA, SITES, make_link

from barf.util.render import render_host_config
from barf.util.sites import BASE_LOCAL_PREF, haversine_km
from barf.vendors import BaseHost, HostInterface
from barf.vendors.linux import LinuxBirdHost, parse_rendered_files

pytestmark = pytest.mark.usefixtures("fake_keys")


def make_leaf(**overrides) -> LinuxBirdHost:
    meta = {
        "hostname": "leaf-x",
        "role": "vpn",
        "asn": 65010,
        "host_id": 6,
        "networks": ["192.168.3.0/24", "2001:db8:3::/64"],
        "bird": {
            "router_id": "192.168.3.1",
            "merge_paths": True,
            "krt_prefsrc": "192.168.3.1",
            "import_filter": "imp_fil",
        },
    }
    meta.update(overrides)
    return LinuxBirdHost(**meta)


def make_links(leaf: BaseHost, unnumbered: bool = True) -> list:
    spine1 = BaseHost(
        hostname="fmt2-vpn-spine-1", role="vpn", asn=65000, address="1.1.1.1"
    )
    spine2 = BaseHost(
        hostname="fmt2-vpn-spine-2", role="vpn", asn=65000, address="1.0.0.1"
    )
    networks = [None, None] if unnumbered else ["172.31.255.8/31", "172.31.255.10/31"]
    return [
        make_link(51070, spine1, leaf, network=networks[0]),
        make_link(51134, spine2, leaf, network=networks[1]),
    ]


def make_sited_links(leaf: BaseHost, spine1_site=None, spine2_site=None) -> list:
    """Two unnumbered spine links, spines optionally carrying a `site`."""
    spine1 = BaseHost(
        hostname="fmt2-vpn-spine-1",
        role="vpn",
        asn=65000,
        address="1.1.1.1",
        site=spine1_site,
    )
    spine2 = BaseHost(
        hostname="sea1-vpn-spine-1",
        role="vpn",
        asn=65001,
        address="1.0.0.1",
        site=spine2_site,
    )
    return [
        make_link(51070, spine1, leaf),
        make_link(51134, spine2, leaf),
    ]


def render_leaf(leaf=None, links=None, sites=None) -> str:
    leaf = leaf or make_leaf()
    links = links if links is not None else make_links(leaf)
    sites = sites if sites is not None else SITES
    return render_host_config(
        leaf,
        links,
        global_meta={"ssh_keys": [], "sites": sites, "community_asn": COMMUNITY_ASN},
        secrets={},
    )


def render_files(leaf=None, links=None) -> dict:
    return parse_rendered_files(render_leaf(leaf, links))


def test_rendered_script_parses_into_owned_files():
    files = render_files()
    assert set(files) == {
        "/etc/wireguard/wg51070.conf",
        "/etc/wireguard/wg51134.conf",
        "/etc/network/interfaces.d/wg51070.conf",
        "/etc/network/interfaces.d/wg51134.conf",
        "/etc/bird/bird.conf",
        "/etc/bird/conf.d/genprog.conf",
    }
    # Every file ends with a newline: they are written verbatim.
    assert all(content.endswith("\n") for content in files.values())


def test_wireguard_conf():
    wg = render_files()["/etc/wireguard/wg51070.conf"]
    assert "ListenPort = 51070" in wg
    # Unnumbered BGP runs over link-local v6; ::/0 must be allowed.
    assert "AllowedIPs = 0.0.0.0/0, ::/0" in wg
    assert "PrivateKey = PRIV-wglink-fmt2-vpn-spine-1--leaf-x-leaf-x" in wg
    assert "Endpoint = 1.1.1.1:51070" in wg
    # Private key material gets a restrictive mode in the script too.
    assert "chmod 600 /etc/wireguard/wg51070.conf" in render_leaf()


def test_ifupdown_unnumbered():
    conf = render_files()["/etc/network/interfaces.d/wg51070.conf"]
    # inet static without an address refuses to parse; manual form.
    assert "iface wg51070 inet manual" in conf
    # manual applies no mtu and does not up the link: both explicit.
    assert "pre-up ip link add $IFACE mtu 1420 type wireguard" in conf
    assert "post-up ip link set $IFACE up" in conf
    # The deterministic link-local the BGP session runs on.
    assert "post-up ip addr add fe80::6/64 dev $IFACE" in conf
    # No stanza options that require/imply a global address.
    assert "        address " not in conf
    assert "netmask" not in conf


def test_ifupdown_numbered():
    leaf = make_leaf()
    conf = render_files(leaf, make_links(leaf, unnumbered=False))[
        "/etc/network/interfaces.d/wg51070.conf"
    ]
    assert "iface wg51070 inet static" in conf
    assert "address 172.31.255.9" in conf
    assert "netmask 255.255.255.254" in conf
    assert "fe80::" not in conf


def test_bird_conf_base():
    conf = render_files()["/etc/bird/bird.conf"]
    assert "router id 192.168.3.1;" in conf
    assert "merge paths on;" in conf
    assert "krt_prefsrc = 192.168.3.1;" in conf
    # Static announce anchors must never hit the kernel.
    assert "if source = RTS_STATIC then reject;" in conf
    # Dual-stack kernel protocols + the drop-in include.
    assert conf.count("protocol kernel {") == 2
    assert 'include "/etc/bird/conf.d/*.conf";' in conf


def test_bird_conf_defaults_without_meta():
    leaf = make_leaf(bird={})
    links = make_links(leaf, unnumbered=False)
    conf = parse_rendered_files(render_leaf(leaf, links))["/etc/bird/bird.conf"]
    # No bird meta: router id falls back to the first numbered link IP.
    assert "router id 172.31.255.9;" in conf
    assert "merge paths" not in conf
    assert "krt_prefsrc" not in conf


def test_genprog_conf_unnumbered():
    conf = render_files()["/etc/bird/conf.d/genprog.conf"]
    assert "define GENPROG_ASN = 65010;" in conf
    # Originate anchors, split per family.
    assert "route 192.168.3.0/24 reject;" in conf
    assert "route 2001:db8:3::/64 reject;" in conf
    # The host's custom import filter is honored on both channels of
    # the shared session template.
    assert conf.count("import filter imp_fil;") == 2
    # One dynamic link-local session per spine.
    assert 'interface "wg51070";' in conf
    assert 'interface "wg51134";' in conf
    assert conf.count("neighbor range fe80::/10 as 65000;") == 2
    assert 'dynamic name "fmt2_vpn_spine_1_";' in conf
    # v4-over-v6 next hops (RFC 5549).
    assert "extended next hop on;" in conf
    # The RA beacon FRR needs for interface-mode discovery.
    assert 'interface "wg51070", "wg51134" {' in conf
    assert "default lifetime 0;" in conf


def test_dummy_interface():
    leaf = make_leaf(
        interfaces=[
            HostInterface(
                name="dum0",
                type="VPNLink",
                _description="underlay loopback",
                addresses=[ipaddress.ip_interface("2001:db8:aaaa::6/128")],
                management=True,
            )
        ]
    )
    files = render_files(leaf)
    conf = files["/etc/network/interfaces.d/dum0.conf"]
    # The VyOS dum0 pattern: a stable identity on a dummy interface.
    assert "pre-up ip link add $IFACE type dummy" in conf
    assert "post-up ip addr add 2001:db8:aaaa::6/128 dev $IFACE" in conf
    assert "post-up ip link set $IFACE up" in conf


def test_no_dummy_without_interfaces():
    assert not [p for p in render_files() if "dum" in p]


def test_bird_local_conf():
    leaf = make_leaf(
        bird={"import_filter": "imp_fil", "local_conf": "filter imp_fil { accept; }\n"}
    )
    files = render_files(leaf)
    # The host's hand-written config deploys as the first drop-in so
    # its definitions parse before genprog.conf references them.
    assert files["/etc/bird/conf.d/00-local.conf"] == "filter imp_fil { accept; }\n"


def test_no_local_conf_no_file():
    assert "/etc/bird/conf.d/00-local.conf" not in render_files()


def test_genprog_conf_numbered():
    leaf = make_leaf(bird={})
    links = make_links(leaf, unnumbered=False)
    conf = parse_rendered_files(render_leaf(leaf, links))[
        "/etc/bird/conf.d/genprog.conf"
    ]
    assert "source address 172.31.255.9;" in conf
    assert "neighbor 172.31.255.8 as 65000;" in conf
    # No import filter configured: import everything.
    assert "import all;" in conf
    # Numbered-only hosts advertise no RAs.
    assert "protocol radv" not in conf


class TestGeographicWeighting:
    """Large-community origin tagging + import local-pref (barf/util/sites.py)."""

    def test_hosts_without_site_render_unchanged(self):
        # No site anywhere: no tag, no generated import filters -- byte
        # for byte the same as the pre-weighting behavior asserted by
        # the tests above.
        conf = render_files()["/etc/bird/conf.d/genprog.conf"]
        assert "bgp_large_community" not in conf
        assert "filter genprog_import_" not in conf
        assert conf.count("import filter imp_fil;") == 2

    def test_no_tag_when_device_has_no_site(self):
        # A device with no site has nothing to weight against either:
        # no tag on originate, and no generated import filters even
        # though its neighbors have sites.
        leaf = make_leaf(site=None)
        links = make_sited_links(leaf, spine1_site="fmt2", spine2_site="sea")
        conf = parse_rendered_files(render_leaf(leaf, links))[
            "/etc/bird/conf.d/genprog.conf"
        ]
        assert "bgp_large_community.add" not in conf
        assert "filter genprog_import_" not in conf

    # A sited host cannot keep a standalone import_filter (see
    # test_import_filter_with_site_weighting_is_an_error), so the
    # weighted tests drop it from the bird meta like sea21's migration.
    SITED_BIRD = {"router_id": "192.168.3.1", "merge_paths": True}

    def test_origin_tag_present_with_site(self):
        leaf = make_leaf(site="sea", bird=self.SITED_BIRD)
        links = make_sited_links(leaf, spine1_site="fmt2", spine2_site="sea")
        conf = parse_rendered_files(render_leaf(leaf, links))[
            "/etc/bird/conf.d/genprog.conf"
        ]
        assert f"bgp_large_community.add(({COMMUNITY_ASN}, 1, 1));" in conf
        # Tag is applied on RTS_STATIC (our own originates) only.
        assert conf.index("RTS_STATIC") < conf.index("bgp_large_community.add")

    def test_import_filters_generated_per_neighbor_site_with_exact_prefs(self):
        leaf = make_leaf(site="sea", bird=self.SITED_BIRD)
        links = make_sited_links(leaf, spine1_site="fmt2", spine2_site="sea")
        conf = parse_rendered_files(render_leaf(leaf, links))[
            "/etc/bird/conf.d/genprog.conf"
        ]
        assert "filter genprog_import_fmt2 {" in conf
        assert "filter genprog_import_sea {" in conf

        # Matches are exact triplets on the fabric-wide community_asn;
        # bird 2.17.5 rejects a `*` in the first field of an lc set.
        assert f"bgp_large_community ~ [({COMMUNITY_ASN}, 1, 1)]" in conf
        assert f"bgp_large_community ~ [({COMMUNITY_ASN}, 1, 2)]" in conf
        assert "(*," not in conf

        d = haversine_km(SEA.coords, FMT2.coords)
        # Heard from the fmt2 spine (base = d): origin sea is
        # BASE - (d + d), origin fmt2 is BASE - (d + 0).
        assert f"bgp_local_pref = {BASE_LOCAL_PREF - 2 * d};" in conf
        assert f"bgp_local_pref = {BASE_LOCAL_PREF - d};" in conf
        # Heard from the sea spine (base = 0): both origins collapse to
        # BASE (origin sea) and BASE - d (origin fmt2) respectively --
        # BASE - d is already covered above, BASE alone confirms it.
        assert f"bgp_local_pref = {BASE_LOCAL_PREF};" in conf

        # Each protocol instance overrides the shared template's import
        # with its neighbor-site-specific filter.
        assert "import filter genprog_import_fmt2;" in conf
        assert "import filter genprog_import_sea;" in conf

        # Untagged (no matching large-community) routes fall through to
        # a plain accept -- no explicit local-pref set for them.
        assert conf.count("accept;\n}") >= 2

    def test_import_check_function_enforced_inside_generated_filter(self):
        # sea21-hv-egg-irl's migration: bird filters cannot call other
        # filters, only functions, so the blacklist moved from a
        # standalone import_filter to import_check_function, called
        # first inside every generated site-weighted import filter.
        leaf = make_leaf(site="sea", bird={"import_check_function": "is_blacklisted"})
        links = make_sited_links(leaf, spine1_site="fmt2", spine2_site="sea")
        conf = parse_rendered_files(render_leaf(leaf, links))[
            "/etc/bird/conf.d/genprog.conf"
        ]
        expected = (
            'if is_blacklisted() then reject "REJECTED ", net, " from ", '
            'proto, " blacklisted";'
        )
        # Present in both generated per-neighbor-site import filters
        # (also in the base template's unused fallback, hence >= 2).
        assert conf.count(expected) >= 2
        # Precedes the site-match rules inside each generated filter.
        fmt2_filter = conf.split("filter genprog_import_fmt2 {")[1].split("}\n")[0]
        assert fmt2_filter.strip().startswith("if is_blacklisted()")
        sea_filter = conf.split("filter genprog_import_sea {")[1].split("}\n")[0]
        assert sea_filter.strip().startswith("if is_blacklisted()")

    def test_import_filter_backward_compat_without_site_weighting(self):
        # A neighbor with no site gets no generated filter; the legacy
        # import_filter (or import_check_function) still governs the
        # template's default import, unchanged from before geographic
        # weighting existed.
        leaf = make_leaf(bird={"import_filter": "imp_fil"})
        conf = render_files(leaf)["/etc/bird/conf.d/genprog.conf"]
        assert conf.count("import filter imp_fil;") == 2
        assert "filter genprog_import_" not in conf

    def test_import_filter_with_site_weighting_is_an_error(self):
        # bird filters cannot call filters, so a standalone
        # import_filter would silently stop applying on weighted peers;
        # the render layer refuses the combination instead.
        leaf = make_leaf(site="sea")  # default bird keeps import_filter
        links = make_sited_links(leaf, spine1_site="fmt2", spine2_site="sea")
        with pytest.raises(ValueError, match="import_check_function"):
            render_leaf(leaf, links)
