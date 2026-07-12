"""Regenerate dnsmasq DNS entries + DHCP reservations from NetBox.

Stdlib-only sibling of `barf generate dns` / `barf generate dhcp` so core
machines can refresh their dnsmasq config without barf (and its heavy
dependency set) installed. The NetBox API key is expected in
NETBOX_API_KEY, rendered to an EnvironmentFile by vault-agent.
"""

import argparse
import json
import os
import sys
import urllib.request
from collections.abc import Iterable, Iterator

DNS_QUERY = """
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
"""

DHCP_QUERY = """
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
"""

IPMI_INTERFACE_NAMES = {"ipmi", "idrac", "ilo", "drac", "bmc", "imm"}


def clean_hostname(data: str) -> str:
    return data.replace(" ", "_").replace(":", "").replace("_-_", "_").replace("/", "_")


def is_non_static(hostname: str) -> bool:
    return (
        "-ap-" in hostname
        or "-phone-" in hostname
        or "-temp-" in hostname
        or hostname.startswith("das-")
    )


def reverse_arpa(ipv4: str) -> str:
    return ".".join(ipv4.split(".")[::-1]) + ".in-addr.arpa"


def strip_prefix(address: dict | None) -> str:
    return ((address or {}).get("address") or "").split("/")[0]


def ipmi_ip(host: dict) -> str:
    for interface in host.get("interfaces") or []:
        if interface["name"].lower() not in IPMI_INTERFACE_NAMES:
            continue
        addresses = interface.get("ip_addresses") or []
        if not addresses:
            continue
        return strip_prefix(addresses[0])
    return ""


def dhcp_hostname(device: str, interface: str) -> str:
    name = f"{clean_hostname(device)}-{clean_hostname(interface)}".lower()
    return "".join(c if c.isalnum() or c == "-" else "-" for c in name)


def dns_lines(hosts: Iterable[dict], domain: str) -> Iterator[str]:
    for host in hosts:
        hostname = host["name"]
        if is_non_static(hostname):
            continue

        ipv4 = strip_prefix(host.get("primary_ip4"))
        ipv6 = strip_prefix(host.get("primary_ip6"))
        if not ipv4 and not ipv6:
            print(hostname, "missing primary ip.", file=sys.stderr)
            continue

        fqdn = f"{clean_hostname(hostname).lower()}.{domain}"

        if ipv6:
            yield f"address=/{fqdn}/{ipv6}"

        if ipv4:
            yield f"address=/{fqdn}/{ipv4}"
            yield f"ptr-record={reverse_arpa(ipv4)},{fqdn}"

        ipmi = ipmi_ip(host)
        if ipmi:
            yield f"address=/ipmi.{fqdn}/{ipmi}"
            yield f"ptr-record={reverse_arpa(ipmi)},{fqdn}"


def dhcp_lines(interfaces: Iterable[dict]) -> Iterator[str]:
    seen_macs = set()

    for interface in interfaces:
        mac = (interface.get("primary_mac_address") or {}).get("mac_address")
        if not mac:
            continue

        owner = interface.get("device") or interface.get("virtual_machine") or {}
        if not owner.get("name"):
            continue

        for address in interface.get("ip_addresses") or []:
            ip_address = strip_prefix(address)
            # dnsmasq matches DHCPv6 reservations on DUID, not MAC; v4 only.
            if not ip_address or ":" in ip_address:
                continue

            # dnsmasq rejects multiple v4 reservations per MAC.
            if mac.lower() in seen_macs:
                continue
            seen_macs.add(mac.lower())

            hostname = dhcp_hostname(owner["name"], interface["name"])
            yield f"dhcp-host={mac},{ip_address},{hostname}"


def render(dns_data: dict, dhcp_data: dict, domain: str) -> str:
    hosts = dns_data["device_list"] + dns_data["virtual_machine_list"]
    interfaces = dhcp_data["interface_list"] + dhcp_data["vm_interface_list"]

    lines = ["# Generated from NetBox by netbox-dnsmasq. Do not edit."]
    lines += dns_lines(hosts, domain)
    lines += dhcp_lines(interfaces)
    return "\n".join(lines) + "\n"


def query_netbox(url: str, token: str, gql_query: str) -> dict:
    request = urllib.request.Request(
        url,
        data=json.dumps({"query": gql_query}).encode(),
        headers={
            # NetBox >= 4.5 v2 tokens (nbt_<key>.<secret>) require Bearer.
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
    )

    with urllib.request.urlopen(request, timeout=60) as response:
        payload = json.load(response)

    if payload.get("errors"):
        raise RuntimeError(f"NetBox GraphQL errors: {payload['errors']}")
    return payload["data"]


def main() -> None:
    parser = argparse.ArgumentParser(description="Generate dnsmasq config from NetBox.")
    parser.add_argument("--output", default="-", help="File to write; '-' for stdout.")
    args = parser.parse_args()

    url = os.environ.get("NETBOX_URL", "https://netbox.generalprogramming.org/graphql/")
    domain = os.environ.get("DNS_DOMAIN", "generalprogramming.org")
    token = os.environ["NETBOX_API_KEY"]

    output = render(
        query_netbox(url, token, DNS_QUERY),
        query_netbox(url, token, DHCP_QUERY),
        domain,
    )

    if args.output == "-":
        sys.stdout.write(output)
    else:
        with open(args.output, "w") as f:
            f.write(output)


if __name__ == "__main__":
    main()
