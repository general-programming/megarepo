import os

from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS

SIZE_1_GB = 1024 * 1024 * 1024


class PsCommand(BaseCommand):
    MATCH_REGEX = "ps"

    async def run(self, cmd: str):
        total_price = 0
        total_nodes = 0

        for provider, _cloud in ALL_CLOUDS.items():
            if "CLOUD" in os.environ:
                if provider != os.environ["CLOUD"]:
                    continue

            total_hourly = 0
            total_data_out = 0
            cloud = _cloud()

            servers = await cloud.list_servers()

            print(f"All {provider} servers ({len(servers)} count)")
            for server in servers:
                tags_friendly = (
                    (", tags " + ";".join(server.server_tags))
                    if server.server_tags
                    else ""
                )
                print(
                    f"{server.server_name} {server.ip4} - {server.server_type} running on "
                    f"{server.datacenter}, [{server.data_out / SIZE_1_GB} GB out], created {server.created}{tags_friendly}"
                )

                server_type = await cloud.get_server_type(server.server_type)
                total_hourly += server_type.price_hourly
                total_data_out += server.data_out / SIZE_1_GB

            print(
                f"Total {provider} per hour cost for {len(servers)} nodes: {total_hourly}"
            )
            print(
                f"Total {provider} data for {len(servers)} nodes: {total_data_out} GB"
            )
            total_price += total_hourly
            total_nodes += len(servers)

            await cloud.cleanup()

        print(
            f"Total hourly cost for {total_nodes} nodes on all providers: {total_price:.3f}"
        )
        print(
            f"Total daily cost for {total_nodes} nodes on all providers: {total_price * 24:.3f}"
        )
        print(
            f"Total weekly cost for {total_nodes} nodes on all providers: {total_price * 24 * 7:.3f}"
        )
        print(
            f"Total monthly cost for {total_nodes} nodes on all providers: {total_price * 24 * 30:.3f}"
        )
