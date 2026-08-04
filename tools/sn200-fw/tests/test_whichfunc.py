"""Tests for whichfunc.py, the address -> containing-function lookup tool.

This is the tool meant to make the mid-stream disassembly mistake
impossible, so its tests use a small synthetic function-map (no firmware
needed) to pin the exact scenario that went wrong before: the same address
existing inside unrelated functions in several independent processor
images at once, and a lookup that must not silently pick the wrong one.
"""

import os
import subprocess
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

import whichfunc

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)
SCRIPT = os.path.join(TOOLS, "whichfunc.py")


def make_map():
    """Two images sharing overlapping virtual addresses, mirroring how
    PROC0..PROC15 each map their own code around 0x7ffa0000+. Mirrors the
    real marker-8 incident: PROC_A has an unrelated function that happens
    to also cover 0x7ffa7d6d; PROC_B is the "real" one where 0x7ffa7d6d is
    `retw.n` inside the function that starts at 0x7ffa7a68."""
    return {
        "revision": "TEST",
        "images": [
            {
                "name": "PROC_A_7ff80000",
                "functions": [
                    {
                        "entry": "0x7ffa7cc8",
                        "end": "0x7ffa82b4",
                        "size": 1516,
                        "confirmed_entry": True,
                        "confirmed_call": False,
                        "callers": [],
                        "calls": [],
                        "gap_to_next": 0,
                        "gap_reason": None,
                    }
                ],
            },
            {
                "name": "PROC_B_7ff80000",
                "functions": [
                    {
                        "entry": "0x7ffa7a68",
                        "end": "0x7ffa7e96",
                        "size": 1070,
                        "confirmed_entry": True,
                        "confirmed_call": True,
                        "callers": ["0x7ffa8000"],
                        "calls": [],
                        "gap_to_next": 0,
                        "gap_reason": None,
                    }
                ],
            },
        ],
    }


@pytest.fixture
def data():
    return make_map()


def test_entry_point_hit(data):
    hits = whichfunc.lookup(data, 0x7FFA7A68)
    assert len(hits) == 1
    name, f, off = hits[0]
    assert name == "PROC_B_7ff80000"
    assert off == 0


def test_inside_function_offset(data):
    hits = whichfunc.lookup(data, 0x7FFA7D6D)
    by_name = {name: (f, off) for name, f, off in hits}
    assert "PROC_A_7ff80000" in by_name
    assert "PROC_B_7ff80000" in by_name
    # the corrected historical fact: PROC_B says this is offset 0x305 into
    # the function starting at 0x7ffa7a68, NOT a fresh entry.
    f_b, off_b = by_name["PROC_B_7ff80000"]
    assert int(f_b["entry"], 16) == 0x7FFA7A68
    assert off_b == 0x7FFA7D6D - 0x7FFA7A68


def test_not_found(data):
    assert whichfunc.lookup(data, 0x12345678) == []


def test_image_scoped_lookup_returns_only_that_image(data):
    hits = whichfunc.lookup(data, 0x7FFA7D6D, image="PROC_B_7ff80000")
    assert len(hits) == 1
    assert hits[0][0] == "PROC_B_7ff80000"


def test_scoped_lookup_on_wrong_image_finds_nothing(data):
    # 0x7ffa8500 is past the end of PROC_B's only function (ends 0x7ffa7e96)
    # but PROC_A's own coverage is elsewhere entirely -- neither should hit
    # when scoped to PROC_B.
    hits = whichfunc.lookup(data, 0x7FFA8500, image="PROC_B_7ff80000")
    assert hits == []


def test_describe_multi_image_ambiguity_is_flagged(data):
    out = whichfunc.describe(data, 0x7FFA7D6D)
    assert "found in 2 images" in out
    assert "PROC_A_7ff80000" in out
    assert "PROC_B_7ff80000" in out


def test_describe_single_hit_is_unambiguous(data):
    out = whichfunc.describe(data, 0x7FFA7A68, image="PROC_B_7ff80000")
    assert "IS the entry point" in out


def test_describe_offset_hit_says_inside_function(data):
    out = whichfunc.describe(data, 0x7FFA7D6D, image="PROC_B_7ff80000")
    assert "inside function 0x7ffa7a68+0x305" in out


def test_describe_not_found_warns_against_disassembling(data):
    out = whichfunc.describe(data, 0x12345678)
    assert "NOT inside any known function" in out
    assert "do not disassemble" in out


# --- CLI smoke test (needs the real function-map.json) ----------------------

MAP_PATH = os.path.join(TOOLS, "function-map.json")

needs_map = pytest.mark.skipif(
    not os.path.exists(MAP_PATH),
    reason="function-map.json not built; run funcmap.py first",
)


@needs_map
def test_cli_reports_marker8_correctly():
    out = subprocess.run(
        [
            sys.executable,
            SCRIPT,
            "--image",
            "PROC12_7ff80000",
            "0x7ffa7a68",
            "0x7ffa7d6d",
        ],
        cwd=TOOLS,
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    lines = out.strip().splitlines()
    assert "IS the entry point" in lines[0]
    assert "inside function 0x7ffa7a68+0x305" in lines[1]


@needs_map
def test_cli_no_args_shows_usage():
    r = subprocess.run(
        [sys.executable, SCRIPT], cwd=TOOLS, capture_output=True, text=True
    )
    assert r.returncode != 0
