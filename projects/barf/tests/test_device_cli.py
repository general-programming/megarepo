import importlib
from types import SimpleNamespace

import click
import pytest
from click.testing import CliRunner

from barf.vendors import DeployDiff

from barf.cli.device import wait_for_device_alive

# "from barf.cli import device" would give the click group of the same
# name, not the module.
device_cli = importlib.import_module("barf.cli.device")


def test_wait_for_device_alive_waits_then_polls(monkeypatch):
    sleeps = []
    monkeypatch.setattr(device_cli.time, "sleep", sleeps.append)
    monkeypatch.setattr(device_cli.time, "monotonic", lambda: float(len(sleeps)))

    versions = iter([None, None, "1.5-rolling"])
    host = SimpleNamespace(hostname="testbox", version=lambda: next(versions))

    assert wait_for_device_alive(host) == "1.5-rolling"
    # 15s head start, then 5s between the two failed polls.
    assert sleeps == [15, 5, 5]


def test_wait_for_device_alive_times_out(monkeypatch):
    monkeypatch.setattr(device_cli.time, "sleep", lambda s: None)
    clock = iter(range(0, 10_000, 500))
    monkeypatch.setattr(device_cli.time, "monotonic", lambda: float(next(clock)))

    host = SimpleNamespace(hostname="testbox", version=lambda: None)
    with pytest.raises(click.ClickException, match="did not come back"):
        wait_for_device_alive(host, timeout=900)


class FakeStatusHost:
    devicetype = "vyos"

    def __init__(
        self,
        hostname,
        version="2.0",
        diff=None,
        management_ip="10.0.0.9",
        reach_error=None,
    ):
        self.hostname = hostname
        self.management_ip = management_ip
        self._version = version
        self._diff = diff
        self._reach_error = reach_error

    def human_version(self):
        if self._reach_error:
            raise RuntimeError(self._reach_error)
        return self._version

    def uptime(self):
        return "9w"

    def diff_config(self, rendered):
        return self._diff


class FakeProvider:
    latest_version = "2.0"

    def is_current(self, version):
        return version == self.latest_version


def run_status(monkeypatch, tmp_path, hosts, render_error=None):
    network = tmp_path / "network.yml"
    network.write_text("hosts: {}\n")

    monkeypatch.setattr(device_cli, "load_network", lambda f: (hosts, [], {}))
    monkeypatch.setattr(
        device_cli, "VaultSecrets", lambda: SimpleNamespace(vyos_api_password="key")
    )
    monkeypatch.setattr(device_cli, "PROVIDERS", {"vyos": FakeProvider()})

    def render(host, links, global_meta, secrets):
        if render_error:
            raise RuntimeError(render_error)
        return f"set rendered {host.hostname}\n"

    monkeypatch.setattr(device_cli, "render_host_config", render)

    return CliRunner().invoke(
        device_cli.device, ["status", str(network)], catch_exceptions=False
    )


def test_status_current_and_consistent(monkeypatch, tmp_path):
    host = FakeStatusHost(
        "box1", diff=DeployDiff(text="", has_changes=False, summary="no changes")
    )
    result = run_status(monkeypatch, tmp_path, [host])
    assert result.exit_code == 0, result.output
    assert "LATEST FIRMWARE" in result.output
    assert "CONFIG CONSISTENT" in result.output
    row = next(line for line in result.output.splitlines() if "box1" in line)
    assert " yes " in row  # firmware current
    assert row.count("yes") == 2  # ...and config consistent


def test_status_outdated_firmware_and_drift(monkeypatch, tmp_path):
    host = FakeStatusHost(
        "box1",
        version="1.9",
        diff=DeployDiff(
            text="+ set x", has_changes=True, summary="+3 ~1 (23 device-only)"
        ),
    )
    result = run_status(monkeypatch, tmp_path, [host])
    assert result.exit_code == 0, result.output
    row = next(line for line in result.output.splitlines() if "box1" in line)
    assert "no (2.0)" in row
    assert "+3 ~1 (23 device-only)" in row


def test_status_render_error_still_reports(monkeypatch, tmp_path):
    host = FakeStatusHost(
        "box1", diff=DeployDiff(text="", has_changes=False, summary="no changes")
    )
    result = run_status(monkeypatch, tmp_path, [host], render_error="no vault")
    assert result.exit_code == 0, result.output
    assert "render error: no vault" in result.output


def test_status_unreachable_host_row(monkeypatch, tmp_path):
    host = FakeStatusHost("box1", reach_error="timed out")
    result = run_status(monkeypatch, tmp_path, [host])
    assert result.exit_code == 0, result.output
    row = next(line for line in result.output.splitlines() if "box1" in line)
    assert "error: timed out" in row


class FakeShellSSH:
    """Records DeviceSSH construction and fakes the interactive shell."""

    calls = []
    status = 0
    fail_with = None

    def __init__(self, host, address, username=None):
        if type(self).fail_with:
            raise RuntimeError(type(self).fail_with)
        type(self).calls.append((host.hostname, address, username))
        self.username = username or "supertech"

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        pass

    def interactive_shell(self):
        return type(self).status


def make_ssh_host(hostname="box1", ssh_ip="10.0.0.9", ssh_username=None):
    return SimpleNamespace(hostname=hostname, ssh_ip=ssh_ip, SSH_USERNAME=ssh_username)


def run_ssh(monkeypatch, tmp_path, hosts, target, status=0, fail_with=None):
    network = tmp_path / "network.yml"
    network.write_text("hosts: {}\n")

    monkeypatch.setattr(device_cli, "load_network", lambda f: (hosts, [], {}))
    monkeypatch.setattr(device_cli, "DeviceSSH", FakeShellSSH)
    FakeShellSSH.calls = []
    FakeShellSSH.status = status
    FakeShellSSH.fail_with = fail_with

    return CliRunner().invoke(
        device_cli.device,
        ["ssh", target, "--filename", str(network)],
        catch_exceptions=False,
    )


def test_ssh_connects_with_vendor_user(monkeypatch, tmp_path):
    result = run_ssh(monkeypatch, tmp_path, [make_ssh_host()], "box1")
    assert result.exit_code == 0, result.output
    assert FakeShellSSH.calls == [("box1", "10.0.0.9", None)]
    assert "connected as supertech@10.0.0.9" in result.output


def test_ssh_passes_vendor_ssh_username(monkeypatch, tmp_path):
    host = make_ssh_host(ssh_username="root")
    result = run_ssh(monkeypatch, tmp_path, [host], "box1")
    assert result.exit_code == 0, result.output
    assert FakeShellSSH.calls == [("box1", "10.0.0.9", "root")]


def test_ssh_propagates_remote_exit_status(monkeypatch, tmp_path):
    result = run_ssh(monkeypatch, tmp_path, [make_ssh_host()], "box1", status=3)
    assert result.exit_code == 3


def test_ssh_rejects_all(monkeypatch, tmp_path):
    result = run_ssh(monkeypatch, tmp_path, [make_ssh_host()], "all")
    assert result.exit_code == 1
    assert "single hostname" in result.output


def test_ssh_unreachable_host(monkeypatch, tmp_path):
    host = make_ssh_host(ssh_ip=None)
    result = run_ssh(monkeypatch, tmp_path, [host], "box1")
    assert result.exit_code == 1
    assert "no reachable SSH address" in result.output


def test_ssh_auth_failure_is_a_clean_error(monkeypatch, tmp_path):
    result = run_ssh(
        monkeypatch,
        tmp_path,
        [make_ssh_host()],
        "box1",
        fail_with="all SSH auth methods failed",
    )
    assert result.exit_code == 1
    assert "all SSH auth methods failed" in result.output
