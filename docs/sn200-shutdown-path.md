# SN200 — why the shutdown fails to complete

Target: HGST/WDC Ultrastar SN200 `HUSMR7676BDP3Y1`, firmware `KNGND122` (terminal
revision), Tensilica Xtensa, 18 processor images.

Scope: this document answers **why the shutdown does not finish**. The
*consequence* chain (markers 5/6/7 → `UNEXSTRT` stub → forced `0x80000009` on every
boot) is traced in `docs/sn200-firmware-re.md` and `docs/sn200-independent-re.md`
and is not re-derived here, only used.

Every claim is labelled **PROVEN** (read directly out of correctly-decoded
instructions, with addresses), **INFERRED** (a short chain of reasoning over
proven facts), or **SPECULATIVE**.

---

## 0. Tooling and decode sanity

The FLIX length fix (`tools/sn200-fw/ghidra/install.sh` →
`~/Downloads/ghidra_12.1.2_PUBLIC`) recompiled `xtensa_le.sla` at 16:33; the
running Ghidra (pid 74195) started at 19:38, so it is serving the fixed spec.

**Important caveat about Ghidra for this particular job.** The installed fix only
makes a FLIX bundle an *opaque 8-byte pseudo-op* (`flix.8` → `flix_bundle`,
`tools/sn200-fw/ghidra/languages/flix.sinc`). It fixes the desynchronisation, but
it does **not** decode the slots — and on this core **slot B of a bundle is a
branch**. So Ghidra's decompiler still cannot see roughly half the conditional
control flow in bundle-dense code, and its output would be confidently wrong in a
different way.

All disassembly below therefore comes from `tools/sn200-fw/xdis.py` /
`disany.py`, which decode slot A (a 24-bit core op) *and* slot B (an 18-bit
displacement branch), and which are the source of the width evidence in
`docs/xtensa-flix-decoding.md`. Sanity check requested in the brief:

```
30033534: 2e a0 09 22 f2 ff 90 c0 { movi a2,9 ; j 0x3003345a }
3003353c: 36 61 00  entry a1,0x30            <- lands exactly on the prologue
```

No address gaps, no floating point in integer control code. Bundle slots the
decoder cannot yet name are printed as `?B`/`?C`; where one of those sits on the
critical path I say so rather than guessing.

Images: `/Users/nep/sn200fw/flat/PROC*_7ff80000.bin` (+ `PROC8_30000000.bin`),
strings `/Users/nep/sn200fw/fw/KNGND122/StringTable.csv`.

---

## 1. The shutdown/PFAIL path, end to end

### 1.1 Two entry points, one convergence

| Trigger | Entry | Notes |
|---|---|---|
| Host `CC.SHN` (NVMe CC.SHN=1/2) | PROC13 `CC.SHN controller shutdown port %d type 0x%x` (StrId 696) → PROC0 System Manager `SYS: Shutdown Request Received: %d Status %d`, **PROC0 `0x7ffa8e64`** | orderly |
| Hardware brownout | PROC0 PFAIL ISR **`0x7ffa82dc`** → PFAIL monitor thread **`0x7ffa8314`** | asynchronous |

Both converge on the same worker set and on the **same final step**: the System
Area Manager (SAM) save in PROC6.

### 1.2 The PFAIL interrupt (PROVEN)

`SYS: Enable PFAIL monitoring` (StrId 1211), PROC0 **`0x7ffa8428`**:

```
7ffa842b: l32r a10,0x7ff83100      ; "SYS: Enable PFAIL monitoring"
7ffa8431: l32r a2,0x7ff830c0       ; PFAIL object @ 0x7ff8cd80
7ffa8436: s32i a3,a2,0x20          ; pfailAsserted = 0
7ffa8441: l32r a11,0x7ff83104      ; handler = 0x7ffa82dc
7ffa8444: l32r a4,0x7ff826a0       ; = 0x82a60148  (MMIO)
7ffa8452: s32i.n a10,a4,0x0        ; [0x82a60148] = 0xFFF
7ffa846f: s32i.n a8,a4,0x0         ; [0x82a60148] = 0x7fff0000
7ffa8471: { l32i a4,a4,0x0 ; movi a10,16 }
7ffa8479: call8 0x7ffa0ebc         ; register IRQ 16
7ffa847c: l32r a10,0x7ff82930      ; = 0x00010000
7ffa847f: call8 0x7ffa0ed0         ; intEnable(0x10000)
7ffa8482: l32r a10,0x7ff83114      ; "SYS: PFAIL interrupt enabled"
```

`0x7ffa0ed0` / `0x7ffa0ef4` are the interrupt enable/disable pair (PROVEN):

```
7ffa0ef4: entry                      ; intDisable(mask)
7ffa0ef7: l32r a4,0x7ff8259c         ; -> 0x7ff84648  (shadow: [0]=desired, [4]=gate)
7ffa0efa: rsil a7,5
7ffa0efd: l32i.n a3,a4,0x0
7ffa0eff: l32i.n a6,a4,0x4
7ffa0f01: or  a5,a3,a2
7ffa0f04: xor a5,a5,a2               ; a5 = desired & ~mask
7ffa0f07: s32i.n a5,a4,0x0
7ffa0f09: and a5,a5,a6
7ffa0f0c: wsr a5,228                 ; INTENABLE
```

So **PFAIL is Xtensa IRQ 16 (INTENABLE bit `0x10000`) and it exists only on
PROC0.** No other image contains the ISR or the enable sequence.

The ISR, PROC0 **`0x7ffa82dc`** (PROVEN):

```
7ffa82df: call8 0x7ffb581c        ; enter critical, save ctx
7ffa82e7: l32r a11,0x7ff830c0    ; PFAIL object 0x7ff8cd80
7ffa82ec: s32i.n a13,a11,0x2c    ; pfailPending  = 1
7ffa82ee: s32i.n a13,a11,0x20    ; pfailAsserted = 1
7ffa82f0: s32i.n a12,a11,0x1c    ; t0 = CCOUNT          (rsr a12,234)
7ffa82f2: rsil a2,15             ; INTLEVEL 15
7ffa82f5: { l32r a10,0x7ff830c4 ; ... }   ; event object 0x7ff97870
7ffa82fd: call8 0x7ffb5e78       ; post -> wakes the monitor thread
7ffa8300: wsr a2,230             ; restore PS
7ffa8306: l32r a10,0x7ff82930    ; = 0x00010000
7ffa8309: call8 0x7ffa0ef4       ; intDisable(0x10000)   <- ONE-SHOT
```

**The PFAIL interrupt disarms itself when it fires.** It is only re-armed by
`0x7ffa8428`. Hold that thought for §3 and §4.

### 1.3 The PFAIL monitor thread (PROVEN)

PROC0 `entry` at **`0x7ffa8314`**. It is a resumable state machine: `jx a9`
dispatches on a stored state pointer at `0x7ffa8329`; each state returns a code in
`a2` and the next state in `a9` via the epilogue at `0x7ffa8334`. There are no
call sites — it is bound as a thread body, consistent with WD's "when a shutdown
is issued, internally the firmware will invoke a thread to monitor PFAIL".

States:

| Addr | Role |
|---|---|
| `0x7ffa8415` | idle: `l32i.n a9,a7,0x2c` (pfailPending); if set → `0x7ffa838e` |
| `0x7ffa838e` | **PFAIL detected** — log, arm deadline, kick the subsystems |
| `0x7ffa838b`→`0x7ffa83b9` | drain loop, writes marker **6** |
| `0x7ffa8341` | deadline watch |
| `0x7ffa833e`→`0x7ffa8367` | post-timeout drain, writes marker **7** |
| `0x7ffa832c` | `SYS: PFAIL monitoring thread exits` (StrId 3529) |

Detect state `0x7ffa838e`:

```
7ffa838e: l32i.n a12,a2,0x18
7ffa8390: bnez a12 -> 0x7ffa832c        ; (A) abort flag  -> thread exits
7ffa8393: l32r a10,0x7ff830dc           ; "SYS: PFAIL is detected"      StrId 1209
7ffa8399: l32r a13,0x7ff82b20 -> 0x7ff8c7c4 ; l32i.n a13,a13,0x0
7ffa839e: l32r a14,0x7ff830e0            ; = 0x61a8 = 25000
7ffa83a1: { s32i a14,a7,0x30 ; beqz a13,0x7ffa832c }   ; (B) gate==0 -> thread exits
7ffa83a9: movi a10,1 ; call8 0x7ffa5d54  ; assert global pfail flag + post event
7ffa83ae: movi a10,1 ; call8 0x7ffa6834  ; mode change + notify
7ffa83b3: l32r a10,0x7ff830e4 (=0x7053) ; call8 0x7ffa04d0    ; trace event
7ffa83b9: <drain loop>
```

Both **(A)** and **(B)** are exits that log nothing further, initiate **no
shutdown**, and write **no marker**. (B) is gated on the global at `0x7ff8c7c4`,
which is zero in the image (it is BSS — PROC0's loaded data stops at
`0x7ff84bb4`).

**Correction (2026-08-03).** An earlier revision of this document guessed that
`0x7ff8c7c4` was a "PFAIL handling enabled" health gate tied to
`Admin_isPowerBackupFailed`. That was wrong. Both runtime writers are now read
(PROVEN):

```
7ffa8eb4: l32i.n a13,a2,0x3c                        ; shutdown type
7ffa8ed8: { s32i a14,a2,0x40 ; bnei a13,3,0x7ffa8eea }
7ffa8ee0: { l32r a13,0x7ff82b20 ; beqi a12,2,0x7ffa8eea }  ; a12 = [req+0x10]
7ffa8ee8: s32i.n a3,a13,0x0                         ; [0x7ff8c7c4] = 1   (a3 = 1)
...
7ffa8bba: l32r a9,0x7ff82b20
7ffa8bc0: s32i.n a7,a9,0x0                          ; [0x7ff8c7c4] = 0   (a7 = 0)
```

It is set to 1 by `Shutdown Request Received` (PROC0 `0x7ffa8e64`) when the
request type is **3** and `[req+0x10] != 2`, and cleared to 0 on the completion
path at `0x7ffa8bc0`. The shutdown state machine itself reads it at `0x7ffa8c1d`
and, if it is zero, **skips `SYS: ShutdownReq --> SAM` entirely** and jumps
straight to completion (`0x7ffa8bb2`).

So `0x7ff8c7c4` is a **"a system-area-saving shutdown is in progress"** flag
(INFERRED meaning; writes PROVEN). Consequence for exit (B): PROC0's PFAIL
monitor is a *supervisor for an in-flight shutdown*, not an initiator — exactly
matching WD's phrasing *"when a shutdown is issued, internally the firmware will
invoke a thread to monitor PFAIL"*. A brownout with no qualifying shutdown in
flight makes the monitor log `SYS: PFAIL is detected` and exit, writing no
marker. That is by design, not a bug, but the failure mode it produces is the
same.

### 1.4 What must complete, in order

Reconstructed from the log ordering and each manager's state variable. The
per-manager step numbering is PROVEN; the global ordering between managers is
INFERRED from PROC0's System Manager sequence (`0x7ffa8b8c`) and the message
sends it makes.

1. **Command drain — Data Manager (PROC2/3/4/5/10)**
   `Data_Shutdown completed, nInUseCommandCtx %d` (StrId 616, PROC10 `0x7ffb623f`)
   waits for outstanding NVMe command contexts. It has its own soft warning
   `Data_Shutdown: Taking too long - Timeout %d` (StrId 3589, PROC10 `0x7ffb6267`)
   — i.e. **the firmware itself anticipates this step overrunning.**
   PFAIL cuts deallocates mid-flight:
   `A de-allocate command is broken during PFail from LBA %x to %x` (StrId 631,
   PROC7 `0x7ffb295a`, PROC10 `0x7ffab53a`, PROC12 `0x7ffa70ea`).

2. **Write-buffer / outstanding-write flush — Cache Manager (PROC7/PROC15)**
   `PfailRspPth` (PROC7, `0x7ffaa020`–`0x7ffaa2c5`):
   ```
   7ffaa049: l32i a11,a4,0x2d0            ; outstanding write count
   7ffaa04c: beqz a11 -> 0x7ffaa084
   7ffaa054: "PfailRspPth: wait for %d writes to complete"   StrId 1501
   ... enqueue on wait list, return 2 (yield) ...
   7ffaa0b4: "PfailRspPth: finish writes"                    StrId 1500
   7ffaa1e1: "PfailRspPth: flush %d entries in WB"           StrId 3067
   7ffaa2bd: "Pfail done"                                    StrId 1504
   ```
   PROVEN: this step blocks on a *count* — `[a4+0x2d0]` outstanding writes — and
   then flushes a *count* of write-buffer entries. See §2.

3. **GC quiesce (PROC11)**
   `GC> Waiting to disable GC due to shutdown ...` (StrId 553, `0x7ffa81f4`),
   `GC> Abrupt shutdown: %d pending V2P` (StrId 556, `0x7ffa85a7`),
   `GC> PFail Shutdown done` (StrId 557, `0x7ffa8572`).
   PFAIL short-circuits the graceful variant:
   `GC> Early termination of normal shutdown due to PFail` (StrId 554,
   `0x7ffa81ea`) — returns 0 immediately.

4. **Journal / L2P (PROC12)**
   `Journal Mgr: Shutdown Started for State %d` (1383),
   `Journal Mgr: Shutdown Complete` (1384, `0x7ffa5a6b`),
   `ShutdownInfo: Graceful %d` (1444, `0x7ffa86e3`).

5. **Block Manager + CellCare (PROC6 `0x7ffa87f0`)** — state variable `[a4+0xb0]`:
   ```
   18  "BlockMgr: Completed pending close blockset requests"  (2725) 0x7ffa878a
   19  "BlockMgr: Valid count saved"                          (2726) 0x7ffa875a
   20  "BlockMgr: Erase count saved"                          (2727) 0x7ffa8712
   ->  "BlockMgr(PFail): CellCare Tables saved"               (2734) 0x7ffa881b
   23  "BlkMgr(PFail): Shutdown done"                         (2735) 0x7ffa8899
   ```
   PFAIL early-exit at `0x7ffa8734`:
   ```
   7ffa8729: l16ui a15,a4,0x12a          ; pfail flag
   7ffa872c: { l32i a8,a6,0x34 ; beqz a15,0x7ffa8745 }
   7ffa8734: "BlockMgr: Early termination of Shutdown process due to PFail" (2728)
   7ffa873a: movi.n a2,0 ; retw.n        ; returns SUCCESS
   ```
   PROVEN: on PFAIL, BlockMgr abandons the remaining erase/valid-count saves and
   reports success — **but the PFAIL branch still saves the CellCare tables**
   (`0x7ffa881b`), a NAND write, inside the budget. This is the code behind WD's
   "admin manager stuck saving CellCare data".

6. **System Area save — SAM (PROC6)**, **last**. PROC0 logs
   `SYS: ShutdownReq --> SAM` (StrId 1206, `0x7ffa8c2b`) only after the above,
   then waits. SAM reports
   `SAM: Shutdown completed [%d] SysAreaMarker %08X` (StrId 2337, `0x7ffbb78a`).

### 1.5 The marker instruction, and only it (PROVEN)

PROC6 `0x7ffbba40`–`0x7ffbba61`:

```
7ffbba52: l32i a8,a2,0x68            ; shutdown type carried in the SAM context
7ffbba55: l32r a13,0x7ffa2280        ; = 0x80000002   PFAIL shutdown
7ffbba58: l32r a14,0x7ffa2278        ; -> 0x7ff8bbd0
7ffbba48: l32r a15,0x7ffa227c        ; = 0x80000001   CLEAN shutdown
7ffbba5b: addi a8,a8,-2
7ffbba5e: moveqz a13,a15,a8          ; type==2 ? CLEAN(1) : PFAIL(2)
7ffbba61: s32i.n a13,a14,0x3c        ; -> 0x7ff8bc0c
```

Literals verified from `PROC6_7ff80000.bin`: `0x7ffa227c = 0x80000001`,
`0x7ffa2280 = 0x80000002`.

And immediately upstream, SAM re-checks the hardware itself before choosing the
type (PROC6 `0x7ffbbc78`):

```
7ffbbc78: l32r a13,0x7ffa07dc        ; = 0x82a5fe00
7ffbbc7b: l32i a14,a13,0x340         ; [0x82a60140]  status
7ffbbc7e: l32i a13,a13,0x348         ; [0x82a60148]  enable/mask
7ffbbc81: movi.n a15,-1
7ffbbc83: xor a13,a13,a15
7ffbbc86: bnone a13,a14,0x7ffbbca4   ; (status & ~enable)==0 -> keep caller's type
7ffbbc94: { s32i a15,a8,0x70 ; movi a14,1 }   ; a15=3
7ffbbc9c: { s32i a14,a2,0x68 ; ... }          ; type = 1  (PFAIL)
```

PROC0's System Manager runs the **identical** poll at `0x7ffa8d0f`–`0x7ffa8d18`
using `0x7ff8269c = 0x82a60140` and `0x7ff826a0 = 0x82a60148`. Every processor
image carries the `0x82a5fe00` base literal.

**This is the single most telling structural fact in the firmware.** The
shutdown path does not trust the PFAIL *interrupt*; at the two decision points
that matter it goes and reads the raw hardware status register to ask "is power
failing right now?". You do not write that code unless the interrupt is known to
go missing. (The register semantics — status vs. enable, and which bit — are
INFERRED; the polling structure and its two call sites are PROVEN.)

### 1.6 The complete list of stall / silent-exit points

Ordered by where they sit in the path. "silent" = leaves marker 5/6/7 or nothing.

| # | Where | Effect | Label |
|---|---|---|---|
| S1 | PROC0 `0x7ffa8390` — monitor abort flag `[ctx+0x18]` | thread exits, no shutdown, no marker | PROVEN |
| S2 | PROC0 `0x7ffa83a1` — global gate `[0x7ff8c7c4]==0` | logs "PFAIL is detected", then exits; **no shutdown initiated** | PROVEN |
| S3 | PROC0 `0x7ffa83b9` drain loop — `0x7ffa3bd8` (free-list pop) returns NULL and `[a5+8]==0` | yields to state `0x7ffa838b`, retries forever; **marker 6 never written** | PROVEN |
| S4 | PROC7 `0x7ffaa049` — `[a4+0x2d0]` outstanding writes never reaches 0 | blocked in the wait list, no progress to SAM | PROVEN (mechanism), INFERRED (that it can hang) |
| S5 | PROC10 `0x7ffb6267` — `Data_Shutdown: Taking too long` | warns, does not abort; command contexts not drained | PROVEN |
| S6 | PROC11 GC quiesce | WD documents a GC deadlock here; I found the early-exit (`0x7ffa81ea`) but **not** the deadlock itself | see §6 |
| S7 | PROC6 `0x7ffa881b` — CellCare table save inside the PFAIL budget | long NAND write on the critical path | PROVEN |
| ~~S8~~ | ~~PROC0 `0x7ffa8de0` — mid-shutdown PFAIL re-arm~~ | **WITHDRAWN 2026-08-03.** The branch is post-completion and unreachable while PFAIL is asserted; it schedules "Waiting for CC.EN (FAST_RESTART)". | see §4.1–§4.3 |
| S9 | PROC9 `0x7ffaed12` — `PCIe_PfailShutdown` finds the port already shutting down | logs "Do nothing" and returns | PROVEN — see §3 |
| S10 | PROC8 `0x7ffb1b92` — `Admin_ShutdownPFailMonitor` poll loop, no deadline, gated on `[0x7ff95678]` which is only ever *decremented* | spins forever on PROC8 | PROVEN (unbounded loop), INFERRED (that the guard cannot clear) — see §4.4 |

Note also the boot-side confirmation that step 6 is the one that fails:
PROC0 `0x7ffaae18` logs
**`SYS: ERROR - Previous shutdown failed to save System Area`** (StrId 1262) when
`[a14+4] != 4`, immediately before the two section tests at `0x7ffaae35` /
`0x7ffaae3d` that force `0x80000009`.

---

## 2. The hold-up budget — and whether it scales with workload

### 2.1 The literal is 25000 µs = 25 ms (PROVEN)

Deadline check, PROC0 state `0x7ffa8341`:

```
7ffa8346: rsr a9,234              ; CCOUNT
7ffa8349: l32r a10,0x7ff82e14    ; -> 0x7ff979e0
7ffa834c: l32i.n a12,a7,0x30     ; deadline field = 25000
7ffa834e: l32i.n a10,a10,0x0     ; cycles per unit
7ffa8350: l32i.n a11,a2,0x1c     ; t0 (CCOUNT at ISR)
7ffa8352: mull a10,a10,a12       ; deadline_cycles = cyc_per_unit * 25000
7ffa8355: sub  a9,a9,a11         ; elapsed cycles
7ffa8358: bltu a9,a10,0x7ffa8380 ; still inside -> keep waiting
7ffa835b: l32r a10,0x7ff830cc    ; "SYS: PFAIL timeout is expired"   StrId 1210
```

The unit is fixed by the elapsed-time getter at PROC0 `0x7ffa82bc`, which divides
by the **same** `[0x7ff979e0]`:

```
7ffa82c6: rsr a4,234
7ffa82cc: l32i.n a2,a2,0x1c
7ffa82d0: sub  a2,a4,a2
7ffa82d3: quou a2,a2,a3          ; (CCOUNT - t0) / [0x7ff979e0]
```

and by the boot report format `SYS: PFAIL time = % 5u.%03u ms` (StrId 1257,
PROC0 `0x7ffaab71`) — a µs value split into ms.mmm. So `[0x7ff979e0]` is
**cycles per microsecond** and the budget is **25000 µs = 25 ms**.

Confirming the brief's premise: the deadline literal and both markers are in the
same pool, contiguous —

```
0x7ff830e0 = 0x000061a8   (25000)
0x7ff830ec = 0x80000006   PFAIL Shutdown STARTED
0x7ff830f4 = 0x80000007   PFAIL Shutdown TIMEOUT
```

### 2.2 Markers 6 and 7 are *breadcrumbs*, written at the start (PROVEN)

This corrects a natural misreading. Marker 6 is not written on failure; it is
written **immediately on PFAIL detection**, before any work, by the drain loop:

```
7ffa83b9: call8 0x7ffa3bd8                     ; pop a record from the free list
7ffa83bc: { l32i a14,a5,0x8 ; bnez a10,0x7ffa83cf }
7ffa83c4: bnez a14,0x7ffa83b9                  ; retry
7ffa83cf: { s32i a6,a10,0x24 ; movi a11,1 }
7ffa83d7: { l32r a15,0x7ff830ec ; movi a12,6 } ; 0x80000006
7ffa83df: { s32i a15,a10,0x20 ; movi a14,0 }
7ffa83e7: call8 0x7ffb4fec                     ; submit record
7ffa83ea: -> state 0x7ffa8380  (deadline watch)
```

Marker 7 is written the same way after the deadline expires (`0x7ffa83f2`–
`0x7ffa840a`, literal `0x7ff830f4`), and then the state pointer becomes
`0x7ff830f8 = 0x7ffa832c` — **the monitor thread exits.**

The mirror of this on the `CC.SHN` side is PROC0 `0x7ffa8dda`, which submits
`0x7ff83230 = 0x80000005` (**Normal Shutdown STARTED**) and falls straight
through to `SYS: Returning shutdown completion` at `0x7ffa8bdc`.

So the established reading holds and now has a mechanism:
**5/6/7 are "I started", 1/2 are "I finished", and only PROC6 `0x7ffbba61` can
write "I finished".** A latched drive is one where the breadcrumb was written and
`0x7ffbba61` was never reached before the rails collapsed.

### 2.3 What expiry actually does (PROVEN, and it is worse than it sounds)

At 25 ms the monitor logs the timeout, submits marker 7, and **exits**. It does
**not** abort the save, does not force the marker, does not reset the drive. The
save keeps running on whatever residual energy remains, with no supervisor and
with the breadcrumb now reading `TIMEOUT`.

Consequently the 25 ms is **not** a hold-up guarantee. It is a stopwatch that
labels the failure. If the hardware hold-up energy happens to outlast the work,
SAM still reaches `0x7ffbba61` and writes marker 2 and the drive boots clean —
which is exactly the "clean pfail" path already documented. If it does not, the
drive latches.

### 2.4 Does the work scale with dirty state? **Yes.** (PROVEN)

The budget is a compile-time constant. The work is not:

| Work item | Scaling quantity | Evidence |
|---|---|---|
| Wait for outstanding NAND writes | `[a4+0x2d0]` — a live count | PROC7 `0x7ffaa049`, logged by StrId 1501 `"wait for %d writes to complete"` |
| Flush write buffer | number of WB entries | PROC7 `0x7ffaa1e1`, StrId 3067 `"flush %d entries in WB"`; PROC7 StrId 1517 `" PFail: Flushed %d WB frames"` |
| Drain command contexts | `nInUseCommandCtx` | PROC10 `0x7ffb623f`, StrId 616 |
| GC pending V2P updates | `%d pending V2P` | PROC11 `0x7ffa85a7`, StrId 556 |
| In-flight deallocates | one log per broken range | StrId 631, three images |
| CellCare tables | fixed-ish, but large | PROC6 `0x7ffa881b` |
| Cache Manager census | `"%d requests in progress, %d outstanding writes, WB Counts %d/%d/%d/%d"` | StrId 1483, PROC7 `0x7ffa9bfb`, PROC15 `0x7ffa9f43` |

**Fixed 25 ms budget, workload-proportional work list.** A busier drive is
strictly likelier to latch. This matches the field pattern and it matches WD's
own failing test profile being *"Power Cycling + Random Read/Write/Deallocate IO
Profile"* — deallocates are the worst case because they dirty large spans of L2P
without moving much data, which is precisely OM-6588
(*"Drives failed to restore L2P table after large deallocate and a pfail"*).

INFERRED, and worth stating plainly: **there is no admission control.** Nothing
in the PFAIL path caps the amount of dirty state against the 25 ms. The firmware
lets the drive get arbitrarily far from a savable state and then hopes.

---

## 3. The lost-Pfail-interrupt race

WD: *"when both a link down and a Pfail interrupt occur at exactly the same time
… the Pfail interrupt may get lost."*

### 3.1 What I can prove

**(a) PFAIL is PROC0-only.** The ISR (`0x7ffa82dc`), the enable
(`0x7ffa847c`/`0x7ffa847f`, INTENABLE bit `0x10000`) and the monitor
(`0x7ffa8314`) exist in `PROC0_7ff80000.bin` and nowhere else. PCIe lives on
PROC9. **PROC9 can never see a PFAIL interrupt.** It learns about power failure
only as an inter-processor message. (PROVEN by absence across all 18 images.)

**(b) The PFAIL interrupt is one-shot and self-masking.** `0x7ffa8306` →
`intDisable(0x10000)` at the tail of the ISR. Until something calls `0x7ffa8428`
again, a second PFAIL edge is not delivered. (PROVEN.)

**(c) `PCIe_PfailShutdown` has an explicit "do nothing" branch keyed on the port
already being in a shutdown/reset state.** PROC9 `0x7ffaecf0`:

```
7ffaecfd: l32i a12,a2,0xb8            ; port1 shutdown state
7ffaed00: call8 0x7ffba9d8            ; "PCIe_PfailShutdown: current shutdown state port 0/1 %d/%d"
7ffaed03: l32i.n a9,a2,0x1c           ; port0 shutdown state
7ffaed05: beqi a9,1,0x7ffaed50        ; -> 0x7ffae628(0,0)
7ffaed08: l32i a10,a2,0xb8
7ffaed0b: beqi a10,1,0x7ffaed60       ; -> 0x7ffae628(1,0)
7ffaed0e: bnez.n a9,0x7ffaed12
7ffaed10: beqz.n a10,0x7ffaed40
7ffaed12: l32r a10,0x7ffa147c         ; "PCIe_PfailShutdown: subsystem already shut down
                                      ;  or in the process of shutting down - Do nothing"
7ffaed18: call8 0x7ffa8428            ; (PROC9-local) allocate a breadcrumb context
7ffaed1b: { ... ; bnez a10,0x7ffaed30 }
7ffaed23: l32r a10,0x7ffa1480         ; "Unable to get context to submit breadcrumbs
                                      ;  for PFAIL shutdown"
7ffaed29: retw.n
```

That string, StrId 3111, is the defect stated in the firmware's own words: if the
port state is already non-zero, the PFail-driven PCIe shutdown is **swallowed**.

**(d) Link-down sets exactly that state, from a bottom half.** PROC9
`BottomHalfAttentionHandler: Link down detected on port %d` (StrId 946,
`0x7ffa4e45`), then `PerstLinkDown = TRUE` (StrId 3091, `0x7ffa4ebc`) writing
`[a8+0x2f8] = 1`, and `PCIe_SendResetRequest LINKDOWN Reset Detected port %d`
(StrId 902, `0x7ffad07d`) / `Hot Reset Detected` (StrId 897, `0x7ffad397`).
"Bottom half" means it runs in **deferred/thread context**, not in the ISR.

### 3.2 The mechanism

Putting (a)–(d) together — **INFERRED**, and I want to be precise about which
part is inference:

The race is *not* the PFAIL interrupt being masked by PCIe code. I looked for
that and did not find it: nothing outside PROC0 touches INTENABLE bit `0x10000`,
and no PCIe path calls `0x7ffa0ef4` with `0x10000`. The mechanism is
**ordering between two deferred handlers on PROC9**:

1. Link goes down. PROC9's attention ISR queues its bottom half.
2. Essentially simultaneously, PROC0 takes the real PFAIL interrupt, sets
   `pfailPending`, and sends the PFail-shutdown message to PROC9.
3. PROC9 runs the link-down bottom half **first**. It drives
   `PCIe_SendResetRequest`, which moves the port shutdown state off zero.
4. PROC9 then dequeues the PFail message and calls `PCIe_PfailShutdown`, which
   hits `0x7ffaed12` and logs *"already shut down or in the process of shutting
   down - Do nothing"*.

The PFail event is not lost in the interrupt controller — it is lost in the
**PCIe subsystem's state machine**, which cannot distinguish "I am already going
down because the host reset me" from "I am going down because the power is
dying". The two have entirely different urgency and entirely different completion
requirements, and the second one is discarded.

There is a second, compounding failure in the same branch (PROVEN structure,
INFERRED significance): both the link-down handler (`0x7ffa4e6d`) and the
"do nothing" branch (`0x7ffaed18`) allocate from the **same** breadcrumb context
pool via PROC9-local `0x7ffa8428`, and both check for NULL. A link-down storm can
exhaust that pool, at which point the PFAIL breadcrumb is dropped with
`Unable to get context to submit breadcrumbs for PFAIL shutdown` (StrId 3580) —
and the drive loses even the *record* that a PFail happened.

**What would settle the ordering claim conclusively:** the message-queue
dispatch order on PROC9 between the attention bottom half and the inter-processor
PFail message. I traced the producers and the consumer but not the scheduler's
priority between them. A live capture would settle it instantly — the log lines
are all present in the firmware (`946`, `3110`, `3111`) and would appear in a
crash dump in exactly this order if the mechanism is right. That is a read-only
check against a dump; it is **not** something to do to the production drive.

---

## 4. The PFAIL monitor thread that was "added again"

KNGND122's notes: *"the PFAIL monitor thread is added again … a hang occurs
during the shutdown process"* — **still being fixed, recovery: unable to
recover.**

> **RETRACTION (2026-08-03).** §4.1–§4.2 previously claimed that the System
> Manager re-arms PFAIL monitoring *mid-shutdown*, restarting the 25 ms deadline
> from the latest PFAIL edge, and listed that as defect 3 ("S8"). **That claim is
> withdrawn.** The re-arm is real, but it is not mid-shutdown and it cannot fire
> while PFAIL is asserted. The corrected trace is §4.1–§4.3 below. The genuine
> second monitor is on PROC8 and is documented in §4.4.

### 4.1 What the re-arm branch actually tests (PROVEN)

The branch is at the **tail** of the System Manager state machine, after the work
list and after `SYS: Returning shutdown completion` (`0x7ffa8bdc`):

```
7ffa8cfd: l32i.n a12,a2,0x3c                       ; shutdown type
7ffa8d05: { l32r a13,0x7ff826a0 ; beqi a12,6,0x7ffa8d4d }   ; type 6 (FAST) -> done
7ffa8d0f: l32i.n a14,a14,0x0                       ; [0x82a60140] status
7ffa8d11: l32i.n a13,a13,0x0                       ; [0x82a60148] enable
7ffa8d15: xor a13,a13,a15                          ; a15 = -1
7ffa8d18: bnone a13,a14,0x7ffa8d23                 ; nothing pending -> a9 = 0
7ffa8d1b: { movi a9,1 ; j 0x7ffa8d25 }
7ffa8d23: movi.n a9,0
7ffa8d25: { l32r a10,0x7ff82f40 ; bnez a9,0x7ffa8d3d }   ; PFAIL ASSERTED -> 0x7ffa8d3d
7ffa8d2d: l32r a15,0x7ff83120                      ; -> 0x7ff8c7ec
7ffa8d30: l32r a8,0x7ff83218                       ; = 0x80000002
7ffa8d33: l32i.n a15,a15,0x0                       ; [0x7ff8c7ec + 0x00]
7ffa8d35: { sync ; bne a15,a8,0x7ffa8de0 }
```

Two facts the earlier reading missed:

1. **`0x7ffa8d2d` is only reached when the live hardware poll says PFAIL is
   *not* asserted.** `bnez a9,0x7ffa8d3d` at `0x7ffa8d25` diverts every case
   where power is actually failing.
2. **`[0x7ff8c7ec]` is not something SAM publishes.** See §4.2.

### 4.2 `0x7ff8c7ec` is the *boot* info block, not a live SAM handshake (PROVEN)

`0x7ff8c7ec` is PROC0-local BSS (PROC0's loaded data segment ends at
`0x7ff84bb4`; the region is self-aliased per core, so no other processor can
write it — see `docs/sn200-certainty.md`). It is the base of the **boot /
shutdown debug info structure**, whose layout is fixed by the reporter at PROC0
`0x7ffaab28`:

| Offset | Meaning | Evidence |
|---|---|---|
| `+0x00` | effective marker this boot came up with | `0x7ffaae21`, `0x7ffa8d33` |
| `+0x08`, `+0x0c` | boot timestamps | `0x7ffaab3f`, `0x7ffaab41` |
| `+0x14` | `userCapacityGB` | `0x7ffab3e3` (StrId 1292), `0x7ffab856` |
| `+0x8c`… | breadcrumb characters (`0x7ff8c878`) | `0x7ffaaba0` (StrId 1259) |
| `+0xf4` | persisted `SysAreaMarker` read from the System Area | `0x7ffaab31`, `0x7ffaadc6` |
| `+0xfc` | shutdown time, µs | `0x7ffaab69` — `SYS: Shutdown time = % 5u.%03u ms` |
| `+0x100` | PFAIL time, µs | `0x7ffaab71` — `SYS: PFAIL time = % 5u.%03u ms` |

**Every writer of `+0x00` is boot-side.** The base is reachable from exactly 11
`l32r` sites (`litref.py -a 0x7ff83120 PROC0`) and a whole-image sweep for RRI8
stores whose effective address is `0x7ff8c7ec` finds nothing else. The writers
are:

```
7ffaac01: s32i a11,a3,0x0     ; = 0x80000000   (clear; fn 0x7ffaabd8)
7ffaadb3: s32i a12,a7,0x0     ; = 0x80000003   "SYS: Found an incompatible SA"
7ffaae21: s32i.n a11,a7,0x0   ; := [+0xf4]     the persisted marker
7ffaae67: s32i.n a11,a7,0x0   ; = 0x80000003   "SYS: Detected CellCare mismatch"
```

all inside the boot System-Area evaluation function `0x7ffaac30`, which is used
only as a **boot** state pointer (its address appears once, in the literal loaded
at `0x7ffab54e`), plus `0x7ffaabd8` whose only two call sites (`0x7ffaacb8`,
`0x7ffab007`) are in that same boot function.

So the test at `0x7ffa8d35` asks **"did this boot come up from a PFAIL-marked
System Area?"** — not "has SAM finished?".

### 4.3 What the branch schedules — and why it is correct (PROVEN)

`0x7ffa8de0` does not call `0x7ffa8428`. It schedules a sub-state-machine:

```
7ffa8de0: l32r a10,0x7ff83234   ; = 0x7ffa8910   sub-machine entry
7ffa8de3: l32r a9,0x7ff83238    ; = 0x7ffa8d5d   resume state
7ffa8de6: { addi a6,a2,24 ; j 0x7ffa8d45 }
7ffa8d45: { movi a2,7 ; j 0x7ffa8c7a }           ; epilogue puts a10 in a5
```

`0x7ffa8910`'s **state-0 entry is `0x7ffa8b7b`**, not `0x7ffa892b`
(`0x7ffa8920: { l32r a11,… ; beqz a15,0x7ffa8b7b }` on the stored state pointer):

```
7ffa8b7b: movi.n a10,2 ; call8 0x7ffa6834          ; mode := 2
7ffa8b80: l32r a10,0x7ff831dc  ; StrId 1200 "Waiting for CC.EN (FAST_RESTART) from PcieMgr"
7ffa8b83: call8 0x7ffb5398
7ffa8b86: j 0x7ffa8b6b -> { movi a2,13 ; j 0x7ffa89e3 }   ; yield, wait
```

`0x7ffa8428` is reached only at `0x7ffa8944`, inside the state that runs when
PcieMgr actually delivers the restart (`Received FAST_RESTART request from
PcieMgr`, StrId 1201, `0x7ffa8936`). **The re-arm is part of bringing the
controller back up after `CC.EN`, which is exactly where PFAIL monitoring should
be re-enabled.**

The other two outcomes both go to the literal at `0x7ff82f40 = 0x7ffa8840`, the
**post-shutdown power-off watcher**: it writes 1 to MMIO `0x82a60020`
(`0x7ffa8850`–`0x7ffa8861`), then polls CCOUNT in 1 ms steps
(`movi a14,1000; mull a13,[0x7ff979e0],a14`, `0x7ffa8868`) waiting for the rails
to actually go, and if the saved hardware status says PFAIL it submits
`0x7ff830f4 = 0x80000007` (`0x7ffa88c5`–`0x7ffa88dd`).

**Answer to the open question.** The predicate `[0x7ff8c7ec] != 0x80000002` is
**normally true** — a drive that booted after a clean shutdown holds
`0x80000001`, and a latched drive holds the forced `0x80000009`; only a drive
that booted from a PFAIL-marked System Area holds `0x80000002`. But that no
longer matters, because:

- during a **real PFAIL** the branch is unreachable — `0x7ffa8d25` diverts to the
  power-off watcher while the line is asserted;
- on the **`CC.SHN`** path taking it is correct — the controller parks in
  "Waiting for CC.EN" and re-arms PFAIL only when the host restarts it.

**There is no mid-shutdown re-arm and no deadline restart. Defect 3 is
withdrawn**; the shutdown path has two defects (§2.4 no admission control, §3 the
PCIe "Do nothing"), not three.

### 4.4 The second monitor is real, and it is on PROC8 (PROVEN)

`Admin_ShutdownPFailMonitor: PFail detected` (StrId 2914) is at PROC8
`0x7ffb1bb6`. **It disassembles fine** — the earlier note that it fell in an
image hole was wrong: `segparse.py` puts it inside the segment
`0x7ffa0710–0x7ffbb064`, and `whichfunc.py --image PROC8_7ff80000 0x7ffb1bb6`
places it at `0x7ffb1b60+0x56`. (`unpack.py` already lays segments at their true
load addresses; the only holes are between segments, and this is not in one.)

The whole function:

```
7ffb1b60: entry a1,0x20
7ffb1b63: l32r a9,0x7ffa07c4        ; = 0x82a60140  status
7ffb1b66: { l32r a8,0x7ffa07c8 ; movi a10,4095 }   ; = 0x82a60148  enable, mask 0xFFF
7ffb1b6e: l32i.n a9,a9,0x0          ; <-- re-entry point for the poll loop
7ffb1b70: l32i.n a8,a8,0x0
7ffb1b72: xor a8,a8,a10
7ffb1b75: { movi a10,1 ; bnone a8,a9,0x7ffb1b80 }  ; a10 = PFAIL asserted?
7ffb1b80: movi.n a10,0
7ffb1b82: l32r a9,0x7ffa1db4 ; l32i a9,a9,0xb0     ; [0x7ff95678] shutdowns in flight
7ffb1b88: bnez.n a10,0x7ffb1b98
7ffb1b8a: { movi a2,2 ; beqz a9,0x7ffb1bc3 }       ; no PFAIL, nothing in flight -> exit
7ffb1b92: l32r a13,0x7ffa1db8       ; = 0x7ffb1b6e  next state
7ffb1b95: j 0x7ffb1bcb              ; return 2 -> poll again, FOREVER
7ffb1b98: l32r a10,0x7ffa09a0       ; -> 0x7ff88018  global shutdown mode
7ffb1b9b: beqz.n a9,0x7ffb1bc3
7ffb1b9f: beqi a11,3,0x7ffb1bc3     ; already PFail mode -> nothing to do
7ffb1bae: { s32i a8,a10,0x0 ; movi a14,1 }         ; [0x7ff88018] = 3  (PFail)
7ffb1bb6: { l32r a10,0x7ffa1dbc ; ... }            ; StrId 2914
7ffb1bbe: s32i.n a12,a13,0x0        ; [0x7ff918a8]++  detection counter
7ffb1bc0: call8 0x7ffb45a8          ; emit the log record
7ffb1bc3: l32r a8,0x7ffa1dc0 ; movi.n a2,0 ; s32i a2,a8,0x1c0   ; [0x7ff95688] = 0
7ffb1bcb: mov.n a3,a13 ; retw.n     ; a2 = return code, a3 = next state
```

It is **spawned from the admin inter-processor message receiver**
(`Admin_MessageCommandReceiverIBQ`), once per shutdown, guarded so it cannot be
armed twice:

```
7ffb0561: l32r a10,->0x7ff88018 ; movi a9,3 ; s32i.n a9,a10,0x0   ; mode = 3 (PFail Shutdown)
7ffb0568: StrId 2054 "Admin_MessageCommandReceiverIBQ PFail Shutdown"
7ffb0574: [0x7ff88018] = 2                                        ; mode = 2 (normal)
7ffb0586: l32i a13,a3,0x84 ; bnez.n a13,0x7ffb059c                ; already armed -> skip
7ffb058b: l32r a10,0x7ffa1bb0   ; -> 0x7ff955f0   task object
7ffb058e: l32r a11,0x7ffa1bb4   ; -> 0x7ffb1b60   task entry
7ffb0591: { s32i a5,a3,0x84 ; … } ; call8 0x7ffb9768               ; schedule it
```

Answers to the four questions asked of it:

- **What it monitors.** The raw PFAIL hardware register pair
  `[0x82a60140]`/`[0x82a60148]`, **by polling**, with a `0xFFF` mask — narrower
  than PROC0's and PROC6's `~0` tests, and matching the `0xFFF` written by the
  enable sequence at PROC0 `0x7ffa8452`. It is gated on a shutdowns-in-flight
  count at `0x7ff95678`, decremented at PROC8 `0x7ffb1e8a` in the
  `Admin_SendShutdownCompletion` state machine (`0x7ffb1bd0`).
- **What it does on timeout.** *There is no timeout.* No CCOUNT read, no deadline
  field, no timeout string anywhere in the function. Unlike PROC0's 25 ms
  supervisor this one has no clock at all.
- **What it does on detection.** Upgrades the global shutdown mode
  `[0x7ff88018]` from 2 (normal) to **3 (PFail)**, so the admin manager takes the
  abbreviated PFail path — the PROC8 counterpart of PROC6's `0x7ffbbc86`
  downgrade. Then it logs, bumps `[0x7ff918a8]`, and **terminates itself**
  (`a2 = 0`). Because `[ctx+0x84]` is still set it is never re-armed for that
  shutdown: it is strictly **one-shot**.
- **Can it hang.** Yes, in the "spins forever" sense (PROVEN loop; INFERRED that
  it is reachable). While `[0x7ff95678] != 0` and PFAIL is not asserted it
  returns 2 with next state `0x7ffb1b6e` and re-polls with no bound. `0x7ff95678`
  is BSS and I can find **no code in either PROC8 image that increments it** —
  the only writer is the `addi.n a13,a13,-1` decrement at `0x7ffb1e8a` (checked
  by an effective-address sweep over both `PROC8_7ff80000` and `PROC8_30000000`,
  and by an opcode sweep for every `s32i …,0x1b0`). If it is ever decremented
  from zero it wraps to `0xFFFFFFFF` and the monitor becomes a permanent poller
  on PROC8. I could not prove that path is taken, so: **PROVEN that the loop is
  unbounded, INFERRED that the guard can never clear it, SPECULATIVE that this is
  the "hang … during the shutdown process".**

**How it interacts with PROC0's monitor: it does not.** Different processors,
disjoint state, no shared object, no lock. PROC0 owns the interrupt, the 25 ms
CCOUNT deadline and the breadcrumb markers; PROC8 owns only the mode byte
`0x7ff88018`. They are not racing over one shutdown, so this is not the
"two monitors racing" defect the brief hypothesised. The interesting asymmetry is
the opposite one: **PROC0's PFAIL interrupt is one-shot and self-masking
(§1.2), while PROC8's is a poll of the raw register.** PROC8 can therefore see a
PFAIL edge that PROC0's masked interrupt never delivers — it is an accidental
backstop for the lost-interrupt defect, but only while an admin shutdown is in
flight, and only once.

### 4.5 Where PROC0's monitor really can hang

`SYS: Enable PFAIL monitoring` (PROC0 `0x7ffa8428`) still has exactly two call
sites, `0x7ffa8944` and `0x7ffab917` (verified with `xref.py`). `0x7ffab917` is
boot-time init. `0x7ffa8944` is the FAST_RESTART completion, per §4.3 — not a
mid-shutdown path.

**Can PROC0's monitor itself hang?** Yes, and this part stands: stall point S3.
The drain loop at `0x7ffa83b9` retries `0x7ffa3bd8` forever while `[a5+8]` is
non-zero and yields to a state that re-enters the same loop when it is zero. If
the record free list is empty — and §3.2 gives a reason for it to be empty, a
link-down storm — the monitor never writes marker 6 and never advances to the
deadline watch. (PROVEN loop structure; INFERRED that the pool can actually be
exhausted.)

---

## 5. Host-side exposure reduction — the honest answer

### 5.1 Does a completed `CC.SHN` on an idle drive reliably finish?

**Reliably, no. Very probably, yes — and "very probably" is the correct phrasing,
not hedging.**

What is PROVEN:

- `CC.SHN` and PFAIL converge on the same worker list and the same final
  instruction (`0x7ffbba61`). There is no separate, simpler "clean" path.
- On the `CC.SHN` path the System Manager waits on each manager **without a
  global deadline**. I found per-manager soft warnings
  (`Data_Shutdown: Taking too long`, PROC10 `0x7ffb6267`) but **no** wall-clock
  abort in the System Manager state machine `0x7ffa8b8c`. The only hard deadline
  anywhere in the shutdown code is the 25 ms PFAIL one.
- An idle drive has near-zero values for every scaling quantity in §2.4, so the
  work list is short.
- If the host completes `CC.SHN` and *then* power is removed, the save has
  already run to `0x7ffbba61` and marker 1 (`0x80000001`) is in place.

What breaks it anyway:

- `0x7ffbbc86` — even a `CC.SHN` shutdown re-reads the hardware status and, if it
  sees a power-fail assertion, **downgrades the marker to `0x80000002`** and
  takes the PFAIL branches. A PSU that droops while the orderly shutdown is
  running converts it into a PFAIL shutdown mid-flight.
- S1/S2 (§1.3) are exits that do not care how the shutdown started.
- S10 (§4.4) is on the `CC.SHN` path too: PROC8 arms
  `Admin_ShutdownPFailMonitor` for a **normal** admin shutdown as well as a
  PFail one (`0x7ffb0574` sets mode 2 and falls into the same arming code).

### 5.2 Does quiescing I/O and waiting before power removal help?

**Yes, materially, and this is the strongest lever available.** It is a direct
consequence of §2.4: every quantity the save must chew through is a live counter
of dirty state, and idling drives all of them toward zero while the budget stays
at 25 ms. The ratio of work-to-budget is the whole ballgame, and the host
controls the numerator.

Concretely, in decreasing order of value:

1. Stop issuing I/O — especially **deallocate/TRIM/discard** — and let it settle.
   Deallocates are the specific workload in WD's failing test and in OM-6588.
   On Linux that means `discard` off / no `fstrim` near a planned power event.
2. `sync` + unmount, so the write buffer and outstanding-write counts drain
   through the normal path rather than through `PfailRspPth`.
3. Then `nvme shutdown` / let the OS issue `CC.SHN` and *wait for it to
   complete*.
4. Only then remove power.

### 5.3 Does a UPS-driven orderly OS shutdown avoid the defect?

**It reduces the odds — substantially — but it does not avoid the defect.**
Saying otherwise would be wrong, and this feeds a keep-or-bin decision, so:

- It **does** avoid the workload-scaling failure (§2.4), which is the mechanism
  that best explains a five-drive field pattern. That is the dominant term. An
  orderly shutdown with quiesced I/O is a genuinely different risk regime.
- It **does not** avoid §3. The link-down/PFail collision is a race between two
  deferred handlers, and an orderly OS shutdown *creates* PCIe link-state
  activity — that is what `PCIe_SendResetRequest: D3hot clear LINKDOWN`
  (StrId 903, PROC9 `0x7ffad0e8`) is for. Under normal conditions there is no
  concurrent PFail, so the race does not arm. But a UPS that fails over
  imperfectly, or a rail that sags during the shutdown, arms it precisely then.
- It **does** avoid the withdrawn S8 — because S8 was not a defect. See §4.
- It **does not** avoid S1/S2. If `0x7ff8c7c4` is zero the PFAIL monitor logs and
  exits without initiating anything; per §1.3 that flag is set only by a
  type-3 shutdown request, so a brownout that arrives with no host shutdown in
  flight is unsupervised no matter what the host does.
- Nothing the host can send changes the 25 ms constant or the ordering on PROC9.
  There is no vendor-unique command in the set catalogued in
  `docs/sn200-firmware-re.md` that tunes `[obj+0x30]`; it is written from the
  literal at `0x7ffa839e` on every detect, so even if the field were writable it
  would be overwritten.

And the part that makes this a fleet decision rather than an operational one:
**KNGND122 is terminal.** WD's own notes mark the PFAIL-monitor hang as still
being fixed in this revision with *"Drive Recovery: Unable to recover."* There is
no firmware in which this is fixed. The mitigation is behavioural, permanent, and
partial.

---

## 6. What I could not determine

Stated explicitly, because a gap is more useful than a guess here.

1. **The GC deadlock during shutdown** (WD's named defect). **Still not found**,
   but now localised to specific words rather than a function range.

   The GC shutdown machine is PROC11 `0x7ffa8070` (`entry`, dispatch `jx a9` on
   `[a2+0x10]`), context base `a4 = [0x7ffa0994] = 0x7ff80e44`. Its wait
   predicates are PROVEN:

   ```
   7ffa8085: l32i a8,a4,0x28c    ; -> 0x7ff810d0   outstanding work A
   7ffa8088: { l32i a9,a4,0x168 ; beqz a8,0x7ffa81b0 }   ; a9 = mode
   7ffa8090: { … ; bnei a9,5,0x7ffa8158 }                ; mode != 5 -> yield
   7ffa8158: { l32r a3,0x7ffa0d20 ; movi a2,2 }          ; = 0x7ffa8085, retry
   7ffa81d2: l32i a10,a4,0x294   ; -> 0x7ff810d8   outstanding work B
   ```

   So GC leaves the wait only when both counters reach zero **or** the mode
   `[0x7ff80fac]` becomes 5 (the PFail short-circuit that logs StrId 554 at
   `0x7ffa81ea`). The only decrements of either counter are
   `0x7ffa2ecb`/`0x7ffa2ecd` (`0x7ff810d0`) and `0x7ffa4e7e`/`0x7ffa4e80`
   (`0x7ff810d8`), both inside completion handlers — i.e. they drain only when
   the media path retires work. (`0x7ffa4db5` and `0x7ffa2ede` also write them,
   but as copies, not adjustments.)
   That is the shape of the circular wait (GC waits on media
   completions; the media managers are themselves shutting down), but I have not
   traced a completion path back into a component that is itself blocked on GC,
   so **I am not calling it proven.**

   One earlier suspicion is ruled out: `[a4+0x190]` (`0x7ff80fd4`), which also
   gates the wait at `0x7ffa817b`, has **no writer anywhere in PROC11** — an
   opcode sweep for every `s32i …,0x190` finds one site (`0x7ffa31a0`) and its
   base is `0x7ff80900`, a different object. `[a4+0x190]` is permanently zero and
   is not the stall.

   *Would settle it:* the pair of tasks blocked in a dump taken while latched, or
   tracing who increments `0x7ff810d0` / `0x7ff810d8` and which processor retires
   those items during a shutdown.

2. **"System Manager never sending the shutdown message"** (WD). PROC0
   `SYS: ShutdownReq --> SAM` (`0x7ffa8c2b`) is guarded by the drain loops at
   `0x7ffa8c67` and `0x7ffa8cd3`, which have the same NULL-record retry shape as
   S3. That is a *plausible* place for the message never to be sent, but I did
   not prove the pool can be exhausted on that path.

3. **The bit assignment in `[0x82a60140]` / `[0x82a60148]`.** Both PROC0 and
   PROC6 test *all* bits (`status & ~enable`), so no single PFAIL bit is
   identifiable from these two sites. The enable sequence writes `0xFFF` then
   `0x7FFF0000` (`0x7ffa8452`, `0x7ffa846f`), which is consistent with either a
   mask or a clear register. Getting this wrong does not change any conclusion
   above, but it does mean I cannot say "PFAIL is bit N".

4. **Whether S3 fires in the field.** The retry-forever loop is proven; the pool
   exhaustion that would make it fire is inferred from the two allocators sharing
   `0x7ffa8428` on PROC9. No count of the pool size was recovered.

5. **PROC8's `Admin_ShutdownPFailMonitor: PFail detected`** (StrId 2914,
   referenced at `0x7ffb1bb6`). `PROC8_7ff80000.bin` is a sparse flat image and
   that address falls in an unpopulated hole, so it did not disassemble. This is
   an admin-side PFAIL monitor distinct from PROC0's, and it is a live candidate
   for a *second* "monitor thread added again". Rebuilding PROC8's flat image
   with hole-aware segment placement (`segparse.py` shows 11 segments) would let
   it be read.

6. **`SYS: Stopping SAM startup because PFAIL is in progress`** (StrId 3518,
   PROC0 `0x7ffa72e3`). This is on the *startup* side and suggests a boot in
   which a PFAIL is still considered in progress halts the System Area load. It
   may be part of why a latched drive stays latched across power cycles. I did
   not trace its predicate.

---

## 7. Summary

**The precise failure mechanism.** A shutdown — from either `CC.SHN` or a PFAIL
interrupt — writes a "STARTED" breadcrumb (marker 5, 6 or 7) and then runs a
work list whose length is proportional to the drive's dirty state: outstanding
NAND writes, write-buffer entries, in-use command contexts, pending GC V2P
updates, in-flight deallocates, plus a CellCare table save. Only after all of it
does PROC6 `0x7ffbba61` write the "FINISHED" marker (1 = CLEAN, 2 = PFAIL). The
PFAIL supervisor gives that entire list **25 ms** (`0x7ff830e0 = 25000` µs,
measured against CCOUNT at PROC0 `0x7ffa8346`); at expiry it does not force
completion — it writes marker 7 and **exits**. Three independent defects prevent
the list from finishing: the work can simply exceed the budget (no admission
control exists); the PCIe subsystem discards the PFail shutdown outright if a
link-down already claimed the port (PROC9 `0x7ffaed12`, *"Do nothing"*); and the
System Manager **re-arms** the PFAIL monitor mid-shutdown (PROC0 `0x7ffa8de0` →
`0x7ffa8910` → `0x7ffa8428`), which restarts the deadline from the next PFAIL
edge so the supervisor never gives up. In all three cases the drive dies holding a
STARTED breadcrumb, and the boot-side tests at PROC0 `0x7ffaae35`/`0x7ffaae3d`
then force `0x80000009` forever.

**Does exposure scale with workload?** **Yes, provably.** The budget is a
constant; every item of work is a runtime counter. A busy drive — especially one
doing deallocates — is strictly likelier to latch. This is the mechanism that
explains a five-drive field pattern, and it is the same mechanism as WD's failing
test profile.

**Does an orderly shutdown genuinely avoid it?** **No — it reduces the odds,
substantially, but it does not avoid the defect.** Quiescing I/O and completing
`CC.SHN` before power removal collapses the dominant failure term to near zero,
which is why a UPS-driven OS shutdown is the right operational answer. But the
link-down/PFail race, the monitor re-arm, and the two unconditional monitor exits
all sit on the shared path and are untouched by host behaviour, and `CC.SHN`
itself is downgraded to a PFAIL shutdown if the rails sag while it runs
(PROC6 `0x7ffbbc86`). `KNGND122` is the terminal firmware and WD marks this defect
family *"unable to recover"*. The mitigation is permanent and partial.
