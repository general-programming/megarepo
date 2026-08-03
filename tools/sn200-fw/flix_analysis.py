#!/usr/bin/env python3
"""Empirically determine the Xtensa FLIX bundle width in SN200 firmware.

Reproduces the evidence in docs/xtensa-flix-decoding.md. Two mutually
independent objective functions are scored against every candidate width:

  1. forward-branch spans -- a conditional branch at X targeting T is ground
     truth, so summing instruction lengths from X to T must land exactly on T.
     Only spans that contain at least one FLIX bundle are informative.
  2. call targets -- a `callN` target should be the address of an `entry`
     prologue. Uses no branch information, so it is fully independent of (1).

Usage:  python3 flix_analysis.py [image_dir]     (default /Users/nep/sn200fw/flat)
"""

import sys
import os
import glob
import collections


def sx(v, b):
    return v - (1 << b) if v & (1 << (b - 1)) else v


class Img:
    def __init__(self, path: str, base: int):
        self.name = os.path.basename(path)[:-4]
        self.d = open(path, "rb").read()
        self.base = base
        self.end = base + len(self.d)

    def has(self, a: int) -> bool:
        return self.base <= a < self.end

    def b(self, a: int) -> int:
        return self.d[a - self.base]

    def w24(self, a: int) -> int:
        o = a - self.base
        return self.d[o] | (self.d[o + 1] << 8) | (self.d[o + 2] << 16)


def ilen(img: Img, pc: int, flixw: int):
    """Length + coarse class of the instruction at pc. Class is one of
    n(ormal) b(ranch) j(ump) c(all) r(eturn) flix bad."""
    if not img.has(pc + 1):
        return None
    op0 = img.b(pc) & 0xF
    if op0 in (0xE, 0xF):
        return (flixw, "flix")
    if 8 <= op0 <= 0xD:
        w = img.b(pc) | (img.b(pc + 1) << 8)
        t = (w >> 4) & 0xF
        r = (w >> 12) & 0xF
        if op0 == 0xD and r == 0xF:
            return (2, "r" if t in (0, 1) else ("bad" if t == 6 else "n"))
        if op0 == 0xD and r != 0:
            return (2, "bad")
        return (2, "n")
    if not img.has(pc + 2):
        return None
    w = img.w24(pc)
    t = (w >> 4) & 0xF
    r = (w >> 12) & 0xF
    op1 = (w >> 16) & 0xF
    if op0 == 0:
        if op1 == 0 and ((w >> 20) & 0xF) == 0 and r == 0:
            m = (t >> 2) & 3
            n = t & 3
            if m == 2:
                return (3, "r" if n in (0, 1) else ("j" if n == 2 else "bad"))
            if m == 3:
                return (3, "c")
            if m == 0:
                return (3, "bad")
        return (3, "n")
    if op0 in (1, 2, 3, 4):
        return (3, "n")
    if op0 == 5:
        return (3, "c")
    if op0 == 6:
        n = (w >> 4) & 3
        m = (w >> 6) & 3
        if n == 0:
            return (3, "j")
        if n in (1, 2):
            return (3, "b")
        return (3, "n" if m == 0 else "b")  # m==0 is `entry`
    if op0 == 7:
        return (3, "b")
    return (3, "bad")


def is_entry(img: Img, a: int) -> bool:
    """`entry a1, imm` -- the canonical windowed-ABI function prologue."""
    return img.has(a + 2) and img.b(a) == 0x36 and (img.b(a + 1) & 0xF) == 1


def find_entries(img: Img) -> list:
    return [a for a in range(img.base, img.end - 4, 4) if is_entry(img, a)]


def branch_target(img: Img, pc: int):
    op0 = img.b(pc) & 0xF
    if not img.has(pc + 2):
        return None
    w = img.w24(pc)
    r = (w >> 12) & 0xF
    imm8 = (w >> 16) & 0xFF
    imm12 = (w >> 12) & 0xFFF
    if op0 == 7:
        return (pc + 4 + sx(imm8, 8)) & 0xFFFFFFFF
    if op0 == 6:
        n = (w >> 4) & 3
        m = (w >> 6) & 3
        if n == 1:
            return (pc + 4 + sx(imm12, 12)) & 0xFFFFFFFF
        if n == 2:
            return (pc + 4 + sx(imm8, 8)) & 0xFFFFFFFF
        if n == 3 and (m in (2, 3) or (m == 1 and r in (0, 1))):
            return (pc + 4 + sx(imm8, 8)) & 0xFFFFFFFF
    return None


def call_target(img: Img, pc: int):
    if img.b(pc) & 0xF != 5:
        return None
    w = img.w24(pc)
    if not ((w >> 4) & 3):
        return None
    return ((pc & ~3) + (sx((w >> 6) & 0x3FFFF, 18) << 2) + 4) & 0xFFFFFFFF


def collect_constraints(img: Img, maxspan: int = 1200) -> list:
    """Forward branches reached from an `entry` anchor through a FLIX-free
    prefix, so the branch address itself is certainly a real boundary."""
    cons = []
    for A in find_entries(img):
        pc = A
        for _ in range(400):
            if not img.has(pc + 2):
                break
            if img.b(pc) & 0xF in (0xE, 0xF):
                break  # confidence ends here
            r = ilen(img, pc, 8)
            if r is None or r[1] == "bad":
                break
            T = branch_target(img, pc)
            if T is not None and pc < T < pc + maxspan and img.has(T):
                cons.append((pc, T))
            if r[1] in ("r", "j"):
                break
            pc += r[0]
    return cons


def test_branch_spans(img: Img, cons: list, flixw: int):
    inf = hit = 0
    for X, T in cons:
        pc = X
        nf = 0
        ok = True
        while pc < T:
            r = ilen(img, pc, flixw)
            if r is None or r[1] == "bad":
                ok = False
                break
            if r[1] == "flix":
                nf += 1
            pc += r[0]
        if not ok or nf == 0:
            continue
        inf += 1
        if pc == T:
            hit += 1
    return inf, hit


def sweep_calls(img: Img, flixw: int, maxsteps: int = 800) -> set:
    tgts = set()
    seen = set()
    work = find_entries(img)[:]
    while work:
        pc = work.pop()
        for _ in range(maxsteps):
            if pc in seen:
                break
            seen.add(pc)
            r = ilen(img, pc, flixw)
            if r is None or r[1] == "bad":
                break
            if r[1] == "c":
                t = call_target(img, pc)
                if t is not None and img.has(t + 2):
                    tgts.add(t)
            if r[1] in ("r", "j"):
                break
            pc += r[0]
    return tgts


def census(img: Img, flixw: int = 8):
    """Byte counts by instruction class, over a recursive-descent sweep."""
    nb = collections.Counter()
    seen = set()
    work = find_entries(img)[:]
    bundles = []
    while work:
        pc = work.pop()
        for _ in range(2000):
            if pc in seen:
                break
            seen.add(pc)
            r = ilen(img, pc, flixw)
            if r is None or r[1] == "bad":
                break
            ln, kind = r
            nb[kind] += ln
            if kind == "flix":
                o = pc - img.base
                bundles.append(int.from_bytes(img.d[o : o + 8], "little"))
            if kind == "c":
                t = call_target(img, pc)
                if t is not None and img.has(t + 2) and t not in seen:
                    work.append(t)
            if kind in ("r", "j"):
                break
            pc += ln
    return nb, bundles


def load_images(d: str) -> list:
    out = []
    for p in sorted(glob.glob(os.path.join(d, "*.bin"))):
        nm = os.path.basename(p)
        base = (
            0x30000000
            if "30000000" in nm
            else (0x7FF80000 if "7ff80000" in nm else 0x00100000)
        )
        out.append(Img(p, base))
    return out


def main():
    d = sys.argv[1] if len(sys.argv) > 1 else "/Users/nep/sn200fw/flat"
    imgs = load_images(d)
    widths = [2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 16, 24, 32]

    print("=== TEST 1: forward-branch spans containing >=1 FLIX bundle ===")
    ti = collections.Counter()
    th = collections.Counter()
    cons = {im.name: collect_constraints(im) for im in imgs}
    for im in imgs:
        for W in widths:
            i, h = test_branch_spans(im, cons[im.name], W)
            ti[W] += i
            th[W] += h
    for W in widths:
        print(
            "  W=%-3d informative=%5d  exact=%5d  (%.4f)%s"
            % (W, ti[W], th[W], th[W] / max(1, ti[W]), "   <== BEST" if W == 8 else "")
        )

    print("\n=== TEST 2 (independent): callN targets landing on `entry` ===")
    tt = collections.Counter()
    hh = collections.Counter()
    for im in imgs:
        for W in widths:
            t = sweep_calls(im, W)
            tt[W] += len(t)
            hh[W] += sum(1 for x in t if is_entry(im, x))
    for W in widths:
        print(
            "  W=%-3d call targets=%5d  on `entry`=%5d  (%.4f)%s"
            % (W, tt[W], hh[W], hh[W] / max(1, tt[W]), "   <== BEST" if W == 8 else "")
        )

    print("\n=== bundle census at W=8 ===")
    tot = collections.Counter()
    allb = []
    for im in imgs:
        nb, bu = census(im)
        tot += nb
        allb += bu
        t = sum(nb.values())
        print(
            "  %-18s decoded=%7dB  flix=%6dB (%4.1f%%)  bundles=%5d"
            % (im.name, t, nb["flix"], 100 * nb["flix"] / max(1, t), len(bu))
        )
    t = sum(tot.values())
    print(
        "  TOTAL decoded=%dB  flix=%dB (%.1f%%)"
        % (t, tot["flix"], 100 * tot["flix"] / t)
    )

    for fmt in (0xE, 0xF):
        vs = [v for v in allb if v & 0xF == fmt]
        if not vs:
            continue
        c = collections.Counter((v >> 48) & 0xFFFF for v in vs)
        val, cnt = c.most_common(1)[0]
        print(
            "  op0=0x%x: %6d bundles; most common bits48-63 = 0x%04x (%.1f%%)"
            % (fmt, len(vs), val, 100 * cnt / len(vs))
        )


if __name__ == "__main__":
    main()
