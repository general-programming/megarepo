"""``barf config``: generate, diff, and deploy device configs."""

import logging
from typing import List, Tuple

import click

from barf.cli.common import print_table, resolve_targets
from barf.util.network import load_network
from barf.util.render import render_host_config, write_rendered_config
from barf.vendors import BaseHost

log = logging.getLogger(__name__)


def _load_targets(filename: str, target: str) -> Tuple[List[BaseHost], list, dict]:
    """Load network.yml and select the templatable hosts for TARGET."""
    hosts, links, global_meta = load_network(filename)
    targets = [h for h in resolve_targets(hosts, target) if h.is_templatable]
    if not targets:
        raise click.ClickException(f"no templatable devices selected by {target!r}")
    return targets, links, global_meta


def _secrets():
    """The Vault secret fetcher, imported lazily to keep --help fast."""
    from barf.util.secrets import VaultSecrets

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
@click.argument("target")
@_FILENAME_OPTION
def config_generate(target: str, filename: str) -> None:
    """Render configs for TARGET (a hostname, or "all") into output/."""
    targets, links, global_meta = _load_targets(filename, target)
    secrets = _secrets()

    for host in targets:
        rendered = render_host_config(host, links, global_meta, secrets)
        path = write_rendered_config(host, rendered)
        click.echo(f"[{host.hostname}] wrote {path}")


@config.command("diff")
@click.argument("target")
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
    target: str, filename: str, show_device_only: bool, show_secrets: bool
) -> None:
    """Diff rendered configs against TARGET (a hostname, or "all")."""
    targets, links, global_meta = _load_targets(filename, target)
    secrets = _secrets()

    results = []
    failed = False
    for host in targets:
        rendered = render_host_config(host, links, global_meta, secrets)
        try:
            diff = host.diff_config(
                rendered,
                redact=not show_secrets,
                show_device_only=show_device_only,
            )
        except NotImplementedError:
            results.append([host.hostname, "skipped: no diff support"])
            continue
        except Exception as e:  # noqa: BLE001 - report per-device failures
            results.append([host.hostname, f"failed: {e}"])
            failed = True
            continue

        if diff.text.strip():
            click.echo(f"--- {host.hostname} ---")
            click.echo(diff.text)
            click.echo("")
        results.append([host.hostname, diff.summary])

    print_table(["DEVICE", "DIFF"], results)

    if failed:
        raise SystemExit(1)


@config.command("deploy")
@click.argument("target")
@_FILENAME_OPTION
@click.option(
    "--yes", is_flag=True, help="Do not ask for confirmation before deploying."
)
def config_deploy(target: str, filename: str, yes: bool) -> None:
    """Deploy rendered configs to TARGET (a hostname, or "all").

    Configs are merged into the device's running config, except for
    config sections the vendor declares as owned (e.g. VyOS
    ``system name-server``): there the rendered config is the whole
    truth and stale device config is deleted. The diff is shown and
    confirmed per device before anything is pushed.
    """
    targets, links, global_meta = _load_targets(filename, target)
    secrets = _secrets()

    results = []
    failed = False
    for host in targets:
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
        except Exception as e:  # noqa: BLE001 - report per-device failures
            results.append([host.hostname, f"deploy failed: {e}"])
            failed = True
            continue

        results.append([host.hostname, f"deployed ({diff.summary})"])

    click.echo("")
    print_table(["DEVICE", "RESULT"], results)

    if failed:
        raise SystemExit(1)
