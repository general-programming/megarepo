import math

import pytest

from barf.util.vyos_api import SystemImage
from barf.vendors import BaseHost
from barf.vendors.vyos import VyOSHost

IMAGE_BYTES = 3 * 2**20  # 3 MiB -> the 2048MB installer floor dominates
IMAGE_URL = (
    "https://files.owo.me/firmware/vyos-2026.06.30-0048-rolling-generic-amd64.iso"
)


class FakeDeviceSSH:
    """Records scripts run on the device; returns happy-path markers.

    Deliberately has no ``put``: images are never pushed over SFTP
    anymore, so any regression that tries blows up with AttributeError.
    """

    instances = []
    script_results = {}

    def __init__(self, host, address, **kwargs):
        self.host = host
        self.address = address
        self.scripts = []
        self.detached = []
        type(self).instances.append(self)

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        pass

    def run_script(self, name, content, **kwargs):
        self.scripts.append((name, content))
        return type(self).script_results.get(
            name, (0, "PRECHECK-OK INSTALL-OK DRAIN-OK REBOOT-OK")
        )

    def run_detached(self, name, content):
        self.detached.append((name, content))
        return f"/tmp/{name}.log"


@pytest.fixture
def fake_ssh(monkeypatch):
    FakeDeviceSSH.instances = []
    FakeDeviceSSH.script_results = {}
    monkeypatch.setattr("barf.util.ssh.DeviceSSH", FakeDeviceSSH)
    monkeypatch.setattr(VyOSHost, "management_ip", "10.0.0.9")
    monkeypatch.setattr(VyOSHost, "ssh_ip", "10.0.0.9")
    monkeypatch.setattr(VyOSHost, "system_images", lambda self: [])
    monkeypatch.setattr(
        VyOSHost,
        "_api_image_delete",
        lambda self, name: type(self)._deleted_images.append(name),
    )
    monkeypatch.setattr(
        VyOSHost, "_detached_reboot_failure", lambda self, address, log: None
    )
    monkeypatch.setattr("barf.vendors.vyos.time.sleep", lambda seconds: None)
    VyOSHost._deleted_images = []
    return FakeDeviceSSH


def make_host():
    return VyOSHost(hostname="testbox", role="vpn", asn=64512)


def test_base_host_does_not_support_updates():
    host = BaseHost(hostname="testbox", role="vpn")
    with pytest.raises(NotImplementedError):
        host.update_host(IMAGE_URL, IMAGE_BYTES)


def test_full_update_installs_and_reboots(fake_ssh):
    result = make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="1.5-rolling")

    ssh = fake_ssh.instances[0]
    assert result == "rebooting into 1.5-rolling"
    assert [name for name, _ in ssh.scripts] == [
        "barf-precheck.sh",
        "barf-install.sh",
    ]
    # The drain+reboot script detaches from the session: the drain cuts
    # the BGP paths the session itself rides on.
    assert [name for name, _ in ssh.detached] == ["barf-reboot.sh"]


def test_install_uses_the_mirror_url(fake_ssh):
    make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="1.5-rolling")

    install_script = dict(fake_ssh.instances[0].scripts)["barf-install.sh"]
    assert f'add system image "{IMAGE_URL}"' in install_script


def test_version_derived_from_url(fake_ssh):
    make_host().update_host(IMAGE_URL, IMAGE_BYTES)

    ssh = fake_ssh.instances[0]
    install_script = dict(ssh.scripts)["barf-install.sh"]
    assert "2026.06.30-0048-rolling" in install_script


def test_precheck_requires_installer_floor_on_root_fs(fake_ssh):
    # The installer refuses below 2GiB free on /, so the precheck demands
    # the same floor even for small images -- and on /, not the /tmp
    # tmpfs the installer never touches.
    make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="x")

    precheck_script = dict(fake_ssh.instances[0].scripts)["barf-precheck.sh"]
    assert "less than 2048MB free in /" in precheck_script
    assert "/tmp" not in precheck_script


def test_precheck_scales_with_oversized_images(fake_ssh):
    # ISO lands in $HOME plus a squashfs copy to /boot, both on /: an
    # image over 1GiB needs more than the installer's 2GiB floor.
    size = 3 * 2**30
    make_host().update_host(IMAGE_URL, size, version="x")

    precheck_script = dict(fake_ssh.instances[0].scripts)["barf-precheck.sh"]
    required_mb = math.ceil(size * 2 / 2**20)
    assert required_mb > 2048
    assert f"less than {required_mb}MB free in /" in precheck_script


def test_precheck_failure_stops_the_update(fake_ssh):
    fake_ssh.script_results["barf-precheck.sh"] = (1, "PRECHECK-FAIL: dirty")

    with pytest.raises(RuntimeError, match="pre-checks failed"):
        make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="x")

    ssh = fake_ssh.instances[0]
    assert [name for name, _ in ssh.scripts] == ["barf-precheck.sh"]
    assert ssh.detached == []


def test_already_default_boot_skips_straight_to_reboot(fake_ssh, monkeypatch):
    monkeypatch.setattr(
        VyOSHost,
        "system_images",
        lambda self: [SystemImage("1.5-rolling", default_boot=True)],
    )

    result = make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="1.5-rolling")

    ssh = fake_ssh.instances[0]
    assert result == "rebooting into 1.5-rolling"
    # No precheck/install; only the detached drain+reboot runs.
    assert ssh.scripts == []
    assert [name for name, _ in ssh.detached] == ["barf-reboot.sh"]


def test_stale_image_is_deleted_and_reinstalled(fake_ssh, monkeypatch):
    # Present but NOT default boot: an interrupted install must not be
    # trusted, or the reboot lands on the old default image.
    monkeypatch.setattr(
        VyOSHost,
        "system_images",
        lambda self: [SystemImage("1.5-rolling", default_boot=False)],
    )

    make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="1.5-rolling")

    assert VyOSHost._deleted_images == ["1.5-rolling"]
    ssh = fake_ssh.instances[0]
    assert [name for name, _ in ssh.scripts] == [
        "barf-precheck.sh",
        "barf-install.sh",
    ]
    assert [name for name, _ in ssh.detached] == ["barf-reboot.sh"]


def test_failed_precheck_leaves_stale_image_alone(fake_ssh, monkeypatch):
    # The stale-image delete runs only after the precheck passes: a
    # failed precheck must leave the device completely untouched.
    monkeypatch.setattr(
        VyOSHost,
        "system_images",
        lambda self: [SystemImage("1.5-rolling", default_boot=False)],
    )
    fake_ssh.script_results["barf-precheck.sh"] = (1, "PRECHECK-FAIL: full disk")

    with pytest.raises(RuntimeError, match="pre-checks failed"):
        make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="1.5-rolling")

    assert VyOSHost._deleted_images == []


def test_drain_failure_surfaced_from_detached_log(fake_ssh, monkeypatch):
    # The device stayed reachable after the drain window and its log has
    # a FAIL marker: the script bailed, no reboot is coming.
    monkeypatch.setattr(
        VyOSHost,
        "_detached_reboot_failure",
        lambda self, address, log: "DRAIN-FAIL: commit failed",
    )

    with pytest.raises(RuntimeError, match="DRAIN-FAIL: commit failed"):
        make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="x")


class FakeLogSSH:
    """DeviceSSH stand-in for reading the detached reboot log."""

    log = ""
    connect_error = None

    def __init__(self, host, address, **kwargs):
        if type(self).connect_error:
            raise type(self).connect_error

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        pass

    def run(self, command, timeout=60):
        return 0, type(self).log


def test_detached_failure_unreachable_means_success(monkeypatch):
    FakeLogSSH.connect_error = OSError("no route to host")
    monkeypatch.setattr("barf.util.ssh.DeviceSSH", FakeLogSSH)

    host = VyOSHost(hostname="testbox", role="vpn", asn=64512)
    assert host._detached_reboot_failure("10.0.0.9", "/tmp/x.log") is None


def test_detached_failure_reports_fail_marker(monkeypatch):
    FakeLogSSH.connect_error = None
    FakeLogSSH.log = "DRAIN-FAIL: commit failed (another config session open?)"
    monkeypatch.setattr("barf.util.ssh.DeviceSSH", FakeLogSSH)

    host = VyOSHost(hostname="testbox", role="vpn", asn=64512)
    failure = host._detached_reboot_failure("10.0.0.9", "/tmp/x.log")
    assert failure == "DRAIN-FAIL: commit failed (another config session open?)"


def test_detached_failure_clean_log_is_success(monkeypatch):
    FakeLogSSH.connect_error = None
    FakeLogSSH.log = "DRAIN-OK: waiting\nREBOOT-OK: going down"
    monkeypatch.setattr("barf.util.ssh.DeviceSSH", FakeLogSSH)

    host = VyOSHost(hostname="testbox", role="vpn", asn=64512)
    assert host._detached_reboot_failure("10.0.0.9", "/tmp/x.log") is None


def test_precheck_fail_marker_kills_update_even_with_rc_zero(fake_ssh):
    # Regression: script-template hijacks `exit`, so a failed precheck can
    # still end with rc 0 and a PRECHECK-OK printed after the failure. The
    # FAIL marker alone must stop the update before any install happens.
    fake_ssh.script_results["barf-precheck.sh"] = (
        0,
        "PRECHECK-FAIL: less than 2048MB free in /\nPRECHECK-OK",
    )

    with pytest.raises(RuntimeError, match="pre-checks failed"):
        make_host().update_host(IMAGE_URL, IMAGE_BYTES, version="x")

    ssh = fake_ssh.instances[0]
    assert [name for name, _ in ssh.scripts] == ["barf-precheck.sh"]
    assert ssh.detached == []
