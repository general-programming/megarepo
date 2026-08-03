import sys
import struct
import os

# Root of the unpacked SN200 firmware working tree. Override with $SN200_FW.
# Expected layout:  $SN200_FW/fw/KNGND122/StringTable.csv
#                   $SN200_FW/flat/PROC8_7ff80000.bin
#                   $SN200_FW/flat/PROC8_30000000.bin
# Build it with:  segparse.py (container) then the flat-image loop in the notes.
BD = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from xdis import run  # noqa: E402  -- needs sys.path set above

lines = (
    open(BD + "/fw/KNGND122/StringTable.csv", "rb").read().decode("latin1").split("\n")
)
IM = {
    0x7FF80000: open(BD + "/flat/PROC8_7ff80000.bin", "rb").read(),
    0x30000000: open(BD + "/flat/PROC8_30000000.bin", "rb").read(),
}
start = int(sys.argv[1], 16)
end = int(sys.argv[2], 16)
base = 0x30000000 if start >= 0x30000000 and start < 0x40000000 else 0x7FF80000
d = IM[base]
strs = {}
for b, dd in IM.items():
    for o in range(0, len(dd) - 3, 4):
        w = struct.unpack_from("<I", dd, o)[0]
        sid = w >> 16
        if (
            0 < sid < len(lines)
            and (w & 0xFF) <= 12
            and ((w >> 8) & 0xFF)
            in (0x00, 0x20, 0x40, 0x60, 0x80, 0xA0, 0xC0, 0xE0, 0x10, 0x30, 0x50, 0x70)
        ):
            strs[b + o] = 'LOG id=%d na=%d "%s"' % (sid, w & 0xFF, lines[sid][:70])
        elif 0x7FF80000 <= w < 0x7FFC0000 or 0x30000000 <= w < 0x30100000:
            strs.setdefault(b + o, "-> %08x" % w)
        else:
            strs.setdefault(b + o, "= 0x%08x (%d)" % (w, w))
print(run(d, base, start, end, strs))
