import os
from typing import List, Tuple

import click
import yaml
from barf.actions import get_secret
from barf.actions import push_config as f_push_config
from barf.common import render_template
from barf.vendors import VENDOR_MAP, BaseHost, NetworkLink
from magic import logging

global_log = logging.getLogger(__name__)


def load_network(filename: str) -> Tuple[List[BaseHost], List[NetworkLink], dict]:
    with open(filename, "r") as f:
        network = yaml.safe_load(f)

    # global meta
    global_meta = network["global_meta"]

    # hosts
    hosts = []
    for hostname, meta in network["hosts"].items():
        if meta["type"] not in VENDOR_MAP:
            raise ValueError("Invalid host type " + meta["type"])

        hostclass = VENDOR_MAP[meta["type"]]

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


@click.command()
@click.option("--push-config", default=False, is_flag=True)
def main(network_filename: str = "network.yml", push_config: bool = False):
    # Load network file
    hosts, links, global_meta = load_network(network_filename)

    # Get secrets from Vault.
    secrets = dict(
        password=get_secret("vyos-password"),
        vyos_api_password=get_secret("vyos-api-password"),
        tacacs_host=get_secret("tacacs-host"),
        tacacs_key=get_secret("tacacs-key"),
    )

    # Render configs
    for host in hosts:
        log = logging.getLogger(host.hostname)

        # XXX/TODO: get device role from somewhere
        device_role = "vpn"
        os.makedirs(f"output/{device_role}/cloud_init", exist_ok=True)

        # Ignore untemplatable devices.
        if not host.is_templatable:
            continue

        # config render
        device_links = [
            link for link in links if host == link.side_a or host == link.side_b
        ]
        rendered_config = render_template(
            f"{device_role}/{host.devicetype}.j2",
            global_meta=global_meta,
            device=host,
            links=device_links,
            secrets=secrets,
        )

        # write config file
        with open(f"output/{device_role}/" + host.hostname, "w") as f:
            f.write(rendered_config)
            log.info("Config saved.")

        # write cloud-init file
        with open(f"output/{device_role}/cloud_init/" + host.hostname, "w") as f:
            f.write("#cloud-config\n")
            yaml.dump(
                {"vyos_config_commands": [x for x in rendered_config.split("\n") if x]},
                f,
            )

        # napalm push
        if push_config:
            f_push_config(host, rendered_config)


if __name__ == "__main__":
    main()
