"""Find, across ALL processor images, the log descriptors for a StrId or regex,
and every l32r that loads them.

    SN200_FW=~/sn200fw python3 logscan.py 'UNEXSTRT'
    SN200_FW=~/sn200fw python3 logscan.py 3520
"""

import os
import sys
import re
import struct
import glob

BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
REV = os.environ.get("SN200_REV", "KNGND122")
lines = open(f"{BD}/fw/{REV}/StringTable.csv", "rb").read().decode("latin1").split("\n")
MAXID = len(lines)


def images() -> dict[str, tuple[int, bytes]]:
    out = {}
    for p in sorted(glob.glob(f"{BD}/flat/*.bin")):
        b = os.path.basename(p)[:-4]
        name, base = b.rsplit("_", 1)
        out[b] = (int(base, 16), open(p, "rb").read())
    return out


def boundaries(d: bytes):
    """Instruction starts under the FLIX model: op0 8..d -> 2 bytes, e/f -> 8, else 3."""
    o = 0
    while o < len(d) - 8:
        yield o
        op0 = d[o] & 0xF
        o += 2 if 8 <= op0 <= 0xD else (8 if op0 >= 0xE else 3)


def scan(pred):
    for name, (base, d) in images().items():
        desc = {}
        for o in range(0, len(d) - 3, 4):
            w = struct.unpack_from("<I", d, o)[0]
            sid = w >> 16
            if (
                0 < sid < MAXID
                and (w & 0xFF) <= 12
                and ((w >> 8) & 0x0F) == 0
                and pred(sid, lines[sid])
            ):
                desc[base + o] = w
        if not desc:
            continue
        refs = {}
        for o in boundaries(d):
            if o + 3 > len(d):
                break
            op0 = d[o] & 0xF
            if op0 == 1:
                who = "l32r a%d" % (d[o] >> 4)
            elif op0 in (0xE, 0xF) and (d[o + 3] & 0xF) == 1:
                # FLIX slot A is a core instruction with its op0 field at bits 24-27;
                # op0 == 1 is l32r, dest register in the high nibble of byte 0.
                who = "FLIX.slotA l32r a%d" % (d[o] >> 4)
            else:
                continue
            imm = d[o + 1] | (d[o + 2] << 8)
            pc = base + o
            t = (((pc + 3) & ~3) + (imm << 2) - 0x40000) & 0xFFFFFFFF
            if t in desc:
                refs.setdefault(t, []).append((pc, who))
        for a, w in sorted(desc.items()):
            sid = w >> 16
            r = refs.get(a, [])
            print(
                "%-22s lit=%08x w=%08x id=%-5d na=%d lvl=%02x"
                % (name, a, w, sid, w & 0xFF, (w >> 8) & 0xFF)
            )
            print("    %s" % lines[sid][:110])
            for pc, who in r:
                print("    <- %08x  %s" % (pc, who))
            if not r:
                print("    <- (no l32r reference found in this image)")


if __name__ == "__main__":
    a = sys.argv[1]
    if a.isdigit():
        scan(lambda i, s: i == int(a))
    else:
        scan(lambda i, s: re.search(a, s, re.I) is not None)
