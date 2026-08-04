"""The load-bearing SN200 claims, re-derived from the firmware on every run.

Every assertion here used to live only in a document or a commit message. If
one of them starts failing, either the firmware images changed or a claim we
have been acting on operationally was wrong -- both are worth stopping for.

The dangerous adjacencies are the point:

  * `0x0003` erases the boot-marker record and sits one nibble from the
    `0x0004` probe we type on healthy drives.
  * `0x0403` is an ungated FACTORY re-init, one nibble from `0x0503`.
  * `0xC6` is admitted with CDW12[7:0] 0x20 *and* 0x30.
"""

import glob
import os
import sys

import pytest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import pcode  # noqa: E402
import sn200_oracle as oracle  # noqa: E402

FW = os.environ.get("SN200_FW", os.path.expanduser("~/sn200fw"))
pytestmark = pytest.mark.skipif(
    not glob.glob(os.path.join(FW, "flat", "PROC8_*.bin")),
    reason=f"no SN200 firmware images under {FW}/flat",
)

MARKER_DISPATCH = 0x7FFAAE69
UNEXSTRT_STUB_WRITER = 0x7FFAAD01
FORCE_MARKER_9 = 0x7FFAAF08


# --------------------------------------------------------------------------
# the admin gate
# --------------------------------------------------------------------------


def test_post_crash_gate_admits_exactly_the_documented_set():
    """A latched drive still accepts these, and nothing else.

    Two entries are why the drive can destroy itself while apparently dead:
    0xCA/0x0f (raw NAND erase) and 0xCA/0x10 (raw page write).
    """
    plain, sub = oracle.gate_allow_list()
    assert plain == {
        0x00,
        0x01,
        0x02,
        0x04,
        0x05,
        0x06,
        0x08,
        0x09,
        0x0A,
        0x0C,
        0x10,
        0x11,
        0xE6,
        0xEC,
        0xFF,
    }
    assert sub[0xC6] == {0x20, 0x30}
    assert sub[0xCA] == {
        0x02,
        0x03,
        0x04,
        0x08,
        0x0D,
        0x0E,
        0x0F,
        0x10,
        0x11,
        0x13,
        0x21,
        0x32,
    }
    assert len(sub[0xCA]) == 12


def test_gate_never_inspects_cdw12_for_0xff():
    """0xFF is admitted on the opcode alone, so every selector is reachable."""
    assert all(oracle.gate_admits(0xFF, b) for b in range(256))


def test_gate_is_inert_outside_post_crash_mode():
    """The allow-list only applies when the startup mode word reads 6."""
    assert oracle.gate_admits(0x13, mode=1)
    assert not oracle.gate_admits(0x13, mode=oracle.POST_CRASH)


# --------------------------------------------------------------------------
# the 0xFF surface
# --------------------------------------------------------------------------


def test_ff_accepts_exactly_nine_encodings():
    """Three command ids, nine CDW12 values, and no unmapped corner."""
    surface = oracle.ff_surface()
    assert set(surface) == {
        0x0003,
        0x0004,
        0x0007,
        0x0103,
        0x0203,
        0x0303,
        0x0403,
        0x0503,
        0x0603,
    }


def test_ff_0003_erases_the_boot_marker_record():
    """One mistyped nibble turns the read-only probe into an erase.

    `0x0004` is the first command in the runbook and is typed on healthy
    drives. `0x0003` posts an erase of EEPROM System-Area section 6, the
    244-byte boot-marker record -- which also trips the "System Area empty"
    latch predicate on the next start.
    """
    r = oracle.ff_classify(0x0003)
    assert (r.verb, r.section) == (oracle.VERB_ERASE, 6)
    assert r.classification == oracle.DESTRUCTIVE
    assert oracle.ff_classify(0x0004).classification == oracle.READ_ONLY


def test_ff_0403_is_an_ungated_factory_reinit():
    """0x0403 posts verb 0x25 with param 1 and tests nothing first."""
    r = oracle.ff_classify(0x0403)
    assert (r.verb, r.param) == (oracle.VERB_REINIT, 1)
    assert r.classification == oracle.CATASTROPHIC


def test_0503_resume_reaches_reinit_only_when_latched():
    """The wipe is gated on startup mode == 6, and nothing else gates it."""
    assert oracle.ff_resume_posts_reinit(5, mode=oracle.POST_CRASH)
    assert not oracle.ff_resume_posts_reinit(5, mode=1)


def test_0603_resume_never_reaches_reinit():
    """The single most operationally important fact about this family.

    `0x0603` erases the PFail dump and returns. There is no drive state and
    no CDW value that makes it schedule a re-init.
    """
    assert not oracle.ff_resume_posts_reinit(6, mode=oracle.POST_CRASH)
    assert not oracle.ff_resume_posts_reinit(6, mode=1)
    r = oracle.ff_classify(0x0603)
    assert (r.verb, r.section) == (oracle.VERB_ERASE, 10)
    assert not r.reinit_when_latched


def test_0503_and_0603_erase_different_sections():
    """CLOG (11) vs PFCL (10). UNEXSTRT stamps CLOG, which is why 0x0603 is inert."""
    assert oracle.ff_classify(0x0503).section == 11
    assert oracle.ff_classify(0x0603).section == 10


def test_0303_writes_the_sbl_eeprom_section():
    """0x0303 posts verb 1 (section write) against section 13, the SBL EEPROM.

    This was UNKNOWN until the lifter learned to step over the reserved-space
    op0=0 encodings on the arm (docs/sn200-tie-opcodes.md). It is a two-stage
    coroutine: the first entry preps a 64-byte scratch buffer and yields, and
    the request is built on the resume at static 0x300335f7. The completion
    handler it registers logs "OAM ERASE CMD: Erase to SBL EEPROM failed",
    which corroborates the section independently of the field decode.
    """
    r = oracle.ff_classify(0x0303)
    assert (r.verb, r.section) == (oracle.VERB_WRITE, 13)
    assert r.classification == oracle.CATASTROPHIC
    assert r.error is None
    assert oracle.FF_ENQUEUE in r.calls


def test_0303_walk_still_steps_over_undecoded_instructions():
    """...and the result is corroborated, not pure: the arm has opaque slots.

    Guards against a future spec change silently turning "we stepped over
    seven unknown instructions" into an unqualified proof.
    """
    assert oracle.ff_classify(0x0303).opaque > 0


# --------------------------------------------------------------------------
# the boot marker dispatch (PROC0)
# --------------------------------------------------------------------------


def _marker_route(marker: int) -> tuple[int | None, list[int]]:
    img = pcode.Image.load("PROC0")
    e = pcode.Emu(img, on_opaque="skip")
    e.setreg("a11", marker)
    e.setreg("a6", 0xDEADBEEF)  # the "empty System Area" literal, kept distinct
    e.setreg("a7", 0x7FF90000)
    e.setreg("a1", 0x7FF98000)
    end = e.run(MARKER_DISPATCH, max_steps=400, stop_on_call=True)
    return end, e.trace


def test_marker_9_routes_to_the_unexstrt_stub_writer():
    """Why a PFCL-only latch cannot be observed.

    The boot that latches writes 0x80000009 and falls into this dispatch,
    which stamps CLOG on that same boot -- so a drive you can probe is always
    both-armed, and 0x0603 alone can never release it.
    """
    _, trace = _marker_route(0x80000009)
    assert UNEXSTRT_STUB_WRITER in trace


def test_only_marker_9_reaches_the_stub_writer():
    """No other marker value stamps CLOG from this dispatch."""
    reaching = [
        m for m in range(16) if UNEXSTRT_STUB_WRITER in _marker_route(0x80000000 | m)[1]
    ]
    assert reaching == [9]


def test_started_markers_share_one_arm():
    """5 (normal STARTED), 6 (PFAIL STARTED) and 7 (PFAIL TIMEOUT) are one case.

    They are breadcrumbs, not verdicts: the dispatch treats all three the same
    "began, never finished" way.
    """
    ends = {m: _marker_route(0x80000000 | m)[0] for m in (5, 6, 7)}
    assert len(set(ends.values())) == 1
    assert _marker_route(0x80000001)[0] not in ends.values()


def test_read_only_marker_8_takes_the_clean_boot_arm():
    """Marker 8 is not a degraded mode -- it lands where CLEAN lands."""
    assert _marker_route(0x80000008)[0] == _marker_route(0x80000001)[0]


# --------------------------------------------------------------------------
# the triage script
# --------------------------------------------------------------------------


def _triage_ff_encodings() -> set[int]:
    """Every 0xFF CDW12 sn200-triage.sh actually sends."""
    import re

    path = os.path.join(
        os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "sn200-triage.sh"
    )
    with open(path) as f:
        text = f.read()
    # join shell line continuations so one invocation is one line
    joined = re.sub(r"\\\n\s*", " ", text)
    out = set()
    for line in joined.splitlines():
        if "--opcode=0xff" not in line.lower():
            continue
        for c in re.finditer(r"--cdw12=0x([0-9a-fA-F]+)", line):
            out.add(int(c.group(1), 16))
    return out


def test_triage_script_only_sends_read_only_vendor_commands():
    """The script must never emit an encoding the firmware says can mutate.

    This replaces a hardcoded allow-list: the classification comes from
    executing PROC8's own dispatch, so adding a command to the script without
    checking it fails here rather than on a drive.
    """
    sent = _triage_ff_encodings()
    assert sent, "found no 0xFF commands in sn200-triage.sh -- the parser has drifted"
    for cdw12 in sorted(sent):
        ok, why = oracle.is_read_only(0xFF, cdw12)
        assert ok, f"sn200-triage.sh sends 0xFF cdw12={cdw12:#06x}, which is {why}"
