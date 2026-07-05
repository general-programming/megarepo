"""Helpers shared by the barf CLI command groups."""

import click

from barf.vendors import BaseHost


def resolve_targets(hosts: list[BaseHost], target: str) -> list[BaseHost]:
    """The hosts selected by TARGET (a hostname, or "all")."""
    if target == "all":
        return hosts

    matches = [h for h in hosts if h.hostname == target]
    if not matches:
        raise click.ClickException(f"unknown device {target!r}")
    return matches


def print_table(headers: list[str], rows: list[list]) -> None:
    widths = [len(h) for h in headers]
    for row in rows:
        for i, cell in enumerate(row):
            widths[i] = max(widths[i], len(str(cell)))

    fmt = "  ".join("{:<" + str(w) + "}" for w in widths)
    click.echo(fmt.format(*headers))
    click.echo(fmt.format(*["-" * w for w in widths]))
    for row in rows:
        click.echo(fmt.format(*[str(c) for c in row]))
