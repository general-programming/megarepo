import os

import hvac
import jinja2
import yaml
from jinja2 import Environment, PackageLoader, make_logging_undefined
from magic import logging
from networkautomation.vendors import NetworkLink
from networkautomation.vendors.edgeos import EdgeOSHost
from networkautomation.vendors.linux import LinuxBirdHost
from networkautomation.vendors.vyos import VyOSHost

global_log = logging.getLogger(__name__)
vault = hvac.Client()
jinja_env = Environment(
    loader=PackageLoader("networkautomation"),
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

        hosts.append(hostclass(
            hostname=hostname,
            address=meta.get("address", None),
            asn=meta["asn"],
            nameservers=meta.get("nameservers", []),
            extra_config=meta.get("extra_config", ""),
        ))

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

    secrets = dict(
        password=get_secret("vyos-password"),
    )

    for device in devices:
        template = jinja_env.get_template(f"vpn/{device.devicetype}.j2")
        os.makedirs("output/vpn/", exist_ok=True)
        device_links = [
            link for link in links
            if device == link.side_a or device == link.side_b
        ]
        with open("output/vpn/" + device.hostname, "w") as f:
            f.write(template.render(
                global_meta=global_meta,
                device=device,
                links=device_links,
                secrets=secrets,
            ))
