from types import SimpleNamespace

import pytest

from barf.model import wireguard
from barf.model.wireguard import (
    WGKeypair,
    WGNetworkLink,
    fetch_keypair,
    prefetch_keypairs,
)
from barf.util.render import prefetch_link_keys


@pytest.fixture(autouse=True)
def clean_cache():
    wireguard._KEY_CACHE.clear()
    yield
    wireguard._KEY_CACHE.clear()


class FakeVault:
    """Counts reads; serves the same keypair for every path."""

    def __init__(self, counter):
        kv = SimpleNamespace(
            read_secret_version=self._read,
            patch=None,
            create_or_update_secret=None,
        )
        self.secrets = SimpleNamespace(kv=SimpleNamespace(v2=kv))
        self._counter = counter

    def _read(self, mount_point, path):
        self._counter.append(path)
        return {"data": {"data": {"private_key": "priv", "public_key": f"pub-{path}"}}}


def make_link(pinned: bool, port: int = 51820) -> WGNetworkLink:
    spine = SimpleNamespace(hostname="spine")
    leaf = SimpleNamespace(hostname="leaf")
    return WGNetworkLink(
        link_id=port, side_a=spine, side_b=leaf, network=None, pinned=pinned
    )


def test_fetch_keypair_caches_across_calls(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    first = fetch_keypair("wg-51820-boxa")
    second = fetch_keypair("wg-51820-boxa")
    assert first is second
    assert reads == ["wg-51820-boxa"]


def test_pinned_link_uses_legacy_port_paths(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    link = make_link(pinned=True, port=51820)
    keys = link.wg_keys(link.side_a)
    assert isinstance(keys, WGKeypair)
    assert reads == ["wg-51820-spine"]


def test_derived_link_uses_pair_paths(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    link = make_link(pinned=False, port=51067)
    link.wg_privkey(link.side_a)
    link.wg_pubkey(link.side_b)
    # Order-free pair name, independent of the port number.
    assert reads == ["wglink-leaf--spine-spine", "wglink-leaf--spine-leaf"]


def test_prefetch_fetches_each_path_once(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    paths = ["wg-51820-boxa", "wg-51820-boxb", "wg-51820-boxa"]
    prefetch_keypairs(paths)
    assert sorted(reads) == ["wg-51820-boxa", "wg-51820-boxb"]

    reads.clear()
    fetch_keypair("wg-51820-boxa")
    prefetch_keypairs(paths)
    assert reads == []


def test_prefetch_link_keys_collects_both_sides_of_target_links(monkeypatch):
    captured = []
    monkeypatch.setattr(
        "barf.model.wireguard.prefetch_keypairs",
        lambda paths, max_workers=16: captured.extend(paths),
    )

    spine = SimpleNamespace(hostname="spine")
    leaf = SimpleNamespace(hostname="leaf")
    other = SimpleNamespace(hostname="other")
    links = [
        WGNetworkLink(link_id=51820, side_a=spine, side_b=leaf, network=None),
        WGNetworkLink(
            link_id=51067, side_a=spine, side_b=leaf, network=None, pinned=False
        ),
        WGNetworkLink(link_id=51821, side_a=other, side_b=other, network=None),
        # IPsec links have no WG keypairs and must not be prefetched.
        WGNetworkLink(
            link_id=51831, side_a=leaf, side_b=other, network="10.0.0.0/31", ipsec=True
        ),
    ]

    prefetch_link_keys([leaf], links)
    assert captured == [
        "wg-51820-leaf",
        "wg-51820-spine",
        "wglink-leaf--spine-leaf",
        "wglink-leaf--spine-spine",
    ]


def test_prefetch_never_generates_missing_keys(monkeypatch):
    import hvac.exceptions

    class MissingVault:
        def __init__(self):
            def read(mount_point, path):
                raise hvac.exceptions.InvalidPath()

            kv = SimpleNamespace(read_secret_version=read)
            self.secrets = SimpleNamespace(kv=SimpleNamespace(v2=kv))

    generated = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: MissingVault())
    monkeypatch.setattr(
        wireguard, "generate_wireguard_keys", lambda: generated.append(1)
    )

    prefetch_keypairs(["wg-51820-boxa"])
    assert generated == []
    assert "wg-51820-boxa" not in wireguard._KEY_CACHE


def test_generate_wireguard_keys_roundtrip():
    from base64 import b64decode

    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey

    keys = wireguard.generate_wireguard_keys()
    priv_raw = b64decode(keys.private_key)
    pub_raw = b64decode(keys.public_key)
    assert len(priv_raw) == 32 and len(pub_raw) == 32

    # The stored public key must be the one wireguard derives from the
    # private key.
    derived = X25519PrivateKey.from_private_bytes(priv_raw).public_key()
    assert (
        derived.public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)
        == pub_raw
    )
