import shutil
import subprocess

import pytest

from barf.util import vyos_scripts

VERSION = "2026.06.30-0048-rolling"
TEMPLATE = "source /opt/vyatta/etc/functions/script-template"


def test_precheck_contents():
    script = vyos_scripts.precheck("/tmp/x.iso", required_mb=1024)
    assert script.startswith("#!/bin/vbash")
    assert "df --output=avail /tmp" in script
    assert str(1024 * 1024) in script
    assert "compare saved" in script
    assert "PRECHECK-OK" in script
    assert "PRECHECK-FAIL" in script


def test_precheck_counts_existing_image_as_free_space():
    # An already-staged copy gets overwritten, so its size is reusable.
    script = vyos_scripts.precheck("/tmp/x.iso", required_mb=1024)
    assert 'du -k "/tmp/x.iso"' in script
    assert "free_kb + existing_kb" in script


def test_precheck_disk_check_runs_before_script_template():
    # script-template hijacks `exit`, so the disk check (and its exit 1)
    # must happen before the template is sourced, and every later exit
    # must use `builtin exit`.
    script = vyos_scripts.precheck("/tmp/x.iso", required_mb=1024)
    assert script.index("df --output=avail") < script.index(TEMPLATE)
    assert "builtin exit 1" in script
    assert "builtin exit 0" in script


def test_install_contents():
    script = vyos_scripts.install(f"/tmp/vyos-{VERSION}.iso", VERSION)
    assert f'add system image "/tmp/vyos-{VERSION}.iso"' in script
    # No early-exit on a mere /boot directory: idempotence is decided by
    # the caller via `show system image`, so the installer always runs.
    assert "already installed" not in script
    # Presence is verified on the filesystem, not via the op wrapper.
    assert f"/lib/live/mount/persistence/boot/{VERSION}" in script
    # The copied image is cleaned up after a successful install.
    assert "rm -f" in script
    assert "INSTALL-OK" in script
    assert "INSTALL-FAIL" in script
    # No config-mode commands here, so the exit-hijacking template must
    # not be sourced at all.
    assert TEMPLATE not in script


def test_install_sweeps_stale_installer_leftovers():
    # An interrupted install leaves /mnt/installation behind and the next
    # run dies with "[Errno 17] File exists" unless it is cleared first.
    script = vyos_scripts.install("/tmp/x.iso", VERSION)
    sweep = script.index("rm -rf /mnt/installation")
    assert "umount" in script
    assert "awk '$3 ~ \"^/mnt/installation\" {print $3}'" in script
    # Deepest mounts unmount first, and the sweep runs before the installer.
    assert "sort -r" in script
    assert sweep < script.index("add system image")


def test_install_answers_prompts_with_defaults_only():
    # The installer's FIRST prompt is the image name: answering "y" would
    # name the image "y", so every scripted answer must be a bare newline.
    script = vyos_scripts.install("/tmp/x.iso", VERSION)
    assert "printf '\\n\\n\\n\\n\\n'" in script
    assert "printf 'y" not in script


def test_drain_and_reboot_with_bgp():
    script = vyos_scripts.drain_and_reboot(VERSION, drain_wait=45, drain_bgp=True)
    assert TEMPLATE in script
    assert "set protocols bgp parameters shutdown" in script
    assert "sleep 45" in script
    assert "REBOOT-OK" in script
    # A failed set/commit must surface instead of rebooting undrained.
    assert script.count("DRAIN-FAIL") == 2
    # The shutdown must never be saved, or the device boots drained.
    assert "\nsave" not in script


def test_reboot_uses_op_mode_not_sudo():
    # sudo systemctl silently dies waiting for a password with no tty;
    # the op-mode reboot handles privileges itself. Detaching is the
    # transport's job (run_detached), not the script's.
    script = vyos_scripts.drain_and_reboot(VERSION, drain_wait=5, drain_bgp=True)
    assert "$OP reboot now" in script
    assert "systemctl reboot" not in script
    assert "nohup" not in script


def test_drain_and_reboot_without_bgp():
    script = vyos_scripts.drain_and_reboot(VERSION, drain_wait=45, drain_bgp=False)
    assert "bgp" not in script
    assert TEMPLATE not in script
    assert "REBOOT-OK" in script


@pytest.mark.skipif(shutil.which("bash") is None, reason="bash not available")
@pytest.mark.parametrize(
    "script",
    [
        vyos_scripts.precheck("/tmp/x.iso", 1024),
        vyos_scripts.install("/tmp/x.iso", VERSION),
        vyos_scripts.drain_and_reboot(VERSION, 30, True),
        vyos_scripts.drain_and_reboot(VERSION, 30, False),
    ],
)
def test_scripts_are_valid_bash(script):
    result = subprocess.run(
        ["bash", "-n"], input=script, capture_output=True, text=True
    )
    assert result.returncode == 0, result.stderr
