import os

import yaml
from barf.actions import get_secret, push_config
from barf.common import render_template
from barf.vendors import NetworkLink
from barf.vendors.edgeos import EdgeOSHost
from barf.vendors.external import ExternalHost
from barf.vendors.linux import LinuxBirdHost
from barf.vendors.vyos import VyOSHost
from magic import logging

global_log = logging.getLogger(__name__)


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
        elif meta["type"] == "external":
            hostclass = ExternalHost
        else:
            raise ValueError("Invalid host type " + meta["type"])

        hosts.append(
            hostclass.from_meta(
                hostname=hostname,
                meta=meta,
            )
        )

    # links
    links = []
    for link_id, link in network["links"].items():
        print(link)
        side_a = next(host for host in hosts if host.hostname == link["side_a"])
        side_b = next(host for host in hosts if host.hostname == link["side_b"])

        links.append(
            NetworkLink(
                link_id=link_id,
                side_a=side_a,
                side_b=side_b,
                network=link["network"],
                secret=link.get("secret", None),
                ipsec=link.get("ipsec", False),
            )
        )

    return hosts, links, global_meta


if __name__ == "__main__":
    devices, links, global_meta = load_network("network.yml")
    os.makedirs("output/vpn/cloud_init", exist_ok=True)

    secrets = dict(
        password=get_secret("vyos-password"),
        api_password=get_secret("vyos-api-password"),
    )

    for device in devices:
        log = logging.getLogger(device.hostname)

        # Ignore untemplatable devices.
        if not device.is_templatable:
            continue

        # config render
        device_links = [
            link for link in links if device == link.side_a or device == link.side_b
        ]
        rendered_config = render_template(
            f"vpn/{device.devicetype}.j2",
            global_meta=global_meta,
            device=device,
            links=device_links,
            secrets=secrets,
        )

        # write config file
        with open("output/vpn/" + device.hostname, "w") as f:
            f.write(rendered_config)
            log.info("Config saved.")

        # write cloud-init file
        with open("output/vpn/cloud_init/" + device.hostname, "w") as f:
            f.write("#cloud-config\n")
            yaml.dump(
                {"vyos_config_commands": [x for x in rendered_config.split("\n") if x]},
                f,
            )

        # napalm push
        if "NAPALM_PUSH" in os.environ:
            push_config(device, rendered_config)
