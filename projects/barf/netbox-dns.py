#!/usr/bin/env python3
import logging
import sys
from typing import Generator

from gql import gql

from common import IPAMHost, get_nb_client

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()

client = get_nb_client()


def create_zone(leases) -> Generator[IPAMHost, None, None]:
    for lease in leases:
        hostname = lease.clean_hostname.lower()
        fqdn = f"{hostname}.generalprogramming.org"

        if lease.ipv6:
            yield f"address=/{fqdn}/{lease.ipv6}"

        if lease.ipv4:
            reverse_arpa = ".".join(lease.ipv4.split(".")[::-1]) + ".in-addr.arpa"
            yield f"address=/{fqdn}/{lease.ipv4}"
            yield f"ptr-record={reverse_arpa},{fqdn}"

        if lease.ipmi_ip:
            reverse_arpa = ".".join(lease.ipmi_ip.split(".")[::-1]) + ".in-addr.arpa"
            yield f"address=/ipmi.{fqdn}/{lease.ipmi_ip}"
            yield f"ptr-record={reverse_arpa},{fqdn}"


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


query = gql(
    """
query {
  device_list {
    name
    primary_ip4 {
      address
    }
    primary_ip6 {
      address
    }
    interfaces {
      name
      ip_addresses {
        address
      }
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
"""
)

if __name__ == "__main__":
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    result = client.execute(query)
    hosts = result["device_list"] + result["virtual_machine_list"]

    # Iterate through all interfaces.
    for host in hosts:
        host_name = host["name"]

        # Ignore hosts that should not have static IPs.
        if non_static_host(host_name):
            continue

        # Ignore interfaces without primary IPs.
        if not host["primary_ip4"] and not host["primary_ip6"]:
            print(host_name, "missing primary ip.", file=sys.stderr)
            continue

        # Try to get the IP.
        ipv4 = (host.get("primary_ip4") or {}).get("address", "").split("/")[0]
        ipv6 = (host.get("primary_ip6") or {}).get("address", "").split("/")[0]

        # Get the IPMI IP address.
        ipmi_ip = None
        for interface in host.get("interfaces", []):
            if interface["name"].lower() in [
                "ipmi",
                "idrac",
                "ilo",
                "drac",
                "bmc",
                "imm",
            ]:
                if not interface["ip_addresses"]:
                    log.warn("No IPMI IP for %s", host_name)
                    continue
                ipmi_ip = interface["ip_addresses"][0]["address"].split("/")[0]
                break

        # Create host dataclass.
        leases.append(
            IPAMHost(
                hostname=host_name,
                ipv4=ipv4,
                ipv6=ipv6,
                ipmi_ip=ipmi_ip,
            )
        )

    for entry in create_zone(leases):
        print(entry)
