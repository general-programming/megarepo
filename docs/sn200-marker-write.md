# The generic boot-marker write — who builds it, and can a host reach it?

Firmware `KNGND122`. Static analysis only; no drive was touched.

`sn200-readonly-startup.md` §6.0a recorded, in passing, that PROC0 has a generic
marker-write request — "request code 6, value from `[ctx+0x50]`" — and that no
`0xFF` selector constructs it. This document is the follow-up nobody had done:
**what does construct it, and is any construction site host-reachable?**

**Verdict: clean negative on reachability, but the mechanism is now fully
mapped, and three prior claims were wrong.**

- The field called "the request code" in §6.0a is **not** the request code. The
  dispatch is on **`[ctx+0x44]`**, a 45-entry jump table. `[ctx+0x48]` is the
  **EEPROM section id**, and the marker write is *verb 1, section 6*.
- Verb 1 is an **EEPROM section WRITE**, not an erase. `sn200-oam-dispatch.md`
  §4.1 has it as "EEPROM-section erase (SBL path)".
- The marker setter **persists immediately** to EEPROM (op 2, section 6, 244
  bytes) — it is not a RAM-only write awaiting a shutdown flush.
- The only host producer of verb 1 is `0xFF`/`CDW12=0x0303`, and it hardcodes
  section **13**. No host command produces verb 1 with section 6, and nothing in
  the OAM handler ever writes the value field at all.
- The brief's open question — "does the crash-latch forcing apply to a marker
  written at RUNTIME?" — is **settled: yes, identically.** There is no
  boot-vs-runtime distinction. Refuted.
- A **new sequencing result** falls out that changes what a UART/SBL primitive
  is worth: a later marker write in the same power-on *cancels* a pending
  re-init, because marker 3 is nothing but a value in that record.

Labels: **PROVEN** = read off the instruction stream. **INFERRED** = follows
from proven facts plus a named assumption. **SPECULATIVE** = neither.

---

## 1. The PROC0 request worker, correctly decoded — PROVEN

`PROC0 0x7ffa3e48` (`entry a1,0x30`) is the System-Area / EEPROM request worker.
It is a coroutine (`l32i.n a11,a2,0x10` / `jx a11` at `0x7ffa3e51`–`0x7ffa3e6b`),
so `a2` is its request context throughout. Its task objects are created once by
`0x7ffa48c8`, which is the **only** site in any image that loads the handler
pointer (`litref.py -v 7ffa3e48` → 1 hit, `0x7ffa48f7`).

The dispatch is a **jump table**, not a compare chain:

```asm
7ffa415e: l32i  a10,a2,0x44           ; the request code
7ffa4169: { l32r a11,0x7ff82b20 ; movi a9,45 ; movi a14,0 }
7ffa4171: { s32i a14,a2,0x88   ; bgeu a10,a9,0x7ffa47f9 }   ; >= 45 -> invalid
7ffa4179: l32r  a9,0x7ff82b24         ; = 0x7ffa4184, the table base
7ffa417c: addx2 a8,a10,a10            ; index * 3
7ffa417f: add.n a8,a8,a9
7ffa4181: jx    a8
7ffa4184: j 0x7ffa47a4                ; entry 0
7ffa4187: j 0x7ffa4709                ; entry 1   <-- the marker write
7ffa418a: j 0x7ffa46eb                ; entry 2
7ffa418d: j 0x7ffa469e                ; entry 3
...                                    ; 45 three-byte `j` entries
```

**`[ctx+0x44]` is the request code; `[ctx+0x48]` is not.** §6.0a of
`sn200-readonly-startup.md` read `[ctx+0x48]`'s `bnei a9,6` as the dispatch. It
is a *sub*-test inside one arm.

### 1.1 The request code is the OAM verb — PROVEN by five field correspondences

`sn200-oam-dispatch.md` §4 established the PROC8-side OAM request layout:
`+0x118` verb, `+0x11c` section, `+0x120`/`+0x128` parameters, `+0x188` status.
PROC0 sees the same structure at a base **0xD4 lower**:

| PROC8 field | PROC0 field | evidence |
|---|---|---|
| `+0x118` verb | `[ctx+0x44]` | table entry **37** (`0x25`) is the "schedule drive re-init" arm `0x7ffa4306`; entry **42** (`0x2a`) is the read arm; entry **3** is the section-erase arm |
| `+0x11c` section | `[ctx+0x48]` | entry 3 (verb 3) tests `[ctx+0x48] == 6` and applies System-Area-specific interlocks (`0x7ffa46a1`) |
| `+0x120` param | `[ctx+0x4c]` | forwarded by `0x7ffa3ca4` to the engine's `+0x54` |
| `+0x124` param | `[ctx+0x50]` | **the marker value** |
| `+0x128` param | `[ctx+0x54]` | `0x0403`/`0x0503` write 1/0 here and `0x7ffa4306` reads `[ctx+0x54]` as the 3-vs-4 selector; for read/write verbs it is the **buffer pointer** |
| `+0x12c` | `[ctx+0x58]` | **length** (`0x0007` sets `+0x12c` = length; `0x7ffa3ca4` forwards `[ctx+0x58]` as the engine's length) |

Five independent correspondences with one constant delta. **PROVEN.**
(Mechanically this is consistent with PROC0 being handed `&req->oam`, an inner
sub-struct, while PROC8 addresses the outer object. The transport itself was
not traced and does not matter for any conclusion here.)

### 1.2 The low-level EEPROM engine and its op codes — PROVEN

Every arm that touches the EEPROM funnels through `0x7ffa3ca4(ctx, op)`, which
fills the static engine object `0x7ff8bb20` and is followed by a submit
(`0x7ffb32f8`) to handler **`0x7ffa5ad8`**:

```asm
7ffa3ca4: entry a1,0x20
7ffa3ca7: l32r  a5,0x7ff82aa4          ; = 0x7ff8bb20, the engine object
7ffa3caa: s32i  a3,a5,0x4c             ; [obj+0x4c] = op
7ffa3cad: l32i  a6,a2,0x48
7ffa3cb0: { s32i a6,a5,0x50 ; ... }    ; [obj+0x50] = section
7ffa3cb8: { l32i a4,a2,0x4c ; ... }
7ffa3cc0: { s32i a4,a5,0x54 ; bnei a3,6,0x7ffa3cd9 }
7ffa3cc8: l32i  a9,a2,0x50 ; s32i a9,a5,0x68     ; op 6: two data WORDS
7ffa3cce: l32i  a8,a2,0x54 ; s32i a8,a5,0x6c
7ffa3cd9: l8ui  a12,a2,0x50 ; s8i  a12,a5,0x5c   ; op != 6: byte flag
7ffa3cdf: l32i  a11,a2,0x54 ; s32i a11,a5,0x60   ;            buffer
7ffa3ce5: l32i  a10,a2,0x58 ; s32i a10,a5,0x64   ;            length
```

Op codes observed, by verb:

| verb (`[ctx+0x44]`) | arm | engine op | meaning |
|---|---|---|---|
| 0 | `0x7ffa47a4` | 7 (when `[ctx+0x50]==2`) | — |
| **1** | **`0x7ffa4709`** | **2 = WRITE** (section ≠ 6, ≠ 4) | **EEPROM section write** |
| 2 | `0x7ffa46eb` | 0 | — |
| 3 | `0x7ffa469e` | 3 = ERASE | section erase (`0x0003`…`0x0603`) |
| 5 | `0x7ffa45f5` | 1 | — |
| 31 | `0x7ffa438d` | 4 | — |
| 32 | `0x7ffa436f` | **6 = two-word write** | takes `[ctx+0x50]`,`[ctx+0x54]` verbatim |
| 37 (`0x25`) | `0x7ffa4306` | — | schedule drive re-init, marker 3/4 |
| 42 (`0x2a`) | `0x7ffa426b` | 8 = READ | `OAM READ RAW SA CMD` |

> **Correction to `sn200-oam-dispatch.md` §4.1.** Verb 1 is *write*, not
> "EEPROM-section erase (SBL path)". `0xFF`/`0x0303` therefore **writes** one
> byte into SBL EEPROM section 13 (`[+0x120]=1`, `[+0x128]=[a2+0]`,
> `[+0x12c]=1` at `0x300335f7`–`0x3003361e`). That is a stronger reason not to
> type it, not a weaker one — the string StrId 1633 *"Erase to SBL EEPROM
> failed"* is what misled the earlier reading.

---

## 2. The construction sites of the marker write — exhaustive, PROVEN

### 2.1 Verb 1 + section 6 (`0x7ffa4709`) — the generic one

```asm
7ffa4709: l32i   a9,a2,0x48                 ; section id
7ffa470c: { ... ; bnei a9,6,0x7ffa4760 }    ; not System Area -> other arms
7ffa4714: l32i   a10,a2,0x50                ; ARBITRARY 32-bit word
7ffa4717: s32i.n a10,a5,0x18                ; -> setter request value field
7ffa4719: { l32i a8,a2,0x54 ; ... }
7ffa4721: { s32i a8,a5,0x1c ; ... }         ; second record word
7ffa4729: s32i.n a9,a5,0x20                 ; = 6
7ffa472b: l32i   a15,a2,0x4c
7ffa472e: bnez.n a15,0x7ffa474d             ; param != 0 -> handler 0x7ffa85d8
7ffa4732: { l32r a12,0x7ff82b54 ; beqz a13,0x7ffa4072 }   ; = 0x7ffa84c8
7ffa473a: { ... ; mov a11,a12 ; mov a12,a5 }
7ffa4742: call8  0x7ffb32f8                 ; submit(a5, setter)
```

Section 4 takes a different branch (`0x7ffa4760` → handler `0x7ffac28c`);
anything else falls to `0x7ffa4786` and becomes a plain op-2 section write.

### 2.2 Verb 37 (`0x25`) — the constrained one

```asm
7ffa4306: l32r a10,0x7ff82b4c    ; 0x80000004 FACTORY
7ffa4309: l32i a11,a2,0x54       ; the selector (PROC8 +0x128)
7ffa430c: l32r a9,0x7ff82b50     ; 0x80000003 REINIT
7ffa431f: { l32r a11,0x7ff82b54 ; mov{eq,ne}z a9,a10,a11 }
7ffa4327: { s32i a9,a5,0x18 ; ... }
7ffa432f: call8 0x7ffb32f8
```

Both values come from PROC0's own literal pool. Not host-controlled.
(The `movnez`-vs-`moveqz` polarity remains **INFERRED**; slot-B ALU sub-opcode
6 is still undecoded. It does not matter — 3 and 4 both destroy the L2P.)

### 2.3 Firmware download / commit (`0x7ffabccc`) — the hardcoded one

`0x7ffabcc3`–`0x7ffabcdb`: bit 0 of the image flags word gates whether the
request is issued; the value is the hardcoded `0x80000003`. Unchanged from
`sn200-readonly-startup.md` §6.0.

### 2.4 That is all of them

`litref.py -a 7ff82b54` (the setter's pointer literal, `0x7ffa84c8`) returns
**six** sites: `PROC0 0x7ffa431f`, `0x7ffa4732`, `0x7ffabccc`, plus one each in
PROC6 (`0x7ffb80d3`), PROC7 (`0x7ffaa096`, `0x7ffb25ee`). The three non-PROC0
hits are coincidental — `0x7ff82b54` is a different literal in those images'
pools, and per `sn200-readonly-startup.md` §5(e) no core other than PROC0 holds
the marker address at all. **Three real callers, exactly as §6.0 recorded.**

### 2.5 The setter is durable, not deferred — PROVEN (new)

`0x7ffa84c8` does not merely poke RAM. After the store at `0x7ffa853b` it
builds an engine request and submits it:

```asm
7ffa853b: s32i.n a10,a5,0x0        ; *(0x7ff8c7ec) = the marker
7ffa853d: l32i.n a15,a2,0x1c
7ffa853f: s32i.n a15,a5,0x4        ; second record word
...
7ffa8585: l32r a7,0x7ff82aa4       ; = 0x7ff8bb20, the engine object
7ffa858b: { s32i a6,a7,0x54  ; movi a9,6 }
7ffa8593: { s32i a9,a7,0x50  ; movi a8,2 }    ; section 6, op 2 = WRITE
7ffa859b: s32i a8,a7,0x4c
7ffa85a6: { s32i a5,a7,0x60  ; movi a14,244 } ; buffer  = 0x7ff8c7ec
7ffa85ae: { s32i a14,a7,0x64 ; ... }          ; length  = 244 bytes
7ffa85b6: call8 0x7ffb32f8                    ; submit to 0x7ffa5ad8
```

244 bytes, section 6 — exactly the record `sn200-readonly-startup.md` §6
described. **A marker written at runtime hits EEPROM at once.** This also
independently confirms op 2 = write and the 244-byte record size.

---

## 3. Host reachability — the negative, with its limits

### 3.1 The `0xFF` surface produces verb 1 exactly once, with section 13 — PROVEN

Full re-decode of the erase-family switch (`PROC8@30000000 0x300336c6`–
`0x30033821`, overlay 21 in the descriptor table, `src2 = 0x30033338`,
runtime delta `+0x4DF88CC8`):

| sub | `CDW12` | verb stored | section stored |
|---|---|---|---|
| 0 | `0x0003` | 3 (`movi a12,3` @ `0x30033772`) | 6 (`movi a11,6`) |
| 1 | `0x0103` | 3 | 3 |
| 2 | `0x0203` | 3 | 9, then chained 8 |
| 3 | `0x0303` | **1** (`movi.n a15,1` @ `0x300335f7`) | **13** (`movi a9,13` @ `0x30033604`) |
| 4 | `0x0403` | 37 | — (`+0x128 = 1`) |
| 5 | `0x0503` | 3 | 11; resume posts verb 37 with `+0x128 = 0` |
| 6 | `0x0603` | 3 | 10 |
| — | `0x0007` | 42 | 6 |

Both constants in the verb-1 arm are `movi` immediates. `CDW12[15:8]` selects
the arm and nothing else; `CDW12[31:16]` is never read (`sn200-oam-dispatch.md`
§3). **No `0xFF` encoding yields verb 1 + section 6, and none writes `+0x124`
(the marker value) at all.**

The nearest miss is `0x0007`: verb 42, section 6, `+0x124 = 1`. It reads the
record it would have to write. One field — the verb — separates the safe read
from the escape.

### 3.2 The value is still unconstructible — PROVEN, re-affirmed

Even granting an arbitrary verb-1/section-6 request, the value must be
`0x80000008`. `litref.py -v 80000008` over all 18 images still returns exactly
two sites (PROC0 `0x7ffaaed3`, a comparison; PROC12 `0x7ffa7d70`, an Event-Log
tag), and `movi` cannot synthesise it. Nothing copies a host CDW into `+0x124`
anywhere in the OAM handler. The conjunction required —
*verb 1* **and** *section 6* **and** *value 0x80000008 under host control* — has
no site satisfying even one of its three conjuncts from a host command.

### 3.3 ⚠ Limit of the sweep, stated honestly

A struct-offset scan for `s32i …,+0x118` / `+0x11c` / `+0x124` is **not
exhaustive**, and prior docs should not be read as if it were. Handlers re-base
the request pointer and then use small displacements: the `0x0007` handler
writes all of `+0x118`…`+0x12c` through `req+0xA0` (`s32i a15,a13,0x78`,
`s32i a2,a13,0x7c`, `s8i a9,a13,0x84`, …). An offset-0x118 scan misses it
entirely.

So "no other host command builds verb 1" rests on:

- the complete `0xFF` table (`sn200-oam-dispatch.md` §3 — three command ids,
  nine encodings, byte-verified), plus
- the value-side argument of §3.2, which is base-pointer-independent.

It does **not** rest on an exhaustive scan of every request builder in PROC8.

> **☑ RESOLVED 2026-08-04 — `sn200-ec-and-allowlist.md`.** `0xEC` is
> `Admin_VUC_Enable` (overlay row 10, handler static `0x3002b6c4`, runtime
> `0x7ffbc24c`). It builds no OAM request, calls no EEPROM primitive, and after
> its parameter validation the **entire host-controlled input space of the
> command is one bit** (six dwords must be zero, three must equal
> `VOID`/`WARR`/`ANTY`, one 16-bit field must be zero, one byte must be 0 or 1).
> Its only effect is a byte at `0x7ff8f1dd`, whose two readers are both inert on
> a latched drive. **It is not the escape.** The `0xFF`-side argument above is
> now the whole story for the vendor surface. (`0xC6` command byte `0x30`, the
> other outstanding unknown, was resolved in the same period — see
> `sn200-c6-30-family.md`.)

---

## 4. Runtime marker vs boot marker — the open question, settled

The brief asked whether the crash-latch's forcing of marker 9 applies to a
marker written at **runtime** rather than at boot. **It does. There is no
distinction.** PROC0 `0x7ffaae13`–`0x7ffaaf0b`:

```asm
7ffaae1e: l32i   a11,a7,0xf4        ; secondary copy
7ffaae21: s32i.n a11,a7,0x0         ; heal the primary from it -- UNCONDITIONAL
7ffaae28: l32r   a12,0x7ff826b8     ; -> 0x7ff9ff60
7ffaae2b: l32i.n a12,a12,0x4        ; boot mode
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }   ; mode 4 skips both tests
7ffaae35: { extw ; ball a9,mask 0x1,0x7ffaaf02 }       ; CLOG
7ffaae3d: { extw ; ball a9,mask 0x4,0x7ffaaf02 }       ; PFCL
7ffaae45: l32i.n a11,a7,0x0                            ; the stored marker
7ffaae47: bne    a11,a6,0x7ffaae69                     ; a6 = 0x80000000 -> dispatch
7ffaae50: j      0x7ffaaf08                            ; empty SA -> force 9
7ffaaf08: l32r   a11,0x7ff83474     ; = 0x80000009
7ffaaf0b: { s32i a11,a7,0x0 ; j 0x7ffaae69 }
```

The marker is read from **one** place — the EEPROM record, healed into
`*(0x7ff8c7ec)` at `0x7ffaae21` — whether it was put there by boot code, by a
shutdown, or by a runtime `0x7ffa84c8` write. The `ball` tests then run before
the dispatch and overwrite it. **PROVEN.** The hoped-for distinction does not
exist.

**Two consequences worth carrying forward:**

1. **`0x7ffaae21` overwrites the primary from the secondary.** Any direct EEPROM
   write must set **both** word 0 and `+0xF4`, or it is discarded. (Already
   noted in `sn200-readonly-startup.md` §8; now confirmed as unconditional —
   the `beqi a10,4` at `0x7ffaae15` only suppresses a log line.)
2. **⚠ Do not erase System-Area section 6 before attempting a `LOAD_N_GO`.**
   The boot-mode-4 path at `0x7ffaae53` masks the stored marker with
   `0xC0000000` (literal `0x7ff82598`) and, if the result is not `0x80000000`,
   **writes `0x80000003` REINIT over it**. An erased section reads `0xFFFFFFFF`
   → mask `0xC0000000` → re-init. So `0xFF`/`0x0003` does not merely arm the
   "empty SA" predicate; it also poisons the one out-of-band route that would
   otherwise ignore the latch. This adjacency was not previously recorded.

---

## 5. What this *does* buy someone with a marker-write primitive — INFERRED

The forcing rule cuts both ways, and this is the one genuinely new operational
result here.

Marker 3 ("Drive REINIT requested") is **nothing but a value in that record**.
`0x7ffa4306` sets only `[req+0x18]`/`[req+0x1c]`, and the setter's only other
side effect is a byte at `0x7ff8cdb4` (`s8i a13,a7,0x0` at `0x7ffa8530`) — RAM
only, read as a guard by the verb-3/section-6 interlock at `0x7ffa46b7`. Nothing
else is armed, and nothing is queued for the next boot.

Therefore **a later marker write in the same power-on cancels a pending
re-init.** If a marker-write primitive is ever available — which today means the
`DiagMgr>` UART / SBL console — the correct order is:

```
1. 0xFF CDW12=0x0603        clear PFCL   (harmless, no marker, no re-init)
2. 0xFF CDW12=0x0503        clear CLOG   (schedules marker 3 -- accept it)
3. write 0x80000008 into the SA section-6 record, BOTH word 0 and +0xF4
   (overwrites the pending marker 3)
4. cold power cycle
   -> both ball tests pass, SA non-empty, marker 8
   -> startup type 3 READ ONLY, L2P restored, namespace present, writes refused
```

This is strictly better than the boot-mode-4 `LOAD_N_GO` route recorded in
`sn200-logic-escapes.md`, because it is persistent rather than per-boot and it
does not depend on the stored marker surviving the `0xC0000000` mask test.

**INFERRED**, and the named assumptions are: (a) step 2's re-init request
completes before step 3's write is issued — an ordering the operator controls
only loosely, since both are asynchronous submissions to the same engine; and
(b) erasing sections 10 and 11 does in fact clear bits 2 and 0 of the flags byte
at `0x7ff8d200`, which is the standing assumption behind the whole `0x0503`
procedure and is not re-proven here.

If step 3 is available, step 2 could equally be replaced by a direct erase of
the crash sections from the SBL console, avoiding the race entirely.

---

## 6. Second target: re-attaching the namespace — clean negative at the gate

A latched drive presents no namespace, and the L2P survives (only `0x0503`'s
re-init blanks it), so any command that re-exposes the namespace would be a full
recovery. There is none, and the reason is one instruction short of trivial.

`Admin_CheckCmdAllowed` `0x7ffa6b18`, post-crash arm, decoded in full
(`0x7ffa6b30`–`0x7ffa6bd1`):

```
allowed opcodes: 0x00 0x01 0x02 0x04 0x05 0x06 0x08 0x09 0x0A 0x0C
                 0x10 0x11 0xE6 0xEC 0xFF
                 0xC6 when CDW12[7:0] in {0x20, 0x30}
                 0xCA -> sub-list at 0x7ffa6d76
everything else: movi a9,1 ; j 0x7ffa6d05   (reject)
```

**Namespace Management `0x0D` and Namespace Attachment `0x15` are absent.**
(The `movi a11,13` at `0x7ffa6b28` is bait — `a11` is never compared against
`a3` in this arm; it belongs to one of the other three gates that share the
function. That is the same trap that made two teardowns disagree, per
`sn200-firmware-flow.md` §5.) **PROVEN.**

So the question never reaches the namespace code. And it would not help if it
did: the namespace is missing because startup type 6 skips the System Area read,
so SAM never restores the L2P and never creates the namespace
(`sn200-readonly-startup.md` §2) — not because a namespace exists and is
detached. Re-attaching a namespace whose translation tables were never loaded is
not a recovery. **INFERRED** on the second half, PROVEN on the gate.

Consistent with `sn200-oam-dispatch.md` §7: nothing in overlay 21 references
namespace startup either.

---

## 7. Verdict

| question | answer |
|---|---|
| What constructs a PROC0 marker-write request? | Three sites, all in PROC0: verb-37 (`0x7ffa4306`, values 3/4 from its own pool), firmware commit (`0x7ffabccc`, hardcoded 3), and **verb 1 + section 6** (`0x7ffa4709`, value verbatim from `[ctx+0x50]`). **PROVEN** |
| Is the generic one host-reachable? | **No.** Verb 1 has exactly one host producer — `0xFF`/`0x0303` — and it hardcodes section 13. **PROVEN** |
| Could the value be host-supplied even so? | **No.** Nothing writes OAM request `+0x124` in the `0xFF` handler, and `0x80000008` is constructed nowhere in 18 images. **PROVEN** |
| Does the latch force marker 9 over a *runtime* write? | **Yes, identically.** One read site, `0x7ffaae21`, healed before the `ball` tests. The hypothesised boot-vs-runtime distinction does not exist. **PROVEN** |
| Does the marker write persist immediately? | **Yes** — the setter itself posts an op-2 write of 244 bytes to section 6. **PROVEN, new** |
| Can a pending re-init be cancelled? | **Yes** — marker 3 is only a value in that record, so a later write overwrites it. Changes the recommended UART sequence (§5). **INFERRED** |
| Any host path that re-attaches/re-creates the namespace? | **No.** `0x0D` and `0x15` are not in the post-crash allow-list. **PROVEN** |
| Net change to the recovery procedure? | **None in-band.** `sn200-readonly-startup.md`'s conclusion stands: this is a code-execution / EEPROM-write problem, not a command-encoding one. |

**Update, 2026-08-04 — the door is closed.** `0xEC` was resolved
(`sn200-ec-and-allowlist.md`): it is `Admin_VUC_Enable`, a one-bit command that
never touches an OAM request. The requirement stated here — *OAM verb 1,
section 6, request `+0x124 = 0x80000008`* — has no host producer anywhere in the
admitted surface. That same pass re-proved the exhaustion argument with a
cleaner sweep: `litref -v 7ffa84c8` returns **exactly three** call sites for the
marker setter, all in PROC0 (verb 37, verb 1 + section 6, firmware commit), and
`litref -v 7ff8c7ec` returns 11 sites, all in PROC0.

`0xC6` command byte `0x30` — the other outstanding unknown — was resolved in the
same period (`sn200-c6-30-family.md`): a SMART-collection family that builds no
OAM request. **Every opcode the post-crash gate admits has now been read for
function, and none of them writes the marker.**
