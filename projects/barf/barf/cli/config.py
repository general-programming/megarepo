"""``barf config``: generate, diff, and deploy device configs."""

import logging
from concurrent.futures import ThreadPoolExecutor
from typing import List, Tuple

import click

from barf.cli.common import print_table, resolve_targets
from barf.util.network import load_network
from barf.util.secrets import VaultSecrets
from barf.util.render import (
    prefetch_link_keys,
    render_host_config,
    write_rendered_config,
)
from barf.vendors import BaseHost

log = logging.getLogger(__name__)


def _load_targets(
    filename: str, targets: Tuple[str, ...]
) -> Tuple[List[BaseHost], list, dict]:
    """Load network.yml and select the templatable hosts for TARGETS."""
    hosts, links, global_meta = load_network(filename)
    selected = [h for h in resolve_targets(hosts, targets) if h.is_templatable]
    if not selected:
        raise click.ClickException(
            f"no templatable devices selected by {', '.join(targets)!r}"
        )
    # Pull the WG keypairs for every involved link concurrently; the
    # serial per-secret Vault reads otherwise dominate render time.
    prefetch_link_keys(selected, links)
    return selected, links, global_meta


def _secrets():
    """The Vault secret fetcher (a seam tests monkeypatch)."""
    return VaultSecrets()


_FILENAME_OPTION = click.option(
    "--filename",
    default="network.yml",
    show_default=True,
    type=click.Path(exists=True),
)


@click.group()
def config():
    """Generate, diff, and deploy device configs from network.yml."""
    pass


@config.command("generate")
@click.argument("targets", nargs=-1, required=True)
@_FILENAME_OPTION
def config_generate(targets: Tuple[str, ...], filename: str) -> None:
    """Render configs for TARGETS (hostnames, or "all") into output/."""
    targets_hosts, links, global_meta = _load_targets(filename, targets)
    secrets = _secrets()

    for host in targets_hosts:
        rendered = render_host_config(host, links, global_meta, secrets)
        path = write_rendered_config(host, rendered)
        click.echo(f"[{host.hostname}] wrote {path}")


def _render_and_diff(
    host: BaseHost,
    links: list,
    global_meta: dict,
    secrets,
    show_device_only: bool,
    show_secrets: bool,
):
    """Render + diff one host; exceptions are returned, not raised.

    Runs on a worker thread (see ``config_diff``), so errors are
    handed back to the main thread to report instead of blowing up
    ``future.result()`` mid-loop.
    """
    rendered = render_host_config(host, links, global_meta, secrets)
    try:
        return host.diff_config(
            rendered,
            redact=not show_secrets,
            show_device_only=show_device_only,
        )
    except Exception as e:  # noqa: BLE001 - handed back to the main thread
        return e


@config.command("diff")
@click.argument("targets", nargs=-1, required=True)
@_FILENAME_OPTION
@click.option(
    "--show-device-only",
    is_flag=True,
    help="Also list config that only exists on the device.",
)
@click.option(
    "--show-secrets",
    is_flag=True,
    help="Do not redact secret values in the diff output.",
)
def config_diff(
    targets: Tuple[str, ...], filename: str, show_device_only: bool, show_secrets: bool
) -> None:
    """Diff rendered configs against TARGETS (hostnames, or "all")."""
    targets_hosts, links, global_meta = _load_targets(filename, targets)
    secrets = _secrets()

    # Each diff is dominated by a network round trip to an independent
    # device; running the fleet serially means paying that latency
    # once per host instead of once, total.
    with ThreadPoolExecutor(max_workers=min(16, len(targets_hosts))) as pool:
        outcomes = list(
            pool.map(
                lambda host: _render_and_diff(
                    host, links, global_meta, secrets, show_device_only, show_secrets
                ),
                targets_hosts,
            )
        )

    results = []
    failed = False
    for host, outcome in zip(targets_hosts, outcomes):
        if isinstance(outcome, NotImplementedError):
            results.append([host.hostname, "skipped: no diff support"])
            continue
        if isinstance(outcome, Exception):
            results.append([host.hostname, f"failed: {outcome}"])
            failed = True
            continue

        diff = outcome
        if diff.text.strip():
            click.echo(f"--- {host.hostname} ---")
            click.echo(diff.text)
            click.echo("")
        results.append([host.hostname, diff.summary])

    print_table(["DEVICE", "DIFF"], results)

    if failed:
        raise SystemExit(1)


@config.command("deploy")
@click.argument("targets", nargs=-1, required=True)
@_FILENAME_OPTION
@click.option(
    "--yes", is_flag=True, help="Do not ask for confirmation before deploying."
)
def config_deploy(targets: Tuple[str, ...], filename: str, yes: bool) -> None:
    """Deploy rendered configs to TARGETS (hostnames, or "all").

    For vendors with declarative ownership (VyOS), the rendered config
    is the whole truth: stale device config is deleted, except under
    the vendor's kept prefixes (device-managed config and sections not
    yet modeled in network.yml), which are merged and never deleted.
    The diff is shown and confirmed per device before anything is
    pushed.
    """
    targets_hosts, links, global_meta = _load_targets(filename, targets)
    secrets = _secrets()

    results = []
    failed = False
    for host in targets_hosts:
        rendered = render_host_config(host, links, global_meta, secrets)
        try:
            diff = host.diff_config(rendered)
        except NotImplementedError:
            results.append([host.hostname, "skipped: no deploy support"])
            continue
        except Exception as e:  # noqa: BLE001 - report per-device failures
            results.append([host.hostname, f"diff failed: {e}"])
            failed = True
            continue

        if not diff.has_changes:
            results.append([host.hostname, "no changes"])
            continue

        click.echo(f"--- {host.hostname} ---")
        click.echo(diff.text)
        if not yes and not click.confirm(f"[{host.hostname}] deploy this config?"):
            results.append([host.hostname, "declined"])
            continue

        try:
            host.push_rendered_config(rendered)
        except NotImplementedError:
            # Vendors with diff support but no push yet (mikrotik):
            # a skip, not a failure -- keep `deploy all` usable.
            results.append([host.hostname, "skipped: no deploy support"])
            continue
        except Exception as e:  # noqa: BLE001 - report per-device failures
            results.append([host.hostname, f"deploy failed: {e}"])
            failed = True
            continue

        results.append([host.hostname, f"deployed ({diff.summary})"])

    click.echo("")
    print_table(["DEVICE", "RESULT"], results)

    if failed:
        raise SystemExit(1)
