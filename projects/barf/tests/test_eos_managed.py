"""EOS scoped-management provider: render, diff, and deploy stay in-scope."""

from typing import Optional

import pytest

from barf.vendors import DeployDiff
from barf.vendors.arista import (
    MANAGED_USERNAME,
    EosHost,
    deterministic_sha512,
)

KEY1 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv erin"
KEY2 = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA backup"

GLOBAL_META = {"ssh_keys": [KEY1, KEY2]}

ADMIN_PW = "admin-pw-test"
ENABLE_PW = "enable-pw-test"


class FakeNode:
    """pyeapi stand-in that records everything sent to the device."""

    def __init__(self, show_output: str = ""):
        self.show_output = show_output
        self.config_calls: list = []
        self.enable_calls: list = []

    def enable(self, commands, encoding="json"):
        self.enable_calls.append(list(commands))
        return [{"result": {"output": self.show_output}} for _ in commands]

    def config(self, commands):
        self.config_calls.append(list(commands))


@pytest.fixture
def host(monkeypatch) -> EosHost:
    host = EosHost(hostname="test-eos-1", role="core")
    host.global_meta = GLOBAL_META
    secrets = {"admin-password": ADMIN_PW, "enable-password": ENABLE_PW}
    monkeypatch.setattr(
        EosHost,
        "secret",
        lambda self, key, default_value=None, secret_path=None: secrets[key],
    )
    return host


def in_sync_output(
    admin_pw: str = ADMIN_PW,
    enable_pw: str = ENABLE_PW,
    salt_seed: str = "other-device-salt",
    key1: Optional[str] = KEY1,
    key2: Optional[str] = KEY2,
) -> str:
    """Device output whose hashes use a foreign salt but matching passwords."""
    admin_hash = deterministic_sha512(admin_pw, salt_seed + ":a")
    enable_hash = deterministic_sha512(enable_pw, salt_seed + ":e")
    lines = [
        f"username {MANAGED_USERNAME} privilege 15 role network-admin"
        f" secret sha512 {admin_hash}",
        "username erin privilege 1 secret sha512 $6$untouchable$hash",
    ]
    if key1:
        lines.append(f"username {MANAGED_USERNAME} ssh-key {key1}")
    if key2:
        lines.append(f"username {MANAGED_USERNAME} ssh-key secondary {key2}")
    lines.append(f"enable password sha512 {enable_hash}")
    return "\n".join(lines)


def test_render_is_deterministic_and_scoped(host):
    first = host.render_managed_config(GLOBAL_META)
    second = host.render_managed_config(GLOBAL_META)
    assert first == second

    lines = first.strip().splitlines()
    assert len(lines) == 4
    assert lines[0].startswith(
        f"username {MANAGED_USERNAME} privilege 15 role network-admin secret sha512 $6$"
    )
    assert lines[1] == f"username {MANAGED_USERNAME} ssh-key {KEY1}"
    assert lines[2] == f"username {MANAGED_USERNAME} ssh-key secondary {KEY2}"
    assert lines[3].startswith("enable password sha512 $6$")
    # Nothing outside the managed slice, ever.
    assert not any(line.startswith("no ") for line in lines)


def test_too_many_ssh_keys_refused(host):
    with pytest.raises(ValueError, match="one primary and one secondary"):
        host.render_managed_config({"ssh_keys": [KEY1, KEY2, "ssh-rsa AAA third"]})


def test_diff_no_changes_despite_foreign_salt(host, monkeypatch):
    node = FakeNode(show_output=in_sync_output())
    monkeypatch.setattr(EosHost, "_eapi_node", lambda self: node)

    diff = host.diff_config("")
    assert isinstance(diff, DeployDiff)
    assert not diff.has_changes
    assert diff.summary == "no changes"


def test_diff_detects_wrong_enable_password(host, monkeypatch):
    node = FakeNode(show_output=in_sync_output(enable_pw="stale-enable"))
    monkeypatch.setattr(EosHost, "_eapi_node", lambda self: node)

    diff = host.diff_config("")
    assert diff.has_changes
    assert "enable password" in diff.text
    # Only the drifted item shows; the in-sync admin user does not.
    assert "username" not in diff.text


def test_diff_detects_missing_admin_and_keys(host, monkeypatch):
    node = FakeNode(show_output="username erin privilege 1 secret sha512 $6$x$y")
    monkeypatch.setattr(EosHost, "_eapi_node", lambda self: node)

    diff = host.diff_config("")
    assert diff.has_changes
    added = [line for line in diff.text.splitlines() if line.startswith("+ ")]
    # admin user, two keys, enable password.
    assert len(added) == 4


def test_diff_redacts_hashes_by_default(host, monkeypatch):
    node = FakeNode(show_output=in_sync_output(admin_pw="rotated"))
    monkeypatch.setattr(EosHost, "_eapi_node", lambda self: node)

    assert "$6$" not in host.diff_config("").text
    assert "$6$" in host.diff_config("", redact=False).text


def test_deploy_sends_only_managed_commands_and_saves(host, monkeypatch):
    node = FakeNode()
    monkeypatch.setattr(EosHost, "_eapi_node", lambda self: node)

    host.push_rendered_config("ignored")

    assert len(node.config_calls) == 1
    sent = node.config_calls[0]
    assert sent == host.managed_commands(GLOBAL_META)
    assert all(
        cmd.startswith((f"username {MANAGED_USERNAME} ", "enable password "))
        for cmd in sent
    )
    assert node.enable_calls[-1] == ["copy running-config startup-config"]


def test_diff_requires_global_meta():
    bare = EosHost(hostname="test-eos-2", role="core")
    with pytest.raises(RuntimeError, match="global_meta not attached"):
        bare._require_global_meta()
