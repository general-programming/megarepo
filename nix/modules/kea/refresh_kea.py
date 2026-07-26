"""Render Kea host reservations from NetBox.

Sibling of nix/modules/dns/refresh_dns.py (same NetBox GraphQL source and
hostname semantics) but emits Kea global-reservation JSON arrays instead of
dnsmasq dhcp-host lines: hosts4.json for Dhcp4 and hosts6.json for Dhcp6,
both keyed on hw-address so DUID churn (Talos mints a fresh DUID-LLT every
lease cycle) cannot rotate addresses. The NetBox API key is expected in
NETBOX_API_KEY, rendered to an EnvironmentFile by vault-agent.
"""

import argparse
import json
import os
import sys
import urllib.request
from collections.abc import Iterable

DHCP_QUERY = """
query {
  interface_list {
    primary_mac_address { mac_address }
    name
    device { name primary_ip4 { address } }
    ip_addresses { address }
  }
  vm_interface_list {
    primary_mac_address { mac_address }
    name
    virtual_machine { name primary_ip4 { address } }
    ip_addresses { address }
  }
}
"""


def clean_hostname(data: str) -> str:
    cleaned = data.replace(" ", "_").replace(":", "")
    return cleaned.replace("_-_", "_").replace("/", "_")


def strip_prefix(address: dict | None) -> str:
    return ((address or {}).get("address") or "").split("/")[0]


def dhcp_hostname(device: str, interface: str, is_primary: bool) -> str:
    """Hostname sent to the DHCP client for one reservation.

    The primary interface gets the bare device name: clients that take their
    hostname from DHCP (e.g. Talos) must not be renamed to device-interface,
    which re-registers kubelets as brand-new nodes. Secondary interfaces keep
    the interface suffix so hostnames stay unique per device.
    """
    if is_primary:
        name = clean_hostname(device).lower()
    else:
        name = f"{clean_hostname(device)}-{clean_hostname(interface)}".lower()
    return "".join(c if c.isalnum() or c == "-" else "-" for c in name)


def reservations(interfaces: Iterable[dict]) -> tuple[list[dict], list[dict]]:
    """Return (v4, v6) Kea reservation lists, one entry max per MAC."""
    seen_macs = set()
    hosts4: list[dict] = []
    hosts6: list[dict] = []

    for interface in interfaces:
        mac = (interface.get("primary_mac_address") or {}).get("mac_address")
        if not mac:
            continue

        owner = (
            interface.get("device") or interface.get("virtual_machine") or {}
        )
        if not owner.get("name"):
            continue

        primary_ip = strip_prefix(owner.get("primary_ip4"))

        # Kea rejects duplicate hw-address reservations within a family;
        # first interface with a usable address wins.
        if mac.lower() in seen_macs:
            continue

        ipv4 = ""
        ipv6 = ""
        for address in interface.get("ip_addresses") or []:
            ip_address = strip_prefix(address)
            if not ip_address:
                continue
            if ":" in ip_address:
                ipv6 = ipv6 or ip_address
            else:
                ipv4 = ipv4 or ip_address

        if not ipv4 and not ipv6:
            continue
        seen_macs.add(mac.lower())

        is_primary = bool(primary_ip) and ipv4 == primary_ip
        hostname = dhcp_hostname(owner["name"], interface["name"], is_primary)

        if ipv4:
            hosts4.append(
                {"hw-address": mac, "ip-address": ipv4, "hostname": hostname}
            )
        if ipv6:
            hosts6.append(
                {
                    "hw-address": mac,
                    "ip-addresses": [ipv6],
                    "hostname": hostname,
                }
            )

    return hosts4, hosts6


def query_netbox(url: str, token: str, gql_query: str) -> dict:
    request = urllib.request.Request(
        url,
        data=json.dumps({"query": gql_query}).encode(),
        headers={
            # NetBox >= 4.5 v2 tokens (nbt_<key>.<secret>) require Bearer.
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
            # Cloudflare bans urllib's default UA with error 1010.
            "User-Agent": "netbox-kea (megarepo nix/modules/kea)",
        },
    )

    with urllib.request.urlopen(request, timeout=60) as response:
        payload = json.load(response)

    if payload.get("errors"):
        raise RuntimeError(f"NetBox GraphQL errors: {payload['errors']}")
    return payload["data"]


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Render Kea reservations from NetBox."
    )
    parser.add_argument("--output4", required=True, help="hosts4.json path")
    parser.add_argument("--output6", required=True, help="hosts6.json path")
    args = parser.parse_args()

    default_url = "https://netbox.generalprogramming.org/graphql/"
    url = os.environ.get("NETBOX_URL", default_url)
    token = os.environ["NETBOX_API_KEY"]

    data = query_netbox(url, token, DHCP_QUERY)
    interfaces = data["interface_list"] + data["vm_interface_list"]
    hosts4, hosts6 = reservations(interfaces)

    if not hosts4 and not hosts6:
        # An empty NetBox response would wipe every reservation; fail and
        # keep the last-good files instead.
        print(
            "NetBox returned no usable reservations; refusing.",
            file=sys.stderr,
        )
        raise SystemExit(1)

    with open(args.output4, "w") as f:
        json.dump(hosts4, f, indent=1)
    with open(args.output6, "w") as f:
        json.dump(hosts6, f, indent=1)


if __name__ == "__main__":
    main()
