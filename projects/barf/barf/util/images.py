import functools
import json
import logging
import urllib.request
from pathlib import Path

from barf.util.progress import PvProgress

log = logging.getLogger(__name__)

CACHE_DIR = Path.home() / ".cache" / "barf" / "images"


class ImageProvider:
    """Upstream image metadata and downloads for one vendor.

    Shared between ``barf device update`` (which stages the image onto
    devices) and ``barf device status`` (which reports whether devices
    are running the latest release).
    """

    @property
    def latest_version(self) -> str:
        raise NotImplementedError

    def is_current(self, version: str) -> bool:
        """Whether a device-reported version string matches the latest."""
        raise NotImplementedError

    def download(self) -> Path:
        """Download the latest image into the local cache, once."""
        raise NotImplementedError


class VyOSImageProvider(ImageProvider):
    RELEASE_URL = "https://api.github.com/repos/vyos/vyos-nightly-build/releases/latest"
    ASSET_SUFFIX = "-generic-amd64.iso"

    @functools.cached_property
    def release(self) -> dict:
        """The latest rolling release metadata, fetched once per process."""
        log.debug("GET %s", self.RELEASE_URL)
        request = urllib.request.Request(
            self.RELEASE_URL, headers={"Accept": "application/vnd.github+json"}
        )
        with urllib.request.urlopen(request, timeout=30) as resp:
            return json.load(resp)

    @functools.cached_property
    def asset(self) -> dict:
        for asset in self.release["assets"]:
            if asset["name"].endswith(self.ASSET_SUFFIX):
                return asset

        raise RuntimeError(
            f"no {self.ASSET_SUFFIX} asset in release {self.latest_version}"
        )

    @property
    def latest_version(self) -> str:
        return self.release["tag_name"]

    def is_current(self, version: str) -> bool:
        return self.latest_version in version

    def download(self) -> Path:
        target = CACHE_DIR / self.asset["name"]
        size = self.asset["size"]
        if target.exists() and target.stat().st_size == size:
            log.debug("Image already cached: %s", target)
            return target

        CACHE_DIR.mkdir(parents=True, exist_ok=True)
        partial = target.with_suffix(".partial")
        progress = PvProgress(f"download {target.name}", size)
        received = 0
        try:
            with (
                urllib.request.urlopen(
                    self.asset["browser_download_url"], timeout=30
                ) as resp,
                open(partial, "wb") as f,
            ):
                while chunk := resp.read(256 * 1024):
                    f.write(chunk)
                    received += len(chunk)
                    progress.update(received)
        finally:
            progress.finish()

        if partial.stat().st_size != size:
            partial.unlink()
            raise RuntimeError(f"short download for {target.name}")

        partial.rename(target)
        return target


PROVIDERS: dict[str, ImageProvider] = {
    "vyos": VyOSImageProvider(),
}
