#!/bin/bash
# SPDX-License-Identifier: GPL-2.0
#
# Re-vendor src/ from a different kernel's drivers/nvme/host, then re-apply
# the nvme-noreset patch. Needs network, git, curl, tar+xz.
#
#   ./fetch-sources.sh --pve 7.0.2-6-pve      # Proxmox VE kernel
#   ./fetch-sources.sh --upstream 6.12.34     # kernel.org stable
set -euo pipefail

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
MODE=""; REL=""
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT

# The subset of drivers/nvme/host needed to build nvme-core.ko.
FILES="core.c ioctl.c sysfs.c pr.c constants.c trace.c trace.h multipath.c
       zns.c fault_inject.c hwmon.c auth.c nvme.h fabrics.h"

case "${1:-}" in
--pve)      MODE=pve;      REL=${2:?kernel release, e.g. 7.0.2-6-pve} ;;
--upstream) MODE=upstream; REL=${2:?kernel version, e.g. 6.12.34} ;;
*) echo "usage: $0 --pve <release> | --upstream <version>" >&2; exit 2 ;;
esac

if [ "$MODE" = upstream ]; then
	maj=${REL%%.*}
	url="https://cdn.kernel.org/pub/linux/kernel/v${maj}.x/linux-${REL}.tar.xz"
	echo "== downloading $url"
	curl -fL "$url" -o "$WORK/l.tar.xz"
	tar -C "$WORK" -xf "$WORK/l.tar.xz" "linux-${REL}/drivers/nvme/host"
	SRC="$WORK/linux-${REL}/drivers/nvme/host"
	PROVENANCE="kernel.org linux-${REL}"
else
	# Proxmox carries no nvme patches of its own; the nvme source is whatever
	# Ubuntu base the pve-kernel submodule points at for that release.
	echo "== resolving $REL via git.proxmox.com/pve-kernel.git"
	git clone -q --bare https://git.proxmox.com/git/pve-kernel.git "$WORK/pve"
	commit=$(git -C "$WORK/pve" log --all --format=%H \
		--grep="update ABI file for $REL" | head -1)
	[ -n "$commit" ] || { echo "no pve-kernel commit for $REL" >&2; exit 1; }
	sub=$(git -C "$WORK/pve" ls-tree "$commit" submodules/ubuntu-kernel | awk '{print $3}')
	[ -n "$sub" ] || { echo "no ubuntu-kernel submodule at $commit" >&2; exit 1; }
	tag=$(git ls-remote --tags https://git.proxmox.com/git/mirror_ubuntu-kernels.git \
		| awk -v s="$sub" '$1==s {print $2}' | sed 's#refs/tags/##; s#\^{}##' | head -1)
	[ -n "$tag" ] || { echo "submodule $sub matches no Ubuntu tag" >&2; exit 1; }
	echo "== $REL -> ubuntu-kernel $tag ($sub)"
	git clone -q --depth 1 --branch "$tag" --no-checkout \
		https://git.proxmox.com/git/mirror_ubuntu-kernels.git "$WORK/u"
	git -C "$WORK/u" sparse-checkout init --cone
	git -C "$WORK/u" sparse-checkout set drivers/nvme/host
	git -C "$WORK/u" checkout -q
	SRC="$WORK/u/drivers/nvme/host"
	PROVENANCE="git.proxmox.com mirror_ubuntu-kernels $tag ($sub), via pve-kernel $commit"
fi

echo "== vendoring into src/"
for f in $FILES; do
	[ -r "$SRC/$f" ] || { echo "missing $f in $SRC" >&2; exit 1; }
	cp "$SRC/$f" "$HERE/src/$f"
done

cat > "$HERE/src/VENDORED-FROM" <<EOF
VENDORED_KERNEL_RELEASE="$REL"
VENDORED_SOURCE="$PROVENANCE"
VENDORED_DATE="$(date -u +%Y-%m-%d)"
EOF

echo "== re-applying patches/nvme-noreset.patch"
( cd "$HERE/src" && patch -p1 --no-backup-if-mismatch < "$HERE/patches/nvme-noreset.patch" )

echo "done. Now: make KDIR=/lib/modules/$REL/build && ./check-crc.sh $REL"
