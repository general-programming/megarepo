#!/usr/bin/env bash
# Determine WHICH dump section has the SN200's Post-Crash latch armed.
#
# COMPLETELY READ-ONLY. Two vendor size probes (opcode 0xC6, NSID 0, 8-byte
# read) and one Identify. It cannot modify the drive: it never emits 0xFF,
# 0xDD, 0xD8 or 0xD9, and every command it does emit is a from-device read.
#
# WHY THIS MATTERS
# ----------------
# The boot-time latch predicate tests TWO independent conditions on one state
# byte -- one for the CRASH section, one for the PFAIL section -- and either
# one alone forces POST CRASH Startup. Clearing the crash section (0xFF/0x0503)
# schedules a Drive REINIT that ZEROES THE NAMESPACE. Clearing the pfail
# section (0xFF/0x0603) does not.
#
# So: if only PFAIL is armed, there may be a non-destructive way out. If CRASH
# is armed, there is not.
#
# HOW ARMED-NESS IS DETECTED  (PROVEN, firmware PROC8 overlay 0x30030d7b)
# ----------------------------------------------------------------------
# The size probe is gated by a predicate of the same SHAPE as the boot latch:
#
#   30030d88: ball a14,a6,0x30030ba9    ; armed  -> return the size
#   30030d90: <log "no valid crash dump available">
#             <return 0x0f860000 = SCT 7, SC 0xC3 = HDMS_DEV_NO_DATA>
#
# **The armed/empty signal is the STATUS CODE, not the returned value.**
# A section that is not armed makes the probe FAIL with SC 0xC3. Do not try to
# read armed-ness out of the size value -- 0x00320000 is a fixed 3.27 MB
# section reservation and does not count down.
#
# CAVEAT (PROVEN, and it is a real limitation): the boot latch and this probe
# read DIFFERENT storage with DIFFERENT bit numbering.
#   boot latch  PROC0 RAM byte  bit 0 = CRASH, bit 2 = PFAIL
#   size probe  word @0x82a60008 (hardware/SPI window)  bit 6 = CRASH, bit 7 = PFAIL
# No code was found propagating one into the other. So "probe says EMPTY" is
# strong evidence the latch will not fire, NOT proof. Never read a clean result
# here as a guarantee that a clear will succeed.
#
# AND NOTE: UNEXSTRT -- any start not preceded by a recorded clean shutdown --
# stamps its stub into the CRASH section (0x0b), PROVEN. So a power-event latch
# is a CRASH latch, and no safe clear exists for it.
#
# Full derivation: docs/sn200-nondestructive-recovery.md
# Dump retrieval:  docs/sn200-crash-dump-retrieval.md

set -uo pipefail

DEV="${1:-}"
if [[ -z "$DEV" || "$DEV" == "-h" || "$DEV" == "--help" ]]; then
	cat <<'EOF'
Usage: check-latch-state.sh /dev/nvmeN

Read-only. Reports which crash-dump sections are armed, and therefore whether a
non-destructive recovery is even possible on this drive.
EOF
	exit 1
fi

if [[ ! -e "$DEV" ]]; then echo "no such device: $DEV" >&2; exit 1; fi
command -v nvme >/dev/null || { echo "nvme-cli not found" >&2; exit 1; }
if [[ -c "$DEV" || -b "$DEV" ]] && [[ "$(id -u)" != "0" ]]; then
	echo "must run as root to talk to $DEV" >&2; exit 1
fi

TMP=$(mktemp -d); trap 'rm -rf "$TMP"' EXIT

# probe <name> <cdw12> <size-dword-index>
# echoes: ARMED <bytes> | EMPTY | ERROR <detail>
probe() {
	local name="$1" c12="$2" dw="$3"
	local out="$TMP/$name.bin" err="$TMP/$name.err"
	if nvme admin-passthru "$DEV" --opcode=0xC6 --namespace-id=0 \
		--cdw10=2 "--cdw12=$c12" --data-len=8 -r >"$out" 2>"$err"; then
		local v
		v=$(python3 -c "import struct,sys;print(struct.unpack_from('<I',open(sys.argv[1],'rb').read(),$dw*4)[0])" "$out")
		echo "ARMED $v"
	else
		# SC 0xC3 (status 0x7c3) is the firmware's explicit
		# "no valid dump available" -- the section is NOT armed.
		if grep -qiE '0x7c3|c3\)|NO_DATA|no data' "$err"; then
			echo "EMPTY"
		else
			echo "ERROR $(tr '\n' ' ' <"$err" | sed 's/  */ /g')"
		fi
	fi
}

echo "=== SN200 latch-state triage (read-only) ==="
echo "device : $DEV"
echo "time   : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

if nvme id-ctrl "$DEV" -b >"$TMP/id.bin" 2>/dev/null && [[ -s "$TMP/id.bin" ]]; then
	echo "Identify Controller byte 3072 ('Post Crash Mode', OM-6402, KNGND110+):"
	od -An -tx1 -j3072 -N16 "$TMP/id.bin" | sed 's/^/   /'
	echo
fi

CRASH=$(probe crash 0x0320 0)
PFAIL=$(probe pfail 0x0520 0)

printf 'CRASH section (CDW12 0x0320) : %s\n' "$CRASH"
printf 'PFAIL section (CDW12 0x0520) : %s\n' "$PFAIL"
echo

c=${CRASH%% *}; p=${PFAIL%% *}
echo "--- interpretation ---"
case "$c/$p" in
EMPTY/ARMED)
	cat <<'EOF'
  Only the PFAIL section is armed.

  This is the one case where a non-destructive clear is plausible: the safe
  clear (0xFF CDW12 0x0603) targets EEPROM section 0x0a and schedules nothing,
  whereas 0x0503 is what arms Drive REINIT.

  It is also SURPRISING and worth reporting. An unclean stop (power event,
  reset loop) stamps its stub into the CRASH section, so a power-event latch
  should show CRASH armed. Seeing PFAIL-only means this drive latched for some
  other reason.

  DO NOT ACT ON IT YET -- the sequence in docs/sn200-nondestructive-recovery.md
  is explicitly UNVERIFIED and has never been run against hardware.
  Pull the dumps first either way.
EOF
	;;
ARMED/*)
	cat <<'EOF'
  The CRASH section is armed. This is the expected result after a power event:
  UNEXSTRT stamps its stub into the CRASH section (PROVEN).

  Clearing it requires 0xFF CDW12 0x0503, and on a latched drive that always
  schedules the Drive REINIT, which REBUILDS THE L2P AND ZEROES THE NAMESPACE.
  There is NO known non-destructive path out of this state.

  Retrieve the dumps first (pull-crash-dump.sh) and copy them off the machine.
  Then decide: the clear is irreversible, but the media and the data are
  currently INTACT -- only the boot path refuses. If this drive's data is worth
  more than the drive, leaving it latched and powered down keeps every future
  option open. Running 0x0503 closes them all.
EOF
	;;
EMPTY/EMPTY)
	cat <<'EOF'
  Neither section reports as armed.

  If the drive is nonetheless latched, this is the case the caveat above warns
  about: the boot predicate is reading state the size probes do not expose.
  A likely cause is an UNEXSTRT stub, which CLEARS the valid bits (producing
  the "invalid" third state) rather than setting them -- so it can arm the
  latch while making the probe report no data.

  Do not clear anything. Capture id-ctrl, the error log and dmesg, and read
  docs/sn200-nondestructive-recovery.md section 5.
EOF
	;;
*)
	cat <<'EOF'
  At least one probe neither succeeded nor returned the "no data" status.

  Do not guess. A probe that fails with something other than SC 0xC3 may mean
  the admin gate rejected it (SC 0xC5 = diagnostic mode) or the command was
  malformed. Capture the exact status and stop.
EOF
	;;
esac
echo
echo "Nothing has been modified. Next step: pull the dumps before anything else."
echo "  sudo ./pull-crash-dump.sh --section all $DEV"
