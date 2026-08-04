"""Tests for structref.py.

The synthetic fixtures pin the two encodings a naive scanner gets wrong:
FLIX slot A at an offset that is not 4-aligned, and the narrow s32i.n whose
immediate is scaled by 4. The firmware-backed tests pin real field accesses
that docs/sn200-marker-write.md rests on.
"""

import os
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import structref  # noqa: E402


BASE = 0x7FF80000
FLAT = os.path.join(os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw")), "flat")
needs_fw = pytest.mark.skipif(
    not os.path.isdir(FLAT), reason="firmware images not present"
)


def plain_rri8(op1: int, t: int, s: int, imm8: int) -> bytes:
    w = 2 | (t << 4) | (s << 8) | (op1 << 12) | (imm8 << 16)
    return bytes([w & 0xFF, (w >> 8) & 0xFF, (w >> 16) & 0xFF])


def flix_rri8(op1: int, t: int, s: int, imm8: int, op0: int = 0xE) -> bytes:
    w = 2 | (t << 4) | (s << 8) | (op1 << 12) | (imm8 << 16)
    q = (
        op0
        | ((w >> 4) & 0xF) << 4
        | ((w >> 8) & 0xF) << 8
        | ((w >> 12) & 0xF) << 12
        | ((w >> 16) & 0xFF) << 16
        | (w & 0xF) << 24
    )
    return q.to_bytes(8, "little")


def hits(data: bytes, off: int):
    return list(structref.scan_image(BASE, data, off))


def test_plain_s32i_found():
    # s32i a11,a2,0x48 -> op1=6, imm8 = 0x48/4 = 0x12
    d = b"\x00" * 16 + plain_rri8(6, 11, 2, 0x12) + b"\x00" * 16
    got = [h for h in hits(d, 0x48) if h[1] == "plain"]
    assert got == [(BASE + 16, "plain", "s32i", 11, 2)]


def test_flix_slot_a_found_at_odd_offset():
    """The trap: FLIX bundles are 8 bytes and are NOT 4-aligned."""
    d = b"\x00" * 19 + flix_rri8(2, 9, 2, 0x11) + b"\x00" * 16  # l32i a9,a2,0x44
    got = [h for h in hits(d, 0x44) if h[1] == "flix"]
    assert got == [(BASE + 19, "flix", "l32i", 9, 2)]


def test_narrow_immediate_is_scaled_by_four():
    # s32i.n a3,a2,0x10 -> op0=9, t=3, s=2, r=4
    w = 9 | (3 << 4) | (2 << 8) | (4 << 12)
    d = b"\x00" * 8 + bytes([w & 0xFF, w >> 8]) + b"\x00" * 8
    assert (BASE + 8, "narrow", "s32i.n", 3, 2) in hits(d, 0x10)
    assert not [h for h in hits(d, 0x4) if h[1] == "narrow"]


def test_narrow_not_reported_beyond_its_range():
    """s32i.n cannot encode 0x48; asking for it must not synthesise narrow hits."""
    d = bytes(range(256)) * 4
    assert not [h for h in hits(d, 0x48) if h[1] == "narrow"]


@needs_fw
def test_proc0_request_code_dispatch_load():
    """0x7ffa415e reads [ctx+0x44], the request code the 45-entry table indexes."""
    got = {a for _, a, _, mn, _, s in structref.scan(0x44, ["PROC0"]) if mn == "l32i"}
    assert 0x7FFA415E in got


@needs_fw
def test_proc0_marker_value_load():
    """0x7ffa4714 reads [ctx+0x50], the word stored verbatim as the boot marker."""
    got = {
        a
        for _, a, _, mn, t, s in structref.scan(0x50, ["PROC0"])
        if mn == "l32i" and t == 10 and s == 2
    }
    assert 0x7FFA4714 in got


@needs_fw
def test_proc8_oam_verb_store_for_sbl_write():
    """0x300335fc stores the OAM verb (=1) for the only verb-1 host producer."""
    got = {
        a
        for _, a, _, mn, _, _ in structref.scan(0x118, ["PROC8_30000000"])
        if mn == "s32i"
    }
    assert 0x300335FC in got


@needs_fw
def test_offset_scan_misses_rebased_accesses():
    """Documented blind spot: the 0x0007 handler writes +0x118 through req+0xA0,
    so an offset-0x118 sweep does not see it. Guards the honesty of the caveat
    in docs/sn200-marker-write.md §3.3 -- if this ever starts passing, the
    exhaustiveness claims in that section can be strengthened."""
    got = {a for _, a, _, _, _, _ in structref.scan(0x118, ["PROC8_30000000"])}
    assert 0x300338C4 not in got
