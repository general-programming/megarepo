import click

from barf.cli.generate import generate
from barf.cli.validate import validate


@click.group()
def cli():
    pass


cli.add_command(generate)
cli.add_command(validate)
