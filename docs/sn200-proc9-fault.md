# What trapped on PROC9

Follow-on to `docs/sn200-fault-record.md`, which proved the `.CDI` record at file
offset `0x11000` of `docs/sn200-dumps/nvme7-crash-128k.bin` is a fatal
synchronous exception on **PROC9**, in an RTOS-dispatched task body, on
`HUSMR7676BDP3Y1` / `KNGND122`.

That document stopped at *"the task's identity is not statically recoverable —
the entry pointer is `[task+0x8]` in RAM"*. **It is recoverable.** The pointer is
written exactly once, at init, from a literal. This document names the task, the
faulting instruction, and the corrupt data structure.

Labels as before: **PROVEN** = read out of correctly-decoded instructions or
observed bytes; **INFERRED** = short chain over proven facts; **SPECULATIVE**.

All disassembly `SN200_FW=~/sn200fw ... --image PROC9_7ff80000`, always from a
function entry.

---

## 0. Bottom line

| Question | Answer | Grade |
|---|---|---|
| What was the faulting task? | **`MI_ControlPrimitiveHandler`**, PROC9 `0x7ffb2890` — the NVMe-MI control-primitive command handler | **PROVEN** |
| Where in it? | `0x7ffb29b8`, `s32i.n a13,a15,0x0`, the fourth store of a circular-list tail insert | **INFERRED**, tightly |
| Why? | `a15 = 0` — the list's **tail pointer was NULL**. A null-pointer store. `EXCCAUSE 3` (LoadStoreError), `EXCVADDR 0` | **INFERRED** |
| What was corrupt? | The NVMe-MI completion FIFO at `0x7ff941d4/0x7ff941d8`: **both head links zeroed**, *and* the "current request" global `0x7ff94954` pointing at **the FIFO's own sentinel** | **PROVEN** (both read out of the register file) |
| One cause for both? | Yes — the sentinel was dequeued and processed as if it were a request, at `0x7ffb2a93`, which zeroes `node->next`/`node->prev` at `0x7ffb2ab0`/`0x7ffb2ab2` | **INFERRED** |
| How did the sentinel get onto the handler's queue? | **Not determined.** No code in PROC9 dequeues that FIFO, so its consumer is off-core | **open** |
| Is it BMC-driven? | The whole subsystem is inbound NVMe-MI. But nothing found makes a *malformed* message the trigger — the error paths are validated and share the same enqueue as the success paths | **SPECULATIVE, unproven** |
| Does OM-6850 explain it? | **No.** Media loss is PROC0/NAND-side. No WD errata or release-note entry names MI, MCTP, SMBus, VDM or the management path | **PROVEN** (absence) |

The firmware has a log string for the precise condition that must precede this:
**StrId 174, `"MI: NVMe-MI: MI_ControlPrimitiveHandler signaled, but cmd list
empty"`**, emitted from `0x7ffb2a35` — the guarded arm of the exact branch that
guards the dequeue that corrupted the list. **That string in PROC9's own log at
dump offset `0x36500` is the confirming read.**

---

## 1. PROC9's tasks

### 1.1 The scheduler, restated — PROVEN

PROC9's entry `0x7ffa38c0` calls seven module inits from the table at
`0x7ff80260` (`l32r a2,0x7ffa08d4`; `bnei a3,7` bounds the walk), then falls into
the scheduler loop at `0x7ffa3935`:

| # | init fn | what it registers |
|---|---|---|
| 0 | `0x7ffbc2e8` | — |
| 1 | `0x7ffa38a8` | 1 task |
| 2 | `0x7ffb0c10` | — |
| 3 | **`0x7ffb74d0`** | (the MI module; the array is registered by `0x7ffb7420`, immediately above it) |
| 4 | `0x7ffaa9a8` | 2 tasks |
| 5 | `0x7ffbaba8` | — |
| 6 | `0x7ffbaf58` | 1 task |

Tasks are registered by **`0x7ffbbe60(node, fn, ctx)`** — PROVEN from its body:

```asm
7ffbbe60: entry a1,0x20
7ffbbe66: s32i a3,a2,0x8      ; node->fn  = a3
7ffbbe6e: s32i.n a10,a2,0x10  ; node->rc  = 0
7ffbbe70: s32i.n a4,a2,0xc    ; node->ctx = a4
7ffbbe7c: l32i.n a8,a5,0x10   ; link onto the ready list at [0x7ff97784+0x10]
```

and the dispatcher `0x7ffbbf60` runs `[node+0x8]` with `[node+0xc]` as its single
argument (`0x7ffbbfa1..0x7ffbbfa5`). **`node+0x8` is written by no other
instruction in the image**, so a task's entry point is fixed at init and a
registration site names it statically.

`xref.py PROC9 7ffbbe60` finds **80 registrations across 38 functions**. Two
blocks dominate: **11 in `0x7ffb7420`** (NVMe-MI, below) and **11 in `0x7ffaeef8`**
(the PCIe module — `PCIe_PfailShutdown` is `0x7ffaecf0`, in the same cluster).
The remaining 58 are singletons and pairs spread across the SMBus, mailbox,
logging and housekeeping modules. The full site list is one command:
`xref.py PROC9 7ffbbe60`.

### 1.2 The NVMe-MI handler array — PROVEN

`0x7ffb7420` registers eleven tasks from an array of identical **0x348-byte**
context objects based at `0x7ff91d78`. Each object embeds its queue node at
`+0x8`, so `node = ctx + 8` and the third argument is `addi a12,a10,-8`
(FLIX slot B; the decode is unconfirmed but the observed `a2 = ctx = node − 8` in
the fault record confirms the value).

| # | ctx | node | entry fn | identity (from the string table) |
|---|---|---|---|---|
| 1 | `0x7ff91d78` | `0x7ff91d80` | **`0x7ffb2890`** | **`MI_ControlPrimitiveHandler`** (StrId 174/175/178/179) ← **faulted** |
| 2 | `0x7ff920c0` | `0x7ff920c8` | `0x7ffb5070` | `MI_ConfigurationGetCmdHandler` (201–207) |
| 3 | `0x7ff92408` | `0x7ff92410` | `0x7ffb54d8` | `MI_ConfigurationSetCmdHandler` (208–210) |
| 4 | `0x7ff92750` | `0x7ff92758` | `0x7ffb5930` | `MI_AdminCmdHandler` (212–216) — MI→NVMe admin passthrough |
| 5 | `0x7ff92a98` | `0x7ff92aa0` | `0x7ffb3408` | `MI_GetHealthStatusCmdHandler` (183–185) |
| 6 | `0x7ff92de0` | `0x7ff92de8` | `0x7ffb42e8` | unidentified — no MI strings; calls the cross-core helpers `0x7ffbda90`/`0x7ffbdae0`/`0x7ffbd808`. Probably a `*CplHandler` (the table has unassigned ones at 180/182/191) |
| 7 | `0x7ff93128` | `0x7ff93130` | `0x7ffb3e68` | `MI_VpdReadWriteCmdHandler` (191–195) |
| 8 | `0x7ff93470` | `0x7ff93478` | `0x7ffb4a78` | `MI_ReadMiDataStructureCmdHandler` (196–200) |
| 9 | `0x7ff93e48` | `0x7ff93e50` | `0x7ffb2f18` | unidentified — no MI strings |
| 10 | `0x7ff937b8` | `0x7ff937c0` | `0x7ffb6340` | `MI_PCIECmdHandler` (217–221) — PCIe config read/write over MI |
| 11 | `0x7ff93b00` | `0x7ff93b08` | `0x7ffb1330` | **the MI message router** — MIC check (3239), `"MI: Request msg routed"` (167), unhandled-type errors (169–173) |

Two more registered outside the array, by `0x7ffa39b0` / `0x7ffa39e8` (both
called from `0x7ffaa7bd`/`0x7ffaa7c0`): `ctx 0x7ff90458 → fn 0x7ffa3dc0` and
`ctx 0x7ff907b8 → fn 0x7ffa3a08`. Neither carries log strings.

Answering the question as asked: **the entire array reachable from the faulting
dispatch is MCTP / NVMe-MI**. The PCIe work is a sibling block registered by
`0x7ffaeef8`; the housekeeping is the scattered remainder. **No shutdown or PFAIL
task is in the array**, consistent with `sn200-fault-record.md` §6.

---

## 2. Which task faulted — PROVEN

`AR58 = 0x7ff91d80` is the dispatcher's `a2` at the moment of `callx8`, i.e. the
node it pulled off the ready list (`0x7ffbbf91: l32i.n a2,a4,0x2c`). `litref.py`
finds **exactly one** reference to that address in the whole image:

```
PROC9_7ff80000   7ffb7423  l32r a10,0x7ffa1b84   -> 7ff91d80
```

paired at `0x7ffb7429` with `l32r a11,0x7ffa1b88`, and `[0x7ffa1b88] = 0x7ffb2890`.
Since `node+0x8` has no other writer, **the entry point is `0x7ffb2890`**.
`AR2 = 0x7ff91d78 = node − 8` corroborates the context argument.

`0x7ffb2890` is `MI_ControlPrimitiveHandler`: it emits StrId 179
`"MI: Control primitive handled: NVMe-MI Msg Type %d: Opcode %d: returned status %d"`
at `0x7ffb29a5`, StrId 175 `"...ControlPrimitiveHandler signaled"` at `0x7ffb2a88`,
and StrId 174 `"...signaled, but cmd list empty"` at `0x7ffb2a35`.

### The window chain closes exactly — PROVEN

Every residual frame in the register file is now accounted for. `call8` advances
`WindowBase` by 2, so a callee's `a0` lands eight physical registers on:

| phys | value | means |
|---|---|---|
| `AR56` | `0xbffa3973` | dispatcher `0x7ffbbf60`, returning into scheduler `0x7ffa3970: call8 0x7ffbbf60` |
| `AR0` | `0xbffbbfa8` | **`0x7ffb2890`**, returning into `0x7ffbbfa5: callx8 a8` |
| `AR8` | `0` | its callee's `a0`, since overwritten by our own `a8 = [a9+4]` |
| `AR16` | `0xbffbaba3` | return of `0x7ffbaba0: call8 0x7ffbac20`, inside `Log_Emit` |
| `AR24` | `0xbffbacfd` | return of `0x7ffbacfa: call8 0x7ffbda90`, inside `0x7ffbac20` |
| `LBEG/LEND` | `0x7ffbdab8`/`0x7ffbdac3` | the `loopnez` at `0x7ffbdab5` inside `0x7ffbda90` |

`0x7ffbaac0` is a second entry point of the log function `0x7ffba9b8` (its own
`entry a1,0x90` at `0x7ffbaac0`). So the chain is

```
MI_ControlPrimitiveHandler 0x7ffb2890
  -> call8 0x7ffbaac0        Log_Emit
       -> call8 0x7ffbac20   log transmit
            -> call8 0x7ffbda90   cross-core write  (the LBEG/LEND loop)
```

returning all the way back before the trap. Five independent artefacts — three
return addresses, `LBEG`, `LEND` — all agree. This upgrades
`sn200-fault-record.md` §5's "structurally sound" dead frames to **exact**.

> **Tooling note.** `xref.py` printed `call{4*(n+1)}` and so labelled every
> `CALL8` as "call12", which contradicted the `CALLINC` bits `31:30` of the saved
> return addresses and briefly looked like the chain did not close. `n` *is* the
> increment ÷ 4. Fixed, with `tools/sn200-fw/tests/test_xref.py`.

---

## 3. The faulting instruction — INFERRED, tightly

Because the frame was entered by `call8`, `AR0..AR15` **are** the function's
`a0..a15`. The prologue of `0x7ffb2890`:

```asm
7ffb2890: entry a1,0x30
7ffb2893: l32i.n a15,a2,0x18                 ; coroutine resume address
7ffb2895: { movi a5,3 ; movi a6,1 ; movi a14,0 }
7ffb289d: { l32r a9,0x7ffa17f8 ; movi a12,300 ; movi a13,124 }
7ffb28a5: { l32r a11,0x7ffa17fc ; beqz a15,0x7ffb2f02 }
7ffb28ad: jx a15                             ; resume
```

`AR5 = 3`, `AR6 = 1`, `AR14 = 0` are the prologue's constants, still live. `a9`,
`a11`, `a12`, `a13` are not — so execution is past a `call8` (which clobbers
`a8..a15`), and past the resume jump.

The only code that regenerates `a9`, `a13`, `a8`, `a15` to the observed values is
the block immediately after the `call8 0x7ffbaac0` at `0x7ffb29a5`:

```asm
7ffb29a5: call8 0x7ffbaac0        ; StrId 179 "MI: Control primitive handled: ..."
7ffb29a8: l32r a13,0x7ffa1818     ; -> 0x7ff94954   g_curRequest
7ffb29ab: l32i.n a13,a13,0x0      ; a13 = the request being completed
7ffb29ad: l32r a9,0x7ffa174c      ; -> 0x7ff941d4   FIFO sentinel  == 0x7ff94140+0x94
7ffb29b0: s32i.n a9,a13,0x0       ;   node->next = &sentinel
7ffb29b2: l32i.n a8,a9,0x4        ;   a8  = sentinel.prev  (the tail)
7ffb29b4: s32i.n a8,a13,0x4       ;   node->prev = tail
7ffb29b6: l32i.n a15,a9,0x4       ;   a15 = tail   (reloaded)
7ffb29b8: s32i.n a13,a15,0x0      ;   tail->next = node        <== FAULT
7ffb29ba: s32i.n a13,a9,0x4       ;   sentinel.prev = node
```

Match, register by register:

| reg | observed | produced by |
|---|---|---|
| `a13` | `0x7ff941d4` | `l32i.n a13,a13,0x0` at `0x7ffb29ab` |
| `a9` | `0x7ff941d4` | `l32r a9,0x7ffa174c` at `0x7ffb29ad` |
| `a8` | `0` | `l32i.n a8,a9,0x4` at `0x7ffb29b2` |
| `a15` | `0` | `l32i.n a15,a9,0x4` at `0x7ffb29b6` |
| `a5,a6,a14` | `3,1,0` | prologue, never clobbered on this path |
| `a2` | `0x7ff91d78` | the ctx argument, preserved across `call8` |
| `a10,a11,a12` | `0`,`0x7ff9ff60`,`0x24` | residue of the `Log_Emit` call |
| `a3,a4,a7` | `0xffffc001`,`0x7ff977a8`,`4` | inherited from the dispatcher's `a11..a15` (`call8` passes `a10..a15` → `a2..a7`); not set by this function |

Of the four stores in the block, `0x7ffb29b0`, `0x7ffb29b4` and `0x7ffb29ba` all
address `0x7ff941d4`/`0x7ff941d8` — valid RAM. **Only `0x7ffb29b8` dereferences
`a15`, and `a15 = 0`.** The two loads at `0x7ffb29b2`/`0x7ffb29b6` read
`0x7ff941d8`, also valid.

> **`EPC1 = 0x7ffb29b8`. `EXCCAUSE = 3` (LoadStoreError). `EXCVADDR = 0`.**
> A null-pointer store. `3 ∉ {4, 5, 15}`, so it routes through the `EXCCAUSE`
> table at `0x7ff81d38` to `0x7ffa0528` → `movi.n a0,6` → **vector index 6**,
> which is precisely what the record holds. Consistent, not assumed.

This is the first reconstruction of `EPC1` for this dump. It is an inference from
register contents, not a read of `EPC1` — which remains **PROVEN absent** from
the record.

---

## 4. The corrupt structure — PROVEN

`0x7ff94140` is the NVMe-MI module's global state (the router `0x7ffb1330` loads
it at `0x7ffb1333` as `a5`). At `+0x94`/`+0x98` it embeds a circular
doubly-linked FIFO whose **queue struct is itself the sentinel node**:

```
0x7ff941d4  = 0x7ff94140+0x94  = sentinel.next   (head)
0x7ff941d8  = 0x7ff94140+0x98  = sentinel.prev   (tail)
```

Both spellings appear in the code and resolve to the same words — the handlers
use the absolute `0x7ff941d4` literal, the router uses `[a5+0x98]`.

`0x7ffb6f50` (the MI init, `s32i a10,a10,0x0` / `s32i a10,a10,0x4` at
`0x7ffb6f5c`/`0x7ffb6f5f`) self-links it, the correct empty state. The individual
handler contexts are self-linked the same way at `0x7ffb7064`.

**Eighteen sites** across all eleven handlers perform the identical tail insert
(`litref.py -v 7ff941d4 PROC9`): `0x7ffb142a`, `0x7ffb1a58`, `0x7ffb1b58`,
`0x7ffb1d78`, `0x7ffb237d`, `0x7ffb2408`, `0x7ffb250a`, `0x7ffb29ad`,
`0x7ffb3670`, `0x7ffb3ff5`, `0x7ffb45d8`, `0x7ffb4c4c`, `0x7ffb520d`,
`0x7ffb5675`, `0x7ffb5eb0`, `0x7ffb67f2` (+ the two init sites). **None of them
null-checks the tail** — correctly, since a sentinel-terminated circular list
cannot have a null tail while its invariant holds.

### Two observations, one cause

The register file proves two things simultaneously:

1. `[0x7ff941d8] = 0` — **the FIFO's tail pointer is NULL**.
2. `[0x7ff94954] = 0x7ff941d4` — **`g_curRequest` points at the FIFO's own
   sentinel**, not at a request object.

Both fall out of one event. The dequeue arm of this same function:

```asm
7ffb2a2f: <boolean-producing insn, not decoded>      ; "is my cmd list non-empty?"
7ffb2a32: bf b0,0x7ffb2a88                            ; -> "signaled"; else fall through
7ffb2a35: { l32r a11,0x7ffa1828 ; movi a10,19 }       ; StrId 174 "signaled, but cmd list empty"
7ffb2a3d: call8 0x7ffbaac0
7ffb2a40: j 0x7ffb29bc                                ; skip the dequeue
...
7ffb2a88: { l32r a11,0x7ffa182c ; movi a10,19 }       ; StrId 175 "signaled"
7ffb2a90: call8 0x7ffbaac0
7ffb2a93: l32i.n a9,a2,0x0        ; node = ctx.next          <-- unguarded pop
7ffb2a95: l32r a13,0x7ffa1818     ; -> 0x7ff94954
7ffb2a98: l32i.n a10,a9,0x4       ; prev
7ffb2a9a: l32i.n a12,a9,0x0       ; next
7ffb2a9c: s32i.n a9,a13,0x0       ; g_curRequest = node
7ffb2a9e: s32i.n a12,a10,0x0      ; prev->next = next
7ffb2aa0: s32i a10,a12,0x4        ; next->prev = prev
7ffb2ab0: s32i.n a8,a9,0x4        ; node->prev = 0      <-- zeroes the links
7ffb2ab2: s32i a8,a9,0x0          ; node->next = 0
```

If `node` is the FIFO sentinel `0x7ff941d4`, this sets `g_curRequest = 0x7ff941d4`
and zeroes `sentinel.next` and `sentinel.prev` — **exactly the two-part state
observed**. The next time the task reaches the completion insert at `0x7ffb29a8`
it reloads `g_curRequest` (still the sentinel), reads `sentinel.prev = 0`, and
stores through it.

**INFERRED, high confidence: the sentinel of the MI completion FIFO was dequeued
and processed as if it were a request. The trap is the delayed consequence, one
dispatch later.**

### What is not determined

**How the sentinel got onto `MI_ControlPrimitiveHandler`'s inbound list.** Two
things block it:

- **PROC9 never dequeues the `0x7ff941d4` FIFO.** All eighteen references insert;
  a full disassembly of PROC9's text (`0x7ffa0000`–`0x7ffbe210`) contains no read
  of `sentinel.next` other than the sentinel-terminator writes. Its consumer is
  therefore **off-core** — which puts an unsynchronised cross-core
  producer/consumer squarely in frame, and the cross-core write helper
  `0x7ffbda90` is literally in the fault's call chain. **SPECULATIVE.**
- **The emptiness guard at `0x7ffb2a2f` is not decodable.** It produces boolean
  `b0` and is tested by `bf b0`; the same idiom drives the scheduler's queue
  selection at `0x7ffa393d`/`0x7ffa3945`/`0x7ffa3950`/`0x7ffa3960` and appears
  **~150 times across PROC9's text**. `xdis.py` renders it `rt0 op2=6`, i.e. it
  does not decode: the encoding (`op0=0, op1=0, op2=6`) collides with base-ISA
  `ADDX8`, which cannot be what feeds a `bf`/`bt` — so it is almost certainly a
  **custom TIE instruction**. What matters is structural and does not need
  the decode: **the emptiness decision and the dequeue read different sources of
  truth** — a hardware/boolean signal versus the software list head at `[ctx+0]`.
  A signal delivered without (or before) the node being linked passes the guard
  and pops whatever `[ctx+0]` holds. That is precisely the condition StrId 174
  exists to report.

Anyone naming the producer from this artefact is guessing.

---

## 5. Is the BMC driving it?

The subsystem is entirely inbound out-of-band management: NVMe-MI over MCTP over
SMBus and PCIe VDM, i.e. **the chassis BMC, outside host control**. That much is
structural and unchanged from `sn200-fault-record.md` §6.

But the attractive version of the story — *a malformed MI message walks off the
end of something* — **is not supported by anything found here**, and the task
brief was right to warn against it:

- Malformed input **is** validated, and loudly: StrId 3239 MIC mismatch
  (`0x7ffb2408`), StrId 215 admin parameter error, StrId 199 invalid data
  structure type, StrId 204/206/207 invalid configuration params, StrId 171–173
  unhandled opcode / message type.
- Every one of those error paths **returns the request through the same tail
  insert** as the success path (`0x7ffb1a58`, `0x7ffb2408`, `0x7ffb237d`,
  `0x7ffb250a`, `0x7ffb1b58`, `0x7ffb1d78` are all abort/error arms). So a
  malformed message is a *queue-traffic* stressor, not a distinct memory-safety
  path. It could plausibly widen a race window; nothing shows it creating one.
- The fault is a **list-invariant** failure, not a bounds or parsing failure. No
  attacker-controlled value appears anywhere in the faulting frame: `a15 = 0` is
  a load from a fixed global.

**Verdict: the BMC is the only thing that makes this code run at all, so BMC
polling is a necessary condition and a legitimate mitigation lever. It is not
shown to be a sufficient one, and the defect may be entirely internal to PROC9's
queue handling.** Grade: **SPECULATIVE**, deliberately.

Two adjacent strings are worth noting because they describe queue-state
anomalies WD anticipated, all in this same module:

- StrId 168 `"MI: Command sent to non-idle slot for EP %d Slot %d"` — slot reuse
- StrId 189/190 `"MI: Command timeout handler called"` /
  `"MI: no valid request msg pointers found in timeout handler"` — a timeout
  handler that force-completes requests, i.e. a second path that can return a
  node to the FIFO. A double-return is a textbook way to produce exactly the
  observed state.
- StrId 166 `"MI: FATAL ERROR: Unable to get context for handler"`, StrId 165
  `"MI: NULL register entry"`

None of these is evidence on its own. All four are things PROC9's log would say.

---

## 6. Cross-check against WD's errata — PROVEN (absence)

OM-6850's stated root cause is *"With back-to-back PFails, PFails that occur in
the middle of a 200 ms power-on window may cause small loss of usable media. Over
time, this leads to a crash."*

**There is no PROC9 path consistent with that.** Media loss, L2P/journal
corruption and garbage collection are PROC0/NAND-side. PROC9 holds no L2P, does
no media I/O, and its faulting task touches nothing but an in-RAM message queue.
The errata points elsewhere.

Re-reading the release-note and errata material collected in
`sn200-firmware-re.md` §8 for the management path: **OM-6850, OM-7044, OM-6836,
OM-6588 and OM-6697 are all deallocate / reset / PFail / SGL host-IO issues.
Not one entry names MI, MCTP, SMBus, VDM, or out-of-band management.** Nor does
`KNGND100_SN2xx_Errata.pdf`. The one PROC9-adjacent artefact in that section is
`UNEXSTRT`, introduced *alongside* the OM-6850 fix — and
`sn200-fault-record.md` already proved this dump is **not** an `UNEXSTRT` stub.

So: **the decoded fault is a defect WD has not published.** That does not make it
new — the release notes are a filtered list — but it does mean the adopted
"crashed drive = OM-6850" story does not cover this drive.

This strengthens, not weakens, `sn200-fault-record.md` §6: whatever latched this
drive, it was not the documented media-loss mechanism and not an unfinished
shutdown.

---

## 7. What would confirm it

In value order. Everything here is static-analysis-only; **no hardware action was
taken and none is proposed against the production drive without the usual review.**

1. **PROC9's own log blocks at dump offset `0x36500`.** Now reachable —
   `tools/nvme-noreset/` gained `max_admin_xfer_ids`, which lifts the 128 KiB
   host cap. Cores 0–3 are at `0x12500 + core*0x4000`; core 9 is `0x36500`.
   What to grep for, in descending decisiveness:

   | StrId | text | what it would mean |
   |---|---|---|
   | **174** | `MI_ControlPrimitiveHandler signaled, but cmd list empty` | **Near-decisive.** The guard at `0x7ffb2a2f` is firing, i.e. dispatch and queue state disagree — the exact precondition of §4 |
   | 175 | `MI_ControlPrimitiveHandler signaled` | the immediately preceding dispatch; pairs with 179 to bracket the crash |
   | 179 | `MI: Control primitive handled: ... returned status %d` | the **last** log line before the trap, emitted from `0x7ffb29a5`, three instructions before `EPC1` |
   | 189/190 | timeout handler called / no valid request msg pointers | a second returner of nodes to the FIFO — supports double-return |
   | 168 | `Command sent to non-idle slot` | slot reuse, an independent queue-state anomaly |
   | 3239 | MIC error | inbound traffic was malformed around the event; supports (does not prove) BMC involvement |

   **A log ending in 179 with a 174 shortly before it confirms the whole chain.**
   A log with none of these refutes §4 and sends this back to the drawing board.

2. **The crash-context struct at `0x7ff83150`.** It holds the real `EPC1`
   (`+0x70`), `EXCCAUSE` (`+0xc8`) and `EXCVADDR` (`+0xcc`), written via
   `call8 0x7ffa2838`. It is **not** in the first 128 KiB. If any section dumps
   it, `EPC1 == 0x7ffb29b8` / `EXCCAUSE == 3` / `EXCVADDR == 0` settles §3
   outright. Unchanged priority from `sn200-fault-record.md` §7.4.

3. **Decode the boolean-producing instruction at `0x7ffb2a2f`.** `xdis.py` gives
   `rt0 op2=6`; the same encoding drives the scheduler's queue selection, so it
   is high-value beyond this bug. It is very likely a custom TIE opcode, which
   means the ISA tables in `docs/sn200-xtensa-isa.md` are incomplete for PROC9.
   Resolving it would say whether the guard *can* disagree with `[ctx+0]`.

4. **Find the FIFO's consumer.** It is not in PROC9. Sweeping the other 17 images
   for reads of `0x7ff941d4`/`0x7ff941d8` — remembering addresses are self-aliased
   per core, so a hit is only meaningful with the shared-window map in
   `docs/sn200-memory-map.md` — would identify the other party and decide the
   cross-core-race hypothesis.

5. **Read `+0x00`/`+0x40` on the other four drives.** Unchanged, still not done,
   still the cheap fleet-wide test. It now has a sharper question attached: if
   another drive's `.CDI` is also `0x49444309` at file offset `0x11000`, PROC9 is
   the fleet-wide mechanism.

6. **BMC NVMe-MI polling vs. the latched/survived split.** Still worth
   correlating (`sn200-certainty.md` line 34), now with the caveat of §5: a
   correlation would be suggestive, its absence would not clear the BMC, since
   *any* MI traffic exercises this queue.

---

## 8. Revised grades

| Claim | Was | Now |
|---|---|---|
| Faulting core is PROC9 | PROVEN | **PROVEN** (unchanged) |
| The task's identity is not statically recoverable | stated in `sn200-fault-record.md` §5 | **superseded** — `node+0x8` has a single writer; it is `0x7ffb2890` |
| The dead frames are Log_Emit → transmit → cross-core write | INFERRED ("structurally sound") | **PROVEN** — three return addresses plus `LBEG`/`LEND` all agree |
| `EPC1` is in the record | — | **PROVEN ABSENT** (unchanged); **reconstructed as `0x7ffb29b8`**, INFERRED |
| `EXCCAUSE` | `∉ {4,5,15}`, PROVEN | **inferred to be 3** (LoadStoreError), consistent with vector index 6 |
| The fault is a null-pointer store on a corrupt list | — | **INFERRED**, from four independently-matching register values |
| A malformed BMC message is the trigger | attractive candidate | **not supported** — the error paths are validated and share the enqueue. Still SPECULATIVE, now with the reasons written down |
| OM-6850 explains these crashes | INFERRED in `sn200-fault-record.md` §6 | **does not cover this drive** — no PROC9 path matches media loss, and no errata entry names the management path |
| The producer that corrupted the FIFO | — | **unknown, and not guessable from this dump** |
