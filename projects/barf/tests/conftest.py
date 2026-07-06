"""Shared fixtures and fabric test data.

The site constants mirror network.yml's real geometry (Seattle and
Fremont) so distance-derived assertions stay meaningful.
"""

from unittest import mock

import hvac.exceptions
import pytest

import barf.vendors as vendors_module
from barf.model import wireguard
from barf.model.wireguard import WGKeypair, WGNetworkLink
from barf.util.sites import Site

SEA = Site(name="sea", id=1, coords=(47.61, -122.33))
FMT2 = Site(name="fmt2", id=2, coords=(37.55, -121.99))
SITES = {"sea": SEA, "fmt2": FMT2}
# Fabric-wide Global Administrator for site-origin communities; tags
# and matches carry this, never the per-host ASN (bird's set grammar
# cannot wildcard the first field of a large-community set).
COMMUNITY_ASN = 4280805525


def make_link(link_id: int, side_a, side_b, network: str = None) -> WGNetworkLink:
    return WGNetworkLink(
        link_id=link_id, side_a=side_a, side_b=side_b, network=network, pinned=False
    )


@pytest.fixture
def fake_keys(monkeypatch):
    """Serve deterministic WireGuard keypairs instead of Vault's."""
    monkeypatch.setattr(
        wireguard,
        "fetch_keypair",
        lambda path, generate_keys=True: WGKeypair(
            public_key=f"PUB-{path}", private_key=f"PRIV-{path}"
        ),
    )


@pytest.fixture
def fake_vault(monkeypatch):
    """Never let a render touch the real Vault.

    Rendering common/vyatta.j2 calls ``device.admin_password``, which
    hits Vault via ``BaseHost.secret()``. barf.vendors.get_vault (the
    module-level name every host's ``__init__`` calls) is patched to a
    mock whose ``read_secret_version`` always misses, so ``secret()``
    falls back to generating a value and "writing" it to the mock
    (a no-op) instead of ever making a real network call.
    """
    fake = mock.MagicMock()
    fake.secrets.kv.v2.read_secret_version.side_effect = hvac.exceptions.InvalidPath()
    monkeypatch.setattr(vendors_module, "get_vault", lambda: fake)
