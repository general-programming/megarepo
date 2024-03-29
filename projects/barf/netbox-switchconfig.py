#!/usr/bin/env python3
import logging

from barf.common import render_template
from barf.vendors import VENDOR_MAP
from gql import gql

from common import get_nb_client

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()

client = get_nb_client()


query = gql(
    """
query ($tag: [String]) {
  device_list(tag: $tag) {
    name
    serial
    asset_tag
    config_context

    primary_ip4 {
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

if __name__ == "__main__":
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    result = client.execute(
        query,
        variable_values={
            "tag": "managed_netdevice",
        },
    )

    for meta in result["device_list"]:
        platform = meta["platform"]["slug"]
        secrets = {}

        if platform not in VENDOR_MAP:
            raise ValueError("Invalid host type " + platform)

        hostclass = VENDOR_MAP[platform]

        device = hostclass.from_netbox_meta(meta)

        print(
            render_template(
                f"network_devices/{platform}.j2",
                device=device,
                interfaces=device.interfaces,
                secrets=secrets,
            )
        )
