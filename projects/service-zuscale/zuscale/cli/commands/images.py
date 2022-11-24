from zuscale.cli.commands.base import BaseCommand
from zuscale.providers import ALL_CLOUDS


class ImagesCommand(BaseCommand):
    MATCH_REGEX = "images"

    async def run(self, cmd: str):
        for provider, _cloud in ALL_CLOUDS.items():
            cloud = _cloud()
            images = await cloud.list_images()

            print(f"All {provider} images ({len(images)} count)")
            for image in images:
                print("%s [%s]" % (image.name, image.arch))

            await cloud.cleanup()
