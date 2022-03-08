import logging

import hvac
import netmiko
from napalm import get_network_driver


def get_secret(secret: str) -> str:
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
    return response["secret"]


def push_config(device, config):
    log = logging.getLogger("configpush." + device.hostname)

    if device.DEVICETYPE not in ["vyos"]:
        return

    driver = get_network_driver(device.DEVICETYPE)
    vendor_secret_key = f"{device.DEVICETYPE}-password"
    napalm_device = driver(
        hostname=f"{device.hostname}.generalprogramming.org",
        username=device.DEVICETYPE,
        password=get_secret(vendor_secret_key),
        optional_args={"port": 22},
    )
    try:
        napalm_device.open()
    except netmiko.ssh_exception.NetmikoTimeoutException as e:
        log.error(e)
        return

    log.info("Connected.")

    if napalm_device.compare_config():
        log.error("Pending changes not commited, not pushing configs.")
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