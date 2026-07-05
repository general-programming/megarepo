from types import SimpleNamespace

import pytest

from barf.vendors import BaseHost
from barf.vendors.vyos import VyOSHost, _parse_uptime, _parse_version

SHOW_VERSION = """
Version:          VyOS 2026.03.28-0028-rolling
Release train:    current

Built by:         autobuild@vyos.net
Built on:         Sat 28 Mar 2026 02:48 UTC
"""

SHOW_UPTIME = """
Uptime: 9w 6h 53m 36s

Load average:
1  minute:   0.00%
"""


def test_parse_version_strips_vyos_prefix():
    assert _parse_version(SHOW_VERSION) == "2026.03.28-0028-rolling"


def test_parse_version_without_prefix():
    assert _parse_version("Version: 1.5.0") == "1.5.0"


def test_parse_version_fallback_first_line():
    assert _parse_version("VyOS 2026.06.30-0048-rolling\nsomething") == (
        "2026.06.30-0048-rolling"
    )


def test_parse_version_empty():
    assert _parse_version("") == "-"


def test_parse_uptime():
    assert _parse_uptime(SHOW_UPTIME) == "9w 6h 53m 36s"


def test_parse_uptime_fallback_first_line():
    assert _parse_uptime("up 4 days\n") == "up 4 days"


def test_parse_uptime_empty():
    assert _parse_uptime("") == "-"


def make_host(hostname: str = "testbox") -> VyOSHost:
    return VyOSHost(hostname=hostname, role="vpn", asn=64512)


@pytest.fixture
def api(monkeypatch):
    """Fake the API surface _api_show touches; returns a settable output."""
    env = SimpleNamespace(output="")
    monkeypatch.setattr(VyOSHost, "management_ip", "10.0.0.9")
    monkeypatch.setattr(
        "barf.util.secrets.VaultSecrets",
        lambda: SimpleNamespace(vyos_api_password="key"),
    )
    monkeypatch.setattr(
        "barf.util.vyos_api.vyos_api_show",
        lambda address, key, path, timeout=10: env.output,
    )
    return env


def test_human_version_via_api(api):
    api.output = SHOW_VERSION
    assert make_host().human_version() == "2026.03.28-0028-rolling"


def test_uptime_via_api(api):
    api.output = SHOW_UPTIME
    assert make_host().uptime() == "9w 6h 53m 36s"


def test_version_treats_placeholder_output_as_dead(api):
    # version() is the liveness probe: a half-booted API answering with
    # empty data must not count as alive.
    api.output = ""
    assert make_host().human_version() == "-"
    assert make_host().version() is None


def test_base_version_is_none_when_unsupported():
    host = BaseHost(hostname="testbox", role="vpn")
    with pytest.raises(NotImplementedError):
        host.human_version()
    # version() wraps human_version and swallows the failure.
    assert host.version() is None


def alive_host(hostname: str, alive: bool = True) -> VyOSHost:
    host = VyOSHost(hostname=hostname, role="vpn", asn=64512)
    host.version = (lambda: "x") if alive else (lambda: None)
    return host


def test_safe_to_reboot_with_redundancy():
    me = alive_host("fmt2-vpn-leaf-1")
    fleet = [me, alive_host("fmt2-vpn-spine-1"), alive_host("fmt2-vpn-leaf-2")]
    assert me.safe_to_reboot(fleet) is True


def test_safe_to_reboot_refuses_last_spine():
    me = alive_host("fmt2-vpn-spine-1")
    fleet = [
        me,
        alive_host("fmt2-vpn-spine-2", alive=False),
        alive_host("fmt2-vpn-leaf-1"),
    ]
    with pytest.raises(RuntimeError, match="no other spine is online"):
        me.safe_to_reboot(fleet)


def test_safe_to_reboot_refuses_last_leaf():
    me = alive_host("fmt2-vpn-leaf-1")
    fleet = [
        me,
        alive_host("fmt2-vpn-spine-1"),
        alive_host("fmt2-vpn-leaf-2", alive=False),
    ]
    with pytest.raises(RuntimeError, match="no other leaf is alive"):
        me.safe_to_reboot(fleet)


class TestVyOSDiffConfig:
    """diff_config runs locally against the /retrieve JSON tree."""

    def wire(self, monkeypatch, running: dict):
        monkeypatch.setattr(VyOSHost, "management_ip", "10.0.0.9")
        monkeypatch.setattr(
            "barf.util.secrets.VaultSecrets",
            lambda: SimpleNamespace(vyos_api_password="key"),
        )
        monkeypatch.setattr(
            "barf.util.vyos_api.vyos_api_retrieve_config",
            lambda address, key, path=None, timeout=30: running,
        )

    def test_no_changes(self, monkeypatch):
        self.wire(monkeypatch, {"system": {"host-name": "testbox"}})
        diff = make_host().diff_config("set system host-name testbox\n")
        assert not diff.has_changes
        assert diff.summary == "no changes"

    def test_addition_and_removal(self, monkeypatch):
        self.wire(
            monkeypatch,
            {
                "system": {
                    "host-name": "oldname",
                    "login": {"user": {"vyos": {"level": "admin"}}},
                },
                "interfaces": {"ethernet": {"eth0": {"hw-id": "aa:bb"}}},
                "service": {"ssh": {"port": "22"}},
            },
        )
        diff = make_host().diff_config(
            "set system host-name testbox\nset system time-zone UTC\n"
        )
        assert diff.has_changes
        assert "+ set system host-name testbox" in diff.text
        assert "- set system host-name oldname" in diff.text
        assert "+ set system time-zone UTC" in diff.text
        # Everything unrendered is a real removal now — including the
        # factory vyos account — while ignored hw-id vanishes from the
        # diff entirely.
        assert "- set service ssh port 22" in diff.text
        assert "- set system login user vyos level admin" in diff.text
        assert "hw-id" not in diff.text
        assert "device-only" not in diff.text

    def test_secret_values_redacted(self, monkeypatch):
        self.wire(monkeypatch, {})
        diff = make_host().diff_config(
            "set interfaces wireguard wg1 private-key 'hunter2'\n"
        )
        assert "hunter2" not in diff.text
        assert "<redacted>" in diff.text


class TestVyOSPushRenderedConfig:
    """push_rendered_config drives the HTTPS /configure endpoint."""

    def wire(self, monkeypatch, running: dict):
        monkeypatch.setattr(VyOSHost, "management_ip", "10.0.0.9")
        monkeypatch.setattr(
            "barf.util.secrets.VaultSecrets",
            lambda: SimpleNamespace(vyos_api_password="key"),
        )
        monkeypatch.setattr(
            "barf.util.vyos_api.vyos_api_retrieve_config",
            lambda address, key, path=None, timeout=30: running,
        )
        calls = SimpleNamespace(configure=None, saved=False)
        monkeypatch.setattr(
            "barf.util.vyos_api.vyos_api_configure",
            lambda address, key, ops, timeout=120: calls.__setattr__("configure", ops),
        )
        monkeypatch.setattr(
            "barf.util.vyos_api.vyos_api_config_save",
            lambda address, key, timeout=60: calls.__setattr__("saved", True),
        )
        return calls

    def test_no_changes_pushes_nothing(self, monkeypatch):
        calls = self.wire(monkeypatch, {"system": {"host-name": "testbox"}})
        make_host().push_rendered_config("set system host-name testbox\n")
        assert calls.configure is None
        assert calls.saved is False

    def test_owned_deletes_come_before_sets(self, monkeypatch):
        calls = self.wire(
            monkeypatch,
            # host-name survives (also rendered), so the delete
            # collapse anchors at the name-server node instead of
            # rising to bare "system".
            {
                "system": {
                    "name-server": ["2606:4700::1111", "2606:4700::1001"],
                    "host-name": "testbox",
                }
            },
        )
        make_host().push_rendered_config(
            "set system host-name testbox\n"
            "set system name-server 2606:4700:4700::1111\n"
            "set system name-server 2606:4700:4700::1001\n"
        )
        assert calls.configure == [
            # Every stale value is being removed, so the whole node is
            # deleted before the correct values are set.
            {"op": "delete", "path": ["system", "name-server"]},
            {"op": "set", "path": ["system", "name-server", "2606:4700:4700::1001"]},
            {"op": "set", "path": ["system", "name-server", "2606:4700:4700::1111"]},
        ]
        assert calls.saved is True

    def test_ignored_config_is_never_deleted(self, monkeypatch):
        calls = self.wire(
            monkeypatch,
            # The rendered time-zone anchors the delete collapse below
            # bare "system"; the ignored ethernet hw-id must survive.
            {
                "system": {"host-name": "old", "time-zone": "UTC"},
                "interfaces": {"ethernet": {"eth0": {"hw-id": "aa:bb"}}},
            },
        )
        make_host().push_rendered_config(
            "set system host-name testbox\nset system time-zone UTC\n"
        )
        assert calls.configure == [
            # The stale value collapses to its node: host-name has no
            # other running config beneath it. The ignored hw-id may
            # not appear as a delete.
            {"op": "delete", "path": ["system", "host-name"]},
            {"op": "set", "path": ["system", "host-name", "testbox"]},
        ]
        assert calls.saved is True
