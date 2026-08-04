#!/bin/sh
# SPDX-License-Identifier: GPL-2.0
# Refuse to build src/ against a kernel it was not vendored for.
set -eu

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
KVER=${1:-$(uname -r)}
PROV="$HERE/src/VENDORED-FROM"

[ -r "$PROV" ] || { echo "src/VENDORED-FROM missing" >&2; exit 1; }
# shellcheck disable=SC1090
. "$PROV"

if [ "$VENDORED_KERNEL_RELEASE" = "$KVER" ]; then
	echo "src/ was vendored from exactly $KVER ($VENDORED_SOURCE)"
	exit 0
fi

# Same upstream base (VERSION.PATCHLEVEL.SUBLEVEL) is worth attempting;
# anything else will not compile.
base_of() { echo "$1" | sed 's/^\([0-9]*\.[0-9]*\)\..*/\1/'; }
if [ "$(base_of "$VENDORED_KERNEL_RELEASE")" = "$(base_of "$KVER")" ]; then
	echo "WARNING: src/ was vendored from $VENDORED_KERNEL_RELEASE, building for $KVER." >&2
	echo "         Same $(base_of "$KVER") base, so it may compile -- check-crc.sh decides." >&2
	exit 0
fi

cat >&2 <<EOF
REFUSING: src/ was vendored from $VENDORED_KERNEL_RELEASE but you asked for $KVER.
          These are different kernel series; the copied drivers/nvme/host will
          not build. Re-vendor first:  ./fetch-sources.sh --pve $KVER
EOF
exit 1
