from types import SimpleNamespace

import pytest

from barf.model import wireguard
from barf.model.wireguard import WGKeypair, get_wg_keys, prefetch_wg_keys
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


def test_get_wg_keys_caches_across_calls(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    first = get_wg_keys("boxa", 51820)
    second = get_wg_keys("boxa", 51820)
    assert first is second
    assert reads == ["wg-51820-boxa"]


def test_prefetch_fetches_each_pair_once(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    pairs = [("boxa", 51820), ("boxb", 51820), ("boxa", 51820)]
    prefetch_wg_keys(pairs)
    assert sorted(reads) == ["wg-51820-boxa", "wg-51820-boxb"]

    # Everything is served from the cache afterwards.
    reads.clear()
    get_wg_keys("boxa", 51820)
    get_wg_keys("boxb", 51820)
    prefetch_wg_keys(pairs)
    assert reads == []


def test_prefetch_link_keys_collects_both_sides_of_target_links(monkeypatch):
    captured = []
    monkeypatch.setattr(
        "barf.model.wireguard.prefetch_wg_keys",
        lambda pairs, max_workers=16: captured.extend(pairs),
    )

    spine = SimpleNamespace(hostname="spine")
    leaf = SimpleNamespace(hostname="leaf")
    other = SimpleNamespace(hostname="other")
    links = [
        SimpleNamespace(link_id=51820, side_a=spine, side_b=leaf, ipsec=False),
        SimpleNamespace(link_id=51821, side_a=other, side_b=spine, ipsec=False),
        SimpleNamespace(link_id=51822, side_a=other, side_b=other, ipsec=False),
        # IPsec links have no WG keypairs and must not be prefetched.
        SimpleNamespace(link_id=51831, side_a=leaf, side_b=other, ipsec=True),
    ]

    prefetch_link_keys([leaf], links)
    # Only the WG link touching the target, but both of its sides.
    assert captured == [("leaf", 51820), ("spine", 51820)]


def test_prefetch_wg_keys_surfaces_errors(monkeypatch):
    def boom():
        raise RuntimeError("vault down")

    monkeypatch.setattr(wireguard, "get_vault", boom)
    with pytest.raises(RuntimeError, match="vault down"):
        prefetch_wg_keys([("boxa", 51820)])


def test_cache_shared_between_prefetch_and_host_objects(monkeypatch):
    reads = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: FakeVault(reads))

    prefetch_wg_keys([("boxa", 51820)])
    keys = get_wg_keys("boxa", 51820)
    assert isinstance(keys, WGKeypair)
    assert keys.public_key == "pub-wg-51820-boxa"
    assert len(reads) == 1


def test_prefetch_never_generates_missing_keys(monkeypatch):
    class MissingVault:
        def __init__(self):
            def read(mount_point, path):
                raise hvac_InvalidPath()

            kv = SimpleNamespace(read_secret_version=read)
            self.secrets = SimpleNamespace(kv=SimpleNamespace(v2=kv))

    import hvac.exceptions

    hvac_InvalidPath = hvac.exceptions.InvalidPath
    generated = []
    monkeypatch.setattr(wireguard, "get_vault", lambda: MissingVault())
    monkeypatch.setattr(
        wireguard, "generate_wireguard_keys", lambda: generated.append(1)
    )

    prefetch_wg_keys([("boxa", 51820)])
    assert generated == []
    assert ("boxa", 51820) not in wireguard._KEY_CACHE
