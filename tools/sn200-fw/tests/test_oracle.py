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
# the 0xCA surface -- the family that holds the two drive-destroying commands
# --------------------------------------------------------------------------


def test_ca_jump_table_implements_thirty_nine_of_sixty_seven():
    """The dispatcher, executed once per command byte.

    Published tables said 37; the executed table says 39, because the two
    inline arms 0x05/0x06 load no overlay handler and were miscounted.
    """
    t = oracle.ca_table()
    assert len(t) == 39
    assert set(t) == {
        0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x08, 0x09, 0x0A,
        0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x20,
        0x21, 0x22, 0x25, 0x26, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37,
        0x38, 0x39, 0x3A, 0x3B, 0x3E, 0x3F, 0x40, 0x41, 0x42,
    }  # fmt: skip
    assert all(sub < oracle.CA_TABLE_LEN for sub in t)


def test_ca_gate_allow_list_contains_no_dead_command_byte():
    """Every value a latched drive still accepts routes to a real handler."""
    _, sub = oracle.gate_allow_list()
    assert sub[0xCA] <= set(oracle.ca_table())


def test_ca_0x33_and_0x37_differ_only_in_the_overlay():
    """A runtime handler pointer does not identify code on its own.

    Both arms load 0x7ffbc68c. Reading the pointer without the overlay index
    the same arm stores would merge the wafer-lot-ID reader with the
    multiplane write/erase handler.
    """
    a, b = oracle.ca_table()[0x33], oracle.ca_table()[0x37]
    assert a.handler_rt == b.handler_rt == 0x7FFBC68C
    assert (a.overlay, a.static) == (28, 0x30038C44)
    assert (b.overlay, b.static) == (26, 0x30035A04)


def test_ca_0f_block_erase_has_no_harmless_sub_value():
    """CONFIRMED by execution, not by reading: CDW12[15:8] is ignored.

    All 256 values of the sub byte produce a byte-identical instruction trace
    through the erase coroutine, and the byte is never even addressed anywhere
    in the handler body. Every well-formed 0xCA/0x0F erases a NAND block.
    """
    r = oracle.ca_classify(0x0F)
    assert r.classification == oracle.DESTRUCTIVE
    assert r.gate_admitted
    assert r.reads_sub_byte is False
    assert oracle.ca_erase_ignores_sub_byte()


def test_ca_10_page_program_reads_the_sub_byte_and_accepts_three_values():
    """CONFIRMED: 0x0010/0x0110 program, 0x0210 shares their coroutine.

    Sub 2 takes the SAME first-entry arm as sub 1 and only parts company after
    the host->DDR transfer, so "it merely fetches a result dword" is true of
    where it ends up and false of how it gets there.
    """
    r = oracle.ca_classify(0x10)
    assert r.classification == oracle.DESTRUCTIVE
    assert r.gate_admitted
    assert r.reads_sub_byte is True
    arms = oracle.ca_write_sub_arms()
    assert arms[0] == oracle.CA_WRITE_ARM_0
    assert arms[1] == arms[2] == oracle.CA_WRITE_ARM_12
    assert arms[3] == arms[0xFF] == oracle.CA_WRITE_ARM_REJECT


def test_ca_raw_read_clamps_to_640_bytes_and_the_writers_do_not():
    """The clamp the recovery estimate rests on, executed.

    0xCA/0x03 asks for anything and gets 640 bytes. Neither 0x0F nor 0x10
    contains the clamp idiom at all -- the write path has no absolute length
    bound, only a CDW10*4 == bytes-transferred consistency check.
    """
    assert oracle.ca_rawread_clamp(request=0x10000) == 640
    assert oracle.ca_has_length_clamp(0x03)
    assert not oracle.ca_has_length_clamp(0x10)
    assert not oracle.ca_has_length_clamp(0x0F)


def test_ca_erase_pwr_char_is_0x12_alone():
    """0x12 owns the VUC_ERASE_PWR_CHAR strings; 0x20 has none of its own.

    Published tables label 0x20 VUC_ERASE_PWR_CHAR too. Attributed to the
    handler bodies the dispatcher actually selects, both strings land in
    0x12's body and 0x20's 1060-byte body carries no string at all -- so 0x20
    is unidentified, not a second erase arm.
    """
    twelve = oracle.ca_classify(0x12)
    assert twelve.classification == oracle.DESTRUCTIVE
    assert any("ERASE_PWR_CHAR" in s for _, _, s in twelve.strings)
    twenty = oracle.ca_classify(0x20)
    assert twenty.strings == []
    assert twenty.classification == oracle.UNKNOWN


def test_ca_nand_get_set_pairs_are_one_value_apart():
    """0x38/0x39 (ONFI features) and 0x3A/0x3B (test-mode register).

    The getter of each pair is one keystroke from a writer of NAND die
    configuration. Neither writer is on the Post-Crash allow-list, so this is
    a healthy-drive hazard.
    """
    for getter, setter in ((0x38, 0x39), (0x3A, 0x3B)):
        assert oracle.ca_classify(getter).classification == oracle.READ_ONLY
        assert oracle.ca_classify(setter).classification == oracle.CATASTROPHIC
        assert not oracle.ca_classify(setter).gate_admitted
        assert (setter, "nibble") in oracle.ca_neighbours(getter)


def test_ca_allow_listed_unknowns_are_reported_as_unknown():
    """The discipline that solved 0xFF/0x0303: say UNKNOWN, do not guess.

    These five are reachable on a latched drive and carry no log string in
    their handler bodies. Two of them take the flash-operation lock, so they
    are not inert either.
    """
    unknown = {
        s
        for s, r in oracle.ca_surface().items()
        if r.classification == oracle.UNKNOWN and r.gate_admitted
    }
    assert unknown == {0x04, 0x11, 0x13, 0x21, 0x32}
    assert oracle.ca_classify(0x04).takes_flash_lock
    assert oracle.ca_classify(0x13).takes_flash_lock
    # 0x11's only callee is the helper the block-erase arm hands CDW13 to.
    assert oracle.ca_classify(0x11).calls == [oracle.FLASH_ADDR_HELPER]
    assert oracle.FLASH_ADDR_HELPER in oracle.ca_classify(0x0F).calls


def test_ca_every_allow_listed_sub_has_a_dangerous_one_nibble_neighbour():
    """Mechanically enumerated instead of discovered one incident at a time.

    Of the twelve command bytes a latched drive accepts, only 0x21 has no
    destructive neighbour a single hex digit away.
    """
    _, sub = oracle.gate_allow_list()
    without = {s for s in sub[0xCA] if not oracle.ca_neighbours(s)}
    assert without == {0x21}
    assert (0x0F, "nibble") in oracle.ca_neighbours(0x03)
    assert (0x10, "+-1") in oracle.ca_neighbours(0x0F)


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


TRIAGE = os.path.join(
    os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "sn200-triage.sh"
)


def _triage_lines() -> list[str]:
    """sn200-triage.sh with shell line continuations joined, so one
    admin-passthru invocation is one line."""
    import re

    with open(TRIAGE) as f:
        return re.sub(r"\\\n\s*", " ", f.read()).splitlines()


def _triage_encodings(opcode: int) -> set[int]:
    """Every CDW12 sn200-triage.sh sends with this opcode."""
    import re

    out = set()
    for line in _triage_lines():
        if f"--opcode=0x{opcode:02x}" not in line.lower():
            continue
        for c in re.finditer(r"--cdw12=0x([0-9a-fA-F]+)", line):
            out.add(int(c.group(1), 16))
    return out


def _triage_ff_encodings() -> set[int]:
    return _triage_encodings(0xFF)


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


def test_triage_script_never_emits_a_0xca_command_at_all():
    """0xCA must stay off the triage script by construction, not by luck.

    Twelve 0xCA command bytes survive the Post-Crash gate, so they are exactly
    the ones a latched-drive script could reach; two of them destroy the drive
    on one well-formed command and five more are unidentified. No 0xCA
    encoding is cleared, so the rule is a blanket one: the script does not
    emit this opcode. `is_read_only` deliberately does not model 0xCA, so
    there is no way to argue an exception past this test.
    """
    assert _triage_encodings(0xCA) == set()
    for line in _triage_lines():
        low = line.lower()
        assert "--opcode=0xca" not in low, f"sn200-triage.sh emits 0xCA: {line.strip()}"
        assert "opcode=202" not in low, f"sn200-triage.sh emits 0xCA: {line.strip()}"
    ok, why = oracle.is_read_only(0xCA, 0x0003)
    assert not ok and "not modelled" in why
