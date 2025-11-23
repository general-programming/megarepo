import ipaddress
import logging
from dataclasses import dataclass
from typing import Generator

from gql import Client, gql
from gql.transport.aiohttp import AIOHTTPTransport
from saltext.vault.utils.vault import read_kv

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()


def _get_nb_client() -> Client:
    # Select your transport with a defined url endpoint
    transport = AIOHTTPTransport(
        url="https://netbox.generalprogramming.org/graphql/",
        headers={
            "Authorization": f"Token {read_kv('salt/kv/data/netbox_ro')['secret']}"
        },
    )

    # Create a GraphQL client using the defined transport
    return Client(transport=transport, fetch_schema_from_transport=True)


def is_internal(address):
    return ipaddress.ip_network(address, strict=False).is_private


def clean_hostname(data: str) -> str:
    return data.replace(" ", "_").replace(":", "").replace("_-_", "_").replace("/", "_")


@dataclass
class IPAMHost:
    hostname: str
    ipv4: str = None
    ipv6: str = None
    mac: str = None
    ipmi_ip: str = None

    @property
    def clean_hostname(self):
        return clean_hostname(self.hostname)


def create_zone(leases, output_json: bool = False) -> Generator[IPAMHost, None, None]:
    for lease in leases:
        hostname = lease.clean_hostname.lower()
        fqdn = f"{hostname}.generalprogramming.org"

        if lease.ipv6:
            if output_json:
                yield {
                    "address": {
                        "fqdn": fqdn,
                        "ip": lease.ipv6,
                    }
                }
            else:
                yield f"address=/{fqdn}/{lease.ipv6}"

        if lease.ipv4:
            reverse_arpa = ".".join(lease.ipv4.split(".")[::-1]) + ".in-addr.arpa"
            if output_json:
                yield {
                    "address": {
                        "fqdn": fqdn,
                        "ip": lease.ipv4,
                    }
                }
                yield {
                    "ptr_record": {
                        "reverse_arpa": reverse_arpa,
                        "fqdn": fqdn,
                    },
                }
            else:
                yield f"address=/{fqdn}/{lease.ipv4}"
                yield f"ptr-record={reverse_arpa},{fqdn}"

        if lease.ipmi_ip:
            reverse_arpa = ".".join(lease.ipmi_ip.split(".")[::-1]) + ".in-addr.arpa"
            if output_json:
                yield {
                    "address": {
                        "fqdn": f"ipmi.{fqdn}",
                        "ip": lease.ipmi_ip,
                    }
                }
                yield {
                    "ptr_record": {
                        "reverse_arpa": reverse_arpa,
                        "fqdn": fqdn,
                    },
                }
            else:
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


def netbox_leases():
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    client = _get_nb_client()
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
            log.debug("Host %s missing primary ip.", host_name)
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

    entries = list(create_zone(leases, True))

    grains = {
        "netbox_dns": {
            "addresses": [],
            "ptr_records": [],
        }
    }
    for entry in entries:
        if entry.get("address"):
            grains["netbox_dns"]["addresses"].append(entry["address"])
        if entry.get("ptr_record"):
            grains["netbox_dns"]["ptr_records"].append(entry["ptr_record"])

    return grains
