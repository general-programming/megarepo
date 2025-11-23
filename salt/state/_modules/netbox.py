import logging
from dataclasses import dataclass
from typing import Generator

from gql import Client, gql
from gql.transport.aiohttp import AIOHTTPTransport

logging.basicConfig(level=logging.WARNING)
log = logging.getLogger()


__virtualname__ = "netbox"


def __virtual__():
    return __virtualname__


def _get_nb_client() -> Client:
    # Select your transport with a defined url endpoint
    transport = AIOHTTPTransport(
        url="https://netbox.generalprogramming.org/graphql/",
        headers={
            "Authorization": f"Token {__salt__['vault.read_secret']('salt/kv/data/netbox_ro')['secret']}"
        },
    )

    # Create a GraphQL client using the defined transport
    return Client(transport=transport, fetch_schema_from_transport=True)


def _clean_hostname(data: str) -> str:
    return data.replace(" ", "_").replace(":", "").replace("_-_", "_").replace("/", "_")


def _generate_lease(lease_name, hostname: str, mac: str, ip: str):
    return {
        "host": lease_name,
        "hostname": hostname,
        "mac": mac,
        "ip": ip,
    }


@dataclass
class _IPAMHost:
    hostname: str
    ipv4: str = None
    ipv6: str = None
    mac: str = None
    ipmi_ip: str = None

    @property
    def clean_hostname(self):
        return _clean_hostname(self.hostname)


def _create_zone(leases, output_json: bool = False) -> Generator[_IPAMHost, None, None]:
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


def _non_static_host(hostname: str) -> bool:
    result = False

    if (
        "-ap-" in hostname
        or "-phone-" in hostname
        or "-temp-" in hostname
        or hostname.startswith("das-")
    ):
        result = True

    return result


dns_query = gql(
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


dhcp_query = gql(
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


def get_leases():
    leases = []

    # Execute query and merge physical device + VM interfaces in one list.
    client = _get_nb_client()
    result = client.execute(dns_query)
    hosts = result["device_list"] + result["virtual_machine_list"]

    # Iterate through all interfaces.
    for host in hosts:
        host_name = host["name"]

        # Ignore hosts that should not have static IPs.
        if _non_static_host(host_name):
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
            _IPAMHost(
                hostname=host_name,
                ipv4=ipv4,
                ipv6=ipv6,
                ipmi_ip=ipmi_ip,
            )
        )

    entries = list(_create_zone(leases, True))

    return entries


def get_dhcp_leases():
    leases = {
        "v4": [],
        "v6": [],
    }

    # Execute query and merge physical device + VM interfaces in one list.
    client = _get_nb_client()
    result = client.execute(dhcp_query)
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
            interface_name = _clean_hostname(interface["name"])
            device_name = _clean_hostname(device_name)
            hostname = f"{device_name}-{interface_name}".lower()

            # Do not use IPs that do not have MAC addresses.
            if not interface["primary_mac_address"]:
                log.warning(f"{ip_address} missing MAC")
                continue

            leases[ip_family].append(
                _generate_lease(
                    lease_name=hostname,
                    hostname=device_name,
                    mac=interface["primary_mac_address"]["mac_address"],
                    ip=ip_address,
                )
            )

    return leases
