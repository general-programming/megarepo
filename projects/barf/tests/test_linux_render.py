import pytest

from barf.common import render_template
from barf.model import wireguard
from barf.model.wireguard import WGKeypair, WGNetworkLink
from barf.vendors import BaseHost


@pytest.fixture(autouse=True)
def fake_keys(monkeypatch):
    monkeypatch.setattr(
        wireguard,
        "fetch_keypair",
        lambda path, generate_keys=True: WGKeypair(
            public_key=f"PUB-{path}", private_key=f"PRIV-{path}"
        ),
    )


def render_leaf():
    leaf = BaseHost(hostname="leaf-x", role="vpn", asn=65010)
    spine1 = BaseHost(hostname="fmt2-vpn-spine-1", role="vpn", asn=65000)
    spine2 = BaseHost(hostname="fmt2-vpn-spine-2", role="vpn", asn=65000)
    links = [
        WGNetworkLink(
            link_id=51000, side_a=spine1, side_b=leaf, network="172.31.255.8/31"
        ),
        WGNetworkLink(
            link_id=51001, side_a=spine2, side_b=leaf, network="172.31.255.10/31"
        ),
    ]
    return render_template(
        "vpn/linux.j2",
        device=leaf,
        secrets={},
        global_meta={"ssh_keys": []},
        vpn_links=links,
    )


def test_wireguard_allows_ipv6():
    # Unnumbered/link-local fabric needs v6 through the tunnel.
    assert "AllowedIPs = 0.0.0.0/0, ::/0" in render_leaf()


def test_stock_bird_conf_includes_confd():
    out = render_leaf()
    assert "> /etc/bird/bird.conf" in out
    assert 'include "/etc/bird/conf.d/*.conf";' in out
    # router id is the host's own /31 address on the first fabric link.
    assert "router id 172.31.255.9" in out
    # dual-stack kernel channels
    assert out.count("protocol kernel {") == 2


def test_genprog_fabric_conf():
    out = render_leaf()
    assert "> /etc/bird/conf.d/genprog.conf" in out
    assert "define GENPROG_ASN = 65010;" in out
    # one BGP protocol per spine, dash-sanitized names
    assert "protocol bgp fmt2_vpn_spine_1 from genprog_fabric" in out
    assert "protocol bgp fmt2_vpn_spine_2 from genprog_fabric" in out
    # correct source (our /31) and neighbor (spine /31) + spine AS
    assert "source address 172.31.255.9;" in out
    assert "neighbor 172.31.255.8 as 65000;" in out
    assert "neighbor 172.31.255.10 as 65000;" in out
    # dual-stack channels + bfd
    assert "extended next hop" not in out  # numbered sessions stay v4 next-hop
    assert 'interface "wg*"' in out
