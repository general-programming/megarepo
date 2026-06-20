import logging

import click
import yaml

from barf.vendors import VENDOR_MAP

log = logging.getLogger(__name__)


@click.command()
@click.argument("filename", default="network.yml", type=click.Path(exists=True))
def validate(filename):
    """Validate a network.yml file."""
    with open(filename) as f:
        network = yaml.safe_load(f)

    errors = []

    if "global_meta" not in network:
        errors.append("missing 'global_meta' section")

    hosts = []
    if "hosts" not in network:
        errors.append("missing 'hosts' section")
    else:
        for hostname, meta in network["hosts"].items():
            if meta["type"] not in VENDOR_MAP:
                errors.append(f"host '{hostname}': unsupported type '{meta['type']}'")
                continue
            hostclass = VENDOR_MAP[meta["type"]]
            try:
                hosts.append(hostclass.from_meta(hostname=hostname, meta=meta))
            except Exception as e:
                errors.append(f"host '{hostname}': {e}")

    if "links" in network:
        host_map = {h.hostname: h for h in hosts}
        for link_id, link in network["links"].items():
            for side in ("side_a", "side_b"):
                name = link.get(side)
                if name and name not in host_map:
                    errors.append(
                        f"link '{link_id}': {side} '{name}' not found in hosts"
                    )

    if errors:
        for error in errors:
            click.echo(f"ERROR: {error}", err=True)
        raise SystemExit(1)

    host_count = len(network.get("hosts", {}))
    link_count = len(network.get("links", {}))
    click.echo(f"OK: {filename} is valid ({host_count} hosts, {link_count} links).")
