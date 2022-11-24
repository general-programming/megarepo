import json
from typing import List

from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS


class DeployCommand(BaseCommand):
    MATCH_REGEX = "deploy"

    async def run(self, cmd: List[str]):
        # Get the path and attempt to load it.
        filepath = cmd[0]

        with open(filepath, "r") as f:
            config = json.load(f)

        # Get the providers and validate them.
        providers = config.get("providers", {})
        for provider_name in providers.keys():
            if provider_name not in ALL_CLOUDS:
                raise Exception(f"{provider_name} is not a valid provider.")

        # Deploy a VM each per provider.
        for provider_name in providers.keys():
            provider = ALL_CLOUDS[provider_name]()
            print(await provider.deploy_server(config))
            await provider.cleanup()
