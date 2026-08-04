#!/usr/bin/env bash
# One-command, READ-ONLY triage for an HGST/WDC Ultrastar SN200.
#
# Answers the only three questions that matter in the moment:
#   1. is this drive latched, and is its data still there?
#   2. WHICH failure is it -- unfinished shutdown, or a firmware trap?
#   3. what am I allowed to do next without destroying it?
#
# NON-DESTRUCTIVE BY CONSTRUCTION. It emits only Identify, 0xC6 vendor reads,
# and 0xFF with CDW12=0x0004 -- the read-only startup-type query. No 0xCA, no
# 0xDD, and no other 0xFF selector: notably not 0x0503 (re-init, zeroes the
# namespace), 0x0403 or 0x0303. A test asserts every one of those. It never
# writes.
#
# It also deliberately issues FEW commands. A latched drive is reset-looping,
# and sustained admin traffic against one has taken a production node down.
set -uo pipefail

DEV=""
DUMP_DIR=""
PULL_DUMP=0
SYSROOT="/sys/class/nvme" # overridable so the tests can fake a controller state

usage() {
	cat <<'EOF'
Usage: sn200-triage.sh [--dump DIR] /dev/nvmeN

  --dump DIR   also pull the first 128 KiB of the crash section into DIR.
               Read-only, but do this BEFORE any recovery -- every recovery
               destroys the evidence.
  -h, --help

With no device given, lists every SN200 found by MODEL (never by nvmeN,
which is not stable across OSes or reboots).
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--dump)
		DUMP_DIR="$2"
		PULL_DUMP=1
		shift 2
		;;
	--sysfs)
		SYSROOT="$2"
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	-*)
		echo "unknown option: $1" >&2
		usage
		exit 1
		;;
	*)
		DEV="$1"
		shift
		;;
	esac
done

have() { command -v "$1" >/dev/null 2>&1; }
have nvme || {
	echo "nvme-cli not found" >&2
	exit 1
}

# nvme-cli 2.13 dumps its entire usage text on ANY failure, which buries the
# one line that matters. Keep the status line, drop the manual.
# nvme-cli emits it ANSI-bolded, so strip escapes BEFORE matching -- a plain
# '^Usage:' anchor never fires against "\e[1mUsage:".
nq() {
	nvme "$@" 2>&1 |
		sed $'s/\033\\[[0-9;]*m//g' |
		grep -v -e '^$' |
		head -1
}

# --- find SN200s by model, never by device number ---------------------------
find_sn200() {
	local n m
	for n in "$SYSROOT"/nvme*; do
		[ -e "$n/model" ] || continue
		m=$(cat "$n/model" 2>/dev/null)
		case "$m" in *HUSMR*) echo "/dev/$(basename "$n")" ;; esac
	done
}

if [[ -z "$DEV" ]]; then
	echo "=== SN200 devices (resolved by model) ==="
	found=$(find_sn200)
	if [[ -z "$found" ]]; then
		echo "  none found"
		echo
		echo "  If you expected one: a drive ABSENT from lspci entirely is a PCIe"
		echo "  link-training failure (UEFI0067), NOT the firmware lockup. Check the"
		echo "  host console / BMC log. No vendor command will fix that."
		exit 0
	fi
	while read -r l; do echo "  $l"; done <<<"$found"
	echo
	echo "Re-run against one of them."
	exit 0
fi

# /dev/nvme7n1 -> /dev/nvme7. Strip the namespace suffix off the BASENAME only:
# ${DEV%n[0-9]*} would also chew a directory called .../foo_n0/ out of the path.
CTRL="$DEV"
[[ "$(basename "$DEV")" =~ ^(nvme[0-9]+)n[0-9]+$ ]] &&
	CTRL="$(dirname "$DEV")/${BASH_REMATCH[1]}"
SYS="$SYSROOT/$(basename "$CTRL")"

echo "=== SN200 triage: $CTRL ==="
echo "  time  : $(date -u +%FT%TZ)"
[ -r "$SYS/model" ] && echo "  model : $(cat "$SYS/model")"
[ -r "$SYS/firmware_rev" ] && echo "  fw    : $(cat "$SYS/firmware_rev")"
STATE=""
[ -r "$SYS/state" ] && STATE=$(cat "$SYS/state")
[ -n "$STATE" ] && echo "  state : $STATE"

# A controller in `resetting` rejects every admin command with EAGAIN, so
# everything below will read as "failed" for a reason that has nothing to do
# with the drive's actual contents. Say so ONCE, up front.
RESETTING=0
case "$STATE" in
resetting | connecting | deleting*)
	RESETTING=1
	echo
	echo "  !! The controller is $STATE. It is reset-looping: the kernel is"
	echo "  !! tearing it down and re-probing it continuously, and NO admin"
	echo "  !! command can be delivered (every one returns EAGAIN)."
	echo "  !! Failures below mean 'could not ask', NOT 'the answer is no'."
	echo "  !! Do NOT retry in a loop -- sustained admin traffic against a"
	echo "  !! reset-looping SN200 has wedged a production node here before."
	echo "  !! To get answers, boot with tools/nvme-noreset/ to stop the loop."
	;;
esac

if ls "${CTRL}"n1 >/dev/null 2>&1; then
	echo "  ns    : PRESENT -- this drive is NOT latched"
	NS_PRESENT=1
else
	echo "  ns    : ABSENT  -- consistent with a latch"
	NS_PRESENT=0
fi

# --- is the data still allocated? -------------------------------------------
echo
echo "--- capacity (is the data still there?) ---"
idc=$(nvme id-ctrl "$CTRL" 2>/dev/null)
tn=$(echo "$idc" | awk '/^tnvmcap/{print $3}')
un=$(echo "$idc" | awk '/^unvmcap/{print $3}')
echo "  tnvmcap = ${tn:-<unreadable>}"
echo "  unvmcap = ${un:-<unreadable>}"

# THREE states, not two. Collapsing "could not read" into "unallocated" would
# tell an operator their data is gone when nothing of the sort was established
# -- and this is the common case, because a reset-looping drive answers nothing.
if [[ -z "$tn" || -z "$un" ]]; then
	DATA_PRESENT=unknown
	echo "  => COULD NOT READ. This says NOTHING about your data. Do not treat"
	echo "     an unreadable Identify as evidence of loss."
elif [[ "$un" == "0" && "$tn" != "0" ]]; then
	DATA_PRESENT=yes
	echo "  => capacity is STILL ALLOCATED to a namespace. THE DATA IS THERE."
else
	DATA_PRESENT=no
	echo "  => capacity reads as unallocated. The namespace is gone."
fi

# --- refuse to aim SN200 vendor opcodes at a foreign drive ------------------
# Everything below is vendor-specific. The same opcode means something else, or
# nothing, on another vendor's controller -- and "nothing" is not guaranteed.
# sysfs is the check that still works when the drive answers no commands.
MODEL=""
[ -r "$SYS/model" ] && MODEL=$(cat "$SYS/model")
if [[ "$MODEL" != *HUSMR* ]]; then
	echo
	echo "  REFUSING to continue: $CTRL is not an SN200."
	echo "  model = '${MODEL:-<unreadable>}'"
	echo "  Vendor opcodes 0xFF/0xC6 are SN200-specific and are NOT safe to"
	echo "  send blind to another controller. Run with no argument to list"
	echo "  the SN200s on this host."
	exit 2
fi

# --- startup mode: the definitive latch check -------------------------------
# Every vendor encoding below is checked against the firmware itself, not
# against a list someone typed here:
#     SN200_FW=~/sn200fw .venv/bin/python tools/sn200-fw/sn200_oracle.py --ff
# tests/test_oracle.py::test_triage_script_only_sends_read_only_vendor_commands
# fails if this script ever grows a 0xFF encoding the oracle cannot show is
# read-only. Adjacency to keep in mind while editing: 0x0003 is one nibble from
# the 0x0004 probe below and erases the drive's boot-marker record.
echo
echo "--- startup mode (0xFF cdw12=0x0004, read-only) ---"
probe=$(nq admin-passthru "$CTRL" --opcode=0xff --namespace-id=0 \
	--cdw10=0 --cdw12=0x0004 --data-len=0)
echo "  ${probe:-<no reply>}"
MODE=""
# nvme-cli prints "NVMe command result:00000600" -- bare hex, no 0x, and older
# builds space it differently. Accept both.
if [[ "$probe" =~ result:[[:space:]]*(0x)?([0-9a-fA-F]+) ]]; then
	res="0x${BASH_REMATCH[2]}"
	MODE=$(((res >> 8) & 0xff))
	echo "  => startup type = $MODE $([ "$MODE" = 6 ] && echo '(INVALID / Post Crash => LATCHED)')"
fi

# --- which section is armed -------------------------------------------------
echo
echo "--- crash sections ---"
CLOG_ARMED=unknown
PFCL_ARMED=unknown
for pair in "CLOG 0x0320" "PFCL 0x0520"; do
	name=${pair%% *}
	sub=${pair##* }
	out=$(nvme admin-passthru "$CTRL" --opcode=0xc6 --namespace-id=0 \
		--cdw10=2 --cdw12="$sub" --data-len=8 -r -b 2>/dev/null | od -A n -t x4 | head -1)
	if [[ -n "$out" ]]; then
		[[ "$name" == CLOG ]] && CLOG_ARMED=yes || PFCL_ARMED=yes
		echo "  $name ($sub): $out"
	elif [[ $RESETTING -eq 1 ]]; then
		# a failed probe normally means "not armed" -- but not when the command
		# never reached the drive
		echo "  $name ($sub): <could not ask; controller is $STATE>"
	else
		[[ "$name" == CLOG ]] && CLOG_ARMED=no || PFCL_ARMED=no
		echo "  $name ($sub): <probe failed -- section not armed>"
	fi
done

# --- the header: WHICH failure mode is this? --------------------------------
echo
echo "--- failure mode (crash section header) ---"
# one read, parsed twice -- a latched drive is reset-looping, so every command
# we do not send is a command that cannot wedge it
HDRF=$(mktemp)
trap 'rm -f "$HDRF"' EXIT
nvme admin-passthru "$CTRL" --opcode=0xc6 --namespace-id=0 \
	--cdw10=32 --cdw12=0x0420 --data-len=128 -r -b >"$HDRF" 2>/dev/null
if [[ ! -s "$HDRF" ]]; then
	echo "  could not read the crash section header"
else
	# version is the LE dword at +0x00; od -t x1 numbers bytes from $1
	ver=$(od -A n -t x1 -N 4 "$HDRF" | awk '{print $4$3$2$1}')
	tag=$(dd if="$HDRF" bs=1 skip=64 count=8 2>/dev/null | tr -d '\0')
	echo "  version = 0x${ver:-????}"
	echo "  tag@+0x40 = '${tag}'"
	case "$ver" in
	00020100)
		echo "  => UNFINISHED SHUTDOWN stamped this section (UNEXSTRT stub)."
		;;
	00020200)
		echo "  => A GENUINE FIRMWARE TRAP armed this section, not a shutdown."
		echo "     This is what was observed on sea1-k8s-2: a fatal exception on"
		echo "     PROC9, the NVMe-MI/SMBus management processor, on a RUNNING"
		echo "     drive. Host-side shutdown mitigations do not address it."
		;;
	*) echo "  => unrecognised version; do not assume either failure mode" ;;
	esac
fi

# --- optional: preserve the evidence ----------------------------------------
if [[ $PULL_DUMP -eq 1 ]]; then
	mkdir -p "$DUMP_DIR"
	out="$DUMP_DIR/crash-128k.bin"
	echo
	echo "--- pulling first 128 KiB of crash section (read-only) ---"
	if nvme admin-passthru "$CTRL" --opcode=0xc6 --namespace-id=0 \
		--cdw10=32768 --cdw12=0x0420 --data-len=131072 -r -b >"$out" 2>/dev/null; then
		echo "  wrote $out ($(wc -c <"$out") bytes)"
		echo "  decode: tools/sn200-fw/decode-crash-dump.py $out --fw-dir ~/sn200fw --rev KNGND122"
	else
		echo "  retrieval failed"
	fi
	echo "  NOTE: 128 KiB is a HOST limit (IOMMU dma_opt_mapping_size, 32 pages),"
	echo "        not the drive's. It covers cores 0-3 of 16. tools/nvme-noreset/"
	echo "        raises it on a diagnostics boot."
fi

# --- the verdict ------------------------------------------------------------
echo
echo "=============================== VERDICT ================================"
if [[ "$MODE" != "6" && $NS_PRESENT -eq 1 ]]; then
	echo "  Drive is HEALTHY. Nothing to do."
	echo "  Prevention: keep DISCARD suppressed, keep firmware at KNGND122,"
	echo "  and do not place single-copy data on it."
elif [[ $RESETTING -eq 1 && "$DATA_PRESENT" == unknown ]]; then
	echo "  Drive is RESET-LOOPING. Triage could not complete -- and that is a"
	echo "  finding, not a failure: nothing here says your data is lost."
	echo
	echo "  The media is almost certainly intact. What is broken is the boot"
	echo "  path and, right now, the host's ability to talk to the controller"
	echo "  at all. You cannot triage through a reset loop."
	echo
	echo "  Next step, in order of preference:"
	echo "    1. If the data matters: STOP. Power the drive down and leave it."
	echo "       A powered-down latched drive preserves every option."
	echo "    2. To investigate: boot a diagnostics OS with tools/nvme-noreset/"
	echo "       (persist_err_noreset_ids) to break the loop, then re-run this."
	echo "    3. Do NOT run recovery commands blind. 0xFF cdw12=0x0503 zeroes"
	echo "       the namespace, and it is the only thing that has ever lost"
	echo "       data on these drives."
elif [[ "$DATA_PRESENT" == yes && "$CLOG_ARMED" == no && "$PFCL_ARMED" == yes ]]; then
	# This state should be UNREACHABLE. The boot that latches on PFCL writes
	# marker 0x80000009 and falls into the marker dispatch, which routes marker 9
	# to the UNEXSTRT stub writer -- stamping CLOG on that same boot. So a drive
	# you can talk to is always already both-armed. Seeing this means a premise
	# is wrong, and guessing would be worse than saying so.
	echo "  Drive is LATCHED and THE DATA IS STILL THERE -- but the section"
	echo "  state is one the firmware analysis says CANNOT happen:"
	echo "      CLOG not armed + PFCL armed"
	echo
	echo "  A PFCL latch stamps CLOG itself on the same boot (marker 9 ->"
	echo "  UNEXSTRT stub writer), so a reachable drive is always both-armed."
	echo "  Do NOT improvise a recovery from this. Capture it (--dump), leave"
	echo "  the drive powered down, and read docs/sn200-section-arming.md --"
	echo "  this observation would refute it."
elif [[ "$DATA_PRESENT" == yes ]]; then
	echo "  Drive is LATCHED and THE DATA IS STILL THERE."
	echo
	echo "  *** DO NOT send 0xFF cdw12=0x0503. It schedules a re-init that"
	echo "  *** blanks the L2P and zeroes the namespace. That is the ONLY"
	echo "  *** thing that has ever destroyed data on these drives."
	echo
	echo "  *** DO NOT run 'nvme wdc get-crash-dump' or dm-cli -- both fire"
	echo "  *** 0x0503 themselves on a successful read."
	echo
	echo "  If the data matters: power the drive DOWN and leave it. That"
	echo "  preserves every option. The only non-destructive route is the"
	echo "  UART/SBL procedure in docs/sn200-data-recovery.md (never yet run)."
	echo
	echo "  If the data does NOT matter: docs/sn200-runbook.md section 2."
elif [[ "$DATA_PRESENT" == no ]]; then
	echo "  Drive is LATCHED and the namespace reads as unallocated -- i.e. a"
	echo "  re-init has already run. The data is gone; the DRIVE is fine."
	echo "  Clear it and cold-cycle: docs/sn200-runbook.md section 2."
else
	echo "  Could not establish the drive's state. Assume nothing."
	echo "  Read docs/sn200-runbook.md before sending any command."
fi
echo "========================================================================"
echo
echo "Nothing was modified: only Identify, 0xC6 reads, and the read-only"
echo "0xFF/0x0004 startup query were issued."
