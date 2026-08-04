# Xtensa FLIX decoding for SN200 firmware

How the HGST/WDC Ultrastar SN200 controller's FLIX/VLIW bundles are encoded,
how that was established, and how to make Ghidra decode them at the right
length. Every claim is labelled **PROVEN**, **INFERRED** or **SPECULATIVE**.

**Bottom line:** bundle *width* is solved (8 bytes) and that is enough to keep
the instruction stream in sync, which is the fix that removes fabricated
instructions. The *contents* of a bundle are still opaque, and roughly **half
of all executable bytes are bundles**, so decompiler output remains
substantially incomplete. Read "What this does not fix" before relying on any
decompilation of FLIX-heavy code.

## Summary of the format

| Property | Value | Confidence |
|---|---|---|
| Bundle length | **8 bytes**, for both formats | PROVEN |
| Format selector | `op0` (low nibble of byte 0) = `0xE` or `0xF` | PROVEN |
| Number of formats | 2 (`0xE` ~72% of bundles, `0xF` ~28%) | PROVEN |
| Bundle alignment | none — bundles start at arbitrary byte offsets | PROVEN |
| Slot 0 layout | bits 4–23 mirror the base-ISA 24-bit operand layout | INFERRED |
| ↳ bits 8–23 | `l32r`-class 16-bit literal offset, base-ISA formula | INFERRED |
| ↳ bits 4–7 | destination address register for that op | INFERRED |
| `0xE` bits 48–63 | `0xc090` in 68% of bundles — likely a NOP'd slot | SPECULATIVE |
| Remaining slots | not recovered | — |

Xtensa reserves `op0 = 0xE` and `0xF` for wide/FLIX instructions and leaves the
encoding to the per-core TIE configuration, so there is no generic answer —
this had to be solved from the binaries.

### ⚠ If you write a literal/xref scanner, scan slot A at **every byte offset**

"Bundle alignment: none" above is not a footnote — it is the single easiest way
to produce a false negative. A scanner that walks 8-byte bundles at 4-aligned
offsets only (the natural thing to write) silently misses every bundle that
starts at an odd address, and a missed `l32r` looks exactly like "this constant
is never loaded".

Ground truth to validate any such scanner against: PROC0 `0x7ffa430f`,
`0x7ffa4317`, `0x7ffa431f`, `0x7ffa4327` are four consecutive 8-byte bundles at
addresses ≡ 3 (mod 4). **`0x7ffa431f` must appear as `l32r a11,0x7ff82b54`.** If
your sweep does not find it, it is aligned-only and its negative results are
worthless. (This cost a retraction during the marker-8 work; the corrected sweep
also turned up a third loader, `0x7ffa4732`, that the aligned scan had hidden.)

Note the target formula still uses `((pc + 3) & ~3)`, so the bundle's own
(unaligned) PC must be fed in — do not round it first.

## Evidence

Reproduce everything with:

```
python3 tools/sn200-fw/flix_analysis.py [/Users/nep/sn200fw/flat]
```

Corpus: all 18 flat images (`FCC`, `PROC0`–`PROC15`, both PROC8 banks),
~1.9 MB total, from firmware `KNGND122`.

### Test 1 — forward branch spans (PROVEN)

A conditional branch at `X` targeting `T` is ground truth: the instruction
lengths from `X` to `T` must sum exactly to `T`. Constraints were only taken
where `X` was reached from an `entry` prologue through a **FLIX-free** prefix
(so `X` is certainly a real boundary) and where the span contains at least one
bundle (so the constraint says something about FLIX width).

| Width | Informative spans | Landed exactly on target |
|---|---|---|
| 4 | 124 | 73 (58.9%) |
| 5 | 148 | 111 (75.0%) |
| 6 | 124 | 68 (54.8%) |
| 7 | 121 | 56 (46.3%) |
| **8** | **170** | **148 (87.1%)** |
| 9 | 125 | 66 (52.8%) |
| 12 | 122 | 55 (45.1%) |
| 16 | 157 | 72 (45.9%) |

On the three images with the most constraints, width 8 is *perfect*: 10/10
(PROC8 overlay), 31/31 (PROC8 main), 36/36 (PROC0).

### Test 2 — call targets (PROVEN, independent)

Uses no branch information at all. An Xtensa `callN` target should be the
address of an `entry` prologue.

| Width | Call targets found | Landed on `entry` |
|---|---|---|
| 4 | 820 | 540 (65.9%) |
| 5 | 1136 | 889 (78.3%) |
| 6 | 903 | 602 (66.7%) |
| **8** | **1179** | **1063 (90.2%)** |
| 12 | 807 | 555 (68.8%) |
| 16 | 968 | 726 (75.0%) |

Width 8 scores **100%** on 12 of the 18 images (e.g. 152/152, 200/200,
103/103, 154/154). No other width exceeds 78% anywhere in aggregate.

Two objective functions with no shared inputs both select 8, so the width is
settled.

### Both `0xE` and `0xF` are 8 bytes (PROVEN)

A 2-D scan over (width for `op0=0xE`) × (width for `op0=0xF`) peaks at
(8, 8) = 87.1%. Next best is (8, 2) at 79.9%. Both opcodes are common —
17,939 and 7,124 bundles respectively in a recursive-descent sweep — so both
are genuine bundle formats.

### Slot 0 carries an `l32r`-class literal reference (INFERRED)

Noticed while validating: bundle-derived literal addresses slot exactly into
the consecutive literal pool used by the surrounding real `l32r` instructions
(`…3360`, `3364`, `3368`, `336c`, `3370` — the `3360` and `3370` entries come
from bundles).

Tested properly. Ground truth `L` = literal addresses referenced by real
base-ISA `l32r`s. For each candidate 16-bit field in the bundle, apply the
Xtensa `l32r` formula `target = ((pc+3) & ~3) + imm16*4 - 0x40000` and count
exact hits in `L`:

| Field | Exact hits / 25063 | Rate |
|---|---|---|
| **bits(8,23)** | **2109** | **8.41%** |
| bits(48,63) | 180 | 0.72% |
| bits(16,31) | 162 | 0.65% |
| random imm16 (null) | 122 | 0.49% |
| **imm shuffled between bundles (permutation null, 20 runs)** | — | **0.41%** |

`bits(8,23)` is **20× the permutation null** and 12× the next-best field. It is
*exactly* where the base ISA puts `l32r`'s `imm16` in a 24-bit instruction.

Supporting detail: for bundles whose `bits(8,23)` literal hits a real pool
entry, the adjacent `bits(4,7)` field never selects `a0` or `a1` (0.0% each,
against 0.7% / 0.1% base rates) — precisely what an `l32r` *destination*
register field should look like, since you do not load literals into the
return address or stack pointer.

About 19% of bundles produce an address within 256 bytes of a known literal
pool, so roughly one bundle in five appears to carry an `l32r` in this slot.

This is labelled INFERRED, not PROVEN: it is a strong statistical association
consistent with slot 0 reusing the base-ISA operand layout, but no individual
bundle has been confirmed against ground-truth execution.

### What was looked for and NOT found

- ~~**Control flow inside bundles.**~~ **RETRACTED — this was wrong, and it is
  the most damaging error in this document's history.** Bundles carry a full
  branch slot: `op0=0xF` slot B is a branch, and `op0=0xE` with `pre=3` is a `j`
  (both now decoded — see "Real p-code semantics for slots A/B/C" below, and
  `xdis.py:flix_branch`). Essentially **every dispatch chain in the SN200
  firmware lives in those slots**.

  The original sweep found no signal because it looked for a single fixed
  PC-relative field across all bundles, while the displacement's position and
  width depend on the slot-B class selector.

  Consequences of having believed it, all real: a linear sweep that cannot see
  these edges makes a conditional path look unconditional, and it made an NVMe-MI
  **reject**-list read as an **allow**-list in `sn200-independent-re.md` §10.1.
  **Never infer "no branch here" from a decode that does not resolve slot B.**
- **Still undecoded:** `op0=0xE` with `pre=1` (~835 bundles in PROC9). Tested for
  a PC-relative field; none found. Probably not control flow — but "probably not"
  is exactly what was said above, so treat any CFG built over these as
  incomplete.
- **Slot boundaries.** Per-bit entropy over 25k bundles shows no clean
  segmentation for either format. The one structural hint is that `op0=0xE`
  bundles have `bits(48,63) == 0xc090` 68% of the time (`op0=0xF` peaks at
  `0x0a80`, 35%), consistent with a NOP'd top slot, but the slot's width and
  encoding are unknown.

### Corroboration

The pre-existing hand-rolled disassembler `tools/sn200-fw/xdis.py` had already
independently assumed 8 bytes for `op0 ∈ {0xE, 0xF}`. This analysis confirms
that choice on a corpus-wide basis. Note that `xdis.py` additionally prints
speculative per-slot decodes; of those, only the `l32r` field has supporting
evidence here — its branch/second-slot guesses do **not**, and should not be
relied on.

## The fix

Stock Ghidra 12.1.2 defines the bundle in
`Ghidra/Processors/Xtensa/data/languages/flix.sinc` as a **3-byte** opaque
pseudo-instruction:

```
:FLIX flix_i20 is op0=0xe & flix_i20 { flix(); }
```

`flix_i20` is a 20-bit field of the 24-bit `insn` token, so the instruction is
3 bytes long. Since real bundles are 8, Ghidra resumes decoding 5 bytes inside
the bundle and manufactures instructions from payload — including floating
point ops in integer control code.

The replacement (`tools/sn200-fw/ghidra/languages/flix.sinc`) introduces a
64-bit token and matches both opcodes with one pattern (`bits(1,3) == 0b111`
covers `0xE` and `0xF`):

```
define token flixtok(64)
    flix_op0  = (0,3)
    flix_op0h = (1,3)
    flix_lo   = (0,31)
    flix_hi   = (32,63)
;
define pcodeop flix_bundle;
:flix.8 flix_hi^","^flix_lo is flix_op0h=7 & flix_hi & flix_lo {
	flix_bundle(flix_hi:4, flix_lo:4);
}
```

The bundle stays **opaque on purpose**. Emitting a guessed `l32r` for the ~19%
of bundles that appear to have one would fabricate operations for the other
81%, and the remaining slots are unknown, so any p-code beyond "something
happened here" would be fiction.

### Install

```
tools/sn200-fw/ghidra/install.sh [/path/to/ghidra]        # default: ~/Downloads/ghidra_12.1.2_PUBLIC
tools/sn200-fw/ghidra/install.sh /path/to/ghidra undo     # revert
```

It backs up the stock spec to `flix.sinc.stock`, installs the replacement and
recompiles with `support/sleigh -a`. Then:

1. **Restart Ghidra** — a running instance caches the compiled `.sla`.
2. **Existing programs keep their stale disassembly** in the project database.
   Per program: Select All → Clear Code Bytes (`C`) → re-run Auto Analysis.
   Re-importing is equivalent.

Because this edits the Ghidra installation directory, it must be re-applied
after a Ghidra upgrade.

## Validation

Headless before/after, identical settings, stock Ghidra auto-analysis, binary
loader at the correct base. Metrics from `tools/sn200-fw/ghidra/FlixMetrics.java`.

| Image | Metric | Before | After |
|---|---|---|---|
| PROC8_30000000 | functions | 14 | **94** |
| | instructions | 98 | **1661** |
| | FLIX bundles | 12 | 510 |
| | flow targets | 10 | 272 |
| | targets off boundary | 3 (**30.0%**) | 18 (**6.6%**) |
| | in-function gaps | 4 (94 B) | 3 (91 B) |
| PROC8_7ff80000 | functions | 284 | **308** |
| | instructions | 2462 | **8110** |
| | FLIX bundles | 204 | 2111 |
| | flow targets | 183 | 1036 |
| | targets off boundary | 12 (**6.6%**) | 2 (**0.19%**) |
| | FP instructions | 9 | 8 |
| PROC13_7ff80000 | functions | 124 | **153** |
| | instructions | 1415 | **4266** |
| | flow targets | 78 | 288 |
| | targets off boundary | 6 (**7.7%**) | 2 (**0.69%**) |
| | FP instructions | 8 | **1** |

The task brief's baseline of 14 functions in PROC8_30000000 is reproduced
exactly, and rises to 94.

The headline number is the **off-boundary branch/call target rate**, the direct
measure of stream desync: 30% → 6.6%, 6.6% → 0.19%, 7.7% → 0.69%. Absolute
counts rise in one case only because 27× more code is now reachable.

### Spot check at `0x3003353c`

Before (per the task brief), Ghidra produced address gaps at `…49`, `…50`,
`…5b`, `…61`, `…65` and an `add.s f8,f0,f0` inside an erase handler. After:

```
3003353c: 36 61 00  entry a1,0x30
3003353f: 5d 02     mov.n a5,a2
30033541: f8 65     l32i.n a15,a5,0x18
30033543: 61 88 ff  l32r a6,0x30033364
30033546: 2e 86 ff b1 78 81 00 c7   { FLIX bundle }
3003354e: cf 25 5d 02 7f 00 80 0a   { FLIX bundle }
30033556: a0 0f 00  jx a15
30033559: 59 21     s32i.n a5,a1,0x8
3003355b: a5 8a f3  call8 0x30026e04
3003355e: 5f 0a 06 00 7a 14 00 0c   { FLIX bundle }
30033566: 91 80 ff  l32r a9,0x30033368
30033569: ae a0 02 c2 02 c0 90 c0   { FLIX bundle }
30033571: 82 2c 62  l32i a8,a12,0x188
30033574: 16 78 04  beqz a8,0x300335bf
30033577: a1 7d ff  l32r a10,0x3003336c
3003357a: 65 36 f8  call8 0x3002b8e0
3003357d: a2 d5 01  addmi a10,a5,256
30033580: 9e 7c ff a1 ac 6a 90 c0   { FLIX bundle }
30033588: b2 1a 37  l16ui a11,a10,0x6e
3003358b: 90 bb 20  or a11,a11,a9
3003358e: b2 6a 2d  s32i a11,a10,0xb4
```

No gaps, no floating point, coherent calls, and the `l32r` literals form a
clean consecutive pool.

**Correction to the task brief:** the `bnall` at `0x3003354d` that was cited as
independent confirmation was itself fabricated — those bytes lie inside the
bundle starting at `0x30033546`. Its apparent target `0x30033576` was
therefore also spurious, which is why nothing was there. This does not affect
the conclusion; it is an example of how convincing the desynced output was.

## What the length-only fix did NOT fix (historical — superseded below)

The bundle-length fix above shipped with the bundle rendered as one **opaque**
`flix_bundle(...)` pseudo-op: no register reads/writes, no branches. That is
no longer the current state of `flix.sinc` — see "Real p-code semantics for
slots A/B/C" below — but the list is kept here because it explains *why* the
follow-up work happened, and because slot A still falls back to the same
opaque behaviour for every op0 other than `l32r`.

1. Roughly half of all executable bytes are FLIX bundles.
2. **No p-code semantics** meant the decompiler could not see roughly half of
   all conditional control flow — slot B carries a real branch on this core —
   which is a materially dangerous failure mode: straight-line code appears
   where a conditional exists. This was a live risk in this project's own
   conclusions before the fix below.
3. Residual off-boundary targets, unproven-absent in-bundle control flow, and
   `call0`-ABI leaf function under-counting (all still true, see below).

## Real p-code semantics for slots A/B/C

`tools/sn200-fw/xdis.py` / `disany.py` decode slots A, B and C well enough to
be ground truth (see the corrections list in
`docs/sn200-independent-re.md`'s "Addendum: re-derivation with slot-B-aware
FLIX decoding" — that addendum is itself downstream of a previous version of
this file and is more current than the tables above for slot B/C specifics).
`flix.sinc` now mirrors that decoder as real SLEIGH constructors instead of
one opaque pseudo-op, in three independent sub-tables (`flixSlotA`,
`flixSlotB_F`/`flixSlotB_E`, `flixSlotC`) built in that order — slot A and C
always execute before slot B's `goto`, matching VLIW "all slots fire, then PC
updates" semantics even when B branches away.

**Two bundle formats, both op0 ∈ {0xE, 0xF}, dispatched by the exact op0
nibble** (not just `bits(1,3)==0b111`, since the two formats' slot B layouts
are unrelated):

- **op0 = 0xF (~28% of bundles): slot B is the branch slot.** `r@28-31`,
  `s@32-35`, signed `imm18@36-53`, mnemonic index `k@55-63` (1-based into
  `xdis.py`'s `BRK` table). All 24 `BRK` mnemonics are implemented with real
  p-code (`if (...) goto rel;`), split into register, B4CONST/B4CONSTU
  immediate, compare-zero, mask (`ball`/`bany`, `1<<r`), and bit-test
  (`bbc`/`bbci`/`bbs`/`bbsi`, also `1<<r`) forms. `k` outside `BRK`'s range
  falls back to an explicit `flix_slotB_unknown(k)` pcodeop call.
- **op0 = 0xE (~72% of bundles): slot B is j / addi / movi(12-bit) / mov / or.**
  Selected by a 2-bit `pre` field at bits 46-47: `pre=3` is an unconditional
  `j` (18-bit signed disp @28-45, real `goto`); `pre=1` is `addi at,as,imm4`
  (confirmed at `0x7ffb4fda`, `docs/sn200-independent-re.md` addendum A.4);
  `pre=2` splits by an opcode nibble @44-47 into `movi at,imm12` (nibble
  `0x8`, zero-extended — sign is unconfirmed), `mov at,as` (6-bit check
  `0x23` @40-45), and an ALU class (nibble `0x9`) of which only `sub=0xE`
  (`or`) is confirmed; every other ALU sub and every other `pre`/opcode
  combination falls back to `flix_slotB_unknown`/`flix_slotB_unknown_alu`.
  **This format's slot B is *not* a conditional branch** except via `j` —
  the conditional branches all live in format 0xF's slot B.
- **Slot C** (both formats, bits 48-63): `movi at,imm8`, class nibble `0xC`
  @60-63, `t@56-59`, `imm8@48-55`, zero-extended. `0xC090` (t=a0, imm8=0x90)
  is modelled as a true no-op (no p-code at all), matching the ~68%
  NOP'd-slot finding — assigning `a0 = 0x90` on every one of those bundles
  would have been a worse, silently-wrong alternative.
- **Slot A**: only `op0=1` (`l32r`, the base-ISA formula applied to bundle
  bits 8-23, dest register bits 4-7) has real semantics. Every other op0 —
  including the very common `op0=2` (base-ISA `movi`/load/store family) —
  still falls back to `flix_slotA_unknown(op0)`. This was priority (c) in the
  task and was deliberately not chased further so as not to risk (a)/(b).

### Validation against hand-traced ground truth

Done with a disposable headless Ghidra project (`analyzeHeadless`), not the
shared interactive instance — a live GUI session with unrelated open work was
running under the same MCP bridge and restarting it to pick up the new `.sla`
would have risked that work. A fresh headless process has no cached-`.sla`
problem to begin with.

- **`0x3003353c` (OAM erase handler).** The `cVar1`/erase-sub-command `beqi`
  ladder decompiles as a real `if`/`else if` chain (previously would have
  been straight-line). The crash-dump-only second call
  (`FUN_30030aa0` gated on `*DAT_30033350 == 6`, i.e. mode 6) is correct
  **when the block at `0x300335ca` is decompiled directly** — see the caveat
  below.
- **`0x7ffa6b30` (Post-Crash admin gate).** Decompiles as an **allow-list**:
  a chain of `if (param_2 == 0x..) goto LAB_7ffa6cfb;` covering exactly the
  opcode set in `docs/sn200-independent-re.md`'s membership table (`0x00,
  0x01, 0x02, 0x04, 0x05, 0x06, 0x08, 0x09, 0x0a, 0x0c, 0x10, 0x11, 0xec,
  0xff, 0xca`, plus the `0xca` sub-list on `param_3`). This was previously
  invisible; the fix makes the allow-list polarity visually unambiguous.
- **`0x7ffaae35` / `0x7ffaae3d` (PROC0 latch tests).** Both decompile to
  `if ((~unaff_a9 & 1) == 0) { LAB_7ffaaf02: ... }` and
  `if ((~unaff_a9 & 4) == 0) goto LAB_7ffaaf02;` — the `ball` mask semantics
  (`1<<r`) are correct and **both branches converge on the same label**,
  which falls through into the forced-marker write at `0x7ffaaf08`.
- **`0x7ffabbf0` (PROC0 marker-3 writer).** The `bbci a9,0,0x7ffabd22` gate at
  `0x7ffabcc6` is a plain base-ISA instruction (unaffected by this spec) and
  decompiles correctly as `if ((*(uint *)(param_1 + 0x78) & 1) != 0) { ... }`
  **when the block at `0x7ffabcc3` is decompiled directly** — see the caveat
  below.

**Caveat found during validation, orthogonal to this spec:** all four target
functions contain a `jx aN` computed jump (`0x30033556`, `0x7ffabc08`, and
similar in the admin-gate/latch functions) implementing a multi-way dispatch.
Ghidra's decompiler cannot recover the jump table (`Could not recover
jumptable ... Too many branches`) and falls back to treating the indirect
jump as a call that returns immediately, truncating the decompiled function
there. This is a pre-existing Ghidra limitation for computed jumps, present
before this work and unrelated to FLIX slot semantics — it affects any
Xtensa binary, FLIX or not. **Practical consequence: decompiling the outer
entry point (e.g. `0x3003353c` or `0x7ffabbf0`) will not show the code past
the `jx`.** To see it, decompile the specific dispatch target directly (force
a function there, e.g. via the MCP `create_function` + `decompile_function`
tools) — the branch semantics inside that block are then correct, as shown
above.

## What this still does not fix

1. **Slot A is opaque for every op0 except `l32r` (1)** — most commonly
   `op0=2` (movi/load/store), which is common in every function shown above
   (`flix_slotA_unknown(2)` calls). Do not trust any dataflow through a
   register that a decompiled function only ever seems to read, never write —
   it may be written by an unresolved slot A.
2. **Most of slot B format 0xE's ALU class is opaque** — only `sub=0xE`
   (`or`) is confirmed; any other value shows as
   `flix_slotB_unknown_alu(sub)` with no effect modelled.
3. **The `jx`/computed-jump function-boundary limitation above** — applies
   independently of this spec but was encountered in every one of the four
   validation targets, so it is likely to recur. Ground truth for code
   reachable only through a computed jump still requires `disany.py`, or
   manually decompiling the target block.
4. **Slot ordering is sequential in p-code, not truly parallel** — A, then C,
   then B. If a real bundle ever has slot B read a register that slot A or C
   in the *same* bundle also writes, the emitted p-code will see the
   post-write value instead of the true pre-bundle value. Not observed in any
   confirmed case, not exhaustively ruled out.
5. **`movi at,imm12` (slot B, format 0xE) is treated as zero-extended.** The
   only confirmed example uses a small positive value; if this is ever seen
   producing an implausibly large positive constant where a small negative
   one would make more sense, revisit the sign.
6. Everything in the original "What this does NOT fix" list that isn't
   superseded above: off-boundary target residue, unproven-absent in-bundle
   control flow beyond what's now decoded, and `call0`-ABI leaf function
   under-counting.

## Files

| Path | Purpose |
|---|---|
| `tools/sn200-fw/flix_analysis.py` | reproduces all width/slot evidence above |
| `tools/sn200-fw/ghidra/languages/flix.sinc` | the SLEIGH fix (length + slot A/B/C semantics) |
| `tools/sn200-fw/ghidra/languages/flix.sinc.orig` | stock Ghidra 12.1.2 spec, for reference |
| `tools/sn200-fw/ghidra/install.sh` | install / undo + recompile |
| `tools/sn200-fw/ghidra/FlixMetrics.java` | headless validation metrics script |
| `tools/sn200-fw/xdis.py` | ground truth for slot A/B/C decode (owned elsewhere) |

## Next steps for cracking the slots

Ordered by expected value:

1. Confirm the slot-0 `l32r` inference against a running target or the crash
   dump path (`tools/sn200-fw/pull-crash-dump.sh`) — a single confirmed
   register value settles it.
2. Diff `PROC2`/`PROC3`/`PROC4`/`PROC5`, which are near-identical images. Small
   deltas between otherwise identical bundles isolate individual fields.
3. Look for a Tensilica TIE/config blob or an `xtensa-esp*-elf` style toolchain
   description in the vendor firmware package; the config is the ground truth
   and would make the remaining slots mechanical.
