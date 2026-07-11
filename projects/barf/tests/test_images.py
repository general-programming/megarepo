import io

import pytest

from barf.util import images

RELEASE = {
    "tag_name": "2026.06.30-0048-rolling",
    "assets": [
        {
            "name": "vyos-2026.06.30-0048-rolling-generic-amd64.iso.minisig",
            "size": 341,
            "browser_download_url": "https://example.invalid/iso.minisig",
        },
        {
            "name": "vyos-2026.06.30-0048-rolling-generic-amd64.iso",
            "size": 3,
            "browser_download_url": "https://example.invalid/iso",
        },
    ],
}


def make_provider(release=RELEASE):
    provider = images.VyOSImageProvider()
    # Prime the cached_property so no network fetch happens.
    provider.__dict__["release"] = release
    return provider


def test_latest_version():
    assert make_provider().latest_version == "2026.06.30-0048-rolling"


def test_asset_picks_the_iso_not_the_signature():
    assert make_provider().asset["name"].endswith("-generic-amd64.iso")


def test_asset_missing_raises():
    provider = make_provider({"tag_name": "x", "assets": []})
    with pytest.raises(RuntimeError):
        provider.asset


def test_is_current():
    provider = make_provider()
    assert provider.is_current("2026.06.30-0048-rolling")
    assert provider.is_current("VyOS 2026.06.30-0048-rolling")
    assert not provider.is_current("2026.03.28-0028-rolling")


def test_download_skips_cached_image(tmp_path, monkeypatch):
    monkeypatch.setattr(images, "CACHE_DIR", tmp_path)

    def explode(*_args, **_kwargs):
        raise AssertionError("cached image must not be re-downloaded")

    monkeypatch.setattr(images.urllib.request, "urlopen", explode)

    provider = make_provider()
    cached = tmp_path / provider.asset["name"]
    cached.write_bytes(b"abc")

    assert provider.download() == cached


def test_download_fetches_and_renames(tmp_path, monkeypatch):
    monkeypatch.setattr(images, "CACHE_DIR", tmp_path)
    monkeypatch.setattr(
        images.urllib.request,
        "urlopen",
        lambda url, timeout=30: io.BytesIO(b"abc"),
    )

    provider = make_provider()
    target = provider.download()

    assert target == tmp_path / provider.asset["name"]
    assert target.read_bytes() == b"abc"
    assert not list(tmp_path.glob("*.partial"))


def test_download_short_read_raises(tmp_path, monkeypatch):
    monkeypatch.setattr(images, "CACHE_DIR", tmp_path)
    monkeypatch.setattr(
        images.urllib.request,
        "urlopen",
        lambda url, timeout=30: io.BytesIO(b"a"),
    )

    provider = make_provider()
    with pytest.raises(RuntimeError):
        provider.download()
    assert not list(tmp_path.glob("*.iso"))


def test_signature_asset_matches_the_iso():
    signature = make_provider().signature_asset
    assert signature is not None
    assert signature["name"].endswith("-generic-amd64.iso.minisig")


def test_download_signature_fetches(tmp_path, monkeypatch):
    monkeypatch.setattr(images, "CACHE_DIR", tmp_path)
    monkeypatch.setattr(
        images.urllib.request,
        "urlopen",
        lambda url, timeout=30: io.BytesIO(b"s" * 341),
    )

    target = make_provider().download_signature()

    assert target == tmp_path / (RELEASE["assets"][1]["name"] + ".minisig")
    assert target.read_bytes() == b"s" * 341


def test_download_signature_none_when_upstream_has_none():
    provider = make_provider(
        {
            "tag_name": "x",
            "assets": [
                {
                    "name": "vyos-x-generic-amd64.iso",
                    "size": 3,
                    "browser_download_url": "https://example.invalid/iso",
                }
            ],
        }
    )
    assert provider.download_signature() is None
