from types import SimpleNamespace

import paramiko
import pytest

from barf.util import ssh as ssh_util

CREDENTIALS = {"username": "supertech", "password": "hunter2"}


class FakeSSHClient:
    """Records connect() attempts; fails the first N of them."""

    fail_first = 0
    connects = []
    closes = 0

    def __init__(self):
        type(self).connects = []
        type(self).closes = 0

    def set_missing_host_key_policy(self, policy):
        pass

    def connect(self, address, **kwargs):
        type(self).connects.append({"address": address, **kwargs})
        if len(type(self).connects) <= type(self).fail_first:
            raise paramiko.AuthenticationException("denied")

    def close(self):
        type(self).closes += 1


@pytest.fixture
def fake_client(monkeypatch):
    monkeypatch.setattr(
        ssh_util,
        "get_supertech_creditentials",
        lambda host: (CREDENTIALS["username"], CREDENTIALS["password"]),
    )
    monkeypatch.setattr(ssh_util.paramiko, "SSHClient", FakeSSHClient)
    return FakeSSHClient


def make_host(devicetype="vyos"):
    return SimpleNamespace(devicetype=devicetype, hostname="testbox")


def test_uses_supertech_user_and_keys_first(fake_client):
    fake_client.fail_first = 0
    ssh_util.DeviceSSH(make_host(), "10.0.0.1")

    assert len(fake_client.connects) == 1
    first = fake_client.connects[0]
    assert first["username"] == "supertech"
    assert first["allow_agent"] is True
    assert "password" not in first


def test_falls_back_to_password_only(fake_client):
    fake_client.fail_first = 1
    ssh_util.DeviceSSH(make_host(), "10.0.0.1")

    assert len(fake_client.connects) == 2
    second = fake_client.connects[1]
    assert second["password"] == "hunter2"
    assert second["allow_agent"] is False
    assert second["look_for_keys"] is False
    # The failed key-auth transport was torn down before the retry.
    assert fake_client.closes == 1


def test_raises_when_all_auth_fails(fake_client):
    fake_client.fail_first = 2
    with pytest.raises(RuntimeError, match="all SSH auth methods failed"):
        ssh_util.DeviceSSH(make_host(), "10.0.0.1")


class FakeSFTP:
    def __init__(self, ssh):
        self.ssh = ssh

    def stat(self, remote):
        if self.ssh.remote_size is None:
            raise FileNotFoundError(remote)
        return SimpleNamespace(st_size=self.ssh.remote_size)

    def put(self, local, remote, callback=None):
        self.ssh.puts += 1
        self.ssh.remote_size = self.ssh.local_size

    def close(self):
        pass


def make_put_ssh(local, remote_md5s, remote_size=None):
    """A DeviceSSH with a fake transport; remote_md5s is consumed per call."""
    ssh = object.__new__(ssh_util.DeviceSSH)
    ssh.host = SimpleNamespace(hostname="testbox", devicetype="vyos")
    ssh.address = "10.0.0.1"
    ssh.username = "supertech"
    ssh.puts = 0
    ssh.local_size = local.stat().st_size
    ssh.remote_size = remote_size
    ssh.client = SimpleNamespace(open_sftp=lambda: FakeSFTP(ssh))
    md5s = list(remote_md5s)
    ssh._remote_md5 = lambda remote: md5s.pop(0)
    return ssh


@pytest.fixture
def local_file(tmp_path):
    path = tmp_path / "image.iso"
    path.write_bytes(b"nyan" * 1024)
    return path


def test_put_verifies_md5_after_copy(local_file):
    good = ssh_util.file_md5(local_file)
    ssh = make_put_ssh(local_file, [good])

    ssh.put(local_file, "/tmp/image.iso")
    assert ssh.puts == 1


def test_put_skips_when_remote_md5_matches(local_file):
    good = ssh_util.file_md5(local_file)
    ssh = make_put_ssh(local_file, [good], remote_size=local_file.stat().st_size)

    ssh.put(local_file, "/tmp/image.iso")
    assert ssh.puts == 0


def test_put_restarts_copy_on_md5_mismatch(local_file):
    good = ssh_util.file_md5(local_file)
    ssh = make_put_ssh(local_file, ["bogus", good])

    ssh.put(local_file, "/tmp/image.iso")
    assert ssh.puts == 2


def test_put_gives_up_after_max_attempts(local_file):
    ssh = make_put_ssh(local_file, ["bogus"] * 3)

    with pytest.raises(RuntimeError, match="md5 mismatch"):
        ssh.put(local_file, "/tmp/image.iso", attempts=3)
    assert ssh.puts == 3
