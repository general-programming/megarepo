import concurrent.futures
import logging
import time
from typing import Optional

import click

from barf.cli.common import print_table, resolve_targets
from barf.util.images import PROVIDERS
from barf.util.network import load_network
from barf.util.render import prefetch_link_keys, render_host_config
from barf.util.secrets import VaultSecrets
from barf.util.ssh import DeviceSSH
from barf.vendors import BaseHost

log = logging.getLogger(__name__)


@click.group()
def device():
    """Interact with live devices."""
    pass


@device.command("status")
@click.argument("filename", default="network.yml", type=click.Path(exists=True))
def device_status(filename: str) -> None:
    """Show endpoint, uptime, firmware, and config drift for VyOS devices."""
    hosts, links, global_meta = load_network(filename)

    # Fail fast on Vault problems in the main thread, and prime the shared
    # secret cache so the probes below never touch Vault themselves.
    VaultSecrets().vyos_api_password

    vyos_hosts = [host for host in hosts if host.devicetype == "vyos"]

    # Prime the release fetch once here; cached_property is not thread-safe.
    provider = PROVIDERS["vyos"]
    try:
        latest = provider.latest_version
    except Exception as e:  # noqa: BLE001 - the table is still useful offline
        log.warning("Could not fetch the latest VyOS release: %s", e)
        latest = None

    # Render in the main thread: templating may create missing secrets
    # in Vault, which must not race across pool workers. The read-only
    # WG key fetches are warmed concurrently first.
    prefetch_link_keys(vyos_hosts, links)
    secrets = VaultSecrets()
    rendered: dict[str, str] = {}
    render_errors: dict[str, str] = {}
    for host in vyos_hosts:
        try:
            rendered[host.hostname] = render_host_config(
                host, links, global_meta, secrets
            )
        except Exception as e:  # noqa: BLE001 - report per-device in the table
            render_errors[host.hostname] = str(e)

    def firmware_cell(version: str) -> str:
        if latest is None:
            return "?"
        if provider.is_current(version):
            return "yes"
        return f"no ({latest})"

    def config_cell(host: BaseHost) -> str:
        if host.hostname in render_errors:
            return f"render error: {render_errors[host.hostname]}"
        try:
            diff = host.diff_config(rendered[host.hostname])
        except Exception as e:  # noqa: BLE001 - report per-device in the table
            return f"error: {e}"
        return "yes" if not diff.has_changes else diff.summary

    def fetch(host: BaseHost) -> list:
        log.debug("Processing device %s", host.hostname)
        address = host.management_ip
        if not address:
            return [
                host.hostname,
                "-",
                "-",
                "-",
                "-",
                "-",
                "error: no reachable address",
            ]

        try:
            version = host.human_version()
            uptime = host.uptime()
        except Exception as e:  # noqa: BLE001 - report the failure in the table
            log.debug("Failed to reach %s via %s: %s", host.hostname, address, e)
            return [host.hostname, address, "-", "-", "-", "-", f"error: {e}"]

        return [
            host.hostname,
            address,
            uptime,
            version,
            firmware_cell(version),
            config_cell(host),
            "ok",
        ]

    rows = []
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
        for row in pool.map(fetch, vyos_hosts):
            rows.append(row)

    print_table(
        [
            "DEVICE",
            "ENDPOINT",
            "UPTIME",
            "VERSION",
            "LATEST FIRMWARE",
            "CONFIG CONSISTENT",
            "STATUS",
        ],
        rows,
    )


@device.command("ssh")
@click.argument("target")
@click.option(
    "--filename",
    default="network.yml",
    show_default=True,
    type=click.Path(exists=True),
)
def device_ssh(target: str, filename: str) -> None:
    """Open an interactive shell on TARGET (a hostname).

    Authenticates the same way deploys do: agent/local keys first as
    the vendor's SSH user, falling back to the shared supertech
    password from Vault. Exits with the remote shell's exit status.
    """
    if target == "all":
        raise click.ClickException("device ssh takes a single hostname")

    hosts, _links, _global_meta = load_network(filename)
    host = resolve_targets(hosts, target)[0]

    address = host.ssh_ip
    if not address:
        raise click.ClickException(f"{host.hostname}: no reachable SSH address")

    try:
        with DeviceSSH(host, address, username=host.SSH_USERNAME) as conn:
            click.echo(f"[{host.hostname}] connected as {conn.username}@{address}")
            status = conn.interactive_shell()
    except RuntimeError as e:
        raise click.ClickException(str(e)) from e

    if status:
        raise SystemExit(status)


def wait_for_device_alive(
    host: BaseHost,
    changed_from: Optional[str] = None,
    initial_wait: int = 15,
    interval: int = 5,
    timeout: int = 900,
) -> str:
    """Block until a device answers on its API, returning its version.

    Waits ``initial_wait`` seconds up front, then polls every ``interval``
    seconds. A rebooting device can keep answering on the old image for a
    while, so pass its pre-reboot version as ``changed_from``: polls
    reporting that version keep waiting instead of being taken as alive.
    """
    click.echo(f"[{host.hostname}] waiting for the device to come alive", nl=False)
    time.sleep(initial_wait)

    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        version = host.version()
        if version is not None and version != changed_from:
            click.echo("")
            return version
        click.echo(".", nl=False)
        time.sleep(interval)

    click.echo("")
    raise click.ClickException(f"{host.hostname} did not come back within {timeout}s")


@device.command("update")
@click.argument("target")
@click.option(
    "--stage",
    is_flag=True,
    help="Only preload the image onto devices; do not drain or reboot.",
)
@click.option(
    "--drain-wait",
    default=5,
    show_default=True,
    help="Seconds to wait after the BGP shutdown before rebooting.",
)
@click.option(
    "--yes", is_flag=True, help="Do not ask for confirmation before rebooting."
)
@click.option(
    "--filename",
    default="network.yml",
    show_default=True,
    type=click.Path(exists=True),
)
def device_update(
    target: str, stage: bool, drain_wait: int, yes: bool, filename: str
) -> None:
    """Update TARGET (a hostname, or "all") to the latest image."""
    hosts, _links, _global_meta = load_network(filename)

    # Fail fast on Vault problems in the main thread, and prime the shared
    # secret cache so probes and vendor calls never touch Vault themselves.
    VaultSecrets().vyos_api_password

    targets = []
    for host in resolve_targets(hosts, target):
        if host.image_provider is None:
            click.echo(
                f"skip {host.hostname}: no image provider for {host.devicetype!r}"
            )
            continue
        targets.append(host)

    if not targets:
        raise click.ClickException("no updatable devices selected")

    # Reboot leaves before spines so the spine safety gate stays meaningful.
    targets.sort(key=lambda h: h.is_spine)

    all_vyos = [h for h in hosts if h.devicetype == "vyos"]

    def run_update(host: BaseHost) -> str:
        provider = host.image_provider
        if provider is None:  # filtered above; keeps the type checker honest
            raise click.ClickException(f"{host.hostname}: no image provider")
        prefix = f"[{host.hostname}]"

        address = host.management_ip
        if not address:
            raise click.ClickException(f"{host.hostname}: no reachable address")

        click.echo(f"{prefix} checking running version via {address}")
        version = host.version()
        if version is None:
            raise click.ClickException(
                f"{host.hostname}: API unreachable via {address}"
            )
        if provider.is_current(version):
            return "already current"

        image = provider.download()
        latest = provider.latest_version

        if stage:
            return host.update_host(str(image), stage=True, version=latest)

        click.echo(f"{prefix} running fleet safety checks")
        host.safe_to_reboot(all_vyos)

        if not yes and not click.confirm(
            f"{prefix} install, drain BGP, and reboot now?"
        ):
            result = host.update_host(str(image), stage=True, version=latest)
            return f"{result} (reboot declined)"

        host.update_host(str(image), stage=False, drain_wait=drain_wait, version=latest)

        click.echo(f"{prefix} device rebooting")
        new_version = wait_for_device_alive(host, changed_from=version)
        if not provider.is_current(new_version):
            raise click.ClickException(
                f"{host.hostname} came back on {new_version!r}, expected {latest}"
            )

        warning = host.verify_routing()
        if warning:
            click.echo(f"{prefix} warning: {warning}")

        return f"updated to {latest}"

    results = []
    failed = False
    for host in targets:
        try:
            results.append([host.hostname, run_update(host)])
        except Exception as e:  # noqa: BLE001 - report per-device failures
            message = e.message if isinstance(e, click.ClickException) else str(e)
            results.append([host.hostname, f"failed: {message}"])
            failed = True
            if not stage:
                click.echo(
                    "aborting remaining devices: a failed update reduces"
                    " redundancy for the rest of the fleet",
                    err=True,
                )
                break

    click.echo("")
    print_table(["DEVICE", "RESULT"], results)

    if failed:
        raise SystemExit(1)


@device.command("cleanup")
@click.argument("target")
@click.option(
    "--filename",
    default="network.yml",
    show_default=True,
    type=click.Path(exists=True),
)
def device_cleanup(target: str, filename: str) -> None:
    """Run housekeeping on TARGET (a hostname, or "all").

    For VyOS this removes system images that are neither running nor the
    default boot image.
    """
    hosts, _links, _global_meta = load_network(filename)
    candidates = resolve_targets(hosts, target)

    results = []
    failed = False
    for host in candidates:
        try:
            actions = host.cleanup_host()
        except NotImplementedError as e:
            if target != "all":
                raise click.ClickException(str(e)) from e
            results.append([host.hostname, "skipped: no cleanup support"])
            continue
        except Exception as e:  # noqa: BLE001 - report per-device failures
            results.append([host.hostname, f"failed: {e}"])
            failed = True
            continue

        for action in actions:
            click.echo(f"[{host.hostname}] {action}")
        results.append([host.hostname, "; ".join(actions) or "nothing to do"])

    click.echo("")
    print_table(["DEVICE", "RESULT"], results)

    if failed:
        raise SystemExit(1)
