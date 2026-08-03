"""Unpack an SN200/Omaha firmware image into per-processor flat memory images.

    python3 unpack.py KNGND122.bin OUTDIR

Produces OUTDIR/fw/<rev>/*(the tar members, StringTable.csv gunzipped) and
OUTDIR/flat/PROC<N>_<basehex>.bin — raw bytes with inter-segment gaps zero-filled,
loaded at the base encoded in the filename. Point $SN200_FW at OUTDIR for drv.py
and logmap3.py.
"""

import sys
import os
import gzip
import glob
import re
import tarfile
from segparse import parse


def flatten(path: str, outdir: str) -> None:
    d = open(path, "rb").read()
    segs = [s for s in parse(d)[0] if s[2] > 0]
    groups: dict[int, list[tuple[int, bytes]]] = {}
    for _o, _do, _dl, la, data in segs:
        groups.setdefault(la >> 28, []).append((la, data))
    name = os.path.basename(path).replace(".bin", "")
    for items in groups.values():
        lo = min(a for a, _ in items)
        hi = max(a + len(x) for a, x in items)
        buf = bytearray(hi - lo)
        for a, x in items:
            buf[a - lo : a - lo + len(x)] = x
        out = os.path.join(outdir, "flat", f"{name}_{lo:08x}.bin")
        open(out, "wb").write(buf)
        print(f"{name} base={lo:#010x} size={hi - lo:#x} -> {out}")


def main() -> None:
    image, outdir = sys.argv[1], sys.argv[2]
    rev = os.path.basename(image).replace(".bin", "")
    fwdir = os.path.join(outdir, "fw", rev)
    os.makedirs(fwdir, exist_ok=True)
    os.makedirs(os.path.join(outdir, "flat"), exist_ok=True)
    with tarfile.open(image) as t:
        t.extractall(fwdir, filter="data")
    st = os.path.join(fwdir, "StringTable.csv.gz")
    if os.path.exists(st):
        open(st[:-3], "wb").write(gzip.open(st, "rb").read())
        print(
            "StringTable.csv:", sum(1 for _ in open(st[:-3], errors="replace")), "lines"
        )

    def order(x: str) -> tuple[int, int]:
        b = os.path.basename(x)
        n = re.findall(r"\d+", b)
        return (0, int(n[0])) if b.startswith("PROC") else (1, 0)

    for p in sorted(
        glob.glob(os.path.join(fwdir, "PROC*.bin"))
        + glob.glob(os.path.join(fwdir, "FCC.bin")),
        key=order,
    ):
        flatten(p, outdir)


if __name__ == "__main__":
    main()
