import logging

import click

from barf.cli.device import device
from barf.cli.generate import generate
from barf.cli.validate import validate


@click.group()
@click.option("-v", "--verbose", is_flag=True, help="Enable debug logging.")
def cli(verbose):
    logging.basicConfig(
        level=logging.DEBUG if verbose else logging.INFO,
        format="%(asctime)s %(name)s %(levelname)s %(message)s",
        # barf/__init__.py already called basicConfig at import time, so force a
        # reconfigure here or the verbose level would be silently ignored.
        force=True,
    )


cli.add_command(device)
cli.add_command(generate)
cli.add_command(validate)
