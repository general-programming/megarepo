"""String table + log-descriptor handling for HGST/WDC Ultrastar SN200 firmware.

The firmware does not store printf format strings in its code. It logs by
numeric StrId and the host resolves them against a StringTable shipped with the
firmware image (`StringTable.csv.gz`) or pulled off the drive (VUC 0xC6, CDW12
0x0220). PROVEN: `StrId N` == CSV line `N + 1` (line 1 is the header, lines
2..(NUMRSVD+1) are `# StrId k reserved` placeholders).

The log-call ABI packs a call site's identity into one 32-bit literal:

    descriptor = (StrId << 16) | (level << 8) | nargs

PROVEN by cross-referencing `l32r a10, <descriptor>` / `call8 <logfn>` pairs
against the CSV: e.g. StrId 1636 "...Bad Erase sub-cmd: %d" has nargs 1,
StrId 1628 "Erase to system area 0 failed" has nargs 0, StrId 3370 (four %x)
has nargs 4.
"""

from __future__ import annotations

import gzip
import io
import re
import struct
from dataclasses import dataclass

# Level bytes seen in genuine descriptors, i.e. literals an `l32r` actually
# loads before a call to the log function. Measured over all 18 KNGND122
# processor images: 0x60 (1782), 0x20 (398), 0x40 (397) dominate; every other
# value has <=13 hits and is noise.
#
# 0x00 is deliberately EXCLUDED even though it does occur. A descriptor with
# level 0 and nargs 0 is a small aligned integer and is indistinguishable from
# an ordinary constant, so admitting it floods a scan with false positives:
# with 0x00 in the set, nargs agreement against the format strings drops from
# 99.87% (1584/1586) to 98.51% (1589/1613). Every one of the extra mismatches
# is a level-0x00 hit. See tests/test_strtab.py::test_nargs_matches_format.
KNOWN_LEVELS = {0x20, 0x40, 0x60, 0x80, 0xA0, 0xC0, 0xE0}
# The widest printf in the KNGND122 string table takes 15 arguments (StrId
# 3189/3190, the "Outstanding Trim ..." watchdog -- the very record most likely
# to matter here). A tighter cap silently drops those call sites and, worse,
# breaks record-chain following at exactly the interesting record.
MAX_NARGS = 15


# An EMITTED record's descriptor has bit 31 set. PROVEN in PROC0's Log_Emit,
# which shows in the clear what PROC8's FLIX bundles hide:
#     7ffb0dcf: l32r   a10, 0x7ff825a0   ; 0x80000000
#     7ffb0dd2: or     a10, a3, a10
#     7ffb0dd5: s32i.n a10, a1, 0x8      ; record+0x08 = descriptor | 0x80000000
# The literal in the firmware's constant pool does NOT have it; the log
# function ORs it in on the way into the ring. So a dump scanner that reuses
# the firmware-image decode verbatim finds nothing: `word >> 16` yields
# 0x8000|StrId and every lookup misses.
EMITTED_FLAG = 0x80000000

# ASSERT/FATAL level. PROVEN: 418 of 520 `break.n` (Xtensa BREAK -> "LOGIC
# TRAP") sites across the 18 images are immediately preceded by a call to that
# image's log function, and the descriptors involved carry level 0x20.
# Informational chatter uses 0x40/0x60.
LEVEL_ASSERT = 0x20


@dataclass
class LogDescriptor:
    raw: int
    str_id: int
    level: int
    nargs: int
    emitted: bool = False

    @classmethod
    def unpack(cls, word: int) -> "LogDescriptor":
        """Decode a descriptor from either a firmware literal pool or an
        emitted on-media log record."""
        emitted = bool(word & EMITTED_FLAG)
        body = word & ~EMITTED_FLAG
        # nargs is masked to 4 bits by the emitter (`extui a9,a9,0,4`), so the
        # upper nibble of the low byte is always zero in a genuine descriptor.
        # Keeping the full byte here makes that a free extra filter.
        return cls(
            word, (body >> 16) & 0xFFFF, (body >> 8) & 0xFF, body & 0xFF, emitted
        )

    @property
    def is_assert(self) -> bool:
        return self.level == LEVEL_ASSERT


class StringTable:
    """StrId -> format string, plus the header metadata."""

    def __init__(self, lines: list[str], header: str = "") -> None:
        self.lines = lines
        self.header = header
        self.meta: dict[str, str] = {}
        for k, v in re.findall(r"(\w+)=(\S+)", header):
            self.meta[k] = v

    @property
    def fw_rev(self) -> str:
        return self.meta.get("FWREV", "?")

    @property
    def num_reserved(self) -> int:
        return int(self.meta.get("NUMRSVD", "0"))

    def __len__(self) -> int:
        return len(self.lines)

    def get(self, str_id: int) -> str | None:
        # StrId N == CSV line N+1 == lines[N] with lines[] holding the header at 0
        if 0 <= str_id < len(self.lines):
            return self.lines[str_id]
        return None

    def text(self, str_id: int) -> str | None:
        """Like get(), but with the CSV's literal `\\n`/`\\r`/`\\t` escapes
        turned into real characters and trailing whitespace stripped."""
        s = self.get(str_id)
        return unescape(s) if s is not None else None

    def plausible(self, str_id: int) -> bool:
        """Could `str_id` be a real log call site?

        StrIds below NUMRSVD (16) are reserved and never emitted, and StrId 0
        is not a string at all -- lines[0] is the CSV header. Admitting 0 makes
        any word of the form 0x0000LLNN decode to the header text, which is a
        common and very confusing false positive in a raw dump scan.
        """
        if str_id < max(self.num_reserved, 1):
            return False
        s = self.get(str_id)
        return bool(s) and not s.startswith("# StrId ")

    def search(self, pattern: str) -> list[tuple[int, str]]:
        rx = re.compile(pattern, re.I)
        return [(i, s) for i, s in enumerate(self.lines) if i and rx.search(s)]

    # -- loaders --------------------------------------------------------------
    @classmethod
    def from_csv_bytes(cls, data: bytes) -> "StringTable":
        text = data.decode("latin1")
        raw = text.split("\n")
        # lines[0] is the header; lines[N] is StrId N because the header
        # occupies CSV line 1 and StrId N lives on CSV line N+1.
        return cls([ln.rstrip("\r") for ln in raw], raw[0] if raw else "")

    @classmethod
    def load(cls, path_or_bytes) -> "StringTable":
        """Accept a path, a .gz path, or raw bytes from the on-drive STRTBL blob."""
        if isinstance(path_or_bytes, (bytes, bytearray)):
            data = bytes(path_or_bytes)
        else:
            with open(path_or_bytes, "rb") as fh:
                data = fh.read()
        return cls.from_blob(data)

    @classmethod
    def from_blob(cls, data: bytes) -> "StringTable":
        """Auto-detect the container the string table arrived in.

        Handles, in order:
          - gzip stream (what `StringTable.csv.gz` is inside the firmware tar)
          - plain CSV text (what you get after gunzipping it)
          - a gzip stream that starts partway in, which is what a raw VUC blob
            looks like if the drive prepends a header. We scan a bounded window
            for the gzip magic rather than assuming an offset.
        """
        if data[:2] == b"\x1f\x8b":
            return cls.from_csv_bytes(gzip.decompress(data))
        # Must be checked before any "looks like the string table" heuristic:
        # a gzip member stores the original filename in its header, so raw
        # gzip bytes contain the literal text "StringTable.csv".
        if data[:8].startswith(b"VERSION="):
            return cls.from_csv_bytes(data)
        # bounded search for an embedded gzip member
        for off in range(0, min(len(data), 4096)):
            if data[off : off + 3] == b"\x1f\x8b\x08":
                try:
                    with gzip.GzipFile(fileobj=io.BytesIO(data[off:])) as gz:
                        out = gz.read()
                    if b"StringTable" in out[:512] or out[:8].startswith(b"VERSION="):
                        return cls.from_csv_bytes(out)
                except OSError:
                    continue
        raise ValueError(
            "unrecognised string-table container; first 32 bytes: %s" % data[:32].hex()
        )


def scan_descriptors(
    data: bytes, table: StringTable, *, aligned: bool = True, base: int = 0
) -> list[tuple[int, LogDescriptor]]:
    """Find every dword in `data` that decodes as a plausible log descriptor.

    This is the workhorse for reading a crash dump whose record framing is not
    fully known: descriptors are self-identifying enough (a valid StrId, a
    known level nibble, a small nargs) that scanning finds the log call sites
    reliably, with a manageable false-positive rate.
    """
    out: list[tuple[int, LogDescriptor]] = []
    step = 4 if aligned else 1
    for off in range(0, len(data) - 3, step):
        word = struct.unpack_from("<I", data, off)[0]
        d = LogDescriptor.unpack(word)
        if d.nargs > MAX_NARGS or d.level not in KNOWN_LEVELS:
            continue
        if not table.plausible(d.str_id):
            continue
        out.append((base + off, d))
    return out


_FMT = re.compile(
    r"%[-+ #0]*[0-9]*(?:\.[0-9]+)?(?:hh|h|ll|l|z|j|t)?([diuoxXcspfgeEGF%])"
)

_ESC = {"\\n": "\n", "\\r": "\r", "\\t": "\t", "\\\\": "\\"}


def unescape(s: str) -> str:
    """The CSV stores escapes literally: a format string ends with the two
    characters `\\` and `n`, not a newline. Turn them into real characters."""
    out = s
    for k, v in _ESC.items():
        out = out.replace(k, v)
    return out.rstrip()


def count_args(fmt: str) -> int:
    return sum(1 for m in _FMT.finditer(unescape(fmt)) if m.group(1) != "%")


def render(fmt: str, args: list[int], table: "StringTable | None" = None) -> str:
    """Render a firmware printf format with integer arguments.

    Firmware args are all raw 32-bit words on the wire.

    `%s` is special. PROVEN: the firmware has no way to put a string in a log
    record, and the contiguous string arrays it needs (the boot-marker names,
    the per-section state trichotomy, the shutdown types) are emitted through
    `SYS: %s` with the StrId computed at runtime and passed AS THE ARGUMENT
    WORD. StrIds 1277-1282 for example never appear as descriptors anywhere in
    any image; they are reached as `1277 + 3*section + state`.

    So a `%s` argument is a StrId, not a pointer -- pass `table` and it is
    resolved recursively.
    """
    fmt = unescape(fmt)
    out: list[str] = []
    pos = 0
    it = iter(args)
    for m in _FMT.finditer(fmt):
        out.append(fmt[pos : m.start()])
        pos = m.end()
        conv = m.group(1)
        if conv == "%":
            out.append("%")
            continue
        try:
            v = next(it)
        except StopIteration:
            out.append(m.group(0))
            continue
        if conv in "diu":
            out.append(str(v - (1 << 32) if conv == "d" and v >= (1 << 31) else v))
        elif conv in "xX":
            s = "%x" % v
            out.append(s.upper() if conv == "X" else s)
        elif conv == "o":
            out.append("%o" % v)
        elif conv == "c":
            out.append(chr(v & 0xFF))
        elif conv == "s":
            if table is not None and table.plausible(v):
                out.append(unescape(table.get(v)))
            else:
                out.append("<StrId %d?>" % v)
        elif conv == "p":
            out.append("<ptr 0x%08x>" % v)
        else:
            out.append("0x%08x" % v)
    out.append(fmt[pos:])
    return "".join(out).rstrip("\n")
