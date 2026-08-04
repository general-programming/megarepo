"""The SN200 crash-dump container, as it actually is on media.

This supersedes the layout *search* in sn200_dump.py for any blob that carries
the container magic. The framing here is PROVEN twice over: recovered from a
real dump pulled off a latched drive, and confirmed instruction-by-instruction
against the block writer at PROC0 0x7ffaf10c. See
docs/sn200-crash-dump-retrieval.md §4.3.

Container:

    0x00000  "\\x00CDH"  header: magic, version, FWREV at +8
    0x00100  "MMAP"      descriptor for an unframed capture region
    0x01000              ~64 KiB of unframed binary
    0x11000  "\\x09CDI"  header of the log section
    0x12500              log blocks; 0x1000 each, 4 per core, 0x4000 per core

Block (0x1000):

    +0x00  char[8]  FWREV                     "KNGND122"
    +0x08  u32      block index in this core's stream
    +0x0c  u32      (core_id << 16) | flags
    +0x10  u32      serial, shared counter sampled at block open
    +0x14  u32      StringTable HASHVAL       0xa1e928ab for KNGND122
    +0x18  records

Record (8 + 4*nargs):

    +0x00  u32  desc    bit31 stale | StrId<<16 | (level>>4)<<12 | idx<<4 | nargs
    +0x04  u32  CCOUNT  raw cycles, not wall time
    +0x08  u32  args[nargs]

Two things a decoder must get right, both of which silently destroy the chain:

  * `level` is only the HIGH nibble of byte 1. The low nibble is the top half of
    an 8-bit per-block record index. Masking `(desc >> 8) & 0xFF` as the level
    makes every record after the 16th in a block look corrupt.
  * bit 31 SET means the record was never committed -- it is leftover from an
    earlier generation of the ring and marks the END of valid data. Log_Emit
    sets that bit in RAM; the dump writer clears it on commit.
"""

from __future__ import annotations

import struct
from dataclasses import dataclass, field

CDH_MAGIC = b"\x00CDH"
CDI_MAGIC = b"\x09CDI"

BLOCK_SIZE = 0x1000
BLOCK_HDR = 0x18
BLOCKS_PER_CORE = 4
LOG_BASE = 0x12500

STALE = 0x8000_0000


@dataclass
class ContainerRecord:
    offset: int
    core: int
    flags: int
    block_index: int
    index: int  # per-block record index, 8-bit
    level: int  # 0x20 assert, 0x40, 0x60 informational
    str_id: int
    ccount: int
    args: list[int] = field(default_factory=list)
    text: str = ""

    @property
    def is_assert(self) -> bool:
        return self.level == 0x20


@dataclass
class Block:
    offset: int
    fwrev: str
    index: int
    core: int
    flags: int
    serial: int
    hashval: int
    records: list[ContainerRecord] = field(default_factory=list)


def is_container(data: bytes) -> bool:
    return data[:4] == CDH_MAGIC


def container_version(data: bytes) -> int:
    return struct.unpack_from("<I", data, 4)[0] if len(data) >= 8 else 0


def container_fwrev(data: bytes) -> str:
    return data[8:16].decode("ascii", "replace") if len(data) >= 16 else ""


def find_blocks(data: bytes, fwrev: bytes | None = None) -> list[int]:
    """Offsets of every log block. Located by FWREV on a BLOCK_SIZE grid, which
    is exact: the writer stores FWREV as the first 8 bytes of every block."""
    if fwrev is None:
        fwrev = data[8:16]
    if len(fwrev) != 8 or not fwrev.strip(b"\x00"):
        return []
    out = []
    off = data.find(fwrev, BLOCK_SIZE)
    while off >= 0:
        if off % BLOCK_SIZE == LOG_BASE % BLOCK_SIZE:
            out.append(off)
        off = data.find(fwrev, off + 1)
    return out


def core_region(core: int) -> tuple[int, int]:
    """(start, end) file offsets of one core's 4-block ring."""
    start = LOG_BASE + core * BLOCKS_PER_CORE * BLOCK_SIZE
    return start, start + BLOCKS_PER_CORE * BLOCK_SIZE


def parse_block(data: bytes, off: int, table=None) -> Block:
    index, stream, serial, hashval = struct.unpack_from("<4I", data, off + 8)
    blk = Block(
        offset=off,
        fwrev=data[off : off + 8].decode("ascii", "replace"),
        index=index,
        core=stream >> 16,
        flags=stream & 0xFFFF,
        serial=serial,
        hashval=hashval,
    )
    end = min(off + BLOCK_SIZE, len(data))
    p = off + BLOCK_HDR
    expect = 0
    while p + 8 <= end:
        desc, ccount = struct.unpack_from("<2I", data, p)
        if desc == 0 or desc & STALE:
            break
        nargs = desc & 0xF
        index8 = (desc >> 4) & 0xFF
        level = ((desc >> 12) & 0xF) << 4
        str_id = desc >> 16
        if index8 != expect or str_id == 0 or p + 8 + 4 * nargs > end:
            break
        args = list(struct.unpack_from("<%dI" % nargs, data, p + 8)) if nargs else []
        rec = ContainerRecord(
            offset=p,
            core=blk.core,
            flags=blk.flags,
            block_index=blk.index,
            index=index8,
            level=level,
            str_id=str_id,
            ccount=ccount,
            args=args,
        )
        if table is not None:
            from sn200_strtab import render

            rec.text = render(table.get(str_id) or "", args, table)
        blk.records.append(rec)
        expect = (expect + 1) & 0xFF
        p += 8 + 4 * nargs
    return blk


def parse_container(data: bytes, table=None) -> list[Block]:
    return [parse_block(data, o, table) for o in find_blocks(data)]


def hash_matches(blocks: list[Block], table) -> bool | None:
    """True/False if the table declares a HASHVAL, None if it does not.
    A mismatch means the table is for a different firmware revision and every
    rendered string will be wrong."""
    want = getattr(table, "hashval", None)
    if want is None:
        return None
    return all(b.hashval == want for b in blocks) if blocks else None


def coverage(data: bytes) -> dict:
    """Which cores the blob actually reaches. A truncated read silently drops
    whole cores, and that is the difference between finding the assert and not."""
    blocks = find_blocks(data)
    cores = sorted({parse_block(data, o).core for o in blocks})
    last_core = (len(data) - LOG_BASE) // (BLOCKS_PER_CORE * BLOCK_SIZE) - 1
    return {
        "blocks": len(blocks),
        "cores_present": cores,
        "highest_complete_core": last_core,
        "bytes_for_16_cores": LOG_BASE + 16 * BLOCKS_PER_CORE * BLOCK_SIZE,
    }
