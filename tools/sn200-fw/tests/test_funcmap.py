"""Tests for funcmap.py, the entry-byte-scan function-boundary builder.

Two incidents motivated this tool: a `bnall` at 0x3003354d that never
existed (disassembly started mid-FLIX-bundle) and marker 8 declared dead
code after disassembling from 0x7ffa7d6d, which is actually `retw.n` inside
the real function that starts at 0x7ffa7a68. The regression tests below
pin exactly those two facts, plus the rest of the known-good anchor list
from the task, so a future change to the scoring heuristics can't quietly
reintroduce either failure mode.
"""

import os
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import funcmap

FW = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
FLAT = os.path.join(FW, "flat")

needs_fw = pytest.mark.skipif(
    not os.path.isdir(FLAT),
    reason="unpacked firmware not found; run unpack.py KNGND122.bin ~/sn200fw",
)


# --- pure unit tests, no firmware required -----------------------------------


@pytest.mark.parametrize(
    "txt,expect",
    [
        ("entry a1,0x30", True),
        ("entry a1,0x400", True),  # exactly at MAX_FRAME
        ("entry a1,0x401", False),  # one byte over MAX_FRAME
        ("entry a1,0x5178", False),  # the actual bogus PROC8 candidate seen in dev
        ("entry a2,0x30", False),  # entry only ever legitimately targets a1
        ("entry a0,0x0", False),
        ("mov.n a1,a2", False),
        ("retw.n", False),
    ],
)
def test_is_plausible_entry(txt, expect):
    assert funcmap.is_plausible_entry(txt) == expect


@pytest.mark.parametrize(
    "txt,expect",
    [
        ("op0=3 ???", True),
        ("op0=4 ???", True),
        ("ERR list index out of range", True),
        ("?B 1234", False),  # a known xdis.py decode gap, not a desync signal
        ("?C 5678", False),
        ("?Balu sub=1 a8,a12,a12", False),
        ("ret", False),
        ("entry a1,0x30", False),
    ],
)
def test_is_hard_unknown(txt, expect):
    assert funcmap.is_hard_unknown(txt) == expect


@pytest.mark.parametrize(
    "txt,expect",
    [
        ("ret", True),
        ("retw", True),
        ("ret.n", True),
        ("retw.n", True),
        ("jx a9", True),
        ("j 0x7ffa8000", False),
        ("call8 0x7ffa8000", False),
    ],
)
def test_is_terminator(txt, expect):
    assert funcmap.is_terminator(txt) == expect


# --- integration tests against the real firmware -----------------------------


@pytest.fixture(scope="module")
def result():
    return funcmap.build()


@pytest.fixture(scope="module")
def jsdata(result):
    return funcmap.to_jsonable(result)


@pytest.fixture(scope="module")
def all_entries(result):
    entries = set()
    for img in result["images"]:
        entries |= set(img["functions"])
    return entries


@needs_fw
def test_scans_all_18_images(jsdata):
    # PROC0-7, PROC9-15 (15) + PROC8's two banks (2) + FCC (1) = 18, matching
    # the flat/*.bin file count the task specifies.
    assert len(jsdata["images"]) == 18


@needs_fw
def test_image_names_match_flat_dir():
    import glob

    flat_names = {
        os.path.basename(p)[:-4] for p in glob.glob(os.path.join(FLAT, "*.bin"))
    }
    got = set(funcmap.load_segments())
    assert got == flat_names


@needs_fw
@pytest.mark.parametrize(
    "addr",
    [
        0x3003353C,  # OAM erase handler
        0x7FFABBF0,  # marker-3 writer
        0x7FFA7A68,  # marker-8 writer -- the corrected entry, not 0x7ffa7d6d
    ],
)
def test_known_good_anchors_are_entries(all_entries, addr):
    assert addr in all_entries


@needs_fw
def test_marker8_incident_does_not_reproduce(result):
    """The original mistake: disassembling from 0x7ffa7d6d (a `retw.n`
    inside the real function) and concluding marker 8 was dead code. Confirm
    0x7ffa7d6d is NOT its own entry, and that in PROC12 -- the image that
    actually writes marker 8 -- it resolves inside the function starting at
    0x7ffa7a68."""
    proc12 = next(i for i in result["images"] if i["name"] == "PROC12_7ff80000")
    assert 0x7FFA7D6D not in proc12["functions"]
    entries = sorted(proc12["functions"])
    import bisect

    i = bisect.bisect_right(entries, 0x7FFA7D6D) - 1
    entry = entries[i]
    f = proc12["functions"][entry]
    assert entry == 0x7FFA7A68
    assert entry <= 0x7FFA7D6D < f["end"]


@needs_fw
def test_bnall_incident_address_is_not_an_entry(all_entries):
    """The other original mistake: a `bnall` claimed at 0x3003354d that
    never existed, from decoding a desynced (mid-FLIX-bundle) stream. It
    must not appear as a function entry in a verified scan."""
    assert 0x3003354D not in all_entries


@needs_fw
def test_functions_within_an_image_do_not_overlap(result):
    for img in result["images"]:
        entries = sorted(img["functions"])
        for a, b in zip(entries, entries[1:]):
            assert img["functions"][a]["end"] <= b, (
                img["name"],
                hex(a),
                hex(img["functions"][a]["end"]),
                hex(b),
            )


@needs_fw
def test_function_extents_are_well_formed(result):
    for img in result["images"]:
        for e, f in img["functions"].items():
            assert f["end"] >= f["entry"]
            assert f["size"] == f["end"] - f["entry"]


@needs_fw
def test_coverage_is_reasonably_high(jsdata):
    total = sum(i["total_bytes"] for i in jsdata["images"])
    func_bytes = sum(i["func_bytes"] for i in jsdata["images"])
    # Regression guard, not a target: catches a change that silently
    # collapses coverage (e.g. an overly strict desync heuristic) without
    # pinning today's exact number, which will drift as xdis.py improves.
    assert func_bytes / total > 0.5


@needs_fw
def test_confirmed_call_targets_are_real_entries(result):
    """Every function flagged confirmed_call=True must actually be the
    target of a call recorded on some other function in the same image --
    this is exactly the cross-check funcmap.py is supposed to perform."""
    for img in result["images"]:
        all_targets = set()
        for f in img["functions"].values():
            all_targets |= {c["target"] for c in f["calls"]}
        for e, f in img["functions"].items():
            if f["confirmed_call"]:
                assert e in all_targets


@needs_fw
def test_fcc_image_correctly_finds_no_windowed_functions(result):
    """FCC.bin's code uses the call0 ABI (plain addi a1,a1,-N / call0 /
    rfei prologues, no `entry`/`retw` at all -- confirmed by direct
    disassembly during development), so a correct scan finds zero
    confirmed functions there. This pins that as an expected, understood
    result rather than a silent scan failure."""
    fcc = next(i for i in result["images"] if i["name"].startswith("FCC"))
    assert len(fcc["functions"]) == 0
