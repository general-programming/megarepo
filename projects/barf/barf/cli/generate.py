import json
import logging
import sys

import click
from gql import gql

from barf.common import render_template
from barf.util.netbox import IPAMHost, clean_hostname, get_nb_client
from barf.vendors import VENDOR_MAP

log = logging.getLogger(__name__)

_DNS_QUERY = gql("""
query {
  device_list {
    name
    primary_ip4 { address }
    primary_ip6 { address }
    interfaces {
      name
      ip_addresses { address }
    }
  }
  virtual_machine_list {
    name
    primary_ip4 { address }
    primary_ip6 { address }
  }
}
""")

_DHCP_QUERY = gql("""
query {
  interface_list {
    primary_mac_address { mac_address }
    name
    device { name }
    ip_addresses { address }
  }
  vm_interface_list {
    primary_mac_address { mac_address }
    name
    virtual_machine { name }
    ip_addresses { address }
  }
}
""")

_CONFIG_QUERY = gql("""
query ($tag: [String]) {
  device_list(tag: $tag) {
    name
    serial
    asset_tag
    config_context
    primary_ip4 { address }
    platform { slug name }
    tags { name }
    interfaces {
      name
      type
      description
      mode
      lag { name }
      tagged_vlans { name vid }
      untagged_vlan { name vid }
      cable {
        id
        terminations {
          cable_end
          _device { name }
        }
      }
      ip_addresses { address }
      vrf { name }
    }
  }
}
""")


def _non_static_host(hostname: str) -> bool:
    return (
        "-ap-" in hostname
        or "-phone-" in hostname
        or "-temp-" in hostname
        or hostname.startswith("das-")
    )


def _dns_entries(leases, output_json: bool = False):
    for lease in leases:
        hostname = lease.clean_hostname.lower()
        fqdn = f"{hostname}.generalprogramming.org"

        if lease.ipv6:
            if output_json:
                yield {"address": {"fqdn": fqdn, "ip": lease.ipv6}}
            else:
                yield f"address=/{fqdn}/{lease.ipv6}"

        if lease.ipv4:
            reverse_arpa = ".".join(lease.ipv4.split(".")[::-1]) + ".in-addr.arpa"
            if output_json:
                yield {"address": {"fqdn": fqdn, "ip": lease.ipv4}}
                yield {"ptr_record": {"reverse_arpa": reverse_arpa, "fqdn": fqdn}}
            else:
                yield f"address=/{fqdn}/{lease.ipv4}"
                yield f"ptr-record={reverse_arpa},{fqdn}"

        if lease.ipmi_ip:
            reverse_arpa = ".".join(lease.ipmi_ip.split(".")[::-1]) + ".in-addr.arpa"
            if output_json:
                yield {"address": {"fqdn": f"ipmi.{fqdn}", "ip": lease.ipmi_ip}}
                yield {"ptr_record": {"reverse_arpa": reverse_arpa, "fqdn": fqdn}}
            else:
                yield f"address=/ipmi.{fqdn}/{lease.ipmi_ip}"
                yield f"ptr-record={reverse_arpa},{fqdn}"


@click.group()
def generate():
    """Generate DNS/DHCP/switch configs from Netbox."""
    pass


@generate.command("dns")
@click.option(
    "--json", "output_json", is_flag=True, help="Output JSON instead of dnsmasq format."
)
def generate_dns(output_json):
    """Generate dnsmasq DNS entries from Netbox."""
    client = get_nb_client()
    result = client.execute(_DNS_QUERY)
    hosts = result["device_list"] + result["virtual_machine_list"]

    leases = []
    for host in hosts:
        host_name = host["name"]

        if _non_static_host(host_name):
            continue

        if not host["primary_ip4"] and not host["primary_ip6"]:
            print(host_name, "missing primary ip.", file=sys.stderr)
            continue

        ipv4 = (host.get("primary_ip4") or {}).get("address", "").split("/")[0]
        ipv6 = (host.get("primary_ip6") or {}).get("address", "").split("/")[0]

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
                    log.warning("No IPMI IP for %s", host_name)
                    continue
                ipmi_ip = interface["ip_addresses"][0]["address"].split("/")[0]
                break

        leases.append(
            IPAMHost(hostname=host_name, ipv4=ipv4, ipv6=ipv6, ipmi_ip=ipmi_ip)
        )

    entries = list(_dns_entries(leases, output_json))

    if output_json:
        output = {"addresses": [], "ptr_records": []}
        for entry in entries:
            if entry.get("address"):
                output["addresses"].append(entry["address"])
            if entry.get("ptr_record"):
                output["ptr_records"].append(entry["ptr_record"])
        print(json.dumps(output, indent=2))
    else:
        for entry in entries:
            print(entry)


@generate.command("dhcp")
def generate_dhcp():
    """Generate DHCP lease entries from Netbox MAC/IP data."""
    client = get_nb_client()
    result = client.execute(_DHCP_QUERY)
    interfaces = result["interface_list"] + result["vm_interface_list"]

    leases = {"v4": [], "v6": []}

    for interface in interfaces:
        for address in interface["ip_addresses"]:
            try:
                ip_address = address["address"].split("/")[0]
            except IndexError:
                continue

            ip_family = "v6" if ":" in ip_address else "v4"

            if "device" in interface:
                device_name = interface["device"]["name"]
            else:
                device_name = interface["virtual_machine"]["name"]

            interface_name = clean_hostname(interface["name"])
            device_name_clean = clean_hostname(device_name)
            hostname = f"{device_name_clean}-{interface_name}".lower()

            if not interface["primary_mac_address"]:
                log.warning("%s missing MAC", ip_address)
                continue

            leases[ip_family].append(
                {
                    "host": hostname,
                    "hostname": device_name_clean,
                    "mac": interface["primary_mac_address"]["mac_address"],
                    "ip": ip_address,
                }
            )

    print(json.dumps(leases))


@generate.command("config")
def generate_config():
    """Generate device configs for Netbox-tagged managed switches."""
    client = get_nb_client()
    result = client.execute(_CONFIG_QUERY, variable_values={"tag": "managed_netdevice"})

    for meta in result["device_list"]:
        platform = meta["platform"]["slug"]

        if platform not in VENDOR_MAP:
            raise click.ClickException(f"Unsupported platform: {platform}")

        device = VENDOR_MAP[platform].from_netbox_meta(meta)
        print(
            render_template(
                f"network_devices/{platform}.j2",
                device=device,
                interfaces=device.interfaces,
                secrets={},
            )
        )
