"""The CDH crash-dump container framing (docs/sn200-crash-dump-retrieval.md §4.3).

Each test pins one of the traps that made the previous decoder fail on the real
dump. They are written against synthetic blobs so they run without the (large,
uncommitted) firmware tree; `test_real_dump_*` add an end-to-end check when the
real artifact is present.
"""

import os
import struct
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

import sn200_container as C  # noqa: E402  -- needs sys.path set above
from sn200_strtab import StringTable  # noqa: E402

FWREV = b"KNGND122"
HASHVAL = 0xA1E928AB
REAL_DUMP = os.path.join(
    HERE, "..", "..", "..", "docs", "sn200-dumps", "nvme7-crash-128k.bin"
)


def desc(str_id, level, index, nargs, stale=False):
    w = (str_id << 16) | ((level >> 4) << 12) | (index << 4) | nargs
    return w | 0x8000_0000 if stale else w


def build(records, core=0, block_index=0, flags=3, at=C.LOG_BASE, total=None):
    """One block at `at`, padded out to a whole container."""
    total = total or (at + C.BLOCK_SIZE)
    buf = bytearray(total)
    buf[0:4] = C.CDH_MAGIC
    struct.pack_into("<I", buf, 4, 0x00020200)
    buf[8:16] = FWREV
    buf[at : at + 8] = FWREV
    struct.pack_into("<4I", buf, at + 8, block_index, (core << 16) | flags, 7, HASHVAL)
    p = at + C.BLOCK_HDR
    for d, ts, args in records:
        struct.pack_into("<2I", buf, p, d, ts)
        p += 8
        for a in args:
            struct.pack_into("<I", buf, p, a)
            p += 4
    return bytes(buf)


def test_record_stride_is_8_plus_args_not_0x14():
    """The in-RAM record is 0x14 + 4*nargs; the on-media one is 8 + 4*nargs.
    Using the RAM stride is what stopped the old decoder chaining at all."""
    data = build(
        [
            (desc(100, 0x60, 0, 2), 0x1111, [0xAA, 0xBB]),
            (desc(101, 0x60, 1, 0), 0x2222, []),
            (desc(102, 0x60, 2, 1), 0x3333, [0xCC]),
        ]
    )
    recs = C.parse_block(data, C.LOG_BASE).records
    assert [r.str_id for r in recs] == [100, 101, 102]
    assert recs[0].args == [0xAA, 0xBB]
    assert recs[2].args == [0xCC]
    assert recs[1].offset - recs[0].offset == 8 + 4 * 2


def test_record_index_spills_into_the_level_byte_past_16():
    """The per-block index is EIGHT bits at desc[11:4], so from record 16 on it
    occupies the low nibble of what looks like the level byte. A decoder that
    reads level as (desc >> 8) & 0xFF sees a corrupt level and stops at 16 --
    which is exactly the ~one-record-per-page artifact we saw on the real dump."""
    recs = [(desc(200 + i, 0x60, i, 0), i, []) for i in range(40)]
    data = build(recs)
    got = C.parse_block(data, C.LOG_BASE).records
    assert len(got) == 40, "index must be read as 8 bits, not 4"
    assert got[16].level == 0x60
    assert got[39].index == 39
    # and the naive reading really would have broken here
    raw = struct.unpack_from("<I", data, got[16].offset)[0]
    assert (raw >> 8) & 0xFF != 0x60


def test_bit31_set_terminates_the_block():
    """Log_Emit SETS bit 31 in RAM; the dump writer CLEARS it on commit
    (`and a13,a10,0x7fffffff`, PROC0 0x7ffaf27b). So a descriptor that still has
    it is uncommitted leftover from an earlier ring generation -- end of data."""
    data = build(
        [
            (desc(300, 0x60, 0, 0), 1, []),
            (desc(301, 0x60, 1, 0), 2, []),
            (desc(302, 0x60, 2, 0, stale=True), 3, []),
            (desc(303, 0x60, 3, 0), 4, []),
        ]
    )
    recs = C.parse_block(data, C.LOG_BASE).records
    assert [r.str_id for r in recs] == [300, 301]


def test_zero_descriptor_terminates_the_block():
    data = build([(desc(400, 0x60, 0, 0), 1, [])])
    recs = C.parse_block(data, C.LOG_BASE).records
    assert len(recs) == 1


def test_index_discontinuity_terminates_the_block():
    """The index is a free integrity check: a wrong framing desynchronises it
    within a couple of records."""
    data = build(
        [
            (desc(500, 0x60, 0, 0), 1, []),
            (desc(501, 0x60, 7, 0), 2, []),
        ]
    )
    assert len(C.parse_block(data, C.LOG_BASE).records) == 1


def test_assert_level_is_recognised():
    data = build([(desc(600, 0x20, 0, 1), 9, [0xDEAD])])
    r = C.parse_block(data, C.LOG_BASE).records[0]
    assert r.level == 0x20 and r.is_assert


def test_block_header_fields():
    data = build(
        [],
        core=5,
        block_index=9,
        flags=7,
        at=C.LOG_BASE + 5 * 4 * C.BLOCK_SIZE,
        total=C.LOG_BASE + 6 * 4 * C.BLOCK_SIZE,
    )
    b = C.parse_block(data, C.LOG_BASE + 5 * 4 * C.BLOCK_SIZE)
    assert b.fwrev == "KNGND122"
    assert (b.core, b.index, b.flags, b.hashval) == (5, 9, 7, HASHVAL)


def test_core_region_grid():
    """4 blocks of 0x1000 per core, 0x4000 apart, first core at 0x12500."""
    assert C.core_region(0) == (0x12500, 0x16500)
    assert C.core_region(3) == (0x1E500, 0x22500)
    assert C.core_region(15)[1] == 0x52500


def test_blocks_are_found_only_on_the_grid_phase():
    """FWREV also appears inside record arguments (the firmware logs its own
    revision as eight %c args). Only the 0x?500 phase is a block header."""
    data = bytearray(build([(desc(700, 0x60, 0, 8), 1, list(b"KNGND122"))]))
    assert C.find_blocks(bytes(data)) == [C.LOG_BASE]


def test_coverage_reports_truncation():
    data = build([], total=0x20000)
    cov = C.coverage(data)
    assert cov["bytes_for_16_cores"] == 0x52500
    assert cov["highest_complete_core"] == 2


def test_hash_mismatch_is_detectable():
    tbl = StringTable([""], "VERSION=1 FWREV=KNGND122 HASHVAL=0xa1e928ab")
    assert tbl.hashval == HASHVAL
    good = C.parse_container(build([]))
    assert C.hash_matches(good, tbl) is True
    bad = good[:]
    bad[0].hashval = 0xDEADBEEF
    assert C.hash_matches(bad, tbl) is False


@pytest.mark.skipif(not os.path.exists(REAL_DUMP), reason="real dump not present")
def test_real_dump_parses_completely():
    data = open(REAL_DUMP, "rb").read()
    assert C.is_container(data)
    assert C.container_fwrev(data) == "KNGND122"
    assert C.container_version(data) == 0x00020200
    blocks = C.parse_container(data)
    assert len(blocks) == 12
    assert sorted({b.core for b in blocks}) == [0, 1, 2, 3]
    assert all(b.hashval == HASHVAL for b in blocks)
    # every block's slot is index mod 4 within its core's 0x4000 region
    for b in blocks:
        start, _ = C.core_region(b.core)
        assert b.offset == start + (b.index % 4) * C.BLOCK_SIZE
    assert sum(len(b.records) for b in blocks) == 733


@pytest.mark.skipif(not os.path.exists(REAL_DUMP), reason="real dump not present")
def test_real_dump_has_no_assert():
    """Documents the finding, and will fail loudly if a longer read ever makes
    it untrue."""
    blocks = C.parse_container(open(REAL_DUMP, "rb").read())
    recs = [r for b in blocks for r in b.records]
    assert {r.level for r in recs} == {0x60}
    assert not [r for r in recs if r.is_assert]
