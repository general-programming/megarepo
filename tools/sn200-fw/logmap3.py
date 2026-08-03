import struct
import sys
import re
import os

# Root of the unpacked SN200 firmware working tree. Override with $SN200_FW.
# Expected layout:  $SN200_FW/fw/KNGND122/StringTable.csv
#                   $SN200_FW/flat/PROC8_7ff80000.bin
#                   $SN200_FW/flat/PROC8_30000000.bin
# Build it with:  segparse.py (container) then the flat-image loop in the notes.
BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
lines = (
    open(BD + "/fw/KNGND122/StringTable.csv", "rb").read().decode("latin1").split("\n")
)
MAXID = len(lines)
IM = {
    0x7FF80000: open(BD + "/flat/PROC8_7ff80000.bin", "rb").read(),
    0x30000000: open(BD + "/flat/PROC8_30000000.bin", "rb").read(),
}


def boundaries(d):
    o = 0
    B = set()
    while o < len(d) - 8:
        B.add(o)
        op0 = d[o] & 0xF
        o += 2 if 8 <= op0 <= 0xD else (8 if op0 >= 0xE else 3)
    return B


ALL = {}
for base, d in IM.items():
    B = boundaries(d)
    D = {}
    for o in range(0, len(d) - 3, 4):
        w = struct.unpack_from("<I", d, o)[0]
        if 0 < (w >> 16) < MAXID and (w & 0xFF) <= 12:
            D[base + o] = w
    for o in sorted(B):
        if o + 3 > len(d):
            break
        op0 = d[o] & 0xF
        if op0 == 1:
            reg = "a%d" % (d[o] >> 4)
            ok = True
        elif op0 in (0xE, 0xF) and (d[o] >> 4) == 9:
            reg = "FLIX%x.s0" % op0
            ok = True
        else:
            ok = False
        if not ok:
            continue
        imm = d[o + 1] | (d[o + 2] << 8)
        pc = base + o
        t = (((pc + 3) & ~3) + (imm << 2) - 0x40000) & 0xFFFFFFFF
        if t in D:
            ALL.setdefault(t, []).append((pc, reg))
    ALL["D_" + hex(base)] = D


def q(pred):
    for base in IM:
        for a, w in sorted(ALL["D_" + hex(base)].items()):
            sid = w >> 16
            s = lines[sid] if sid < MAXID else "?"
            if not pred(sid, s):
                continue
            r = ALL.get(a, [])
            print(
                "%08x w=%08x id=%-5d na=%d lvl=%02x refs=%s | %s"
                % (
                    a,
                    w,
                    sid,
                    w & 0xFF,
                    (w >> 8) & 0xFF,
                    ",".join("%08x/%s" % x for x in r),
                    s[:110],
                )
            )


if __name__ == "__main__":
    a = sys.argv[1]
    if a.isdigit():
        q(lambda i, s: i == int(a))
    else:
        q(lambda i, s: re.search(a, s, re.I) is not None)
