from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS


class PricingCommand(BaseCommand):
    MATCH_REGEX = "pricing"

    async def run(self, cmd: str):
        provider = None
        if len(cmd) > 0:
            provider = cmd[0]

        instances = []

        for provider_name, _cloud in ALL_CLOUDS.items():
            # Allow us to select only single provider.
            if provider:
                if provider_name != provider:
                    continue

            cloud = _cloud()
            for i in await cloud.list_types():
                instances.append(i)
            await cloud.cleanup()

        for instance in sorted(instances, key=lambda x: x.price_hourly):
            print(instance)
