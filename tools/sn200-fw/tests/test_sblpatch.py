"""Tests for sblpatch.py's 0x100-block de-interleave.

SBLPATCH.bin holds two .BIN/.SEG chains interleaved one 0x100-byte block at a
time. Parsing it as a single chain (which is what every earlier attempt did)
finds the odd stream's chain, walks off the end of the first segment into the
other stream's bytes, and yields a code segment that will not disassemble. The
tests below pin the two properties that make the split correct: parity ordering
and .SEG data offsets being relative to the .BIN header *in stream coordinates*.
"""

import os
import struct
import sys

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import sblpatch  # noqa: E402

BLOCK = sblpatch.BLOCK


def build(even: bytes, odd: bytes) -> bytes:
    """Interleave two streams back into a container image."""
    n = max(len(even), len(odd))
    even = even.ljust(n, b"\x00")
    odd = odd.ljust(n, b"\x00")
    out = bytearray()
    for i in range(0, n, BLOCK):
        out += even[i : i + BLOCK] + odd[i : i + BLOCK]
    return bytes(out)


def make_chain(prefix: int, segs: list[tuple[int, bytes]]) -> bytes:
    """Build `prefix` filler bytes, then a .BIN header and .SEG chain."""
    body = bytearray(b"\x00" * prefix)
    body += b".BIN" + b"\x00" * 12
    for load, data in segs:
        do = len(body) - prefix + 0x10
        body += struct.pack("<4sIII", b".SEG", do, len(data), load) + data
    body += struct.pack("<4sIII", b".SEG", 0xFFFFFFFF, 0, 0)
    body += b"\x00" * (-len(body) % BLOCK)
    return bytes(body)


def test_deinterleave_is_parity_ordered() -> None:
    d = b"".join(bytes([i]) * BLOCK for i in range(6))
    ev, od = sblpatch.deinterleave(d)
    assert ev == bytes([0]) * BLOCK + bytes([2]) * BLOCK + bytes([4]) * BLOCK
    assert od == bytes([1]) * BLOCK + bytes([3]) * BLOCK + bytes([5]) * BLOCK


def test_chain_offsets_are_relative_to_bin_header() -> None:
    """A .SEG data offset is measured from the .BIN header, not the stream."""
    stream = make_chain(0x300, [(0x7FFB6000, b"code" * 4), (0x7FF98000, b"str")])
    b, segs = sblpatch.chain(stream)
    assert b == 0x300
    assert [(la, data) for _do, _dl, la, data in segs] == [
        (0x7FFB6000, b"code" * 4),
        (0x7FF98000, b"str"),
    ]


def test_two_chains_survive_interleaving() -> None:
    even = make_chain(0x200, [(0x7FFB6000, bytes(range(64)))])
    odd = make_chain(0x000, [(0x7FFA0710, bytes(range(255, 191, -1)))])
    img = build(even, odd)
    ev, od = sblpatch.deinterleave(img)
    assert sblpatch.chain(ev)[1][0][3] == bytes(range(64))
    assert sblpatch.chain(od)[1][0][3] == bytes(range(255, 191, -1))


def test_single_chain_parse_of_the_raw_image_is_wrong() -> None:
    """Guard the trap: parsing the un-split image yields corrupt segment data."""
    even = make_chain(0x200, [(0x7FFB6000, b"\xaa" * 0x400)])
    odd = make_chain(0x000, [(0x7FFA0710, b"\x55" * 0x400)])
    img = build(even, odd)
    _b, segs = sblpatch.chain(img)
    assert segs and segs[0][3] != b"\x55" * 0x400
