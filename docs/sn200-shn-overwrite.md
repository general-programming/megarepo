# Can a host `CC.SHN` overwrite the pending re-init marker? — **No, twice over**

Firmware `KNGND122`. **No hardware was touched.** Static analysis plus p-code
lifting (`tools/sn200-fw/pcode.py`).

This closes the single open lead of `docs/sn200-nondestructive-clear.md` §4: the
idea that `0xFF`/`0x0503` schedules the wipe only as a *record value*, and that a
host shutdown — a PCIe register write, not an admin command, so the Post-Crash
allow-list never sees it — could reach `PROC6 0x7ffbba61` and overwrite that
record with marker 1/2 before the next boot.

**Both gating questions come back negative, independently, and each on its own
is sufficient to kill the lead.**

| question | answer |
|---|---|
| 1 — does a host `CC.SHN` produce shutdown-request **type 3**? | **No. PROVEN.** It produces type **1** or **7**. Type 3 is produced by exactly one thing in the firmware: `PCIe_PfailShutdown`. So `0x7ffa8ee8` never fires on the host path, `0x7ff8c7c4` stays 0, and `0x7ffa8c22` skips `SYS: ShutdownReq --> SAM` entirely. |
| 1b — is there another way in? | **A second writer of `0x7ff8c7c4` exists** (missed by every prior pass) — and it is explicitly **skipped when the startup type is 3 or 6**. A Post-Crash boot *is* startup type 6. **PROVEN** store + guard. |
| 2 — is the System-Area save safe when the SA was never read? | **No. It would blank it.** On startup type 6 SAM logs *"SAM: Unsupported startup type"* and returns without reading the System Area; the in-RAM SA image at `0x7ff8bbd0` is **BSS** — not in any of PROC6's loaded segments — and no "was it loaded" guard exists on the save. **PROVEN** for the not-read and BSS halves. |

Labels: **PROVEN** = read off correctly-lifted instructions or produced by
executing them. **INFERRED** = short chain over proven facts. **SPECULATIVE** =
neither.

`docs/sn200-runbook.md` is **unchanged**. There is no candidate procedure to
write, tested or untested.

---

## 0. A decoder bug the answer turns on — found, fixed, pinned

Before anything else, because the whole of §2 rests on one instruction.

`flix.sinc`'s slot-B `addi` read the immediate from **bits 32-35 only**. The real
immediate is **eight bits split across two non-adjacent nibbles**:

```
imm8 = (bits 40-43) << 4 | (bits 32-35)     signed
```

`docs/sn200-shutdown-path.md` §6a.6 had already derived this by hand; the SLEIGH
spec never got it. The consequence is silent and severe: every multiple of 16
lifts as **0**, and every small negative lifts as a large positive — so a
`struct` base gets re-pointed and every field offset read off it is wrong.

The fix is anchored **outside the decoder**, which is why it is trustworthy:
PROC11 `0x7ffa2d52` must produce the list sentinel that PROC11 `0x7ffa08b4`
holds as a literal.

```
[0x7ffa08b4] = 0x7ff80910          a7 = 0x7ff80900   =>  imm must be 16
before:  addi a11,a7,0x0
after:   addi a11,a7,0x10          ✔
0x7ffa2142:  addi a4,a4,0x10       ✔  (loop stride, 16-byte records)
0x7ffa2ed6:  addi a14,a14,-0x1     ✔  (the sign case)
```

`tools/sn200-fw/ghidra/languages/flix.sinc` +
`tests/test_pcode.py::test_slot_b_addi_immediate_spans_two_nibbles`.
Full suite: **249 passed** (248 before, +1 new). `sn200_oracle.py` and
`tests/test_oracle.py` were **not** touched.

The instruction this changes:

```asm
7ffa8e83: { s32i a9,a11,0x50 ; addi a2,a11,0x14 ; mov a11,a9 }
                                          ^^^^ was lifted as 0x4
```

`a11 = 0x7ff8ce20`, so `a2 = 0x7ff8ce34` and **`[a2+0x3c]` is `[obj+0x50]` — the
word this very bundle just stored from `[msg+0x8]`.** Under the old decode `a2`
would have been `obj+4` and `[a2+0x3c]` a never-written word, which would have
made the whole shutdown-type test unreadable. §2 depends on this.

---

## 1. The shutdown request, end to end

### 1.1 There is exactly one producer of a shutdown-request message — PROVEN

`logscan.py 'SendShutdown'` returns one sender in 20 images: **PROC9
`0x7ffaeba0`**, `PCIe_SendShutdownReq --> Mgr 0x%x (%u, %u)` (StrId 3579). It
builds the message on its own stack frame:

```asm
7ffaeba0: entry a1,0x40
7ffaeba3: s32i.n a4,a1,0x8         ; msg+0x08 = TYPE      <- caller's a12
7ffaeba5: s32i.n a5,a1,0x10        ; msg+0x10 = STATUS    <- caller's a13
7ffaebb5: { s8i a15,a1,0xc ; movi a13,254 ; movi a14,0 }  ; msg+0x0c = port
7ffaebc0: { s32i a13,a1,0x4 ; ... }                       ; msg+0x04 = MSGID 254
7ffaebd1: { s32i a8,a1,0x0 ; ... }                        ; msg+0x00 = 0x80000000
7ffaebd9: call8 0x7ffbda90                                ; IPC send
```

PROC0 re-broadcasts the same request to the remaining managers at `0x7ffa8c84`
(`movi a12,254` → `s32i a12,a1,0x4`, and `s32i a8,a1,0x8` with
`a8 = [a2+0x3c]`), i.e. **the type is forwarded verbatim**. No other site in any
image constructs a MSGID-254 message.

So the message layout is fixed and the *only* free variable is `PCIe_SendShutdownReq`'s
`a4`.

### 1.2 The two consumers agree on the field — PROVEN

**PROC0 `0x7ffa8e64`** (`SYS: Shutdown Request Received: %d Status %d`,
StrId 3530) copies `[msg+0x8]` into its own context and then gates on it:

```asm
7ffa8e7c: l32i.n a9,a4,0x8                  ; msg+0x08 = type
7ffa8e81: l32i.n a12,a4,0x10                ; msg+0x10 = status
7ffa8e83: { s32i a9,a11,0x50 ; addi a2,a11,0x14 ; mov a11,a9 }
7ffa8eb4: l32i.n a13,a2,0x3c                ; == [obj+0x50] == the type
7ffa8ed8: { s32i a14,a2,0x40 ; bnei a13,3,0x7ffa8eea }
7ffa8ee0: { l32r a13,0x7ff82b20 ; beqi a12,2,0x7ffa8eea }
7ffa8ee8: s32i.n a3,a13,0x0                 ; [0x7ff8c7c4] = 1
```

Lifted, not read (`pcode.py --pcode`): `bnei a13,0x3` and `beqi a12,0x2` are the
spec's own output, and the store target is `ram[7ff82b20]` → `0x7ff8c7c4`. This
reproduces `sn200-shutdown-path.md` §1.3 exactly, and now with the *source* of
both operands identified: they are message fields, not context fields.

**PROC8 `0x7ffb0536`** independently confirms the semantics of the value:

```asm
7ffb0547: { addi a10,a6,68 ; mov a11,a4 ; movi a12,20 }
7ffb054f: call8 0x7ffba990                  ; copy 20 bytes of msg into the port slot
7ffb055b: l32i a9,a6,0x4c                   ; == msg+0x08
7ffb055e: bnei a9,3,0x7ffb0571              ; 3  -> [0x7ff88018] = 3, StrId 2054 "PFail Shutdown"
7ffb0571: beqi a9,7,0x7ffb057b              ; 7  -> leave the mode word alone
7ffb0574: [0x7ff88018] = 2                  ; else -> normal shutdown
```

**Type 3 means PFAIL.** That is the firmware's own word for it.

### 1.3 Every producer of the type value — PROVEN, by construction

Three call sites reach `PCIe_SendShutdownReq` with a constant, two with a
context field. `xref.py PROC9 7ffaeba0` gives all five:

| site | thread | type passed |
|---|---|---|
| `0x7ffae708` | `0x7ffae6a8` | **6** (`movi.n a12,6`) — the first phase |
| `0x7ffaeda1` | `0x7ffaed70` `PCIe_OnePort_ShutdownThread` | **7** (`movi.n a12,7`) |
| `0x7ffae8a0`, `0x7ffae940`, `0x7ffaea58` | both | `[portctx+0x20]` |

`portctx` is `0x7ff81bd0 + port*156` (`0x7ffa13c8`; stride confirmed by the
`Shutdown status Port0 … Port1 …` log reading `+0x20`/`+0x1c` and
`+0xbc`/`+0xb8`). `+0x20` is the port's **Type**, and it has exactly three
writers:

```asm
7ffae689: s32i.n a4,a5,0x20      ; PCIe_RequestShutdown  -- its 3rd argument
7ffaec6d: s32i a11,a12,0x20      ; PCIe_DualPort_RequestShutdown, "set port %d to type %d"
7ffaec95: s32i a13,a12,0x20      ; PCIe_DualPort_RequestShutdown, movi a13,7
```

and their callers close the loop:

| caller | callee | type |
|---|---|---|
| `PCIe_PfailShutdown` `0x7ffaed40` / `0x7ffaed50` / `0x7ffaed68` | `PCIe_RequestShutdown` | **3** |
| `PCIe_DualPort_RequestShutdown` `0x7ffaec80` | `PCIe_RequestShutdown` | **1** |
| `0x7ffab65b` — **the host `CC.SHN` handler** | `PCIe_DualPort_RequestShutdown` | **7** |
| `0x7ffa9e60` | `PCIe_DualPort_RequestShutdown` | 7 |
| `0x7ffac5d0`, `0x7ffad2f0` (reset / D3hot) | `PCIe_DualPort_RequestShutdown` | 5 |

The resulting enum — 1 normal per-port, 3 PFail, 5 reset/D3, 6 first phase,
7 final controller shutdown — is corroborated by PROC8's three-way test in §1.2
and by PROC0's own `beqi a12,6` FAST arm (`sn200-shutdown-path.md` §4.1).

### 1.4 The host path, named exactly — PROVEN

`CC.SHN controller shutdown port %d type 0x%x` (StrId 696) is PROC9
`0x7ffab648`, and the next instruction is the call:

```asm
7ffab648: { l32r a10,0x7ffa1170 ; mov a11,a4 }     ; StrId 696, port
7ffab650: call8 0x7ffba9d8                          ; log
7ffab653: { l32i a11,a1,0x8 ; mov a10,a4 ; movi a12,0x7 }
7ffab65b: call8 0x7ffaec00                          ; PCIe_DualPort_RequestShutdown(port, x, 7)
```

`PCIe_DualPort_RequestShutdown` then branches on the *other* port's state:

- other port already shut down → `0x7ffaec85`: `portctx+0x20 = 7`, state = 2,
  schedule `PCIe_OnePort_ShutdownThread` → sends type **7**;
- otherwise → `0x7ffaec6a`: log *"action: set port %d to type %d"*, then
  `PCIe_RequestShutdown(port, x, 1)` → `portctx+0x20 = 1` → thread `0x7ffae6a8`
  sends type **6** then type **1**.

Which arm a single-port SN200 takes was not determined and **does not matter**:
neither is 3.

> **Answer to question 1.** A host `CC.SHN` produces shutdown-request type
> **1** or **7**, never 3. `PROC0 0x7ffa8ee8` does not fire, `0x7ff8c7c4` stays
> 0, and `PROC0 0x7ffa8c22` (`beqz a15, 0x7ffa8bb2`) jumps straight to
> *"SYS: Returning shutdown completion"* — **`SYS: ShutdownReq --> SAM` is never
> logged and `PROC6 0x7ffbba61` is never reached.** No marker 1/2 is written.
> **PROVEN.**

---

## 2. The second writer of `0x7ff8c7c4` — new, and it is guarded on exactly the
## boot we are stuck in

Every prior document records **two** runtime writers of `0x7ff8c7c4`
(`0x7ffa8ee8` set, `0x7ffa8bc0` clear). `litref.py -v 7ff8c7c4` returns **six**
`l32r` sites, and two of them were never examined:

```
PROC0  7ffa4169   7ffa8399   7ffa8bba   7ffa8c1d   7ffa8ee0   7ffab8f4
```

- **`0x7ffab8f4`** — `s32i.n a11,a10,0x0` with `a11 = [a1+0x10]`, inside boot
  init immediately before `0x7ffab917` (`SYS: Enable PFAIL monitoring`). Boot
  initialisation, not a runtime path.
- **`0x7ffa4169`** — loads the pointer into `a11` and then dispatches through a
  jump table (`[a2+0x44]`, bound 45, table at `0x7ff82b24` → `0x7ffa4184`)
  inside the PROC0 system task `0x7ffa3e48`. **Table entry 3** stores through it:

```asm
7ffa469e: l32i a9,a2,0x48
7ffa46a1: bnei a9,6,0x7ffa46cd            ; request code must be 6
7ffa46a4: l32r a10,0x7ff82b9c             ; -> 0x7ff8c788
7ffa46a7: l32i.n a10,a10,0x30             ; the PROC0 STARTUP TYPE
7ffa46a9: beqi a10,3,0x7ffa46b1           ; type 3 (READ ONLY) -> skip
7ffa46ac: beqi a10,6,0x7ffa46b1           ; type 6             -> skip
7ffa46af: s32i.n a5,a11,0x0               ; [0x7ff8c7c4] = a5
```

`a5` is set to 1 at the function's entry (`0x7ffa3e5b`, `movi a5,1`); that it is
still 1 at `0x7ffa46af` is **INFERRED** (the function is large and `a5` is
rewritten on other paths). The *store* and the *guard* are **PROVEN**.

And the guard names our exact situation. PROC0's boot marker dispatch:

```asm
7ffaac82: { movi a11,1273 ; movi a5,6 }   ; StrId 1273 "SYS: Post Crash startup"
7ffaac8a: l32r a12,0x7ff82b9c             ; -> 0x7ff8c788
7ffaac90: s32i.n a5,a12,0x30              ; startup type := 6
```

**A Post-Crash boot is startup type 6.** So even granting the most favourable
reading of `a5`, this second door is shut on precisely the drives that need it —
and on read-only startups (type 3) too.

> **PROVEN: on a latched drive there is no writer of `0x7ff8c7c4` at all.**
> The SA-saving shutdown flag cannot be raised, by the host or by anything else,
> on a Post-Crash boot.

---

## 3. Question 2 — the save would blank the System Area

This is the one the brief called the real hazard, and it is a hazard.

### 3.1 On startup type 6, SAM never reads the System Area — PROVEN

PROC6 `0x7ffba898` is the SAM startup state machine. It dispatches on
`a12 = [ctx+0x30]`, the startup type:

```asm
7ffba938: { addi a14,a6,104 ; beqz a12,0x7ffbaa00 }   ; 0 FIRST
7ffba940: { extw ; beqi a12,3,0x7ffba9dc }            ; 3 READ ONLY
7ffba948: { extw ; beqi a12,1,0x7ffba9e5 }            ; 1 NORMAL
7ffba950: { extw ; beqi a12,2,0x7ffbaa18 }            ; 2 RECOVERY
7ffba958: { l32r a11,0x7ffa1f88 ; beqi a12,5,0x7ffba978 }
7ffba960: beqi a12,4,0x7ffba978
7ffba963: s32i.n a15,a2,0x34                          ; = 1, error status
7ffba965: { l32r a11,0x7ffa2118 ; movi a10,22 }       ; StrId 2317
7ffba96d: call8 0x7ffbc820
7ffba970: j 0x7ffba8a8                                ; completion tail
```

`[0x7ffa2118]` decodes to **StrId 2317 — `SAM: Unsupported startup type [%d]`**.
There is no arm for **6**, so a Post-Crash boot takes the default: it sets an
error status and completes. **No System-Area read happens.** This is the
executable form of `sn200-readonly-startup.md` §2's claim, and it is now the
firmware's own diagnostic string rather than an inference.

### 3.2 The in-RAM System-Area image is BSS — PROVEN

The image PROC6 stamps the marker into is based at `0x7ff8bbd0`
(`0x7ffbba58: l32r a14,0x7ffa2278`; marker at `+0x3c` = `0x7ff8bc0c`).
`segparse.py` on `PROC6.bin`:

```
load=0x7ff80000-0x7ff810ec
load=0x7ffa0000-0x7ffa0184   0x7ffa019c-0x7ffa01a4   0x7ffa01bc-0x7ffa01c4
load=0x7ffa01d8-0x7ffa02a0   0x7ffa0400-0x7ffa068c   0x7ffa0710-0x7ffbfc6c
```

`0x7ff8bbd0` **falls in no loaded segment.** It is BSS: zero at reset, and the
only thing that fills it is the System-Area read that §3.1 proves does not
happen on a type-6 boot.

### 3.3 No guard, and the marker store is unconditional — PROVEN store,
### INFERRED absence

The SAM shutdown state machine is PROC6 `0x7ffbb8d8` (zero `callN` sites — a
bound coroutine). It walks the section table, and its marker step is:

```asm
7ffbba52: l32i a8,a2,0x68            ; SAM's own 2-valued type: 1 PFail, 2 clean
7ffbba5b: addi a8,a8,-2
7ffbba5e: moveqz a13,a15,a8
7ffbba61: s32i.n a13,a14,0x3c        ; -> 0x7ff8bc0c, in the BSS image
```

Nothing between the state machine's `entry` and this store tests whether the SA
was ever loaded. I did not find a guard; I did not walk the section-by-section
serialiser far enough to prove it writes *all* sections rather than only dirty
ones, so "it writes the whole directory" is **not** claimed here.

It does not need to be. The marker is the point of the whole lead, the marker
lives in that image, and that image is zeros.

> **Answer to question 2.** If the System-Area save were forced to run on a
> Post-Crash boot, it would serialise an image that was never populated. Marker
> 1/2 would be written into a blank record. **This would not rescue the drive,
> it would destroy the good copy.** Decisive negative — do not soften it, and do
> not go looking for a way to force `0x7ff8c7c4` from a debug console "just to
> try it", because §2 and §3 together mean the forced version is *worse* than
> doing nothing.

---

## 4. What this closes, and what it leaves

**Closed.** The `CC.SHN` marker-overwrite lead in
`sn200-nondestructive-clear.md` §4 is dead at both gates. It is the sixth lead
to die here (marker 8, `Admin_VucFlashRead`, host-side LOAD_N_GO, the `0x0603`
branch, the host-reachable marker write, and now this), and unlike some of the
others it is dead for two unrelated reasons, either of which suffices.

The reframing that produced it — that verb `0x25` destroys nothing at runtime
and the wipe is a four-byte record value actioned on the next boot — **still
stands** (`sn200-nondestructive-clear.md` §3.1). What is now proven is that no
host-reachable action rewrites that record later in the same power-on. The
record is overwritable in principle and unreachable in practice.

**Incidentally settled, and useful elsewhere.** `sn200-shutdown-path.md` §6
item 6 asks about StrId 3518 *"SYS: Stopping SAM startup because PFAIL is in
progress"* and why a latched drive stays latched across power cycles. §1.4 and
§2 give the mechanism directly: on a Post-Crash boot the SA-save flag cannot be
raised, so **no shutdown of any kind — clean, host-issued, or otherwise — can
write a finishing marker.** A latched drive cannot un-latch itself by being shut
down properly. That is worth carrying into the operational advice: `nvme
shutdown` on an already-latched drive is neither helpful nor harmful, it is
inert.

**Still open, and unaffected by this pass.** `0xC6`/`0x30` (admitted by the
gate, handler unwalked), `0xE6`, `0xEC`, and the five allow-listed `0xCA`
UNKNOWNs. None of them can reach section 11 (`sn200-nondestructive-clear.md`
§1.2 is opcode-independent), but that bounds the *EEPROM* surface, not the
marker one.

**Not attempted, deliberately.** Forcing `0x7ff8c7c4` or the SAM save from the
`DiagMgr>` UART. §3 says the result would be a blanked System Area. If anyone
ever gets code execution on PROC0, the thing to write is marker **8** directly
(`sn200-readonly-startup.md` §6.0a) — not to drive the shutdown path.

---

## 5. Scoreboard

| claim | label |
|---|---|
| `flix.sinc` slot-B `addi` immediate is 8 bits split across bits 40-43 and 32-35, signed | PROVEN (anchored on `[0x7ffa08b4] = 0x7ff80910`), fixed, tested |
| PROC0 `0x7ffa8e83` is `addi a2,a11,0x14`; `[a2+0x3c]` is the type word from `[msg+0x8]` | PROVEN |
| `PCIe_SendShutdownReq` PROC9 `0x7ffaeba0` is the only producer of a MSGID-254 shutdown request | PROVEN |
| Message layout: `+0x04` MSGID, `+0x08` type, `+0x0c` port, `+0x10` status | PROVEN |
| Type 3 = PFAIL, per PROC8 `0x7ffb055e` → StrId 2054 | PROVEN |
| Type reaches `SendShutdownReq` from 3 constants (6, 7) and `[portctx+0x20]`; `+0x20` has exactly 3 writers | PROVEN |
| Type **3** is produced only by `PCIe_PfailShutdown` `0x7ffaed40`/`50`/`68` | PROVEN |
| Host `CC.SHN` (`0x7ffab65b`) requests type 7 → sends type 1 or 7, never 3 | PROVEN |
| Therefore `0x7ff8c7c4` is not set by a host shutdown, and the SAM save is skipped | PROVEN |
| `0x7ff8c7c4` has a **third** writer, PROC0 `0x7ffa46af`, previously unrecorded | PROVEN (store + guard); value `a5 = 1` INFERRED |
| That writer is skipped when the startup type is 3 or 6 | PROVEN |
| A Post-Crash boot is startup type 6 (`0x7ffaac82`, StrId 1273) | PROVEN |
| SAM has no startup arm for type 6; it logs StrId 2317 and never reads the SA | PROVEN |
| The in-RAM SA image `0x7ff8bbd0` is BSS in PROC6 | PROVEN (segment table) |
| No "SA was loaded" guard on the SAM save; the marker store is unconditional | PROVEN store, INFERRED absence of a guard |
| Forcing the save on a type-6 boot would write a blank System Area | INFERRED (short chain over the three rows above) |
| The whole `CC.SHN` marker-overwrite lead | **DEAD** |
