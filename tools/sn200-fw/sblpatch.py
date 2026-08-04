"""Decode SBLPATCH.bin -- the SN200 bootloader patch container.

SBLPATCH.bin is **two independent .BIN/.SEG chains interleaved in 0x100-byte
blocks**. Block index parity selects the stream: even blocks (starting with the
0x100-byte PMCSEEPM001 header and the boot register script) carry the SBL,
odd blocks carry the MBL. Each stream is an ordinary .BIN/.SEG chain whose
data offsets are relative to its own `.BIN` header, in *stream* coordinates.

    python3 sblpatch.py --list  [FILE]
    python3 sblpatch.py --extract OUTDIR [FILE]     # writes SBL_*.bin / MBL_*.bin
    python3 sblpatch.py --script [FILE]             # boot register script

`--extract` writes flat images named like the ones unpack.py produces, so
disany.py picks them up:  SN200_FW=... python3 disany.py SBL 7ffb6000 7ffb6100
"""

import os
import re
import struct
import sys

BLOCK = 0x100
DEFAULT = os.path.expanduser("~/sn200fw/fw/KNGND110/SBLPATCH.bin")


def deinterleave(d: bytes) -> tuple[bytes, bytes]:
    """Split into (even, odd) block streams. even -> SBL, odd -> MBL."""
    blocks = [d[i : i + BLOCK] for i in range(0, len(d), BLOCK)]
    return (
        b"".join(blocks[0::2]),
        b"".join(blocks[1::2]),
    )


def chain(s: bytes) -> tuple[int, list[tuple[int, int, int, bytes]]]:
    """Parse the .BIN/.SEG chain in one stream. Returns (bin_off, segments)."""
    m = re.search(rb"\.BIN", s)
    if not m:
        return -1, []
    b = m.start()
    segs = []
    off = b + 0x10
    while off + 0x10 <= len(s) and s[off : off + 4] == b".SEG":
        _, do, dl, la = struct.unpack("<4sIII", s[off : off + 0x10])
        if do == 0xFFFFFFFF or dl == 0:
            break
        segs.append((do, dl, la, s[b + do : b + do + dl]))
        off = b + do + dl
    return b, segs


def script(d: bytes):
    """Yield boot register-script records from the header block.

    16 bytes each: {op, addr, mask, value}. op 0x1004 = write,
    0x1003 = read-modify-write.
    """
    off = 0x30
    while off + 0x10 <= BLOCK * 2:
        op, addr, mask, val = struct.unpack("<IIII", d[off : off + 0x10])
        if op not in (0x1003, 0x1004):
            break
        yield off, op, addr, mask, val
        off += 0x10
        if off % BLOCK == 0:  # script continues in the next EVEN block
            off += BLOCK


def streams(path: str) -> dict[str, tuple[int, list]]:
    d = open(path, "rb").read()
    ev, od = deinterleave(d)
    return {"SBL": (ev, chain(ev)), "MBL": (od, chain(od))}


def main() -> None:
    args = sys.argv[1:]
    if not args:
        sys.exit(__doc__)
    mode = args[0]
    if mode == "--extract":
        outdir, path = args[1], (args[2] if len(args) > 2 else DEFAULT)
    else:
        outdir, path = None, (args[1] if len(args) > 1 else DEFAULT)

    if mode == "--script":
        d = open(path, "rb").read()
        for off, op, addr, mask, val in script(d):
            kind = "write" if op == 0x1004 else "rmw  "
            print(f"{off:#06x}  {kind} {addr:#010x} mask={mask:#010x} val={val:#010x}")
        return

    for name, (s, (b, segs)) in streams(path).items():
        print(f"== {name}  stream={len(s):#x} .BIN@{b:#x} nseg={len(segs)}")
        for do, dl, la, _ in segs:
            print(f"   .SEG do={do:#08x} len={dl:#08x} load={la:#010x}-{la + dl:#010x}")
        if mode == "--extract":
            os.makedirs(outdir, exist_ok=True)
            groups: dict[int, list] = {}
            for _, _, la, data in segs:
                groups.setdefault(la >> 20, []).append((la, data))
            for items in groups.values():
                lo = min(a for a, _ in items)
                hi = max(a + len(x) for a, x in items)
                buf = bytearray(hi - lo)
                for a, x in items:
                    buf[a - lo : a - lo + len(x)] = x
                out = os.path.join(outdir, f"{name}_{lo:08x}.bin")
                open(out, "wb").write(buf)
                print(f"   -> {out}  {lo:#010x}+{hi - lo:#x}")


if __name__ == "__main__":
    main()
