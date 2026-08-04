# SN200 — vulnerability audit of the Post-Crash reachable attack surface

Scope: the complete set of admin commands that survive the Post-Crash gate on a
latched Ultrastar SN200 running KNGND122, audited for memory-safety and logic
bugs that a host could use to break its own drives out of the latch.

Authorised work on the owner's own hardware. Everything here is **static
analysis**. No command in this document has been sent to a drive, and §7 is
explicit about which ones must never be.

Companions: `docs/sn200-firmware-re.md`, `docs/sn200-independent-re.md`,
`docs/sn200-shutdown-path.md`, `docs/sn200-nondestructive-recovery.md`,
`docs/sn200-crash-dump-retrieval.md`, `docs/sn200-logic-escapes.md`.

Claims are labelled **PROVEN** / **INFERRED** / **SPECULATIVE**.

---

## 0. Tooling note that cost real time

`disany.py` takes `PROC<n>[@base]`. For **PROC8 you must write the base
explicitly** — `'PROC8@7ff80000'` for the main image, `'PROC8@30000000'` for the
overlay bank. Bare `PROC8` glob-sorts to `PROC8_30000000.bin` and silently
disassembles the wrong image, printing `ERR index out of range` for every
address. Every other processor has one image and needs no `@`.

Literal-pool cross-referencing, used throughout this document:

```
l32r target = ((pc + 3) & ~3) + (imm16 - 0x10000) * 4
```

A log descriptor word is `(StrId << 16) | nargs` with byte 1 == 0. To find where
a string is used: find that word in the image's literal pool, then find the
`l32r` whose computed target is that literal. Both helper scripts are trivial
and are reproduced inline in §8.

---

## 1. The objective, stated precisely

The whole exercise reduces to **one 32-bit word**: `0x7ff87c64` in PROC8's
address space, the startup-mode global. Writing any value != 6 into it does two
things at once:

1. **Lifts the admin gate.** `Admin_CheckCmdAllowed` @ `0x7ffa6b18` re-reads the
   word on *every* command (`0x7ffa6b1b: l32r a8,[0x7ffa09b0]=0x7ff87c64` then
   `l32i.n a8,a8,0` then `bnei a8,6,0x7ffa6bd9`). It is not cached. **PROVEN.**
2. **Disarms the destructive half of the repair.** The `0xFF`/`0x0503` crash-dump
   erase re-reads the same word at command time and only schedules the
   namespace-destroying Drive REINIT when it equals 6:

```asm
30033704: l32r  a14,[0x30033350] = 0x7ff87c64
30033707: l32i.n a14,a14,0x0
30033709: { sync/extw ; bnei a14,6,0x300335bf }   ; != 6 -> plain success tail
30033711: { s32i a7,a12,0x128 ; movi a15,37 }     ; == 6 -> verb 37 = schedule REINIT
30033719: { s32i a15,a12,0x118 ; ... }
30033721: call8 0x30030aa0
```

`0x300335bf` is the ordinary success tail (`addmi a10,a5,256 ; addi a10,a10,-84 ;
j 0x30033591`), shared with every other erase arm. **PROVEN** — this re-derives
and confirms `sn200-nondestructive-recovery.md`'s "narrow exception", and shows
it is narrow only because the mode word is hard to change, not because the erase
behaves differently.

So the win condition is not "arbitrary code execution". It is:

> **transiently write any non-6 value to PROC8 `0x7ff87c64`, then issue
> `0xFF` CDW12 `0x0503`.** That erases the crash section *without* arming the
> reinit, and the latch lifts on the next clean start with the namespace intact.

Both reads are re-issued per command, so the write does not have to persist — it
only has to hold across one command.

> **Result, stated up front: that write does not exist on the host surface.**
> The rest of this document is the audit that establishes it. §3 shows the mode
> word has exactly two writers in the entire firmware, both driven by
> inter-processor messages rather than by any NVMe command; §4 shows no handler
> reachable through the gate yields a controlled write of any kind. §5.1 is the
> bottom line. Read §7 before touching a drive regardless.

### 1.1 Why the crash-section handles are the wrong target

Goal #2 in the brief (write the boot marker / crash-section state directly) is
**not reachable from the host-facing processor at all**. The Crash and PFail
section handles `0x7ff85364` / `0x7ff85374` appear as literals in **PROC0's image
only** — 50 `l32r` sites in PROC0, zero in PROC8 or any other core. The
`0x7ff8xxxx` range is per-core local memory, not a shared window: every
processor has its own image based at `0x7ff80000`. A write primitive on PROC8
therefore cannot reach PROC0's section state. **PROVEN**, both for the literal
distribution and for the per-core address-space claim — `0x7ff80000` is one
512 KiB slot of the SoC unit grid, self-aliased, so the address resolves to the
issuing core. No alias to another core's slot exists in any image. See
`sn200-memory-map.md` §1, §4.

This is why `0x7ff87c64` is the target and the section bits are not: the mode
word lives on the processor the host can actually talk to.

### 1.2 What a write primitive is worth — the resume-PC ABI

A crucial structural finding, and it lowers the bar considerably.

Every VUC/OAM handler in PROC8's overlay bank opens with the same coroutine
resume sequence:

```asm
3003353c: entry a1,0x30                                 ; the OAM-ERASE sub-handler
3003353f: mov.n a5,a2
30033541: l32i.n a15,a5,0x18
3003354e: { l32i a12,a5,0x174 ; beqz a15,0x30033559 }   ; 0 -> real entry
30033556: jx a15                                        ; else resume
```

`[request + 0x18]` is a **resume PC reached by indirect jump**. The pattern
repeats across the overlay bank (~100 `jx` sites, with `l32i a9,[a2+0x18]`
immediately after `entry` at `0x30023d80`, `0x30023ef8`, `0x30023fe0`,
`0x30024694`, `0x30024788`, `0x3002609c`, `0x300261b8`, `0x30027678`, … ).
**PROVEN.**

Consequence: **a write primitive does not need to be arbitrary to be decisive.**
Anything that can place one attacker-chosen word at `frame+0x18` of a live
request object yields immediate PC control on the Xtensa core — which is win #3,
and from there win #1 is a two-instruction store. It also means a use-after-free
of a request object is a full compromise rather than a crash.

**Correction to an earlier reading of my own.** I initially described this as
"offset `0x18` of the request object" in a way that invited conflation with the
host's CDW0. They are different objects `0x100` apart, and the distinction is
load-bearing:

| field | address | contents |
|---|---|---|
| resume PC | `frame + 0x18` | firmware-set continuation |
| CDW0 (opcode in low byte) | `ctx + 0x18` = **`frame + 0x118`** | host-set |

The NVMe command context is `coroutine_frame + 0x100` (`addmi aN,a2,256` at
`0x7ffa6e03`, `0x7ffa6e0e`, `0x7ffa71c0`, `0x30031c79`, `0x3003bf2d`,
`0x30033c07`). **PROVEN.** So host command dwords do *not* land on or near the
resume PC, and the "one controlled word" route requires a genuine
write-anywhere primitive, not merely influence over a command field. No path
was found writing host data at a host-controlled offset anywhere near
`frame+0x18`.

### 1.3 The command-context field map

`sn200-crash-dump-retrieval.md` §1.5 leaves the identity of the gate's `+0x38`
selector as INFERRED. It can be **narrowed**, though not fully closed — see the
caveat below the table:

| offset | meaning |
|---|---|
| `ctx+0x18` | CDW0; opcode = low byte (`0x7ffa7315 extui a10,a11,0,8`) |
| `ctx+0x38` | **CDW12[7:0]** — the "cmd" byte |
| `ctx+0x39` | **CDW12[15:8]** — the "subcmd" byte |
| `frame+0x130` | CDW10, transfer length in **dwords** (`0x30030a44 slli a15,a10,2`) |

Five independent handlers read `+0x38` then `+0x39` as a `(cmd, subcmd)` pair
(`0x30030d14`, `0x3003726e`, `0x30033c2b`, and the gate itself), which is
exactly libdmi's `cmd[0x30] = (subcmd << 8) | cmd_id`.

> ### ⚠ Which CDW that is remains UNRESOLVED — and the readings conflict
>
> Three independent attempts produced **three mutually inconsistent answers**,
> so the table row above is marked CDW12 only because that is what the external
> evidence says, not because the firmware was read that way.
>
> | source | claim | implies `+0x38` is |
> |---|---|---|
> | `libdmi_core.so.0.39` + nvme-cli WDC plugin | selector at SQE byte `0x30` | **CDW12** |
> | static read A — CDW0 at `frame+0x118` | SQE starts `frame+0x118` | CDW8 / PRP2 |
> | static read B — SQE at `ctx+0x110` (from `memset(ctx+0x100,0,160)` / `memcpy(...,64)` at `0x7ffa72b3`) | `ctx+0x138` = CDW10 | **CDW10** |
>
> The two static readings differ by 8 bytes and neither lands on CDW12.
> `sn200-crash-dump-retrieval.md` §1.5 already flagged that a raw-SQE mapping
> puts `+0x38` at CDW8, "which makes no sense as a selector" — that observation
> stands, and it is now clear it signals a mis-derived base rather than a strange
> firmware.
>
> **Do not treat this as settled, and do not let it change how commands are
> constructed.** The strongest anchor remains WD's own library and nvme-cli,
> which both place the selector in CDW12 and are what the working crash-dump
> retrieval procedure is built on. Resolving it properly means deriving the SQE
> base from the DMA that fills it, not from a `memset` length.
>
> **Safety consequence, and it is the reason this box is here.** If the
> selector really is CDW10 rather than CDW12, then a command believed inert
> because "CDW12 is zero" could carry a live OAM sub-command in CDW10. That
> cuts across the §7 table. Until this is resolved, treat **CDW10, CDW11 and
> CDW12 as all selector-bearing** on `0xFF`, `0xC6` and `0xCA`, and zero every
> one of them on any command not deliberately using them.

### 1.4 The mode word's neighbourhood rules out the easy overflow

Every data literal in PROC8's main image between `0x7ff87000` and `0x7ff88400`:

```
… 7ff87c00 7ff87c4c 7ff87c50 7ff87c58 7ff87c5c 7ff87c60 7ff87c64
   7ff87c68 7ff87c6c 7ff87c70 7ff87c78 7ff87c80 7ff87d88 …
```

`0x7ff87c64` sits in a dense run of **individually referenced scalar globals** —
a `.bss` scalar cluster, not the tail of a large array. **PROVEN.** So a classic
linear buffer overflow is very unlikely to walk into it. What reaches it is an
*indexed* write with a host-controlled index, or a fully controlled pointer
write. That shapes the whole audit: look for `base + host_index` stores, not for
`memcpy` past the end of a buffer.

---

## 2. Negative results that matter

These are findings, not gaps. Each one closes off a line of attack that would
otherwise be the obvious first thing to try.

### 2.1 The admin opcode dispatch is a compare chain, not a jump table — **PROVEN**

The single most likely place for an unchecked-index arbitrary jump is the
top-level opcode dispatch. It is not there. After the gate returns, the
dispatcher splits on `opcode < 0x80` vs `> 0x80` and then performs a
**binary-search chain of `beqi`/`bgeu`/`bltu` comparisons**, storing a per-opcode
handler literal and branching to a common tail:

```asm
7ffa7276: { movi a14,12 ; bltui a11,128,0x7ffa7c0d }   ; standard opcodes
7ffa7281: { sync/extw  ; bltu a10,a11,0x7ffa7523 }     ; vendor opcodes

7ffa7c0d: { movi a8,8 ; bgeu a8,a11,0x7ffa7cbc }
7ffa7c17: bgeui a11,10,0x7ffa7c25
7ffa7c25: bltui a11,16,0x7ffa7c70
7ffa7c2a: { movi a15,5 ; bltu a9,a11,0x7ffa7c3f }
…
7ffa7523: { l32r a13,… ; movi a8,216 }
7ffa752b: { l32r a10,… ; bgeu a8,a11,0x7ffa75cb }
7ffa7536: { sync/extw ; bgeu a9,a11,0x7ffa7c95 }
…
```

There is no `addx4`, no computed table base, and no `jx` anywhere in the dispatch
path. The opcode is never used as an index. **No off-by-one is possible here
because there is no index.**

### 2.2 The `0xFF` OAM sub-command selector is bounded by construction — **PROVEN**

```asm
300336c6: l8ui a11,a12,0x8d              ; sub-command byte
300336c9: beqz  a11,0x30033772           ; 0
300336cc: { … ; beqi a11,1,0x30033795 }  ; 1
300336d4: { … ; beqi a11,2,0x300337b8 }  ; 2
300336dc: beqi  a11,3,0x30033661         ; 3
300336df: { … ; beqi a11,4,0x300337db }  ; 4
300336e7: { … ; beqi a11,5,0x300337fe }  ; 5
300336ef: beqi  a11,6,0x3003374f         ; 6
300336f2: l32r  a10,LOG 1636 "OAM ERASE CMD: Received Bad Erase sub-cmd: %d."
```

A compare chain with an explicit default-reject that logs. No table, no index,
no off-by-one. Every one of the seven arms is accounted for and every arm's
destination is known (§7).

### 2.2.1 The full `0xFF` erase table, re-derived — and what verb 37 is

Each arm was decoded to its verb (`[req+0x118]`) and section id (`[req+0x11c]`).
CDW12 encodes `(sub << 8) | cmd_id`, with `cmd_id = 0x03` for the erase family.

| sub | CDW12 | site | verb | section | meaning |
|---|---|---|---|---|---|
| 0 | `0x0003` | `0x30033772` | 3 | 6 | System Area 0 |
| 1 | `0x0103` | `0x30033795` | 3 | 3 | Bad Block list |
| 2 | `0x0203` | `0x300337b8` | 3 | 9 **then 8** | BIST Script **+ Status**, chained |
| 3 | `0x0303` | `0x30033661` | 1 | 13 | ☠ **SBL EEPROM — permanent brick** |
| 4 | `0x0403` | `0x300337db` | **37** | — | ☠ **Drive Uninit**, `[req+0x128] = 1` |
| 5 | `0x0503` | `0x300337fe` | 3 | 11 (`0x0b`) | Crash Dump |
| 6 | `0x0603` | `0x3003374f` | 3 | 10 (`0x0a`) | PFail Crash Dump |

**PROVEN**, and it confirms the table in `sn200_vuc.py` exactly.

Sub 2 is **one chained arm covering two sections**, not two sub-commands:
`0x30033643` → `0x3003372c` sets verb 3 / section 8 after the section-9 pass.
That is why the firmware carries nine erase-failure strings for seven
sub-commands — a discrepancy that has confused earlier passes.

Two things fall out of this that prior work could not settle:

- **Verb 37 (`0x25`) is the boot-marker writer.** `sn200-nondestructive-recovery.md`
  lists "what verb 0x25 actually does" as COULD NOT DETERMINE. It is now
  determined by construction: sub 4 (**Drive Uninit**) issues verb 37 with
  `[req+0x128] = 1`, and the mode-6 arm of the crash-dump erase issues verb 37
  with `[req+0x128] = 0` (`0x30033711: s32i a7,a12,0x128`, `a7 = 0`). Same
  mechanism, payload selects which marker. **INFERRED, high confidence** —
  "writes the reinit boot marker" is now supported by a second, independent
  witness rather than being a guess.
- **Correction to `sn200-crash-dump-retrieval.md` §1.3.** That section proves the
  `0xC6` read path is side-effect free partly by observing that neither
  `0x30030aa0` nor `0x30031d10` — described there as "the EEPROM erase" —
  appears among its call targets. `0x30031d10` is almost certainly **not** an
  erase primitive. It has exactly three callers in the whole overlay bank, all
  in the OAM region, all with a `(dst, value, length)` shape on a pointer derived
  from the local request block: `(req+16, 0, 32)` at `0x30033899` and
  `0x300336b3`, and `(req+120, 4095, 64)` at `0x30033666`. That is a buffer-fill
  helper operating on RAM, not an EEPROM operation. **INFERRED, high
  confidence.** The *conclusion* of §1.3 still stands — it rests on two further
  independent legs (nvme-cli must issue a separate `0xFF/0x0503` to clear a
  dump, and libdmi's `post_fn` is pure host-side bookkeeping) — but the
  `0x30031d10` leg of the argument should be struck rather than relied on.

- **Sub 3 reaches its verb/section pair by a longer route.** It first calls
  `0x30031d10` with `(dst, 4095, 64)`, then continues via `0x300335f7` which sets
  **verb 1, section 13**, whose failure arm logs *"Erase to SBL EEPROM failed"*.
  It is one increment from `0x0403` Drive Uninit and two from `0x0503`.
  **Do not sweep this space.**

Every arm's identity is confirmed twice over: once by the verb/section immediates
fed to the submit routine, and once by relocating each arm's continuation literal
through the overlay rule of §6.1 onto the block that logs a named failure string.
Both methods agree on all seven.

### 2.3 The `0xCA` gate sub-list, independently re-verified — **PROVEN**

```asm
7ffa6d76: beqi a4,8      -> allow      7ffa6d87: beqi a4,2   -> allow
7ffa6d79: beq  a4,a10(=0x11)-> allow   7ffa6d8a: beqi a4,4   -> allow
7ffa6d7c: beqi a4,3      -> allow      7ffa6d8d: beq  a4,a11(=0x0D) -> allow
7ffa6d81: beq  a4,a8(=0x0F) -> allow   7ffa6d92: beq  a4,a9(=0x0E)  -> allow
7ffa6d84: beqi a4,16     -> allow      7ffa6d97: beq  a4,a14(=0x13) -> allow
                                       7ffa6d9c: beq  a4,a8(=0x21)  -> allow
                                       7ffa6da1: bne  a4,a9(=0x32) -> REJECT
```

`{0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32}` —
confirms the prior reading exactly. Again a compare chain, not a table.

### 2.4 The NVMe-MI admin path is not a gate bypass — **PROVEN**

`0x7ffafb70`–`0x7ffafd2f` is an NVMe-MI admin-command **field validator** that
returns small result codes 0–7 and never dispatches anything. It accepts a
strict subset of the already-allowed opcodes — `0x02` Get Log Page, `0x06`
Identify, `0x09` Set Feature, `0x0A` Get Feature, `0x10` Firmware Activate,
`0x11` Firmware Download — and rejects the rest with StrId 2037
*"Admin: NVMeMI ADMIN Cmd, unsupported cmd"*. It also applies **tighter** bounds
than the mainline path (e.g. `bltu a8,a11` against 64 for two LIDs, against 512
for others, and `dataRange > ((numDW + 1) << 2)` rejection at StrId 2031). There
is no second admin entry point that skips `Admin_CheckCmdAllowed`.

### 2.5 A plain crash is not useful — **INFERRED, high confidence**

Any unclean stop re-arms the latch: `UNEXSTRT` stamps a fresh stub into the
CRASH section on every start not preceded by a recorded clean shutdown
(`sn200-nondestructive-recovery.md` §5, PROVEN there). A firmware assert or
watchdog reset is exactly such a start. So **crashing the drive strictly worsens
the position**: it adds a crash record and buys nothing, because the mode word is
recomputed from the section state on the next boot. Do not pursue "find a crash"
as a goal, and treat an accidental crash as a real cost, not a neutral outcome.

The one exception would be a crash that runs *after* a successful non-destructive
`0x0503`, which is irrelevant because at that point the drive should be stopped
cleanly anyway.

### 2.6 Firmware commit / load-n-go remain closed — **INFERRED, high confidence**

Re-confirmed from prior work rather than re-derived: Commit Action 0b011 is
unsupported (`extui a8,a10,3,2` then `blti a8,3` at `0x30025e48`), the activate
path demands a reset, and the one override that exists — StrId 3044
*"SYS: Load-n-go boot override of failed shutdown."* — is reached only from
`0x7ffaaf6b`, the convergence of startup states 5/6/7. State 9 (Post Crash) has a
different dispatch edge. Load-n-go overrides *"the shutdown didn't finish"*, not
*"there is a crash record"*.

---

## 3. The mode word's only writers

Established by scanning every image for the literal value `0x7ff87c64`, then
resolving every `l32r` to it, then decoding each site.

**Only two instructions in the entire firmware write `0x7ff87c64`**, and both are
in `Admin_IBQCommandReceiver` @ PROC8 `0x7ffb00d8` — the **inter-processor
message** receiver, not a host command path:

```asm
; MSGID 0x10 -- "System Inited Done"
7ffb0138: l32i.n a13,a2,0x10
7ffb013a: beqi  a13,5,0x7ffb014a
7ffb013f: l32r  a10,LOG 2049 "Admin_IBQCommandReceiver System Inited Done (Src Mgr = %d)."
7ffb014a: l32r  a14,[0x7ffa09b0] = 0x7ff87c64
7ffb0157: { s32i a13,a14,0x0 ; … }        ; <-- write #1, value from msg[+0x10]

; MSGID in the "Startup Req" range
7ffb0196: l32r  a10,LOG 2051 "Admin_IBQCommandReceiver Startup Req MSGID 0x%x"
7ffb019c: l32r  a15,[0x7ffa09b0] = 0x7ff87c64
7ffb019f: { l32i a14,a2,0x10 ; … }
7ffb01a7: { s32i a14,a15,0x0 ; … }        ; <-- write #2, value from msg[+0x10]
```

**PROVEN.** Both take the new mode straight out of an IBQ message payload word.
The MSGID itself is `[a2+0xc]`, dispatched by another compare/range chain
(`0x7ffb00da`–`0x7ffb01d5`): write #1 is reached by **MSGID `0x10`**
("System Inited Done"), write #2 by **MSGID 260/261** (`0x104`/`0x105`,
"Startup Req").

**And the receiver is unreachable from any command path.** The enclosing
function `0x7ffb0088` has exactly one caller, inside `0x7ffb0608` — which has
**zero callers**. It is a task entry driven by the inter-processor message
queue, not something any NVMe command dispatch can reach. **PROVEN.** This is
the independent, from-the-other-end closure of the objective: even a perfect
write-anywhere primitive would have been needed, because nothing narrower —
no influence over a command field, no confused index — touches this word.

Two consequences:

- **There is no host command that sets the startup mode.** Not by design, not by
  accident. The only legitimate writers are internal messages from the system
  manager on another core.
- **The most valuable single primitive would be an IBQ message injection**, not a
  memory corruption: one forged `MSGID 0x10` with payload word `[+0x10] != 6`
  does the entire job through fully intended code. Whether any host-reachable
  handler can post an IBQ message with a host-controlled MSGID and payload is the
  question with the highest expected value in this whole document, and it is
  **unresolved** — see §6.

---

## 4. Per-handler audit results

### 4.1 Spec-defined handlers `0x02`, `0x06`, `0x09` — **no exploitable bug**

Handler addresses, resolved from the dispatch literal pool at `0x7ffa0f38`–
`0x7ffa0f74`:

| opcode | handler |
|---|---|
| `0x02` Get Log Page | PROC8 `0x7ffa4d08` |
| `0x06` Identify | PROC8 `0x7ffab518` |
| `0x09` Set Features | PROC8 `0x7ffaa628` |
| `0x0A` Get Features | descriptor `0x7ffbc92c` — see below. Unaudited; §6. |

On that last row: `0x7ffbc92c` is **not** a missing extraction. `segparse.py` on
`PROC8.bin` shows eleven segments, the last loading `0x7ffa0710-0x7ffbb064`.
There is no segment above `0x7ffbb064`, and none covering `0x7ff81f38-0x7ffa0000`
either. **PROVEN: those ranges are BSS/heap, runtime-initialised.** The many
dispatch literals pointing into `0x7ffbc000-0x7ffbe000` (`0x7ffbc110`,
`0x7ffbc198`, `0x7ffbc24c`, `0x7ffbc440`, `0x7ffbc504`, `0x7ffbd948`, …) are
therefore pointers into runtime-built structures, not into code we are missing.
Nothing needs re-extracting; the handler bodies for those entries simply do not
exist as static data and are reached by message dispatch.

**The most-suspected primitive does not exist.** The hypothesis that Set Features
stores into a FID-indexed table is **false, PROVEN**. `0x7ffaa72d`–`0x7ffaa93d`
is a balanced compare tree; every accepted leaf selects a **constant `l32r`
literal** descriptor pointer, and the eventual store address is independent of
the FID:

```asm
7ffaa720: l8ui a12,a13,0x80                      ; FID = CDW10[7:0]
7ffaa72d: { l32r a14,0x7ffa0af0 ; blti a12,10,0x7ffaa78c }
7ffaa737: { sync/extw ; blt a8,a12,0x7ffaa7d8 }  ; a8 = 10
7ffaa741: l32r a13,0x7ffa12a0                    ; CONSTANT descriptor
7ffaa7c2: { s32i a13,a14,0x10 ; mov a10,a12 }    ; address independent of FID
```

Accepted FIDs, exhaustively: `0x01`–`0x0B`, `0x7E`, `0x7F`, `0x80`–`0x83`,
`0xF0`. Everything else falls to `0x7ffaa6b4` → `0xC0040000 >> 17 = 0x6002`
(Invalid Field). Comparisons use the signed forms but the FID comes from `l8ui`,
so its range is 0..255 and there is no sign-extension hole.

**Get Log Page length arithmetic is safe, PROVEN.** The command context stores
CDW10 as two 16-bit halves (`ctx+0x130`, `ctx+0x132`), proven independently by
the Abort handler reading SQID/CID from the same pair at `0x7ffa6742`/`0x7ffa674a`.
Get Log Page reads only `l16ui a14,a2,0x132` — **NUMDL alone, zero-extended**.
No read of NUMDU exists anywhere on the path. So `(NUMD+1)*4 <= 256 KiB` and the
classic 32-bit length overflow is unreachable. The truncation is a spec deviation
that makes transfers *smaller*, never larger.

**`LPOL`/`LPOU` are not read at all** on this path — log page offset is simply
unimplemented, so there is no offset arithmetic to overflow.

LID dispatch (`0x7ffa5087`–`0x7ffa50e8`) is a compare chain over
`{0x01, 0x02, 0x03, 0x04, 0x05, 0x80, 0xC1, 0xC2, 0xC3, 0xCA}` with a default
arm logging StrId 3328 and returning `0x82120000 >> 17 = 0x4109` (Invalid Log
Page). Only four vendor LIDs exist and each merely enqueues a message with a
constant descriptor. NSID is range-checked `1..128` (plus `0xFFFFFFFF` where
legal) on every path that consumes it, followed by an attached-namespace bitmap
test.

**Identify** accepts CNS `{0x00, 0x01, 0x02, 0x10, 0x11, 0x12, 0x13}` by compare
chain. Both output loops are counter-bounded rather than host-length-bounded —
the namespace-list loop hard-caps at 16 entries (`bgeui a12,16`) and at NSID 128,
and the running buffer offset is capped at 4095 (`l32r a14,=0xfff ; bltu a14,a9`)
before the 4096-byte transfer. CNTID is used only as a `bltu` threshold, never as
an index.

**Verdict: four of the five hunted bug classes are affirmatively absent here.**
No table index, no off-by-one, no NSID index, no length overflow, no
FID-indexed store.

The fifth (mode-6 uninitialised object) could not be resolved statically and is
**not** being claimed. Identify takes its 4096-byte buffer from `0x7ffac068` and
stores it at `[req+0x7c]` with no NULL test (`0x7ffab885`), and Get Log Page
loads DDR staging pointers from BSS globals `[0x7ff82904]`, `[0x7ff828f4]`,
`[0x7ff828e4]` with no NULL test. All read as zero in the image because they are
BSS, so static analysis cannot distinguish "initialised before any admin command
in mode 6" from "left NULL". Even if NULL, the result is a store to low memory —
a crash, not a controlled write, and per §2.5 a crash is a net loss.

---

### 4.2 `0xC6` and `0xCA` — **no exploitable bug**

**`0xC6` is "VUC SCSI Ported Command"** (StrIds 1617/1618), a SCSI tunnel. The
top-level dispatch does no selector check at all (`0x7ffa7bf5`); both `0x20` and
`0x30` reach the same overlay coroutine at `0x3003147c`, whose selector dispatch
at `0x30031542` accepts `{0x20, 0x21, 0x22, 0x23, 0x30, 0xB7, 0xCD}`. Only
`0x20` and `0x30` survive the gate.

**The undocumented `0x30` family is a dead end — INFERRED, well supported.** All
seven arms are structurally identical (`call8 0x3002c1a0` with a per-selector
descriptor, then `movi a2,10 ; retw` = coroutine yield). Four independent
observations agree that `0x30` is a **zero-length control/handshake** command
the firmware also issues to itself, not a host data-transfer feature:

1. `0x7ffa7306`–`0x7ffa7313`: the activity-classification store is skipped for
   `0xC6` and for selector `0x30`.
2. `0x7ffa6e0e`–`0x7ffa6e27`: the completion path has a `C6 && 0x30`-only branch
   clearing two per-queue slots instead of the generic teardown.
3. `0x3003170d`–`0x3003173e`: `selector == 0x30` with `CDW10 == 0` is the only
   combination that passes the length precheck silently.
4. `0x3002fed6`–`0x3002fefe`: the firmware **synthesises** a `C6`/`0x30` itself
   (`ctx[0x17c]=0xC9 ; ctx[0x180]=0xC6 ; byte[a7+0xb0]=0x30`).

No memcpy, no host length, no host offset on that arm.

**Newly documented (PROVEN):** the `0xC6`/`0x20` "Get Drive Log" sub-dispatch at
`0x30030d14` accepts sub-commands **0–8**, not 0–6 as previously recorded. Sub 7
→ `0x30030b18`, sub 8 → `0x30030ae7`. **Corrected:** they do *not* route into
the `.CDH`-magic reader and touch no crash section. Each allocates a job
(`0x30022504`, retry-looping on failure), fills it with `type = 7` and
`len = 1122` 64-byte units (= 71808 = `0x11880`, the size both arms return),
spawns a worker coroutine — `0x7ffa972c` for sub 7, `0x7ffa43c0` for sub 8 —
and DMAs the 71808-byte result. Neither calls an erase primitive, but they are
the only arms that *spawn a producer* rather than pointing at an existing
section, and `0x7ffa43c0` mutates a DRAM counter table at
`0x7ff879f8 + (idx<<4) + 0x1f0`. Still **unidentified**; do not send. Full
per-arm breakdown in `sn200-crash-dump-retrieval.md` §1.2.4.

**`0xCA`'s handler dispatch IS table-driven — and the bound is correct. PROVEN.**
This was the single most promising structural lead in the brief, and it is
clean:

```asm
7ffa75e3: l8ui  a12,a12,0x38                        ; sub-value = CDW12[7:0]
7ffa75ee: { movi a11,28 ; movi a13,27 ; movi a8,67 } ; slot C: imm8=0x43 = 67
7ffa75f6: { movi a10,26 ; bgeu a12,a8,0x7ffa78e3 }   ; sub >= 67 -> default arm
7ffa75fe: l32r  a9,=0x7ffa760e                       ; table base
7ffa7601: addx2 a8,a12,a12                           ; 3 * sub
7ffa7604: add.n a8,a8,a9
7ffa7606: { movi a9,31 ; mov a0,a8 }                 ; -> 3-byte `j` slot
```

Three independent confirmations the bound is exactly right: the slot-C immediate
decodes as 67 (`be a0 1c d2 1b 80 43 c8` → `imm8 = 0x43`); the table
`0x7ffa760e`..`0x7ffa76d6` is exactly **67** three-byte slots; and
`base + 3*67 = 0x7ffa76d7` is simultaneously the first byte past the table and
the target of entry 0, so the compiler's own arithmetic agrees. `bgeu` on an
`l8ui`-zero-extended byte leaves no signed-index path. **No off-by-one, no
missing bound, no unchecked index.**

The gate's twelve allowed sub-values are a **strict subset** of the implemented
table — the "gate allows what the handler does not cover" case specifically
hunted for **does not occur**. Sub-values 20–28 (CellCare VUCs, confirmed at
`0x3003bf20`) are reachable through the default arm but are **not** on the
Post-Crash allow-list.

### 4.3 One genuine surface expansion: raw NAND page **read** is reachable

**PROVEN from the firmware's own strings**, which name the encoding explicitly:
StrId 1871 `ERROR Flash_ReadRawData(0xCA/0x03/0x01) returned status 0x%x` and
StrId 1872 `ERROR Flash_ReadCacheData(0xCA/0x03/0x02)`.

```asm
3003726e: l8ui  a9,a11,0x39
30037271: beqz  a9,0x300372b9      ; subcmd 0
30037274: beqi  a9,1,0x300372d2    ; subcmd 1  Flash_ReadRawData
3003727c: beqi  a9,2,0x30037293    ; subcmd 2  Flash_ReadCacheData
3003727f: l32r  a10,LOG 1873 "ADMIN: Invalid field in cmd"   -> reject
```

`0x03` **is** on the Post-Crash allow-list, so **`0xCA` / CDW12 = `0x0003`,
`0x0103`, `0x0203` is a raw NAND page read on a latched drive.** Its length
handling is sound: `0x30037026` rejects zero length and `0x30037031`–`0x30037039`
(`movi a11,640 ; minu a10,a10,a11`) clamps to 640 bytes **before** use. This is
a data-disclosure surface, not a write, and not a way out of the latch.

### 4.4 The one unresolved item that must not be probed

The raw **write/erase** family — StrIds 1875–1880 (`Flash_WritePageRaw`,
`Flash_ProgNANDPageRaw`, `Erase SLC block`, `Erase MLC block`) and 3465
(`VUC Erase BlkType`) — lives in a different coroutine at ~`0x3003d5c0`. Its
entry state at `0x3003db19` reads `ctx+0x39` and accepts **`{0, 1, 2}` — the
same sub-sub space as the raw-read family**:

```asm
3003db1c: l8ui a12,a6,0xf9      ; = frame+0x139 = ctx+0x39
3003db1f: beqz.n a12,0x3003db57
3003db21: beqi a12,1,0x3003db38
3003db24: beqi a12,2,0x3003db38
3003db27: l32r a10,=0xc0040000  ; else SC 0x02, DNR
```

**Which opcode and selector carry it could not be established** — it is reached
through overlay-descriptor indirection, which at the time of the attempt looked
like an unmapped region. Because its sub-sub space collides with the raw-read
family's, **the possibility that some `0xCA` sub-sub value reaches a page program
or block erase is UNRESOLVED, not excluded.** A raw block erase on a latched
drive is unrecoverable data loss. Close this by static analysis or not at all.

**This is now tractable and should be the first thing anyone picks up.** §6.1
solved the indirection: overlay code is linked at `0x300xxxxx` but executes from
`0x7ffbc000`, and `static = ddr_src + (runtime - 0x7ffbc000)` resolves the
descriptors that blocked this. The identical technique settled `0xEC`, `0xFF`
and `0xEF` (§4.8). Nothing about this question requires touching a drive.

### 4.5 `0x7ff861cc` is not a write primitive — **PROVEN**

The dispatcher's conditional store that looked promising:

```asm
7ffa7300: movi a15,198
7ffa7303: beq  a11,a15,0x7ffa7315    ; CDW0 == 0xC6 -> skip
7ffa7306: l8ui a8,a13,0x38
7ffa730b: beq  a8,a9(=0x30),0x7ffa7315
7ffa730e: l32r a15,=0x7ff861cc
7ffa7311: l32i.n a10,a1,0x28
7ffa7313: s32i.n a10,a15,0x0
```

Sweeping every store to `[a1+0x28]` in the enclosing coroutine (`entry` at
`0x7ffa6db4`, range to `0x7ffa7e00`) finds exactly **one**, in the prologue that
re-runs on every resume: `7ffa6dc5: s32i.n a12,a1,0x28` with `a12 = 1`. So this
is **a constant `1` written to a fixed address**. Neither value nor address is
host-controlled. `0x7ff861cc` is a small PCIe-diagnostics control block
(`+0x0` one-shot flag, `+0x4` written by the `0xD7` path, `+0x8` the injected
error word behind StrId 2991). The `C6`/`0x30` exclusion is an activity
*classification* decision, not a security check.

### 4.6 Queues, Abort, AER, Firmware Commit — **all clean**

The textbook target — a QID bound that is `<=` where it should be `<`, or applied
after the descriptor store — **is not present**.

**Create I/O CQ** (`0x30022f5a`) and **Create I/O SQ** (`0x30022920`) both reject
QID 0, then bound against `NCQA+1` / `NSQA+1` read from `[0x7ff8fbb0+0x274]`:

```asm
30022fc0: { l32r a11,=0x00020000 ; beqz a7,0x3002301b }  ; QID == 0 -> reject
30022fce: extui a15,a15,16,16                            ; NCQA
30022fd1: addi.n a15,a15,1
30022fd3: bltu a15,a7,0x3002301b                         ; QID > NCQA+1 -> reject
```

**The `+1` is not an off-by-one. PROVEN.** Set Features FID `0x07` at
`0x30023faf` ignores the host's NSQR/NCQR and returns `[0x7ff8fbb0+0x274]`
verbatim as CQE DW0; that field is 0-based in NVMe, so allocated queues = N+1 and
legal IDs are 1..N+1 — exactly what is admitted. Descriptor addresses are
`(0x66400 | 0x63400) + QID*4` **OR**-ed with the port base rather than added, so
no wrap. Every store happens in a later coroutine stage reached only after
validation. QSIZE is bounded 1..1023 (`addi a11,a11,-1 ; bltu a11,1023`, so
QSIZE 0 underflows to `0xFFFFFFFF` and is correctly rejected), the associated
CQID must already exist, and PRP1's page offset is checked.

**Delete I/O SQ/CQ** additionally require the queue state word to read 1
(`beqi a14,1` / `bnei a8,1`), so deleting a never-created or already-deleted
queue is a clean status return — **no stale pointer, no use-after-free**,
including in mode 6.

**Abort** uses the SQID only as a *read* index and is in fact one stricter than
Delete SQ (it omits the `+1`). **AER** is not a fixed array at all — it pops from
a doubly-linked free pool and returns `0x820A0000` (SC 0x05, AER Limit Exceeded)
on exhaustion, so over-subscription cannot overflow anything.

**Firmware Commit slot index is correctly bounded**: `extui a9,a10,0,3` then
`bge a8,a9` against the slot count from `[0x7ff85db0]`. The inclusive compare is
right for NVMe's 1-based slot numbering, and both operands are non-negative so
the signed `bge` is not a hazard.

**Mode-6 NULL deref not found here:** every queue command gates first on
`[ctx+0x16c]` bit 0 (controller-enabled) and returns a status word rather than
dereferencing when clear. An I/O subsystem that never started produces a clean
CQE, not a deref.

### 4.7 The one real bug found: a 32-bit wrap in Firmware Image Download

**PROVEN that the arithmetic wraps.** `PROC8@30000000`, entry `0x30025590`,
check at `0x300257e5`:

```asm
300257e5: l32i  a10,a2,0x134         ; OFST (CDW11, dwords)
300257e8: l32r  a11,=0x00400000      ; 4 MiB staging limit
300257eb: slli  a14,a10,2            ; OFST*4      <-- 32-bit, wrappable
300257ee: bltu  a11,a14,0x30025820   ; -> StrId 2177 "…exceeds DDR allocation"
300257f1: l32i  a15,a2,0x130         ; NUMD (CDW10)
300257f4: { add a15,a15,a10 ; movi a8,4 }   ; NUMD+OFST  <-- wrappable
300257fc: addx4 a15,a15,a8           ; (NUMD+OFST)*4 + 4
300257ff: bltu  a11,a15,0x30025820
```

Both `OFST << 2` and `NUMD + OFST` are computed in 32 bits with no overflow
guard, so both comparisons can be wrapped past. The check *is* applied to the
sum (answering the question posed), but the sum itself is unguarded.

**It is nevertheless not exploitable, on two independent grounds — INFERRED,
high confidence:**

1. **The use site mirrors the same wrap.** The DMA destination at `0x30025632`
   is `addx4 a14,a14,a8` = `(OFST<<2) + staging_base_lo`, with explicit carry
   propagation into a high word at `0x3002563a`–`0x30025642`. That is the
   identical mod-2³² `OFST*4` the check used, so an OFST wrap merely aliases back
   inside the window rather than escaping it. Wrapping the second check requires
   `NUMD ≳ 0x3FF00000`, i.e. a nominal transfer of ~4 GiB.
2. **The staging buffer is not the overlay code bank**, so even a successful
   overwrite would not be code execution. The destination is a **64-bit** address
   (low word from BSS pointer `[0x7ff827b4]`, separate high word, explicit
   carry) — not the core-mapped `0x30000000` bank. The staging window is 4 MiB
   while the overlay bank spans `0x30022238-0x30040078`; the sizes are
   incompatible with one allocation. The string set agrees: StrId 2183
   "Host to DDR transfer failed" (DMA stage) is distinct from StrIds 2190–2192
   "System Check/Write/Select Image failed" (a later System-Manager stage).

Not PROVEN, because `[0x7ff827b4]` is a runtime pointer that cannot be resolved
from the image.

**Encoding, recorded for completeness only — do not issue:** opcode `0x11`,
NSID `0`, CDW10 = NUMD, CDW11 = OFST, data length `(NUMD+1)*4`.
**Risk if triggered: HIGH and asymmetric.** A download landing anywhere
unintended corrupts a staging area that a subsequent Commit may write to flash.
On a drive already latched, that is precisely the class of action that converts a
recoverable drive into an unrecoverable one, in exchange for a primitive that
two independent arguments say does not escape its window. **Do not attempt.**

### 4.8 `0xE6`, `0xFF`, `0xEC` — **no bug on any of the three**

**`0xE6` — audited in full, no bug.** Handler `Admin_VucGetDiagnosticData`
@ PROC8 `0x7ffb375c` (extent `0x7ffb375c-0x7ffb3cdf`), worker
`Admin_BuildE6Entry` `0x7ffb2ef0-0x7ffb375b`.

- **It has no sub-command structure at all.** The demux at
  `0x7ffa7587-0x7ffa75a4` is an opcode range walk that installs a handler
  pointer and parses no CDW. Neither function reads the SQE copy region
  (`[req+0x174]+0x10..0x4F`); every access is at offset ≥ `0x10C`. **CDW10–CDW15
  never reach it.** The only host quantities are the data pointer and transfer
  length at `[req+0x200]`/`[req+0x204]`.
- The one apparent arbitrary-offset write is bounded: an index from
  `[req+0x19c]` scaled ×16 at `0x7ffb3aa5-0x7ffb3ad6`. That is a firmware
  iterator over the 36-byte-record directory at `0x7ff80570`, which has exactly
  40 records (0–39), matching the `movi a9,39` bound. Of 22 bank-wide writers of
  `+0x19c`, **none is host-fed**. Maximum touched offset is `a5+0x2FF` inside a
  1024-byte buffer.
- The firmware **already implements the check an attacker would need missing**:
  `0x7ffb3379-0x7ffb3389` computes capacity from the host transfer length and
  **skips** the section rather than truncating into it. Every `memcpy`/`memset`/
  `memcmp` length is a compile-time constant and no multiply can wrap.

**`0xFF` — no controlled write.** Every store in `0x3003353c-0x30033821` targets
a compile-time-constant offset of `a5` or `[a5+0x174]`. The call list is 8×
logger, 8× submit, 2× constant-length fill, plus two others. **Nothing writes
`request+0x18`**, so there is no PC control via the coroutine resume pointer
(§1.2).

**`0xEC` — resolved and audited: it is `Admin_VUC_Enable`, and it is clean.**
Handler `0x3002b6c4` (runtime `0x7ffbc24c`), overlay 11, identified from its own
log descriptors StrId 1920 *"ADM: Admin_VUC_Enable SUCCESSFUL. New State: %u"*
and StrId 1919 *"…FAILED. Invalid input command parameters detected"*. It is the
switch that turns the vendor-command surface on and off. Its complete parameter
check:

```asm
3002b756: bnez CDW10[7:0]           -> FAIL      ; reserved, must be 0
3002b75e..3002b77b: MPTR lo/hi, PRP1 lo/hi, PRP2 lo/hi all must be 0
3002b780: l16ui ctx+0x13a ; bnez    -> FAIL      ; upper half reserved
3002b785: CDW11 != 0x564F4944       -> FAIL      ; 'VOID'
3002b78e: CDW12 != 0x57415252       -> FAIL      ; 'WARR'
3002b797: CDW13 != 0x414E5459       -> FAIL      ; 'ANTY'
3002b7a0: l8ui a12,a10,0x39                      ; requested state
3002b7a3: beqi a12,1 -> OK ; beqz a12 -> OK ; else FAIL
```

Two sub-commands only — 0 disable, 1 enable — writing the byte at `0x7ff8f1dd`.
**Memory-safety audit: negative.** No memcpy on the reachable path, the one
memset has an immediate length of 24, no length or count field is read from the
SQE at all, the single allocation is NULL-checked (`bnez a10,0x3002b729`), and no
host value is used as or added to a pointer.

Two non-memory findings, both **PROVEN** and neither useful for the objective:

- **The vendor-command unlock is a hardcoded constant**, `VOIDWARRANTY` spread
  big-endian across CDW11–CDW13, with no nonce, challenge or signature. Any host
  that can send an admin command can flip the global VUC-enable flag. **This does
  not widen the Post-Crash surface**: gate 1 is an allow-list evaluated first, so
  enabling VUC only affects gate 2's `0xD8-0xDF` band, which gate 1 rejects in
  mode 6 regardless.
- **A malformed `0xEC` is a disable primitive** — the failure path at
  `0x3002b7c4` clears the flag *before* returning Invalid Field, so a garbage
  `0xEC` forcibly disables VUC rather than merely failing. Availability nit.

**Correction to the `0xFF` entry point.** `0x3003353c` is **not** the `0xFF`
handler entry — it is the OAM-ERASE sub-handler. The real entry is
`Admin_OamCmd` at `0x30033448` (runtime `0x7ffbc110`), whose top-level dispatch
at `0x300334b5`–`0x300334fd` admits exactly three selector values: **3** (OAM
ERASE → `0x3003353c`), **4** (inline status read), **7** (OAM READ RAW SA →
`0x30033824`), with a default logging StrId 1626 *"OAM CMD: Received Unsupported
Command 0x%08x"*. Compare chain, explicit default, no table index.

**A genuinely useful non-destructive read primitive** sits at selector 4:

```asm
30033500: l32r a9,-> 0x7ff87c64 ; l32i.n a10,a9,0x0
30033505: bnei a10,128,0x30033519
30033519: l32i.n a8,a9,0xc ; slli a11,a10,8 ; or a8,a8,a11
30033521: s32i a8,a2,0x154                    ; -> completion DW0
```

`0xFF` with selector `0x04` returns `(startup_mode << 8) | [0x7ff87c70]` in
CQE DW0. It only reads, transfers no data, touches no media, and is inside the
allow-list. This is WD's `gf_nvme_sys_init_done` and it is the precise,
non-inferential answer to "is this drive latched, and in what state" — worth
knowing exactly because the destructive members of the same family are one
selector byte away.

**`Admin_VUC_Mi_Test_OVL022` is carried on `0xEF`, not `0xE6`/`0xEC` — and
`0xEF` is not in the allow-list.** Relocating, `0x30033338 + 0x5f4 = 0x3003392c`
→ runtime `0x7ffbc5f4`, which is exactly the pointer the demux installs for
opcode `0xEF` at `0x7ffa757a`. **PROVEN.** On a latched drive `0xEF` is rejected
with `0x7C5` before reaching any of it. Its own selectors are validated to
`{0,1}` and `{0,1,2}` with a logging default, and its host↔DDR transfers use
constant lengths (16, 259, 231) into fixed buffers. **Open question 6.2 #5 is
closed: the MI command-injection surface is not reachable while latched.**

**Residual, SPECULATIVE, denial-of-service only.** `0x7ffb3044` loads the global
at `0x7ff827c4` (zero in flash) as a DMA source base; in mode 6, SAM/BlockMgr
never initialise, so it may still be NULL → LoadProhibited → controller reset.
Same shape at `0x7ffb3306` and `0x7ffb356a`. **Not attacker-steerable; no
controlled read or write.** And per §2.5 a controller reset is an unclean stop
that re-arms the latch, so it is not merely unhelpful, it is a net loss.

## 5. Ranked findings

Ranked by (a) likelihood of working and (b) risk of making a drive worse.
**Nothing in this table is a recommendation to send a command.**

| # | finding | class | works? | risk | verdict |
|---|---|---|---|---|---|
| 1 | **No controlled write to `0x7ff87c64` exists on the host surface** | — | — | — | The objective is unreachable by memory corruption. §3, §5.1 |
| 2 | FW Download `OFST<<2` / `NUMD+OFST` 32-bit wrap (§4.7) | integer overflow, **real** | Very unlikely — use site mirrors the wrap; staging ≠ overlay bank | **HIGH, asymmetric** | Do not attempt |
| 3 | `0xCA`/`0x03`/{0,1,2} raw NAND page **read** (§4.3) | surface expansion, correctly bounded | Works as designed | Low (read) | Info disclosure only; no path out of the latch |
| 4 | Mode-6 NULL DMA base in `0xE6` (§4.8) | uninitialised pointer | Plausible | **Negative value** | Yields a reset, which re-arms the latch |
| 5 | `0xC6`/`0x20` subs 7–8; `0xCA` subs 20–28 (§4.2) | undocumented, unidentified | Unknown | Unknown | **Do not probe** |
| 6 | Raw NAND **write/erase** family sub-sub collision (§4.4) | UNRESOLVED | Unknown | **Catastrophic** | Close statically or not at all |

### 5.1 The honest bottom line

**I audited the Post-Crash-reachable surface and found no exploitable
memory-safety bug.** That is the result, and it is a real one rather than a
failure to look hard enough.

**Coverage, stated exactly, because "every handler" would be an overstatement.**
Audited: `0x00`, `0x01`, `0x02`, `0x04`, `0x05`, `0x06`, `0x08`, `0x09`, `0x0C`,
`0x10`, `0x11`, `0xC6`, `0xCA`, `0xE6`, `0xEC`, `0xFF` — **sixteen of the
seventeen allow-listed opcodes.** The sole gap is **`0x0A` Get Features**, whose
descriptor was not chased through the overlay relocation of §6.1 before this
document was closed; it is now tractable rather than blocked, and it is the first
thing to pick up if this is resumed (§6.2). Everything below concerns the sixteen
that were audited:

- Every selector in the reachable surface — admin opcode, Get Log Page LID,
  Identify CNS, Set Features FID, `0xFF` OAM sub-command, `0xC6` cmd byte,
  `0xCA` sub-value — is either a hand-written compare chain with an explicit
  default-reject arm, or (in the one table-driven case, `0xCA`) a jump table
  whose bound is **provably exact by three independent measurements**.
- Every length path is either a compile-time constant, a 16-bit-sourced value
  that cannot overflow 32 bits, or explicitly clamped before use.
- The one genuine integer wrap that does exist (§4.7) is mirrored at its use
  site and lands in a buffer that is not the code bank.
- The two "juicy" leads resolved as benign under scrutiny: `0x7ff861cc` receives
  a constant `1`, and `0xC6`/`0x30` is a zero-length internal handshake.
- WD's validation in the vendor commands turned out to be **thicker**, not
  thinner, than the spec paths — `0xE6` already implements the capacity check an
  attacker would want missing, and the raw-flash read clamps to 640 bytes before
  use.

And the objective was independently foreclosed from the other end: the mode word
`0x7ff87c64` has exactly two writers in the entire firmware, both inside
`Admin_IBQCommandReceiver`, reached only by inter-processor message (MSGID
`0x10` "System Inited Done" at `0x7ffb014a`, and MSGID 260/261 "Startup Req" at
`0x7ffb019c`). The enclosing function's only caller has **zero callers** — it is
a task entry driven by the IBQ, not by any NVMe command. **No host opcode reaches
it.** So even a hypothetical bug would have had to be a full write-anywhere
primitive; nothing narrower would have sufficed.

### 5.2 What this means for the five drives

The memory-safety route is closed. That does not change the standing advice, and
it removes a reason to gamble:

- **Reading is safe and remains so.** `0xC6`/`0x20` crash-dump and drive-log
  retrieval is unaffected by anything here, and §6.1 notes a zero-risk way it
  might be extended to name the remaining unknown VUCs.
- **Do not trade a recoverable drive for an experiment.** Every candidate in the
  table above is either useless (#3, #4), unknown (#5), or carries a
  drive-ending downside for a primitive two independent arguments say does not
  work (#2, #6). A drive left latched still has intact media and intact data; a
  drive that has taken a bad `0xFF` or a stray firmware download may not.
- **The remaining live lines of enquiry are elsewhere**, not in this document:
  `docs/sn200-logic-escapes.md` pursues legal state transitions (and reaches a
  boot-mode conclusion worth reading alongside `sn200-independent-re.md` §6.2,
  which they appear to disagree with — see §6.4 below), and
  `sn200-nondestructive-recovery.md` covers the destructive-but-known path.

---

## 6. Open questions

### 6.1 RESOLVED: `0x7ffbc000+` is the overlay **execution window**

This section previously recorded the `0x7ffbc000`–`0x7ffbef01` region as an
unresolvable runtime-populated string blob. **That was wrong, and the correct
answer unlocks the rest of the bank.**

The overlay bank is **linked at `0x300xxxxx` but executes from a RAM window**,
named in the bank header at `PROC8@30000000 0x30000000`:
`"OVB\0", 0x7ff9f000 (rodata), 0x7ffbc000 (text)`. The descriptor table at
`[0x7ff8197c] = 0x7ff81af4` holds 16-byte records
`{load_addr, size, ddr_src, 0}`, two per overlay. The relocation is:

```
static_address = ddr_src + (runtime_address - 0x7ffbc000)
```

Overlay 11: `ddr_src = 0x3002b478`, size `0x380`. Overlay 22:
`ddr_src = 0x30033338`, size `0xa00`. **PROVEN.**

This explains every symptom that made the region look like a string blob: the
demux stores **runtime** addresses, which is why `0x3003353c` has zero xrefs and
why the literal `0x3003353c` appears in no image. The "913 unaligned references"
were relocated code pointers, not string starts. My BSS/trace-name inference was
a plausible reading of the evidence and it was still wrong; recorded here so the
mistake is not repeated.

Consequences, all now realised in §4:

- `0xEC` **is** resolvable and was audited (§4.8).
- The second-stage dispatcher is `0x7ffb9208`; `[obj+0x20]` is an **overlay id**,
  not a handler index, and the handler PC travels separately in `[ctx+0x10]`.
  Its index math `(id-1) << 5` is bounds-checked at `0x7ffb9314`
  (`bltu count,id → reject`) and `0x7ffb9317` (`beqz id → reject`) **before any
  dereference**, and the id comes only from `movi` immediates in the demux.
  No off-by-one, not host-controlled.
- **Every `call8`/`l32r` target inside overlay code must be relocated before it
  means anything.** Overlay-22 `0x30030aa0` and overlay-11 `0x30028be0` are both
  the scheduler `0x7ffb9768`. Any earlier analysis that treated raw overlay
  branch targets as static addresses should be re-checked.
- This is also the tool that could close §4.4, the one genuinely dangerous
  open question.

### 6.2 Unresolved, ranked by how much they matter

1. **Does any `0xCA/0x03` sub-sub value reach a page program or block erase?**
   (§4.4.) Highest-stakes open question in the document. Must be closed
   statically; probing it risks unrecoverable data loss.
2. **Which CDW carries the vendor selector** (§1.3). Three readings, three
   different answers, none of them agreeing with WD's own library. This is
   safety-relevant, not merely tidy: it decides whether a "zeroed CDW12" command
   is actually inert. Resolve by deriving the SQE base from the DMA that fills
   it.
3. **`0x0A` Get Features is unaudited** — the only allow-listed opcode with no
   coverage. It was blocked on the unmapped-descriptor problem, which §6.1 has
   since solved, so this is now straightforwardly tractable.
4. **The CDW13 dword-offset bound on the `0xC6`/`0x20` read path.** `xdis.py`
   cannot decode two Xtensa TIE opcodes (`qrst op1=a op2={0,1}` and
   `qrst op1=b op2={0,1}`) that always appear adjacent around 64-bit address
   arithmetic (`0x30030de4`, `0x3003c017`); these are almost certainly the
   64-bit add applying CDW13 to a media base. The bound could not be verified.
   Reads are believed non-destructive regardless.
5. **Whether an IBQ message can be posted with a host-controlled MSGID and
   payload** (§3). This is the highest-*value* unknown: it would be a legitimate,
   memory-safety-free route to setting the mode word. Nothing found so far
   suggests it is reachable, but the sender side was not exhaustively audited.
**Closed since the first draft:** the MI command-injection surface
(`Admin_VUC_Mi_Test_OVL022` / `VUC_MI_TEST_COMMAND_INJECT_CMD`) is carried on
opcode **`0xEF`**, which is not in the Post-Crash allow-list, so it is
unreachable while latched (§4.8).

### 6.3 A cross-document discrepancy someone should settle

`docs/sn200-logic-escapes.md` (parallel investigation, legal state transitions
only) concludes that a boot with firmware boot-mode `4` (`LOAD_N_GO`) **skips
the crash-section latch entirely**, citing `0x7ffaae2d`
(`beqi a12,4,0x7ffaae53`) jumping over all three marker-9 forcing routes at
`0x7ffaae35`/`0x7ffaae3d`/`0x7ffaae4a`.

`sn200-independent-re.md` §6.2 concludes the opposite: that the load-n-go
override (StrId 3044) is reached only from `0x7ffaaf6b`, the convergence of
startup states 5/6/7, and that state 9 has a different dispatch edge — so
load-n-go overrides *"the shutdown didn't finish"*, not *"there is a crash
record"*.

These cannot both be right. They may be describing **two different mechanisms**
— a boot-mode test *upstream* of marker selection versus a state-9 override
*downstream* of it — in which case both stand and only the wording conflicts.
Nothing in the present document depends on the answer, but anyone acting on the
load-n-go route must resolve it first. **Not adjudicated here.**

### 6.4 Tooling fixes worth making if this is resumed

- `xdis.py` does not decode two FLIX slot-B classes (`pre=1`, and `pre=2` with
  `sub != 0xE`); they print as `?B`/`?Balu`. No control flow depends on them —
  every branch is slot-B `pre=3`/branch-format and decodes cleanly — but the ALU
  ops in length chains are inferred from context rather than read off. This is
  the highest-value decoder fix.
- The two TIE opcodes above are the second.
- Slot-C renderings of the form `movi a2,138` / `movi a2,140` recur identically
  at dozens of call sites and are a decoder artifact, not real instructions.
  Do not rely on them.

---

## 7. Commands that must never be sent

Unchanged from `sn200-nondestructive-recovery.md`, restated because this
document names more of the surface and therefore raises the chance of a typo
landing somewhere terrible.

| command | effect |
|---|---|
| ☠ `0xFF` CDW12 `0x0303` | erase SBL EEPROM — **permanent brick** |
| ☠ `0xFF` CDW12 `0x0403` | Drive Uninit — verb 37, marker payload 1 |
| ☠ `0xFF` CDW12 `0x0503` **while mode == 6** | erases the crash section **and** schedules Drive REINIT → rebuilds L2P, **zeroes the namespace** |
| ☢ `0xDD` | Start Secure Purge — irreversible, no confirmation argument |
| ☠ `nvme wdc get-crash-dump` | reads the dump then **automatically fires `0x0503`** |
| ⚠ `0xCA` CDW12 `0x??03` sub-sub values other than `{0,1,2}` | the raw write/erase family shares this sub-sub space (§4.4). **UNRESOLVED.** A block erase here is unrecoverable. |
| ⚠ `0xC6` CDW12 sub 7 / sub 8 (`0x0720`, `0x0820`) | newly discovered, **unidentified** (§4.2) |

**The adjacency problem is the real hazard.** `0x0303` (brick) and `0x0403`
(uninit) sit immediately below `0x0503` and `0x0603`. Do not sweep, do not
typo, do not let shell history complete the wrong one, and do not write a script
that can emit any `0xFF` sub-command it was not explicitly asked for.

**And the selector may not be in the CDW you think.** Per §1.3 the CDW index
carrying the vendor selector is unresolved, with static reads pointing at CDW10
and WD's library at CDW12. Until that is settled, a command is only safely inert
if **CDW10, CDW11 and CDW12 are all zero**. Zero all three on anything not
deliberately using them, and do not reason "CDW12 is clear, therefore this is
harmless".

The complete `0xFF` selector space is identified — all of 0, 1, 2, 3, 4, 5, 6
under OAM ERASE, plus top-level selectors 3, 4 and 7 — so there is no unknown
value in it and no reason for anyone to go looking by trial.

**Explicitly not recommended: blind probing of any unidentified vendor
sub-command.** This document names several newly discovered ones
(`0xC6`/`0x20` subs 7–8, `0xCA` subs 20–28, the `0x3003d5c0` raw-write family).
Naming them is not a recommendation to issue them. Every one of them sits in an
opcode space whose neighbours destroy drives.

---

## 8. Reproducing this

Everything above came from two helper scripts plus `disany.py`. Neither touches
hardware.

**Find every reference to a global.** Per image, locate literals holding the
target value, then find the `l32r` instructions pointing at those literals. The
displacement formula is the part that is easy to get wrong:

```python
import glob, os, struct, sys
targets = set(int(x, 16) for x in sys.argv[1:])
for p in sorted(glob.glob(os.path.expanduser('~/sn200fw/flat/*.bin'))):
    base = int(os.path.basename(p)[:-4].rsplit('_', 1)[1], 16)
    d = open(p, 'rb').read()
    lits = {}
    for o in range(0, len(d) - 3, 4):
        w = struct.unpack_from('<I', d, o)[0]
        if w in targets:
            lits[base + o] = w
    if not lits:
        continue
    for o in range(0, len(d) - 2):
        if (d[o] & 0xF) != 1:            # l32r, narrow form
            continue
        imm16 = d[o + 1] | (d[o + 2] << 8)
        pc = base + o
        lit = ((pc + 3) & ~3) + ((imm16 - 0x10000) * 4)
        if lit in lits:
            print("%-22s %08x: l32r a%-2d,[%08x] = %08x"
                  % (os.path.basename(p), pc, d[o] >> 4, lit, lits[lit]))
```

**Match literals per image, never by address across images.** Every core has its
own literal pool at the same addresses holding different values; cross-image
address matching produces a page of confident nonsense. This bit me first time.

The same script with the match changed to `(w >> 16) == StrId and (w & 0xff) <= 12
and ((w >> 8) & 0xf) == 0` finds log call sites, which is how most handlers here
were located.

**Segment map**, used to establish that `0x7ffbc000+` is BSS rather than a
missing file:

```sh
cd tools/sn200-fw && python3 segparse.py ~/sn200fw/fw/KNGND122/PROC8.bin
```

**Caveats that apply to every listing above.** A linear sweep desynchronises
across data regions, so treat any single decoded line far from a known `entry`
as suspect and re-anchor. `xdis.py` does not decode two FLIX slot-B classes or
two Xtensa TIE opcodes (§6.3); those print as `?B`/`?Balu`/`qrst`. No control
flow in this document depends on an undecoded slot — every branch is slot-B
branch-format and decodes cleanly — but arithmetic shown as `?Balu` is inferred
from context rather than read off.
