"""dnsmasq dhcp-script Discord webhook.

Port of salt/state/dhcp_server/files/dhcpd_discordhook.py.j2 (isc-dhcpd
on-commit hook) for dnsmasq. dnsmasq invokes this as:

    webhook.py add|old|del <mac> <ip> [hostname]

Posts on add/old (the receiving Cloudflare Worker dedupes by IP+MAC), does
nothing on del. The webhook URL is read from an env file rendered by
vault-agent since dnsmasq passes no environment to the script.

DHCP must never block on this: hard 2s timeout, every failure exits 0.
"""

import json
import socket
import sys
import urllib.request

ENV_FILE = "/run/vault-agent/dhcp-webhook.env"


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


def main() -> None:
    if len(sys.argv) < 4:
        return

    action, mac, ip = sys.argv[1:4]
    hostname = sys.argv[4] if len(sys.argv) > 4 else ""

    if action not in ("add", "old"):
        return

    url = webhook_url()
    if not url:
        return

    body = json.dumps(
        {
            "ip": ip,
            "hostname": hostname or "N/A",
            "mac": mac,
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


if __name__ == "__main__":
    main()
