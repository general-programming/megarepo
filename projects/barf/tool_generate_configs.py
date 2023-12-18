import os
from functools import lru_cache
from typing import List, Tuple

import click
import yaml
from barf.actions import get_secret
from barf.actions import push_config as f_push_config
from barf.common import render_template
from barf.model.wireguard import WGNetworkLink
from barf.vendors import VENDOR_MAP, BaseHost
from gql import gql
from magic_logger import logging

from common import get_nb_client

global_log = logging.getLogger(__name__)

query = gql(
    """
query ($tag: [String]) {
  vlan_list {
    name
    vid
  }

  device_list(tag: $tag) {
    name
    serial
    asset_tag
    config_context

    primary_ip4 {
      address
    }

    primary_ip6 {
      address
    }

    platform {
      slug
      name
    }

    tags {
      name
    }

    interfaces {
      name
      type
      description
      mode
      enabled

      lag {
        name
      }

      tagged_vlans {
        name
        vid
      }

      untagged_vlan {
        name
        vid
      }

      cable {
        id
        terminations {
          cable_end
          _device {
            name
          }
        }
      }

      ip_addresses {
        address
      }

      vrf {
        name
      }
    }
  }
}"""
)


def load_network(filename: str) -> Tuple[List[BaseHost], List[WGNetworkLink], dict]:
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
        side_a = next(host for host in hosts if host.hostname == link["side_a"])
        side_b = next(host for host in hosts if host.hostname == link["side_b"])

        links.append(
            WGNetworkLink(
                link_id=link_id,
                side_a=side_a,
                side_b=side_b,
                network=link.get("network", None),
                secret=link.get("secret", None),
                ipsec=link.get("ipsec", False),
            )
        )

    return hosts, links, global_meta


class VaultSecrets:
    """Secret fetcher that uses Vault and caches secrets.

    This lets us avoid hitting Vault for every single secret fetched.
    Static secrets in theory will not change during the lifetime of the script.
    """

    @lru_cache(maxsize=256)
    def __getattr__(self, key: str) -> str:
        """Fetch a secret from Vault.

        Args:
            key (str): The key to fetch.

        Returns:
            str: The secret's value.
        """
        key = key.replace("_", "-").strip()

        return get_secret(key)


def get_nb_hosts():
    netbox_client = get_nb_client()
    hosts = []

    # Execute query and merge physical device + VM interfaces in one list.
    result = netbox_client.execute(
        query,
        variable_values={
            "tag": "managed_netdevice",
        },
    )

    for meta in result["device_list"]:
        platform = meta["platform"]["slug"]

        if platform not in VENDOR_MAP:
            raise ValueError("Invalid host type " + platform)

        hostclass = VENDOR_MAP[platform]
        hosts.append(hostclass.from_netbox_meta(meta, result["vlan_list"]))

    return hosts


@click.command()
@click.option("--push-config", default=False, is_flag=True)
@click.option("--push-host", default=None)
def main(
    network_filename: str = "network.yml",
    push_config: bool = False,
    push_host: str = None,
):
    # Load network file
    hosts, links, global_meta = load_network(network_filename)

    # Merge network file with Netbox switches.
    hosts.extend(get_nb_hosts())

    # Get secrets from Vault.
    secrets = VaultSecrets()

    # Render configs
    for host in hosts:
        log = logging.getLogger(host.hostname)

        if push_host and host.hostname != push_host:
            log.debug("Skipping host %s", host.hostname)
            continue

        role_meta = {}

        # vpn role meta
        if host.role == "vpn":
            role_meta["vpn_links"] = [
                link for link in links if host == link.side_a or host == link.side_b
            ]

        # Ignore untemplatable devices.
        if not host.is_templatable:
            continue

        # config render
        os.makedirs(f"output/{host.role}/cloud_init", exist_ok=True)

        rendered_config = render_template(
            f"{host.role}/{host.devicetype}.j2",
            device=host,
            secrets=secrets,
            global_meta=global_meta,
            **role_meta,
        )

        # write config file
        with open(f"output/{host.role}/" + host.hostname, "w") as f:
            f.write(rendered_config)
            log.info("Config saved.")

        # write cloud-init file
        with open(f"output/{host.role}/cloud_init/" + host.hostname, "w") as f:
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
