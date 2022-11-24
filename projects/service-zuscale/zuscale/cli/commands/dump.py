import os

from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS


class DumpCommand(BaseCommand):
    MATCH_REGEX = "dump"

    async def run(self, cmd: str):
        for provider, _cloud in ALL_CLOUDS.items():
            cloud = _cloud()
            print(cloud.NAME)

            ssh_keys = await cloud.list_ssh_keys()
            print(f"All SSH keys ({len(ssh_keys)} count)")
            for key in ssh_keys:
                print(key)

            images = await cloud.list_images()
            print(f"All images ({len(images)} count)")
            for image in images:
                print(image.name)

            servers = await cloud.list_servers()
            print(f"All servers ({len(servers)} count)")
            for server in servers:
                print(server)

            server_types = await cloud.list_types()
            print(f"All server types ({len(server_types)} count)")
            for server_type in server_types:
                print(server_type)

            if "NUKE_ALL_YES_PLEASE" in os.environ:
                await cloud.delete_all_servers(sleep=None)

            await cloud.cleanup()
            print("-" * 60)
