#!/usr/bin/env bash
# Write KNGND122 into every WRITABLE firmware slot of an HGST/WDC Ultrastar SN200
# so that no future activation can land the drive on an older, buggier revision.
#
# NON-ACTIVATING BY CONSTRUCTION. The only Firmware Commit this script can emit
# is Commit Action 0 ("downloaded image replaces the indicated slot, do not
# activate"). It never targets slot 0 (= "controller chooses") or slot 1 (=
# read-only factory fallback), never activates, and never resets the drive. The
# drive keeps running whatever it was already running, start to finish.
#
#   *** Commit Action 3 is NOT implemented on this firmware. ***
#   The handler extracts only 2 bits of CA (extui a8,a10,3,2) and rejects 3 with
#   0xC0040000 (Generic / Invalid Field). CA 4/5/6 alias onto 0/1/2, so a boot-
#   partition commit is silently reinterpreted as an ordinary slot commit.
#
#   *** Never feed this KNGND110.bin. ***
#   It is byte-identical to KNGND110+sblpatch+k.bin, writes EVERY slot including
#   the read-only one, and updates the secondary boot loader. This script refuses
#   any image containing an SBLPATCH.bin member.
#
# See docs/sn200-firmware-flashing.md for the full derivation and provenance.

set -euo pipefail

# The one image this tool exists to write. Verified locally, 2026-08-04.
WANT_SHA=b11298346020af0f3a859e5a0d849c464eed186c9a102cf8956b3f6c44db3e70
WANT_SIZE=1762048
WANT_REV=KNGND122
# Commit Action 0 -- "Image Replaced but Not Activated". The only one allowed.
COMMIT_ACTION=0
# Slot 1 is read only (FRMW bit 0); slot 0 means "controller chooses".
FIRST_WRITABLE_SLOT=2

DEV=""
IMAGE=""
XFER=4096
SLOTS=""
DRY_RUN=0
FORCE_ACTIVE=0
SKIP_SHA=0

usage() {
	cat <<'EOF'
Usage: fill-fw-slots.sh --image KNGND122.bin [options] /dev/nvmeN

  --image FILE        the firmware bundle to write (must be KNGND122.bin)
  --slots LIST        comma list of slots to fill (default: every writable
                      slot the drive reports, i.e. 2..N). Slot 1 is always
                      refused; it is read-only by design and is the factory
                      fallback this whole exercise exists to preserve.
  --xfer N            fw-download transfer size, bytes (default 4096, which is
                      what WD's own library uses)
  --rewrite-active    also rewrite the currently active slot. Off by default:
                      there is nothing to gain by rewriting the image the drive
                      is already running, and something to lose.
  --skip-sha          skip the image sha256 check. Only for a deliberately
                      different image; you are on your own.
  --dry-run           print the commands, execute nothing
  -h, --help          this

This script never activates and never resets. If a drive genuinely needs a slot
activated, do it by hand: `nvme fw-commit DEV --slot=N --action=2`, then a clean
OS shutdown and a COLD power cycle -- never `nvme reset` or `subsystem-reset`,
which are unclean stops that can re-arm the Post Crash latch.

Exit codes: 0 ok, 1 usage/preflight, 2 image rejected, 3 drive state refused,
            4 download or commit failed, 5 post-verify mismatch.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--image) IMAGE="$2"; shift 2 ;;
	--slots) SLOTS="$2"; shift 2 ;;
	--xfer) XFER="$2"; shift 2 ;;
	--rewrite-active) FORCE_ACTIVE=1; shift ;;
	--skip-sha) SKIP_SHA=1; shift ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) usage; exit 0 ;;
	-*) echo "unknown option: $1" >&2; usage; exit 1 ;;
	*) DEV="$1"; shift ;;
	esac
done

[[ -n "$DEV" ]] || { usage; exit 1; }
[[ -n "$IMAGE" ]] || { echo "--image is required" >&2; exit 1; }
[[ -f "$IMAGE" ]] || { echo "no such image: $IMAGE" >&2; exit 1; }
if (( XFER % 4096 != 0 )) || (( XFER <= 0 )); then
	echo "--xfer must be a positive multiple of 4096" >&2; exit 1
fi

if [[ $DRY_RUN -eq 0 ]]; then
	command -v nvme >/dev/null || { echo "nvme-cli not found" >&2; exit 1; }
	[[ -e "$DEV" ]] || { echo "no such device: $DEV" >&2; exit 1; }
	# Only a real device node needs root. A plain file means we are being
	# exercised against the test harness, which must not need privileges.
	if [[ -c "$DEV" || -b "$DEV" ]] && [[ "$(id -u)" != "0" ]]; then
		echo "must run as root to talk to $DEV" >&2
		exit 1
	fi
fi

# GNU coreutils on Linux, BSD on macOS (so the tests run on a dev laptop).
if command -v sha256sum >/dev/null; then
	sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null; then
	sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	sha256() { echo "nosha"; }
fi

filesize() { python3 -c 'import os,sys;print(os.path.getsize(sys.argv[1]))' "$1"; }

# jsonq <python expr over `d`> -- reads the JSON document on stdin.
# `python3 -c`, not a heredoc: a heredoc would eat the stdin we are piping in.
JSONQ_PY='
import json, re, sys
d = json.load(sys.stdin)
# nvme-cli >=2.x nests the payload under the device name and uses long field
# names ("Firmware Rev Slot 2": "3617007620172762699 (KNGND122)"); older
# builds emit flat frs1..frs7 / afi. Normalise both to frsN + afi so the rest
# of the script does not care which nvme-cli it is talking to.
if len(d) == 1 and isinstance(next(iter(d.values())), dict):
    inner = next(iter(d.values()))
    if any("Firmware Rev" in k or k.startswith("frs") for k in inner):
        d = inner
norm = {}
for k, v in d.items():
    m = re.match(r"^(?:frs(\d+)|Firmware Rev Slot (\d+))$", k)
    if m:
        n = m.group(1) or m.group(2)
        s = str(v)
        # value may be "<decimal> (KNGND122)", a bare revision, or a u64 blob
        rev = re.search(r"\(([^)]*)\)", s)
        if rev:
            s = rev.group(1)
        elif s.isdigit():
            try:
                s = int(s).to_bytes(8, "little").decode("ascii", "ignore")
            except Exception:
                pass
        norm["frs" + n] = s.strip()
    elif k == "afi" or "Active Firmware Slot" in k:
        norm["afi"] = int(v)
d = {**d, **norm}
try:
    print(eval(sys.argv[1]))
except Exception:
    print("")
'
jsonq() { python3 -c "$JSONQ_PY" "$1"; }

echo "=== SN200 firmware slot fill ==="
echo "device : $DEV"
echo "image  : $IMAGE"
echo "action : Commit Action ${COMMIT_ACTION} (replace slot, DO NOT activate)"
echo

# --- step 0: authenticate the image, host side only --------------------------
echo "[0] validating image"
sz=$(filesize "$IMAGE")
echo "    size   : $sz"
if [[ "$sz" != "$WANT_SIZE" ]] && [[ $SKIP_SHA -eq 0 ]]; then
	echo "    !! expected $WANT_SIZE bytes for $WANT_REV" >&2
	exit 2
fi
if (( sz % 4 != 0 )); then
	echo "    !! image size is not a multiple of 4; fw-download will refuse it" >&2
	exit 2
fi

got_sha=$(sha256 "$IMAGE")
echo "    sha256 : $got_sha"
if [[ $SKIP_SHA -eq 0 && "$got_sha" != "$WANT_SHA" && "$got_sha" != "nosha" ]]; then
	echo "    !! not $WANT_REV. Refusing." >&2
	echo "    !! expected $WANT_SHA" >&2
	exit 2
fi

# List the members ONCE, into a variable. Do not pipe tar into grep -q: grep
# exits on first match, SIGPIPEs tar, and `set -o pipefail` then reports a
# failure that depends on where in the archive the match happened to be.
# `tar` warns "a lone zero block" on these images -- the bundle deliberately has
# one end-of-archive block instead of two, followed by a 256-byte trailer.
members=$(tar -tf "$IMAGE" 2>/dev/null || true)

# THE SAFETY ASSERTION. KNGND110.bin is byte-identical to
# KNGND110+sblpatch+k.bin and is distinguishable ONLY by this extra tar member.
# That image writes every slot including the read-only one and updates the
# secondary boot loader -- the exact opposite of what this tool is for.
if grep -qi 'SBLPATCH' <<<"$members"; then
	echo "    !! image contains SBLPATCH.bin -- this is the +sblpatch+k variant." >&2
	echo "    !! It writes EVERY slot, destroys the read-only fallback, and" >&2
	echo "    !! updates the SBL (no supported downgrade afterwards). Refusing." >&2
	exit 2
fi
# The bundle must look like what the drive parses: a tar carrying FWHEADER.bin.
if ! grep -q 'FWHEADER[.]bin' <<<"$members"; then
	echo "    !! image is not an SN200 firmware bundle (no FWHEADER.bin)" >&2
	exit 2
fi
echo "    bundle ok: $(grep -c . <<<"$members") members, no SBLPATCH"
echo

# --- step 1: read the drive's starting state, read-only ----------------------
echo "[1] reading drive state"
IDJSON=""
FWJSON=""
NSLOTS=5
RO=1
ACTIVE=0
NEXT=0

if [[ $DRY_RUN -eq 0 ]]; then
	IDJSON=$(nvme id-ctrl "$DEV" -o json)
	FR=$(jsonq 'd["fr"].strip()' <<<"$IDJSON")
	MN=$(jsonq 'd["mn"].strip()' <<<"$IDJSON")
	SN=$(jsonq 'd["sn"].strip()' <<<"$IDJSON")
	FRMW=$(jsonq 'int(d["frmw"])' <<<"$IDJSON")
	echo "    model  : $MN"
	echo "    serial : $SN"
	echo "    fr     : $FR"
	echo "    frmw   : $FRMW"

	case "$FR" in
	KNGN*) ;;
	*)
		echo "    !! fr '$FR' is not on the generic KNGN branch." >&2
		echo "    !! KNGND122.bin will be refused as incompatible. Stopping." >&2
		exit 3
		;;
	esac

	# FRMW: bit0 = slot 1 read only, bits3:1 = slot count, bit4 = activate w/o reset.
	RO=$(( FRMW & 1 ))
	NSLOTS=$(( (FRMW >> 1) & 7 ))
	NORESET=$(( (FRMW >> 4) & 1 ))
	echo "    slots  : $NSLOTS   slot1-read-only: $RO   activate-without-reset: $NORESET"
	if (( NSLOTS < FIRST_WRITABLE_SLOT )); then
		echo "    !! drive reports $NSLOTS slot(s); nothing writable. Stopping." >&2
		exit 3
	fi

	FWJSON=$(nvme fw-log "$DEV" -o json)
	AFI=$(jsonq 'int(d["afi"])' <<<"$FWJSON")
	ACTIVE=$(( AFI & 7 ))
	NEXT=$(( (AFI >> 4) & 7 ))
	echo "    afi    : $AFI  (active slot $ACTIVE, next-reset slot $NEXT)"
	for i in $(seq 1 "$NSLOTS"); do
		v=$(jsonq "d.get('frs$i','')" <<<"$FWJSON")
		printf '    slot %d : %s\n' "$i" "${v:-<empty>}"
	done

	# AFI bits 6:4 name the slot to activate at next reset; 0 means "nothing
	# pending". Some drives instead leave it equal to the ACTIVE slot after an
	# activation has already completed -- that is not a pending change, it is
	# the drive restating the status quo, and refusing on it is a false
	# positive. Only a next-reset slot that differs from the active one is a
	# genuine pending activation.
	if (( NEXT != 0 && NEXT != ACTIVE )); then
		echo "    !! an activation to slot $NEXT is pending (active is $ACTIVE)." >&2
		echo "    !! Resolve that before rewriting slots. Stopping." >&2
		exit 3
	fi
fi
echo

# --- step 2: decide the slot list --------------------------------------------
if [[ -n "$SLOTS" ]]; then
	IFS=',' read -r -a want <<<"$SLOTS"
else
	want=()
	for i in $(seq "$FIRST_WRITABLE_SLOT" "$NSLOTS"); do want+=("$i"); done
fi

targets=()
for s in "${want[@]}"; do
	s="${s// /}"
	[[ "$s" =~ ^[0-9]+$ ]] || { echo "bad slot '$s'" >&2; exit 1; }
	# Hard refusals, not warnings. Slot 0 means "controller chooses", which the
	# firmware's range check (FS <= slot_count) happily accepts.
	if (( s < FIRST_WRITABLE_SLOT )); then
		echo "!! slot $s is not writable (slot 0 = controller-chooses, slot 1 = read-only). Refusing." >&2
		exit 1
	fi
	if (( s > NSLOTS )); then
		echo "!! slot $s is beyond the drive's $NSLOTS slots. Refusing." >&2
		exit 1
	fi
	if (( s == ACTIVE )) && [[ $FORCE_ACTIVE -eq 0 ]]; then
		echo "    skipping slot $s: it is the ACTIVE slot (--rewrite-active to override)"
		continue
	fi
	targets+=("$s")
done

if [[ ${#targets[@]} -eq 0 ]]; then
	echo "nothing to do"
	exit 0
fi
echo "[2] slots to fill: ${targets[*]}"
echo

# --- step 3: download + commit, one slot at a time ---------------------------
# The image is re-downloaded before every commit. Neither the NVMe spec nor this
# firmware guarantees the download buffer survives a Firmware Commit, and 1.7 MB
# is cheap next to finding out that it did not.
for s in "${targets[@]}"; do
	echo "--- slot $s ---"
	dl=(nvme fw-download "$DEV" "--fw=$IMAGE" "--xfer=$XFER")
	ci=(nvme fw-commit "$DEV" "--slot=$s" "--action=$COMMIT_ACTION")

	if [[ $DRY_RUN -eq 1 ]]; then
		printf '%s\n' "${dl[*]}"
		printf '%s\n' "${ci[*]}"
		continue
	fi

	echo "    downloading $(filesize "$IMAGE") bytes in ${XFER}-byte chunks"
	"${dl[@]}" || { echo "    !! fw-download failed" >&2; exit 4; }

	echo "    committing to slot $s with action $COMMIT_ACTION (no activation)"
	"${ci[@]}" || { echo "    !! fw-commit failed for slot $s" >&2; exit 4; }

	# Verify between every step, not just at the end.
	after=$(nvme fw-log "$DEV" -o json)
	got=$(jsonq "d.get('frs$s','').strip()" <<<"$after")
	afi2=$(jsonq 'int(d["afi"])' <<<"$after")
	echo "    slot $s now: ${got:-<empty>}   afi: $afi2"
	if [[ "$got" != "$WANT_REV" ]]; then
		echo "    !! slot $s reads '$got', expected $WANT_REV" >&2
		exit 5
	fi
	# CA=0 must not change the active slot, nor introduce a pending activation
	# to a DIFFERENT slot. A next-reset field equal to the active slot is the
	# drive restating the status quo (see the note at the pre-flight check) and
	# is not evidence that a different commit action was applied.
	next2=$(( (afi2 >> 4) & 7 ))
	if (( (afi2 & 7) != ACTIVE )) || (( next2 != 0 && next2 != ACTIVE )); then
		echo "    !! afi moved (active $ACTIVE -> $((afi2 & 7)), next-reset $next2)." >&2
		echo "    !! A commit action other than 0 was applied." >&2
		echo "    !! STOP and investigate before touching another drive." >&2
		exit 5
	fi
done
echo

# --- step 4: final state -----------------------------------------------------
echo "[4] final firmware log"
if [[ $DRY_RUN -eq 0 ]]; then
	final=$(nvme fw-log "$DEV" -o json)
	for i in $(seq 1 "$NSLOTS"); do
		v=$(jsonq "d.get('frs$i','')" <<<"$final")
		mark=""
		(( i == 1 )) && mark="  (read-only, factory fallback -- untouched)"
		(( i == ACTIVE )) && mark="  (active)"
		printf '    slot %d : %-10s%s\n' "$i" "${v:-<empty>}" "$mark"
	done
	echo
	echo "No reset was performed and none is needed: Commit Action 0 does not"
	echo "activate. The drive is still running the image it started with."
else
	printf '%s\n' "nvme fw-log $DEV -o json"
fi
