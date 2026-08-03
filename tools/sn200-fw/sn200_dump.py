"""Structural analysis of an SN200 crash-dump / drive-log blob.

The on-media framing of the SN200's log records is not documented anywhere and
was not recoverable from the firmware within the effort budget (the FLIX
decoder is incomplete -- see docs/sn200-firmware-re.md §1). Rather than guess a
layout, this module *derives* it from the blob using the one thing that is
PROVEN: the log descriptor word

    (StrId << 16) | (level << 8) | nargs

is self-identifying. A run of consecutive log records therefore forms a chain:
if you know the frame layout, the descriptor of record N+1 sits a computable
distance after the descriptor of record N, because nargs tells you how many
argument words record N carries.

`find_frame_layout` searches the small space of plausible layouts and scores
each by the longest clean chain it produces. A layout that is wrong produces
chains of length 1-2; the right one walks hundreds of records. That is a
self-validating result: it does not depend on trusting a guess.

If no layout scores well, `scan_records` falls back to an unframed descriptor
scan, which still surfaces the assert text -- just without reliable arguments.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass, field

from sn200_strtab import (
    KNOWN_LEVELS,
    MAX_NARGS,
    LogDescriptor,
    StringTable,
    count_args,
    render,
)


@dataclass
class FrameLayout:
    """Where the pieces of one log record sit, relative to the descriptor.

    All units are 32-bit words:

        [ pre ][ descriptor ][ mid ][ nargs args ][ pad ]

    `mid` matters: on the SN200 the timestamp sits BETWEEN the descriptor and
    the arguments, so a model without it cannot express the real layout.
    """

    pre: int
    pad: int
    align: int = 1  # record start alignment, in words
    mid: int = 0  # words between the descriptor and the first argument

    def record_words(self, nargs: int) -> int:
        n = self.pre + 1 + self.mid + nargs + self.pad
        if self.align > 1:
            n = ((n + self.align - 1) // self.align) * self.align
        return n

    def arg0_word(self) -> int:
        """Word offset of the first argument, relative to the descriptor."""
        return 1 + self.mid

    def describe(self) -> str:
        return "pre=%d desc=1 mid=%d args=nargs pad=%d align=%d words" % (
            self.pre,
            self.mid,
            self.pad,
            self.align,
        )


# The real SN200 log record. PROVEN from Log_Emit (PROC8 0x7ffb45a8, with
# byte-identical copies in PROC6/9/13/14; PROC0's 0x7ffb0d80 shows in plain
# 3-byte encodings what PROC8 hides inside FLIX bundles):
#
#   7ffb45f4: s32i.n a9,a1,0x8    ; record+0x08 = descriptor | 0x80000000
#   7ffb45f6: rsr    a11,234      ; CCOUNT, the Xtensa cycle counter
#   7ffb461a: s32i   a11,a1,0x10  ; record+0x10 = timestamp
#   7ffb463d: loop   a0,...       ; vararg copy, trip count = nargs
#   7ffb4665: s32i.n a8,a12,0x14  ; args from record+0x14, stride 4
#   7ffb4669: extui  a9,a9,0,4    ; nargs is 4 bits
#
#   struct fw_log_record {
#       u32 hdr_a;                 /* 0x00 pre-filled, content unknown */
#       u32 hdr_b;                 /* 0x04 pre-filled, content unknown */
#       u32 desc;                  /* 0x08 0x80000000|(StrId<<16)|(lvl<<8)|nargs */
#       u32 hdr_d;                 /* 0x0c pre-filled, content unknown */
#       u32 timestamp;             /* 0x10 raw CCOUNT -- CYCLES, NOT WALL TIME */
#       u32 arg[nargs];            /* 0x14 raw 32-bit words */
#   };                             /* length = 0x14 + 4*nargs, VARIABLE */
#
# No core id is stamped: there is no `rsr ... PRID` anywhere in Log_Emit.
# INFERRED: the collector attributes a core from which per-core ring the
# record came out of. The ring writer (PROC8 0x7ffb4868) has 1023 slots.
SN200_LOG_LAYOUT = FrameLayout(pre=2, pad=0, align=1, mid=2)


@dataclass
class Record:
    offset: int
    desc: LogDescriptor
    args: list[int] = field(default_factory=list)
    pre_words: list[int] = field(default_factory=list)
    text: str = ""
    timestamp: int | None = None  # raw CCOUNT cycles, not wall time

    @property
    def str_id(self) -> int:
        return self.desc.str_id

    @property
    def is_assert(self) -> bool:
        return self.desc.is_assert


def _is_descriptor(word: int, table: StringTable) -> bool:
    d = LogDescriptor.unpack(word)
    if d.level not in KNOWN_LEVELS or d.nargs > MAX_NARGS:
        return False
    if not table.plausible(d.str_id):
        return False
    # A descriptor whose nargs disagrees with its format string is almost
    # always a coincidence rather than a real call site.
    return count_args(table.get(d.str_id)) == d.nargs


def _chain_len(
    data: bytes, start: int, layout: FrameLayout, table: StringTable, limit: int = 4096
) -> tuple[int, int]:
    """Follow the record chain from `start` (offset of the first descriptor).
    Returns (records_walked, end_offset)."""
    n = 0
    off = start
    while n < limit and 0 <= off <= len(data) - 4:
        word = struct.unpack_from("<I", data, off)[0]
        if not _is_descriptor(word, table):
            break
        d = LogDescriptor.unpack(word)
        n += 1
        step = layout.record_words(d.nargs) * 4
        if step <= 0:
            break
        off += step
    return n, off


def find_frame_layout(
    data: bytes,
    table: StringTable,
    *,
    max_pre: int = 4,
    max_pad: int = 4,
    max_mid: int = 3,
    min_chain: int = 8,
) -> tuple[FrameLayout | None, int]:
    """Score candidate frame layouts; return the best and its longest chain.

    `SN200_LOG_LAYOUT` is tried first, so on a genuine SN200 dump the answer is
    the documented layout and the search only ever serves as a cross-check. The
    wider search is kept because the *crash dump container* is not the same
    thing as the drive log: the container header, and whether it embeds the
    rings verbatim, are still unrecovered (see docs §4.5).
    """
    starts = [
        o
        for o in range(0, len(data) - 3, 4)
        if _is_descriptor(struct.unpack_from("<I", data, o)[0], table)
    ]
    if not starts:
        return None, 0

    candidates = [SN200_LOG_LAYOUT]
    for pre in range(max_pre + 1):
        for pad in range(max_pad + 1):
            for mid in range(max_mid + 1):
                for align in (1, 2, 4):
                    lay = FrameLayout(pre, pad, align, mid)
                    if lay != SN200_LOG_LAYOUT:
                        candidates.append(lay)

    best: FrameLayout | None = None
    best_len = 0
    for layout in candidates:
        longest = 0
        # sampling the starts keeps this linear enough on multi-MB dumps
        for s in starts[:2000]:
            if s < layout.pre * 4:
                continue
            n, _ = _chain_len(data, s, layout, table)
            if n > longest:
                longest = n
            if longest > 512:
                break
        if longest > best_len:
            best_len, best = longest, layout
    # Accept either an absolutely long chain, or a short one that nonetheless
    # accounts for most of the descriptors present -- a small log region is
    # still a real one. Noise fails both: it scatters many descriptors that
    # chain no further than themselves, so the share stays tiny.
    if best_len < min_chain and not (best_len >= 3 and best_len >= 0.5 * len(starts)):
        return None, best_len
    return best, best_len


def parse_chain(
    data: bytes,
    start: int,
    layout: FrameLayout,
    table: StringTable,
    limit: int = 100000,
) -> list[Record]:
    out: list[Record] = []
    off = start
    while len(out) < limit and 0 <= off <= len(data) - 4:
        word = struct.unpack_from("<I", data, off)[0]
        if not _is_descriptor(word, table):
            break
        d = LogDescriptor.unpack(word)
        args = []
        base = off + layout.arg0_word() * 4
        for i in range(d.nargs):
            ao = base + i * 4
            if ao + 4 > len(data):
                break
            args.append(struct.unpack_from("<I", data, ao)[0])
        pre = []
        for i in range(layout.pre, 0, -1):
            po = off - i * 4
            if po >= 0:
                pre.append(struct.unpack_from("<I", data, po)[0])
        # On the documented layout the last `mid` word is the CCOUNT timestamp.
        ts = None
        if layout.mid:
            to = off + layout.mid * 4
            if to + 4 <= len(data):
                ts = struct.unpack_from("<I", data, to)[0]
        fmt = table.get(d.str_id) or "<StrId %d unknown>" % d.str_id
        out.append(Record(off, d, args, pre, render(fmt, args, table), ts))
        off += layout.record_words(d.nargs) * 4
    return out


def scan_records(
    data: bytes, table: StringTable, layout: FrameLayout = SN200_LOG_LAYOUT
) -> list[Record]:
    """Unframed fallback: every descriptor, arguments read at `layout`'s
    argument offset. Message text stays reliable; argument values do not,
    because nothing has confirmed these are really consecutive records."""
    out = []
    a0 = layout.arg0_word() * 4
    for off in range(0, len(data) - 3, 4):
        word = struct.unpack_from("<I", data, off)[0]
        if not _is_descriptor(word, table):
            continue
        d = LogDescriptor.unpack(word)
        args = [
            struct.unpack_from("<I", data, off + a0 + 4 * i)[0]
            for i in range(d.nargs)
            if off + a0 + 4 + 4 * i <= len(data)
        ]
        ts = None
        if layout.mid and off + layout.mid * 4 + 4 <= len(data):
            ts = struct.unpack_from("<I", data, off + layout.mid * 4)[0]
        fmt = table.get(d.str_id) or ""
        out.append(Record(off, d, args, [], render(fmt, args, table), ts))
    return out


# Keywords that mark a record as a candidate root cause, strongest first.
#
# Tiers 0-1 are generic "something died here" markers. Tier 2 is deliberately
# SN200-specific: docs/sn200-firmware-re.md §8 establishes that the trigger for
# this drive's lockups is a large deallocate/TRIM racing the L2P journal flush,
# with the 15-second "Outstanding Trim ..." watchdog (StrId 3189/3190) as the
# thing that actually fires. None of the generic keywords match that string --
# it says "Trim", not "trap" -- so without this tier the single most probable
# root-cause record is ranked below routine noise.
ASSERT_PATTERNS = [
    # tier 0: an assertion or trap fired
    "assert",
    "logic trap",
    "logictrap",
    "panic",
    "fatal",
    "exception",
    "unrecoverable",
    # tier 1: generic failure vocabulary
    "crash",
    "trap",
    "watchdog",
    "timeout",
    "corrupt",
    # tier 2: the SN200's documented failure mode (§8)
    "outstanding trim",
    "deallocate",
    "de-allocate",
    "l2p",
    "journal",
    "log replay",
    "end marker",
    "unexstrt",
    "post crash",
    "reinit",
    "blockset",
    "system area",
    # tier 3: weakest
    "unexpected",
    "invalid state",
    "failed",
    "error",
]


def rank_asserts(records: list[Record], table: StringTable) -> list[Record]:
    """Records that look like the thing that actually fired, best first.

    The strongest signal is not a keyword at all: `level == 0x20` IS the assert
    level. PROVEN -- the firmware's assert idiom is

        l32r  a10, <descriptor>
        call8 <Log_Emit>
        break.n                  ; Xtensa BREAK -> the "LOGIC TRAP" shutdown type

    and 418 of the 520 `break.n` sites across the 18 images are immediately
    preceded by exactly that call. There is no separate assert record type, no
    __FILE__/__LINE__, and no assert format string: **the StrId of the level
    0x20 record IS the assert identity**. Keywords are only a fallback for
    ranking within that set and for dumps where the level was not recovered.
    """
    scored = []
    for r in records:
        fmt = (table.get(r.str_id) or "").lower()
        kw_tier = len(ASSERT_PATTERNS)
        for i, kw in enumerate(ASSERT_PATTERNS):
            if kw in fmt:
                kw_tier = i
                break
        if r.is_assert:
            scored.append((0, kw_tier, r.offset, r))
        elif kw_tier < len(ASSERT_PATTERNS):
            scored.append((1, kw_tier, r.offset, r))
    scored.sort(key=lambda t: (t[0], t[1], t[2]))
    return [r for _, _, _, r in scored]


# --- container sniffing ------------------------------------------------------
EYECATCHERS = {
    b"\x7fELF": "ELF (the format libied.so's assert_dump_decoder.c expects)",
    b"E6LG": "E6LG host capture-diagnostics container",
    b"CRSHDMP": "crash dump section tag",
    b"PFCRDMP": "pfail crash dump section tag",
    b"DRVLOG": "drive log section tag",
    b"STRTBL": "string table section tag",
    b"UNEXSTRT": "UNEXSTRT stub header (see docs §7)",
    b"\x1f\x8b\x08": "gzip stream",
}


def sniff(data: bytes) -> list[str]:
    notes = []
    if not data:
        return ["empty"]
    if all(b == 0 for b in data[:4096]):
        notes.append("first 4 KiB is all zero")
    if all(b == 0xFF for b in data[:4096]):
        notes.append("first 4 KiB is all 0xFF -- looks like erased flash")
    for magic, what in EYECATCHERS.items():
        i = data.find(magic)
        if i >= 0 and i < 1 << 20:
            notes.append(
                "%s at offset 0x%x (%s)" % (magic.decode("latin1", "replace"), i, what)
            )
    return notes or ["no known eyecatcher in the first 1 MiB"]


def ascii_runs(data: bytes, minlen: int = 6, limit: int = 200):
    out = []
    cur = bytearray()
    start = 0
    for i, b in enumerate(data):
        if 32 <= b < 127:
            if not cur:
                start = i
            cur.append(b)
        else:
            if len(cur) >= minlen:
                out.append((start, cur.decode()))
                if len(out) >= limit:
                    return out
            cur = bytearray()
    if len(cur) >= minlen:
        out.append((start, cur.decode()))
    return out
