# Executing the SN200 firmware instead of reading it

Every claim in the SN200 documents used to be somebody's careful read of a
byte stream. That process produced four errors serious enough to invalidate
operational advice — overlay call targets resolved in the wrong address space,
two `xdis.py` FLIX slot bugs (one of which corrupted a pointer), an agent that
fabricated instructions out of bundle-tail bytes, and `[ctx+0x48]` documented as
"the request code" when it is the EEPROM section id.

This toolchain replaces the read with an execution. The load-bearing claims are
now `pytest` assertions that re-derive themselves from `~/sn200fw/flat/*.bin` on
every run.

| Piece | What it is |
|---|---|
| `tools/sn200-fw/ghidra/languages/flix.sinc` | the SLEIGH extension: FLIX bundle decode for this core |
| `tools/sn200-fw/pcode.py` | builds the `.sla`, loads images, lifts to p-code, walks control flow, **executes** p-code |
| `tools/sn200-fw/sn200_oracle.py` | answers "what does opcode+CDW12 do?" by running PROC8's own dispatch |
| `tools/sn200-fw/tests/test_pcode.py` | the lifter cannot be confidently wrong |
| `tools/sn200-fw/tests/test_oracle.py` | the boot/latch claims, re-derived |

```sh
# once; not in pyproject's dev group because pypcode needs >=3.12 and the
# project still declares requires-python >=3.11
uv pip install --python .venv/bin/python pypcode
SN200_FW=~/sn200fw .venv/bin/python tools/sn200-fw/pcode.py PROC8@30000000 300335a3 300335c2 --pcode
SN200_FW=~/sn200fw .venv/bin/python tools/sn200-fw/sn200_oracle.py --gate --ff
SN200_FW=~/sn200fw .venv/bin/pytest tools/sn200-fw/tests/ -q
```

**No CPU emulator is involved.** QEMU and Unicorn both support only stock Xtensa
cores (DC232B/DC233C/fsf); this one is a custom FLIX/TIE configuration whose TIE
description is not public. What runs here is the *p-code* Ghidra's SLEIGH emits —
a few hundred instructions of the boot path, over a synthetic memory map.

---

## 1. The build, and why it just works

`pypcode` ships **both** the Ghidra decompiler's SLEIGH runtime **and** the
`sleigh` compiler binary (`pypcode/bin/sleigh`), and its bundled Xtensa language
files are **byte-identical** to Ghidra 12.1.2's. So `pcode.build_sla()` copies
pypcode's stock Xtensa directory, drops our `flix.sinc` over it, and compiles
with pypcode's own compiler — which guarantees the `.sla` format version matches
the lifter. No Ghidra installation is needed at all. Output is cached under
`tools/sn200-fw/ghidra/build/` (gitignored) and keyed on a hash of every spec
input, so editing `flix.sinc` rebuilds automatically.

`ghidra/install.sh` is unchanged and still installs the same `flix.sinc` into a
real Ghidra for interactive work. The two paths now share one spec file.

> **Trap.** An *identity* disassembly action — `off = flixA_imm8;` with no
> arithmetic — makes pypcode's `sleigh` **exit 1 with no diagnostic at all**.
> Write `off = flixA_imm8 + 0;`. Two hours went into that one.

---

## 2. What the spec gained, and the two bugs it found

Before this work `flix.sinc` decoded exactly one slot-A instruction (`l32r`).
Everything else in slot A — every load, every store, every call — lifted to an
opaque `flix_slotA_unknown` pseudo-op. Added here, all cross-checked
instruction-for-instruction against `xdis.py`:

- **op0=2, the LSAI class**: `l8ui l16ui l32i l16si l32ai s8i s16i s32i s32ri
  movi addi addmi`. This is what makes the EEPROM request fields readable —
  `s32i a15,a12,0x118` (the verb) and `s32i a7,a12,0x128` (the parameter) were
  previously invisible.
- **op0=5 and the RST0 indirect forms**: `call0/4/8/12`, `callx0/4/8/12`, `ret`,
  `retw`, `jx`. ~740 call sites across the images were missing from the call
  graph while this slot stayed opaque, including the OAM enqueue.
- **op0=0 RST0 arithmetic** (`and or xor add addx2/4/8 sub subx2/4/8`, `extw`)
  and the **narrow forms** (`l32i.n s32i.n add.n addi.n mov.n`).

Deliberately *not* decoded: the narrow branch forms (`beqz.n`/`bnez.n`) in slot
A. A bogus branch out of slot A would corrupt every control-flow claim built on
this spec, and no confirmed example exists.

### 2.1 Two spec defects found by building it — PROVEN

**Slot C bit 7 is `mov`, not `movi`.** `0xC <t> 8 <d>` is `mov a<d>,a<t>`. The
old rule read it as `movi a<t>,0x80|d`, which at `0x3003377a` writes the
constant 140 over `a5` — the very pointer the next bundle dereferences with
`s32i a11,a5,0x11c`. `docs/sn200-oam-dispatch.md` §1.2 flagged this by hand;
the spec now decodes it correctly and
`test_slot_c_high_byte_is_mov_not_movi` keeps it that way. `xdis.py` still
prints the wrong form (it carries a warning comment instead).

**Format-0xF bundles have no slot C at all — NEW.** The branch opcode index
`k` occupies bits 55-63, which is *inside* the field the 0xE format uses for
slot C (bits 48-63). The spec was building a slot C for both formats, inventing
a `movi`/`mov` register write out of the branch opcode at **846 sites across
the 18 images**. Any one of those would have silently overwritten a live
register in the decompiler. The 0xF constructor now builds slot A and slot B
only. This was not previously documented anywhere.

---

## 3. What is now PROVEN by execution

`Emu` in `pcode.py` interprets the lifted p-code over a flat memory that starts
out as the firmware image itself, so globals and literal pools read their real
values. Results below are produced by running code, not by reading it.

### 3.1 The Post-Crash gate

`sn200_oracle.py --gate` runs `Admin_CheckCmdAllowed` (`0x7ffa6b18`) once per
opcode with the startup-mode word forced to 6, and reads the verdict register:

```
post-crash gate admits on opcode alone: 0x00 0x01 0x02 0x04 0x05 0x06 0x08
                                        0x09 0x0a 0x0c 0x10 0x11 0xe6 0xec 0xff
  0xc6 additionally requires CDW12[7:0] in: 0x20 0x30
  0xca additionally requires CDW12[7:0] in: 0x02 0x03 0x04 0x08 0x0d 0x0e 0x0f
                                            0x10 0x11 0x13 0x21 0x32
```

**This reproduces `sn200-firmware-flow.md` §5 exactly**, including the 12-entry
`0xCA` sub-list, which no document had ever enumerated. It also confirms
`0xC6`/`0x30` is admitted alongside `0x20` — the pair a sibling investigation is
mapping. Outside mode 6 the gate is inert (`test_gate_is_inert_outside_post_crash_mode`).

### 3.2 The `0xFF` erase family, field by field

`sn200_oracle.py --ff` executes the command-id dispatch, then the sub-dispatch,
then reads the EEPROM request object the handler filled in:

| CDW12 | class | verb | section | note |
|---|---|---|---|---|
| `0x0003` | DESTRUCTIVE | 3 erase | 6 System Area | ☠ one nibble from the `0x0004` probe |
| `0x0004` | READ-ONLY | — | — | startup-mode probe |
| `0x0007` | READ-ONLY | — | — | read raw System Area |
| `0x0103` | DESTRUCTIVE | 3 erase | 3 bad block list | |
| `0x0203` | DESTRUCTIVE | 3 erase | 9 BIST script | |
| `0x0303` | **UNKNOWN** | — | — | undecodable, see §4 |
| `0x0403` | CATASTROPHIC | `0x25` re-init | — | param **1**, no startup-type gate |
| `0x0503` | CATASTROPHIC | 3 erase | 11 CLOG | resume posts verb `0x25` iff mode == 6 |
| `0x0603` | DESTRUCTIVE | 3 erase | 10 PFCL | resume posts nothing, ever |

Nine encodings; every other CDW12 is rejected with no side effect. This matches
`sn200-oam-dispatch.md` §4.1 arm for arm — the first time that table has been
confirmed by anything other than a careful read.

Two results that were previously INFERRED and are now **PROVEN**:

- `0x0403` stores **1** into request `+0x128` and `0x25` into `+0x118`, with no
  preceding test of the startup mode. The store is read off the executed
  p-code, not off a hand decode.
- `0x0503`'s resume posts verb `0x25` with `+0x128 = 0` **only** when
  `*(0x7ff87c64) == 6`; run it with mode 1 and it takes the plain completion
  tail. `0x0603`'s resume reaches the completion tail for *every* mode and
  every erase status — there is no reachable path to the re-init.

*(The `+0x128` **polarity** — that 1 means FACTORY and 0 means REINIT — is
still INFERRED: that selection happens in PROC0, in a slot-B ALU sub-opcode
this spec does not decode.)*

### 3.3 The boot marker dispatch — executed for every marker value

`0x7ffaae69` run once per marker `0x8000000N`:

| marker | route |
|---|---|
| 1 CLEAN, 2 PFAIL, 3 REINIT, 4 FACTORY, **8 READ ONLY** | the same normal-boot arm |
| 5, 6, 7 (shutdown STARTED / TIMEOUT) | one shared "began, never finished" arm |
| **9** | `0x7ffaad01`, the **UNEXSTRT stub writer** |
| 0, 10-15 | the default/invalid arm |

Marker 9 is the **only** value that reaches the stub writer
(`test_only_marker_9_reaches_the_stub_writer`), which is the mechanism behind
the withdrawn PFCL-only recovery in `sn200-oam-dispatch.md` §7.1: the boot that
latches on PFCL routes to marker 9 and stamps CLOG on that same boot, so a
drive you can probe is always both-armed.

Marker 8 landing on the *same* arm as marker 1 is the executable form of
"marker 8 is not a degraded mode" (`sn200-firmware-flow.md` §6).

> **Caveat on marker 0.** The dispatch also compares against `a6`, which the
> caller sets from context. The tests give `a6` a distinct sentinel so it
> cannot alias a real marker; the marker-0 route is therefore not characterised
> here.

### 3.4 Triage script self-validation

`tests/test_oracle.py::test_triage_script_only_sends_read_only_vendor_commands`
parses every `0xFF` encoding `sn200-triage.sh` actually emits and requires the
oracle — i.e. the firmware — to classify each as read-only. The script no
longer carries a hand-maintained allow-list of "safe" encodings; adding a
command without checking it fails in CI rather than on a drive.

---

## 4. What this lifter **cannot** do

Read this section before quoting anything above as proof.

**Custom TIE opcodes are undecodable, and always will be without the TIE
description.** Xtensa reserves `QRST op1=6` and `op1=7` for `CUST0`/`CUST1`, the
per-core TIE extension space. There are **3040** of them in slot A across the
images. Neither Ghidra's stock spec nor `xdis.py` can read them; they are not
"not yet decoded", they are *not describable* from public information.

**Do they affect the boot path?** Mostly no, and where they do the tooling says
so rather than guessing. Sweeping the eleven boot/latch functions:

| function | insns | opaque bundles |
|---|---|---|
| boot marker dispatch `0x7ffaae69` | 93 | 1 |
| UNEXSTRT stub writer `0x7ffaad01` | 51 | 0 |
| latch predicate `0x7ffaae2d` | 113 | 1 |
| force-marker-9 `0x7ffaaf08` | 95 | 1 |
| admin gate `0x7ffa6b18` | 132 | 2 |
| `0xFF` command-id dispatch | 50 | 6 |
| `0xFF` erase sub-dispatch | 60 | 6 |
| `0x0503` resume | 25 | 2 |
| `0x0603` resume | 17 | 1 |
| EEPROM submitter `0x7ffb4fec` | 23 | 6 |
| `0xFF` handler `0x7ffbc110` | 54 | 6 |
| **total** | **713** | **32 (4.5 %)** |

The one place it bites hard is **`0xFF`/`0x0303`**, the SBL EEPROM arm: after
its `memset` the code runs straight into three custom-TIE bundles and SLEIGH
cannot even determine instruction lengths, so the walk stops. The oracle reports
it as **UNKNOWN**, not as safe and not as the hand-decoded "verb 1, section 13,
permanent brick" — that claim stands on the hand decode alone and this toolchain
neither confirms nor refutes it. **Treat `0x0303` as catastrophic regardless.**

Other limits, each of them a way to be wrong if ignored:

- **`Emu` has two opacity policies and only one of them is honest.** The default
  raises `Opaque` and refuses to answer. `on_opaque="skip"` — which the oracle
  uses — steps over the undecoded slot and records it. A "skip" result is
  corroborated, not proven: it is trustworthy only because the answers it
  produces match three independent hand teardowns. If a future result depends on
  a skipped slot's register write, it will be wrong and nothing will say so.
- **Slot B ALU sub-opcodes other than `or` are undecoded.** This is what keeps
  the `0x0403`-vs-`0x0503` marker *polarity* at INFERRED.
- **No parallelism.** SLEIGH runs slot A, then C, then B sequentially. Real FLIX
  hardware reads all three slots' operands before any writes. A bundle where one
  slot reads a register another slot writes would lift wrongly. None has been
  observed on the boot path; none has been ruled out fleet-wide.
- **The register window is not modelled** for FLIX-bundled calls (no
  `swap8`/`restore8`). The call *edge* is trustworthy; register state across it
  is not.
- **All 34 overlays share one runtime window** at `0x7ffbc000`. A `0x7ffbxxxx`
  address does not name code until you say which overlay is resident —
  `Image.load("PROC8", overlay=22)` — and `pcode.overlay_for()` will tell you
  how ambiguous a given address is.
- **Calls are not entered by default.** Every result above is
  intraprocedural plus the specific callee identities resolved by the overlay
  delta rule.
- **Nothing here has touched hardware.** Static analysis and p-code execution
  only.

---

## 5. Where this should go next

- The `0xCA` sub-list is now enumerated but not *classified*: 12 admitted
  sub-opcodes, two of which (`0x0f` raw NAND erase, `0x10` raw page write)
  destroy a drive on one well-formed command. Running each through its handler
  the way §3.2 does for `0xFF` is the obvious next target.
- `0xC6`/`0x30` is admitted by the gate and remains unidentified. The oracle
  proves reachability; it does not yet walk the handler.
- `0xE6` and `0xEC` are admitted on the opcode alone and have never been walked.
- The `0x0303` custom-TIE wall is worth one attempt at inference from
  surrounding context, clearly labelled SPECULATIVE if it happens.
