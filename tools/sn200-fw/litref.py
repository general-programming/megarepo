"""Find every `l32r` that loads a given literal, across all SN200 flat images.

    SN200_FW=~/sn200fw python3 litref.py -a 7ff82b54          # by literal ADDRESS
    SN200_FW=~/sn200fw python3 litref.py -v 80000008          # by literal VALUE
    SN200_FW=~/sn200fw python3 litref.py -v 80000008 PROC12   # one image only

Complements xref.py, which finds CALLn sites. Constants too wide for `movi`
(anything outside a 12-bit signed immediate) must come from a literal pool, so
sweeping l32r is a sound way to enumerate the producers of such a constant.

FLIX bundles are 8 bytes and are **not aligned** (see docs/xtensa-flix-decoding.md),
so slot A is decoded at every byte offset. An aligned-only sweep silently misses
loads and turns them into false "this constant is never loaded" results.
"""

import glob
import os
import struct
import sys

BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))


def slot_a_word(q: int) -> int:
    """Reassemble FLIX slot A into a base-ISA 24-bit instruction word."""
    return (
        ((q >> 24) & 0xF)
        | ((q >> 4) & 0xF) << 4
        | ((q >> 8) & 0xF) << 8
        | ((q >> 12) & 0xF) << 12
        | ((q >> 16) & 0xFF) << 16
    )


def l32r_target(pc: int, imm16: int) -> int:
    """Base-ISA l32r: ((PC+3) & ~3) + (imm16 << 2) - 0x40000. PC is the real,
    possibly unaligned, bundle address -- do not round it before calling."""
    return (((pc + 3) & ~3) + (imm16 << 2) - 0x40000) & 0xFFFFFFFF


def l32rs(base: int, d: bytes):
    """Yield (pc, reg, literal_addr, kind) for every l32r, plain or FLIX slot A."""
    n = len(d)
    for o in range(0, n - 2):
        pc = base + o
        if (d[o] & 0xF) == 1:  # plain 3-byte l32r
            yield (
                pc,
                (d[o] >> 4) & 0xF,
                l32r_target(pc, d[o + 1] | (d[o + 2] << 8)),
                "plain",
            )
        if o + 8 <= n and (d[o] & 0xF) in (0xE, 0xF):
            w = slot_a_word(int.from_bytes(d[o : o + 8], "little"))
            if (w & 0xF) == 1:
                yield pc, (w >> 4) & 0xF, l32r_target(pc, (w >> 8) & 0xFFFF), "flix"


def images(only: str | None = None):
    for p in sorted(glob.glob(f"{BD}/flat/*.bin")):
        name = os.path.basename(p)[:-4]
        if only and not name.startswith(only):
            continue
        yield name, int(name.rsplit("_", 1)[1], 16), open(p, "rb").read()


def pool_slots(base: int, d: bytes, value: int) -> set[int]:
    """Addresses of 4-aligned words equal to `value` (literal pools are aligned)."""
    return {
        base + o
        for o in range(0, len(d) - 3, 4)
        if struct.unpack_from("<I", d, o)[0] == value
    }


def find(mode: str, val: int, only: str | None = None):
    """Yield (image, pc, reg, literal_addr, kind)."""
    for name, base, d in images(only):
        want = {val} if mode == "-a" else pool_slots(base, d, val)
        if not want:
            continue
        for pc, reg, tgt, kind in l32rs(base, d):
            if tgt in want:
                yield name, pc, reg, tgt, kind


if __name__ == "__main__":
    if len(sys.argv) < 3 or sys.argv[1] not in ("-a", "-v"):
        sys.exit(__doc__)
    hits = list(find(sys.argv[1], int(sys.argv[2], 16), *sys.argv[3:4]))
    for name, pc, reg, tgt, kind in hits:
        print("%-22s %08x  l32r a%-2d,0x%08x  [%s]" % (name, pc, reg, tgt, kind))
    print(f"{len(hits)} site(s)")
