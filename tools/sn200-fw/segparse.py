import struct
import sys
import os


def parse(d):
    off = 0x10
    segs = []
    while off + 0x10 <= len(d) and d[off : off + 4] == b".SEG":
        _, do, dl, la = struct.unpack("<4sIII", d[off : off + 0x10])
        segs.append((off, do, dl, la, d[do : do + dl]))
        off = do + dl
    return segs, off


if __name__ == "__main__":
    for p in sys.argv[1:]:
        d = open(p, "rb").read()
        segs, end = parse(d)
        print(f"== {os.path.basename(p)} size={len(d)} nseg={len(segs)} end={end:#x}")
        for o, do, dl, la, data in segs:
            print(
                f"  seg@{o:#08x} data={do:#08x} len={dl:#010x} load={la:#010x}-{la + dl:#010x}"
            )
