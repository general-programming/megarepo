import os
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


class FakeChannel:
    """A shell channel serving scripted recv() chunks."""

    def __init__(self, chunks, status=0):
        self.chunks = list(chunks)
        self.status = status
        self.sent = b""
        self.resizes = []
        self.closed = False

    def settimeout(self, timeout):
        pass

    def recv(self, size):
        return self.chunks.pop(0)

    def sendall(self, data):
        self.sent += data

    def resize_pty(self, width, height):
        self.resizes.append((width, height))

    def exit_status_ready(self):
        return True

    def recv_exit_status(self):
        return self.status

    def close(self):
        self.closed = True


def make_shell_ssh(channel, invoke_kwargs):
    ssh = object.__new__(ssh_util.DeviceSSH)
    ssh.host = SimpleNamespace(hostname="testbox", devicetype="vyos")
    ssh.address = "10.0.0.1"
    ssh.username = "supertech"

    def invoke_shell(**kwargs):
        invoke_kwargs.update(kwargs)
        return channel

    ssh.client = SimpleNamespace(invoke_shell=invoke_shell)
    return ssh


def test_interactive_shell_requires_a_tty(monkeypatch):
    monkeypatch.setattr(
        ssh_util, "sys", SimpleNamespace(stdin=SimpleNamespace(isatty=lambda: False))
    )
    ssh = make_shell_ssh(FakeChannel([]), {})

    with pytest.raises(RuntimeError, match="interactive terminal"):
        ssh.interactive_shell()


def test_interactive_shell_pumps_and_restores_terminal(monkeypatch):
    channel = FakeChannel([b"hello ", b"world", b""], status=3)
    invoke_kwargs = {}
    ssh = make_shell_ssh(channel, invoke_kwargs)

    stdin_r, stdin_w = os.pipe()
    stdout_r, stdout_w = os.pipe()
    os.write(stdin_w, b"ls\n")

    monkeypatch.setattr(
        ssh_util,
        "sys",
        SimpleNamespace(
            stdin=SimpleNamespace(isatty=lambda: True, fileno=lambda: stdin_r),
            stdout=SimpleNamespace(fileno=lambda: stdout_w),
        ),
    )
    monkeypatch.setattr(
        ssh_util,
        "shutil",
        SimpleNamespace(get_terminal_size=lambda: os.terminal_size((120, 40))),
    )

    tcsetattrs = []
    monkeypatch.setattr(
        ssh_util,
        "termios",
        SimpleNamespace(
            tcgetattr=lambda fd: "saved-attrs",
            tcsetattr=lambda fd, when, attrs: tcsetattrs.append(attrs),
            TCSADRAIN=object(),
        ),
    )
    monkeypatch.setattr(ssh_util, "tty", SimpleNamespace(setraw=lambda fd: None))

    handlers = []

    def fake_signal(signum, handler):
        handlers.append(handler)
        return "prior-handler"

    monkeypatch.setattr(
        ssh_util, "signal", SimpleNamespace(signal=fake_signal, SIGWINCH=28)
    )

    # Local keystrokes first, then remote output until the remote EOF.
    selects = iter(
        [
            ([stdin_r], [], []),
            ([channel], [], []),
            ([channel], [], []),
            ([channel], [], []),
        ]
    )
    monkeypatch.setattr(
        ssh_util, "select", SimpleNamespace(select=lambda *args: next(selects))
    )

    try:
        status = ssh.interactive_shell()
    finally:
        for fd in (stdin_r, stdin_w, stdout_w):
            os.close(fd)

    assert status == 3
    assert channel.closed
    assert channel.sent == b"ls\n"
    assert os.read(stdout_r, 65536) == b"hello world"
    os.close(stdout_r)

    assert invoke_kwargs["width"] == 120
    assert invoke_kwargs["height"] == 40

    # The tty attrs were restored and the prior SIGWINCH handler put back.
    assert tcsetattrs == ["saved-attrs"]
    assert handlers[-1] == "prior-handler"

    # The registered resize handler forwards the new terminal size.
    handlers[0](28, None)
    assert channel.resizes == [(120, 40)]
