"""Kea run_script hook -> Discord webhook bridge.

libdhcp_run_script.so invokes this as `webhook_adapter.py <hook-point>` with
lease details in LEASES{4,6}_AT<n>_* environment variables. Posts the same
JSON body the retired dnsmasq dhcp-script webhook did, so the receiving
Cloudflare Worker needs no changes.

DHCP must never block on this: hard 2s timeout, every failure exits 0.
"""

import json
import os
import socket
import sys
import urllib.request

ENV_FILE = "/run/vault-agent/kea-dhcp-webhook.env"


def webhook_url() -> str | None:
    """Return the WEBHOOK_URL value from the vault-agent env file."""
    try:
        with open(ENV_FILE) as f:
            for line in f:
                key, sep, value = line.strip().partition("=")
                if sep and key == "WEBHOOK_URL":
                    return value
    except OSError:
        pass
    return None


def leases(prefix: str) -> list[dict]:
    """Collect LEASES{4,6}_AT<n>_* env vars into lease dicts."""
    try:
        size = int(os.environ.get(f"{prefix}_SIZE", "0"))
    except ValueError:
        return []

    found = []
    for n in range(size):
        at = f"{prefix}_AT{n}_"
        ip = os.environ.get(f"{at}ADDRESS", "")
        if not ip:
            continue
        found.append(
            {
                "ip": ip,
                "mac": os.environ.get(f"{at}HWADDR", "") or "N/A",
                "hostname": (
                    os.environ.get(f"{at}HOSTNAME", "").rstrip(".") or "N/A"
                ),
            }
        )
    return found


def post(url: str, lease: dict) -> None:
    body = json.dumps(
        {
            "ip": lease["ip"],
            "hostname": lease["hostname"],
            "mac": lease["mac"],
            "dhcp_hostname": socket.gethostname(),
        }
    ).encode()

    request = urllib.request.Request(
        url,
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )

    try:
        urllib.request.urlopen(request, timeout=2)
    except Exception:
        # Swallow everything; a dead webhook must not break DHCP.
        pass


def main() -> None:
    if len(sys.argv) < 2:
        return

    hook_point = sys.argv[1]
    if hook_point == "leases4_committed":
        prefix = "LEASES4"
    elif hook_point == "leases6_committed":
        prefix = "LEASES6"
    else:
        return

    url = webhook_url()
    if not url:
        return

    for lease in leases(prefix):
        post(url, lease)


if __name__ == "__main__":
    main()
