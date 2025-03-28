#!/usr/bin/env python3
import json
import logging

from gql import gql

from common import clean_hostname, get_nb_client

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


query = gql(
    """
query {
  interface_list {
    primary_mac_address {
      mac_address
    }
    name
    device { name }
    ip_addresses {
      address
    }
  }
  vm_interface_list {
    primary_mac_address {
      mac_address
    }
    name
    virtual_machine { name }
    ip_addresses {
      address
    }
  }
}"""
)

if __name__ == "__main__":
    leases = {
        "v4": [],
        "v6": [],
    }

    # Execute query and merge physical device + VM interfaces in one list.
    result = client.execute(query)
    interfaces = result["interface_list"] + result["vm_interface_list"]

    # Iterate through all interfaces.
    for interface in interfaces:
        for address in interface["ip_addresses"]:
            # Try to get the IP.
            try:
                ip_address = address["address"].split("/")[0]
            except IndexError:
                continue

            # handle for IPv6 IPs.
            if ":" in ip_address:
                ip_family = "v6"
            else:
                ip_family = "v4"

            # Device name for physical / virt.
            if "device" in interface:
                device_name = interface["device"]["name"]
            else:
                device_name = interface["virtual_machine"]["name"]

            # Clean the names for DHCPd.
            interface_name = clean_hostname(interface["name"])
            device_name = clean_hostname(device_name)
            hostname = f"{device_name}-{interface_name}".lower()

            # Do not use IPs that do not have MAC addresses.
            if not interface["primary_mac_address"]:
                log.warning(f"{ip_address} missing MAC")
                continue

            leases[ip_family].append(
                generate_lease(
                    lease_name=hostname,
                    hostname=device_name,
                    mac=interface["primary_mac_address"]["mac_address"],
                    ip=ip_address,
                )
            )

    print(json.dumps(leases))
