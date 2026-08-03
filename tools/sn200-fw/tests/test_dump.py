"""Offline validation of the crash-dump decoder.

The on-media record framing is unknown, so the decoder derives it. These tests
build synthetic dumps in several plausible framings using the PROVEN descriptor
packing and real KNGND122 StrIds, then check that `find_frame_layout` recovers
the framing it was given -- including that it does NOT confidently recover a
framing from noise.
"""

import os
import struct
import subprocess
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sn200_dump import (
    SN200_LOG_LAYOUT,
    FrameLayout,
    find_frame_layout,
    parse_chain,
    rank_asserts,
    scan_records,
    sniff,
)
from sn200_strtab import (
    EMITTED_FLAG,
    LEVEL_ASSERT,
    LogDescriptor,
    StringTable,
    count_args,
    render,
)

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)
FW = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
CSV = os.path.join(FW, "fw", "KNGND122", "StringTable.csv")

needs_fw = pytest.mark.skipif(not os.path.exists(CSV), reason="firmware not unpacked")


@pytest.fixture(scope="module")
def table():
    return StringTable.load(CSV)


def pick_strids(table, n=40):
    """Real StrIds whose nargs the decoder can verify, biased to include the
    Post-Crash-relevant ones."""
    wanted = [3520, 2933, 1804, 1636, 3038, 1774, 1607]
    out = [i for i in wanted if table.plausible(i)]
    for i in range(200, len(table)):
        if len(out) >= n:
            break
        if table.plausible(i) and count_args(table.get(i)) <= 4 and i not in out:
            out.append(i)
    return out


def build_dump(
    table,
    layout: FrameLayout,
    str_ids,
    level=0x60,
    seed=0x1000,
    emitted=False,
    args_for=None,
):
    """Emit a byte-exact synthetic log region in the given framing.

    `emitted=True` sets bit 31 of the descriptor, which is what the firmware's
    Log_Emit actually writes into the ring.
    """
    buf = bytearray()
    expected = []
    ts = seed
    for sid in str_ids:
        nargs = count_args(table.get(sid))
        rec = bytearray()
        for i in range(layout.pre):
            rec += struct.pack("<I", 0xDEAD0000 | i)
        desc_off = len(buf) + len(rec)
        word = (sid << 16) | (level << 8) | nargs
        if emitted:
            word |= EMITTED_FLAG
        rec += struct.pack("<I", word)
        # `mid` words sit between descriptor and args; on the real layout the
        # last of them is the CCOUNT timestamp.
        for i in range(layout.mid):
            rec += struct.pack("<I", ts if i == layout.mid - 1 else 0xBBBB0000 | i)
        if args_for is not None:
            args = list(args_for(sid, nargs))
        else:
            args = [(0xA0000000 | (sid << 8) | i) & 0xFFFFFFFF for i in range(nargs)]
        for a in args:
            rec += struct.pack("<I", a)
        total = layout.record_words(nargs) * 4
        rec += b"\x00" * (total - len(rec))
        buf += rec
        expected.append((desc_off, sid, args, ts))
        ts += 0x37
    return bytes(buf), expected


@needs_fw
@pytest.mark.parametrize(
    "layout",
    [
        FrameLayout(0, 0, 1),  # bare: descriptor then args
        FrameLayout(1, 0, 1),  # timestamp, descriptor, args
        FrameLayout(2, 0, 1),  # 64-bit timestamp
        FrameLayout(1, 1, 1),  # timestamp, descriptor, args, trailer
        FrameLayout(2, 0, 2),  # 64-bit timestamp, even-word aligned records
    ],
)
def test_layout_is_recovered_from_a_synthetic_dump(table, layout):
    data, expected = build_dump(table, layout, pick_strids(table, 60))
    found, chain = find_frame_layout(data, table)
    assert found is not None, "no layout derived (best chain %d)" % chain
    # Recovering an equivalent stride is what matters; check by re-parsing.
    recs = parse_chain(data, expected[0][0], found, table)
    assert len(recs) >= len(expected) * 0.9, (
        "derived %s walked only %d of %d records"
        % (found.describe(), len(recs), len(expected))
    )
    got = {(r.offset, r.str_id) for r in recs}
    want = {(o, s) for o, s, _, _ in expected}
    assert len(got & want) >= len(want) * 0.9


@needs_fw
def test_arguments_are_recovered_exactly(table):
    layout = FrameLayout(1, 0, 1)
    data, expected = build_dump(table, layout, pick_strids(table, 40))
    recs = parse_chain(data, expected[0][0], layout, table)
    by_off = {r.offset: r for r in recs}
    checked = 0
    for off, sid, args, _ in expected:
        if off in by_off:
            assert by_off[off].args == args
            assert by_off[off].str_id == sid
            checked += 1
    assert checked >= 35


@needs_fw
def test_rendered_text_matches_the_string_table(table):
    layout = FrameLayout(1, 0, 1)
    ids = [i for i in (3520, 2933, 1804) if table.plausible(i)]
    data, expected = build_dump(table, layout, ids)
    recs = parse_chain(data, expected[0][0], layout, table)
    texts = " | ".join(r.text for r in recs)
    assert "UNEXSTRT" in texts
    assert "Schedule reinit after crash dump erase failed" in texts
    # StrId 1804 takes one %x arg; check it was substituted, not left literal
    assert "%x" not in texts


@needs_fw
def test_random_noise_does_not_yield_a_confident_layout(table):
    import random

    rnd = random.Random(1234)
    noise = bytes(rnd.getrandbits(8) for _ in range(256 * 1024))
    layout, chain = find_frame_layout(noise, table, min_chain=8)
    assert layout is None or chain < 8, (
        "derived a layout from noise (chain %d) -- the guard is too weak" % chain
    )


@needs_fw
def test_erased_flash_yields_nothing(table):
    layout, chain = find_frame_layout(b"\xff" * 65536, table)
    assert layout is None
    assert "erased flash" in " ".join(sniff(b"\xff" * 65536))


def _benign(table, n=5):
    """StrIds with no printf args and no failure vocabulary at all."""
    bad = (
        "assert",
        "trap",
        "fail",
        "crash",
        "error",
        "timeout",
        "corrupt",
        "unexpected",
        "invalid",
        "trim",
        "deallocate",
        "l2p",
        "journal",
        "reinit",
        "blockset",
        "system area",
        "replay",
        "unexstrt",
        "post crash",
        "end marker",
    )
    return [
        i
        for i in range(300, len(table))
        if table.plausible(i)
        and count_args(table.get(i)) == 0
        and not any(k in table.get(i).lower() for k in bad)
    ][:n]


@needs_fw
def test_assert_ranking_puts_a_logic_trap_first(table):
    layout = FrameLayout(1, 0, 1)
    trap = [i for i, s in table.search("Logic Trap")][:1]
    if not trap:
        pytest.skip("no logic-trap string in this table")
    data, expected = build_dump(table, layout, _benign(table) + trap)
    recs = parse_chain(data, expected[0][0], layout, table)
    ranked = rank_asserts(recs, table)
    assert ranked, "nothing ranked"
    assert ranked[0].str_id in trap


@needs_fw
def test_assert_ranking_surfaces_the_trim_watchdog(table):
    """docs/sn200-firmware-re.md §8: the documented trigger for this drive is a
    large deallocate racing the L2P journal flush, and the record that fires is
    the "Outstanding Trim ..." watchdog. It contains no generic assert keyword,
    so it needs the SN200-specific tier of ASSERT_PATTERNS to rank at all."""
    layout = FrameLayout(1, 0, 1)
    trim = [i for i, s in table.search("Outstanding Trim")][:1]
    if not trim:
        pytest.skip("no trim-watchdog string in this table")
    data, expected = build_dump(table, layout, _benign(table) + trim)
    recs = parse_chain(data, expected[0][0], layout, table)
    ranked = rank_asserts(recs, table)
    assert ranked, "the trim watchdog did not rank at all"
    assert ranked[0].str_id in trim


@needs_fw
def test_scan_mode_still_finds_messages_without_framing(table):
    layout = FrameLayout(3, 2, 4)  # deliberately exotic
    data, expected = build_dump(table, layout, pick_strids(table, 30))
    recs = scan_records(data, table)
    found = {r.str_id for r in recs}
    assert len({s for _, s, _, _ in expected} & found) >= 25


@needs_fw
def test_cli_runs_end_to_end(table, tmp_path):
    layout = FrameLayout(1, 0, 1)
    data, _ = build_dump(table, layout, pick_strids(table, 30))
    dump = tmp_path / "crash.bin"
    dump.write_bytes(data)
    out = tmp_path / "out.json"
    r = subprocess.run(
        [
            sys.executable,
            os.path.join(TOOLS, "decode-crash-dump.py"),
            str(dump),
            "--string-table",
            CSV,
            "--json",
            str(out),
        ],
        capture_output=True,
        text=True,
    )
    assert r.returncode == 0, r.stdout + r.stderr
    assert "SN200 crash dump decode" in r.stdout
    assert "record framing" in r.stdout
    import json

    j = json.loads(out.read_text())
    assert j["fw_rev"] == "KNGND122"
    assert j["records"]


@needs_fw
def test_cli_warns_on_a_mismatched_firmware_revision(table, tmp_path):
    dump = tmp_path / "crash.bin"
    dump.write_bytes(b"\x00" * 4096)
    r = subprocess.run(
        [
            sys.executable,
            os.path.join(TOOLS, "decode-crash-dump.py"),
            str(dump),
            "--string-table",
            CSV,
            "--rev",
            "KNGND100",
        ],
        capture_output=True,
        text=True,
    )
    assert r.returncode == 0
    assert "StrIds are NOT stable" in r.stdout


def test_sniff_reports_known_eyecatchers():
    assert any("ELF" in n for n in sniff(b"\x7fELF" + b"\x00" * 100))
    assert any("gzip" in n for n in sniff(b"\x1f\x8b\x08" + b"\x00" * 100))
    assert any("UNEXSTRT" in n for n in sniff(b"xx" + b"UNEXSTRT" + b"\x00" * 64))


# --- the real SN200 record format ---------------------------------------------
# PROVEN from Log_Emit (PROC8 0x7ffb45a8 / PROC0 0x7ffb0d80):
#   record = [hdr_a][hdr_b][desc|0x80000000][hdr_d][ccount][args...]
#   length = 0x14 + 4*nargs


@needs_fw
def test_emitted_descriptor_has_bit31_and_still_decodes(table):
    """The literal in the firmware's constant pool has no bit 31; Log_Emit ORs
    it in on the way into the ring. A decoder that reuses the firmware-image
    decode verbatim gets StrId 0x8000|N and finds nothing at all."""
    word = EMITTED_FLAG | (3038 << 16) | (LEVEL_ASSERT << 8) | 0
    d = LogDescriptor.unpack(word)
    assert d.str_id == 3038
    assert d.level == LEVEL_ASSERT
    assert d.emitted is True
    assert d.is_assert is True
    # and the naive decode is wrong, which is the whole point
    assert (word >> 16) != 3038


@needs_fw
def test_real_sn200_layout_is_recovered_and_preferred(table):
    data, expected = build_dump(
        table, SN200_LOG_LAYOUT, pick_strids(table, 60), emitted=True
    )
    found, chain = find_frame_layout(data, table)
    assert found == SN200_LOG_LAYOUT, "got %r (chain %d)" % (found, chain)
    assert chain >= 50


@needs_fw
def test_real_layout_recovers_args_and_ccount_exactly(table):
    ids = pick_strids(table, 40)
    data, expected = build_dump(table, SN200_LOG_LAYOUT, ids, emitted=True)
    recs = parse_chain(data, expected[0][0], SN200_LOG_LAYOUT, table)
    by_off = {r.offset: r for r in recs}
    checked = 0
    for off, sid, args, ts in expected:
        if off in by_off:
            r = by_off[off]
            assert r.str_id == sid
            assert r.args == args, "args wrong at 0x%x" % off
            assert r.timestamp == ts, "ccount wrong at 0x%x" % off
            checked += 1
    assert checked >= 35


@needs_fw
def test_record_length_is_0x14_plus_4_nargs(table):
    for nargs in range(0, 16):
        assert SN200_LOG_LAYOUT.record_words(nargs) * 4 == 0x14 + 4 * nargs


@needs_fw
def test_args_are_read_after_the_timestamp_not_immediately(table):
    """A layout without `mid` would read the CCOUNT as argument 0."""
    assert SN200_LOG_LAYOUT.arg0_word() == 3  # desc, hdr_d, ccount, then args
    ids = [
        i
        for i in range(300, len(table))
        if table.plausible(i) and count_args(table.get(i)) == 1
    ][:1]
    if not ids:
        pytest.skip("no single-arg string")
    data, expected = build_dump(table, SN200_LOG_LAYOUT, ids, emitted=True)
    recs = parse_chain(data, expected[0][0], SN200_LOG_LAYOUT, table)
    assert recs[0].args == expected[0][2]
    assert recs[0].args[0] != recs[0].timestamp


@needs_fw
def test_percent_s_argument_is_resolved_as_a_strid(table):
    """PROVEN: the firmware cannot put a string in a log record. The per-section
    state trichotomy (StrIds 1277-1282) is emitted through a `%s` format with
    the StrId computed at runtime as 1277 + 3*section + state and passed as the
    ARGUMENT WORD. Those StrIds appear as descriptors nowhere in any image."""
    if not (table.plausible(1275) and table.plausible(1279)):
        pytest.skip("string table lacks the expected entries")
    assert "%s" in table.get(1275)
    out = render(table.get(1275), [1279], table)
    assert out == "SYS: Crash Dump section is in invalid state"
    # 1277 + 3*section + state, section 1 = PFail, state 1 = detected
    assert (
        render(table.get(1275), [1277 + 3 * 1 + 1], table)
        == "SYS: PFail Crash Dump is detected"
    )
    # without the table there is nothing to resolve against, and it says so
    assert "StrId" in render(table.get(1275), [1279], None)


@needs_fw
def test_assert_level_outranks_a_mere_keyword_match(table):
    """level 0x20 IS the assert. A level-0x60 record whose text merely contains
    'failed' must not outrank an actual assert-level record."""
    noisy = [i for i, s in table.search("failed")][:1]
    quiet = [
        i
        for i in range(300, len(table))
        if table.plausible(i) and count_args(table.get(i)) == 0
    ][:1]
    if not (noisy and quiet):
        pytest.skip("string table lacks suitable entries")
    a, _ = build_dump(table, SN200_LOG_LAYOUT, noisy, level=0x60, emitted=True)
    b, _ = build_dump(
        table, SN200_LOG_LAYOUT, quiet, level=LEVEL_ASSERT, emitted=True, seed=0x9000
    )
    recs = scan_records(a + b, table)
    ranked = rank_asserts(recs, table)
    assert ranked, "nothing ranked"
    assert ranked[0].is_assert
    assert ranked[0].str_id in quiet


@needs_fw
def test_strid_zero_is_not_a_valid_call_site(table):
    """lines[0] is the CSV header, not a string. Admitting StrId 0 makes any
    word of the form 0x0000LLNN decode to the header text -- a very confusing
    false positive that showed up immediately on the first realistic dump."""
    assert not table.plausible(0)
    hdr = struct.pack("<I", (0 << 16) | (0x60 << 8) | 0)
    assert not any(r.str_id == 0 for r in scan_records(hdr * 8, table))


@needs_fw
def test_a_short_but_complete_chain_is_still_accepted(table):
    """A small log region is a real one. Requiring an absolutely long chain
    made the decoder fall back to scan mode on a 5-record dump."""
    ids = pick_strids(table, 5)
    data, _ = build_dump(table, SN200_LOG_LAYOUT, ids, emitted=True)
    layout, chain = find_frame_layout(b"\x00" * 64 + data, table)
    assert layout == SN200_LOG_LAYOUT
    assert chain == len(ids)


@needs_fw
def test_realistic_dump_names_the_assert(table):
    """The whole point of the exercise, end to end: an unknown container header
    followed by a plausible failure sequence, and the decoder must name the
    assert and resolve the %s record."""
    import random

    rnd = random.Random(7)
    buf = bytearray(rnd.getrandbits(8) for _ in range(0x100))
    ts = [0x51000000]

    def rec(sid, lvl, args):
        n = count_args(table.get(sid))
        assert n == len(args)
        t0 = ts[0]
        ts[0] += rnd.randint(1000, 90000)
        return struct.pack(
            "<IIIII", 0, 0, EMITTED_FLAG | (sid << 16) | (lvl << 8) | n, 0, t0
        ) + b"".join(struct.pack("<I", a) for a in args)

    for sid, lvl, a in [
        (317, 0x60, [0x0, 0x1C9C380]),  # deallocate request
        (
            3189,
            0x40,
            [0, 42, 1, 3, 9, 0x4000, 0x1C9C380, 0, 0, 0, 0, 0, 0x1770, 0x2EE0, 0],
        ),  # trim watchdog, 15 args
        (1275, 0x60, [1278]),  # SYS: %s
        (48, LEVEL_ASSERT, []),  # THE ASSERT
        (1774, 0x60, []),  # persistent internal error AEN
    ]:
        if table.plausible(sid):
            buf += rec(sid, lvl, a)

    layout, chain = find_frame_layout(bytes(buf), table)
    assert layout == SN200_LOG_LAYOUT, "framing not recovered past the header"
    recs = []
    off = 0
    while off <= len(buf) - 4:
        w = struct.unpack_from("<I", buf, off)[0]
        from sn200_dump import _is_descriptor

        if _is_descriptor(w, table):
            recs = parse_chain(bytes(buf), off, layout, table)
            break
        off += 4
    texts = {r.str_id: r.text for r in recs}
    assert 48 in texts and texts[48] == "STK: Overflow detected"
    # the %s record resolved its StrId argument
    assert texts.get(1275) == "SYS: Crash Dump is detected"
    # the 15-argument watchdog rendered every field
    assert "numOfRange 16384" in texts.get(3189, "")
    ranked = rank_asserts(recs, table)
    assert ranked[0].str_id == 48, "the assert did not rank first"
    assert ranked[0].is_assert
