"""Tests for litref.py -- especially the unaligned-FLIX trap.

The synthetic fixtures below are the real guard: an aligned-only sweep passes
every other test and fails `test_flix_slot_a_found_at_odd_offset`.
"""

import os
import struct
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import litref  # noqa: E402


BASE = 0x7FF80000
LIT = BASE + 0x04  # literal pool sits below the code; l32r only reaches backwards
CODE = 0x300  # byte offset of the synthetic code


def plain_l32r(pc: int, reg: int, target: int) -> bytes:
    """Encode a base-ISA 3-byte `l32r areg, target` sited at `pc`."""
    imm16 = ((target - ((pc + 3) & ~3)) + 0x40000) >> 2
    assert 0 <= imm16 <= 0xFFFF, imm16
    return bytes([(reg << 4) | 1, imm16 & 0xFF, imm16 >> 8])


def flix_l32r(pc: int, reg: int, target: int, op0: int = 0xE) -> bytes:
    """Encode an 8-byte FLIX bundle whose slot A is `l32r areg, target`."""
    imm16 = ((target - ((pc + 3) & ~3)) + 0x40000) >> 2
    w = 1 | (reg << 4) | (imm16 << 8)  # base-ISA 24-bit word
    q = (
        op0
        | ((w >> 4) & 0xF) << 4
        | ((w >> 8) & 0xF) << 8
        | ((w >> 12) & 0xF) << 12
        | ((w >> 16) & 0xFF) << 16
        | ((w >> 0) & 0xF) << 24
    )
    return q.to_bytes(8, "little")


def test_l32r_target_matches_encoder():
    for pc in (BASE + CODE, BASE + CODE + 1, BASE + CODE + 2, BASE + CODE + 3):
        enc = plain_l32r(pc, 5, LIT)
        assert litref.l32r_target(pc, enc[1] | (enc[2] << 8)) == LIT


def test_plain_l32r_is_found():
    img = bytearray(0x400)
    img[CODE : CODE + 3] = plain_l32r(BASE + CODE, 7, LIT)
    hits = [h for h in litref.l32rs(BASE, bytes(img)) if h[2] == LIT]
    assert (BASE + CODE, 7, LIT, "plain") in hits


@pytest.mark.parametrize("off", [CODE, CODE + 1, CODE + 2, CODE + 3])
def test_flix_slot_a_found_at_odd_offset(off):
    """FLIX bundles are NOT 4-aligned. An aligned-only sweep fails off%4 != 0.

    Ground truth in the real image: PROC0 0x7ffa431f is `l32r a11,0x7ff82b54`
    inside a bundle at an address == 3 (mod 4). See docs/xtensa-flix-decoding.md.
    """
    img = bytearray(0x400)
    img[off : off + 8] = flix_l32r(BASE + off, 11, LIT)
    hits = [h for h in litref.l32rs(BASE, bytes(img)) if h[2] == LIT]
    assert (BASE + off, 11, LIT, "flix") in hits


def test_flix_format_f_also_decoded():
    img = bytearray(0x400)
    off = CODE + 1
    img[off : off + 8] = flix_l32r(BASE + off, 3, LIT, op0=0xF)
    hits = [h for h in litref.l32rs(BASE, bytes(img)) if h[2] == LIT]
    assert (BASE + off, 3, LIT, "flix") in hits


def test_pool_slots_only_matches_aligned_words():
    img = bytearray(0x40)
    struct.pack_into("<I", img, 0x10, 0x80000008)
    img[0x21:0x25] = struct.pack("<I", 0x80000008)  # unaligned: not a pool slot
    got = litref.pool_slots(BASE, bytes(img), 0x80000008)
    assert got == {BASE + 0x10}


def test_no_match_returns_nothing():
    img = bytes(0x200)
    assert [h for h in litref.l32rs(BASE, img) if h[2] == 0x7FF80999] == []
