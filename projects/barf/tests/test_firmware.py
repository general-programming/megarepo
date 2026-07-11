from pathlib import Path

import botocore.exceptions
import pytest

from barf.util import firmware

META = {
    "firmware": {
        "s3_endpoint": "https://acct.r2.cloudflarestorage.com",
        "bucket": "public",
        "prefix": "firmware",
        "public_base": "https://files.owo.me",
    }
}


class FakeS3:
    """head_object/upload_file stand-in tracking bucket contents."""

    def __init__(self, objects=None):
        self.objects = dict(objects or {})
        self.uploads = []

    def head_object(self, Bucket, Key):
        if Key not in self.objects:
            raise botocore.exceptions.ClientError(
                {"Error": {"Code": "404", "Message": "not found"}}, "HeadObject"
            )
        return {"ContentLength": self.objects[Key]}

    def upload_file(self, filename, bucket, key, Callback=None):
        size = Path(filename).stat().st_size
        if Callback:
            Callback(size)
        self.objects[key] = size
        self.uploads.append(key)


def make_mirror(objects=None):
    mirror = firmware.FirmwareMirror.from_meta(META)
    client = FakeS3(objects)
    # Prime the cached_property so no Vault/boto3 access happens.
    mirror.__dict__["_client"] = client
    return mirror, client


@pytest.fixture
def image(tmp_path):
    path = tmp_path / "vyos-2.0-generic-amd64.iso"
    path.write_bytes(b"nyan" * 100)
    return path


@pytest.fixture
def signature(tmp_path):
    path = tmp_path / "vyos-2.0-generic-amd64.iso.minisig"
    path.write_bytes(b"sig")
    return path


def test_from_meta_requires_config():
    with pytest.raises(KeyError, match="no firmware mirror configured"):
        firmware.FirmwareMirror.from_meta({})


def test_client_uses_vault_credentials(monkeypatch):
    captured = {}
    monkeypatch.setattr(
        firmware,
        "get_secret",
        lambda path, key=None: {"access_key_id": "ak", "secret_access_key": "sk"},
    )
    monkeypatch.setattr(
        firmware.boto3,
        "client",
        lambda service, **kwargs: (
            captured.update(service=service, **kwargs) or "client"
        ),
    )

    assert firmware.FirmwareMirror.from_meta(META)._client == "client"
    assert captured["service"] == "s3"
    assert captured["endpoint_url"] == META["firmware"]["s3_endpoint"]
    assert captured["aws_access_key_id"] == "ak"
    assert captured["aws_secret_access_key"] == "sk"


def test_publish_uploads_signature_before_image(image, signature):
    mirror, client = make_mirror()

    url = mirror.publish(image, signature)

    assert url == f"https://files.owo.me/firmware/{image.name}"
    # VyOS fetches <url>.minisig during install, so the signature must
    # be in place before the image URL is ever handed out.
    assert client.uploads == [
        f"firmware/{signature.name}",
        f"firmware/{image.name}",
    ]


def test_publish_without_signature(image):
    mirror, client = make_mirror()

    url = mirror.publish(image)

    assert client.uploads == [f"firmware/{image.name}"]
    assert url.endswith(image.name)


def test_publish_skips_already_mirrored(image, signature):
    mirror, client = make_mirror(
        {
            f"firmware/{image.name}": image.stat().st_size,
            f"firmware/{signature.name}": signature.stat().st_size,
        }
    )

    mirror.publish(image, signature)

    assert client.uploads == []


def test_publish_reuploads_on_size_mismatch(image):
    mirror, client = make_mirror({f"firmware/{image.name}": 1})

    mirror.publish(image)

    assert client.uploads == [f"firmware/{image.name}"]


def test_upload_verifies_mirrored_size(image):
    mirror, client = make_mirror()

    def truncated_upload(filename, bucket, key, Callback=None):
        client.objects[key] = 1

    client.upload_file = truncated_upload

    with pytest.raises(RuntimeError, match="expected"):
        mirror.publish(image)


def test_head_errors_other_than_missing_propagate(image):
    mirror, client = make_mirror()

    def forbidden(Bucket, Key):
        raise botocore.exceptions.ClientError(
            {"Error": {"Code": "403", "Message": "denied"}}, "HeadObject"
        )

    client.head_object = forbidden

    with pytest.raises(botocore.exceptions.ClientError):
        mirror.publish(image)


def test_prefix_and_base_are_normalized():
    mirror = firmware.FirmwareMirror(
        s3_endpoint="https://acct.r2.cloudflarestorage.com",
        bucket="public",
        public_base="https://files.owo.me/",
        prefix="/firmware/",
    )
    assert mirror.public_url("x.iso") == "https://files.owo.me/firmware/x.iso"
