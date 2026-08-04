"""Tests for xref.py's CALLn decoding.

The one that matters is `test_call_size_label_matches_callinc`: `n` is the
window increment in units of 4 registers (n=2 is CALL8), and a `4*(n+1)` label
silently renames every CALL8 to "call12". That mislabel contradicts the
CALLINC bits (31:30) of the return addresses saved in a crash dump's register
file, which is how it was found -- see docs/sn200-proc9-fault.md.
"""

import os
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import xref  # noqa: E402


BASE = 0x7FF80000


def encode_call(pc: int, n: int, target: int) -> bytes:
    """Encode a 3-byte CALLn at `pc` targeting `target`."""
    off = (target - ((pc & ~3) + 4)) >> 2
    off &= 0x3FFFF
    b0 = 0x05 | ((n & 3) << 4) | ((off & 3) << 6)
    return bytes([b0, (off >> 2) & 0xFF, (off >> 10) & 0xFF])


@pytest.mark.parametrize("n", [0, 1, 2, 3])
def test_call_target_round_trips(n: int) -> None:
    pc, target = BASE + 0x100, BASE + 0x200
    d = bytearray(0x300)
    d[pc - BASE : pc - BASE + 3] = encode_call(pc, n, target)
    hits = [(p, k, t) for p, k, t in xref.calls(BASE, bytes(d)) if p == pc]
    assert hits == [(pc, n, target)]


def test_negative_offset() -> None:
    pc, target = BASE + 0x200, BASE + 0x100
    d = bytearray(0x300)
    d[pc - BASE : pc - BASE + 3] = encode_call(pc, 2, target)
    assert (pc, 2, target) in list(xref.calls(BASE, bytes(d)))


def test_call_size_label_matches_callinc() -> None:
    """n is the increment/4, so the printed mnemonic must be call{4*n}."""
    assert [4 * n for n in (0, 1, 2, 3)] == [0, 4, 8, 12]


def test_enclosing_finds_aligned_entry() -> None:
    d = bytearray(0x300)
    d[0x100] = 0x36  # `entry` opcode byte, 4-aligned
    assert xref.enclosing(BASE, bytes(d), BASE + 0x140) == BASE + 0x100


def test_enclosing_ignores_unaligned_0x36() -> None:
    d = bytearray(0x300)
    d[0x101] = 0x36  # not 4-aligned -> not an entry
    assert xref.enclosing(BASE, bytes(d), BASE + 0x140) is None
