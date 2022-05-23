#!/usr/bin/env python3
import logging

from barf.common import render_template
from gql import gql

from common import get_nb_client

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()

client = get_nb_client()


query = gql(
    """
query ($device_name: [String]) {
  device_list (name: $device_name) {
    name
    serial
    asset_tag
  }
  interface_list (device: $device_name) {
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
      _termination_a_device {
        name
      }
      _termination_b_device {
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
            "device_name": "fmt2-con-sw-140752-1",
        },
    )

    device_role = "con-sw"
    device_type = "cisco"

    print(
        render_template(
            f"{device_role}/{device_type}.j2",
            interfaces=result["interface_list"],
        )
    )
