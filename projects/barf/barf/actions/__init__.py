import logging
from typing import Tuple

import hvac
import netmiko
from barf.vendors import BaseHost
from napalm import get_network_driver


def get_secret(secret: str, key: str = "secret") -> str:
    """Vault secret fetcher.

    Args:
        secret (str): The secret's path.

    Returns:
        str: The secret's value.
    """
    vault = hvac.Client()
    response = vault.secrets.kv.v2.read_secret_version(path=secret,)[
        "data"
    ]["data"]

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
    """Open a connection to a host.

    Args:
        host (BaseHost): The host to connect to.

    Returns:
        netmiko.base_connection.BaseConnection: The connection to the host.
    """
    log = logging.getLogger("connection." + host.hostname)
    extra_args = {
        "allow_agent": True,
        "use_keys": True,
    }

    # Get NAPALM driver for the device.
    device_driver = host.DEVICETYPE
    if device_driver == "cisco":
        device_driver = "ios"
    elif device_driver != "eos":
        extra_args.update(
            {
                "port": 22,
            }
        )

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


def push_config(device: BaseHost, config: str):
    log = logging.getLogger("configpush." + device.hostname)

    # These platforms are only supported for now.
    if device.DEVICETYPE not in ["vyos", "eos", "cisco"]:
        return

    # Attempt to connect with the FQDN.
    # If that fails, connect with the IP directly.
    napalm_device = None
    addresses = [
        f"{device.hostname}.generalprogramming.org",
        device.management_address,
        device.address,
    ]

    while addresses:
        address = addresses.pop(0)
        if not address:
            continue

        try:
            napalm_device = open_connection(device, address)
            break
            log.error(e)
            log.error("Failed to connect to %s", address)

    if not napalm_device:
        log.error("failed to connect to device")
        return

    napalm_device.load_merge_candidate(config=config)
    log.info(napalm_device.compare_config())

    confirmation = (
        input(f"Confirm push for '{device.hostname}': [y]es/[N]/[d]iscard: ")
        .lower()
        .strip()
    )

    if confirmation == "y":
        napalm_device.commit_config()
        log.info("Pushing configuration.")
    elif confirmation == "d":
        napalm_device.discard_config()
        log.info("Discarding configuration.")
