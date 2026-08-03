"""Disassemble a range of any SN200 processor image, annotating l32r literals.

SN200_FW=~/sn200fw python3 disany.py PROC0 7ffaae00 7ffab000
SN200_FW=~/sn200fw python3 disany.py PROC8@30000000 300336c6 300336f5
"""

import os
import sys
import struct
import glob

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from xdis import run

BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
REV = os.environ.get("SN200_REV", "KNGND122")
lines = open(f"{BD}/fw/{REV}/StringTable.csv", "rb").read().decode("latin1").split("\n")


def load(sel: str) -> tuple[int, bytes]:
    proc, _, want = sel.partition("@")
    cands = sorted(glob.glob(f"{BD}/flat/{proc}_*.bin"))
    if want:
        cands = [c for c in cands if c.endswith(f"_{want}.bin")]
    if not cands:
        sys.exit(f"no image for {sel}")
    p = cands[0]
    return int(os.path.basename(p)[:-4].rsplit("_", 1)[1], 16), open(p, "rb").read()


def annotate(imgs: list[tuple[int, bytes]]) -> dict[int, str]:
    ann = {}
    for base, d in imgs:
        for o in range(0, len(d) - 3, 4):
            w = struct.unpack_from("<I", d, o)[0]
            sid = w >> 16
            if 0 < sid < len(lines) and (w & 0xFF) <= 12 and ((w >> 8) & 0x0F) == 0:
                ann[base + o] = 'LOG id=%d na=%d "%s"' % (
                    sid,
                    w & 0xFF,
                    lines[sid][:70],
                )
            elif 0x7FF80000 <= w < 0x7FFC0000 or 0x30000000 <= w < 0x30100000:
                ann.setdefault(base + o, "-> %08x" % w)
            else:
                ann.setdefault(base + o, "= 0x%08x (%d)" % (w, w))
    return ann


if __name__ == "__main__":
    sel, start, end = sys.argv[1], int(sys.argv[2], 16), int(sys.argv[3], 16)
    base, d = load(sel)
    others = [load(x) for x in (os.environ.get("SN200_ALSO") or "").split() if x]
    print(run(d, base, start, end, annotate([(base, d)] + others)))
