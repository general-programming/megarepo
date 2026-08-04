"""Find every load/store at a given struct offset, across all SN200 flat images.

    SN200_FW=~/sn200fw python3 structref.py 0x48                 # any access
    SN200_FW=~/sn200fw python3 structref.py 0x118 s32i           # stores only
    SN200_FW=~/sn200fw python3 structref.py 0x44 l32i PROC0      # one image

Complements litref.py (l32r literals) and xref.py (CALLn sites). This is what
recovers a request/message struct's field map once one field is known.

Covers RRI8 loads/stores in plain 3-byte form **and** in FLIX slot A (bundles
are 8 bytes and not 4-aligned -- see docs/xtensa-flix-decoding.md), plus the
narrow l32i.n/s32i.n forms, which only reach offsets 0..60.

⚠ THIS IS NOT AN EXHAUSTIVE FIELD SWEEP. Handlers routinely re-base the struct
pointer (`addmi a12,a2,256` / `addi a12,a12,-84`) and then address the same
field with a different displacement. PROC8's OAM `0x0007` handler writes
+0x118..+0x12c through `req+0xA0`, so a scan for offset 0x118 does not see it.
Treat a negative result as "not found by this method", never as "does not
exist" -- see docs/sn200-marker-write.md §3.3.
"""

import glob
import os
import sys

BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))

# RRI8 op1 -> (mnemonic, immediate scale). op0 == 2 for the whole class.
RRI8 = {
    0x0: ("l8ui", 1),
    0x1: ("l16ui", 2),
    0x2: ("l32i", 4),
    0x4: ("s8i", 1),
    0x5: ("s16i", 2),
    0x6: ("s32i", 4),
    0x9: ("l16si", 2),
    0xB: ("l32ai", 4),
    0xF: ("s32ri", 4),
}


def slot_a_word(q: int) -> int:
    """Reassemble FLIX slot A into a base-ISA 24-bit instruction word."""
    return (
        ((q >> 24) & 0xF)
        | ((q >> 4) & 0xF) << 4
        | ((q >> 8) & 0xF) << 8
        | ((q >> 12) & 0xF) << 12
        | ((q >> 16) & 0xFF) << 16
    )


def decode_rri8(w: int) -> tuple[str, int, int, int] | None:
    """(mnemonic, t, s, byte offset) for an RRI8 load/store word, else None."""
    if (w & 0xF) != 2:
        return None
    ent = RRI8.get((w >> 12) & 0xF)
    if ent is None:
        return None
    name, scale = ent
    return name, (w >> 4) & 0xF, (w >> 8) & 0xF, ((w >> 16) & 0xFF) * scale


def scan_image(base: int, d: bytes, off: int):
    """Yield (addr, how, mnemonic, t, s) for accesses at struct offset `off`."""
    for o in range(len(d) - 2):
        w = d[o] | (d[o + 1] << 8) | (d[o + 2] << 16)
        r = decode_rri8(w)
        if r and r[3] == off:
            yield base + o, "plain", r[0], r[1], r[2]
    for o in range(len(d) - 7):
        if (d[o] & 0xF) not in (0xE, 0xF):  # FLIX bundle formats
            continue
        r = decode_rri8(slot_a_word(int.from_bytes(d[o : o + 8], "little")))
        if r and r[3] == off:
            yield base + o, "flix", r[0], r[1], r[2]
    if off % 4 or off > 60:  # narrow forms cannot encode it
        return
    for o in range(len(d) - 1):
        op0 = d[o] & 0xF
        if op0 not in (8, 9):
            continue
        w = d[o] | (d[o + 1] << 8)
        if ((w >> 12) & 0xF) * 4 == off:
            name = "l32i.n" if op0 == 8 else "s32i.n"
            yield base + o, "narrow", name, (w >> 4) & 0xF, (w >> 8) & 0xF


def scan(off: int, images: list[str] | None = None):
    for p in sorted(glob.glob(f"{BD}/flat/*.bin")):
        name = os.path.basename(p)[:-4]
        if images and not any(i in name for i in images):
            continue
        base = int(name.rsplit("_", 1)[1], 16)
        for hit in scan_image(base, open(p, "rb").read(), off):
            yield (name, *hit)


def main(argv: list[str]) -> int:
    if not argv:
        print(__doc__)
        return 2
    off = int(argv[0], 0)
    kinds = argv[1].split(",") if len(argv) > 1 and argv[1] else None
    images = argv[2].split(",") if len(argv) > 2 else None
    n = 0
    for name, addr, how, mn, t, s in scan(off, images):
        if kinds and not any(mn.startswith(k) for k in kinds):
            continue
        print("%-22s %08x  %-6s %-7s a%d,a%d,0x%x" % (name, addr, how, mn, t, s, off))
        n += 1
    print("%d site(s)" % n)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
