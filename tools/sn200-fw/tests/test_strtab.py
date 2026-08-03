"""Offline validation of the string table and log-descriptor ABI.

The heavyweight test here (`test_nargs_matches_format`) is the real proof that
the decode path is right: it takes every log descriptor that the firmware
actually loads with an `l32r`, across all 18 KNGND122 processor images, and
checks that the `nargs` field packed into the descriptor equals the number of
printf conversions in the string the StrId resolves to. Nothing about that
agreement is guaranteed by construction -- if the StrId indexing were off by
one, or the descriptor layout wrong, it would collapse.
"""

import glob
import os
import struct
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from sn200_strtab import (
    KNOWN_LEVELS,
    MAX_NARGS,
    LogDescriptor,
    StringTable,
    count_args,
    render,
    scan_descriptors,
    unescape,
)
import sn200_vuc

FW = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
CSV = os.path.join(FW, "fw", "KNGND122", "StringTable.csv")
GZ = CSV + ".gz"

needs_fw = pytest.mark.skipif(
    not os.path.exists(CSV),
    reason="unpacked firmware not found; run unpack.py KNGND122.bin ~/sn200fw",
)


@pytest.fixture(scope="module")
def table():
    return StringTable.load(CSV)


# --- descriptor packing ------------------------------------------------------
def test_descriptor_unpack():
    d = LogDescriptor.unpack((1636 << 16) | (0x40 << 8) | 1)
    assert (d.str_id, d.level, d.nargs) == (1636, 0x40, 1)


def test_descriptor_roundtrip_of_a_real_literal():
    # 0x064bc206 is a real literal from PROC15; StrId 1611, level 0xc2, nargs 6.
    d = LogDescriptor.unpack(0x064BC206)
    assert d.str_id == 1611


# --- format string handling --------------------------------------------------
def test_unescape_turns_literal_backslash_n_into_newline():
    # The CSV stores the two characters '\' and 'n', not a newline.
    assert unescape("hello\\n") == "hello"
    assert unescape("a\\nb") == "a\nb"


@pytest.mark.parametrize(
    "fmt,n",
    [
        ("no args here", 0),
        ("one %d", 1),
        ("%s and %x and %08X", 3),
        ("100%% literal, one %d", 1),
        ("%02x %ld %llu", 3),
    ],
)
def test_count_args(fmt, n):
    assert count_args(fmt) == n


def test_render_signed_and_unsigned():
    assert render("v=%d", [0xFFFFFFFF]) == "v=-1"
    assert render("v=%u", [0xFFFFFFFF]) == "v=4294967295"
    assert render("v=0x%x", [0xDEAD]) == "v=0xdead"
    assert render("v=0x%X", [0xDEAD]) == "v=0xDEAD"


def test_render_is_short_on_args_without_crashing():
    assert "%d" in render("a=%d b=%d", [1])


# --- string table indexing ---------------------------------------------------
@needs_fw
def test_header_metadata(table):
    assert table.fw_rev == "KNGND122"
    assert table.num_reserved == 16


@needs_fw
@pytest.mark.parametrize(
    "str_id,expect",
    [
        # Golden values cross-checked against docs/sn200-firmware-re.md, which
        # derived them independently.
        (16, "EEPROM: Warning. Failed to set GPIO"),
        (1804, "Admin cmd rejected due to Post Crash startup mode"),
        (2933, "OAM ERASE CMD: Schedule reinit after crash dump erase failed."),
        (3038, "POST CRASH Startup"),
        (3520, "SYS: UNEXSTRT detected"),
        (1273, "SYS: Post Crash startup"),
    ],
)
def test_strid_is_csv_line_plus_one(table, str_id, expect):
    assert table.get(str_id).startswith(expect)


@needs_fw
def test_reserved_ids_are_marked_implausible(table):
    for i in range(1, 16):
        assert not table.plausible(i)


@needs_fw
def test_gzip_and_plain_loaders_agree():
    a = StringTable.load(CSV)
    b = StringTable.load(GZ)
    assert a.lines == b.lines


@needs_fw
def test_from_blob_finds_an_embedded_gzip_member():
    raw = open(GZ, "rb").read()
    t = StringTable.from_blob(b"\x00" * 64 + raw)
    assert t.fw_rev == "KNGND122"


@needs_fw
def test_search(table):
    hits = dict(table.search("Post Crash"))
    assert 1804 in hits and 3038 in hits


# --- the big one -------------------------------------------------------------
def _images():
    return sorted(glob.glob(os.path.join(FW, "flat", "*.bin")))


def _instruction_starts(d: bytes):
    """FLIX length model: op0 8..d -> 2 bytes, e/f -> 8 bytes, else 3."""
    o = 0
    while o < len(d) - 8:
        yield o
        p = d[o] & 0xF
        o += 2 if 8 <= p <= 0xD else (8 if p >= 0xE else 3)


def _referenced_descriptors(d: bytes, table: StringTable):
    """Descriptor literals that an l32r (plain or FLIX slot-0) actually loads."""
    cand = {}
    for o in range(0, len(d) - 3, 4):
        w = struct.unpack_from("<I", d, o)[0]
        x = LogDescriptor.unpack(w)
        if (
            0 < x.str_id < len(table)
            and x.nargs <= MAX_NARGS
            and table.plausible(x.str_id)
        ):
            cand[o] = x
    for o in _instruction_starts(d):
        if o + 3 > len(d):
            break
        op0 = d[o] & 0xF
        if op0 == 1 or (op0 in (0xE, 0xF) and (d[o] >> 4) == 9):
            imm = d[o + 1] | (d[o + 2] << 8)
            tgt = ((o + 3) & ~3) + (imm << 2) - 0x40000
            if tgt in cand:
                yield cand[tgt]


@needs_fw
@pytest.mark.skipif(not _images(), reason="no flat images")
def test_nargs_matches_format(table):
    ok = bad = 0
    seen = set()
    mismatches = []
    for p in _images():
        d = open(p, "rb").read()
        for x in _referenced_descriptors(d, table):
            if x.level not in KNOWN_LEVELS or x.str_id in seen:
                continue
            seen.add(x.str_id)
            n = count_args(table.get(x.str_id))
            if n == x.nargs:
                ok += 1
            else:
                bad += 1
                mismatches.append((x.str_id, x.level, x.nargs, n))
    total = ok + bad
    assert total > 1400, "expected ~1586 referenced descriptors, got %d" % total
    rate = ok / total
    assert rate > 0.99, "nargs agreement fell to %.2f%%; mismatches: %r" % (
        100 * rate,
        mismatches[:20],
    )


@needs_fw
@pytest.mark.skipif(not _images(), reason="no flat images")
def test_level_zero_is_excluded_because_it_is_noise(table):
    """Admitting level 0x00 measurably degrades the scan. This test pins the
    reason KNOWN_LEVELS omits it, so nobody 'fixes' it back in."""

    def agreement(levels):
        ok = bad = 0
        seen = set()
        for p in _images():
            d = open(p, "rb").read()
            for x in _referenced_descriptors(d, table):
                if x.level not in levels or x.str_id in seen:
                    continue
                seen.add(x.str_id)
                if count_args(table.get(x.str_id)) == x.nargs:
                    ok += 1
                else:
                    bad += 1
        return ok / (ok + bad)

    assert agreement(KNOWN_LEVELS) > agreement(KNOWN_LEVELS | {0x00})


@needs_fw
def test_scan_descriptors_finds_a_known_call_site(table):
    """StrId 2933 'Schedule reinit after crash dump erase failed' is documented
    as living in PROC8's overlay bank."""
    p = os.path.join(FW, "flat", "PROC8_30000000.bin")
    if not os.path.exists(p):
        pytest.skip("PROC8 overlay image not unpacked")
    found = {d.str_id for _, d in scan_descriptors(open(p, "rb").read(), table)}
    assert 2933 in found


# --- vendor command encodings ------------------------------------------------
def test_cdw12_encoding_matches_nvme_cli_defines():
    # nvme-cli plugins/wdc/wdc-nvme.c: CMD 0x20, SUBCMD 0x03/0x04/0x05/0x06,
    # CDW12 = (SUBCMD << WDC_NVME_SUBCMD_SHIFT) | CMD, SUBCMD_SHIFT = 8.
    assert sn200_vuc.SECTIONS["crash"]["size_cdw12"] == 0x0320
    assert sn200_vuc.SECTIONS["crash"]["body_cdw12"] == 0x0420
    assert sn200_vuc.SECTIONS["pfail"]["size_cdw12"] == 0x0520
    assert sn200_vuc.SECTIONS["pfail"]["body_cdw12"] == 0x0620
    assert sn200_vuc.SECTIONS["strtbl"]["body_cdw12"] == 0x0220
    assert sn200_vuc.SECTIONS["drvlog"]["body_cdw12"] == 0x0020


def test_string_table_size_is_the_second_dword():
    assert sn200_vuc.SECTIONS["strtbl"]["size_dword"] == 1
    assert sn200_vuc.SECTIONS["drvlog"]["size_dword"] == 0
    assert (
        sn200_vuc.SECTIONS["strtbl"]["size_cdw12"]
        == sn200_vuc.SECTIONS["drvlog"]["size_cdw12"]
        == 0x0120
    )


def test_destructive_commands_are_listed_so_tooling_can_refuse_them():
    assert 0x0503 in sn200_vuc.FORBIDDEN_CDW12  # clear crash -> schedules REINIT
    assert 0x0303 in sn200_vuc.FORBIDDEN_CDW12  # erase SBL EEPROM -> brick
    assert 0x0403 in sn200_vuc.FORBIDDEN_CDW12  # drive uninit
    assert 0xDD in sn200_vuc.FORBIDDEN_OPCODES  # secure purge
    assert 0xFF in sn200_vuc.FORBIDDEN_OPCODES


def test_offset_register_is_cdw13():
    assert sn200_vuc.OFFSET_CDW == 13
