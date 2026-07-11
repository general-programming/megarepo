"""Mirror firmware images into R2 so devices download them directly.

``barf device update`` publishes the vendor image (and its ``.minisig``
signature, when upstream ships one) into an S3-compatible bucket served
publicly, then hands each device the public URL to install from. The
mirror endpoints live in network.yml ``global_meta.firmware``; only the
R2 API tokens live in Vault:

    vault kv put secret/r2-firmware \\
        access_key_id=<R2 access key ID> \\
        secret_access_key=<R2 secret access key>
"""

import functools
import logging
from pathlib import Path
from typing import Optional

import boto3
import botocore.exceptions

from barf.actions import get_secret
from barf.util.progress import PvProgress

log = logging.getLogger(__name__)

VAULT_SECRET = "r2-firmware"


class FirmwareMirror:
    """Publishes images and their signatures to an S3-compatible bucket."""

    def __init__(
        self,
        s3_endpoint: str,
        bucket: str,
        public_base: str,
        prefix: str = "firmware",
    ):
        """A mirror rooted at ``prefix`` inside ``bucket``.

        Args:
            s3_endpoint: The S3 API endpoint (the R2 account URL).
            bucket: The bucket name.
            public_base: The public base URL serving the bucket.
            prefix: The key prefix objects are published under.
        """
        self.s3_endpoint = s3_endpoint
        self.bucket = bucket
        self.public_base = public_base.rstrip("/")
        self.prefix = prefix.strip("/")

    @classmethod
    def from_meta(cls, global_meta: dict) -> "FirmwareMirror":
        """The mirror described by network.yml ``global_meta.firmware``."""
        meta = global_meta.get("firmware")
        if not meta:
            raise KeyError(
                "global_meta: no firmware mirror configured;"
                " add a firmware: block with s3_endpoint, bucket,"
                " and public_base"
            )
        return cls(
            s3_endpoint=meta["s3_endpoint"],
            bucket=meta["bucket"],
            public_base=meta["public_base"],
            prefix=meta.get("prefix", "firmware"),
        )

    @functools.cached_property
    def _client(self):
        credentials = get_secret(VAULT_SECRET, key=None)
        return boto3.client(
            "s3",
            endpoint_url=self.s3_endpoint,
            aws_access_key_id=credentials["access_key_id"],
            aws_secret_access_key=credentials["secret_access_key"],
            # R2 ignores regions but boto3 wants one.
            region_name="auto",
        )

    def public_url(self, name: str) -> str:
        return f"{self.public_base}/{self.prefix}/{name}"

    def _object_size(self, key: str) -> Optional[int]:
        """The mirrored object's size, or None when it does not exist."""
        try:
            return self._client.head_object(Bucket=self.bucket, Key=key)[
                "ContentLength"
            ]
        except botocore.exceptions.ClientError as e:
            if e.response["Error"]["Code"] in ("404", "NoSuchKey", "NotFound"):
                return None
            raise

    def _upload(self, local: Path) -> None:
        """Upload ``local`` under the prefix, skipping a same-size copy."""
        key = f"{self.prefix}/{local.name}"
        size = local.stat().st_size

        if self._object_size(key) == size:
            log.debug("%s already mirrored at %s", local.name, key)
            return

        progress = PvProgress(f"upload {local.name}", size)
        sent = 0

        def update(chunk: int) -> None:
            nonlocal sent
            sent += chunk
            progress.update(sent)

        try:
            self._client.upload_file(str(local), self.bucket, key, Callback=update)
        finally:
            progress.finish()

        mirrored = self._object_size(key)
        if mirrored != size:
            raise RuntimeError(
                f"mirror upload of {key} ended up {mirrored} bytes, expected {size}"
            )

    def publish(self, image: Path, signature: Optional[Path] = None) -> str:
        """Mirror an image and its signature, returning the image's URL.

        The signature lands first: VyOS fetches ``<url>.minisig`` during
        ``add system image``, so the image URL must never be visible
        before its signature is.
        """
        if signature is not None:
            self._upload(signature)
        self._upload(image)
        return self.public_url(image.name)
