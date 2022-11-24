from zuscale.cli.commands.deploy import DeployCommand
from zuscale.cli.commands.dump import DumpCommand
from zuscale.cli.commands.images import ImagesCommand
from zuscale.cli.commands.pricing import PricingCommand
from zuscale.cli.commands.ps import PsCommand
from zuscale.cli.commands.purge import PurgeCommand

ALL_COMMANDS = {
    "dump": DumpCommand,
    "pricing": PricingCommand,
    "ps": PsCommand,
    "images": ImagesCommand,
    "deploy": DeployCommand,
    "purge": PurgeCommand,
}
