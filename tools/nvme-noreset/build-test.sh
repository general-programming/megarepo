#!/bin/sh
# SPDX-License-Identifier: GPL-2.0
#
# Reproducible compile + ABI check in a throwaway amd64 Debian 13 container.
# Useful from a non-Linux workstation. Requires docker.
#
#   ./build-test.sh [kernel-release]        default 7.0.2-6-pve
set -eu

HERE=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
KVER=${1:-7.0.2-6-pve}
REPO=${PVE_REPO:-http://download.proxmox.com/debian/pve/dists/trixie/pve-test/binary-amd64}
DEB="proxmox-headers-${KVER%-pve}-pve_${KVER%-pve}_amd64.deb"

docker run --rm --platform linux/amd64 -v "$HERE":/src:ro debian:trixie bash -euc "
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq build-essential bc bison flex libelf-dev libssl-dev \
        kmod libdw-dev dwarves curl ca-certificates >/dev/null
curl -sfLO $REPO/$DEB
dpkg -i $DEB >/dev/null
cp -a /src /build && cd /build
make KDIR=/usr/src/linux-headers-$KVER modules
./check-crc.sh $KVER /build/src/Module.symvers
modinfo /build/src/nvme-core.ko | grep -E '^(vermagic|depends|parm:( *(persist_err_noreset_ids|zero_discard_ids|max_admin_xfer_ids)))'
"
