from __future__ import annotations

import concurrent.futures
import ipaddress
from dataclasses import dataclass
from typing import TYPE_CHECKING, Iterable, Optional

import hvac

from barf.model import get_vault
from barf.model.network import NetworkLink

if TYPE_CHECKING:
    from barf.vendors import BaseHost

# Process-wide keypair cache keyed by Vault secret path. Without it
# every host render re-fetches its peers' keys from Vault.
_KEY_CACHE: dict[str, "WGKeypair"] = {}


@dataclass
class WGKeypair:
    """A keypair for a WireGuard tunnel."""

    public_key: str
    private_key: str

    def to_dict(self):
        """Return a dict representation of this keypair for Vault."""
        return {
            "public_key": self.public_key,
            "private_key": self.private_key,
        }


@dataclass
class WGNetworkLink(NetworkLink):
    """A link between two hosts using WireGuard."""

    # None means an unnumbered link (interface-based BGP peering).
    network: Optional[str]
    secret: Optional[str] = None
    ipsec: bool = False
    # True when network.yml pins the port explicitly. Pinned links use
    # the legacy port-based Vault key paths; derived links use the
    # pair-based paths — so unpinning a link in network.yml is the
    # whole per-link migration (new port, new paths, fresh keys).
    pinned: bool = True

    @property
    def pair(self) -> str:
        """Canonical, order-free name for the two endpoints."""
        return "--".join(sorted([self.side_a.hostname, self.side_b.hostname]))

    def _key_path(self, hostname: str) -> str:
        if self.pinned:
            return f"wg-{self.link_id}-{hostname}"
        return f"wglink-{self.pair}-{hostname}"

    def wg_keys(self, host: "BaseHost") -> "WGKeypair":
        """The WireGuard keypair for ``host``'s side of this link."""
        return fetch_keypair(self._key_path(host.hostname))

    def wg_privkey(self, host: "BaseHost") -> str:
        return self.wg_keys(host).private_key

    def wg_pubkey(self, host: "BaseHost") -> str:
        return self.wg_keys(host).public_key

    def get_ip(self, host: "BaseHost", with_netmask: bool = False) -> str:
        """Return the IP address of the host on this link."""
        if not self.network:
            return None

        if ":" in self.network:
            network = ipaddress.IPv6Network(self.network)
        else:
            network = ipaddress.IPv4Network(self.network)

        # get the A + B IPS
        side_a_ip = next(network.hosts())
        side_b_ip = side_a_ip + 1

        if with_netmask:
            side_a_ip = f"{side_a_ip}/{network.prefixlen}"
            side_b_ip = f"{side_b_ip}/{network.prefixlen}"

        if host == self.side_a:
            return side_a_ip
        elif host == self.side_b:
            return side_b_ip
        else:
            return None

    @property
    def unnumbered(self) -> bool:
        """Whether this link is unnumbered."""
        return self.network is None


def generate_wireguard_keys() -> WGKeypair:
    """Generate a WireGuard keypair (Curve25519 per RFC 7748).

    Pure Python via the cryptography library — no `wg` binary needed;
    the kernel clamps the scalar on use exactly as `wg genkey` would.
    """
    from base64 import b64encode

    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric.x25519 import X25519PrivateKey

    private = X25519PrivateKey.generate()
    priv_raw = private.private_bytes(
        serialization.Encoding.Raw,
        serialization.PrivateFormat.Raw,
        serialization.NoEncryption(),
    )
    pub_raw = private.public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw
    )

    return WGKeypair(
        public_key=b64encode(pub_raw).decode(),
        private_key=b64encode(priv_raw).decode(),
    )


def prefetch_keypairs(secret_paths: Iterable[str], max_workers: int = 16) -> None:
    """Warm the keypair cache with parallel Vault reads.

    Vault KV v2 has no batch-read API, so "multi fetch" means doing the
    per-secret reads concurrently. Each worker gets its own hvac client
    (``get_vault`` builds one per call), so this is thread-safe.
    """
    missing = [p for p in dict.fromkeys(secret_paths) if p not in _KEY_CACHE]
    if not missing:
        return

    def fetch(secret_path: str) -> None:
        # Read-only: warming a cache must never create secrets. A
        # genuinely missing keypair is left for the render to generate
        # (serially, in the main thread).
        try:
            fetch_keypair(secret_path, generate_keys=False)
        except ValueError:
            pass

    workers = min(max_workers, len(missing))
    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
        # fetch_keypair fills the cache; consume the iterator to
        # surface fetch errors here rather than mid-render.
        list(pool.map(fetch, missing))


def fetch_keypair(secret_path: str, generate_keys: bool = True) -> WGKeypair:
    """Get the WireGuard keypair stored at a Vault secret path.

    Args:
        secret_path: The cluster-secrets path holding the keypair.
        generate_keys: Whether to generate (and store) a new keypair if
            one doesn't exist.
    """
    cached = _KEY_CACHE.get(secret_path)
    if cached is not None:
        return cached

    vault = get_vault()

    try:
        response = vault.secrets.kv.v2.read_secret_version(
            mount_point="cluster-secrets",
            path=secret_path,
        )["data"]["data"]
        private_key = response["private_key"]
        public_key = response["public_key"]
        result = WGKeypair(
            public_key=public_key,
            private_key=private_key,
        )
    except hvac.exceptions.InvalidPath:
        if not generate_keys:
            raise ValueError(f"Secret '{secret_path}' does not exist.")
        result = generate_wireguard_keys()
        vault.secrets.kv.v2.create_or_update_secret(
            mount_point="cluster-secrets",
            path=secret_path,
            secret=result.to_dict(),
        )

    _KEY_CACHE[secret_path] = result
    return result
