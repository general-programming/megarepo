import argparse
import asyncio
import os

from zuscale.cli.commands import ALL_COMMANDS

parser = argparse.ArgumentParser(description='bad cli')

parser.add_argument('command', nargs='+', help='command to run')


async def main():
    args = parser.parse_args()

    command = ALL_COMMANDS.get(args.command[0], None)()
    if not command:
        raise Exception("Command not valid?")

    await command.run(args.command[1:])

if __name__ == "__main__":
    cloud_type = os.environ.get("CLOUD", "all").strip().lower()

    loop = asyncio.get_event_loop()
    loop.run_until_complete(main())
