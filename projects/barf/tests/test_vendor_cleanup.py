from types import SimpleNamespace

import pytest

from barf.util.vyos_api import SystemImage
from barf.vendors import BaseHost
from barf.vendors.vyos import VyOSHost


@pytest.fixture
def cleanup_env(monkeypatch):
    """Fake the API surface _cleanup_old_images touches."""
    env = SimpleNamespace(images=[], deleted=[])

    monkeypatch.setattr(VyOSHost, "management_ip", "10.0.0.9")
    monkeypatch.setattr(
        "barf.util.secrets.VaultSecrets",
        lambda: SimpleNamespace(vyos_api_password="key"),
    )
    monkeypatch.setattr(
        "barf.util.vyos_api.vyos_api_show",
        lambda address, key, path, timeout=10: "image table",
    )
    monkeypatch.setattr(
        "barf.util.vyos_api.parse_system_images", lambda output: env.images
    )
    monkeypatch.setattr(
        "barf.util.vyos_api.vyos_api_image_delete",
        lambda address, key, name, timeout=60: env.deleted.append(name),
    )
    return env


def make_host():
    return VyOSHost(hostname="testbox", role="vpn", asn=64512)


def test_base_host_does_not_support_cleanup():
    host = BaseHost(hostname="testbox", role="vpn")
    with pytest.raises(NotImplementedError):
        host.cleanup_host()


def test_cleanup_deletes_only_unused_images(cleanup_env):
    cleanup_env.images = [
        SystemImage("2026.06.30-0048-rolling", default_boot=True, running=True),
        SystemImage("2026.03.28-0028-rolling"),
        SystemImage("y"),
    ]

    actions = make_host().cleanup_host()

    assert cleanup_env.deleted == ["2026.03.28-0028-rolling", "y"]
    assert actions == [
        "deleted image 2026.03.28-0028-rolling",
        "deleted image y",
    ]


def test_cleanup_spares_default_boot_and_running(cleanup_env):
    cleanup_env.images = [
        SystemImage("running-img", running=True),
        SystemImage("default-img", default_boot=True),
    ]

    assert make_host().cleanup_host() == []
    assert cleanup_env.deleted == []


def test_cleanup_fails_when_image_list_is_empty(cleanup_env):
    cleanup_env.images = []

    with pytest.raises(RuntimeError, match="could not list system images"):
        make_host().cleanup_host()
