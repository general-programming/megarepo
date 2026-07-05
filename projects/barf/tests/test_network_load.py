import pytest

from barf.util.network import load_network

NETWORK_YML = """
global_meta:
  ssh_keys: []
hosts:
  spine-1:
    address: 192.0.2.1
    asn: 65001
    type: vyos
    role: vpn
  leaf-1:
    asn: 65002
    type: vyos
    role: vpn
  oracle-1:
    address: 198.51.100.9
    asn: 65003
    type: external
    role: vpn
links:
  leaf-1:
    spine-1: 51820
  oracle-1:
    spine-1: {port: 51900, network: 172.31.255.0/31, secret: oracle-psk, ipsec: true}
"""


def write(tmp_path, text):
    path = tmp_path / "network.yml"
    path.write_text(text)
    return str(path)


def test_grouped_links_parse(tmp_path):
    hosts, links, _meta = load_network(write(tmp_path, NETWORK_YML))

    bare = next(link for link in links if link.link_id == 51820)
    # The uplink (inner key) is side_a: spine-first ordering decides
    # IP assignment on numbered links.
    assert bare.side_a.hostname == "spine-1"
    assert bare.side_b.hostname == "leaf-1"
    assert bare.network is None
    assert not bare.ipsec

    full = next(link for link in links if link.link_id == 51900)
    assert full.side_a.hostname == "spine-1"
    assert full.side_b.hostname == "oracle-1"
    assert full.network == "172.31.255.0/31"
    assert full.secret == "oracle-psk"
    assert full.ipsec is True


def test_unknown_link_host_is_an_error(tmp_path):
    broken = NETWORK_YML.replace(
        "  oracle-1:\n    spine-1: {port", "  nosuchbox:\n    spine-1: {port"
    )
    with pytest.raises(ValueError, match="unknown host 'nosuchbox'"):
        load_network(write(tmp_path, broken))


def test_duplicate_port_is_an_error(tmp_path):
    dupe = NETWORK_YML.replace("port: 51900", "port: 51820")
    with pytest.raises(ValueError, match="port 51820 used by both"):
        load_network(write(tmp_path, dupe))
