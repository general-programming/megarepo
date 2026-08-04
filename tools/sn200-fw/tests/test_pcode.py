"""Tests for pcode.py -- the SLEIGH/pypcode lifter for SN200 FLIX bundles.

These guard the failure mode that made the lifter worth building: a decoder
that is confidently wrong. Each test names a specific way a bundle can be
mis-lifted, all of which have happened at least once by hand:

  * `test_slot_a_agrees_with_reference_decoder` -- a slot-A field read off by
    one nibble. Nothing downstream would notice.
  * `test_slot_c_high_byte_is_mov_not_movi` -- the decode that writes a small
    constant over a live pointer (docs/sn200-oam-dispatch.md 1.2).
  * `test_l32r_literal_needs_the_whole_instruction` -- lifting from a buffer
    that ends mid-instruction silently relocates a pc-relative load.
  * `test_overlay_call_target_resolves_in_runtime_space` -- the wrong address
    space made 174/174 overlay call targets look unresolvable.
"""

import glob
import os
import re
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import pcode  # noqa: E402
import xdis  # noqa: E402

FW = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
pytestmark = pytest.mark.skipif(
    not glob.glob(os.path.join(FW, "flat", "PROC8_*.bin")),
    reason=f"no SN200 firmware images under {FW}/flat",
)


@pytest.fixture(scope="module")
def proc8():
    return pcode.Image.load("PROC8")


def _canon(text: str):
    """(mnemonic, operands) with register names kept and numbers normalised."""
    m = re.match(r"([a-z0-9._]+)\s*(.*)", text.strip())
    mnem, ops = m.group(1), m.group(2)
    vals = []
    for tok in ops.split(","):
        tok = tok.strip()
        if re.fullmatch(r"a\d+", tok):
            vals.append(tok)
        elif tok:
            try:
                vals.append(int(tok, 0) & 0xFFFFFFFF)
            except ValueError:
                vals.append(tok)
    return mnem, vals


# Cosmetic naming differences between the two decoders, not disagreements:
# xdis prints the ordering barrier as "sync/extw" and spells the call window
# increment in registers, the spec in raw n.
def _spelling(text: str) -> str:
    text = text.replace("sync/extw", "extw")
    for n, name in enumerate(("call0", "call4", "call8", "call12")):
        text = text.replace(f"call0x{n} ", f"{name} ")
    return text


def test_slot_a_agrees_with_reference_decoder(proc8):
    """Every slot A both decoders understand must decode identically.

    xdis.py is the independently derived reference. A disagreement means one
    of them is inventing an instruction, which is exactly how a fabricated
    read of a bundle tail gets into a document.
    """
    ctx = pcode.context()
    disagreements = []
    agreed = 0
    for base, data in proc8.segs:
        for off in range(0, len(data) - 16, 4):
            if (data[off] & 0xF) not in (0xE, 0xF):
                continue
            pc = base + off
            ref = xdis.flix_slotA(int.from_bytes(data[off : off + 8], "little"), pc)
            if "?" in ref:
                continue
            insn = ctx.disassemble(data[off : off + 16], pc).instructions[0]
            if insn.length != 8:
                continue
            got = insn.body.split(" | ")[0]
            if got.startswith("?slotA"):
                continue
            rc, gc = _canon(_spelling(ref)), _canon(_spelling(got))
            if rc == gc:
                agreed += 1
            else:
                disagreements.append((hex(pc), ref, got))
    assert agreed > 4000, (
        f"only {agreed} slot-A comparisons -- the sweep stopped finding bundles"
    )
    assert not disagreements[:20], disagreements[:20]


def test_slot_c_high_byte_is_mov_not_movi(proc8):
    """Slot C 0xC..8t is `mov`, and decoding it as `movi` corrupts a pointer.

    At 0x3003377a the following bundle dereferences a5. Reading slot C as
    `movi a5,140` overwrites it with a small constant; the correct read is
    `mov a12,a5`.
    """
    insn = pcode.lift(proc8, 0x3003377A)[0]
    assert insn.body.split(" | ")[2] == "mov a12,a5"
    assert "movi a5" not in insn.body


def test_l32r_literal_needs_the_whole_instruction(proc8):
    """A buffer that stops mid-instruction relocates the literal it loads."""
    good = pcode.lift(proc8, 0x300335D0)[0]
    assert "ram[3003337c:" in good.pcode()[0]
    ctx = pcode.context()
    truncated = ctx.translate(proc8.read(0x300335D0, 2), 0x300335D0)
    assert "ram[3003337c:" not in " ".join(
        pcode.PcodePrettyPrinter.fmt_op(o) for o in truncated.ops
    )


def test_flix_call_targets_are_not_invisible(proc8):
    """call0/4/8/12 in slot A must produce a CALL edge.

    They were opaque in the first version of the spec, which silently removed
    hundreds of call-graph edges -- including the OAM enqueue.
    """
    trace = pcode.walk(proc8, 0x300336BE, limit=400)
    assert trace.calls, "the erase sub-dispatch reaches no calls at all"


def test_overlay_call_target_resolves_in_runtime_space(proc8):
    """`static + (0x7ffbc000 - src2)` is the rule; the naive read gives nonsense.

    Overlay 22's enqueue call 0x30030aa0 must resolve to 0x7ffb9768, the OAM
    worker enqueue, not to a static address that is not a function at all.
    """
    delta = proc8.overlay_delta(22)
    assert delta == 0x7FFBC000 - 0x30033338
    assert 0x30030AA0 + delta == 0x7FFB9768
    assert 0x30031D10 + delta == 0x7FFBA9D8  # memset, not "the EEPROM primitive"
    assert 0x3002B8E0 + delta == 0x7FFB45A8  # the log function


def test_every_overlay_shares_one_runtime_window(proc8):
    """A 0x7ffbxxxx address names code only once you say which overlay is in.

    All 34 descriptors target 0x7ffbc000, so "the function at 0x7ffbc110" is
    ambiguous on its own.
    """
    descs = proc8.overlay_descriptors()
    assert len(descs) >= 30
    assert {d[0] for d in descs.values()} == {pcode.OVERLAY_WINDOW}
    assert len(pcode.overlay_for(proc8, 0x7FFBC110)) > 1


def test_undecoded_slot_refuses_to_answer(proc8):
    """An undecoded slot must raise, not quietly execute as a no-op.

    The custom TIE opcodes (QRST op1=6/7, CUST0/CUST1) have no public
    description; treating them as nops would turn "we cannot tell" into a
    confident wrong answer.
    """
    e = pcode.Emu(proc8)  # default policy is "raise"
    with pytest.raises(pcode.Opaque):
        e.setreg("a1", 0x7FF90000)
        e.run(0x7FFA6B18, stop_at=(0x7FFA6D05,), max_steps=500)
