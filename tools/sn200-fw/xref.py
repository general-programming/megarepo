"""Find call sites targeting an address, and the enclosing `entry` of an address.

    SN200_FW=~/sn200fw python3 xref.py PROC0 7ffaac30       # who calls it
    SN200_FW=~/sn200fw python3 xref.py PROC0 7ffaaf02 --fn  # which function contains it

CALLn encoding (Xtensa): byte0 low 4 bits = 5, n = bits 4..5, 18-bit signed offset in
bits 6..23; target = ((PC & ~3) + 4) + (offset << 2).
"""

import os
import sys
import glob

BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))


def load(sel: str) -> tuple[int, bytes]:
    proc, _, want = sel.partition("@")
    c = sorted(glob.glob(f"{BD}/flat/{proc}_*.bin"))
    if want:
        c = [x for x in c if x.endswith(f"_{want}.bin")]
    if not c:
        sys.exit(f"no image for {sel}")
    return int(os.path.basename(c[0])[:-4].rsplit("_", 1)[1], 16), open(
        c[0], "rb"
    ).read()


def calls(base: int, d: bytes):
    """Yield (pc, n, target) for every CALLn-shaped 3 bytes."""
    for o in range(0, len(d) - 2):
        b0 = d[o]
        if (b0 & 0x0F) != 5:
            continue
        n = (b0 >> 4) & 3
        off = (b0 >> 6) | (d[o + 1] << 2) | (d[o + 2] << 10)
        if off & 0x20000:
            off -= 0x40000
        yield base + o, n, (((base + o) & ~3) + 4 + (off << 2)) & 0xFFFFFFFF


def enclosing(base: int, d: bytes, addr: int) -> int | None:
    """Nearest preceding 4-aligned `entry` (byte 0x36)."""
    a = addr & ~3
    while a > base:
        if d[a - base] == 0x36:
            return a
        a -= 4
    return None


if __name__ == "__main__":
    sel, target = sys.argv[1], int(sys.argv[2], 16)
    base, d = load(sel)
    if "--fn" in sys.argv:
        e = enclosing(base, d, target)
        print(
            f"{target:08x} is inside function entry @ {e:08x}"
            if e
            else "no entry found"
        )
        target = e
    hits = [(pc, n) for pc, n, t in calls(base, d) if t == target]
    print(f"call sites -> {target:08x}: {len(hits)}")
    for pc, n in hits:
        print(f"  {pc:08x}  call{4 * (n + 1)}")
