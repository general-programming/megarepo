#!/bin/sh
# SPDX-License-Identifier: GPL-2.0
#
# Verify the rebuilt nvme-core.ko exports byte-identical symbol CRCs to the
# stock one. If it does not, stock nvme.ko (and nvme-fabrics/tcp/rdma/fc)
# will REFUSE to load against it -- which on a host that boots from NVMe or
# runs NVMe-backed ceph OSDs means total storage loss on the next boot.
#
# Usage: ./check-crc.sh [kernel-version] [path/to/built/Module.symvers]
set -eu

KVER=${1:-$(uname -r)}
OURS=${2:-$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)/src/Module.symvers}

for c in "/usr/src/linux-headers-$KVER/Module.symvers" \
	 "/lib/modules/$KVER/build/Module.symvers"; do
	[ -r "$c" ] && { STOCK=$c; break; }
done
: "${STOCK:?cannot find a stock Module.symvers for $KVER (install the matching kernel headers)}"
[ -r "$OURS" ] || { echo "no built Module.symvers at $OURS -- run make first" >&2; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

awk -F'\t' '$3 ~ /(^|\/)nvme-core$/ { print $2, $1 }' "$STOCK" | sort > "$tmp/stock"
awk -F'\t' '{ print $2, $1 }' "$OURS" | sort > "$tmp/ours"

if [ ! -s "$tmp/stock" ]; then
	echo "FAIL: $STOCK lists no nvme-core exports (is nvme-core built in, =y?)" >&2
	exit 1
fi

rc=0
join -v1 "$tmp/stock" "$tmp/ours" | while read -r s _; do
	echo "MISSING from our build: $s"
done > "$tmp/missing"
join "$tmp/stock" "$tmp/ours" -o 1.1,1.2,2.2 | awk '$2 != $3 { print "CRC MISMATCH: " $1 " stock=" $2 " ours=" $3 }' > "$tmp/diff"

if [ -s "$tmp/missing" ] || [ -s "$tmp/diff" ]; then
	cat "$tmp/missing" "$tmp/diff"
	echo
	echo "FAIL: rebuilt nvme-core is NOT ABI-compatible with the stock one."
	echo "      Stock nvme.ko will not load against it. Do NOT install."
	echo "      Cause is almost always that src/ was vendored from a different"
	echo "      kernel source than $KVER. Re-run ./fetch-sources.sh."
	rc=1
else
	echo "OK: $(wc -l < "$tmp/stock") nvme-core exports, all CRCs identical to stock $KVER."
	echo "    Stock nvme.ko / nvme-fabrics / nvme-tcp will load unchanged."
fi
exit $rc
