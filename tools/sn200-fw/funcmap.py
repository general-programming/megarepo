"""Build a verified function-boundary map for the SN200 KNGND122 images.

Method (see docs/sn200-function-map.md for the full writeup):

  1. Byte-scan every real segment (from the .SEG headers, NOT the zero-padded
     flat/*.bin merge -- the merge pads huge address gaps with zeros that are
     not part of any segment and would otherwise be miscounted as "scanned").
     `entry` is the only Xtensa opcode whose byte 0 is 0x36 (op0=6, n=3, m=0
     are all determined by that single byte), so every 0x36 byte is a
     candidate function entry.
  2. Each candidate is pre-filtered by `is_plausible_entry`: real `entry`
     instructions only ever legitimately target a1 with a modest frame size
     (see that function's docstring for the measured distribution). This
     alone rejects most coincidental 0x36 bytes before a walk is attempted.
  3. Each surviving candidate is *validated* by a bounded forward walk from
     that byte, stopping at the FIRST terminator (ret/retw/ret.n/retw.n/jx)
     reached without a hard desync signature (`is_hard_unknown`: a reserved
     op0 class or a decode exception -- NOT xdis.py's ordinary "?"-bearing
     FLIX decode gaps, which don't affect byte alignment). This is exactly
     the check the two known incidents (bnall-that-never-existed, marker 8
     "dead code") skipped -- both came from disassembling starting at an
     address nobody had verified was an instruction boundary.
  4. Overlap resolution: candidates are processed in ascending address
     order and one whose validated range starts before the previous
     accepted function's end is dropped as a misaligned re-sync artifact --
     real Xtensa function bodies never overlap.
  5. call8/call4/call12 targets (always statically resolvable) and callx*
     preceded by an l32r into the same register (a common compiler idiom)
     are collected during the *confirmed* walks only, and cross-checked
     against the confirmed-entry set. Targets that don't land on an entry
     are reported per image, never silently dropped.

python3 funcmap.py                    # scan and write function-map.json
python3 funcmap.py --self-test         # scan and check the known-good anchors
"""

import glob
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from segparse import parse
from xdis import dis

FWROOT = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
REV = os.environ.get("SN200_REV", "KNGND122")
OUT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "function-map.json")

TERMINATORS = {"ret", "retw", "ret.n", "retw.n"}
CALL_RE = re.compile(r"^call(\d+) 0x([0-9a-f]+)$")
CALLX_RE = re.compile(r"^callx(\d+) a(\d+)$")
L32R_RE = re.compile(r"^l32r a(\d+),0x([0-9a-f]+)$")
ENTRY_RE = re.compile(r"^entry a(\d+),0x[0-9a-f]+$")

MAX_WALK = 8192  # bytes: reject a candidate that doesn't reach a
# terminator within this many bytes of forward walk


def is_terminator(txt: str) -> bool:
    return txt in TERMINATORS or txt.startswith("jx ")


def is_unknown(txt: str) -> bool:
    return "?" in txt or txt.startswith("ERR")


def is_hard_unknown(txt: str) -> bool:
    """A decode result that is actually suspicious of desync, as opposed to
    merely landing on one of xdis.py's known-incomplete FLIX slot B/C (or
    narrow) sub-forms. Every op0 class fixes its instruction's length by
    construction (narrow=2, FLIX=8, else=3) regardless of whether the finer
    sub-opcode is recognized, so a "?B"/"?C"/"?narrow"/etc. does not imply
    lost byte alignment -- measured directly: PROC8's overlay bank runs
    legitimate, correctly-terminating functions that are 20-50% "?B"/"?C"
    over long stretches, because xdis.py doesn't yet decode every FLIX
    slot-B ALU sub-op. `op0=%x ???` is different: op0 3 and 4 are reserved
    encodings barely used by any real compiler, so hitting one is a real
    warning sign, as is a hard decode exception."""
    return txt.startswith("op0=") or txt.startswith("ERR")


MAX_FRAME = 0x400  # bytes; see docs/sn200-function-map.md "entry sanity filter"


def is_plausible_entry(txt: str) -> bool:
    """`entry as, imm` only ever legitimately targets a1 (the stack
    pointer) with a modest frame size in this firmware -- measured over
    every 0x36 byte in all 18 images, entry a1 accounts for ~70% of hits
    and its frame-size distribution is tight (median 0x20, 99th pct 0x90)
    with a long tail of >0x400 outliers that are all coincidental 0x36
    bytes landing inside unrelated code, not real prologues. Candidates
    using any other register, or an implausible frame, are rejected before
    ever attempting a walk."""
    m = ENTRY_RE.match(txt)
    if not m:
        return False
    if m.group(1) != "1":
        return False
    imm = int(txt.rsplit("0x", 1)[1], 16)
    return imm <= MAX_FRAME


def load_segments() -> dict[str, list[tuple[int, bytes]]]:
    """proc name -> list of (load_addr, group_key) grouped like unpack.py,
    but returns the REAL (unpadded) segment bytes per group, keyed by the
    same "PROC8_30000000" style name flat/*.bin uses."""
    images: dict[str, list[tuple[int, bytes]]] = {}
    files = sorted(glob.glob(os.path.join(FWROOT, "fw", REV, "PROC*.bin"))) + [
        os.path.join(FWROOT, "fw", REV, "FCC.bin")
    ]
    for p in files:
        if not os.path.exists(p):
            continue
        name = os.path.basename(p)[:-4]
        d = open(p, "rb").read()
        segs = [s for s in parse(d)[0] if s[2] > 0]
        groups: dict[int, list[tuple[int, bytes]]] = {}
        for _o, _do, _dl, la, data in segs:
            groups.setdefault(la >> 28, []).append((la, data))
        for items in groups.values():
            lo = min(a for a, _ in items)
            key = f"{name}_{lo:08x}"
            images[key] = sorted(items, key=lambda x: x[0])
    return images


def scan_image(name: str, segs: list[tuple[int, bytes]]) -> dict:
    """segs: list of (load_addr, bytes) for this image's real segments."""
    seg_ranges = [(a, a + len(b), a, b) for a, b in segs]

    def in_segment(addr: int):
        for lo, hi, base, data in seg_ranges:
            if lo <= addr < hi:
                return base, data, hi
        return None

    candidates = []
    implausible = []
    for lo, hi, base, data in seg_ranges:
        for o, byte in enumerate(data):
            if byte != 0x36:
                continue
            addr = lo + o
            try:
                _ln, txt = dis(data, addr, base)
            except Exception:
                continue
            if is_plausible_entry(txt):
                candidates.append(addr)
            else:
                implausible.append(addr)
    candidates.sort()

    validated_end = {}  # addr -> Phase A validated end (LAST terminator reached)
    rejected = list(implausible)

    for c in candidates:
        info = in_segment(c)
        if info is None:
            continue
        base, data, hi = info
        pc = c
        n_unknown_total = 0
        n_instr = 0
        last_term_end = None
        while pc < hi and (pc - c) < MAX_WALK:
            try:
                ln, txt = dis(data, pc, base)
            except Exception:
                break
            if is_hard_unknown(txt):
                # A reserved/invalid op0 class, or a decode exception: a
                # genuine desync signal (see is_hard_unknown), reject. Plain
                # "?B"/"?C"/"?narrow"-style soft unknowns (an xdis.py decode
                # gap, not a desync -- see is_hard_unknown) do not bail the
                # walk; they are tallied on the function for reporting only.
                break
            if is_unknown(txt):
                n_unknown_total += 1
            if n_instr > 0 and is_plausible_entry(txt):
                # A second plausible `entry` before any terminator: not
                # valid straight-line windowed-ABI code, reject.
                break
            if is_terminator(txt):
                # Stop at the FIRST terminator. We deliberately do not try
                # to walk past ret/retw/jx looking for a "real" later exit:
                # once control has definitely left the function (a plain
                # ret/retw) or gone indirect (jx, e.g. a compiled switch's
                # dispatch through a jump table -- whose target *data*,
                # not code, typically follows in memory), continuing to
                # decode the following bytes as this function's own
                # straight-line code is unjustified without a real control-
                # flow graph. That was tried and it silently swallowed a
                # neighboring real function (0x7ffa7a68) into a dispatcher's
                # "extent" by wandering through non-code bytes after its jx.
                # Stopping early undercounts multi-exit functions' extents
                # a little; it never claims bytes that are not this
                # function's, which is the property that matters here.
                last_term_end = pc + ln
                break
            pc += ln
            n_instr += 1
        if last_term_end is not None:
            validated_end[c] = last_term_end
        else:
            rejected.append(c)

    # Overlap resolution: real Xtensa function bodies never overlap. Two
    # candidates whose Phase-A-validated [entry, end) ranges overlap cannot
    # both be genuine -- greedily keep the lower (earlier) address, which is
    # the one that was reached first by a real control-flow path in the
    # byte stream, and drop the other as a misaligned re-sync artifact.
    confirmed = {}
    covered_until = 0
    for c in sorted(validated_end):
        if c < covered_until:
            rejected.append(c)
            continue
        confirmed[c] = None
        covered_until = validated_end[c]

    entries = sorted(confirmed)
    functions = {}
    calls_out: dict[
        int, list[tuple[int, int]]
    ] = {}  # caller image-local -> [(addr,target)]
    call_targets_without_entry = []

    for i, e in enumerate(entries):
        next_e = entries[i + 1] if i + 1 < len(entries) else None
        info = in_segment(e)
        base, data, hi = info
        limit = min(next_e, hi) if next_e else hi
        pc = e
        last_term_end = None
        calls = []
        reg_l32r = {}  # register -> resolved value, cleared conservatively
        gap_reason = None
        while pc < limit:
            try:
                ln, txt = dis(data, pc, base)
            except Exception:
                gap_reason = "decode exception"
                break
            m = CALL_RE.match(txt)
            if m:
                tgt = int(m.group(2), 16)
                calls.append((pc, tgt))
            m = CALLX_RE.match(txt)
            if m:
                reg = int(m.group(2))
                if reg in reg_l32r:
                    calls.append((pc, reg_l32r[reg]))
            m = L32R_RE.match(txt)
            if m:
                reg_l32r[int(m.group(1))] = int(m.group(2), 16)
            elif " a" in txt:
                # crude but safe: any other instr naming a register as a
                # first operand may clobber it; drop our record for that
                # register rather than risk a stale resolution.
                mm = re.match(r"^\S+ a(\d+)", txt)
                if mm:
                    reg_l32r.pop(int(mm.group(1)), None)
            if is_terminator(txt):
                last_term_end = pc + ln
            pc += ln
        end = last_term_end if last_term_end else e
        if last_term_end is None:
            gap_reason = gap_reason or "no terminator found before next entry"
        functions[e] = {
            "entry": e,
            "end": end,
            "size": end - e,
            "confirmed_entry": True,
            "confirmed_call": False,
            "callers": [],
            "calls": [{"at": a, "target": t} for a, t in calls],
            "gap_to_next": (next_e - end) if (next_e and end < next_e) else 0,
            "gap_reason": gap_reason,
        }
        calls_out[e] = calls

    entry_set = set(entries)
    for e, calls in calls_out.items():
        for at, tgt in calls:
            if tgt in entry_set:
                functions[tgt]["confirmed_call"] = True
                functions[tgt]["callers"].append(at)
            else:
                call_targets_without_entry.append(
                    {"caller_func": e, "at": at, "target": tgt}
                )

    total_seg_bytes = sum(len(b) for _, b in segs)
    func_bytes = sum(f["size"] for f in functions.values())
    # Bytes not attributed to any identified function. Reported as coverage
    # rather than used directly -- see the per-image stats below.
    _gap_bytes = total_seg_bytes - func_bytes

    # per-segment leading/trailing gaps (bytes never claimed by any function)
    leading_trailing_gap = 0
    for lo, hi, base, data in seg_ranges:
        in_seg_entries = sorted(a for a in entries if lo <= a < hi)
        if not in_seg_entries:
            leading_trailing_gap += hi - lo
            continue
        leading_trailing_gap += in_seg_entries[0] - lo
        last_fn = functions[in_seg_entries[-1]]
        leading_trailing_gap += hi - last_fn["end"]

    return {
        "name": name,
        "segments": [{"addr": a, "len": len(b)} for a, b in segs],
        "total_bytes": total_seg_bytes,
        "functions": functions,
        "rejected_candidates": rejected,
        "call_targets_without_entry": call_targets_without_entry,
        "func_bytes": func_bytes,
        "gap_bytes": func_bytes
        and (total_seg_bytes - func_bytes)
        or (total_seg_bytes - func_bytes),
        "leading_trailing_gap": leading_trailing_gap,
    }


def build() -> dict:
    images = load_segments()
    out_images = []
    for name in sorted(images):
        segs = images[name]
        out_images.append(scan_image(name, segs))
    return {"revision": REV, "images": out_images}


def to_jsonable(result: dict) -> dict:
    images = []
    for img in result["images"]:
        funcs = []
        for e in sorted(img["functions"]):
            f = img["functions"][e]
            funcs.append(
                {
                    "entry": "0x%08x" % f["entry"],
                    "end": "0x%08x" % f["end"],
                    "size": f["size"],
                    "confirmed_entry": f["confirmed_entry"],
                    "confirmed_call": f["confirmed_call"],
                    "callers": ["0x%08x" % a for a in sorted(f["callers"])],
                    "calls": [
                        {"at": "0x%08x" % c["at"], "target": "0x%08x" % c["target"]}
                        for c in f["calls"]
                    ],
                    "gap_to_next": f["gap_to_next"],
                    "gap_reason": f["gap_reason"],
                }
            )
        images.append(
            {
                "name": img["name"],
                "segments": [
                    {"addr": "0x%08x" % s["addr"], "len": s["len"]}
                    for s in img["segments"]
                ],
                "total_bytes": img["total_bytes"],
                "func_bytes": img["func_bytes"],
                "leading_trailing_gap": img["leading_trailing_gap"],
                "num_functions": len(funcs),
                "num_rejected_candidates": len(img["rejected_candidates"]),
                "rejected_candidates": [
                    "0x%08x" % a for a in sorted(img["rejected_candidates"])
                ],
                "call_targets_without_entry": [
                    {
                        "caller_func": "0x%08x" % c["caller_func"],
                        "at": "0x%08x" % c["at"],
                        "target": "0x%08x" % c["target"],
                    }
                    for c in img["call_targets_without_entry"]
                ],
                "functions": funcs,
            }
        )
    return {"revision": result["revision"], "images": images}


ANCHORS = [
    0x3003353C,
    0x7FFAAE35,
    0x7FFAAE3D,
    0x7FFAAF08,
    0x7FFA6B30,
    0x7FFABBF0,
    0x7FFA7A68,
    0x7FFBA9DC,
    0x7FFBBA61,
]


def self_test(result: dict) -> None:
    all_entries = set()
    for img in result["images"]:
        all_entries |= set(img["functions"])
    for a in ANCHORS:
        status = "ENTRY" if a in all_entries else "NOT AN ENTRY"
        print("anchor 0x%08x: %s" % (a, status))


if __name__ == "__main__":
    result = build()
    if "--self-test" in sys.argv:
        self_test(result)
    js = to_jsonable(result)
    with open(OUT, "w") as f:
        json.dump(js, f, indent=1)
    total_funcs = sum(i["num_functions"] for i in js["images"])
    total_bytes = sum(i["total_bytes"] for i in js["images"])
    func_bytes = sum(i["func_bytes"] for i in js["images"])
    print(
        "wrote %s: %d images, %d functions, coverage %d/%d bytes (%.1f%%)"
        % (
            OUT,
            len(js["images"]),
            total_funcs,
            func_bytes,
            total_bytes,
            100.0 * func_bytes / total_bytes,
        )
    )
