from __future__ import annotations

import ipaddress
import subprocess
from dataclasses import dataclass
from typing import TYPE_CHECKING, Optional

import hvac
from barf.model import get_vault
from barf.model.network import NetworkLink

if TYPE_CHECKING:
    from barf.vendors import BaseHost


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

    network: str
    secret: Optional[str] = None
    ipsec: bool = False

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
    """Generate a WireGuard private & public key.

    Requires that the 'wg' command is available on PATH

    Returns:
        (str, str): (private_key, public_key)
    """

    privkey = subprocess.check_output("wg genkey", shell=True).decode("utf-8").strip()
    pubkey = (
        subprocess.check_output(f"echo '{privkey}' | wg pubkey", shell=True)
        .decode("utf-8")
        .strip()
    )

    return WGKeypair(
        public_key=pubkey,
        private_key=privkey,
    )


def get_wg_keys(host: str, port: int, generate_keys: bool = True) -> WGKeypair:
    """Get the WireGuard private, public keys for a host.

    Args:
        host (str): The host to get the WireGuard keypair for.
        port (int): The port to get the WireGuard keypair for.
        generate_keys (bool): Whether to generate a new keypair if one doesn't exist.

    Returns:
        WGKeypair: The WireGuard keypair for the host.
    """
    secret_path = f"wg-{port}-{host}"
    vault = get_vault()

    try:
        response = vault.secrets.kv.v2.read_secret_version(
            mount_point="cluster-secrets",
            path=secret_path,
        )["data"]["data"]
        print(response, secret_path)
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

    return result
