#!/bin/bash
# SPDX-License-Identifier: GPL-2.0
#
# Install the patched nvme-core.ko through DKMS.
#
# THIS REPLACES THE nvme-core MODULE FOR EVERY NVMe DEVICE ON THE HOST.
# Read README.md before running it on anything you care about.
set -euo pipefail

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
PKG=nvme-noreset
VER=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"/\1/p' "$HERE/dkms.conf")
KVER=${KVER:-$(uname -r)}
FORCE=0

usage() {
	cat <<EOF
usage: $0 [--kver <kernel-version>] --yes-replace-nvme-core

  --kver X          build for kernel X (default: running kernel, $KVER)
  --yes-replace-nvme-core
                    required acknowledgement that this replaces the nvme-core
                    module used by EVERY NVMe device on this host.

Environment: KVER can be set instead of --kver.
EOF
	exit 2
}

while [ $# -gt 0 ]; do
	case "$1" in
	--kver) KVER=$2; shift 2 ;;
	--yes-replace-nvme-core) FORCE=1; shift ;;
	-h|--help) usage ;;
	*) echo "unknown argument: $1" >&2; usage ;;
	esac
done

[ "$(id -u)" -eq 0 ] || { echo "must run as root" >&2; exit 1; }

if [ "$FORCE" -ne 1 ]; then
	echo "REFUSING: this replaces nvme-core for every NVMe device on the host."
	echo "Re-run with --yes-replace-nvme-core once you have read README.md."
	exit 1
fi

command -v dkms >/dev/null || { echo "dkms is not installed" >&2; exit 1; }
[ -d "/lib/modules/$KVER/build" ] || {
	echo "no kernel build tree for $KVER -- install the matching headers" >&2; exit 1; }

echo "== vendored source check"
"$HERE/vendored-version.sh" "$KVER" || exit 1

echo "== staging /usr/src/$PKG-$VER"
rm -rf "/usr/src/$PKG-$VER"
mkdir -p "/usr/src/$PKG-$VER"
cp -a "$HERE"/. "/usr/src/$PKG-$VER/"
rm -rf "/usr/src/$PKG-$VER/.git"

echo "== dkms build"
dkms remove -m "$PKG" -v "$VER" -k "$KVER" >/dev/null 2>&1 || true
dkms add -m "$PKG" -v "$VER"
dkms build -m "$PKG" -v "$VER" -k "$KVER"

echo "== ABI check against stock $KVER"
SYMV="/var/lib/dkms/$PKG/$VER/build/src/Module.symvers"
if ! "$HERE/check-crc.sh" "$KVER" "$SYMV"; then
	echo
	echo "ABORTED before install. Nothing on this system has been changed"
	echo "apart from the DKMS build tree; run:  dkms remove -m $PKG -v $VER --all"
	exit 1
fi

echo "== dkms install (stock nvme-core.ko is saved by dkms as original_module)"
dkms install -m "$PKG" -v "$VER" -k "$KVER" --force

cat <<EOF

Installed. NOTHING CHANGES until you both reload the module and set a
parameter -- with no parameters the patched driver behaves exactly like stock.

Arm it for the SN200 (adjust the id / BDF to your device):

  # persist across boots + initramfs
  echo 'options nvme_core persist_err_noreset_ids=1c58:0023 zero_discard_ids=1c58:0023' \\
      > /etc/modprobe.d/nvme-noreset.conf
  update-initramfs -u -k $KVER
  # then reboot, or (if no NVMe is in use) rmmod nvme nvme_core && modprobe nvme

Verify:  dmesg | grep nvme-noreset
Rollback: see README.md
EOF
