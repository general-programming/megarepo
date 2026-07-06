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


PROFILE_YML = """
global_meta:
  ssh_keys: []
  nameservers: [10.0.0.53, 1.1.1.1]
profiles:
  leaf: &leaf
    asn: 65002
    type: vyos
    role: vpn
    networks: [10.9.0.0/24]
hosts:
  leaf-1:
    <<: *leaf
  leaf-2:
    <<: *leaf
    nameservers: [9.9.9.9]
"""


def test_global_nameservers_default_and_override(tmp_path):
    hosts, _links, _meta = load_network(write(tmp_path, PROFILE_YML))
    leaf1 = next(h for h in hosts if h.hostname == "leaf-1")
    leaf2 = next(h for h in hosts if h.hostname == "leaf-2")
    assert leaf1.nameservers == ["10.0.0.53", "1.1.1.1"]
    assert leaf2.nameservers == ["9.9.9.9"]


def test_profiles_merge_and_are_otherwise_ignored(tmp_path):
    hosts, _links, _meta = load_network(write(tmp_path, PROFILE_YML))
    assert {h.hostname for h in hosts} == {"leaf-1", "leaf-2"}
    leaf1 = next(h for h in hosts if h.hostname == "leaf-1")
    assert leaf1.asn == 65002
    assert leaf1.networks == ["10.9.0.0/24"]


DERIVED_YML = """
global_meta:
  ssh_keys: []
hosts:
  spine-1:
    id: 1
    asn: 65001
    type: vyos
    role: vpn
  leaf-1:
    id: 3
    asn: 65002
    type: vyos
    role: vpn
  leaf-2:
    asn: 65003
    type: vyos
    role: vpn
links:
  leaf-1:
    spine-1: {}
"""


def test_derived_port_from_host_ids(tmp_path):
    _hosts, links, _meta = load_network(write(tmp_path, DERIVED_YML))
    link = links[0]
    # 51000 + min(1,3)*64 + max(1,3)
    assert link.link_id == 51000 + 1 * 64 + 3
    assert link.pinned is False


def test_pinned_port_sets_pinned_flag(tmp_path):
    src = DERIVED_YML.replace("spine-1: {}", "spine-1: 51820")
    _hosts, links, _meta = load_network(write(tmp_path, src))
    assert links[0].link_id == 51820
    assert links[0].pinned is True


def test_derivation_without_ids_is_an_error(tmp_path):
    src = DERIVED_YML.replace(
        "links:\n  leaf-1:\n    spine-1: {}", "links:\n  leaf-2:\n    spine-1: {}"
    )
    with pytest.raises(ValueError, match="needs an `id`"):
        load_network(write(tmp_path, src))


SITES_YML = """
global_meta:
  ssh_keys: []
  community_asn: 4280805525
  sites:
    sea: {id: 1, coords: [47.61, -122.33]}
    fmt2: {id: 2, coords: [37.55, -121.99]}
hosts:
  spine-1:
    asn: 65001
    type: vyos
    role: vpn
    site: fmt2
  leaf-1:
    asn: 65002
    type: vyos
    role: vpn
    site: sea
  leaf-2:
    asn: 65003
    type: vyos
    role: vpn
"""


def test_sites_parse_and_hosts_carry_their_site(tmp_path):
    hosts, _links, global_meta = load_network(write(tmp_path, SITES_YML))
    assert global_meta["sites"]["sea"].id == 1
    assert global_meta["sites"]["fmt2"].id == 2
    by_name = {h.hostname: h for h in hosts}
    assert by_name["spine-1"].site == "fmt2"
    assert by_name["leaf-1"].site == "sea"
    # Hosts without a `site:` are allowed (no tagging/weighting for them).
    assert by_name["leaf-2"].site is None


def test_unknown_host_site_is_an_error(tmp_path):
    src = SITES_YML.replace("site: sea", "site: nowhere")
    with pytest.raises(ValueError, match="unknown site 'nowhere'"):
        load_network(write(tmp_path, src))


def test_duplicate_site_id_is_an_error(tmp_path):
    src = SITES_YML.replace(
        "fmt2: {id: 2, coords: [37.55, -121.99]}",
        "fmt2: {id: 1, coords: [37.55, -121.99]}",
    )
    with pytest.raises(ValueError, match="id 1 used by both"):
        load_network(write(tmp_path, src))


def test_no_sites_block_is_fine(tmp_path):
    hosts, _links, global_meta = load_network(write(tmp_path, NETWORK_YML))
    assert global_meta["sites"] == {}
    assert all(h.site is None for h in hosts)


def test_sites_without_community_asn_is_an_error(tmp_path):
    src = SITES_YML.replace("  community_asn: 4280805525\n", "")
    with pytest.raises(ValueError, match="community_asn is missing"):
        load_network(write(tmp_path, src))
