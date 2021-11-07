#!/usr/bin/env python3
import logging

from common import get_nb_client
from gql import gql

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()

client = get_nb_client()


def generate_lease(lease_name, hostname: str, mac: str, ip: str):
    return {
        "host": lease_name,
        "hostname": hostname,
        "mac": mac,
        "ip": ip,
    }


query = gql("""
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
}""")


def cisco_interface(interface: dict) -> str:
    result = []
    config_result = []

    # Generate the interface name.
    if interface["type"] == "LAG":
        interface_name = "Port-Channel" + interface["name"]
    else:
        interface_name = interface["name"]

    result.append(f"interface {interface_name}")

    # Handle switchport configs
    if interface["mode"] == "ACCESS":
        # config_result.append("switchport mode access")
        # config_result.append(f"switchport access vlan {interface['untagged_vlan']['vid']}")

        # oh no
        config_result.append("switchport mode trunk")
        config_result.append(f"switchport trunk native vlan {interface['untagged_vlan']['vid']}")
        config_result.append(f"switchport trunk allowed vlan {interface['untagged_vlan']['vid']}")
        config_result.append("spanning-tree portfast edge trunk")
    elif interface["mode"] == "TAGGED_ALL":
        config_result.append("switchport mode trunk")
    elif interface["mode"] == "TAGGED":
        config_result.append("switchport mode trunk")
        # TODO: Handle tagged VLANs

    # Port channel handling.
    if interface["lag"]:
        result.append("channel-group " + interface["lag"]["name"])

    # Description generation - Netbox description and cable.
    description = interface.get("description", "")

    if interface["cable"]:
        cable_description = (
            interface["cable"]["_termination_a_device"]["name"]
            + " -> "
            + interface["cable"]["_termination_b_device"]["name"]
        )
        if description:
            description += " (" + cable_description + ")"
        else:
            description = cable_description

    if description:
        result.append("description " + description)

    # Shutdown or not shutdown the port if it is configured.
    if config_result:
        result.extend(config_result)
        result.append("no shutdown")
    else:
        result.append("shutdown")

    # Generate config.
    result.append("!")
    return "\n".join(result)


if __name__ == "__main__":
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    result = client.execute(query, variable_values={
        "device_name": "fmt2-con-sw-140752-1"
    })
    for interface in result["interface_list"]:
        print(cisco_interface(interface))
