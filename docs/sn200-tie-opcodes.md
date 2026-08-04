# The SN200's custom instruction space — what it is, and what it cost us

The SN200's Xtensa core is a custom FLIX/TIE configuration. Its TIE description
is the chip designer's property (PMC-Sierra / Microsemi Flashtec lineage) and is
not public; there is no file to find and this document does not pretend to have
one. What it does is bound the problem: **which encodings are custom, how many
there are, how long they are, what shape their operands have, and — the part
that mattered — which of them were actually blocking anything.**

Headline result: **they were not blocking `0xFF`/`0x0303`.** The instruction
that stopped the lifter at `0x30033673` is not a TIE instruction at all. Once
the spec could step past it, `0x0303` resolved by execution, and the most
dangerous entry in `sn200-command-reference.md` moved from **UNKNOWN** to
**PROVEN**. See §5.

Labels: **PROVEN** (re-derivable by running a tool in this repo), **INFERRED**
(statistical or structural argument with a stated margin), **SPECULATIVE**
(a guess — nothing is built on these).

---

## 1. Where the custom space lives — PROVEN

### 1.1 CUST0 is `QRST op1=6`, CUST1 is `QRST op1=7`

Settled, and the "op1=4" reading that was circulating is wrong.

The Xtensa base ISA puts `op0` in bits 0–3; `op0=0` selects the **QRST** group,
whose sub-opcode is `op1` (bits 16–19). Ghidra's stock Xtensa processor module
— shipped inside `pypcode`, and the same files this project compiles its `.sla`
from — carries the assignment verbatim in
`processors/Xtensa/data/languages/cust.sinc`, quoting the Cadence manual:

```
# Per the manual:
# CUST0 and CUST1 opcode encodings shown in Table 7-193 are permanently reserved
# for designer-defined opcodes.

:cust0 ... is op0=0x0 & op1=0x6 & op2 & ar & as & at { cust0(); }
:cust1 ... is op0=0x0 & op1=0x7 & op2 & ar & as & at { cust1(); }
```

Independent elimination confirms it: `op1=4` and `op1=5` are both **EXTUI**
(`shiftimm = s | ((op1 & 1) << 4)`), and `xdis.py` decodes them as `extui` with
correct shift/mask semantics at thousands of sites across the corpus. `op1=4`
cannot be CUST0. **PROVEN.**

The GBATEK Xtensa opcode tables and the Cadence ISA summary agree on the rest of
the QRST map (`0`=RST0, `1`=RST1, `2`=RST2, `3`=RST3, `4`/`5`=EXTUI, `8`=LSCX,
`9`=LSC4, `A`=FP0, `B`=FP1) and on the detail that turned out to matter:
**`SLL ar,as` requires the `t` field to be zero.**

### 1.2 The ISA defines no operand layout for CUST0/CUST1

The manual reserves the space and says nothing further: field meaning inside
`op2`/`r`/`s`/`t` is entirely designer-defined. Any operand claim below is
therefore inference from this firmware, never from a specification.

### 1.3 Length is never in doubt — this was a misdiagnosis

`sn200-pcode-toolchain.md` §4 said "SLEIGH cannot even determine instruction
lengths". That is not what was happening.

**Xtensa fixes instruction length from `op0` alone**: `op0 ∈ 0x8..0xD` → 2
bytes, `op0 ∈ {0xE,0xF}` → 8 bytes on this core (the FLIX bundle), everything
else → 3 bytes. A custom instruction in the `op0=0` space is *always three
bytes*, whatever it does. What SLEIGH actually reported was
`Unable to resolve constructor` — no constructor **matched**, so the decoder
could not produce an instruction at all and the walk stopped. That is a spec
coverage problem, not a length problem, and it is fixable without knowing a
single thing about the instruction's semantics. §4 is the fix.

---

## 2. Census — PROVEN

`tools/sn200-fw/funcmap.py`'s confirmed function extents, linearly decoded with
`xdis.py`, across all 18 images. Only real instruction boundaries are counted;
nothing is byte-scanned.

| population | sites |
|---|---|
| plain 3-byte instructions at real boundaries | 155 372 |
| of those, `op0=0` (QRST) | 47 961 |
| **CUST0 (`op1=6`)** | **2 873** |
| **CUST1 (`op1=7`)** | **3 856** |
| `op0=0` encodings the stock SLEIGH spec cannot resolve | **14 315** (9.2 %, 2 383 distinct words) |

The 3 040 figure in `sn200-pcode-toolchain.md` §4 is in the right region but was
counted differently (all byte offsets rather than confirmed extents); the
directly comparable number here is **2 422** CUST0 instructions in FLIX slot A.

The two custom classes are **almost perfectly disjoint in where they appear**,
which is the first real structural fact about them:

| class | in FLIX slot A | plain 3-byte |
|---|---|---|
| CUST0 `op1=6` | 2 422 | 451 |
| CUST1 `op1=7` | 2 | 3 854 |

**CUST1 is essentially never bundled; CUST0 almost always is.** A FLIX slot's
contents are chosen by the TIE designer, so this says CUST0 is in slot A's issue
set and CUST1 is not — i.e. CUST0 is a cheap, schedulable operation and CUST1 is
not (long latency, or it uses a port slot A does not have). **INFERRED**, from a
clean 1 000:1 asymmetry.

### 2.1 The custom space is bigger than CUST0/CUST1

The unresolved-encoding census is the more useful list, because it is what
actually stops a lifter. Top classes by site count:

| `op1` | `op2` | base-ISA meaning | sites | reading |
|---|---|---|---|---|
| `1` | `a` | `SLL ar,as`, **`t` must be 0** | **4 375** | every one of these has `t ≠ 0` — a distinct opcode squatting in SLL's reserved field |
| `0` | `6` | RT0 (`neg`/`abs`, `s ∈ {0,1}`) | 2 945 | `s ∉ {0,1}` — reserved RT0 sub-space |
| `2` | `6` | RST2, reserved | 1 668 | reserved |
| `0` | `7` | RST0, reserved | 1 134 | reserved |
| `b` | `0` | FP1, reserved | 810 | see §3 |
| `0` | `0` | ST0 with an undefined `r` | 232 | reserved |

plus a long tail across `op1 = 8..f` at 3–140 sites each.

**So the designer did not confine themselves to CUST0/CUST1.** Roughly 10 000 of
the 14 315 unresolved sites are in *reserved sub-encodings of standard classes*,
not in the space the manual set aside. That is the single most practically
important finding in this document: a tool that only handles `op1 ∈ {6,7}` will
still stop dead, which is exactly what happened to the `0x0303` walk.

---

## 3. The floating-point decodes are fake — INFERRED, strong

Ghidra's stock spec decodes `QRST op1=0xA/0xB` as the Xtensa **FPU option**
(`add.s`, `mul.s`, `round.s`, …). `sn200-xtensa-isa.md` §3.1 already recorded
"a floating-point `add.s f8,f0,f0` inside integer control code" as a *symptom of
a misdecode*. It is worse than a cosmetic problem: unlike an unresolved
encoding, a fake FP decode **succeeds**, so it propagates silently.

Two corpus tests say this core has no usable FPU:

1. **There is not one `rfr` or `wfr` instruction in any of the 18 images.**
   Those (`op1=a & op2=f & t=4` / `t=5`) are the only way to move a value
   between the AR file and the FR file. The `op1=a & op2=f` sites that do exist
   carry `t ∈ {0, 8, 12}` — never 4 or 5. Without AR↔FR transfers no compiled
   function can take a float argument or return one, and no float can be
   constructed from an integer. An FPU that cannot be loaded is not an FPU.
2. **`op0=3` (LSCI — `lsi`/`ssi`/`lsiu`/`ssiu`, the FP load/store class) does
   not behave like LSCI.** The ISA defines only four values of that
   instruction's `r` selector (`0`, `4`, `8`, `0xC`); across 877 sites all
   sixteen appear, roughly uniformly (the four defined ones take 33 % where
   chance alone gives 25 %). This is not the FP load/store class either.

`Emu` therefore treats p-code `FLOAT_*` ops as opaque rather than executing
them (`pcode.py`, `_alu`). Under the default `on_opaque="raise"` policy it still
refuses to answer; under `"skip"` it steps over and records, like any other
undecoded slot.

**Not claimed:** that `op1=a`/`op1=b` are TIE instructions in the manual's sense.
Cadence reserves FP0/FP1 for itself, so a designer would not normally be given
them. What is claimed is only that **executing them as floating point is
fabrication**, and the lifter no longer does it.

---

## 4. What was added to the tooling

### 4.1 `flix.sinc`: a length-only catch-all for `op0=0`

```
define pcodeop xt_op0_reserved;

:xt.rsvd "{op1="^op1^", op2="^op2^", r="^ar^", s="^as^", t="^at^"}"
    is op0=0 & op1 & op2 & ar & as & at
{
    xt_op0_reserved();
}
```

It constrains **only `op0`**, so every real constructor (each of which
constrains `op0` + `op1` + `op2` at minimum) is strictly more specific and still
wins. Verified by construction: after the change `0x100000` still decodes as
`and a0,a0,a0`, `0x0000a0` as `ret`, `0x060a40` as `cust0`, and only genuinely
unmatched encodings fall through to `xt.rsvd`.

It asserts **nothing** except the three-byte length. No register is written, so
whatever the real instruction produced stays stale in the emulator.

### 4.2 `pcode.py`: `cust0`, `cust1`, `xt_op0_reserved` are opaque, not fatal

They joined `OPAQUE_PCODEOPS`, so `Emu(on_opaque="skip")` steps over and
**records** them, and `Insn.opaque()` reports them. The honesty property is
preserved: the default policy still raises, and `sn200_oracle.py` reports the
opaque count for every answer it gives.

### 4.3 `sn200_oracle.py`: two-stage coroutine arms are followed

`0xFF` sub-command 3 is the only arm that preps, yields, and builds its request
on the *next* entry. The oracle now follows the resume PC the yield leaves in
`a3` instead of reporting "no enqueue reached".

`.venv/bin/pytest tools/sn200-fw/tests/ -q` → **237 passed** (was 236; one
obsolete test replaced by two).

---

## 5. The payoff: `0xFF`/`0x0303` is PROVEN, not refuted

### 5.1 What was actually blocking it

The walk stopped at static `0x30033673`, bytes `40 e8 a1`:

```
op0=0  op1=1  op2=0xa  r=0xe  s=8  t=4
```

`op1=1, op2=0xa` is `SLL ar,as` — **whose `t` field must be zero, and this one is
4.** Not CUST0, not CUST1, not FLIX; a reserved-field encoding in the shift
class, three bytes long like every other `op0=0` instruction. Two more
instructions on the same arm (`0x0b1100`, `0x0a1000` — an FP1-class and an
FP0-class word) were the second and third walls.

The custom-TIE explanation in `sn200-pcode-toolchain.md` §4 was wrong about the
cause. It was right about the consequence.

### 5.2 The arm, executed

`0xFF`/`0x0303` is a **two-stage coroutine**, which is why it looked so
different from its siblings.

**Stage 1, `0x30033661`** (`ff_erase_arm(3)` reaches it; 7 opaque instructions
stepped over):

```
30033661: l32i.n a5,a1,0x8                      ; a5 = command context
30033663: addmi  a10,a5,256
30033666: { addi a10,a10,120 ; movi a11,4095 ; movi a12,64 }
3003366e: call8  0x30031d10                     ; fill 64 bytes at ctx+0x178
30033671: l32i.n a14,a2,0x0
30033673: <reserved op1=1 op2=a>                ; opaque
30033676: <reserved op1=b op2=0>                ; opaque
30033679: <FP0-class word>                      ; opaque
3003367c: l32i.n a12,a1,0x0
3003367e: l32i.n a13,a1,0x4
30033680: s32i.n a13,a5,0x2c
30033682: { s32i a12,a5,0x28 ; movi a11,376 }
3003368a: cust1 {op2=0xc, r=a5, s=a9, t=a8}     ; opaque
3003368d: cust1 {op2=0x4, r=a5, s=a11, t=a4}    ; opaque
30033690: { add a15,a5,a11 ; movi a2,16 }       ; a15 = ctx+0x178, the buffer
30033698: <reserved op1=0 op2=7>                ; opaque
3003369b: l32r  a2,-> 0x7ffbc352                ; resume-2 PC
3003369e: l32r  a9,-> 0x7ffbc2bf                ; = static 0x300335f7
300336a1: { movi a10,6 ; j 0x30033599 }         ; yield, a3 = a9
```

**Stage 2, `0x300335f7`** — reached by following the yielded resume PC through
the overlay-22 delta. Nothing here is opaque:

```
300335f7: movi.n a15,1
300335f9: s32i   a15,a12,0x120                       ; +0x120 = 1
300335fc: { s32i a15,a12,0x118 ; movi a9,13 }        ; VERB   = 1
30033604: { s32i a9,a12,0x11c  ; mov a8,a2  }        ; SECTION = 13
3003360c: l32i.n a8,a8,0x0
3003360e: { s32i a8,a12,0x128 ; ... }
30033616: { s8i  a15,a2,0x24  ; ... }
3003361e: { s32i a15,a12,0x12c ; mov a11,a6 ; movi a2,138 }
30033626: call8  0x30030aa0                          ; OAM worker ENQUEUE
30033629: l32r   a9,-> 0x7ffbc2b0                    ; = static 0x300335e8
```

and the completion handler it registers, `0x300335e8`, logs StrId 1632
**`"OAM ERASE CMD: Erase to SBL EEPROM failed."`** — an independent corroboration
of the section id that does not depend on the field decode at all.

Compare the sub-0 arm at `0x30033772`, which is the same shape in one stage:
`+0x120 = 0`, verb `3`, section `6`, `call8 0x30030aa0`.

### 5.3 Verdict

```
0x0303  CATASTROPHIC  cmd 0x03 erase family; verb 0x1 (section write); section 13 (SBL EEPROM)
```

- **PROVEN by execution**: verb `1`, section `13`, and the OAM enqueue is
  reached. `sn200_oracle.py --ff` reproduces it; `test_0303_writes_the_sbl_eeprom_section`
  asserts it.
- **Corroborated, not pure**: seven instructions on the stage-1 path were
  stepped over. None of them writes `a12` (the request pointer) or any register
  the stage-2 stores read, and stage 2 itself is fully decoded — but this is
  recorded, and `test_0303_walk_still_steps_over_undecoded_instructions` will
  fail loudly if a future spec change quietly turns it into an unqualified
  proof.
- **The hand decode in `sn200-command-reference.md` was right**: verb 1,
  section 13. One detail in it was wrong and is corrected in §5.4.
- **"Permanent brick" remains INFERRED**, from the SBL's role in boot, not from
  anything executed here. Nothing in this work makes `0x0303` safer. It got
  *more* certain, not less.

### 5.4 A correction to `sn200-command-reference.md`

The reference said `0x0303` "calls the EEPROM primitive `0x30031d10` directly,
not the flash-erase primitive `0x30030aa0`". It calls **both**: `0x30031d10` in
stage 1 to fill a 64-byte buffer at `ctx+0x178`, and `0x30030aa0` in stage 2 to
enqueue the request. It is not structurally exempt from the OAM path — it is the
same path, reached one coroutine hop later.

Note also that the posted verb is **1 (section write)**, not 3 (section erase),
even though the log strings call it an erase. Combined with the 64-byte buffer
built in stage 1, the shape is "write a payload into EEPROM section 13", not
"blank it". For the operator this changes nothing.

---

## 6. Operand shapes — INFERRED

Both results below have a stated margin over a null. Neither is strong enough to
put semantics in `flix.sinc`, and neither is used by any tool.

### 6.1 CUST0, FLIX slot A, `op2=0`, `r=0` — a one-in/one-out operation feeding a call

2 462 sites, 2 418 of them in slot A. `r` is 0 at 2 423 of 2 462. The remaining
two fields behave like registers:

- **`t` is a destination register.** It never takes `a0` or `a1` — the exact
  signature `sn200-xtensa-isa.md` §3.3 used to separate register fields from
  immediate fields. Its histogram peaks hard on the outgoing-argument registers:
  `a10` (555), `a11` (398), `a4` (363), `a12` (253).
- **43 % of these bundles are immediately followed by `call8` (691) or `retw.n`
  (372).** The idiom is unmistakable in context — the bundle's slot B/C set up
  the *other* arguments (`mov a11,a3`, `movi a12,0`) and slot A's CUST0 produces
  the one that is otherwise never written:

```
7ffbd188: entry a1,0x20
7ffbd18b: { cust0 a10,a2 ; mov a11,a3 ; movi a12,0 }
7ffbd193: call8 0x7ffa6550
```

So: **`cust0 a<t>, a<s>` — one source register in, one result out, cheap enough
to bundle, and the compiler's chosen way to materialise a call argument.** What
it computes is **UNKNOWN**. It is not a `mov` (the compiler has `mov`), and it is
not any base-ISA ALU operation (they all have encodings the compiler uses
elsewhere).

Plausible-but-unsupported candidates, listed so nobody re-derives them and
mistakes them for findings: a pointer/handle translation, an L2P bit-field
extract, a byte swap, a per-core object base. **SPECULATIVE, all of them.** No
test in this repo distinguishes them and none should be quoted.

### 6.2 CUST1, plain 3-byte — `r` is a base register, `s` a word offset

3 854 sites. The idiom that suggested it, seen at three unrelated addresses:

```
7ffa86a5: s32i.n a15,a1,0x20      ; word 8 of the frame
7ffa86a7: cust1 {op2=0xc, r=a1, s=a8, t=a0}
7ffa86ad: s32i   a14,a1,0x24      ; word 9
7ffa86b0: cust1 {op2=0x4, r=a1, s=a9, t=a0}
```

Corpus test: for each CUST1 site, does a 32-bit load or store within ±3
instructions use `a<r>` as its base and `s*4` as its offset?

| hypothesis | hit rate |
|---|---|
| base = `a<r>`, word offset = `s` | **10.8 %** (416/3854) |
| swapped: base = `a<s>`, offset = `r` (control) | 1.6 % |
| random `(r,s)` (null) | 0.4 % |

A 7× margin over the swapped control and 27× over the null. The direction is
real; the absolute rate is low because most CUST1 sites have no adjacent AR
access to match against, which is itself consistent with the operation moving
data between memory and a register file the AR-based decoder cannot see.

**Reading: a substantial part of the CUST1 space is load/store of a TIE register
file, addressed as `[a<r> + s*4]`, with `op2` selecting the register / width /
direction.** INFERRED. `op2` spreads over `0..7` and `0xc..0xf` with peaks at
`3`, `5`, `4`, `c`, `d` — consistent with a small family of related opcodes, not
one instruction.

---

## 7. What was deliberately not done

- **No TIE file was searched for.** It is not public; the brief said so and the
  brief was right.
- **No semantics were added to `flix.sinc`.** The only thing the spec now
  asserts about a custom instruction is that it is three bytes long. That claim
  follows from `op0` alone and cannot be wrong.
- **FP0/FP1 were not overridden in the spec**, only refused by the emulator. An
  override would need per-`op2` constructors outranking Ghidra's, which is a lot
  of surface area to get wrong for no gain over refusing to execute them.
- **The remaining 3 040-odd CUST0/CUST1 sites outside the boot path were not
  characterised individually.** §6 is the whole of what the clustering produced
  that survives scrutiny; the rest was pattern-matching that did not beat its
  null.

## 8. What would move this further

1. **A second firmware revision.** `~/sn200fw/fw/KNGND110` exists. Diffing the
   same function across two builds constrains what a custom opcode does far
   better than any static idiom census: an instruction that changes with a
   changed constant is arithmetic on that constant.
2. **The `0xFF` handler is now fully walkable — the same treatment for `0xCA`.**
   Twelve `0xCA` sub-opcodes survive the Post-Crash gate, two of which destroy a
   drive on one well-formed command, and none has been executed the way `0xFF`
   now is. Nothing in the custom-opcode space blocks that work any more.
3. **`op1=1 op2=a` with `t ≠ 0` is the single highest-value target** (4 375
   sites, more than CUST0). It sits inside the shift class and its `t` field
   takes a small set of values — a good candidate for the "is it a shift with an
   extra operand?" question, which a KNGND110 diff could answer.
