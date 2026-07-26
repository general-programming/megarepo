package sshx

import "fmt"

// Oneshot vbash scripts for `barf device update`, a byte-for-byte port of
// barf/util/vyos_scripts.py (these run as root on production routers, so a
// text diff is a blast-radius diff). Each stage is one self-contained script
// run in a single SSH exec, printing "<STAGE>-OK"/"<STAGE>-FAIL" markers the
// caller checks IN ADDITION TO the exit code (see ScriptOK).
//
// Trap: sourcing `script-template` replaces the `exit` builtin with the
// config-mode `exit`, so a plain `exit 1` after it falls through. The
// template is sourced only where config-mode commands are needed, and every
// exit past that point uses `builtin exit`.

// Op-mode commands via the wrapper work regardless of shell context.
const scriptHeader = `#!/bin/vbash
OP=/opt/vyatta/bin/vyatta-op-cmd-wrapper
set -o pipefail
`

const configTemplate = "source /opt/vyatta/etc/functions/script-template"

// Stage markers, each naming a "<MARKER>-OK"/"<MARKER>-FAIL" line pair.
const (
	MarkerPrecheck = "PRECHECK"
	MarkerInstall  = "INSTALL"
	MarkerDrain    = "DRAIN"
	MarkerReboot   = "REBOOT"
)

// PrecheckScript refuses to touch a device with a full disk or unsaved
// config; checkDir defaults to "/".
//
// Headroom is checked on the root filesystem, where the installer puts the
// ISO and squashfs -- NOT /tmp, a small tmpfs it never touches. Failing here
// leaves the device untouched rather than dying mid-install with prompts
// being blind-fed newlines. Only committed-but-unsaved changes are visible.
func PrecheckScript(requiredMB int, checkDir string) string {
	if checkDir == "" {
		checkDir = "/"
	}
	return scriptHeader + fmt.Sprintf(`
# Disk check first, in plain bash, where `+"`exit`"+` still works normally.
free_kb="$(df --output=avail %s | tail -1 | tr -d ' ')"
if [ "$free_kb" -lt %d ]; then
    echo "PRECHECK-FAIL: less than %dMB free in %s"
    exit 1
fi

%s
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
`, checkDir, requiredMB*1024, requiredMB, checkDir, configTemplate)
}

// InstallScript installs the image from its mirror URL and makes it the
// default boot image. The device fetches imageURL and "<imageURL>.minisig"
// itself, verifying against its bundled release key; unsigned, the
// continue-without-verification prompt gets a bare newline like every other
// prompt, taking the installer's default rather than answering yes.
// Idempotence lives in the caller: a directory in /boot does not prove the
// image is the default boot image.
func InstallScript(imageURL, version string) string {
	return scriptHeader + fmt.Sprintf(`
# A previously interrupted install leaves /mnt/installation mounted and
# populated, and the installer dies on it with "[Errno 17] File exists".
# Nothing else uses that path, so sweep it before we start.
if [ -d /mnt/installation ]; then
    echo "clearing leftovers from a previously interrupted install"
    for mountpoint in $(mount | awk '$3 ~ "^/mnt/installation" {print $3}' | sort -r); do
        sudo umount "$mountpoint"
    done
    sudo rm -rf /mnt/installation
fi

# Accept the installer's defaults: image name, default boot, copy config,
# copy SSH host keys. The NAME prompt comes first, so every answer must be
# a plain newline -- a literal "y" there names the image "y".
printf '\n\n\n\n\n' | $OP add system image "%s"

if [ -d "/lib/live/mount/persistence/boot/%s" ] || [ -d "/boot/%s" ]; then
    echo "INSTALL-OK: image %s installed and set for next boot"
else
    echo "INSTALL-FAIL: %s not present after add system image"
    exit 1
fi
`, imageURL, version, version, version, version)
}

// DrainAndRebootScript gracefully drains BGP, then reboots into the staged
// image; drainWait is in seconds. The BGP shutdown is committed but
// deliberately NOT saved, so the device boots with peering enabled again --
// rollback if the reboot goes sideways.
func DrainAndRebootScript(version string, drainWait int, drainBGP bool) string {
	drain := ""
	if drainBGP {
		drain = fmt.Sprintf(`
%s
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

echo "DRAIN-OK: bgp shutdown committed (not saved), waiting %ds"
sleep %d
`, configTemplate, drainWait, drainWait)
	}

	return scriptHeader + drain + fmt.Sprintf(`
echo "REBOOT-OK: going down for image %s"
# The op-mode reboot handles privileges itself (sudo would silently die
# waiting for a password with no tty). This script runs detached from
# the SSH session (Client.RunDetached), because the drain above cuts
# the very BGP paths that session rides on.
$OP reboot now
`, version)
}
