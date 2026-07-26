package sshx

import (
	"strings"
	"testing"
)

// Golden copies of the oneshot scripts, diffed byte-for-byte against
// projects/barf/barf/util/vyos_scripts.py (only intentional difference:
// one comment naming the Go API instead of DeviceSSH.run_detached). They
// run as root on production routers, so they are pinned, not described.

func TestPrecheckScriptGolden(t *testing.T) {
	want := "#!/bin/vbash\nOP=/opt/vyatta/bin/vyatta-op-cmd-wrapper\nset -o pipefail\n\n# Disk check first, in plain bash, where `exit` still works normally.\nfree_kb=\"$(df --output=avail / | tail -1 | tr -d ' ')\"\nif [ \"$free_kb\" -lt 2097152 ]; then\n    echo \"PRECHECK-FAIL: less than 2048MB free in /\"\n    exit 1\nfi\n\nsource /opt/vyatta/etc/functions/script-template\nconfigure\ndiff=\"$(compare saved)\"\nexit\n\nif ! echo \"$diff\" | grep -q \"No changes\"; then\n    echo \"PRECHECK-FAIL: committed but unsaved configuration changes:\"\n    echo \"$diff\"\n    builtin exit 1\nfi\n\necho \"PRECHECK-OK\"\nbuiltin exit 0\n"
	if got := PrecheckScript(2048, "/"); got != want {
		t.Fatalf("precheck script drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestInstallScriptGolden(t *testing.T) {
	want := "#!/bin/vbash\nOP=/opt/vyatta/bin/vyatta-op-cmd-wrapper\nset -o pipefail\n\n# A previously interrupted install leaves /mnt/installation mounted and\n# populated, and the installer dies on it with \"[Errno 17] File exists\".\n# Nothing else uses that path, so sweep it before we start.\nif [ -d /mnt/installation ]; then\n    echo \"clearing leftovers from a previously interrupted install\"\n    for mountpoint in $(mount | awk '$3 ~ \"^/mnt/installation\" {print $3}' | sort -r); do\n        sudo umount \"$mountpoint\"\n    done\n    sudo rm -rf /mnt/installation\nfi\n\n# Accept the installer's defaults: image name, default boot, copy config,\n# copy SSH host keys. The NAME prompt comes first, so every answer must be\n# a plain newline -- a literal \"y\" there names the image \"y\".\nprintf '\\n\\n\\n\\n\\n' | $OP add system image \"https://ex/vyos-1.iso\"\n\nif [ -d \"/lib/live/mount/persistence/boot/1.5\" ] || [ -d \"/boot/1.5\" ]; then\n    echo \"INSTALL-OK: image 1.5 installed and set for next boot\"\nelse\n    echo \"INSTALL-FAIL: 1.5 not present after add system image\"\n    exit 1\nfi\n"
	if got := InstallScript("https://ex/vyos-1.iso", "1.5"); got != want {
		t.Fatalf("install script drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDrainAndRebootScriptGolden(t *testing.T) {
	want := "#!/bin/vbash\nOP=/opt/vyatta/bin/vyatta-op-cmd-wrapper\nset -o pipefail\n\nsource /opt/vyatta/etc/functions/script-template\nconfigure\nif ! set protocols bgp parameters shutdown; then\n    echo \"DRAIN-FAIL: could not stage the bgp shutdown\"\n    builtin exit 1\nfi\nif ! commit; then\n    echo \"DRAIN-FAIL: commit failed (another config session open?)\"\n    builtin exit 1\nfi\nexit\n\necho \"DRAIN-OK: bgp shutdown committed (not saved), waiting 5s\"\nsleep 5\n\necho \"REBOOT-OK: going down for image 1.5\"\n# The op-mode reboot handles privileges itself (sudo would silently die\n# waiting for a password with no tty). This script runs detached from\n# the SSH session (Client.RunDetached), because the drain above cuts\n# the very BGP paths that session rides on.\n$OP reboot now\n"
	if got := DrainAndRebootScript("1.5", 5, true); got != want {
		t.Fatalf("drain+reboot script drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDrainAndRebootWithoutBGP(t *testing.T) {
	want := "#!/bin/vbash\nOP=/opt/vyatta/bin/vyatta-op-cmd-wrapper\nset -o pipefail\n\necho \"REBOOT-OK: going down for image 1.5\"\n# The op-mode reboot handles privileges itself (sudo would silently die\n# waiting for a password with no tty). This script runs detached from\n# the SSH session (Client.RunDetached), because the drain above cuts\n# the very BGP paths that session rides on.\n$OP reboot now\n"
	got := DrainAndRebootScript("1.5", 5, false)
	if got != want {
		t.Fatalf("no-drain script drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	// A host with no ASN must not get a BGP shutdown staged on it.
	if strings.Contains(got, "bgp") {
		t.Fatal("a host with no ASN must not have BGP touched")
	}
}

func TestPrecheckScalesTheFreeSpaceFloor(t *testing.T) {
	script := PrecheckScript(4096, "/boot")
	if !strings.Contains(script, "df --output=avail /boot") {
		t.Fatalf("check dir not honoured:\n%s", script)
	}
	// The comparison is in KiB, the message in MiB.
	if !strings.Contains(script, "-lt 4194304") || !strings.Contains(script, "less than 4096MB free in /boot") {
		t.Fatalf("free space floor wrong:\n%s", script)
	}
}

func TestInstallScriptFeedsPromptsPlainNewlines(t *testing.T) {
	script := InstallScript("https://mirror/vyos-2026.iso", "2026")
	// The NAME prompt comes first: a literal "y" would name the image "y".
	if !strings.Contains(script, "printf '\\n\\n\\n\\n\\n' | $OP add system image \"https://mirror/vyos-2026.iso\"") {
		t.Fatalf("installer prompts are not plain newlines:\n%s", script)
	}
}
