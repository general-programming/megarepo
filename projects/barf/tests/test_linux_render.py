import pytest

from barf.common import render_template
from barf.model import wireguard
from barf.model.wireguard import WGKeypair, WGNetworkLink
from barf.vendors import BaseHost
from barf.vendors.linux import LinuxBirdHost, parse_rendered_files


@pytest.fixture(autouse=True)
def fake_keys(monkeypatch):
    monkeypatch.setattr(
        wireguard,
        "fetch_keypair",
        lambda path, generate_keys=True: WGKeypair(
            public_key=f"PUB-{path}", private_key=f"PRIV-{path}"
        ),
    )


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
        WGNetworkLink(
            link_id=51070,
            side_a=spine1,
            side_b=leaf,
            network=networks[0],
            pinned=False,
        ),
        WGNetworkLink(
            link_id=51134,
            side_a=spine2,
            side_b=leaf,
            network=networks[1],
            pinned=False,
        ),
    ]


def render_leaf(leaf=None, links=None) -> str:
    leaf = leaf or make_leaf()
    return render_template(
        "vpn/linux.j2",
        device=leaf,
        secrets={},
        global_meta={"ssh_keys": []},
        vpn_links=links if links is not None else make_links(leaf),
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
