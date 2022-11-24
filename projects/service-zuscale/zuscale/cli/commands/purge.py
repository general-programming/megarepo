from typing import List

from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS


class PurgeCommand(BaseCommand):
    MATCH_REGEX = "purge"

    async def run(self, cmd: List[str]):
        # Get the cloud provider for the given argument.
        provider = cmd[0]

        if provider not in ALL_CLOUDS:
            raise Exception(f"{provider} is not a valid provider.")

        provider = ALL_CLOUDS[provider]()

        # List all machines hosted on this provider.
        all_servers = await provider.list_servers()

        # Filter out servers with safe tags.
        for index, server in enumerate(all_servers):
            if server.is_persistent:
                all_servers.remove(server)

        # Prompt user if they want to continue and purge if yes.
        for server in all_servers:
            print(server.server_name, server.server_tags)

        prompt_text = f"Killing {len(all_servers)} servers. Y/N? "
        if not self.prompt(prompt_text, "y"):
            print("Refusing to kill servers.")
            return await provider.cleanup()

        await provider.delete_all_servers(sleep=None)
        await provider.cleanup()
