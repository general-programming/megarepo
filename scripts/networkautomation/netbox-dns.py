#!/usr/bin/env python3
import logging
import sys

from gql import gql

from common import get_nb_client, clean_hostname

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()

client = get_nb_client()


def generate_dns(hostname: str, ipv4: str, ipv6: str):
    return {
        "hostname": hostname,
        "ipv4": ipv4,
        "ipv6": ipv6,
    }

def create_zone(leases):
    print("$ORIGIN generalprogramming.org.")
    print("$TTL 3600")

    for lease in leases:
        hostname = lease["hostname"].lower()

        if lease["ipv6"]:
            lease_type = "AAAA"
            print(f"{hostname}\t{lease_type}\t{lease['ipv6']}")

        if lease["ipv4"]:
            lease_type = "A"
            print(f"{hostname}\t{lease_type}\t{lease['ipv4']}")


def non_static_host(hostname: str) -> bool:
    result = False

    if (
        "-ap-" in hostname
        or "-phone-" in hostname
        or "-temp-" in hostname
        or hostname.startswith("das-")
    ):
        result = True

    return result

query = gql("""
query {
  device_list {
    name
    primary_ip4 {
      address
    }
    primary_ip6 {
      address
    }
  }

  virtual_machine_list {
    name
    primary_ip4 {
      address
    }
    primary_ip6 {
      address
    }
  }
}
""")

if __name__ == "__main__":
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    result = client.execute(query)
    interfaces = result["device_list"] + result["virtual_machine_list"]

    # Iterate through all interfaces.
    for interface in interfaces:
        # Ignore hosts that should not have static IPs.
        if non_static_host(interface["name"]):
            continue

        # Ignore interfaces without primary IPs.
        if not interface["primary_ip4"] and not interface["primary_ip6"]:
            print(interface["name"], "missing primary ip.", file=sys.stderr)
            continue

        # Try to get the IP.
        ipv4 = (interface.get("primary_ip4") or {}).get("address", "").split("/")[0]
        ipv6 = (interface.get("primary_ip6") or {}).get("address", "").split("/")[0]

        # Device name for physical / virt.
        device_name = interface["name"]

        # Clean the names for DHCPd.
        interface_name = clean_hostname(interface["name"])
        device_name = clean_hostname(device_name)
        hostname = f"{device_name}-{interface_name}".lower()

        leases.append(generate_dns(
            hostname=device_name,
            ipv4=ipv4,
            ipv6=ipv6,
        ))

    create_zone(leases)
