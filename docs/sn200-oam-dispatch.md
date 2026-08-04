# `Admin_OamCmd` — the complete `0xFF` dispatch table

Opcode `0xFF` is the one vendor family that passes the Post-Crash gate intact,
so it was the last plausible place a software escape from the latch could hide.
This document enumerates **every** selector it accepts, what each one does, and
whether any of them can release a latched drive without destroying the data.

**Verdict up front: clean negative on the escape, and a decisive positive on the
`0x0603`/`0x0503` question.**

- The `0xFF` surface is **three** command ids — `0x03`, `0x04`, `0x07` — and
  nothing else. There is no boot-mode write, no marker write other than
  re-init, no namespace attach, and no unmapped corner left to search.
- **`0x0603` cannot wipe anything.** Its handler posts one EEPROM erase of the
  PFCL section and returns. It contains no startup-type test and no second
  request. **PROVEN.**
- **`0x0503` is the sole wiper.** Its *resume* handler, on erase success, tests
  `*(0x7ff87c64) == 6` and, if so, posts re-init verb `0x25`. **PROVEN.**
- So the long-standing "send `0x0603` then `0x0503`" procedure has always been
  one harmless command followed by the destructive one. See §7 for the
  narrow — but real — case where `0x0603` alone recovers a drive with its data.

Labels: **PROVEN** = read off the instruction stream. **INFERRED** = follows
from proven facts plus a named assumption. **SPECULATIVE** = neither.

---

## 1. Two methodology corrections that this work depends on

### 1.1 ☠ Overlay call targets must be resolved in **runtime** space — PROVEN

Every prior teardown of `PROC8`'s overlay bank resolved `callN` targets in the
static DDR image (`0x300xxxxx`) and concluded that ~99 % of them were
unresolvable. **That is an artefact of the wrong address space.** `callN` is
PC-relative and the linker fixed the displacements for the *execution* address
`0x7ffbc000`, not for the DDR staging copy. The correct rule is:

```
runtime_target = static_target + (0x7ffbc000 − overlay_src2)
```

i.e. apply the *same* delta used for the code itself. Statistically decisive
over overlay 22 (`src2 = 0x30033338`, delta `+0x4DF88CC8`):

| interpretation | `callN`-shaped sites whose target is an `entry` |
|---|---|
| static (`0x300xxxxx`) | **0 / 174** |
| runtime (`0x7ffbxxxx`) | **63 / 174** |

(174 counts a raw byte-sweep, so most are false positives; the point is the
0-vs-63 split.) Three independent semantic confirmations:

- `call8 → 0x3002b8e0` from overlay 22 resolves to **`0x7ffb45a8`**, the log
  function — and its `a10` is always a `StrId` literal.
- The identical rule applied to **overlay 26** (`src2 = 0x30035378`, a different
  delta) sends `call8 → 0x3002d920` to the *same* `0x7ffb45a8`. Two overlays,
  two deltas, one log function.
- `0x30030aa0` is **not** an `entry` in the static image; `0x7ffb9768` is.

**Consequence for existing docs.** Statements of the form "the handler's callee
set excludes the erase primitive `0x30030aa0` and the EEPROM primitive
`0x30031d10`" (`sn200-vuc-flash-read.md` §3, `sn200-crash-dump-retrieval.md`
§1413/§426) compare *static* labels. Those labels are not stable identities —
the same static word means a different function from each overlay. The
conclusions may still be right, but the arguments need redoing in runtime space.
And the two functions are misnamed:

| static (as written in old docs) | true runtime identity (from overlay 22) |
|---|---|
| `0x30030aa0` "flash erase primitive" | `0x7ffb9768` — **enqueue a request on the OAM worker list** (stores callback at `+0x8`, request at `+0xc`, links into `[0x7ff96b04+0x10]`). Not an erase. |
| `0x30031d10` "EEPROM primitive" | `0x7ffba9d8` — **`memset`** (byte-replicate + word/byte fill loop). Not EEPROM. |

### 1.2 Two FLIX slot decodes are wrong in `xdis.py`

- **Slot C class `0xC` with `imm8` bit 7 set is not `movi`.** At
  `0x3003377a` the decoder emits `movi a5,140`, which would corrupt the very
  pointer the next bundle dereferences (`s32i a11,a5,0x11c`). Reading the same
  field as `mov a(imm&0xF), a(reg)` yields `mov a12,a5`, `mov a11,a6`,
  `mov a10,a2` — each of which is spelled out explicitly in slot B of a sibling
  arm. `imm8 < 0x80` remains a genuine `movi` (confirmed at `0x7ffa75ee`,
  `movi a8,67`, the `0xCA` table bound).
- **Slot B `pre = 1` is undecoded** (`?B`). It is *not* a branch — conditional
  branches only occur in the format-`0xF` bundle — so no control flow is being
  missed, but register moves are. `?B 30a1a` is `mov a5,a10` (forced: both
  coroutines below reload the same value from `ctx+0x174` on resume).

Neither affects any conclusion here; both are recorded so the next reader does
not re-derive them.

---

## 2. Reaching the handler — PROVEN

`PROC8@7ff80000 0x7ffa7547`–`0x7ffa7574`, inside the admin dispatcher
`0x7ffa6db4`:

```asm
7ffa7547: { movi a10,11    ; bltu a13,a11,0x7ffa755c }   ; a13 = 236, opcode > 236 ->
7ffa755c: { movi a14,254   ; ...                     }
7ffa755f: { movi a10,22    ; bgeu a14,a11,0x7ffa7574 }   ; opcode <= 254 -> elsewhere
7ffa7567: l32i.n a12,a6,0x0
7ffa7569: l32r  a13,0x7ffa0e30                           ; -> 0x7ffbc110  <-- the handler
7ffa756c: { s32i a10,a12,0x20 ; j 0x7ffa6e89 }
```

Only `a11 == 255` falls through. `litref.py -v 7ffbc110` returns **exactly one
site** — there is no second door.

`0x7ffbc110` lies in **overlay 22** (descriptor table entry 21 at
`0x7ff81ae4 + 21*0x20`: `dst = 0x7ffbc000`, `len = 0xa00`, `src2 = 0x30033338`),
so the static body is **`0x30033448`**, and the overlay's own literal pool sits
at its base `0x30033338`. This maps the region `sn200-firmware-flow.md` flagged
as unmapped. The already-documented erase switch at `0x300336c6` falls inside
the same overlay, which independently confirms the base.

Runtime ↔ static for everything below: `static = runtime − 0x7ffbc000 + 0x30033338`.

### The gate — PROVEN

`Admin_CheckCmdAllowed` `0x7ffa6b18`, Post-Crash arm:

```asm
7ffa6ba0: movi a8,255
7ffa6ba3: { extw ; beq a3,a8,0x7ffa6cfb }   ; -> allowed
```

`a3` is the opcode; `a4` (the `CDW12[7:0]` byte the `0xCA` and `0xC6` arms
inspect) is **never consulted for `0xFF`**. So **every selector in this document
is reachable on a latched drive** — including the two catastrophic ones.

---

## 3. The command-id dispatch — PROVEN, and it is complete

`0x30033448` (`entry a1,0x30`) is a coroutine: `l32i.n a9,a2,0x18` / `jx a9`
resumes, otherwise first entry falls to `0x300334b5`:

```asm
300334b5: addmi a11,a2,256           ; a2+0x100 = the parsed-command struct
300334b8: l8ui  a11,a11,0x38         ; = CDW12[7:0]
...
300334db: { s8i a10,a2,0x47 ; beqi a11,3,0x30033529 }   ; -> erase coroutine
300334e3: beqi a11,7,0x30033531                          ; -> read-raw-SA coroutine
300334e6: beqi a11,4,0x30033500                          ; -> startup-mode probe (inline)
300334e9: l32r a13,-> 0x40040000                         ; else: invalid
300334ef: l32r a10,-> StrId 1626 "OAM CMD: Received Unsupported Command 0x%08x."
300334f7: s32i a12,a2,0x160                              ; status |= 0x40040000
```

All three `beqi` sit at verified instruction boundaries (hand-decoded from the
bundle bytes; the `?B` slots in this run are format-`0xE`, which has no
conditional-branch slot). **There are no other command ids.** `CDW12[31:16]` is
never read by any arm.

| `CDW12[7:0]` | handler (static / runtime) | shape | identity |
|---|---|---|---|
| `0x03` | `0x3003353c` / `0x7ffbc204` | coroutine | **OAM ERASE CMD** — 7 sub-commands, §4 |
| `0x04` | `0x30033500` / `0x7ffbc1c8` | inline | **startup-mode probe** — pure read, §5 |
| `0x07` | `0x30033824` / `0x7ffbc4ec` | coroutine | **OAM READ RAW SA CMD** — §6 |
| anything else | `0x300334e9` / `0x7ffbc1b1` | inline | rejected, `status |= 0x40040000`, no side effect |

**Only command id `0x03` reads the sub byte.** `0x04` and `0x07` never touch
`CDW12[15:8]`, so `0x0104`, `0xFF04`, `0x9907` … all behave exactly as `0x0004`
/ `0x0007`. That is a small safety margin on the two harmless commands, and it
is the *only* place one exists.

### The full set of valid `0xFF` `CDW12` values

```
0x0003  0x0103  0x0203  0x0303  0x0403  0x0503  0x0603     (erase family)
0x0004                                                      (startup-mode probe)
0x0007                                                      (read raw System Area)
```

Nine encodings. Everything else returns "Received Unsupported Command" and does
nothing. In particular **`0xFF`/`0x0720` and `0xFF`/`0x0820` are not commands at
all** — command id `0x20` is not 3, 4 or 7. Those two selectors belong to
opcode **`0xC6`** (`sn200-command-reference.md` line 79); the "never send"
table in `sn200-runbook.md` attributes them to `0xFF`, which is wrong in both
directions: harmless under `0xFF`, still unidentified under `0xC6`.

---

## 4. `CDW12[7:0] = 0x03` — the erase family, arm by arm

Coroutine `0x3003353c`. It allocates an **OAM request object** (`call8 →
0x7ffafacc`), stashes it at `ctx+0x174`, `memset`s 32 bytes of it, then
switches on the sub byte:

```asm
300336be: l32i.n a12,a1,0x8        ; the command context
300336c0: addmi  a12,a12,256
300336c3: addi   a12,a12,-84       ; = ctx+0xac
300336c6: l8ui   a11,a12,0x8d      ; = ctx+0x139 = CDW12[15:8]
```

Every arm fills the same request fields and posts it to the OAM worker list:

| request field | meaning |
|---|---|
| `+0x118` | **verb** — `1` = section **WRITE**, `3` = section erase, `0x20` = two-word write, `0x25` = schedule drive re-init, `0x2a` = read |
| `+0x11c` | EEPROM **section id** |
| `+0x120` / `+0x124` / `+0x128` / `+0x12c` | verb parameters; for read/write verbs `+0x128` is the **buffer** and `+0x12c` the **length** |
| `+0x188` | completion status (zeroed by `0x7ffba674`, tested on resume) |

The verb space is shared across the IPC boundary: PROC0's worker `0x7ffa3e48`
dispatches a 45-entry jump table at `0x7ffa4184` on the same numbers, seeing the
whole struct at a base **0xD4 lower** (`+0x118` → `[ctx+0x44]`, `+0x11c` →
`[ctx+0x48]`, …). Verb 1 is a **write**, not an erase — StrId 1633 *"Erase to
SBL EEPROM failed"* is a misleading string, and `0x0303` writes one byte into
SBL EEPROM section 13. Full decode in `sn200-marker-write.md` §1.

### 4.1 The table — PROVEN

| sub | `CDW12` | arm (static / runtime) | verb | section | resume handler | what happens after a *successful* erase |
|---|---|---|---|---|---|---|
| 0 | `0x0003` | `0x30033772` / `0x7ffbc43a` | 3 | `6` System Area | `0x30033571` | return |
| 1 | `0x0103` | `0x30033795` / `0x7ffbc45d` | 3 | `3` Bad Block list | `0x30033652` | return |
| 2 | `0x0203` | `0x300337b8` / `0x7ffbc480` | 3 | `9` BIST Script | `0x30033643` | **chains** a second erase, verb 3 section `8` BIST Status (`0x3003372c`), resume `0x30033634` |
| 3 | `0x0303` | `0x30033661` / `0x7ffbc329` | 1 | `13` SBL | `0x300335f7` → `0x300335e8` | return ☠ (drive will not POST) |
| 4 | `0x0403` | `0x300337db` / `0x7ffbc4a3` | **`0x25`** | — | `0x300335d9` | ☠ posts re-init **with `+0x128 = 1`**, unconditionally |
| 5 | `0x0503` | `0x300337fe` / `0x7ffbc4c6` | 3 | `11` (`0x0b`) CLOG | `0x300335ca` | **if `*(0x7ff87c64) == 6`**, posts re-init with `+0x128 = 0` |
| 6 | `0x0603` | `0x3003374f` / `0x7ffbc417` | 3 | `10` (`0x0a`) PFCL | `0x300335a3` | **return — nothing else at all** |
| else | — | `0x300336f2` | — | — | — | StrId 1636 "Received Bad Erase sub-cmd: %d", `status |= 0x40040000` |

Section ids read directly off the `movi` in each arm's second bundle
(`6`, `3`, `9`, `13`, —, `11`, `10`) and corroborated by the failure string each
resume handler logs (StrIds 1628–1636, verbatim: "system area 0", "bad block
table 0", "BIST Script", "BIST Status", "SBL EEPROM", "Drive Uninit", "Crash
Dump", "PFail Crash Dump").

### 4.2 The decisive difference between sub 5 and sub 6 — PROVEN

The *forward* arms are near-identical: same verb, same shape, differing only in
the section constant (`11` vs `10`) and the continuation literal. The
**resume handlers are not**:

```asm
; sub 6 resume, 0x300335a3  (0x0603)
300335a3: l32i   a13,a12,0x188      ; erase status
300335a6: beqz.n a13,0x300335bf     ; success -> the plain completion tail. THE END.
300335a8: l32r   a10,-> StrId 1635 "OAM ERASE CMD: Erase to PFail Crash Dump failed."

; sub 5 resume, 0x300335ca  (0x0503)
300335ca: l32i   a11,a12,0x188
300335cd: beqz   a11,0x30033704     ; success -> KEEP GOING
300335d0: l32r   a10,-> StrId 1634 "OAM ERASE CMD: Erase to Crash Dump failed."

30033704: l32r   a14,-> 0x7ff87c64  ; the startup-mode word
30033707: l32i.n a14,a14,0x0
30033709: { extw ; bnei a14,6,0x300335bf }        ; not latched -> plain return
30033711: { s32i a7,a12,0x128 ; movi a15,37 }     ; param = 0, verb = 0x25
30033719: { s32i a15,a12,0x118 ; mov a11,a6 }
30033721: call8 -> 0x7ffb9768                     ; POST THE RE-INIT
30033724: l32r a9,-> 0x7ffbc279                   ; resume: StrId 2933
                                                  ; "Schedule reinit after crash dump erase failed."
```

**`0x0603` has no `bnei a14,6`, no second post, and no path to verb `0x25`.**
There is no register value, no CDW, and no drive state that makes it schedule
anything. This is the single most operationally important fact in this
document.

> **Correction to `sn200-command-reference.md`:** the claim that `0x0603` has a
> "byte-for-byte identical handler to `0x0503` except the section-id constant"
> is wrong. The *arms* are near-identical; the *resume handlers* are where they
> diverge, and the divergence is exactly the wipe.

### 4.3 `0x0403` vs the reinit fired by `0x0503` — one field, `+0x128` — PROVEN/INFERRED

Both post verb `0x25`. The only difference is the parameter:

| | `+0x128` | PROC0 outcome |
|---|---|---|
| `0x0403` Drive Uninit | **1** | marker `0x80000004` — **FACTORY** |
| `0x0503` auto-reinit (latched only) | **0** | marker `0x80000003` — **REINIT** |

PROC0 `0x7ffa4306`–`0x7ffa4327` loads both literals and selects between them
with a conditional-move on `[req+0x54]` before storing to the marker setter's
value field. The *selection* is PROVEN; the *polarity* (non-zero ⇒ `0x80000004`)
is **INFERRED** — the FLIX slot-B ALU sub-opcode `6` is not decoded, so
`movnez` vs `moveqz` is not read off the stream. It is corroborated two ways:
`0x0503` is field-observed to produce a healthy full-capacity zeroed drive
(REINIT, marker 3), and the existing `sn200-readonly-startup.md` §6.0 trace
reaches the same assignment independently.

Either way both markers destroy the L2P. The distinction matters only for
naming, not for safety.

### 4.4 ☠ `0x0003` is one nibble from the triage probe

`0x0003` erases **EEPROM System-Area section 6** — the section that holds the
244-byte boot-marker record (`sn200-readonly-startup.md` §6). The very first
command in the runbook is `--cdw12=0x0004`. **A single mistyped digit turns the
safe read into an erase of the drive's boot-state record**, which additionally
trips the third latch predicate ("System Area empty") on the next start. This
adjacency was not previously documented and is more dangerous than the
already-flagged `0x0403`/`0x0503` pair, because `0x0004` is typed on *every*
drive, including healthy ones.

Full adjacency map for the `0xFF` family:

```
0x0003  erase System Area 0        <- one nibble from the probe
0x0004  startup-mode probe (SAFE)
0x0007  read raw System Area (safe)
0x0103  erase bad block table
0x0203  erase BIST script + status
0x0303  ☠ erase SBL EEPROM — permanent brick
0x0403  ☠ Drive Uninit — FACTORY reinit, ungated
0x0503  crash-dump erase — REINIT (wipes) when latched
0x0603  pfail-dump erase — never wipes
```

---

## 5. `CDW12 = 0x0004` — the startup-mode probe — PROVEN read-only

```asm
30033500: l32r   a9,-> 0x7ff87c64
30033503: l32i.n a10,a9,0x0            ; startup type
30033505: bnei   a10,128,0x30033519
30033508: l32r   a15,-> 0x81800000     ; type == 0x80: fail with 0x81800000
...
30033519: l32i.n a8,a9,0xc             ; flags word
3003351b: slli   a11,a10,8
3003351e: or     a8,a8,a11
30033521: s32i   a8,a2,0x154           ; CQE DW0 = (startup_type << 8) | flags
```

No call, no store outside the command context. **PROVEN read-only.** The
field-observed `0x00000601` decodes as type 6 (`INVALID` / Post Crash), flags 1.
Type `0x80` is a distinct "not ready" report, not a mode.

---

## 6. `CDW12 = 0x0007` — read raw System Area — INFERRED read-only

Coroutine `0x30033824`. Allocates a request, then:

```asm
300338ac: s8i   a9,a13,0x84       ; a9 = 1        (request+0x124)
300338b4: s32i  a8,a13,0x80       ; 0             (request+0x120)
300338bc: s32i  a2,a13,0x7c       ; section 6     (request+0x11c)  <- System Area
300338c4: s32i  a15,a13,0x78      ; verb 42 (0x2a) (request+0x118)  <- READ
300338c9: s32i  a14,a13,0x88      ; buffer  ] from a descriptor at 0x7ff82904
300338d4: s32i  a12,a13,0x8c      ; length  ]
300338dc: call8 -> 0x7ffb9768     ; post
```

On success (`request+0x188 == 0`) it builds a second request that DMAs
**`0x20000` = 131 072 bytes** (literal `0x300333d0`) to the host and posts it;
on failure it logs StrId 2934 *"OAM READ RAW SA CMD: Read of System Area
journal from EEPROM failed."*

Callee set for the whole handler, resolved in runtime space: `0x7ffafa88`
(free), `0x7ffafacc` (alloc), `0x7ffb45a8` (log), `0x7ffb9768` (enqueue),
`0x7ffba674`/`0x7ffba698`/`0x7ffba8a8` (request-field setters — `0x7ffba674`
writes only `+0x178…+0x188` of the request and zeroes the status),
`0x7ffba9d8` (`memset`). **No erase verb, no program path, no media write.**
Marked INFERRED rather than PROVEN only because verb `0x2a` was not traced
through the worker to its leaf.

This is the one genuinely useful *new* read on a latched drive: it returns the
System-Area journal, i.e. the drive's **actual** stored boot marker, rather than
the mode the boot code inferred. It needs a 131 072-byte host buffer; the length
is hard-coded, so a short buffer is a DMA-length mismatch, not a truncation.
Not yet exercised on hardware.

---

## 7. The escape hunt — result

The brief asked for a selector that sets the boot mode, clears the latch without
scheduling the re-init, writes the marker byte directly, or re-attaches the
namespace. Against the complete table in §3:

| wanted | present in the `0xFF` surface? |
|---|---|
| set boot mode / `LOAD_N_GO` | **No.** `0x7ff87c64` is *read* by `0x0004` and by the `0x0503` gate. Nothing in overlay 22 stores to it. |
| write an arbitrary boot marker | **No.** Only verb `0x25` is reachable, and only with `+0x128 ∈ {0,1}` ⇒ markers 3 and 4. PROC0's generic marker-write is **verb 1 with section id 6**, value taken verbatim from request `+0x124` — and the only `0xFF` producer of verb 1 is sub 3 (`0x0303`), which hardcodes section **13**. No selector writes `+0x124` at all. Followed to the end in `sn200-marker-write.md`. |
| clear the latch without the re-init | **Only for a PFCL-armed latch** — see below. For a CLOG-armed latch, `0x0503` is the only eraser of section `0x0b`, and its own resume handler schedules the re-init before it can return. There is no host-visible knob between the two. |
| re-attach the namespace | **No.** Nothing in overlay 22 references namespace startup. |

The re-init cannot be dodged by racing either: the mode word is read *after* the
erase completes, and it is only ever written by boot-side code, so its value is
fixed for the life of the power-on.

### 7.1 A PFCL-only latch — PROVEN mechanism, **WITHDRAWN as a procedure**

> **☠ Read `sn200-section-arming.md` before acting on anything below.** The
> mechanism in this section is correct and unchanged. Its *precondition* is
> unreachable: the boot that latches on PFCL writes marker `0x80000009` at
> `0x7ffaaf08` and falls into the marker dispatch, which routes marker 9 to the
> `UNEXSTRT` stub writer `0x7ffaad01` — the one and only `verb == 1` write
> against either crash section in PROC0 — which stamps **section 11 (CLOG)** on
> that same boot. A drive you can probe is always both-armed. On a both-armed
> drive `0x0603` is inert (the boot predicate is an OR) and destroys the PFail
> dump. The "test it before believing it" recipe below is retained only as a
> record of the reasoning; **do not run step 2.**


The boot predicate latches on **CLOG bit 0 OR PFCL bit 2 OR empty System Area**
(`sn200-firmware-flow.md` §2). `0x0603` clears the PFCL section, and clears it
with **no marker, no re-init, no data cost**. Therefore:

> **If a drive is latched with PFCL armed and CLOG clear, `0xFF`/`0x0603`
> followed by a cold power cycle should return it to a normal startup with the
> L2P and all user data intact.**

Confidence: **high on the mechanism** (the handler is fully read and posts
exactly one erase), **moderate on the scope**. The scope caveat is real and was
not invented here: `UNEXSTRT` — the stub written on *any* start not preceded by
a recorded clean shutdown — stamps section `0x0b` **CLOG**, so every ordinary
power-event latch, and every one of the ~5 s reset-loop iterations, arms the arm
that `0x0603` cannot touch. A PFCL-only latch is the exception, not the rule.

**Test it before believing it**, and test it in this order:

```sh
# 1. Which section is armed? Success = armed; SC 0xC3 = not armed.
nvme admin-passthru $D --opcode=0xc6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b   # CLOG
nvme admin-passthru $D --opcode=0xc6 -n 0 --cdw10=2 --cdw12=0x0520 --data-len=8 -r -b   # PFCL

# 2. ONLY if CLOG says "not armed" and PFCL says "armed":
nvme admin-passthru $D --opcode=0xff -n 0 --cdw10=0 --cdw12=0x0603 --data-len=0
#    then a COLD power cycle, then re-probe 0x0004 and expect byte[1] != 6.
```

Adjacent dangerous encodings for that one command, spelled out: `0x0403` is one
nibble away and is an ungated FACTORY re-init; `0x0303` is two away and is a
permanent brick; `0x0503` is one away and is the wipe. There is no typo of
`0x0603` inside the family that is merely inert.

### 7.2 What this changes about the standard procedure

Nothing about the outcome, everything about the reasoning. The runbook's
`0x0603` → `0x0503` sequence has always been *one inert command followed by the
destructive one*. `0x0603` was never contributing to the recovery of a
CLOG-latched drive and was never the thing that cost the data. Dropping it
changes nothing; keeping it changes nothing. **The data cost is `0x0503`, alone,
and only because the drive is already in mode 6 when it is sent.**

The tempting inverse — "so send `0x0503` *before* the drive latches" — is the
circularity already recorded in `sn200-nondestructive-recovery.md` §3: reaching
a non-latched state requires the section to be clear, which is the thing you
were trying to achieve.

---

## 8. Per-selector safety summary

| `CDW12` | reachable while latched | class | trace |
|---|---|---|---|
| `0x0004` | yes | **read-only** (PROVEN) | no call, no store outside the command context |
| `0x0007` | yes | **read-only** (INFERRED) | verb `0x2a` read + host DMA; callee set has no erase/program |
| `0x0003` | yes | **destructive** | erases System Area 0 — also arms the "empty SA" latch predicate |
| `0x0103` | yes | **destructive** | erases bad-block table 0 |
| `0x0203` | yes | **destructive** | erases BIST Script, then chains BIST Status |
| `0x0303` | yes | ☠ **catastrophic** | erases SBL EEPROM — drive will not POST |
| `0x0403` | yes | ☠ **catastrophic** | verb `0x25` param 1 ⇒ marker 4 FACTORY, **no startup-type gate** |
| `0x0503` | yes | **destructive when latched** | erases CLOG, then verb `0x25` param 0 ⇒ marker 3 REINIT iff mode == 6 |
| `0x0603` | yes | **erases PFail dump only** | no marker, no re-init, no user-data cost |
| anything else | yes | inert | `status |= 0x40040000`, no side effect |

"Reachable while latched" is uniform because the Post-Crash gate admits opcode
`0xFF` on the opcode alone and never inspects `CDW12`.
