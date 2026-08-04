# SN200 Xtensa instruction encoding reference

Authoritative, consolidated encoding reference for the Tensilica Xtensa core
used in the HGST/WDC Ultrastar SN200 firmware (`KNGND122`, 16× `PROCn` cores
+ `FCC`). Nothing here is public documentation — every fact was reverse
engineered from the firmware images in this project, several after an
earlier, wrong guess produced fabricated instructions or false conclusions.
Those episodes are recorded in full in **Corrections** below; do not repeat
them.

**Ground truth is `tools/sn200-fw/xdis.py` / `disany.py`**, and for literal
(`l32r`) cross-references specifically, **`tools/sn200-fw/litref.py`**.
Where any other source (this doc, `flix.sinc`, an RE doc) disagrees with
these tools, the tools win. `flix.sinc` (the Ghidra SLEIGH spec) is derived
from `xdis.py` and should track it; treat any divergence between the two as
a bug in `flix.sinc`, not a second opinion.

Labels: **PROVEN** (confirmed against execution, a counter-example search,
or a corpus-wide statistical test with a clear margin over a null),
**INFERRED** (consistent with strong circumstantial/statistical evidence,
no direct confirmation), **SPECULATIVE** (a guess; do not build conclusions
on it).

---

## 1. Base ISA subset observed

Standard Xtensa windowed-ABI encoding: `op0` = low nibble of byte 0 selects
the format. 2-byte "narrow" (RRRN-class) instructions have `op0` in
`0x8..0xD`; everything else with `op0 != 0xE/0xF` is a plain 3-byte
instruction; `op0 ∈ {0xE, 0xF}` is an 8-byte FLIX bundle (§2). All of the
below is **PROVEN** by cross-referencing `tools/sn200-fw/xdis.py:dis()`
against `retw`/`entry` anchor density (see §6, `sn200-firmware-re.md` §"CPU
architecture").

### 3-byte core instructions (`op0` not in `{0x8..0xF}`)

24-bit word `w`, fields `t=(w>>4)&0xF`, `s=(w>>8)&0xF`, `r=(w>>12)&0xF`,
`op1=(w>>16)&0xF`, `op2=(w>>20)&0xF`, `imm8=(w>>16)&0xFF`,
`imm12=(w>>12)&0xFFF`, `imm16=(w>>8)&0xFFFF`, `off18=(w>>6)&0x3FFFF`.

| Mnemonic | `op0` | selector | Notes |
|---|---|---|---|
| `entry as, imm12*8` | 6 | `n=3,m=0` (top 2 bits of byte0, i.e. `r=0`... see below) | frame size in bytes = `imm12*8`; the **only** opcode whose byte 0 is `0x36` — used for entry-point byte-scanning (§6) |
| `retw` / `ret` / `jx as` | 0 | `op1=0,op2=0,r=0`, `m=(t>>2)&3,n=t&3` | `m=2`: `n=0`→`ret`, `1`→`retw`, `2`→`jx as` |
| `callx0/4/8/12 as` | 0 | `op1=0,op2=0,r=0,m=3` | `n` selects window size `[0,4,8,12]` |
| `call0/4/8/12 tgt` | 5 | `n=(w>>4)&3` | PC-relative, `tgt=((pc&~3)+(s(off18,18)<<2)+4)` |
| `movsp at,as` | 0 | `op1=0,op2=0,r=1` | |
| `sync`/`extw` | 0 | `op1=0,op2=0,r=2` | not disambiguated |
| `rfei t,s` | 0 | `op1=0,op2=0,r=3` | return-from-exception; used by the **call0 ABI** (§4) |
| `break s,t` | 0 | `op1=0,op2=0,r=4` | |
| `syscall` | 0 | `op1=0,op2=0,r=5` | |
| `rsil at,imm` | 0 | `op1=0,op2=0,r=6` | |
| `waiti imm` | 0 | `op1=0,op2=0,r=7` | |
| `and/or/xor ar,as,at` | 0 | `op1=0,op2=1/2/3` | |
| `ssai/ssr/ssl/ssa8l/ssa8b/rotw/nsa/nsau` | 0 | `op1=0,op2=4` | keyed on `r` |
| `neg/abs ar,at` | 0 | `op1=0,op2=6` | keyed on `s` |
| `add/addx2/addx4/addx8/sub/subx2/subx4/subx8` | 0 | `op1=0,op2=8..0xF` | |
| `slli ar,as,shift` | 0 | `op1=1,op2∈{0,1}` | `shift = 32-(((op2&1)<<4)|t)` |
| `srai ar,at,shift` | 0 | `op1=1,op2∈{2,3}` | |
| `srli ar,at,shift` | 0 | `op1=1,op2=4` | |
| `xsr at,sr` | 0 | `op1=1,op2=6` | |
| `src/srl/sll/sra` | 0 | `op1=1,op2=8..0xB` | |
| `mul16u/mul16s` | 0 | `op1=1,op2=0xC/0xD` | |
| `quou/quos/remu/rems/mull/muluh/mulsh` | 0 | `op1=2` | |
| `rsr/wsr at,sr` | 0 | `op1=3,op2=0/1` | |
| `sext ar,as,imm` / `clamps` | 0 | `op1=3,op2=2/3` | `imm = t+7` |
| `min/max/minu/maxu` | 0 | `op1=3,op2=4..7` | |
| `moveqz/movnez/movltz/movgez ar,as,at` | 0 | `op1=3,op2=8..0xB` | |
| `movf/movt ar,as,bt` | 0 | `op1=3,op2=0xC/0xD` | |
| `rur`/`wur` | 0 | `op1=3,op2=0xE/0xF` | |
| `extui ar,at,shiftimm,mask+1` | 0 | `op1∈{4,5}` | `shiftimm = s\|((op1&1)<<4)` |
| `l32r at, target` | 1 | — | `target = ((pc+3)&~3) + (imm16<<2) - 0x40000` |
| `l8ui/l16ui/l32i/s8i/s16i/s32i/l16si/movi/l32ai/addi/addmi/s32c1i/s32ri` | 2 | keyed on `r` | see table below |
| `j target` | 6 | `n=0` | `target = pc+4+s(off18,18)` |
| `beqz/bnez/bltz/bgez as, target` | 6 | `n=1` | `target = pc+4+s(imm12,12)` |
| `beqi/bnei/blti/bgei as, B4CONST[r], target` | 6 | `n=2` | `target = pc+4+s(imm8,8)` |
| `entry as, imm12*8` | 6 | `n=3,m=0` | |
| `bf/bt b, target` | 6 | `n=3,m=1,r=0/1` | |
| `loop/loopnez/loopgtz as, target` | 6 | `n=3,m=1,r=8/9/0xA` | |
| `bltui/bgeui as, B4CONSTU[r], target` | 6 | `n=3,m=2/3` | |
| `bnone/beq/blt/bltu/ball/bbc/bany/bne/bge/bgeu/bnall/bbs as,at,target` | 7 | `r=0..0xD` (`5,0xD`=`bbc`/`bbs`) | |
| `bbci as,bit,target` | 7 | `r∈{6,7}` | `bit = ((r&1)<<4)\|t` |
| `bbsi as,bit,target` | 7 | `r∈{0xE,0xF}` | `bit = ((r&1)<<4)\|t` |

`op0 = 2` (load/store/`movi`/`addi` family), keyed on `r`:

| `r` | Mnemonic |
|---|---|
| 0 | `l8ui at,as,imm8` |
| 1 | `l16ui at,as,imm8*2` |
| 2 | `l32i at,as,imm8*4` |
| 4 | `s8i at,as,imm8` |
| 5 | `s16i at,as,imm8*2` |
| 6 | `s32i at,as,imm8*4` |
| 7 | `cache` (op unresolved beyond the mnemonic) |
| 9 | `l16si at,as,imm8*2` |
| 0xA | `movi at, s(((s<<8)\|imm8),12)` — **sign-extended 12-bit immediate**, split across the `s` and `imm8` fields |
| 0xB | `l32ai at,as,imm8*4` |
| 0xC | `addi at,as,s(imm8,8)` |
| 0xD | `addmi at,as,s(imm8,8)*256` |
| 0xE | `s32c1i at,as,imm8*4` |
| 0xF | `s32ri at,as,imm8*4` |

`op0 = 8..0xD`: 2-byte narrow (RRRN-class) instructions, `w=b0|(b1<<8)`,
`t=(w>>4)&0xF`, `s=(w>>8)&0xF`, `r=(w>>12)&0xF`:

| `op0` | Mnemonic |
|---|---|
| 8 | `l32i.n at,as,r*4` |
| 9 | `s32i.n at,as,r*4` |
| 0xA | `add.n ar,as,at` |
| 0xB | `addi.n ar,as,imm` — **`r` is the destination, `t` is the immediate** (`t==0` means `-1`). See Corrections — this field order was originally inverted. |
| 0xC, `t&8=0` | `movi.n as, v` — 7-bit signed immediate `((t&7)<<4)\|r`, `v = imm7 if imm7<0x60 else imm7-0x80` |
| 0xC, `t&8` | `beqz.n`/`bnez.n as, target` — 6-bit unsigned displacement `((t&3)<<4)\|r` |
| 0xD, `r=0` | `mov.n at,as` |
| 0xD, `r=0xF` | `t=0` `ret.n`, `1` `retw.n`, `2` `break.n`, `3` `nop.n`, `6` `ill.n` |

`B4CONST = [-1,1,2,3,4,5,6,7,8,10,12,16,32,64,128,256]`,
`B4CONSTU = [32768,65536,2,3,4,5,6,7,8,10,12,16,32,64,128,256]`, indexed by
the branch's 4-bit `r`/mask field — same table used in FLIX slot B (§2).

---

## 2. FLIX bundle formats — PROVEN width, mixed-confidence contents

`op0 ∈ {0xE, 0xF}` (low nibble of byte 0; equivalently `bits(1,3)==0b111`)
selects an **8-byte** VLIW bundle. Both the width and the two-format split
are **PROVEN** — see §5 (Corrections) for how badly wrong the earlier
3-byte guess was. No alignment requirement: bundles start at whatever byte
offset the previous instruction ended on, including addresses ≡ 3 (mod 4).
Field bit numbers are LSB-0 over the 64-bit little-endian bundle
(`q = int.from_bytes(d[o:o+8], "little")` in `xdis.py`), i.e. bit 0 is the
LSB of byte 0.

Confirmed distribution (18-image corpus): `op0=0xE` ≈ 72% of bundles,
`op0=0xF` ≈ 28%.

### Slot A (bits 4–27, all formats)

Mirrors the base-ISA 24-bit instruction layout, relocated so the FLIX
selector nibble can live at bits 0–3: `op0@24-27`, `t@4-7`,
`s+r+imm8@8-23` (16 bits, matching base ISA `imm16`).

| Field | Bits | Status |
|---|---|---|
| `op0` | 24-27 | PROVEN (determines which slot-A form, mirrors base ISA `op0`) |
| `t` (dest reg) | 4-7 | INFERRED — statistically never `a0`/`a1` when `op0=1`, consistent with an `l32r` destination |
| `imm16` (`l32r` literal offset) | 8-23 | **PROVEN** (upgraded from INFERRED — see Corrections §3.7), `op0=1` only: `target = ((pc+3)&~3) + (imm16<<2) - 0x40000`, same formula as base-ISA `l32r`, computed on the bundle's **real, possibly-unaligned PC** — do not round the PC before applying it. `bits(8,23)` scored 8.41% exact-hit rate against real literal pools vs. 0.41–0.49% for permutation/random nulls (≥17× margin); independently reproduced by `tools/sn200-fw/litref.py`, the reference tool for resolving "what literal does this `l32r` load" / "where is this literal referenced" in either plain or FLIX-slot-A form |
| every other `op0` | — | **UNRESOLVED**. `op0=2` (base-ISA `movi`/load/store) is common and still opaque — `flix_slotA_unknown(op0)` in `flix.sinc`. Do not trust any register that a decompiled FLIX function only ever reads, never writes; it may be written by unresolved slot A. |

### Slot C (bits 48–63, both formats): `movi at, imm8`

**PROVEN.** `op@60-63` (class `0xC`), `t@56-59`, `imm8@48-55`, zero-extended
(display-only; sign is unconfirmed — see §5 caveats). `0xC090` (`t=a0,
imm8=0x90`) is the canonical **NOP'd-slot** encoding: ~68% of all `0xE`
bundles' slot C, confirmed as a true no-op both statistically (nibble
56-59 spreads uniformly over `a1..a15`, never coincidentally hits `a0`
except in this exact pattern) and semantically (§5, "Slot C existed and
was missed entirely").

### Slot B, format `0xF`: the branch slot

**PROVEN**, the highest-value fix in the whole spec — this slot carries
essentially every conditional dispatch in the firmware.

| Field | Bits | Meaning |
|---|---|---|
| `r` | 28-31 | register operand, or `B4CONST`/`B4CONSTU` index, or `1<<r` bit/mask index, depending on mnemonic class |
| `s` | 32-35 | `as` register operand |
| `imm18` | 36-53 | **signed** 18-bit branch displacement. `target = pc + 4 + s(imm18,18)`. **Not `imm12`** — see Corrections |
| `k` | 55-63 | 1-based index into `BRK`, alphabetically-sorted branch mnemonics (bit 54 always 0) |

`BRK` (index → mnemonic; `None` = index 0, unused):

```
1 ball   2 bany   3 bbc    4 bbci   5 bbs    6 bbsi   7 beq    8 beqi
9 bge   10 bgei  11 bgeu  12 bgeui 13 blt   14 blti  15 bltu  16 bltui
17 bnall 18 bne  19 bnei  20 bnone 21 beqz  22 bgez  23 bltz  24 bnez
```
Confirmed semantically: `7=beq`, `8=beqi`, `0x12=bne`, `0x13=bnei`,
`0x15=beqz`, `0x18=bnez`; the remainder are PROVEN statistically (register
operands never land on `a0`/`a1`, matching real code) plus the `ball`/`bany`
mask finding below.

Operand class by mnemonic:
- register (`as, at`): `beq bge bgeu blt bltu bnall bne bnone`
- `B4CONST[r]` immediate: `beqi bnei blti bgei`
- `B4CONSTU[r]` immediate: `bltui bgeui`
- compare-against-zero (`as` only): `beqz bnez bltz bgez`
- **mask, `1<<r`, not a register** (PROVEN — see Corrections): `ball bany`
- bit-test, `1<<r`: `bbc bbci bbs bbsi`

`k` outside `1..24`: unresolved (`flix_slotB_unknown(k)`).

### Slot B, format `0xE`: `j` / `addi` / `movi(12-bit)` / `mov` / `or`

**Mixed confidence.** Selected by a 2-bit `pre` field at bits 46-47. This
format's slot B is **not** a conditional branch (except via `j`) — all
conditional branches live in format `0xF`'s slot B above.

| `pre` | Form | Bits | Status |
|---|---|---|---|
| 3 | `j target` | signed `disp18@28-45`, `target = pc+4+disp18` | PROVEN |
| 1 | `addi at,as,imm4` | `t@28-31`, `s@36-39`, `imm4@32-35` | PROVEN, one confirmed instance (`0x7ffb4fda` = `addi a2,a2,4`, word-push loop, `sn200-independent-re.md` addendum A.4). `imm4` sign unconfirmed — treated unsigned, only a positive case observed |
| 2, opcode nibble `0x8` @44-47 | `movi at,imm12` | `t@28-31`, `imm12@32-43` | PROVEN presence, **zero-extension unconfirmed** (see §5 caveats). Example: `0x7ffb4fdb` → `movi a11,1277` |
| 2, 6-bit check `0x23` @40-45 | `mov at,as` | `t@28-31`, `as@36-39` | PROVEN |
| 2, opcode nibble `0x9` @44-47 | ALU class, `sub@40-43` | `t@28-31`, `s@32-35`, `r@36-39` | only `sub=0xE` = `or at,as,ar` confirmed (`0x7ffab120`, result stored by the next instruction's `s32i a11,a5,0x108`); every other `sub` unresolved (`flix_slotB_unknown_alu`) |
| other (`pre=0`, or `pre=2` outside the classes above) | — | UNRESOLVED (`flix_slotB_unknown`). `pre=1` bundles besides the confirmed `addi` case (~835 in PROC9 alone) were tested for a PC-relative field and found none — probably not control flow, but not proven. |

### Bundle-level dispatch

Two constructors, both `op0 ∈ {0xE,0xF}`, dispatched on the exact `op0`
nibble (their slot-B layouts are unrelated, so `bits(1,3)==0b111` alone is
not enough to pick the right slot-B decoder):

```
op0=0xF: { slotA ; slotB_F (branch) ; slotC }
op0=0xE: { slotA ; slotB_E (j/addi/movi/mov/or) ; slotC }
```

`flix.sinc` builds slot A, then slot C, then slot B (branch/jump) last, so
A's and C's register writes are never skipped by B's `goto` — this matches
VLIW "all slots fire, then PC updates" semantics for every confirmed case.
It does **not** model true parallel register reads: if slot B ever read a
register that slot A/C in the *same* bundle also writes, the emitted
p-code would see the post-write value. Not observed, not exhaustively
ruled out.

---

## 3. Corrections

Each of these was believed, shipped in earlier analysis, and was wrong.
Recorded here so the mistake-class doesn't recur.

### 3.1 FLIX bundle width: believed 3 bytes → actually 8

Stock Ghidra 12.1.2's `Xtensa` module (and this project's earliest
assumption) treats `op0=0xE` as a 3-byte pseudo-op. Real bundles are 8
bytes. **Symptom:** Ghidra resumed decoding 5 bytes inside every bundle and
fabricated instructions from payload bytes — a `bnall` at `0x3003354d` that
never existed (the bytes were the tail of the bundle starting at
`0x30033546`; its "target" `0x30033576` was therefore also spurious), and a
floating-point `add.s f8,f0,f0` inside integer control code
(`FUN_7ffa6b18` decompiled as `flix(); halt_baddata();`). Proven by two
independent objective tests (forward-branch span landing, call-target
landing on `entry`) that both peak sharply at width 8 across an 18-image,
~1.9 MB corpus — see `docs/xtensa-flix-decoding.md` for the full tables.

### 3.2 Slot B branch displacement: believed `imm12`@36-47 → actually `imm18`@36-53

**Symptom:** desynced sweeps and apparent "dead code." Counter-example:
`0x7ffa6d1b` with `disp = -216` targets `0x7ffa6c47`; `-216` does not fit
in a signed 12-bit field, ruling out `imm12` outright.

### 3.3 `ball`/`bany` operand: believed a register → actually a single-bit immediate mask (`1<<r`)

**Symptom:** mask constants read as nonsense (spurious register numbers).
**PROVEN** statistically over all 18 images: every genuine register-operand
branch (`beq` n=888, `bgeu` n=621, `bltu`, `bne`, `bnone`) avoids `a0`/`a1`
in the operand field entirely, while `ball` (n=180) and `bany` (n=171) have
`r=0` as by far the most common value, then `r=1` — the signature of an
immediate field, not a register field. Confirmed semantically in PROC0's
section-state manager: `0x7ffab265 ball a8,<r=0>` falls through to log
"Crash Dump section is erased"; `0x7ffab0d9 ball a14,<r=2>` falls through
to "PFail Crash Dump section is erased"; the tested byte is built from the
single-bit constants 1, 2, 4, 8, i.e. `1<<0`=Crash armed, `1<<2`=PFail
armed. **`bnall` and `bnone` are genuine register forms — do not "fix"
those too.**

### 3.4 `addi.n` field order: believed `(t=dest, r=imm)` → actually `(r=dest, t=imm)`

**Symptom:** a phantom stack-pointer corruption. `addi.n a11,a11,1` was
printed as `addi.n a1,a11,11` — the RRRN encoding puts the destination in
`r` and the immediate in `t` (`t==0` encodes `-1`), and the tool had them
swapped. Found and fixed in the middle of the crash-dump timestamp update
code (`sn200-independent-re.md`).

### 3.5 Slot C: believed absent → actually a full third slot, missed entirely

**Symptom:** live registers with no visible source in the disassembly, and
`0xC090` — the canonical NOP'd-slot encoding — decoding as a fabricated
`a0 = 0x90` write when it was misread as a live `movi` instead of the no-op
it actually is. Confirmed semantically at `0x7ffaad0c` (PROC0), where slot
C `0xCB00` supplies the 0 fill byte to a 256-byte `memset` of the crash
header buffer, and at `0x7ffa6b28` (PROC8), where slot C `0xCB0D` supplies
the opcode constant `0x0D` that a sibling routine at `0x7ffa6db4` spells
out literally.

### 3.6 "No control flow inside bundles" (retracted) — most damaging error in this project's history

An early sweep looked for a single fixed PC-relative field across all
bundles, found none, and concluded FLIX bundles never branch. **Wrong**:
`op0=0xF` slot B is a branch and `op0=0xE` with `pre=3` is a `j` — the
displacement's position and width depend on the slot-B class selector,
which the sweep didn't vary. Essentially every dispatch chain in the
firmware lives in those slots. Consequence: a linear sweep blind to these
edges makes conditional paths look unconditional, and it made the NVMe-MI
admin command set in `sn200-independent-re.md` §10.1 read as an
**allow-list** when it is actually a **reject-list** — inverted polarity,
the highest-leverage correction on record for this project. **Never infer
"no branch here" from a decode that does not resolve slot B.**

### 3.7 Slot-A `l32r` target formula: a divergent scanner produced a silent false negative

An earlier literal-reference sweep used a formula for the FLIX slot-A
`l32r` target that diverged from the one derived and validated in §2 —
dropping or mis-ordering one of three easy-to-drop parts: the `+3` before
the align-down, the `-0x40000` bias, or (most subtly) rounding/using the
wrong PC instead of the bundle's real, possibly-unaligned address.
**Symptom, and why this class is worse than the others in this list:** the
sweep reported that the StrId-1447 log descriptor at `0x7ffa0940` had **no
reference in any of the 18 images** — a clean, confident negative result
that nearly overturned a correct finding elsewhere, rather than an obviously
broken decode like a fabricated instruction. It has exactly one reference:
`PROC12 0x7ffa2620`, a slot-A `l32r` into the logger, reached by
`bnei a13,1,0x7ffa2620`. The correct formula —

```
target = ((pc + 3) & ~3) + (imm16 << 2) - 0x40000
```

computed on the bundle's real PC — is implemented in
`tools/sn200-fw/litref.py` (`l32r_target()`), which independently
reproduces the §2 slot-A finding and is now ground truth for literal
cross-references alongside `xdis.py`/`disany.py`. **Lesson: "this constant
is never referenced" is exactly the kind of result a subtly wrong formula
produces silently — treat any such negative from a hand-rolled scanner as
suspect until cross-checked against `litref.py`.**

---

### 7. Slot-A literal target computed with a divergent formula — a SILENT false negative

**Wrong:** a variant slot-A `l32r` target formula (omitting the `+3` before the
align-down, the `-0x40000` bias, or computing from a slot offset rather than the
bundle's real PC).

**Right:** `target = ((pc + 3) & ~3) + (imm16 << 2) - 0x40000`, on the bundle PC.
`tools/sn200-fw/litref.py` implements it; treat it as ground truth alongside
`xdis.py`/`disany.py` for resolving literal references.

**Symptom, and why it is the most dangerous class here:** a sweep reported the
StrId-1447 log descriptor at `0x7ffa0940` as having **no reference in any of the
18 images**. It has exactly one — `PROC12 0x7ffa2620`, a slot-A `l32r` into the
logger, reached by `bnei a13,1,0x7ffa2620`. The miss nearly overturned a correct
finding.

Unlike a fabricated instruction, which *looks* wrong, "this address is
referenced nowhere" reads as a clean negative result. Any argument of the form
"nothing references X, therefore X is dead/unreachable" must be re-run with the
correct formula before it is believed.


## 4. ABI and calling convention

**Windowed ABI (PROVEN, the default for `PROCn` main/overlay code).**
- `entry as, framesize` at function start rotates the register window and
  allocates `framesize` bytes (`imm12*8`) on the stack pointed to by `as`
  (always `a1` in every confirmed function). It is the only opcode whose
  byte 0 is `0x36` (`op0=6, n=3, m=0`), which makes it a reliable
  byte-scan target for finding function entries (§6).
- `call4`/`call8`/`call12` rotate the window by 4/8/12 registers on call;
  the callee's own `entry` further extends the frame. `call0` performs a
  PC-relative call **without** rotating the window (used by the call0 ABI
  below, and by ordinary windowed-ABI leaf helpers that were compiled
  without their own register window — see `sn200-function-map.md`'s
  `call_targets_without_entry` discussion, e.g. `0x3002c1a0`, a clean
  `addx2`/`l8ui`/`l32r` leaf with no `entry`/`retw` at all, called via
  `call8` from windowed callers).
- `retw`/`retw.n` returns and un-rotates the window; `ret`/`ret.n` returns
  without window handling (call0-style, or after a `movsp`).
- `callx0/4/8/12 as`: computed call variant, same window-rotation rule
  keyed by the mnemonic, target in `as`.

**call0 ABI (PROVEN, `FCC_00100000` specifically — no windowed registers at
all).** Direct disassembly of `FCC.bin`'s code segment (`0x120180`+) shows
`addi a1,a1,-N` / … / `addi a1,a1,N` frame setup around `call0` and plain
`ret`, plus `rfei` (return-from-exception) — never `entry`/`retw`. All 242
byte-scan hits for `0x36` in `FCC_00100000` are coincidental and were
correctly rejected by the `entry a1, <=0x400` plausibility filter (§6);
the honest function count for a windowed-ABI scanner over this image is
**zero**. Mapping FCC's real functions needs a call0-ABI-aware scanner
(look for `addi a1,a1,-N` prologues terminated by `ret`) — out of scope for
the current tooling, a known gap not a defect.

---

## 5. Address-space and layout facts

- **Sixteen independent Xtensa cores** (`PROC0`..`PROC15`) plus a separate
  `FCC` (Flash Channel Controller) core, connected by a message-passing
  fabric. Each `PROCn` has its **own** address space that overlaps every
  other `PROCn`'s numerically (they all load around `0x7ff80000`+); the
  same numeric address routinely falls inside unrelated functions in
  several images at once. Always qualify an address with its image
  (`whichfunc.py --image PROC12_7ff80000 <addr>`), never assume a bare
  address is unambiguous.
- **`.BIN`/`.SEG` container.** Each `PROC*.bin`/`FCC.bin` starts with ASCII
  `.BIN` padded to 0x10, then a chain of 16-byte segment headers:
  `struct seg { char magic[4]=".SEG"; u32 file_offset_of_data; u32
  data_len; u32 load_addr; }`, chain terminated by `0xffffffff`. Parser:
  `tools/sn200-fw/segparse.py`; `tools/sn200-fw/unpack.py` flattens an
  image to per-processor memory images in one step. The flattened
  `flat/*.bin` files **zero-pad** large address-space gaps between real
  segments — do not scan them for function entries or treat their padding
  as "scanned but empty" real code (`sn200-function-map.md` §Method).
- **Overlay relocation.** Overlay code is linked at `0x300xxxxx` and
  executes from a fixed runtime window at `0x7ffbc000`. A descriptor table
  (`{load_addr, size, ddr_src, 0}`, two entries per overlay: text at
  `0x7ffbc000`, rodata at `0x7ff9f000`) gives the static (linked) address
  for any runtime address in that window:
  `static_address = ddr_src + (runtime_address - 0x7ffbc000)`. PROVEN,
  confirmed for multiple overlays (e.g. overlay 11: `ddr_src=0x3002b478`,
  size `0x380`; overlay 22: `ddr_src=0x30033338`, size `0xa00`; overlay 26:
  `ddr_src=0x30035378`, size `0x2940`; overlay 31: `ddr_src=0x3003bcb8`,
  size `0x20c0`).
- **`PROC8_30000000` (the "OVB" overlay bank) is bank-switched.** Its
  `.SEG` headers show a real, **unfilled** ~136 KB gap between
  `0x30000af8` and `0x30022238` (confirmed against the original
  `PROC8.bin` container, not the zero-padded flat merge). Well-formed,
  correctly-decoded code in this bank calls into that gap
  (`call8 0x30020da0` from a verified entry at `0x30028aa4`). The only
  explanation consistent with the segment data is memory-mapped bank
  switching: the dump captured whichever overlay page was resident at
  capture time, and the gap holds a *different, not-currently-dumped*
  overlay page at the same address window. Do not treat calls into this
  gap as broken or as evidence of a decode desync — the map only claims
  what it can see.

---

## 6. Practical decoding rules

- **Always disassemble from a verified function entry, never an arbitrary
  address.** Use `tools/sn200-fw/whichfunc.py <addr>` (add `--image
  <name>` once you know which core) before trusting any disassembly that
  starts mid-function. Two real mistakes in this project came from
  skipping this check: the fabricated `bnall` at `0x3003354d` (§3.1), and
  marker 8 being declared "dead code" after disassembling from
  `0x7ffa7d6d`, which is actually a `retw.n` *inside* the real function
  whose entry is `0x7ffa7a68` (`whichfunc.py` now reports this correctly:
  offset `0x305` into that function, not a function start).
- **Never trust Ghidra's decompiler for slot-A `op0 != 1` or format-`0xE`
  ALU forms other than `or`.** These are explicitly unresolved
  (`flix_slotA_unknown`, `flix_slotB_unknown_alu` pcodeops in `flix.sinc`)
  — any dataflow the decompiler shows through a register that a FLIX
  bundle "reads but never writes" may actually be written by one of these
  unmodelled slots.
- **`?`-bearing `xdis.py` output is not a desync signal.** Every Xtensa
  `op0` class fixes its instruction length by construction (narrow=2,
  FLIX=8, everything else=3) regardless of whether the finer sub-opcode is
  named, so a `?B`/`?C`/`?Balu` result means "real code hit a decoder
  gap," not "byte alignment was lost." Rejecting functions on this basis
  was tried and incorrectly threw out well-formed functions in PROC8's
  overlay bank that legitimately run 20–50% `?B`/`?C` over long, correctly
  terminating stretches.
- **`jx aN` computed dispatch truncates decompilation of the outer
  function.** Ghidra cannot recover the jump table for these ("Could not
  recover jumptable... Too many branches") and treats the indirect jump as
  a call that returns immediately, stopping the decompiled function there.
  This is a pre-existing Ghidra limitation for computed jumps generally,
  independent of the FLIX work, but it recurred in every validation target
  in this project. To see code past a `jx`, decompile the specific
  dispatch target directly (force a function there and decompile that),
  not the outer `entry`.
- **A literal/xref scanner must check slot A at every byte offset, not
  just 4-aligned ones.** FLIX bundles have no alignment requirement; a
  4-aligned-only sweep silently misses every bundle starting at an odd
  address. Ground truth to validate any such scanner against: PROC0
  `0x7ffa430f`, `0x7ffa4317`, `0x7ffa431f`, `0x7ffa4327` are four
  consecutive 8-byte bundles at addresses ≡ 3 (mod 4), and `0x7ffa431f`
  must decode as `l32r a11,0x7ff82b54`. This caught a real bug (a hidden
  third literal loader at `0x7ffa4732`) during development.
- **`entry` byte-scanning needs a plausibility filter and a forward-walk
  validation, not just the `0x36` byte match.** A real `entry` only ever
  targets `a1` with a modest frame (median 0x20 B, 99th pct 0x90); anything
  else is a coincidental `0x36` inside unrelated code (the worst observed
  false positive: `entry a8,0x5178`, an absurd 20856-byte frame, sitting a
  few bytes inside a real function's FLIX-encoded body). Confirm candidates
  by walking forward to the first terminator (`ret`/`retw`/`ret.n`/`retw.n`/
  `jx`); stop at the *first* terminator, not the last — walking past it
  through a dispatcher's jump-table-only successors previously swallowed a
  neighboring real function into a bogus 3137-byte "extent."

---

## Source map

| Topic | Primary source |
|---|---|
| Ground-truth decoder | `tools/sn200-fw/xdis.py`, `tools/sn200-fw/disany.py` |
| Ground-truth literal cross-references | `tools/sn200-fw/litref.py` |
| SLEIGH spec (Ghidra) | `tools/sn200-fw/ghidra/languages/flix.sinc` |
| FLIX width/slot derivation, evidence tables | `docs/xtensa-flix-decoding.md` |
| Function-boundary scanning, `entry` byte-scan method | `docs/sn200-function-map.md`, `tools/sn200-fw/funcmap.py`, `tools/sn200-fw/whichfunc.py` |
| `.BIN`/`.SEG` container, CPU architecture proof | `docs/sn200-firmware-re.md` §1 |
| Overlay relocation formula, descriptor table | `docs/sn200-attack-surface.md` §6.1, `docs/sn200-dangerous-commands.md` |
| `addi.n`/allow-list-vs-reject-list corrections | `docs/sn200-independent-re.md` (FLIX re-derivation addendum), `docs/sn200-readonly-startup.md` §10.1 retraction |

This document does not duplicate the evidence tables, validation runs, or
per-function walkthroughs in the sources above — it states the encoding
facts and points at where each was established. For "why 8 bytes and not
5, 6, 9, 12, 16," see `docs/xtensa-flix-decoding.md`'s Test 1/Test 2
tables; for "why is this a reject-list," see `sn200-independent-re.md` and
`sn200-readonly-startup.md` §10.1 directly.
