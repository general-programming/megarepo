# `0xC6` command byte `0x30` — the last un-audited post-crash surface, resolved

Firmware `KNGND122`. Static analysis only; no drive was touched.

`0x30` was the largest un-identified action family reachable on a latched drive:
seven sub-commands, admitted by the post-crash gate alongside `0x20`, one nibble
from every read the runbook types. This document names all seven, traces each to
its effector, and answers the escape question.

**Verdict up front — a clean negative, with a positive identification.**

- **`0xC6`/`0x30` is the SMART / drive-statistics *collection* family.** Sub 0's
  worker emits StrId 1952 `"SMART update failed from one of the managers - did
  not save to DDR"`. `0x20` reads the log; `0x30` refreshes the counters behind
  it. **PROVEN.**
- **No sub writes the boot marker, the CLOG/PFCL arming, the namespace, the
  L2P, the gate, or the startup-type variable.** The strongest single fact:
  `litref -v` over both PROC8 images returns **0 sites** for `0x7ff8c7ec` (the
  marker), `0x7ff8d200` (the crash-section flags byte), and the constants
  `0x80000003` / `0x80000008` / `0x80000009`. PROC8 cannot name any of them.
  **PROVEN.**
- **No sub builds an OAM/EEPROM request.** The `0xFF` erase family's signature —
  a 32-bit verb at `req+0x118` and section id at `req+0x11c` before
  `call8 0x7ffb9768` — appears **nowhere** in the `0x30` subtree.
- **Three of the seven disable themselves on a latched drive.** Subs 2, 4 and 5
  read `*(0x7ff87c64)` and return immediately when it is `6` (Post Crash),
  because their work needs a System Area that startup type 6 never loaded.
- **Two prior claims in `sn200-c6-dispatch.md` were wrong and are corrected in
  §6**, one of them operator-facing: **`0x30` does *not* require `CDW10 == 0`.**
  A mistyped `0x0320` probe (which carries `CDW10 = 2`) passes the length check
  and executes sub 3.

Labels: **PROVEN** = read off correctly-decoded instructions. **INFERRED** =
short chain over proven facts. **SPECULATIVE** = neither.

---

## 1. Address-space groundwork, and a corrected constant

Overlay 18 (`dst = 0x7ffbc000`, `src2 = 0x3002ea38`):

```
runtime = static + 0x4FF8D5C8        (= 0x7ffbc000 − 0x3002ea38)
```

`sn200-c6-dispatch.md` §1 prints this delta as `0x4DF915C8`. That is a typo —
its own table (`0x3002c1a0 → 0x7ffb9768`, `0x30026fe0 → 0x7ffb45a8`) is
consistent only with `0x4FF8D5C8`, and the typo'd value resolves nothing.
Every runtime address below uses the corrected constant. **PROVEN by
arithmetic.**

Two decoder facts, recorded so the next reader does not re-derive them:

- **`xdis.py`'s `addi.n` operand order is right, do not "fix" it.** Xtensa RRRN
  puts the *destination* in `r` and the *immediate* in `t`. The interlock is the
  enqueue: every sub-arm does `addi.n a2,a5,8` and passes `a2` as the worker
  node, and `0x7ffb9768` zeroes `node+0x10` — which is `a5+0x18`, exactly the
  resume-PC slot each worker reads back with `l32i.n a9,a2,0x18`.
- **The recurring bogus `movi aX,138` / `movi aX,130` in slot C is a `mov`.**
  Slot-C class `0xC [s] 8 [t]` is `mov at,as`; `xdis.py` reads it as
  `movi a<s>, 0x8|t`. Interlock: the shared yield epilogue at `0x3002fe63`
  prints `; movi a10,130` where the *identical* epilogue in sub 2
  (`0x3002f6d8`) has no slot C and its callers instead set `a2` explicitly with
  `movi.n a2,6`. `0x8a`/`0x82` are `0x80|10` and `0x80|2`. Anything a prior
  agent built on a slot-C `movi` with an operand in 128–143 is suspect.

---

## 2. The dispatch — all seven arms, PROVEN

Top-level `0x3003147c` routes command byte `0x30` (`beq a10,a11` with
`a11 = 48` at `0x3003158e`) to `0x3003162c`, which enqueues handler
`[0x3002eebc] = 0x7ffbd400` = static **`0x3002fe38`**.

`0x3002fe38` reads the sub byte at `ctx+0x139` (`= CDW12[15:8]`, via
`addmi a7,a2,256 ; addi a7,a7,-16 ; l8ui a9,a7,0x49`) and dispatches:

| sub | `CDW12` | arm | handler static / runtime | enqueued worker (main image) |
|---|---|---|---|---|
| 0 | `0x0030` | `0x3002ffae` | `0x3002f908` / `0x7ffbced0` | `0x7ffa9374`, `0x7ffa9174` |
| 1 | `0x0130` | `0x3002ffc3` | `0x3002fac4` / `0x7ffbd08c` | — (calls `0x7ffb75e0` inline) |
| 2 | `0x0230` | `0x30030011` | `0x3002f610` / `0x7ffbcbd8` | — (mailbox inline) |
| 3 | `0x0330` | `0x3002ffd2` | `0x3002ef44` / `0x7ffbc50c` | `0x7ffa6408`, `0x7ffa550c` |
| 4 | `0x0430` | `0x3002ffe7` | `0x3002f9a8` / `0x7ffbcf70` | `0x7ffa97f4` |
| 5 | `0x0530` | `0x3002fffc` | `0x3002f9fc` / `0x7ffbcfc4` | `0x7ffa9a00`, `0x7ffb247c` |
| 6 | `0x0630` | `0x3002ff66` | `0x3002f700` / `0x7ffbccc8` | — (mailbox inline) |
| ≥7 | `0x0730`…`0xFF30` | `0x3002feac` | inline | — (trace-ring record, §5) |

Handler addresses are read out of the literal pool at `0x3002ebcc`–`0x3002ec1c`,
not guessed. Sub 1's arm has one extra step: it calls `0x3002c400`
(`0x7ffbd9c8`) on `0x7ff8f46c` and, if that returns `1`, yields with resume PC
`0x7ffbd519` (= its own enqueue site `0x3002ff51`) — i.e. it *waits* rather than
diverting.

---

## 3. What each sub does — per-sub, with its effector

Every sub is a coroutine of the same shape: allocate a job (`0x30022504` =
`0x7ffafacc`), fill fixed fields, enqueue a worker on the list head at
`0x7ff96b04` via `0x3002c1a0` (= `0x7ffb9768`), yield, free on resume. The only
effectors any of them reach are:

| effector | runtime | what |
|---|---|---|
| `0x3002d72c` | `0x7ffbacf4` | internal **mailbox transmit** — sends a 12/20/32-byte message and reads the reply |
| `0x3002d410` | `0x7ffba9d8` | `memset` on the job |
| `0x3002c400` / `0x3002c430` | `0x7ffbd9c8` / `0x7ffbd9f8` | list get / put |
| `0x30022504` / `0x300224c0` | `0x7ffafacc` / `0x7ffafa88` | alloc / free |
| `0x30026fe0` | `0x7ffb45a8` | `Log_Emit` |

**There is no erase primitive, no program primitive, no EEPROM section API, no
DMA-to-host descriptor and no PROC0 request anywhere in the closure.** PROVEN by
exhaustive extraction of `callN` targets at real instruction boundaries over
`0x3002ef44`–`0x3003002c` and over every enqueued main-image worker.

### 3.0 Sub 0 `0x0030` — SMART update, **runs while latched**

`0x3002f908` loops a counter at `job+0x184` from 0 to 130, enqueueing
`0x7ffa9374` each pass; `0x7ffa9374` is where StrId 1952
`"SMART update failed from one of the managers - did not save to DDR"` is
emitted (`l32r a10,0x7ffa1124 ; call8 0x7ffb45a8` at `0x7ffa93f8`). It calls
`0x7ffa9130` (a setter for `*(0x7ff87c5c)`) and `0x7ffa9144` (its reader).
`0x7ff87c5c` has **exactly two references in all 18 images** — that setter and
that reader — so it is a private flag of the statistics module, not a boot
variable. Despite the address, it is *not* adjacent-in-function to the startup
type at `0x7ff87c64`.

Class: **state-mutating, DRAM only.** No startup-type guard. **PROVEN.**

### 3.1 Sub 1 `0x0130` — per-manager request census, **runs while latched**

`0x3002fac4` walks `[*(0x7ff821b0) + 0x14]` entries; for each it builds a byte
selector at `ctx+0x48..0x4b`, calls `0x3002a018` (= `0x7ffb75e0`) with a15 = 16
then 17, and copies the returned words into RAM tables at `0x7ff917f0` (+0x00…
+0x20) and `0x7ff8f49c`, accumulating carries. `0x7ffb75e0` is a 20-byte
mailbox message builder that ends in `call8 0x7ffbacf4`.

Class: **state-mutating, DRAM only.** **PROVEN.**

### 3.2 Sub 2 `0x0230` — **inert while latched**

```asm
3002f628: l32r a8,-> 0x7ff87c64
3002f62b: l32r a9,-> 0x7ff96b04
3002f62e: l32i.n a8,a8,0x0
3002f630: { l32i a9,a9,0x0 ; beqi a8,6,0x3002f69f }   ; startup type 6 -> tail
```

`0x3002f69f` is the common tail (the non-6 path falls into it at `0x3002f698`),
so type 6 skips the entire body: a 12-byte mailbox message carrying
`0x0003ffff` and `0x82180000` to `0x3002d72c`. **PROVEN.**

### 3.3 Sub 3 `0x0330` — counter aggregator, **runs while latched**, largest arm

`0x3002ef44`, 0x6cc bytes, nine enqueue sites. It reads 64-bit counter pairs out
of the DRAM tables at `0x7ff85d00`, `0x7ff85d60`, `0x7ff86004`, `0x7ff86030`,
`0x7ff828c4`, `0x7ff828d4`, computes deltas with carry, and writes them back to
the same tables; it drives a byte loop counter at `job+0x179` (0..2) and
enqueues `0x7ffa6408` (list shuffling: `0x7ffac068`/`0x7ffac0a4`/`0x7ffb99c8`/
`0x7ffb99f8`) and `0x7ffa550c` (20-byte mailbox reads). It calls the mailbox
only indirectly and never calls `Log_Emit`.

Class: **state-mutating, DRAM only.** **PROVEN.**

> **`sn200-c6-dispatch.md` §5's "logs StrId 111 *OCP interrupt dispatch table is
> full*" is a false positive, and it was on the wrong sub.** The word is the
> constant `0x006f6006` at `0x3002f211` — inside sub **3**, not sub 6 — and it
> merely satisfies `disany.py`'s log-word heuristic (`sid < len(table)`,
> `na <= 12`, middle nibble 0). StrId 111 takes zero arguments; this word claims
> six. No sub of `0x30` registers anything with the interrupt dispatcher.

### 3.4 Sub 4 `0x0430` — **inert while latched**

`0x3002f9a8` enqueues `0x7ffa97f4`. That worker's first-entry path is
`0x7ffa98a6`:

```asm
7ffa98a6: l32r a13,-> 0x7ff87c64
7ffa98a9: l32i.n a13,a13,0x0
7ffa98ab: beqi a13,6,0x7ffa9887      ; -> movi.n a2,0 ; mov.n a3,a9 ; retw.n
```

`0x7ffa9887` is a bare `return 0`, so the coroutine completes without ever
yielding and is never resumed. The body it skips (`0x7ffa9809`) is the 20-byte
mailbox message that composes `(job+68) & 0x0003ffff` against `0x82180000`.
**PROVEN.**

### 3.5 Sub 5 `0x0530` — **inert while latched**, and the closest thing to a lead

`0x3002f9fc` loops index 0..4 (`*(0x7ff917e4)`), reads
`*(0x7ff81410 + idx*4)`, and:

- entry `== 53` → enqueue `0x7ffa9a00`, which begins `l32r a8,-> 0x7ff87c64 ;
  l32i.n a8,a8,0x0 ; beqi a8,6,0x7ffa9907` — the same skip;
- otherwise → `memset(job+0x110, 0, 56)`, set `job+0x11c` and `job+0x120`
  (16-bit), zero `job+0x130/0x134/0x138`, enqueue `0x7ffb247c`.

`0x7ffb247c` is the interesting one, and it is also gated:

```asm
7ffb2518: l32r a12,-> 0x7ff87c64
7ffb251b: l32i.n a12,a12,0x0
7ffb251d: beqi a12,6,0x7ffb24f1      ; -> tail, return 0
7ffb2520: mov.n a10,a2
7ffb2522: call8 0x7ffb1fe4
```

`0x7ffb1fe4` looks alarming at a glance — it writes `[a2+0x118] = 1`,
`[a2+0x11c]`, `[a2+0x120]`, `[a2+0x124]`, the exact offsets the `0xFF` erase
family uses for verb / section / params, and it logs StrId 1620
`"Invalid Admin SysArea index %d"`. **It is not an OAM request builder.** It is
a *System-Area index → (buffer, type, size)* resolver: given an index in 40..56
it picks a RAM buffer pointer out of a table (`0x7ff82744`, `0x7ff827d4`,
`0x7ff82804`, `0x7ff82824`, …) into `+0x124` and a type code into `+0x11e`. The
consumers, `0x7ffb22cc` and `0x7ffb2394`, then perform a System-Area **load**
through the mailbox and log StrIds 1621/1622 `"Admin: SysArea not loaded, Frame
doesn't exist"` / `"Admin: SysArea load error"`. Read path, not write path.

Two distinct structs share the `+0x118`/`+0x11c` offsets. **That is the trap in
this family**: an offset-only scan for "OAM verb at +0x118" produces a false
positive here. The discriminator is the *enqueued handler function*, not the
field offset — `0xFF`'s erase arms enqueue a PROC0-forwarding handler, these
enqueue statistics workers.

The gate also explains itself: startup type 6 never loads the System Area
(`sn200-readonly-startup.md` §2), so every SysArea access would fail. **PROVEN.**

### 3.6 Sub 6 `0x0630` — **runs while latched**

`0x3002f700` builds two 32-byte mailbox messages — `{266, 44, 0, 0, …}` then
`{266, 43, 0, 0, …}` — sends each with `call8 0x3002d72c`, reads back 64-bit
result pairs, and accumulates them into `0x7ff85cf0 + 0x50/0x54/0x58/0x5c` plus
a 16-bit field at `0x7ff8efb8`. Also touches `*(0x7ff8efb4)` and reads the
hardware base `0x83900000`.

Class: **state-mutating, DRAM only.** **PROVEN.**

---

## 4. Per-sub safety matrix

| sub | `CDW12` | runs while latched | reads | writes | destructive |
|---|---|---|---|---|---|
| 0 | `0x0030` | **yes** | DRAM SMART tables | DRAM, `*(0x7ff87c5c)` | no |
| 1 | `0x0130` | **yes** (waits on `0x7ff8f46c`) | mailbox | `0x7ff917f0`, `0x7ff8f49c` | no |
| 2 | `0x0230` | no — startup-type-6 skip | — | — | no |
| 3 | `0x0330` | **yes** | mailbox, DRAM counters | DRAM counters | no |
| 4 | `0x0430` | no — startup-type-6 skip | — | — | no |
| 5 | `0x0530` | no — startup-type-6 skip (both branches) | — | — | no |
| 6 | `0x0630` | **yes** | mailbox counters 43/44 | `0x7ff85cf0+`, `0x7ff8efb8` | no |
| ≥7 | `0x0730`+ | **yes** | — | 64-byte trace-ring record | no |

Nothing in the column that matters: **no erase, no program, no EEPROM section
write, no marker write, no re-init verb `0x25`, no namespace call, no L2P
touch.** PROVEN over the full call closure.

---

## 5. The default arm — what `0x0730`…`0xFF30` actually does

Not "a forwarded/ported command". `0x3002feac`:

```asm
3002feac: call8 0x3001e83c              ; = 0x7ffabe04 -- pop a 64-byte node off a free list
3002feaf: bnez a10,0x3002febf           ; empty -> yield and retry
3002fec5: memset(ctx+0x178, 0, 64)
3002fed6: s32i 201,ctx+0x17c            ; 0xC9
3002fede: s32i 198,ctx+0x180            ; 0xC6
3002feee: s8i  48, (ctx+240)+0xb0       ; 0x30
3002fef6: s8i  <sub>, (ctx+240)+0xb1
3002fefe: call8 0x3002d3c8              ; copy 64 bytes into the node
3002ff09: call8 0x3001e900              ; = 0x7ffabec8 -- push node onto the ring at [x+0x1fc]
```

`0x7ffabec8` is a plain ring insert (`s8i a2,a3,0x49` with `a2 = 2`; link into
`[a4+0x1fc]`). So an out-of-range sub value writes one **diagnostic trace
record** naming the opcode and returns. Harmless, but it is a write, and it
consumes a node from a fixed-size pool. **PROVEN.**

---

## 6. Corrections to `sn200-c6-dispatch.md`

### 6.1 ⚠ `0x30` does **not** require `CDW10 == 0` — operator-facing

§3's "length rule" reads the branch polarity backwards for the two exempt
commands. Full decode of `0x3003170a` (with `a12 = 34`, `a10 = 48` set in the
prologue at `0x30031481`/`0x30031489`):

```asm
3003170a: l32i a13,a2,0x130                     ; CDW10
3003170d: { l8ui a11,a14,0x38 ; bnez a13,0x3003171d }   ; CDW10 != 0 -> 1171d
30031715: beq  a11,a12,0x3003171b               ; cmd == 0x22
30031718: bne  a11,a10,0x30031723               ; cmd != 0x30 -> INVALID LENGTH
3003171b: beqz a13,0x3003173e                   ; CDW10 == 0 -> accept, no log
3003171d: bne  a11,a12,0x3003173e               ; cmd != 0x22 -> 1173e
30031720: bne  a11,a10,0x30031746               ; cmd != 0x30 -> log 1617, accept
30031723: -> StrId 1616 "VUC SCSI Ported Command Invalid Length = %x"
3003173e: beq  a11,a10,0x300314ed               ; cmd == 0x30 -> accept
30031746: -> StrId 1617, then accept
```

Trace `cmd = 0x30, CDW10 = 2`: `bnez` taken → `0x3003171d` → `bne a11,a12`
taken (`0x30 != 0x22`) → `0x3003173e` → `beq a11,a10` taken → **accepted**, and
it does not even log. The rule is one-directional: **`CDW10 == 0` is rejected
for every command byte except `0x22` and `0x30`; a non-zero `CDW10` is accepted
for all of them.**

Consequence: mistyping the runbook's crash-dump size probe
`CDW12 = 0x0320, CDW10 = 2` as `0x0330` is **not** caught by the length check.
It executes sub 3.

The conclusion "`0x30` cannot return host data" survives, but on different
evidence: the `0x30` handler and every one of its workers never program a host
DMA descriptor, and the shared completion path `0x3003149c` only copies 28 bytes
of status back into the command context. **PROVEN.**

### 6.2 Other corrections

| §5 claim | correction |
|---|---|
| runtime delta `0x4DF915C8` | typo; `0x4FF8D5C8` (§1) |
| "sub 6 … logs StrId 111 *OCP interrupt dispatch table is full*" | false positive on a data constant, and in sub **3**'s range (§3.3) |
| "the default arm … has the shape of a forwarded/ported command" | it is a 64-byte trace-ring record (§5) |
| "`0x30` is unidentified" | SMART / statistics collection (§3.0) |
| "state-mutating in at least two arms" | five of eight arms mutate DRAM; **none** mutates persistent state |

---

## 7. The prize questions, answered

| question | answer |
|---|---|
| Does any `0x30` sub write or influence the boot marker / startup type? | **No.** All three references to `0x7ff87c64` in the subtree are `l32i` + `beqi …,6` guards. PROC8 contains **zero** references to the marker RAM `0x7ff8c7ec` or to `0x80000003/8/9` (`litref -v`, all 18 images). **PROVEN.** |
| Does any sub clear CLOG (section 11) without scheduling re-init? | **No.** Clearing CLOG requires OAM verb 3 + section 11 reaching PROC0. No `0x30` sub constructs any OAM request; the flags byte `0x7ff8d200` is unnameable from PROC8. The missing half of a non-destructive latch release is **not** here. **PROVEN.** |
| Does any sub attach/expose the namespace or influence the L2P load? | **No.** No namespace or LBN call in the closure, and the closure's only effector is an internal mailbox carrying fixed statistic IDs. **PROVEN.** |
| Does any sub alter the post-crash gate or `0x7ff87c64`? | **No.** The gate is a `movi`/`beq` chain in the executing image; nothing in the subtree writes text, and the only writer of `0x7ff87c64`-adjacent state (`0x7ffa915c` → `0x7ff87c60`) is not called from any `0x30` path. **PROVEN.** |
| Is `0x30` an action family rather than a read family? | **Yes**, and the actions are DRAM statistics refreshes driven by an internal mailbox. It is an action family with nothing worth actioning. |

**This is the fifth dead lead, and it dies with evidence rather than with a
shrug.** The `0xC6` surface is now fully mapped: seven command bytes, two of
them gate-passing, both identified, neither an escape. The only unresolved door
in the whole vendor surface remains `0xEC` (`sn200-marker-write.md` §7).

---

## 8. New one-nibble adjacencies

Beyond `0x__20` ↔ `0x__30` (already recorded) and `0x0003`/`0x0004`,
`0x0403`/`0x0503`:

- **`0x0320` ↔ `0x0330`, and the length check will not save you.** `0x0320` is
  the crash-dump *size probe* — the single most-typed command in the recovery
  procedure, and it is typed with `CDW10 = 2`. §6.1 proves that
  `CDW12 = 0x0330, CDW10 = 2` is accepted without even a log line, and sub 3 is
  the largest arm that actually executes on a latched drive.
- **`0x0020` ↔ `0x0030`.** The drive-log body read is one nibble from the SMART
  update driver, which loops up to 131 worker dispatches and mutates DRAM SMART
  state. Also ungated.
- **`0x0230`/`0x0430`/`0x0530` are the *safe* mistypes** — they self-disable on
  a latched drive. `0x0030`/`0x0130`/`0x0330`/`0x0630` are the ones that run.
  The asymmetry is the opposite of intuitive: the higher-numbered subs are the
  harmless ones.
- **`0x0630` ↔ `0x0730`.** Sub 6 executes; sub 7 falls to the default arm and
  consumes a node from a fixed-size trace-ring pool. Neither is destructive, but
  they are different code.
