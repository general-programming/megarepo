"""Oneshot vbash scripts for ``barf device update``.

Each stage of the updater is a single self-contained script executed in
one SSH exec, so we never dribble commands at a device one by one. The
scripts print ``<STAGE>-OK`` / ``<STAGE>-FAIL`` markers that the caller
checks in addition to the exit code.

Trap: sourcing ``script-template`` replaces the ``exit`` builtin with the
config-mode ``exit`` command, so a plain ``exit 1`` after it silently
falls through and the script keeps running. The template is therefore
only sourced where config-mode commands are needed, and every real
script exit after that point uses ``builtin exit``.
"""

import posixpath

# Op-mode commands via the wrapper work regardless of shell context.
_HEADER = """#!/bin/vbash
OP=/opt/vyatta/bin/vyatta-op-cmd-wrapper
set -o pipefail
"""

_CONFIG_TEMPLATE = "source /opt/vyatta/etc/functions/script-template"


def precheck(image_path: str, required_mb: int) -> str:
    """Refuse to touch a device with a full disk or unsaved config.

    An already-staged copy at ``image_path`` is overwritten by the
    transfer, so its size counts toward the free space.

    Note: this catches committed-but-unsaved changes. Uncommitted edits
    live only inside another user's config session and are not visible
    from here.
    """
    image_dir = posixpath.dirname(image_path) or "/tmp"
    return (
        _HEADER
        + f"""
# Disk check first, in plain bash, where `exit` still works normally.
free_kb="$(df --output=avail {image_dir} | tail -1 | tr -d ' ')"
existing_kb=0
if [ -f "{image_path}" ]; then
    existing_kb="$(du -k "{image_path}" | cut -f1)"
fi
if [ "$((free_kb + existing_kb))" -lt {required_mb * 1024} ]; then
    echo "PRECHECK-FAIL: less than {required_mb}MB free in {image_dir} (counting the $((existing_kb / 1024))MB reusable image)"
    exit 1
fi

{_CONFIG_TEMPLATE}
configure
diff="$(compare saved)"
exit

if ! echo "$diff" | grep -q "No changes"; then
    echo "PRECHECK-FAIL: committed but unsaved configuration changes:"
    echo "$diff"
    builtin exit 1
fi

echo "PRECHECK-OK"
builtin exit 0
"""
    )


def install(image_path: str, version: str) -> str:
    """Install the copied image and make it the default boot image.

    Idempotence lives in the caller: a directory in /boot alone does not
    prove the image is the default boot image, so the caller checks
    ``show system image`` via the API and only runs this script when a
    real install is needed.
    """
    return (
        _HEADER
        + f"""
# A previously interrupted install leaves /mnt/installation mounted and
# populated, and the installer dies on it with "[Errno 17] File exists".
# Nothing else uses that path, so sweep it before we start.
if [ -d /mnt/installation ]; then
    echo "clearing leftovers from a previously interrupted install"
    for mountpoint in $(mount | awk '$3 ~ "^/mnt/installation" {{print $3}}' | sort -r); do
        sudo umount "$mountpoint"
    done
    sudo rm -rf /mnt/installation
fi

# Accept the installer's defaults: image name, default boot, copy config,
# copy SSH host keys. The NAME prompt comes first, so every answer must be
# a plain newline -- a literal "y" there names the image "y".
printf '\\n\\n\\n\\n\\n' | $OP add system image "{image_path}"

if [ -d "/lib/live/mount/persistence/boot/{version}" ] || [ -d "/boot/{version}" ]; then
    rm -f "{image_path}"
    echo "INSTALL-OK: image {version} installed and set for next boot"
else
    echo "INSTALL-FAIL: {version} not present after add system image"
    exit 1
fi
"""
    )


def drain_and_reboot(version: str, drain_wait: int, drain_bgp: bool) -> str:
    """Gracefully drain BGP, then reboot into the staged image.

    The BGP shutdown is committed but deliberately NOT saved: the device
    boots into the new image with peering enabled again automatically,
    which doubles as rollback if the reboot goes sideways.
    """
    drain = ""
    if drain_bgp:
        drain = f"""
{_CONFIG_TEMPLATE}
configure
if ! set protocols bgp parameters shutdown; then
    echo "DRAIN-FAIL: could not stage the bgp shutdown"
    builtin exit 1
fi
if ! commit; then
    echo "DRAIN-FAIL: commit failed (another config session open?)"
    builtin exit 1
fi
exit

echo "DRAIN-OK: bgp shutdown committed (not saved), waiting {drain_wait}s"
sleep {drain_wait}
"""

    return (
        _HEADER
        + drain
        + f"""
echo "REBOOT-OK: going down for image {version}"
# The op-mode reboot handles privileges itself (sudo would silently die
# waiting for a password with no tty). This script runs detached from
# the SSH session (DeviceSSH.run_detached), because the drain above cuts
# the very BGP paths that session rides on.
$OP reboot now
"""
    )
