from __future__ import annotations

import functools
import logging
from typing import TYPE_CHECKING, Any, Optional, Tuple

import hvac
from napalm import get_network_driver

if TYPE_CHECKING:
    from barf.vendors import BaseHost


@functools.lru_cache(maxsize=128)
def get_secret(secret: str, key: Optional[str] = "secret") -> Any:
    """Vault secret fetcher, cached per (secret, key) for the process.

    Args:
        secret: The secret's path.
        key: The key to return from the secret, or None for the whole
            secret dict.

    Returns:
        The secret's value, or the full secret dict when key is None.
    """
    vault = hvac.Client()
    response = vault.secrets.kv.v2.read_secret_version(
        path=secret,
    )["data"]["data"]

    if key:
        return response[key]
    else:
        return response


def get_supertech_creditentials(host: BaseHost) -> Tuple[str, str]:
    credentials = get_secret(
        "supertech-credentials",
        key=None,
    )

    return credentials["username"], credentials["password"]


def open_connection(host: BaseHost, hostname: str):
    """Open a NAPALM connection to a host.

    Args:
        host (BaseHost): The host to connect to.
        hostname (str): The address to reach it on.

    Returns:
        The opened NAPALM device.
    """
    log = logging.getLogger("connection." + host.hostname)

    device_driver = host.NAPALM_DRIVER
    if not device_driver:
        raise NotImplementedError(f"{host.devicetype!r} devices have no NAPALM driver")

    extra_args = {
        "allow_agent": True,
    }
    if device_driver != "eos":
        extra_args["port"] = 22

    driver = get_network_driver(device_driver)
    username, password = get_supertech_creditentials(host)

    if device_driver == "vyos":
        username = "vyos"

    napalm_device = driver(
        hostname=hostname,
        username=username,
        password=password,
        optional_args=extra_args,
    )
    napalm_device.open()

    log.info("Connected.")

    return napalm_device
