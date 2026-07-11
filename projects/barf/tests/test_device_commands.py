import importlib
from types import SimpleNamespace

import pytest
from click.testing import CliRunner

from barf.cli.device import device as device_group
from barf.util.firmware import FirmwareMirror

device_cli = importlib.import_module("barf.cli.device")

IMAGE_NAME = "vyos-2.0-generic-amd64.iso"


class FakeProvider:
    latest_version = "2.0"
    has_signature = False

    def is_current(self, version: str) -> bool:
        return version == "2.0"

    def download(self):
        return SimpleNamespace(
            name=IMAGE_NAME, stat=lambda: SimpleNamespace(st_size=123)
        )

    def download_signature(self):
        if not self.has_signature:
            return None
        return SimpleNamespace(name=IMAGE_NAME + ".minisig")


class FakeMirror:
    published = []

    @classmethod
    def from_meta(cls, global_meta):
        return cls()

    def publish(self, image, signature=None):
        type(self).published.append((image.name, signature))
        return f"https://files.owo.me/firmware/{image.name}"


class FakeHost:
    def __init__(
        self,
        hostname: str,
        devicetype: str = "vyos",
        running: str = "1.0",
        provider: FakeProvider | None = None,
    ):
        self.hostname = hostname
        self.devicetype = devicetype
        self.asn = 64512
        self.is_spine = "-spine-" in hostname
        self.management_ip = "10.0.0.1"
        self._running = running
        self._provider = provider
        self.update_calls = []
        self.cleanup_calls = 0

    @property
    def image_provider(self):
        return self._provider

    def version(self):
        return self._running

    def safe_to_reboot(self, fleet):
        return True

    def verify_routing(self):
        return None

    def update_host(self, url, size, drain_wait=5, version=None):
        self.update_calls.append({"url": url, "size": size, "version": version})
        self._running = version
        return f"rebooting into {version}"

    def cleanup_host(self):
        self.cleanup_calls += 1
        return ["deleted image old"]


@pytest.fixture
def cli_env(monkeypatch, tmp_path):
    """A CliRunner plus fakes wired into the device CLI module."""
    env = SimpleNamespace(hosts=[], runner=CliRunner())

    network_file = tmp_path / "network.yml"
    network_file.write_text("hosts: {}\n")
    env.filename = str(network_file)

    monkeypatch.setattr(
        device_cli, "load_network", lambda filename: (env.hosts, [], {})
    )
    monkeypatch.setattr(
        device_cli,
        "VaultSecrets",
        lambda: SimpleNamespace(vyos_api_password="key"),
    )
    monkeypatch.setattr(
        device_cli,
        "wait_for_device_alive",
        lambda host, changed_from=None, **kwargs: host.version(),
    )
    monkeypatch.setattr(device_cli, "FirmwareMirror", FakeMirror)
    FakeMirror.published = []
    return env


def invoke(env, *args, **kwargs):
    return env.runner.invoke(
        device_group, [*args, "--filename", env.filename], **kwargs
    )


def test_update_unknown_target_errors(cli_env):
    cli_env.hosts = [FakeHost("a", provider=FakeProvider())]
    result = invoke(cli_env, "update", "nope")
    assert result.exit_code != 0
    assert "unknown device" in result.output


def test_update_skips_unsupported_vendors(cli_env):
    cli_env.hosts = [FakeHost("router", devicetype="mikrotik", provider=None)]
    result = invoke(cli_env, "update", "all")
    assert "skip router: no image provider for 'mikrotik'" in result.output
    assert result.exit_code != 0  # nothing updatable selected


def test_update_already_current_short_circuits(cli_env):
    host = FakeHost("a", running="2.0", provider=FakeProvider())
    cli_env.hosts = [host]
    result = invoke(cli_env, "update", "all", "--yes")
    assert result.exit_code == 0
    assert "already current" in result.output
    assert host.update_calls == []


def test_update_declined_confirmation_skips_device(cli_env):
    host = FakeHost("a", provider=FakeProvider())
    cli_env.hosts = [host]
    result = invoke(cli_env, "update", "all", input="n\n")
    assert result.exit_code == 0
    assert "(reboot declined)" in result.output
    # The image is still mirrored, but the device is never touched.
    assert FakeMirror.published == [(IMAGE_NAME, None)]
    assert host.update_calls == []


def test_update_full_run_updates_and_verifies(cli_env):
    host = FakeHost("a", provider=FakeProvider())
    cli_env.hosts = [host]
    result = invoke(cli_env, "update", "all", "--yes")
    assert result.exit_code == 0
    assert "updated to 2.0" in result.output
    assert host.update_calls == [
        {
            "url": f"https://files.owo.me/firmware/{IMAGE_NAME}",
            "size": 123,
            "version": "2.0",
        }
    ]


def test_update_mirrors_the_signature_alongside(cli_env):
    provider = FakeProvider()
    provider.has_signature = True
    cli_env.hosts = [FakeHost("a", provider=provider)]

    result = invoke(cli_env, "update", "all", "--yes")

    assert result.exit_code == 0
    (published,) = FakeMirror.published
    assert published[0] == IMAGE_NAME
    assert published[1].name == IMAGE_NAME + ".minisig"


def test_update_requires_mirror_config(cli_env, monkeypatch):
    monkeypatch.setattr(device_cli, "FirmwareMirror", FirmwareMirror)
    cli_env.hosts = [FakeHost("a", provider=FakeProvider())]

    result = invoke(cli_env, "update", "all", "--yes")

    assert result.exit_code != 0
    assert "no firmware mirror configured" in result.output


def test_update_failure_aborts_remaining_devices(cli_env):
    first = FakeHost("a-leaf-1", provider=FakeProvider())
    second = FakeHost("b-leaf-2", provider=FakeProvider())
    first.safe_to_reboot = lambda fleet: (_ for _ in ()).throw(
        RuntimeError("refusing to reboot")
    )
    cli_env.hosts = [first, second]

    result = invoke(cli_env, "update", "all", "--yes")
    assert result.exit_code == 1
    assert "aborting remaining devices" in result.output
    assert "failed: refusing to reboot" in result.output
    assert second.update_calls == []


def test_cleanup_reports_actions(cli_env):
    host = FakeHost("a", provider=FakeProvider())
    cli_env.hosts = [host]
    result = invoke(cli_env, "cleanup", "a")
    assert result.exit_code == 0
    assert "deleted image old" in result.output
    assert host.cleanup_calls == 1


def test_cleanup_all_skips_unsupported(cli_env):
    unsupported = FakeHost("ext", devicetype="external")
    unsupported.cleanup_host = lambda: (_ for _ in ()).throw(
        NotImplementedError("'external' devices do not support cleanup")
    )
    cli_env.hosts = [unsupported, FakeHost("a")]

    result = invoke(cli_env, "cleanup", "all")
    assert result.exit_code == 0
    assert "skipped: no cleanup support" in result.output


def test_cleanup_single_unsupported_errors(cli_env):
    unsupported = FakeHost("ext", devicetype="external")
    unsupported.cleanup_host = lambda: (_ for _ in ()).throw(
        NotImplementedError("'external' devices do not support cleanup")
    )
    cli_env.hosts = [unsupported]

    result = invoke(cli_env, "cleanup", "ext")
    assert result.exit_code != 0


def test_cleanup_failure_sets_exit_code(cli_env):
    host = FakeHost("a")
    host.cleanup_host = lambda: (_ for _ in ()).throw(RuntimeError("api down"))
    cli_env.hosts = [host]

    result = invoke(cli_env, "cleanup", "all")
    assert result.exit_code == 1
    assert "failed: api down" in result.output
