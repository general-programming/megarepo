"""Lift SN200 Xtensa/FLIX firmware to Ghidra p-code with pypcode.

The point is verification by execution rather than by reading prose: every
claim about the boot/latch path becomes something a test can re-derive from
the instruction stream.

The SLEIGH spec is Ghidra's stock Xtensa plus our FLIX extension
(``ghidra/languages/flix.sinc``), compiled with the ``sleigh`` binary that
ships inside pypcode so the ``.sla`` format always matches the lifter.

    SN200_FW=~/sn200fw python3 pcode.py PROC8@30000000 300335a3 300335c0
    SN200_FW=~/sn200fw python3 pcode.py --pcode PROC0 7ffaae69 7ffaae90
    SN200_FW=~/sn200fw python3 pcode.py --overlay 22 PROC8 7ffbc110 7ffbc130
"""

from __future__ import annotations

import argparse
import glob
import hashlib
import os
import shutil
import struct
import subprocess
import sys
from dataclasses import dataclass, field
from functools import lru_cache

from pypcode import Arch, BadDataError, Context, OpCode, PcodeOp, PcodePrettyPrinter

HERE = os.path.dirname(os.path.abspath(__file__))
FW_DIR = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
LANG_ID = "Xtensa:LE:32:default"

# Every overlay is copied to the same runtime window, so a 0x7ffbxxxx address
# only names code once you say which overlay is resident.
OVERLAY_WINDOW = 0x7FFBC000
# Descriptor records live in PROC8's low image: 0x20 apart, (dst, len, src2)
# at +0x00/+0x04/+0x08.  Overlay numbering in the docs is 1-based.
OVERLAY_TABLE = 0x7FF81AF4
OVERLAY_STRIDE = 0x20
OVERLAY_MAX = 40

# p-code ops our FLIX spec emits when a slot is not understood.  Anything that
# depends on one of these is NOT proven by the lifter.
OPAQUE_PCODEOPS = (
    "flix_slotA_unknown",
    "flix_slotB_unknown",
    "flix_slotB_unknown_alu",
    "flix_slotC_unknown",
    "flix_unknown",
)


# --------------------------------------------------------------------------
# SLEIGH build
# --------------------------------------------------------------------------


def _pypcode_dir() -> str:
    import pypcode

    return os.path.dirname(os.path.abspath(pypcode.__file__))


def _spec_inputs() -> tuple[str, str]:
    """(stock Xtensa language dir shipped with pypcode, our flix.sinc)."""
    stock = os.path.join(_pypcode_dir(), "processors", "Xtensa", "data", "languages")
    return stock, os.path.join(HERE, "ghidra", "languages", "flix.sinc")


def _build_stamp() -> str:
    stock, flix = _spec_inputs()
    h = hashlib.sha256()
    for p in sorted(glob.glob(os.path.join(stock, "*"))) + [flix]:
        if os.path.basename(p).endswith(".sla"):
            continue
        h.update(os.path.basename(p).encode())
        with open(p, "rb") as f:
            h.update(f.read())
    return h.hexdigest()[:16]


def build_sla(force: bool = False) -> str:
    """Compile the FLIX-extended Xtensa spec; return the .ldefs path.

    Cached under ``ghidra/build/`` and keyed on a hash of every spec input, so
    an edit to flix.sinc rebuilds automatically.
    """
    stock, flix = _spec_inputs()
    out = os.path.join(HERE, "ghidra", "build", "Xtensa", "data", "languages")
    ldefs = os.path.join(out, "xtensa.ldefs")
    stampfile = os.path.join(out, ".stamp")
    want = _build_stamp()
    if (
        not force
        and os.path.exists(ldefs)
        and os.path.exists(os.path.join(out, "xtensa_le.sla"))
    ):
        try:
            with open(stampfile) as f:
                if f.read().strip() == want:
                    return ldefs
        except OSError:
            pass

    sleigh = os.path.join(_pypcode_dir(), "bin", "sleigh")
    if not os.path.exists(sleigh):
        raise RuntimeError(f"pypcode ships no sleigh compiler at {sleigh}")
    shutil.rmtree(out, ignore_errors=True)
    os.makedirs(out)
    for p in glob.glob(os.path.join(stock, "*")):
        if p.endswith(".sla"):
            continue
        shutil.copy(p, out)
    shutil.copy(flix, os.path.join(out, "flix.sinc"))
    r = subprocess.run([sleigh, "-a", out], capture_output=True, text=True)
    if r.returncode != 0 or not os.path.exists(os.path.join(out, "xtensa_le.sla")):
        raise RuntimeError(f"sleigh failed:\n{r.stdout}\n{r.stderr}")
    with open(stampfile, "w") as f:
        f.write(want)
    return ldefs


@lru_cache(maxsize=1)
def context() -> Context:
    ldefs = build_sla()
    arch = Arch("Xtensa", ldefs)
    lang = next(x for x in arch.languages if x.id == LANG_ID)
    return Context(lang)


# --------------------------------------------------------------------------
# images
# --------------------------------------------------------------------------


@dataclass
class Image:
    """One processor's flat images, plus optionally a resident overlay."""

    proc: str
    segs: list[tuple[int, bytes]] = field(default_factory=list)
    overlay: int | None = None

    @classmethod
    def load(cls, proc: str, overlay: int | None = None) -> "Image":
        name, _, want = proc.partition("@")
        paths = sorted(glob.glob(os.path.join(FW_DIR, "flat", f"{name}_*.bin")))
        if want:
            paths = [p for p in paths if p.endswith(f"_{want}.bin")]
        if not paths:
            raise FileNotFoundError(f"no image for {proc} under {FW_DIR}/flat")
        segs = []
        for p in paths:
            base = int(os.path.basename(p)[:-4].rsplit("_", 1)[1], 16)
            with open(p, "rb") as f:
                segs.append((base, f.read()))
        img = cls(proc=name, segs=segs)
        if overlay is not None:
            img.overlay = overlay
        return img

    # -- overlays ----------------------------------------------------------
    def overlay_descriptors(self) -> dict[int, tuple[int, int, int]]:
        """{1-based overlay number: (dst, len, src2)} read from the image."""
        out = {}
        for i in range(OVERLAY_MAX):
            try:
                raw = self._raw(OVERLAY_TABLE + i * OVERLAY_STRIDE, 12)
            except KeyError:
                break
            dst, ln, src2 = struct.unpack("<3I", raw)
            if dst != OVERLAY_WINDOW or not ln or not src2:
                continue
            out[i + 1] = (dst, ln, src2)
        return out

    def overlay_delta(self, n: int) -> int:
        """runtime = static + delta for overlay n."""
        dst, _, src2 = self.overlay_descriptors()[n]
        return dst - src2

    def to_static(self, addr: int) -> int:
        if self.overlay is None:
            return addr
        return addr - self.overlay_delta(self.overlay)

    # -- reads -------------------------------------------------------------
    def _raw(self, addr: int, n: int) -> bytes:
        for base, data in self.segs:
            if base <= addr and addr + n <= base + len(data):
                return data[addr - base : addr - base + n]
        raise KeyError(f"{addr:#x}+{n} not mapped in {self.proc}")

    def read(self, addr: int, n: int) -> bytes:
        """Read n bytes at a *runtime* address, honouring the resident overlay."""
        dst, ln = OVERLAY_WINDOW, 0
        if self.overlay is not None:
            dst, ln, _ = self.overlay_descriptors()[self.overlay]
            if dst <= addr < dst + ln:
                return self._raw(self.to_static(addr), n)
        return self._raw(addr, n)

    def word(self, addr: int) -> int:
        return struct.unpack("<I", self.read(addr, 4))[0]


def overlay_for(image: Image, runtime_addr: int) -> list[int]:
    """Overlays whose window covers runtime_addr (ambiguous by construction)."""
    return [
        n
        for n, (dst, ln, _) in image.overlay_descriptors().items()
        if dst <= runtime_addr < dst + ln
    ]


# --------------------------------------------------------------------------
# lifting
# --------------------------------------------------------------------------

# Longest FLIX bundle we ever emit is 8 bytes; give SLEIGH slack so it never
# truncates the tail of an instruction (a short buffer silently mis-lifts
# pc-relative loads -- see docs/sn200-pcode-toolchain.md).
LIFT_SLACK = 32


@dataclass
class Insn:
    addr: int
    length: int
    mnem: str
    body: str
    ops: list[PcodeOp]

    @property
    def text(self) -> str:
        return f"{self.mnem} {self.body}".strip()

    def pcode(self) -> list[str]:
        return [PcodePrettyPrinter.fmt_op(o) for o in self.ops]

    def opaque(self) -> list[str]:
        """Names of unresolved FLIX slots this instruction lifted to."""
        out = []
        for o in self.ops:
            if o.opcode == OpCode.CALLOTHER and o.inputs:
                nm = o.inputs[0].getUserDefinedOpName()
                if nm in OPAQUE_PCODEOPS:
                    out.append(nm)
        return out


def lift(image: Image, addr: int, count: int = 1) -> list[Insn]:
    """Lift `count` instructions starting at runtime address `addr`."""
    ctx = context()
    out: list[Insn] = []
    pc = addr
    for _ in range(count):
        buf = image.read(pc, LIFT_SLACK)
        dz = ctx.disassemble(buf, pc)
        if not dz.instructions:
            raise ValueError(f"no instruction at {pc:#x}")
        i = dz.instructions[0]
        tr = ctx.translate(buf[: i.length], pc)
        out.append(
            Insn(
                pc,
                i.length,
                i.mnem,
                i.body,
                [o for o in tr.ops if o.opcode != OpCode.IMARK],
            )
        )
        pc += i.length
    return out


def lift_range(image: Image, start: int, end: int) -> list[Insn]:
    out = []
    pc = start
    while pc < end:
        i = lift(image, pc)[0]
        out.append(i)
        pc += i.length
    return out


# --------------------------------------------------------------------------
# control flow over p-code
# --------------------------------------------------------------------------

_FLOW = {
    OpCode.BRANCH,
    OpCode.CBRANCH,
    OpCode.BRANCHIND,
    OpCode.CALL,
    OpCode.CALLIND,
    OpCode.RETURN,
}


def flow(insn: Insn) -> tuple[list[int], list[int], bool, bool]:
    """(branch targets, call targets, terminates, has indirect flow)."""
    branches, calls = [], []
    term = False
    indirect = False
    for o in insn.ops:
        if o.opcode in (OpCode.BRANCH, OpCode.CBRANCH):
            v = o.inputs[0]
            if v.space.name == "ram":
                branches.append(v.offset)
            else:
                indirect = True
            if o.opcode == OpCode.BRANCH:
                term = True
        elif o.opcode == OpCode.CALL:
            v = o.inputs[0]
            if v.space.name == "ram":
                calls.append(v.offset)
        elif o.opcode in (OpCode.BRANCHIND, OpCode.CALLIND):
            indirect = True
            if o.opcode == OpCode.BRANCHIND:
                term = True
        elif o.opcode == OpCode.RETURN:
            term = True
    return branches, calls, term, indirect


@dataclass
class Trace:
    """Everything statically reachable from a start address, no recursion."""

    start: int
    insns: dict[int, Insn] = field(default_factory=dict)
    calls: set[int] = field(default_factory=set)
    indirect: set[int] = field(default_factory=set)
    opaque: dict[int, list[str]] = field(default_factory=dict)
    truncated: set[int] = field(default_factory=set)

    def reaches(self, addr: int) -> bool:
        return addr in self.insns

    def calls_any(self, targets) -> set[int]:
        return self.calls & set(targets)

    def literals(self) -> dict[int, int]:
        """{instruction addr: literal address} for every l32r reached."""
        out = {}
        for a, i in self.insns.items():
            for o in i.ops:
                if o.opcode == OpCode.LOAD or (
                    o.opcode == OpCode.COPY and o.inputs[0].space.name == "ram"
                ):
                    v = o.inputs[-1]
                    if v.space.name == "ram":
                        out[a] = v.offset
        return out


def walk(
    image: Image, start: int, limit: int = 4000, stop_at: set[int] | None = None
) -> Trace:
    """Intraprocedural sweep: follow both arms of every branch, do not enter calls."""
    t = Trace(start=start)
    todo = [start]
    stop_at = stop_at or set()
    while todo:
        pc = todo.pop()
        while True:
            if pc in t.insns or pc in stop_at or len(t.insns) >= limit:
                break
            try:
                i = lift(image, pc)[0]
            except (KeyError, ValueError, BadDataError):
                t.truncated.add(pc)
                break
            t.insns[pc] = i
            if i.opaque():
                t.opaque[pc] = i.opaque()
            br, ca, term, ind = flow(i)
            t.calls.update(ca)
            if ind:
                t.indirect.add(pc)
            todo.extend(br)
            if term:
                break
            pc += i.length
    return t


# --------------------------------------------------------------------------
# p-code execution
# --------------------------------------------------------------------------


class Opaque(Exception):
    """Execution hit a slot the SLEIGH spec does not decode.

    Never swallow this into a boolean answer -- an undecoded slot means the
    lifter does not know what the hardware does, which is a different result
    from "it does not happen".
    """


class Halt(Exception):
    """Execution stopped for a reason the caller asked for (limit, ret, stop)."""


# CALLOTHER pseudo-ops that are genuinely irrelevant to data flow: window
# rotation and memory ordering. Executing past them is safe *for the
# register-constant reasoning we do*, not in general.
BENIGN_CALLOTHER = {
    "rotateRegWindow",
    "restoreRegWindow",
    "swap4",
    "swap8",
    "swap12",
    "restore4",
    "restore8",
    "restore12",
}


class Emu:
    """A tiny p-code interpreter over one image.

    Not a CPU emulator: it executes exactly the p-code our SLEIGH spec emits,
    over a flat memory that starts out as the firmware image itself, so
    globals and literal pools read their real values. Calls are not entered
    unless the caller asks; an undecoded FLIX slot raises `Opaque`.
    """

    def __init__(
        self, image: Image, follow_calls: bool = False, on_opaque: str = "raise"
    ):
        self.img = image
        self.follow_calls = follow_calls
        # "raise": refuse to guess (default). "skip": step over the undecoded
        # slot and record it, so the caller can judge whether it could have
        # affected the answer. Never let a "skip" result be reported as PROVEN.
        self.on_opaque = on_opaque
        self.opaque: list[tuple[int, str]] = []
        self.regs: dict[tuple[int, int], int] = {}
        self.mem: dict[int, int] = {}  # byte-addressed overrides
        self.uniq: dict[int, int] = {}
        self.pc = 0
        self.trace: list[int] = []
        self.calls: list[int] = []
        self.stores: list[tuple[int, int, int]] = []  # (addr, size, value)
        self.retstack: list[int] = []
        self.stop_on_call = False

    # -- storage -----------------------------------------------------------
    def _rd_mem(self, addr: int, size: int) -> int:
        out = 0
        for i in range(size):
            b = self.mem.get(addr + i)
            if b is None:
                try:
                    b = self.img.read(addr + i, 1)[0]
                except KeyError:
                    b = 0
            out |= b << (8 * i)
        return out

    def _wr_mem(self, addr: int, size: int, val: int) -> None:
        for i in range(size):
            self.mem[addr + i] = (val >> (8 * i)) & 0xFF

    def poke(self, addr: int, val: int, size: int = 4) -> None:
        self._wr_mem(addr, size, val)

    def peek(self, addr: int, size: int = 4) -> int:
        return self._rd_mem(addr, size)

    def _reg_name_off(self, name: str) -> int:
        # a0..a15 live at register-space offsets 0x00..0x3c.
        return 4 * int(name[1:])

    def setreg(self, name: str, val: int) -> None:
        self.regs[(self._reg_name_off(name), 4)] = val & 0xFFFFFFFF

    def getreg(self, name: str) -> int:
        return self.regs.get((self._reg_name_off(name), 4), 0)

    def _read(self, v) -> int:
        sp = v.space.name
        if sp == "const":
            return v.offset
        if sp == "unique":
            return self.uniq.get(v.offset, 0) & ((1 << (8 * v.size)) - 1)
        if sp == "register":
            return self.regs.get((v.offset, v.size), 0) & ((1 << (8 * v.size)) - 1)
        if sp == "ram":
            return self._rd_mem(v.offset, v.size)
        raise Opaque(f"read from space {sp}")

    def _write(self, v, val: int) -> None:
        val &= (1 << (8 * v.size)) - 1
        sp = v.space.name
        if sp == "unique":
            self.uniq[v.offset] = val
        elif sp == "register":
            self.regs[(v.offset, v.size)] = val
        elif sp == "ram":
            self._wr_mem(v.offset, v.size, val)
        else:
            raise Opaque(f"write to space {sp}")

    # -- execution ---------------------------------------------------------
    def run(
        self, start: int, stop_at=(), max_steps: int = 20000, stop_on_call=False
    ) -> int:
        """Execute from `start`; return the pc where it stopped.

        `stop_on_call` is True to stop at the first call of any kind, or a
        collection of target addresses to stop only at those (calls to
        anything else are stepped over without entering them).
        """
        self.stop_on_call = stop_on_call
        self.pc = start
        stop = set(stop_at)
        steps = 0
        while True:
            if self.pc in stop:
                return self.pc
            steps += 1
            if steps > max_steps:
                raise Halt(f"step limit at {self.pc:#x}")
            self.trace.append(self.pc)
            insn = lift(self.img, self.pc)[0]
            nxt = self.pc + insn.length
            try:
                nxt = self._exec(insn, nxt, stop)
            except _Ret:
                if not self.retstack:
                    return self.pc
                nxt = self.retstack.pop()
            if nxt is None:
                return self.pc
            self.pc = nxt

    def _exec(self, insn: Insn, fallthrough: int, stop: set) -> int | None:
        ops = insn.ops
        i = 0
        while i < len(ops):
            o = ops[i]
            oc = o.opcode
            i += 1
            if oc == OpCode.CALLOTHER:
                nm = o.inputs[0].getUserDefinedOpName()
                if nm in OPAQUE_PCODEOPS:
                    if self.on_opaque != "skip":
                        raise Opaque(f"{nm} at {insn.addr:#x}: {insn.text}")
                    self.opaque.append((insn.addr, insn.text))
                    continue
                if nm not in BENIGN_CALLOTHER:
                    raise Opaque(f"pseudo-op {nm} at {insn.addr:#x}")
                continue
            if oc == OpCode.BRANCH:
                v = o.inputs[0]
                if v.space.name == "const":  # p-code-relative
                    i += _signed(v.offset, 8) - 1
                    continue
                return v.offset
            if oc == OpCode.CBRANCH:
                cond = self._read(o.inputs[1])
                v = o.inputs[0]
                if v.space.name == "const":
                    if cond:
                        i += _signed(v.offset, 8) - 1
                    continue
                if cond:
                    return v.offset
                continue
            if oc == OpCode.BRANCHIND:
                return self._read(o.inputs[0])
            if oc in (OpCode.CALL, OpCode.CALLIND):
                tgt = (
                    o.inputs[0].offset if oc == OpCode.CALL else self._read(o.inputs[0])
                )
                self.calls.append(tgt)
                soc = self.stop_on_call
                if soc is True or (soc and soc is not False and tgt in soc):
                    return None
                if self.follow_calls and tgt not in stop:
                    self.retstack.append(fallthrough)
                    return tgt
                continue
            if oc == OpCode.RETURN:
                raise _Ret()
            self._alu(o)
        return fallthrough

    def _alu(self, o: PcodeOp) -> None:
        oc = o.opcode
        out = o.output
        ins = o.inputs
        if oc == OpCode.LOAD:
            addr = self._read(ins[1])
            self._write(out, self._rd_mem(addr, out.size))
            return
        if oc == OpCode.STORE:
            addr = self._read(ins[1])
            val = self._read(ins[2])
            self._wr_mem(addr, ins[2].size, val)
            self.stores.append((addr, ins[2].size, val))
            return
        a = self._read(ins[0]) if ins else 0
        b = self._read(ins[1]) if len(ins) > 1 else 0
        sz = out.size if out is not None else 4
        bits = 8 * sz
        sa = _signed(a, 8 * ins[0].size) if ins else 0
        sb = _signed(b, 8 * ins[1].size) if len(ins) > 1 else 0
        f = {
            OpCode.COPY: lambda: a,
            OpCode.INT_ADD: lambda: a + b,
            OpCode.INT_SUB: lambda: a - b,
            OpCode.INT_MULT: lambda: a * b,
            OpCode.INT_AND: lambda: a & b,
            OpCode.INT_OR: lambda: a | b,
            OpCode.INT_XOR: lambda: a ^ b,
            OpCode.INT_LEFT: lambda: a << b,
            OpCode.INT_RIGHT: lambda: a >> b,
            OpCode.INT_SRIGHT: lambda: sa >> b,
            OpCode.INT_NEGATE: lambda: ~a,
            OpCode.INT_2COMP: lambda: -a,
            OpCode.INT_ZEXT: lambda: a,
            OpCode.INT_SEXT: lambda: sa,
            OpCode.INT_EQUAL: lambda: int(a == b),
            OpCode.INT_NOTEQUAL: lambda: int(a != b),
            OpCode.INT_LESS: lambda: int(a < b),
            OpCode.INT_LESSEQUAL: lambda: int(a <= b),
            OpCode.INT_SLESS: lambda: int(sa < sb),
            OpCode.INT_SLESSEQUAL: lambda: int(sa <= sb),
            OpCode.INT_CARRY: lambda: int(a + b >= (1 << (8 * ins[0].size))),
            OpCode.INT_SCARRY: lambda: 0,
            OpCode.INT_SBORROW: lambda: 0,
            OpCode.BOOL_AND: lambda: int(bool(a) and bool(b)),
            OpCode.BOOL_OR: lambda: int(bool(a) or bool(b)),
            OpCode.BOOL_XOR: lambda: int(bool(a) != bool(b)),
            OpCode.BOOL_NEGATE: lambda: int(not a),
            OpCode.SUBPIECE: lambda: a >> (8 * b),
            OpCode.POPCOUNT: lambda: bin(a).count("1"),
            OpCode.INT_DIV: lambda: a // b if b else 0,
            OpCode.INT_REM: lambda: a % b if b else 0,
            OpCode.INT_SDIV: lambda: int(sa / sb) if sb else 0,
            OpCode.INT_SREM: lambda: sa - sb * int(sa / sb) if sb else 0,
        }.get(oc)
        if f is None:
            raise Opaque(f"unimplemented p-code op {oc}")
        if out is not None:
            self._write(out, f() & ((1 << bits) - 1))


class _Ret(Exception):
    pass


def _signed(v: int, bits: int) -> int:
    return v - (1 << bits) if v & (1 << (bits - 1)) else v


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("proc", help="PROC0, PROC8@30000000, ...")
    ap.add_argument("start", help="hex start address")
    ap.add_argument("end", nargs="?", help="hex end address (default: start+32)")
    ap.add_argument(
        "--overlay", type=int, help="1-based overlay resident at 0x7ffbc000"
    )
    ap.add_argument("--pcode", action="store_true", help="print p-code")
    ap.add_argument(
        "--walk",
        action="store_true",
        help="follow control flow instead of a linear sweep",
    )
    ap.add_argument(
        "--overlays",
        action="store_true",
        help="dump the overlay descriptor table and exit",
    )
    a = ap.parse_args(argv)

    img = Image.load(a.proc, overlay=a.overlay)
    if a.overlays:
        for n, (dst, ln, src2) in sorted(img.overlay_descriptors().items()):
            print(
                f"overlay {n:2d}  dst={dst:#010x} len={ln:#06x} src2={src2:#010x} delta={dst - src2:+#x}"
            )
        return 0

    start = int(a.start, 16)
    end = int(a.end, 16) if a.end else start + 32
    insns = (
        sorted(walk(img, start).insns.values(), key=lambda i: i.addr)
        if a.walk
        else lift_range(img, start, end)
    )
    for i in insns:
        raw = " ".join("%02x" % b for b in img.read(i.addr, i.length))
        print(f"{i.addr:08x}: {raw:<24} {i.text}")
        if a.pcode:
            for line in i.pcode():
                print(f"{'':>10}  {line}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
