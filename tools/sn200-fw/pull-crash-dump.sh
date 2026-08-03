#!/usr/bin/env bash
# Retrieve the crash dump (and friends) from a latched HGST/WDC Ultrastar SN200.
#
# READ-ONLY. Every command this script can emit is a vendor READ (opcode 0xC6,
# NSID 0, data direction from-device). It never emits 0xFF, 0xDD, 0xD8 or 0xD9,
# and it will refuse to run if you try to make it.
#
#   *** DO NOT use `nvme wdc get-crash-dump`. ***
#   That command clears the dump after a successful read (nvme-cli
#   wdc_do_crash_dump() -> wdc_do_clear_dump(), opcode 0xFF CDW12 0x0503), and
#   clearing the crash dump schedules a Drive REINIT that ZEROES THE NAMESPACE.
#
# See docs/sn200-crash-dump-retrieval.md for the full procedure and provenance.

set -euo pipefail

DEV=""
OUTDIR=""
SECTIONS="crash"
CHUNK=65536
SINGLE_SHOT=0
OFFSET_CDW=13
RESTART=0
SKIP_PROBE=0
DRY_RUN=0
WINDOW_WARN_MS=3000

usage() {
	cat <<'EOF'
Usage: pull-crash-dump.sh [options] /dev/nvmeN

  --outdir DIR        output directory (default: ./sn200-dump-<serial>-<UTCstamp>)
  --section LIST      comma list of crash,pfail,strtbl,drvlog or "all"
                      (default: crash)
  --chunk-size N      bytes per command (default 65536, must be a multiple of 4)
  --single-shot       one command for the whole section; needs an unlimited
                      command window (vfio-pci, or the patched nvme driver).
                      Equivalent to --chunk-size 0.
  --offset-cdw N      13 (default, what nvme-cli uses) or 11 (what WD's own
                      library uses for the 0xE6 dump). Only change this if the
                      offset probe tells you to.
  --restart           discard an existing partial pull and start over
  --skip-offset-probe skip the read-only offset-semantics verification.
                      NOT RECOMMENDED -- without it a silently-ignored offset
                      field yields a file that is chunk 0 repeated forever.
  --dry-run           print the commands, execute nothing
  -h, --help          this

Exit codes: 0 ok, 1 usage/preflight, 2 offset probe failed, 3 transfer failed.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--outdir) OUTDIR="$2"; shift 2 ;;
	--section) SECTIONS="$2"; shift 2 ;;
	--chunk-size) CHUNK="$2"; shift 2 ;;
	--single-shot) SINGLE_SHOT=1; shift ;;
	--offset-cdw) OFFSET_CDW="$2"; shift 2 ;;
	--restart) RESTART=1; shift ;;
	--skip-offset-probe) SKIP_PROBE=1; shift ;;
	--dry-run) DRY_RUN=1; shift ;;
	-h | --help) usage; exit 0 ;;
	-*) echo "unknown option: $1" >&2; usage; exit 1 ;;
	*) DEV="$1"; shift ;;
	esac
done

[[ -n "$DEV" ]] || { usage; exit 1; }
[[ "$CHUNK" == "0" ]] && SINGLE_SHOT=1
[[ "$OFFSET_CDW" == "13" || "$OFFSET_CDW" == "11" ]] || {
	echo "--offset-cdw must be 13 or 11" >&2; exit 1; }
if [[ $SINGLE_SHOT -eq 0 ]] && (( CHUNK % 4 != 0 )); then
	echo "--chunk-size must be a multiple of 4" >&2; exit 1
fi
[[ "$SECTIONS" == "all" ]] && SECTIONS="crash,pfail,strtbl,drvlog"

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

# --- section table (mirrors tools/sn200-fw/sn200_vuc.py) ----------------------
# name|size_cdw12|body_cdw12|size_dword_index|tag
sect_meta() {
	case "$1" in
	crash) echo "0x0320|0x0420|0|CRSHDMP " ;;
	pfail) echo "0x0520|0x0620|0|PFCRDMP " ;;
	strtbl) echo "0x0120|0x0220|1|STRTBL  " ;;
	drvlog) echo "0x0120|0x0020|0|DRVLOG  " ;;
	*) return 1 ;;
	esac
}

now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }

# vuc_read <cdw12> <ndw> <bytes> <offset_dwords> <outfile>
# The only command shape this script will ever put on the wire.
vuc_read() {
	local c12="$1" ndw="$2" nbytes="$3" odw="$4" out="$5"
	local -a cmd=(nvme admin-passthru "$DEV"
		--opcode=0xC6 --namespace-id=0
		"--cdw10=${ndw}" "--cdw12=${c12}"
		"--data-len=${nbytes}" -r)
	if [[ "$odw" != "0" ]]; then
		cmd+=("--cdw${OFFSET_CDW}=${odw}")
	fi
	if [[ $DRY_RUN -eq 1 ]]; then
		printf '%s > %s\n' "${cmd[*]}" "$out"
		return 0
	fi
	local t0 t1
	t0=$(now_ms)
	"${cmd[@]}" >"$out" 2>"${out}.err" || {
		echo "FAILED: ${cmd[*]}" >&2
		sed 's/^/  /' "${out}.err" >&2 || true
		return 1
	}
	t1=$(now_ms)
	local dt=$((t1 - t0))
	if (( dt > WINDOW_WARN_MS )); then
		echo "  !! chunk took ${dt} ms -- close to the ~5000 ms reset window;" >&2
		echo "     consider a smaller --chunk-size" >&2
	fi
	rm -f "${out}.err"
	# nvme-cli writes the payload to stdout with -r; assert we got what we asked
	local got
	got=$(wc -c <"$out")
	if [[ "$got" != "$nbytes" ]]; then
		echo "  !! short read: asked $nbytes got $got" >&2
		return 1
	fi
	return 0
}

# le32 <file> <dword-index>
le32() {
	python3 - "$1" "$2" <<'PY'
import struct,sys
d=open(sys.argv[1],'rb').read()
i=int(sys.argv[2])
print(struct.unpack_from('<I',d,i*4)[0])
PY
}

# ---------------------------------------------------------------------------
if [[ -z "$OUTDIR" ]]; then
	SER="unknown"
	if [[ $DRY_RUN -eq 0 ]]; then
		SER=$(nvme id-ctrl "$DEV" -o json 2>/dev/null |
			python3 -c 'import json,sys;print(json.load(sys.stdin)["sn"].strip())' 2>/dev/null || echo unknown)
	fi
	OUTDIR="./sn200-dump-${SER}-$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$OUTDIR"
LOG="$OUTDIR/pull.log"
exec > >(tee -a "$LOG") 2>&1

echo "=== SN200 crash dump pull ==="
echo "device      : $DEV"
echo "outdir      : $OUTDIR"
echo "sections    : $SECTIONS"
if [[ $SINGLE_SHOT -eq 1 ]]; then
	echo "mode        : SINGLE-SHOT (needs an unlimited command window)"
else
	echo "mode        : chunked, $CHUNK bytes/command, offset in CDW${OFFSET_CDW}"
fi
echo "started     : $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo

# --- step 0: non-destructive state capture ----------------------------------
if [[ $DRY_RUN -eq 0 ]]; then
	echo "[0] capturing controller state (standard commands, read-only)"
	nvme id-ctrl "$DEV" -b >"$OUTDIR/id-ctrl.bin" 2>/dev/null || true
	if [[ -s "$OUTDIR/id-ctrl.bin" ]]; then
		# OM-6402 added "Post Crash Mode" at byte 3072 of Identify Controller
		# (KNGND110+). Non-zero means the drive is latched.
		pcm=$(od -An -tx1 -j3072 -N16 "$OUTDIR/id-ctrl.bin" | tr -s ' ')
		echo "    Post Crash Mode (id-ctrl byte 3072..3087):$pcm"
	fi
	nvme id-ctrl "$DEV" -o json >"$OUTDIR/id-ctrl.json" 2>/dev/null || true
	nvme error-log "$DEV" -o json >"$OUTDIR/error-log.json" 2>/dev/null || true
	nvme smart-log "$DEV" -o json >"$OUTDIR/smart-log.json" 2>/dev/null || true
	echo
fi

FAILED=0

for name in ${SECTIONS//,/ }; do
	meta=$(sect_meta "$name") || { echo "unknown section: $name" >&2; exit 1; }
	IFS='|' read -r SZ_CDW12 BODY_CDW12 SZ_DW TAG <<<"$meta"
	sdir="$OUTDIR/$name"
	[[ $RESTART -eq 1 ]] && rm -rf "$sdir"
	mkdir -p "$sdir/chunks"

	echo "[$name] tag='${TAG}' size-cdw12=$SZ_CDW12 body-cdw12=$BODY_CDW12"

	# --- step 1: size probe (8-byte read) ---
	if ! vuc_read "$SZ_CDW12" 2 8 0 "$sdir/size.bin"; then
		echo "[$name] size probe FAILED -- skipping section"
		FAILED=1
		continue
	fi
	if [[ $DRY_RUN -eq 1 ]]; then
		echo "[$name] (dry run: would now read the body in chunks)"
		continue
	fi
	SIZE=$(le32 "$sdir/size.bin" "$SZ_DW")
	echo "[$name] size = $SIZE bytes (0x$(printf %x "$SIZE"))"
	if [[ "$SIZE" == "0" ]]; then
		echo "[$name] section is empty -- nothing to retrieve"
		continue
	fi
	if (( SIZE % 4 != 0 )); then
		echo "[$name] size is not dword aligned; rounding down"
		SIZE=$(( SIZE & ~3 ))
	fi

	# --- step 2: offset-semantics verification (read-only, 3 commands) ---
	# Without this, a silently-ignored offset field produces a file that is
	# chunk 0 repeated N times and looks superficially plausible.
	if [[ $SINGLE_SHOT -eq 0 && $SKIP_PROBE -eq 0 ]]; then
		P=$CHUNK
		while (( 2 * P > SIZE )); do P=$(( P / 2 )); done
		if (( P < 4 )); then
			echo "[$name] section too small to probe; using single-shot"
			SINGLE_SHOT_THIS=1
		else
			echo "[$name] verifying offset semantics with a ${P}-byte probe (read-only)"
			vuc_read "$BODY_CDW12" $(( 2 * P / 4 )) $(( 2 * P )) 0 "$sdir/probe_A.bin" || { FAILED=1; continue; }
			vuc_read "$BODY_CDW12" $(( P / 4 )) "$P" 0 "$sdir/probe_B.bin" || { FAILED=1; continue; }
			vuc_read "$BODY_CDW12" $(( P / 4 )) "$P" $(( P / 4 )) "$sdir/probe_C.bin" || { FAILED=1; continue; }
			verdict=$(python3 - "$sdir/probe_A.bin" "$sdir/probe_B.bin" "$sdir/probe_C.bin" "$P" <<'PY'
import sys
A=open(sys.argv[1],'rb').read(); B=open(sys.argv[2],'rb').read()
C=open(sys.argv[3],'rb').read(); P=int(sys.argv[4])
if B != A[:P]:
    print("NONDETERMINISTIC"); sys.exit()
if C == A[P:2*P]:
    print("OFFSET_OK"); sys.exit()
if C == B:
    print("OFFSET_IGNORED"); sys.exit()
print("UNKNOWN")
PY
)
			echo "[$name] offset probe verdict: $verdict"
			case "$verdict" in
			OFFSET_OK) : ;;
			OFFSET_IGNORED)
				cat >&2 <<EOF
[$name] *** CDW${OFFSET_CDW} IS BEING IGNORED BY THE DRIVE ***
    Every chunk would return the same bytes. Chunked retrieval is impossible
    with this offset register. Options, in order of preference:
      1. Re-run with --offset-cdw $(( OFFSET_CDW == 13 ? 11 : 13 ))
      2. Get an unlimited command window (vfio-pci with no AER submitted, or
         the patched nvme driver) and re-run with --single-shot.
    Refusing to write a corrupt dump.
EOF
				FAILED=2
				continue
				;;
			NONDETERMINISTIC)
				echo "[$name] *** two reads of the same range disagreed ***" >&2
				echo "    The drive is not returning a stable image. Do not trust a chunked pull." >&2
				FAILED=2
				continue
				;;
			*)
				echo "[$name] *** offset probe inconclusive; refusing to continue ***" >&2
				FAILED=2
				continue
				;;
			esac
		fi
	fi

	# --- step 3: the pull ---
	MANIFEST="$sdir/manifest.tsv"
	[[ -f "$MANIFEST" ]] || printf 'offset\tlength\tsha256\n' >"$MANIFEST"

	if [[ $SINGLE_SHOT -eq 1 || "${SINGLE_SHOT_THIS:-0}" == "1" ]]; then
		STEP=$SIZE
	else
		STEP=$CHUNK
	fi
	SINGLE_SHOT_THIS=0

	off=0
	n=0
	while (( off < SIZE )); do
		len=$STEP
		(( off + len > SIZE )) && len=$(( SIZE - off ))
		cf=$(printf '%s/chunks/%010d.bin' "$sdir" "$off")
		if [[ -s "$cf" && "$(wc -c <"$cf")" == "$len" ]] &&
			awk -F'\t' -v o="$off" -v l="$len" -v h="$(sha256 "$cf")" \
				'$1==o && $2==l && $3==h {found=1} END{exit !found}' "$MANIFEST" 2>/dev/null; then
			printf '\r[%s] chunk %d @ 0x%x  (resumed)      ' "$name" "$n" "$off"
		else
			printf '\r[%s] chunk %d @ 0x%x len %d          ' "$name" "$n" "$off" "$len"
			if ! vuc_read "$BODY_CDW12" $(( len / 4 )) "$len" $(( off / 4 )) "$cf"; then
				echo
				echo "[$name] transfer failed at offset $off." >&2
				echo "    Re-run the same command line to resume from here." >&2
				FAILED=3
				break
			fi
			printf '%d\t%d\t%s\n' "$off" "$len" "$(sha256 "$cf")" >>"$MANIFEST"
		fi
		off=$(( off + len ))
		n=$(( n + 1 ))
	done
	echo

	if (( off >= SIZE )); then
		out="$OUTDIR/${name}.bin"
		: >"$out"
		while IFS= read -r cf; do cat "$cf" >>"$out"; done < <(find "$sdir/chunks" -maxdepth 1 -type f | sort)
		got=$(wc -c <"$out")
		echo "[$name] reassembled $got / $SIZE bytes -> $out"
		echo "[$name] sha256 $(sha256 "$out")"
		if [[ "$got" != "$SIZE" ]]; then
			echo "[$name] *** SIZE MISMATCH ***" >&2
			FAILED=3
		fi
		# distinct-chunk sanity check: catches a silently-ignored offset that
		# somehow survived the probe.
		if (( SIZE > STEP )); then
			uniq=$(cut -f3 "$MANIFEST" | tail -n +2 | sort -u | wc -l)
			total=$(( $(wc -l <"$MANIFEST") - 1 ))
			echo "[$name] chunk diversity: $uniq distinct of $total"
			if (( uniq <= 1 && total > 1 )); then
				echo "[$name] *** every chunk is identical -- the dump is almost certainly bogus ***" >&2
				FAILED=3
			fi
		fi
	fi
	echo
done

echo "finished: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
if (( FAILED )); then
	echo "COMPLETED WITH ERRORS (code $FAILED)" >&2
	exit "$FAILED"
fi
echo "OK. Next: decode-crash-dump.py $OUTDIR/crash.bin --string-table <StringTable.csv>"
