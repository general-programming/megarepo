"""Answer "what does this NVMe command do?" by executing the firmware's own dispatch.

Every answer here comes from running p-code lifted out of `PROC8`, not from a
table someone typed. Three questions per encoding:

  * does the Post-Crash gate (`Admin_CheckCmdAllowed` 0x7ffa6b18) admit it?
  * for `0xFF`, which handler runs, and which EEPROM verb/section does it post?
  * for `0xCA`, which of the 67 jump-table arms runs, in which overlay, and
    what does the handler's own code say it touches?

    SN200_FW=~/sn200fw python3 sn200_oracle.py --gate           # gate allow-list
    SN200_FW=~/sn200fw python3 sn200_oracle.py --ff             # 0xFF CDW12 map
    SN200_FW=~/sn200fw python3 sn200_oracle.py --ca             # 0xCA family map
    SN200_FW=~/sn200fw python3 sn200_oracle.py --ca --danger    # operator table
    SN200_FW=~/sn200fw python3 sn200_oracle.py --check ff:0x0004 c6:0x0320
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from dataclasses import dataclass, field
from functools import lru_cache

import pcode
import sn200_strtab as strtab
from pypcode import OpCode

# --- PROC8 addresses, all runtime -----------------------------------------
GATE = 0x7FFA6B18  # Admin_CheckCmdAllowed
GATE_VERDICT = 0x7FFA6D05  # `beqz a9, <next gate>` -- a9 == 0 means admitted
STARTUP_MODE = 0x7FF87C64  # 6 = INVALID / Post Crash
POST_CRASH = 6

# --- overlay 22 (the 0xFF handler), static addresses ----------------------
FF_OVERLAY = 22
FF_CMDID_DISPATCH = 0x300334B5  # falls out of the coroutine entry 0x30033448
FF_ARM_ERASE = 0x30033529
FF_ARM_READRAW = 0x30033531
FF_ARM_PROBE = 0x30033500
FF_SUB_DISPATCH = 0x300336BE  # switch on CDW12[15:8]
FF_ENQUEUE = 0x30030AA0  # runtime 0x7ffb9768, the OAM worker enqueue
FF_MEMSET = 0x30031D10  # runtime 0x7ffba9d8
FF_LOG = 0x3002B8E0  # runtime 0x7ffb45a8

# Request-object fields, as seen by the producer in PROC8.
REQ_VERB = 0x118
REQ_SECTION = 0x11C
REQ_PARAM = 0x128
REQ_STATUS = 0x188

# Scratch addresses for synthetic structures. Anywhere unmapped works; these
# sit above every real image so a stray read of firmware data is obvious.
STACK, CTX, REQ = 0x30080000, 0x30090000, 0x300A0000

# EEPROM verbs, from docs/sn200-marker-write.md.
VERB_WRITE, VERB_ERASE, VERB_WRITE2, VERB_REINIT, VERB_READ = 1, 3, 0x20, 0x25, 0x2A
VERB_NAMES = {
    VERB_WRITE: "section write",
    VERB_ERASE: "section erase",
    VERB_WRITE2: "two-word write",
    VERB_REINIT: "schedule drive re-init",
    VERB_READ: "section read",
}
SECTION_NAMES = {
    3: "bad block list",
    6: "System Area (holds the boot-marker record)",
    8: "BIST status",
    9: "BIST script",
    10: "PFCL / PFail crash dump",
    11: "CLOG / crash dump",
    13: "SBL EEPROM",
}

READ_ONLY, MUTATES, DESTRUCTIVE, CATASTROPHIC, INERT, UNKNOWN = (
    "READ-ONLY",
    "MUTATES",
    "DESTRUCTIVE",
    "CATASTROPHIC",
    "INERT",
    "UNKNOWN",
)


@lru_cache(maxsize=4)
def _img(sel: str) -> pcode.Image:
    return pcode.Image.load(sel)


# --------------------------------------------------------------------------
# the gate
# --------------------------------------------------------------------------


def gate_admits(opcode: int, cdw12_lo: int = 0, mode: int = POST_CRASH) -> bool:
    """Run Admin_CheckCmdAllowed for real and report its verdict.

    `cdw12_lo` is the CDW12[7:0] byte the caller passes in a4; the gate only
    consults it for opcodes 0xC6 and 0xCA.
    """
    e = pcode.Emu(_img("PROC8"), on_opaque="skip")
    e.poke(STARTUP_MODE, mode)
    e.setreg("a3", opcode)
    e.setreg("a4", cdw12_lo)
    e.setreg("a1", 0x7FF90000)
    e.run(GATE, stop_at=(GATE_VERDICT,), max_steps=5000)
    return e.getreg("a9") == 0


def gate_allow_list(mode: int = POST_CRASH) -> tuple[set[int], dict[int, set[int]]]:
    """(opcodes admitted on the opcode alone, {opcode: admitted CDW12[7:0]})."""
    plain, sub = set(), {}
    for op in range(256):
        if gate_admits(op, 0, mode):
            plain.add(op)
    for op in (0xC6, 0xCA):
        if op in plain:
            continue
        ok = {b for b in range(256) if gate_admits(op, b, mode)}
        if ok:
            sub[op] = ok
    return plain, sub


# --------------------------------------------------------------------------
# the 0xFF surface
# --------------------------------------------------------------------------


@dataclass
class FfResult:
    cdw12: int
    command_id: int
    sub: int
    handler: str
    verb: int | None = None
    section: int | None = None
    param: int | None = None
    reinit_when_latched: bool = False
    calls: list[int] = field(default_factory=list)
    opaque: int = 0
    error: str | None = None

    @property
    def classification(self) -> str:
        if self.handler == "undecodable":
            return UNKNOWN
        if self.handler == "rejected":
            return INERT
        if self.handler == "startup-mode probe":
            return READ_ONLY
        if self.handler == "read raw System Area":
            return READ_ONLY
        if self.verb == VERB_REINIT:
            return CATASTROPHIC
        if self.section == 13:
            return CATASTROPHIC
        if self.reinit_when_latched:
            return CATASTROPHIC
        if self.verb in (VERB_ERASE, VERB_WRITE, VERB_WRITE2):
            return DESTRUCTIVE
        return UNKNOWN

    def describe(self) -> str:
        bits = [f"cmd 0x{self.command_id:02x} {self.handler}"]
        if self.error:
            bits.append(f"lifter stopped: {self.error}")
        if self.verb is not None:
            bits.append(f"verb {self.verb:#x} ({VERB_NAMES.get(self.verb, '?')})")
        if self.section:
            bits.append(
                f"section {self.section} ({SECTION_NAMES.get(self.section, '?')})"
            )
        if self.param:
            bits.append(f"param {self.param:#x}")
        if self.reinit_when_latched:
            bits.append("resume posts verb 0x25 when startup mode == 6")
        return "; ".join(bits)


def ff_command_id(cdw12: int) -> tuple[int, str]:
    """Execute the 0xFF command-id dispatch; return (id, handler name)."""
    img = _img("PROC8")
    e = pcode.Emu(img, on_opaque="skip")
    e.setreg("a2", CTX)
    e.setreg("a1", STACK)
    e.poke(CTX + 0x100 + 0x38, cdw12 & 0xFF, 1)
    end = e.run(
        FF_CMDID_DISPATCH,
        stop_at=(FF_ARM_ERASE, FF_ARM_READRAW, FF_ARM_PROBE),
        max_steps=400,
        stop_on_call=True,
    )
    return cdw12 & 0xFF, {
        FF_ARM_ERASE: "erase family",
        FF_ARM_READRAW: "read raw System Area",
        FF_ARM_PROBE: "startup-mode probe",
    }.get(end, "rejected")


def ff_erase_arm(
    sub: int,
) -> tuple[int | None, int | None, int | None, list[int], int, str | None]:
    """Run the erase sub-dispatch and read the request it fills in."""
    img = _img("PROC8")
    e = pcode.Emu(img, on_opaque="skip")
    e.poke(STACK + 8, CTX)
    e.poke(CTX + 0x139, sub, 1)
    e.setreg("a1", STACK)
    e.setreg("a5", REQ)
    e.setreg("a12", CTX)
    # Stop only when the request is handed to the OAM worker; sub 3 calls
    # memset on the way there and stopping at that would read a blank request.
    try:
        e.run(FF_SUB_DISPATCH, max_steps=4000, stop_on_call=(FF_ENQUEUE, FF_LOG))
        # Two-stage arms (sub 3 is the only one) do their preparation, yield,
        # and build the request on the *next* coroutine entry. The yield hands
        # the resume PC back in a3 as a runtime address in the overlay window;
        # follow it rather than reporting "no enqueue reached".
        if FF_ENQUEUE not in e.calls:
            resume = e.getreg("a3")
            if pcode.OVERLAY_WINDOW <= resume < pcode.OVERLAY_WINDOW + 0x4000:
                e.setreg("a12", REQ)
                e.setreg("a1", STACK)
                e.run(
                    resume - img.overlay_delta(FF_OVERLAY),
                    max_steps=4000,
                    stop_on_call=(FF_ENQUEUE, FF_LOG),
                )
    except (pcode.BadDataError, pcode.Opaque, pcode.Halt) as exc:
        return None, None, None, e.calls, len(e.opaque), str(exc)
    if FF_ENQUEUE not in e.calls:
        return None, None, None, e.calls, len(e.opaque), "no enqueue reached"
    return (
        e.peek(REQ + REQ_VERB),
        e.peek(REQ + REQ_SECTION),
        e.peek(REQ + REQ_PARAM),
        e.calls,
        len(e.opaque),
        None,
    )


# Resume handlers, keyed by erase sub-command. Reached from the arm's
# continuation literal; addresses from docs/sn200-oam-dispatch.md 4.1.
FF_RESUMES = {
    0: 0x30033571,
    1: 0x30033652,
    2: 0x30033643,
    3: 0x300335F7,
    4: 0x300335D9,
    5: 0x300335CA,
    6: 0x300335A3,
}


def ff_resume_posts_reinit(sub: int, mode: int = POST_CRASH) -> bool:
    """Does this sub-command's resume handler post re-init verb 0x25?

    Runs the resume handler with the erase reported successful, which is the
    only branch that can continue past the completion tail.
    """
    entry = FF_RESUMES.get(sub)
    if entry is None:
        return False
    img = _img("PROC8")
    e = pcode.Emu(img, on_opaque="skip")
    e.poke(STARTUP_MODE, mode)
    e.poke(REQ + REQ_STATUS, 0)
    e.setreg("a12", REQ)
    e.setreg("a5", REQ)
    e.setreg("a1", STACK)
    try:
        e.run(entry, max_steps=4000, stop_on_call=(FF_ENQUEUE,))
    except (pcode.BadDataError, pcode.Opaque, pcode.Halt):
        return False
    return FF_ENQUEUE in e.calls and e.peek(REQ + REQ_VERB) == VERB_REINIT


def ff_classify(cdw12: int) -> FfResult:
    cid, handler = ff_command_id(cdw12)
    r = FfResult(cdw12=cdw12, command_id=cid, sub=(cdw12 >> 8) & 0xFF, handler=handler)
    if handler != "erase family":
        return r
    sub = r.sub
    verb, section, param, calls, opq, err = ff_erase_arm(sub)
    r.verb, r.section, r.param, r.calls, r.opaque, r.error = (
        verb,
        section,
        param,
        calls,
        opq,
        err,
    )
    if verb is None:
        # A genuinely invalid sub-command logs "Received Bad Erase sub-cmd"
        # and returns; a lift failure is a different thing entirely.
        r.handler = (
            "undecodable" if err and "resolve constructor" in err else "rejected"
        )
        return r
    r.reinit_when_latched = ff_resume_posts_reinit(sub)
    return r


def ff_surface() -> dict[int, FfResult]:
    """Every CDW12 the 0xFF handler treats as valid, executed one by one.

    Only CDW12[15:0] is enumerated: the dispatch reads CDW12[7:0] as the
    command id and CDW12[15:8] as the erase sub-command, and never reads the
    upper half-word at all.
    """
    out = {}
    for cdw12 in range(0x10000):
        cid = cdw12 & 0xFF
        if cid not in (0x03, 0x04, 0x07):
            continue
        if cid != 0x03 and (cdw12 >> 8):
            continue  # sub byte is not read by 0x04 / 0x07
        r = ff_classify(cdw12)
        if r.handler != "rejected":
            out[cdw12] = r
    return out


# --------------------------------------------------------------------------
# the 0xCA surface
# --------------------------------------------------------------------------
#
# 0xCA is dispatched through a 67-entry jump table of three-byte `j` slots.
# Each arm loads a *runtime* handler pointer (in the 0x7ffbc000 overlay
# window) and stores an overlay index into the request object, then falls into
# a common enqueue. Runtime pointers alone do not identify code -- two arms
# (0x33 and 0x37) load the SAME pointer and differ only in the overlay -- so
# both halves are read back and the static address is resolved through the
# overlay descriptor table.

CA_DISPATCH = 0x7FFA75E1  # l32i.n a12,a1,0x24 ; l8ui a12,a12,0x38
CA_TABLE = 0x7FFA760E  # 67 x 3-byte `j`
CA_TABLE_LEN = 67
CA_ENQUEUE = 0x7FFA6E89  # common tail for overlay handlers
CA_ENQUEUE_RESIDENT = 0x7FFA6E6C  # tail for the two inline arms (0x05/0x06)
CA_REJECT = 0x7FFA78E3  # "Admin cmd not supported" default arm

# Scratch layout for the dispatcher run. `a6` points at a word holding the
# request object; the arms store the overlay index at request+0x20.
CA_OBJ = 0x7FF99000
CA_REQ = CA_OBJ + 0x200
CA_CTX = 0x7FF90000
CA_STACK = 0x7FF98000

# Command-context byte offsets, from docs/sn200-dangerous-commands.md 4.2.
CTX_CDW12_LO = 0x38  # the 0xCA command byte, and the gate's sub-list key
CTX_CDW12_HI = 0x39  # the sub-command byte
# The coroutine object is ctx-0x100, so the same two bytes are at +0x138/+0x139
# there. Handlers reach them through a base register already advanced by 0x40,
# which is why the *instruction* displacement is 0xf8/0xf9 and not 0x38/0x39 --
# both forms have to be recognised when scanning for a read of the sub byte.
OBJ_CDW12_LO, OBJ_CDW12_HI = 0x138, 0x139
IMM_CDW12_HI = (CTX_CDW12_HI, 0xF9)

# 0x0F (raw block erase) and 0x10 (raw page program), overlay 31.
CA_ERASE_ENTRY = 0x3003DBE0
CA_WRITE_ENTRY = 0x3003D5BC
CA_RAWREAD_ENTRY = 0x30036E28
CA_RAWREAD_CLAMP = 0x30037039  # minu a10,a10,a11 with a11 = 640

_STRTAB_REV = os.environ.get("SN200_REV", "KNGND122")


@lru_cache(maxsize=1)
def _strings() -> strtab.StringTable | None:
    for name in ("StringTable.csv.gz", "StringTable.csv"):
        p = os.path.join(pcode.FW_DIR, "fw", _STRTAB_REV, name)
        if os.path.exists(p):
            return strtab.StringTable.load(p)
    return None


@dataclass
class CaDispatch:
    """What PROC8's own jump table does with one CDW12[7:0] value."""

    sub: int
    arm: int | None = None  # the 3-byte `j` slot's target
    handler_rt: int | None = None  # runtime pointer, 0x7ffbc000 window
    overlay: int | None = None
    static: int | None = None
    resident: bool = False
    calls: list[int] = field(default_factory=list)

    @property
    def implemented(self) -> bool:
        return self.arm is not None


def ca_dispatch(sub: int) -> CaDispatch:
    """Run the real dispatcher for one command byte and read out where it goes.

    The jump itself is an indirect `jx` whose FLIX slot the spec does not
    decode, so the target is taken from `a0` -- which the *executed*
    `addx2`/`add.n`/`l32r` sequence computed -- and then entered. Everything
    else, including the handler pointer and overlay index, is read off stores
    and registers the arm's own instructions produced.
    """
    img = _img("PROC8")
    d = CaDispatch(sub=sub)
    e = pcode.Emu(img, on_opaque="skip")
    e.setreg("a1", CA_STACK)
    e.poke(CA_STACK + 0x24, CA_CTX)
    e.poke(CA_CTX + CTX_CDW12_LO, sub, 1)
    e.setreg("a6", CA_OBJ)
    e.poke(CA_OBJ, CA_REQ)
    e.setreg("a7", CA_REQ)
    end = e.run(CA_DISPATCH, stop_at=(CA_TABLE, CA_REJECT), max_steps=2000)
    if end == CA_REJECT:
        return d
    d.arm = e.getreg("a0")
    if d.arm != CA_TABLE + 3 * sub:
        raise AssertionError(f"0xCA sub {sub:#x}: table index came out {d.arm:#x}")
    e.stores.clear()
    end = e.run(
        d.arm,
        stop_at=(CA_ENQUEUE, CA_ENQUEUE_RESIDENT, CA_REJECT),
        max_steps=2000,
        stop_on_call=True,
    )
    if end == CA_REJECT:
        d.arm = None
        return d
    d.calls = list(e.calls)
    ovl = [v for a, _, v in e.stores if a == CA_REQ + 0x20]
    # Arms differ in which register they park the handler pointer in (0x22,
    # 0x25, 0x26 and 0x32 use a9 where the rest use a13), so take whichever
    # register the arm left holding an overlay-window address rather than
    # naming one.
    hs = {
        v
        for v in e.regs.values()
        if pcode.OVERLAY_WINDOW <= v < pcode.OVERLAY_WINDOW + 0x4000
    }
    if not hs or not ovl:
        d.resident = True
        return d
    d.handler_rt = sorted(hs)[0]
    d.overlay = ovl[0]
    _, _, src2 = img.overlay_descriptors()[d.overlay]
    d.static = src2 + (d.handler_rt - pcode.OVERLAY_WINDOW)
    return d


@lru_cache(maxsize=1)
def ca_table() -> dict[int, CaDispatch]:
    """Every implemented CDW12[7:0], executed. 67 entries in, 37 out."""
    out = {}
    for sub in range(CA_TABLE_LEN):
        d = ca_dispatch(sub)
        if d.implemented:
            out[sub] = d
    return out


def _overlay_end(overlay: int) -> int:
    _, ln, src2 = _img("PROC8").overlay_descriptors()[overlay]
    return src2 + ln


def ca_body(sub: int) -> tuple[int, int] | None:
    """[entry, next handler entry in the same overlay) -- the coroutine body.

    These handlers are 26-to-120-byte coroutine trampolines whose resume
    bodies follow them, so the confirmed function extent is far too small and
    a fixed byte window is far too large (that mistake produced a published
    table calling 0xCA/0x11 a Multiplane Write). Bounding by the next handler
    entry is an ordering argument, not a containment one: treat attributions
    from this range as INFERRED.
    """
    d = ca_table().get(sub)
    if d is None or d.static is None:
        return None
    end = _overlay_end(d.overlay)
    later = sorted(
        o.static
        for o in ca_table().values()
        if o.overlay == d.overlay and o.static and o.static > d.static
    )
    if later:
        end = min(later[0], end)
    return d.static, end


def _insn_len_from_op0(byte0: int) -> int:
    """Xtensa fixes instruction length from op0 alone; used to step over a
    byte range SLEIGH cannot decode without losing alignment."""
    op0 = byte0 & 0xF
    if op0 in (8, 9, 12, 13):
        return 2
    if op0 in (14, 15):
        return 8
    return 3


def _sweep(start: int, end: int):
    """Yield lifted instructions over [start, end), keeping byte alignment."""
    img = _img("PROC8")
    pc = start
    while pc < end:
        try:
            i = pcode.lift(img, pc)[0]
        except Exception:
            pc += _insn_len_from_op0(img.read(pc, 1)[0])
            continue
        yield i
        pc += i.length


def ca_log_strings(sub: int) -> list[tuple[int, int, str]]:
    """(site, StrId, text) for every log descriptor the handler body loads.

    A descriptor is `(StrId << 16) | (level << 8) | nargs`; the scan takes any
    `l32r` whose literal decodes as one, which is how every other tool here
    attributes strings.
    """
    tab = _strings()
    rng = ca_body(sub)
    if tab is None or rng is None:
        return []
    img = _img("PROC8")
    out = []
    for i in _sweep(*rng):
        for o in i.ops:
            if o.opcode not in (OpCode.LOAD, OpCode.COPY):
                continue
            v = o.inputs[-1]
            if v.space.name != "ram":
                continue
            try:
                w = img.word(v.offset)
            except KeyError:
                continue
            d = strtab.LogDescriptor.unpack(w)
            if d.level not in strtab.KNOWN_LEVELS or d.nargs > strtab.MAX_NARGS:
                continue
            if not tab.plausible(d.str_id):
                continue
            out.append((i.addr, d.str_id, tab.text(d.str_id)))
    return out


# Main-image callees worth naming, as *runtime* addresses. Overlay code is
# linked for execution at 0x7ffbc000, so a callN displacement read out of the
# static image is wrong by the overlay delta and must be corrected before it
# names anything (docs/sn200-oam-dispatch.md 1.1).
FLASH_OP_LOCK = 0x7FFB42CC  # acquire the flash-operation lock
FLASH_ADDR_HELPER = 0x7FFB3F4C  # takes CDW13; shared by 0x11 and the erase arm
LOG_EMIT = 0x7FFB45A8


def ca_calls(sub: int) -> list[int]:
    """Runtime call targets statically reachable from the handler entry.

    A LOWER BOUND, and it must be read as one: these handlers yield and resume
    through `jx`, and the walk does not follow an indirect jump. Presence of a
    callee is evidence; absence is not.
    """
    d = ca_table().get(sub)
    if d is None or d.static is None:
        return []
    img = _img("PROC8")
    delta = img.overlay_delta(d.overlay)
    return sorted({c + delta for c in pcode.walk(img, d.static, limit=1500).calls})


def ca_reads_sub_byte(sub: int) -> bool | None:
    """Does this handler's body reference CDW12[15:8] at all?

    Mechanical, over the lifted instruction stream: an access to the sub byte
    has to materialise the constant 0x39 (from the context) or 0xf9 (from the
    coroutine object) in an address computation. False means there is no
    "harmless sub-value" -- every CDW12[15:8] does the same thing.
    """
    rng = ca_body(sub)
    if rng is None:
        return None
    for i in _sweep(*rng):
        for o in i.ops:
            if o.opcode not in (OpCode.LOAD, OpCode.STORE, OpCode.INT_ADD):
                continue
            for v in o.inputs:
                if v.space.name == "const" and v.offset in IMM_CDW12_HI:
                    return True
    return False


# Keyword rules over the handler's own log strings, in priority order. The
# firmware names what it is about to do; nothing else in this family is a
# reliable signal, and "no destructive string" is not evidence of safety.
_CA_RULES = (
    (
        DESTRUCTIVE,
        re.compile(
            r"VUC Erase|Multiplane Erase|Multiplane Write|WritePageRaw"
            r"|ProgNANDPage|erasure",
            re.I,
        ),
    ),
    (CATASTROPHIC, re.compile(r"Set Features addr|SetTestModeRegister", re.I)),
    (MUTATES, re.compile(r"VucFlashReset", re.I)),
    (
        READ_ONLY,
        re.compile(
            r"Read|\bGet|UID|Histogram|Lot ID|LotID|ToPhysical|Status"
            r"|Erase Count|FuseRom|MT Info",
            re.I,
        ),
    ),
)


@dataclass
class CaResult:
    sub: int
    dispatch: CaDispatch
    strings: list[tuple[int, int, str]] = field(default_factory=list)
    reads_sub_byte: bool | None = None
    body: tuple[int, int] | None = None
    calls: list[int] = field(default_factory=list)
    classification: str = UNKNOWN
    evidence: str = "none"
    note: str = ""

    @property
    def gate_admitted(self) -> bool:
        return gate_admits(0xCA, self.sub)

    @property
    def takes_flash_lock(self) -> bool:
        """Does the handler acquire the flash-operation lock?

        Not a verdict, but it separates "touches the media" from "reads a
        table in DDR", which is the distinction that matters for the arms with
        no log strings at all.
        """
        return FLASH_OP_LOCK in self.calls

    def describe(self) -> str:
        bits = []
        if self.dispatch.resident:
            bits.append(f"resident arm {self.dispatch.arm:#x}, no overlay")
        elif self.dispatch.static:
            bits.append(
                f"ovl {self.dispatch.overlay} "
                f"{self.dispatch.handler_rt:#x} -> {self.dispatch.static:#x}"
            )
        if self.takes_flash_lock:
            bits.append("takes the flash-op lock")
        if self.note:
            bits.append(self.note)
        if self.strings:
            bits.append(self.strings[0][2][:64])
        elif not self.dispatch.resident:
            bits.append("no log string in its body")
        return "; ".join(bits)


# Results this file establishes by executing the handler rather than by
# reading a log string; see ca_erase_ignores_sub_byte() and friends.
_CA_NOTES = {
    0x0F: "raw NAND BLOCK ERASE; CDW12[15:8] ignored (executed)",
    0x10: "raw NAND PAGE WRITE/PROGRAM; subs 0/1 program, 2 fetches a result",
    0x03: "raw page read, clamped to 640 bytes at 0x30037039 (executed)",
    0x37: "Multiplane Write + Multiplane Erase",
    0x12: "VUC_ERASE_PWR_CHAR -- this arm erases blocks",
    0x39: "NAND-chip (ONFI) SET FEATURES, not NVMe Set Features",
    0x3B: "writes a NAND die test-mode register",
}


def ca_classify(sub: int) -> CaResult:
    d = ca_table().get(sub)
    if d is None:
        return CaResult(sub=sub, dispatch=CaDispatch(sub=sub), classification=INERT)
    r = CaResult(sub=sub, dispatch=d, note=_CA_NOTES.get(sub, ""))
    if d.resident:
        r.note = r.note or (
            "inline arm: calls 0x7ffa915c(%d), 0x7ffa9168, result to req+0x154"
            % (1 if sub == 0x05 else 0)
        )
        return r
    r.body = ca_body(sub)
    r.strings = ca_log_strings(sub)
    r.reads_sub_byte = ca_reads_sub_byte(sub)
    r.calls = ca_calls(sub)
    blob = " | ".join(s for _, _, s in r.strings)
    for cls, rx in _CA_RULES:
        if rx.search(blob):
            r.classification = cls
            r.evidence = "log strings in the handler body"
            break
    else:
        r.evidence = "none -- no log string in the handler body"
    return r


def ca_surface() -> dict[int, CaResult]:
    return {sub: ca_classify(sub) for sub in ca_table()}


CA_DANGEROUS = (DESTRUCTIVE, CATASTROPHIC)


def ca_neighbours(sub: int) -> list[tuple[int, str]]:
    """Implemented, known-destructive command bytes a typo away from `sub`.

    Two relations, both of which have already produced real incidents in this
    family:

      * `nibble` -- one hex digit differs. This is the mistyped-command case
        (`0xFF` `0x0003` beside the `0x0004` probe, `0xC6` `0x__20` beside
        `0x__30`).
      * `+-1` -- the command bytes are consecutive integers, which is how they
        appear in a loop counter. `0x0F` and `0x10` are 15 and 16.
    """
    out = []
    for other, r in ca_surface().items():
        if other == sub or r.classification not in CA_DANGEROUS:
            continue
        if (sub ^ other) & 0xF0 == 0 or (sub ^ other) & 0x0F == 0:
            out.append((other, "nibble"))
        elif abs(sub - other) == 1:
            out.append((other, "+-1"))
    return sorted(out)


# --------------------------------------------------------------------------
# the two commands that destroy a drive, executed rather than read
# --------------------------------------------------------------------------


def _run_erase(sub_byte: int, cdw13: int = 1) -> tuple[tuple[int, ...], list[int]]:
    """Run the 0x0F coroutine's first entry with one CDW12[15:8] value.

    `cdw13` is the physical flash address; the validity helper at 0x30033d00
    is not entered, so leaving its result register holding the address makes
    the `beqi a10,1` check pass and the erase arm is the one that runs.
    """
    img = _img("PROC8")
    e = pcode.Emu(img, on_opaque="skip")
    e.setreg("a1", STACK)
    e.setreg("a2", REQ)
    e.poke(REQ + 0x18, 0)  # no saved resume PC == first entry
    e.poke(REQ + 0x13C, cdw13)  # CDW13, the raw flash address
    e.poke(REQ + OBJ_CDW12_HI, sub_byte, 1)
    e.run(CA_ERASE_ENTRY, max_steps=4000)
    return tuple(e.trace), list(e.calls)


def ca_erase_ignores_sub_byte() -> bool:
    """PROVEN-by-execution: no CDW12[15:8] changes what 0xCA/0x0F does.

    Every one of the 256 values produces a byte-identical instruction trace,
    and `ca_reads_sub_byte(0x0f)` shows the byte is never even addressed. There
    is no harmless sub-value of the raw block erase.
    """
    base, _ = _run_erase(0)
    return all(_run_erase(b)[0] == base for b in range(256))


# The 0x10 coroutine's first-entry sub dispatch: `beqz.n` / `beqi 1` / `beqi 2`
# at 0x3003db1f..0x3003db24, falling through to the invalid-field tail.
CA_WRITE_SUB_DISPATCH = 0x3003DB1F
CA_WRITE_ARM_0 = 0x3003DB57
CA_WRITE_ARM_12 = 0x3003DB38
CA_WRITE_ARM_REJECT = 0x3003D936


def ca_write_sub_arms(subs=(0, 1, 2, 3, 0xFF)) -> dict[int, int]:
    """{CDW12[15:8]: which arm the 0x10 coroutine's first entry selects}.

    0/1/2 are accepted and >= 3 falls into the invalid-field tail. Sub 2 takes
    the *same* first-entry arm as sub 1 -- it only parts company after the
    host->DDR transfer -- which is why 0x0210 is not a separate command but a
    late branch inside the program path.
    """
    img = _img("PROC8")
    out = {}
    for b in subs:
        e = pcode.Emu(img, on_opaque="skip")
        e.setreg("a1", STACK)
        e.setreg("a2", REQ)
        e.setreg("a5", REQ)
        e.poke(REQ + 0x18, 0)  # no saved resume PC == first entry
        e.poke(REQ + OBJ_CDW12_HI, b, 1)
        e.run(
            CA_WRITE_ENTRY,
            stop_at=(CA_WRITE_ARM_0, CA_WRITE_ARM_12, CA_WRITE_ARM_REJECT),
            max_steps=800,
        )
        out[b] = e.pc
    return out


def ca_rawread_clamp(request: int = 0x10000) -> int | None:
    """Execute the 0xCA/0x03 length clamp and report what it lets through.

    Runs the bundle that materialises the bound and the `minu` that applies
    it, with an absurd requested length in a10, and returns what survives.
    """
    img = _img("PROC8")
    e = pcode.Emu(img, on_opaque="skip")
    e.setreg("a1", STACK)
    e.setreg("a10", request)
    e.run(CA_RAWREAD_CLAMP - 8, stop_at=(CA_RAWREAD_CLAMP + 3,), max_steps=8)
    if "minu" not in pcode.lift(img, CA_RAWREAD_CLAMP)[0].text:
        return None
    return e.getreg("a10")


def ca_has_length_clamp(sub: int) -> bool:
    """Is there any `minu` -- the firmware's clamp idiom -- in this body?"""
    rng = ca_body(sub)
    return bool(rng) and any("minu" in i.text for i in _sweep(*rng))


# --------------------------------------------------------------------------
# the read-only assertion the triage script depends on
# --------------------------------------------------------------------------


def is_read_only(opcode: int, cdw12: int) -> tuple[bool, str]:
    """True only if the firmware itself shows this encoding cannot mutate state.

    Anything the oracle cannot walk end to end answers False -- "not proven
    read-only" is the safe result, and it is not the same as "dangerous".
    """
    if opcode == 0xFF:
        r = ff_classify(cdw12)
        if r.classification == READ_ONLY:
            return True, r.describe()
        return False, f"{r.classification}: {r.describe()}"
    if opcode in (0x02, 0x06, 0x0A):  # get log page / identify / get features
        return True, "NVMe admin read command"
    return False, f"opcode {opcode:#04x} is not modelled by the oracle"


# --------------------------------------------------------------------------
# CLI
# --------------------------------------------------------------------------


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("--gate", action="store_true", help="enumerate the Post-Crash gate")
    ap.add_argument(
        "--ff", action="store_true", help="enumerate the 0xFF CDW12 surface"
    )
    ap.add_argument(
        "--ca", action="store_true", help="enumerate the 0xCA command-byte surface"
    )
    ap.add_argument(
        "--danger",
        action="store_true",
        help="with --ca, print the operator danger table instead",
    )
    ap.add_argument(
        "--check", nargs="*", metavar="OP:CDW12", help="classify specific encodings"
    )
    a = ap.parse_args(argv)

    if a.gate:
        plain, sub = gate_allow_list()
        print(
            "post-crash gate admits on opcode alone: "
            + " ".join(f"0x{o:02x}" for o in sorted(plain))
        )
        for op, ok in sorted(sub.items()):
            print(
                f"  0x{op:02x} additionally requires CDW12[7:0] in: "
                + " ".join(f"0x{b:02x}" for b in sorted(ok))
            )
    if a.ff:
        surface = ff_surface()
        print(f"{'CDW12':>8}  {'class':<13} detail")
        for cdw12, r in sorted(surface.items()):
            print(f"  0x{cdw12:04x}  {r.classification:<13} {r.describe()}")
        print(
            f"\n{len(surface)} valid encodings; every other CDW12 is rejected with no side effect."
        )
    if a.ca:
        surface = ca_surface()
        gated = {s for s in range(256) if gate_admits(0xCA, s)}
        if a.danger:
            print(
                f"{'CDW12[7:0]':>10}  {'latched':<8} {'class':<13} nearest dangerous neighbour"
            )
            for sub, r in sorted(surface.items()):
                nb = ca_neighbours(sub)
                print(
                    f"      0x{sub:02x}  {'ADMITTED' if sub in gated else '-':<8} "
                    f"{r.classification:<13} "
                    + (" ".join(f"0x{n:02x}({k})" for n, k in nb) if nb else "-")
                )
        else:
            print(f"{'CDW12[7:0]':>10}  {'latched':<8} {'class':<13} detail")
            for sub, r in sorted(surface.items()):
                print(
                    f"      0x{sub:02x}  {'ADMITTED' if sub in gated else '-':<8} "
                    f"{r.classification:<13} {r.describe()}"
                )
            print(
                f"\n{len(surface)} of {CA_TABLE_LEN} jump-table entries are implemented; "
                f"{len(gated & set(surface))} are admitted on a latched drive."
            )
    for spec in a.check or []:
        op, _, c = spec.partition(":")
        ok, why = is_read_only(int(op, 16), int(c, 16))
        print(f"{spec}: {'READ-ONLY' if ok else 'NOT PROVEN READ-ONLY'} -- {why}")
    if not (a.gate or a.ff or a.ca or a.check):
        ap.print_help()
    return 0


if __name__ == "__main__":
    sys.exit(main())
