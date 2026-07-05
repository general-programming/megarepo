import concurrent.futures
import logging

import click

from barf.util.network import load_network
from barf.util.secrets import VaultSecrets
from barf.util.vyos_api import vyos_api_show

log = logging.getLogger(__name__)


def _parse_version(output: str) -> str:
    """Pull the version string out of ``show version`` output."""
    for line in output.splitlines():
        if line.lower().startswith("version:"):
            return line.split(":", 1)[1].strip()
    return output.strip().splitlines()[0] if output.strip() else "-"


def _parse_uptime(output: str) -> str:
    """Pull a human uptime out of ``show system uptime`` output."""
    for line in output.splitlines():
        line = line.strip()
        if not line:
            continue
        if line.lower().startswith("uptime:"):
            return line.split(":", 1)[1].strip()
        return line
    return "-"


def _print_table(headers, rows):
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(str(cell)))

    fmt = "  ".join("{:<" + str(w) + "}" for w in widths)
    click.echo(fmt.format(*headers))
    click.echo(fmt.format(*["-" * w for w in widths]))
    for row in rows:
        click.echo(fmt.format(*[str(c) for c in row]))


@click.group()
def device():
    """Interact with live devices."""
    pass


@device.command("status")
@click.argument("filename", default="network.yml", type=click.Path(exists=True))
def device_status(filename):
    """Show management endpoint, uptime, and version for all VyOS devices."""
    hosts, _links, _global_meta = load_network(filename)
    secrets = VaultSecrets()
    key = secrets.vyos_api_password

    vyos_hosts = [host for host in hosts if host.devicetype == "vyos"]

    def fetch(host):
        log.debug("Processing device %s", host.hostname)
        address = host.management_ip
        if not address:
            return [host.hostname, "-", "-", "-", "error: no reachable address"]

        try:
            version = _parse_version(vyos_api_show(address, key, ["version"]))
            uptime = _parse_uptime(vyos_api_show(address, key, ["system", "uptime"]))
            return [host.hostname, address, uptime, version, "ok"]
        except Exception as e:  # noqa: BLE001 - report the failure in the table
            log.debug("Failed to reach %s via %s: %s", host.hostname, address, e)
            return [host.hostname, address, "-", "-", f"error: {e}"]

    rows = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        for row in pool.map(fetch, vyos_hosts):
            rows.append(row)

    _print_table(["DEVICE", "ENDPOINT", "UPTIME", "VERSION", "STATUS"], rows)
