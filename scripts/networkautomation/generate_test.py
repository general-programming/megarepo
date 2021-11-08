import os

import hvac
import jinja2
import netmiko
import yaml
from barf.vendors import NetworkLink
from barf.vendors.edgeos import EdgeOSHost
from barf.vendors.linux import LinuxBirdHost
from barf.vendors.vyos import VyOSHost
from jinja2 import Environment, PackageLoader, make_logging_undefined
from magic import logging
from napalm import get_network_driver

global_log = logging.getLogger(__name__)
vault = hvac.Client()
jinja_env = Environment(
    loader=PackageLoader("barf"),
    autoescape=False,
    undefined=make_logging_undefined(global_log, base=jinja2.Undefined),
    trim_blocks=True,
    lstrip_blocks=True,
)


def get_secret(secret: str) -> str:
    """Vault secret fetcher.

    Args:
        secret (str): The secret's path.

    Returns:
        str: The secret's value.
    """
    response = vault.secrets.kv.v2.read_secret_version(
        path=secret,
    )["data"]["data"]
    return response["secret"]


def load_network(filename: str):
    with open(filename, "r") as f:
        network = yaml.safe_load(f)

    # global meta
    global_meta = network["global_meta"]

    # hosts
    hosts = []
    for hostname, meta in network["hosts"].items():
        if meta["type"] == "vyos":
            hostclass = VyOSHost
        elif meta["type"] == "edgeos":
            hostclass = EdgeOSHost
        elif meta["type"] == "linux":
            hostclass = LinuxBirdHost
        else:
            raise ValueError("Invalid host type " + meta["type"])

        hosts.append(hostclass.from_meta(
            hostname=hostname,
            meta=meta,
        ))

    # links
    links = []
    for link_id, link in network["links"].items():
        side_a = next(host for host in hosts if host.hostname == link["side_a"])
        side_b = next(host for host in hosts if host.hostname == link["side_b"])

        links.append(NetworkLink(
            link_id=link_id,
            side_a=side_a,
            side_b=side_b,
            network=link["network"]
        ))

    return hosts, links, global_meta


if __name__ == "__main__":
    devices, links, global_meta = load_network("network.yml")
    os.makedirs("output/vpn/", exist_ok=True)

    secrets = dict(
        password=get_secret("vyos-password"),
    )

    for device in devices:
        log = logging.getLogger(device.hostname)

        # config render
        device_links = [
            link for link in links
            if device == link.side_a or device == link.side_b
        ]
        template = jinja_env.get_template(f"vpn/{device.devicetype}.j2")
        rendered_config = template.render(
            global_meta=global_meta,
            device=device,
            links=device_links,
            secrets=secrets,
        )

        # write config file
        with open("output/vpn/" + device.hostname, "w") as f:
            f.write(rendered_config)

        # write cloud-init file
        with open("output/vpn/cloud-init-" + device.hostname, "w") as f:
            f.write("#cloud-config\n")
            yaml.dump({
                "vyos_config_commands": [x for x in rendered_config.split("\n") if x]
            }, f)

        # napalm
        napalm_push = "NAPALM_PUSH" in os.environ
        if napalm_push:
            if not isinstance(device, VyOSHost):
                continue
            driver = get_network_driver("vyos")
            napalm_device = driver(
                hostname=f"{device.hostname}.generalprogramming.org",
                username="vyos",
                password=secrets["password"],
                optional_args={"port": 22},
            )
            try:
                napalm_device.open()
            except netmiko.ssh_exception.NetmikoTimeoutException as e:
                log.error(e)
                continue
            log.info("Connected.")

            if napalm_device.compare_config():
                log.error("Pending changes not commited, not pushing configs.")
                continue

            napalm_device.load_merge_candidate(config=rendered_config)
            log.info(napalm_device.compare_config())

            confirmation = input("Confirm push? Y/[N]: ")
            if confirmation.lower().strip() == "y":
                napalm_device.commit_config()
