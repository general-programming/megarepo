# SN200 marker 8 / READ ONLY startup — can a host request it?

Firmware `KNGND122`. Static analysis only; no drive was touched.

**Answer, up front: no — and not because the route is hard to find, but because
the thing everyone has been chasing is not what it looked like.**

`docs/sn200-firmware-re.md` §13.6 claims *"PROC12 `0x7ffa7a68` writes marker 8
when `[ctx+0x48] == 6`"*. The instruction decode in §13.6 is **correct**. The
*interpretation* is **wrong**: the 32-bit word it stores is an **Event-Log record
tag**, not the System-Area startup marker. The two enums share the
`0x80000000 | N` encoding convention and overlap numerically, which is what
produced the false positive. §13.6 is retracted here — see §5 for the disproof
and §7 for what to change.

The other half of §13.6 survives intact and is worth keeping: **if** marker 8
could be set, it really would be a non-destructive recovery. That part is
re-verified below (§2). It is simply not reachable — from a host command, from
NVMe-MI, from the UART console, or from any other core.

---

## 1. Method

- Disassembly: `tools/sn200-fw/disany.py` / `xdis.py` (FLIX slots A, B, C).
  Every listing below was entered at a function `entry` or at a branch target
  proven by a decoded branch — never mid-stream.
- Literal/xref sweeps: scripts in the session scratchpad, covering **both** plain
  3-byte `l32r` and `l32r` hidden in FLIX slot A. All 18 flat images
  (`PROC0..PROC15`, `PROC8@30000000`, `FCC`).
- Toolchain sanity check: `disany.py PROC8@30000000 3003353c 30033560` →
  `entry a1,0x30`, no gaps, no float ops. ✔

---

## 2. What marker 8 would do, if it could be set — **PROVEN, unchanged**

The System-Area startup marker lives at `*(0x7ff8c7ec)` in PROC0's map
(`0x7ff83120` holds the pointer). PROC0 `0x7ffaac30` reads it and dispatches at
`0x7ffaae69`–`0x7ffaaede`:

```asm
7ffaaed3: l32r a15,0x7ff83478        ; = 0x80000008
7ffaaed6: { sync/extw ; beq a11,a15,0x7ffaaff5 }
...
7ffaaff5: { movi a11,1272 ; j 0x7ffaac8a ; movi a5,3 }
```

`a5` is the startup type, stored by `0x7ffaac8a` (`s32i.n a5,a12,0x30`,
`a12 = 0x7ff8c788`). `a11` is the StrId logged via `"%s\n"`.

- StrId 1272 = `"SYS: Read-only startup"`
- startup type **3**; the type-name array is StrId 303+type
  (`303 FIRST / 304 NORMAL / 305 RECOVERY / 306 READ ONLY / 307 FIRMWARE UPDATE /
  308 FAST / 309 INVALID`) → **READ ONLY STARTUP**

SAM (PROC6 `0x7ffba898`) switches on the type at `0x7ffba940`, and type 3 sets a
single flag bit then **falls into the NORMAL path** — System Area read, L2P
restored, namespace present, writes refused at the admin/IO layer
(StrIds 1833, 1988, 2007, 3210, 3266, 510, 1494). BlockMgr does the same
(`0x7ffa66e8`, StrId 2671 `"BlockMgr: Read Only Startup"`).

So the posture is real and is exactly what a latched drive needs. Nothing below
weakens this. The problem is purely that no code sets the marker to 8.

### 2.1 The marker enum, now pinned exactly — **PROVEN**

PROC0 `0x7ffaacea` names a marker by indexing a `u16` StrId table:

```asm
7ffaacea: l32r a13,0x7ff83338        ; = 0x7fffffff
7ffaaced: l32r a12,0x7ff83438        ; -> 0x7ff81180   (the name table)
7ffaacf0: and a11,a11,a13            ; index = marker & 0x7fffffff
7ffaacf3: { l32r a10,0x7ff8343c ; addx2 a11,a11,a12 }
7ffaacfb: l16ui a11,a11,0x0          ; StrId
7ffaacfe: call8 0x7ffb5398
```

The table at PROC0 `0x7ff81180` is exactly the 11 consecutive `u16`s
`3029..3039` (verified by byte scan; it is the only such table in any image), so
**marker N → StrId 3029+N**:

| marker | StrId | name |
|---|---|---|
| 0 | 3029 | No previous marker found |
| 1 | 3030 | CLEAN shutdown |
| 2 | 3031 | PFAIL shutdown |
| 3 | 3032 | Drive REINIT requested |
| 4 | 3033 | FACTORY drive REINIT requested |
| 5 | 3034 | Normal Shutdown STARTED |
| 6 | 3035 | PFAIL Shutdown STARTED |
| 7 | 3036 | PFAIL Shutdown TIMEOUT |
| **8** | **3037** | **READONLY Startup requested** |
| 9 | 3038 | POST CRASH Startup |
| 10 | 3039 | Invalid marker |

The enum has **11 members, 0..10**. Remember this bound; §5 turns on it.

---

## 3. `0x80000008` exists twice in the entire firmware — **PROVEN**

Exhaustive scan of all 18 flat images for the word `0x80000008` in any 4-aligned
pool slot, plus every `l32r` (plain **and** FLIX slot A) that loads it:

| image | literal | referenced from | what it does |
|---|---|---|---|
| PROC0 | `0x7ff83478` | `0x7ffaaed3` | **comparison only** (the boot dispatch above) |
| PROC12 | `0x7ffa0d94` | `0x7ffa7d70` | the `moveqz` in §4 |

There is no third site. `movi` cannot synthesise `0x80000008` (12-bit signed
immediate), so any producer must load it from a pool — which makes this scan a
sound enumeration of producers, provided each hit is decoded from a valid entry
point. Both were.

---

## 4. What writes `[ctx+0x48]` — the full chain, **PROVEN**

### 4.1 The consumer: PROC12 `0x7ffa7a68` is a coroutine, and `ctx` is a pooled request object

```asm
7ffa7a68: entry a1,0x70
7ffa7a6b: l32i.n a9,a2,0x10                       ; saved continuation
7ffa7a75: { movi a6,276 ; beqz a9,0x7ffa7e99 }    ; first entry -> 0x7ffa7e99
7ffa7a7d: jx a9                                   ; otherwise resume
```

`a2` = `ctx`. Its layout, recovered from use:

| off | meaning |
|---|---|
| `+0x00`,`+0x04` | doubly-linked free-list pointers |
| `+0x10` | saved continuation PC (coroutine resume) |
| `+0x30`,`+0x34` | ring write index / wrap counter |
| `+0x48` | **event code** |
| `+0x4c` | record counter |
| `+0x54`,`+0x58` | the **two words of the record about to be appended** |
| `+0x60`,`+0x64` | caller-supplied payloads |

Release path (`0x7ffa7d53`) pushes `ctx` back onto the list at
`*(0x7ff81670)+0x198`. Allocation pops from `*(0x7ff81428)+0x3dc`.

### 4.2 Two dispatches on `[ctx+0x48]`

**First entry**, `0x7ffa7e99` — default case logs StrId 1425
`"JournalMgr: Request to record invalid log event %d"`:

| event code | target | word stored to `[ctx+0x54]` |
|---|---|---|
| 0 | `0x7ffa7fb7` | `[ctx+0x40]` (caller-supplied) |
| 1 | `0x7ffa7edf` | computed |
| 2 | `0x7ffa7fd9` | `0x80000009` |
| 3 | `0x7ffa7fea` | `0x80000001` or `0x80000002` (select) |
| 4 | `0x7ffa8008` | `0x80000003` |
| 5 | `0x7ffa7fc8` | `0x80000005` |
| 6 | `0x7ffa8019` | `0x80000007` |
| other | — | StrId 1425, then `break.n` |

Tail: `0x7ffa7ed2` increments `[ctx+0x4c]` and jumps to `0x7ffa7d38`, which hands
`&ctx[0x54]` with a count of **2 words** to the writer and suspends with
resume PC `0x7ffa7b2a` (literal `0x7ffa0d8c = 0x7ffa7b2a`).

**Resume**, `0x7ffa7b2a`:

```asm
7ffa7b2a: l32i a11,a2,0x48
7ffa7b2d: { sync/extw ; beqi a11,4,0x7ffa7ce8 }   ; -> 0x80000004
7ffa7b35: { sync/extw ; beqi a11,5,0x7ffa7d70 }
7ffa7b3d: { sync/extw ; beqi a11,6,0x7ffa7d70 }
```

```asm
7ffa7d70: l32r a13,0x7ffa0d94        ; = 0x80000008
7ffa7d73: l32r a12,0x7ffa0d98        ; = 0x80000006
7ffa7d76: addi a14,a11,-6
7ffa7d79: moveqz a12,a13,a14         ; a11 == 6 -> a12 = 0x80000008
7ffa7d7c: { s32i a12,a2,0x54 ; j 0x7ffa7cee }
```

I re-decoded `0x7ffa7d70`..`0x7ffa7d7b` byte by byte from the branch target
(`d1 09 e4 | c1 09 e4 | e2 cb fa | e0 cd 83`) and confirm §13.6's listing is
right, including `moveqz` (RST3 `op2=8`, `r=a12 s=a13 t=a14`).

So the second record is always the first + 1: 3→4, 5→6, 7→8.

### 4.3 The event code is set locally in PROC12, from an inter-processor message

Every store to offset `0x48` in every image was enumerated (plain + FLIX slot A).
On the *journal request object* (identified by the `+0x3dc` free-list pop and the
`+0x0/+0x4` links) there are exactly two:

**`0x7ffa2450`, inside `0x7ffa2380` — constant 5:**

```asm
7ffa2433: { l32r a3,0x7ffa0824 ; movi a9,5 }
7ffa2441: l32i a3,a3,0x3dc      ; free-list head
7ffa2444: ...unlink...
7ffa2450: s32i a9,a3,0x48       ; [obj+0x48] = 5
```

**`0x7ffa26d6`, inside `0x7ffa2648` — the only source of 6:**

```asm
7ffa26b6: l32i a11,a11,0x3dc    ; free-list head
7ffa26b9: ...unlink...
7ffa26c1: l32i.n a12,a2,0x10                       ; a2 = the received message
7ffa26c3: { s32i a3,a11,0x0 ; movi a4,4 }
7ffa26cb: { s32i a3,a11,0x4 ; ... ; movi a3,6 }
7ffa26d3: moveqz a3,a4,a12                         ; [msg+0x10]==0 -> 4, else 6
7ffa26d6: s32i a3,a11,0x48
```

**`[ctx+0x48] == 6` iff `[msg+0x10] != 0`.** Both `0x7ffa2380` and `0x7ffa2648`
load the handler pointer `0x7ffa7a68` from `0x7ffa0938` (the only pointer literal
to it in any image) while filling the object.

### 4.4 Who delivers the operation code

Both are called only from the Journal Manager's message dispatcher, **PROC12
`0x7ffa28c0`**, which has **zero `CALLn` call sites** — it is a task entry driven
by the inter-processor message queue. Operation code in `a11`, message pointer in
`a4`:

```asm
7ffa2968: { sync/extw ; bltui a11,5,0x7ffa2a68 }
7ffa2970: { sync/extw ; bltui a11,6,0x7ffa2d80 }   ; op 5
7ffa2978: { sync/extw ; beqi a11,6,0x7ffa2e6d }    ; op 6
7ffa2980: l32r a10,0x7ffa0950   ; StrId 1388 "Journal Mgr: Invalid operation (%u)"
```

```asm
7ffa2d80: mov.n a10,a4 ; call8 0x7ffa2380     ; op 5 -> event code 5
7ffa2e6d: mov.n a10,a4 ; call8 0x7ffa2648     ; op 6 -> event code 4 or 6
```

`call8` passes the caller's `a10` as the callee's `a2`, so **`a2` of `0x7ffa2648`
is the received message buffer** and `[msg+0x10]` is a message field — the same
shape as PROC8's `Admin_IBQCommandReceiver`, which takes its startup-mode word
from `[req+0x10]` of an IBQ message (MSGID 260/261).

### 4.5 The request-code space

| code | producer | first record | second record |
|---|---|---|---|
| 0 | `0x7ffa2db7` (variable) | `[ctx+0x40]` | — |
| 1 | — | computed | — |
| 2 | `0x7ffa2f57` (constant 2) | `0x80000009` | — |
| 3 | (op 3 path) | `0x80000001`/`0x80000002` | — |
| 4 | `0x7ffa26d6` when `[msg+0x10]==0` | `0x80000003` | `0x80000004` |
| 5 | `0x7ffa2450` (constant 5) | `0x80000005` | `0x80000006` |
| 6 | `0x7ffa26d6` when `[msg+0x10]!=0` | `0x80000007` | **`0x80000008`** |
| ≥7 | — | invalid (`break.n`) | — |

---

## 5. Why these are **not** startup markers — the disproof

Four independent facts, each decoded:

**(a) The record is appended to a two-word ring, not stored to the marker word.**
`0x7ffa7d38` / `0x7ffa7d15` / `0x7ffa7ccb` all do
`addi aN,a2,84 ; movi aM,2` — address `&ctx[0x54]`, count **2 words** — with the
ring position in `[ctx+0x30]`/`[ctx+0x34]` and a record counter in `[ctx+0x4c]`.
The startup marker is a **single** word at `*(0x7ff8c7ec)`.

**(b) The strings say "log event", and they say it in pairs.**
The default case is StrId 1425 `"JournalMgr: Request to record invalid log
event %d"`. The replay side (PROC12 `0x7ffa7818`) logs StrId 1422
`"Journal Manager: Invalid Log event 0x%08x - 0x%08x found at %d in record %d"`
— **two 32-bit words per log event**, exactly matching `[ctx+0x54]`/`[ctx+0x58]`.

**(c) The tag space is wider than the marker space.**
The same pool feeds `0x7ffa0d74 = 0x8000000E` (loaded at `0x7ffa7cac`, stored to
`[ctx+0x54]` at `0x7ffa7cb4` on the ring-full/sentinel path — the Event Log
**End Marker**, cf. StrId 1419 *"Event Log Replay failed as End Marker not found
at %d-%d"*). `0x0E` = 14 is **outside** the 11-member marker enum of §2.1; PROC0
would reject it as `"SYS: Bad startup marker"`. A single enum cannot be both.

**(d) The semantics contradict.** Event code 5 comes from `0x7ffa2380`, and that
function is the **Format** recorder — it validates `[msg+0x8] ∈ {0,1}` and
otherwise logs StrId 1447 `"JournalMgr: Event for Format type %d not supported"`
(`0x7ffa23a2: bnei a13,1,0x7ffa2620`). Event code 5 emits tags `0x80000005` and
`0x80000006`, which in the marker enum are *"Normal Shutdown STARTED"* and
*"PFAIL Shutdown STARTED"*. A format operation does not write shutdown markers.
The two enums are simply different.

**And the ownership split confirms it.** The Journal Manager's log is
**NAND-resident, per-zone**: the same function logs StrId 1428 *"Journal Manager:
LBN %d exceeded V2P image unit end"* (`0x7ffa7ab8`) and StrId 1427 *"Saving image
and log records for zone %d"* (`0x7ffa7d84`); replay errors cite
*"Flash error %d, lbn 0x%x record %d"* (StrId 1417). The startup marker lives in
the **EEPROM SA Journal**, owned entirely by PROC0 — StrIds 1247–1252
(`"EEPROM: Creating new SA Journal"`, `"Scanning SA Journal"`,
`"Index=%d. Written Journal Entry %d. Slot %d. CRC=%08X"`) are referenced only
from PROC0 `0x7ffa5584`.

> **Conclusion (PROVEN):** PROC12 `0x7ffa7d70` writes an **Event-Log record tag**
> `0x80000008` into a NAND journal. It does not touch the System-Area startup
> marker. §13.6 of `sn200-firmware-re.md` is wrong and must be corrected.

---

## 6. Who *can* write the startup marker — exhaustive

Scan for every `l32r` (plain + FLIX slot A) whose literal equals `0x7ff8c7ec`,
across all images:

```
PROC0 0x7ffa8d2d  0x7ffaab2b  0x7ffaabdb  0x7ffab35b
      0x7ffab369  0x7ffab3dd  0x7ffab3fb  0x7ffab84e   (+ 0x7ffaac3b, the dispatch)
```

**No other core references it at all.** The PROC0 uses at `0x7ffab3xx`/`0x7ffab84e`
touch `+0x14` (user capacity) and `+0x2c`, not the marker; `0x7ffaab2b`/`0x7ffaabdb`
are the shutdown-debug dump and the first-time-startup path.

Marker producers, complete:

| marker | producer | how |
|---|---|---|
| 0 | PROC0 `0x7ffaafc0`, `0x7ffaaf78` | `s32i.n a6,a7,0x0`, `a6 = 0x80000000` |
| 1, 2 | PROC6 SAM `0x7ffbba61` | `s32i.n a13,a14,0x3c`; `0x7ff8bbd0` is loaded exactly **once** in PROC6 (`0x7ffbba58`), so this is the only site |
| 3 | PROC0 `0x7ffaaef7`, `0x7ffa430c`, `0x7ffabccf` | |
| 4 | PROC0 `0x7ffa4306` | selected against 3 by `[a2+0x54]`, staged into a queue record and submitted (`0x7ffb32f8`) |
| 5 | PROC0 `0x7ffa8dda` | record `[rec+0x20] = 0x80000005`, `call8 0x7ffb4fec` |
| 6 | PROC0 `0x7ffa83d7` | record `[rec+0x20] = 0x80000006` |
| 7 | PROC0 `0x7ffa83f2`–`0x7ffa840a` | record `[rec+0x20] = 0x80000007` |
| 9 | PROC0 `0x7ffaaf08` | `s32i a11,a7,0x0` |
| **8** | **none** | — |

Every producer loads its value from a literal pool. `0x80000008` appears in
PROC0's pool once and is only ever *compared*. **Marker 8 has no writer anywhere
in the firmware.** PROC0's handler at `0x7ffaaff5` is dead code — reachable only
if the EEPROM SA Journal already contained an 8, which nothing can put there.

### 6.0 The marker *setter*, decoded — **PROVEN**

There is a single boot-marker setter service in PROC0, at **`0x7ffa84c8`** (a
coroutine; zero direct `callN` sites, reached only through the pointer literal at
`0x7ff82b54`). Its store is:

```asm
7ffa84cd: { l32r a5,0x7ff83120 ; movi a6,0 }   ; a5 = 0x7ff8c7ec
...
7ffa851b: l32i.n a10,a2,0x18                   ; a10 = the requested marker
7ffa851d: { l32r a12,0x7ff82b4c ; beq a10,a11,0x7ffa8528 }   ; a11 = 0x80000003
7ffa8525: bne a10,a12,0x7ffa8535                             ; a12 = 0x80000004
7ffa8528: LOG 1338 "SYS: Scheduling drive re-init on next startup"
...
7ffa8535: l32r a14,0x7ff825a0        ; = 0x80000000
7ffa8538: beq a10,a14,0x7ffa8541
7ffa853b: s32i.n a10,a5,0x0          ; *(0x7ff8c7ec) = a10   <-- THE marker write
```

Note carefully: the `0x80000003`/`0x80000004` comparison at `0x7ffa851d` only
selects whether to log StrId 1338. **The store at `0x7ffa853b` takes whatever
`[req+0x18]` holds** (guarded only against `0x80000000`). So the setter is *not*
value-restricted — which makes the callers, not the setter, the thing to
enumerate.

Corrected sweep (see §6.2) — three `l32r` sites load `0x7ff82b54`:

| loader | caller | marker supplied |
|---|---|---|
| `0x7ffa431f` | OAM verb-`0x25` "schedule drive re-init" (`0x7ffa3e48`) | `0x80000003` / `0x80000004`, selected by `[req+0x54]` |
| `0x7ffa4732` | same function, second submit path | from the same request object |
| `0x7ffabccc` | firmware download / commit | `0x80000003` |

Every one of them sources the value from PROC0's literal pool
(`0x7ff82b4c = 0x80000004`, `0x7ff82b50 = 0x80000003`). **None can produce
`0x80000008`** — and `0x7ff83478`, PROC0's only `0x80000008`, is loaded at
`0x7ffaaed3` and nowhere else. Independent second proof of §3, from the consumer
side.

### 6.1 The one residual path, and why it is also closed

None of those 9 pointer loads *initialises* `[0x7ff8c7ec+0]` either — PROC0 only
ever overrides it. The marker therefore arrives by a **block read of the SA
header from the EEPROM SA Journal** into a parent structure whose base is never
spelled as `0x7ff8c7ec` (note `0x7ff8c788`, the startup-type word, sits `0x64`
below it in the same block). **INFERRED**, but it is the only shape consistent
with the evidence.

That makes the residual question: *can anything write an arbitrary SA Journal
entry?* The journal writer is PROC0 `0x7ffa5584`, fed from PROC0's internal work
queue (`0x7ffb4fec` → `0x7ffa3c28`, and `0x7ffb32f8`). Every submitter found
stages a **compile-time constant** marker from PROC0's own literal pool
(`0x80000000/3/4/5/6/7/9`) or PROC6's (`0x80000001/2`). No submitter copies a
host-supplied word into that field.

And no raw System-Area *write* command exists: the only raw-SA string is
StrId 2935 `"OAM READ RAW SA CMD: Read of System Area journal from EEPROM
failed."` — read-only. (The OAM *erase* sub-commands do exist and include
`Erase to SBL EEPROM` and `Drive Uninit`; do not go near them.)

### 6.2 ⚠ Tooling trap that nearly invalidated this: FLIX bundles are **not** 4-aligned

My first literal-xref sweep scanned FLIX bundles only at 4-aligned offsets. That
is wrong. `0x7ffa430f`, `0x7ffa4317`, `0x7ffa431f`, `0x7ffa4327` are consecutive
8-byte bundles at addresses ≡ 3 (mod 4). The aligned-only scan **missed
`0x7ffa431f` entirely** — i.e. it missed one of the three loaders of the marker
setter.

Every sweep in this document was re-run with slot A decoded at **every byte
offset**, validated by requiring the corrected scan to find `0x7ffa431f`. It did,
and it also turned up a previously unrecorded third loader (`0x7ffa4732`) and the
setter's own reference (`0x7ffa84cd`).

**The §3 result is unchanged under the corrected scan**: `0x80000008` still has
exactly two references in all 18 images. That the central negative survived a
strictly wider search is the main reason to believe it.

Recorded in `docs/xtensa-flix-decoding.md`. Any earlier finding in the SN200 docs
that rests on an aligned-only FLIX sweep should be re-run.

### 6.3 Limits of this claim — stated honestly

- The opcode→handler binding for `0xEC` / `0xFF` / `0xEF` is **runtime-built
  BSS** (descriptor region `0x7ffbc1xx`–`0x7ffbc6xx`, past the last image load
  range `0x7ffbb064`). Those bindings are not in the image and cannot be resolved
  statically. So "no vendor sub-command reaches it" rests on the *producer-side*
  sweep (§3, §6) rather than on having walked every vendor handler.
- That producer-side sweep is the stronger argument anyway: a handler can only
  set marker 8 by getting `0x80000008` into the SA Journal entry, and the value
  exists in exactly two places in the firmware, neither of which is a write to
  that field. Synthesising it arithmetically is conceivable, but it would still
  have to be stored through one of the 9 enumerated `0x7ff8c7ec` references or
  submitted as a journal record — and none of those carries a computed word.
- `0x80000000` is a common sign-bit constant, so an exhaustive "was it built by
  OR-ing" sweep is not tractable. This is the one gap I cannot fully close.

---

## 6.4 Host command surface — exhaustive, and it confirms the negative

Full enumeration of the allow-listed surface (separate sweep, all PROVEN unless
noted). Nothing here reaches `0x7ffa84c8` except the three rows marked ⚠.

| opcode | what it is | reaches marker setter? |
|---|---|---|
| `0xFF` cmd 3 (`CDW12 = 0x??03`) | OAM ERASE, 7 sub-commands | ⚠ subs 4 and 5 only |
| `0xFF` cmd 4 (`0x0004`) | `sys_init_done` / startup-type probe — reads `*(0x7ff87c64)` | no (pure read) |
| `0xFF` cmd 7 (`0x??07`) | **`OAM READ RAW SA CMD`** — reads the System Area journal from EEPROM to host | no (read) |
| `0xFF` other | LOG 1626 `"Received Unsupported Command"`, status `0x40040000` | no |
| `0xCA` | real dispatch table at `0x7ffa760e`, 67 slots; the "12-entry sub-list" at `0x7ffa6d76` is the *gate's* inline compare chain, not a table. Allow-listed subs {2,3,4,8,13,14,15,16,17,19,33,50} are reads / flash-controller resets | no |
| `0x09` Set Features | FIDs 1–11, 126–131, **240 (0xF0)** only. No FID ≥ 0xC0 other than 0xF0. No APST (0x0C) | no |
| `0x0A` Get Features | same FID set; pure read in every arm | no |
| `0xE6` | log-dump reader (big-endian length header, DDR→host DMA) | no (pure read) |
| `0xEC` | dispatches to overlay handler `0x7ffbc24c`; **semantics UNKNOWN** — the pointer did not resolve under any recovered overlay delta | unresolved |
| `0xC6` cmd `0x20` | `VUC Get Drive Log`, subs 0–8, all reads | no |
| `0xC6` cmd `0x30` | `VUC Reset Drive Stats` — a writer, but statistics only (INFERRED) | no |
| `0x10` Firmware Commit | ⚠ sets marker 3 via `0x7ffabccc` | ⚠ marker 3 only |

### ⚠⚠ New landmine found while doing this: `0xFF` / CDW12 `0x0403`

OAM ERASE **sub 4 = "Drive Uninit"** posts verb `0x25` with parameter
`[req+0x128] = 1`, and — unlike sub 5 (crash-dump erase, parameter 0) — it has
**no startup-type gate**. Sub 5 is guarded by `bnei a14,6` at `0x30033709`; sub 4
jumps straight to the post at `0x300337e3`. Parameter 1 selects the **FACTORY**
re-init marker.

`0x0403` is one hex nibble from `0x0503`, is unconditionally allow-listed while
latched, and is strictly worse. **Do not type it.** (That parameter 1 → marker 4
rather than 3 is INFERRED — the conditional-move operand order in slot B does not
decode reliably — but the *ungated* part is PROVEN and is the dangerous part.)

### Possibly useful, and read-only: `0xFF` / CDW12 `0x0007`

`OAM READ RAW SA CMD` (handler `0x30033824`) builds a request with
`[req+0x78] = 42`, DMAs the System Area journal from EEPROM to the host, and is
in the Post-Crash allow-list. It is not in `libdmi_core`. **This is a
non-destructive way to read the drive's actual current startup marker** rather
than infer it. Failure logs StrId 2934. It is a read; it posts verb 42, which is
not the marker setter.

---

## 6.5 Out-of-band: NVMe-MI over SMBus, and the UART — both closed

**NVMe-MI (PROC9) — PROVEN.** Router `MI_CommandRouter` `0x7ffb1330`; the message
type is extracted at `0x7ffb15ad` (`extui a10,a10,11,4`):

| NMIMT | handler | |
|---|---|---|
| 0 Control Primitive | `0x7ffb2890` | |
| 1 NVMe-MI Command | opcode chain `0x7ffb1805` | opcodes `0x00`–`0x07` only |
| 2 NVMe Admin tunnel | filter `0x7ffb2531` → `0x7ffb5930` | see below |
| 4 PCIe Command | `0x7ffb6340` | |
| 3, 5–15 | rejected | **no vendor-defined message type exists** |

The admin tunnel is **narrower** than the PCIe path, not wider. Gate `0x7ffb2531`
admits exactly `{0x02 Get Log Page, 0x06 Identify, 0x09 Set Features (only when
`[msg+0x40]==2`), 0x0A Get Features, 0x10 FW Commit, 0x11 FW Image Download}`.
`0x0D/0x15/0x80/0x81/0x82` are enumerated **only to be rejected**, and everything
`> 0xBF` — which is every vendor opcode, including `0xFF` and `0xCA` — falls into
the "unhandled admin cmd opcode" arm. **The VUC channel is not reachable
out-of-band at all.**

> Correction to `docs/sn200-independent-re.md` §10.1: it lists
> `0x0D, 0x15, 0x81, 0x82, 0xBF` as *allowed*. They are not — that is the reject
> list read as an allow list, and `0xBF` is not an opcode but the bound of a
> `bgeu` range check. This was the sole basis for treating SMBus as a promising
> escape route; it is not one.

Neither MI write path touches persistent boot state: **Configuration Set**
(`0x7ffb54d8`) writes three CFGIDs into **volatile RAM at `0x7ff94174`**, and
**VPD Write** (`0x7ffb42e8`) reaches the external FRU/VPD I²C EEPROM only
(240-byte cap, 16-byte page writes via PROC0's SMBus2 master) — never the SPI
System Area. PROC9 contains **no marker constant at all**.

The MI stack *does* come up on a latched drive (startup is internal message id 61
into `0x7ffb7540`; the gate at `0x7ffb759d` is a plain "not already started"
check, not a startup-type test). It simply has nothing to offer once there.

Curiosity, not a route: `Get Message Type Support` (`0x7ffb9246`) advertises MCTP
message type `0x7E` (vendor-defined PCI), but the SMB receive dispatch at
`0x7ffb8d9f` accepts only `0x00` and `0x04`; a `0x7E` message is silently dropped
(StrId 288). The endpoint claims a vendor channel it does not implement — which
is probably why one would expect a vendor path to exist.

## 6.6 The extra nail: a latched drive overwrites the marker anyway

Even granting a hypothetical writer, it would not help the drives in question.
PROC0 `0x7ffaaf02`–`0x7ffaaf0b`:

```asm
7ffaaf02: l32r a10,0x7ff83484   ; StrId 3042 "SYS: Detected a CRASH or PFCRASH section."
7ffaaf05: call8 0x7ffb5398
7ffaaf08: l32r a11,0x7ff83474   ; = 0x80000009  POST CRASH
7ffaaf0b: { s32i a11,a7,0x0 ; j 0x7ffaae69 }
```

Whenever either crash-section latch bit is set, the marker is **forced to 9**
before the dispatch re-runs. A marker of 8 would be discarded on the way in. So
on a latched drive the read-only path is doubly unreachable: nothing writes 8,
and 8 would be overwritten if it were written.

---

## 7. Corrections to make in the other SN200 docs

- **§13.6** — retract. The instruction decode stands; the claim that it writes the
  *startup marker* does not. Replace with a pointer here.
- **§13.6a** — the brute-force literal table is still correct and still useful.
  Its "READONLY (8): `0x7ffaaed3` only — the dispatch comparison" row was, after
  all, the right reading of PROC0. The PROC12 hit belongs to a different enum.
- **§6 item 5** ("try to steer the startup marker to READ ONLY") — close it.
  Not speculative any more: there is no writer.

In `docs/sn200-independent-re.md`:

- **§10.1** — the NVMe-MI admin allow-list is a reject-list misread as an
  allow-list, and `0xBF` is a range bound, not an opcode. See §6.5.

In `docs/xtensa-flix-decoding.md` (both already applied):

- The unaligned-bundle warning for literal scanners (§6.2 here), and
- the note that `op0=0xF` slot B is a full branch slot and `op0=0xE` with
  `pre=3` is a `j`. Every dispatch chain in this document lives in those slots.
  A linear sweep cannot see those edges — which is exactly how a reject-list came
  to be read as an allow-list. This is the highest-leverage correction of the
  set, because the same blind spot will keep producing the same class of error.

New tooling: `tools/sn200-fw/litref.py` (+ `tests/test_litref.py`) does the
`l32r` literal sweep correctly, with the unaligned-FLIX case pinned by a test.
`xref.py` still covers `CALLn` sites.

---

## 8. Verdict

| question | answer |
|---|---|
| What writes `[ctx+0x48]`? | PROC12 `0x7ffa2450` (constant 5) and `0x7ffa26d6` (`[msg+0x10]!=0 ? 6 : 4`), both on a pooled Journal-Manager request object, both driven by the IBQ task entry `0x7ffa28c0`. **PROVEN** |
| Is it host-reachable? | No. `0x7ffa28c0` has no call sites; the operation code arrives as an inter-processor message. **PROVEN** |
| Does `[ctx+0x48]==6` set startup marker 8? | **No.** It appends an Event-Log record tagged `0x80000008` to a NAND journal. Different enum, different medium, different structure. **PROVEN** |
| Can anything set startup marker 8? | No. Exhaustive literal + xref sweep of all 18 images: `0x80000008` exists twice; PROC0's is a comparison, PROC12's is the Event-Log tag. **PROVEN** |
| Exact command encoding to request it? | **None exists.** |

**Marker 8 / READ ONLY startup is real, is genuinely non-destructive, and is
unreachable.** The read-only posture was built into the firmware and then never
wired to a producer — the handler is vestigial. There is no host command, no
vendor sub-command, no out-of-band route and no other core that can set it,
because nothing anywhere sets it.

For the owner: **stop hoping for a software route to a read-only boot.** The
decision on these five drives is now a hardware/vendor decision, not a
reverse-engineering one.
