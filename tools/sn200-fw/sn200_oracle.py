"""Answer "what does this NVMe command do?" by executing the firmware's own dispatch.

Every answer here comes from running p-code lifted out of `PROC8`, not from a
table someone typed. Two questions per encoding:

  * does the Post-Crash gate (`Admin_CheckCmdAllowed` 0x7ffa6b18) admit it?
  * for `0xFF`, which handler runs, and which EEPROM verb/section does it post?

    SN200_FW=~/sn200fw python3 sn200_oracle.py --gate           # gate allow-list
    SN200_FW=~/sn200fw python3 sn200_oracle.py --ff             # 0xFF CDW12 map
    SN200_FW=~/sn200fw python3 sn200_oracle.py --check ff:0x0004 c6:0x0320
"""

from __future__ import annotations

import argparse
import sys
from dataclasses import dataclass, field
from functools import lru_cache

import pcode

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
    for spec in a.check or []:
        op, _, c = spec.partition(":")
        ok, why = is_read_only(int(op, 16), int(c, 16))
        print(f"{spec}: {'READ-ONLY' if ok else 'NOT PROVEN READ-ONLY'} -- {why}")
    if not (a.gate or a.ff or a.check):
        ap.print_help()
    return 0


if __name__ == "__main__":
    sys.exit(main())
