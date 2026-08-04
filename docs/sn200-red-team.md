# SN200 root cause — red team

An adversarial pass over the established conclusion. The brief was to **break**
it, not to review it. Everything below was re-derived from the images with
`xdis.py`/`disany.py`/`litref.py`/`xref.py`, always from a function entry or a
proven branch target; nothing is taken on the authority of the earlier docs.

Labels: **PROVEN** = read out of correctly-decoded instructions or observed
bytes; **INFERRED** = short chain over proven facts; **SPECULATIVE** = plausible,
unsupported.

## Bottom line

Seven attacks. **Two land.**

| # | Attack | Result |
|---|---|---|
| 1 | Marker ordering is not STARTED-then-FINISHED | **held** (and a new, worse finding falls out) |
| 2 | 25 ms is not the work list's budget | **LANDS — the stated mechanism is a non-sequitur** |
| 3 | The 5/6/7 → latch edge does not exist | **held — and is now closed instruction-by-instruction** |
| 4 | The drive's own dump contradicts the stub story | **LANDS — the retrieved section is not an UNEXSTRT stub** |
| 5 | The field pattern is not "power events" | **LANDS — the best-documented latch had no power event** |
| 6 | WD's note describes a narrower bug | **partially lands — and WD's own root cause is a different one** |
| 7 | Alternative mechanisms need no unfinished shutdown | **lands as a live competitor, not as a refutation** |

The claim's **mechanics** survive intact and are in better shape than before: the
ordering is right, and the disputed control-flow edge is now proven. What does
*not* survive is the **causal story built on top of them**, and the confidence
attached to it:

- "The work exceeds the 25 ms budget" **cannot cause a latch.** Expiry writes a
  breadcrumb and exits; it aborts nothing. The binding constraint is hold-up
  energy, which appears nowhere in the firmware and has never been measured.
- The only crash section ever retrieved from real hardware **is not an UNEXSTRT
  stub**, and the log frozen inside it shows the *preceding* shutdown having
  **completed successfully**.
- The best-instrumented latch event in the field log involved **no power event
  and no shutdown at all**.

The honest restatement is: *markers 5/6/7 latch the drive, provably; whether
that is what actually latched these drives is unproven, and the one hardware
sample points elsewhere.*

---

## Attack 1 — is the "STARTED" write really first? **Held.**

If `CC.SHN` wrote marker 5 *after* the work list, a shutdown that died mid-list
would leave the previous marker, not 5, and the narrative inverts.

**PROVEN.** PROC0, the only two loads of `0x7ff83230 = 0x80000005`
(`litref.py -a 0x7ff83230`) are the boot dispatcher and this:

```
7ffa8dc2: { s32i a7,a10,0x24 ; movi a11,1 }
7ffa8dca: { l32r a15,0x7ff83230 ; movi a12,6 ; movi a13,1 }   ; 0x80000005
7ffa8dd2: { s32i a15,a10,0x20 ; movi a14,0 ; movi a15,0 }
7ffa8dda: call8 0x7ffb4fec                       ; submit the record
7ffa8ddd: j 0x7ffa8bdc                           ; StrId 1207 "SYS: Returning shutdown completion"
```

and the System-Area save is requested **downstream of that jump**:

```
7ffa8c1d: l32r a15,0x7ff82b20 -> 0x7ff8c7c4 ; l32i.n a15,a15,0x0
7ffa8c22: beqz a15,0x7ffa8bb2                    ; no SA-saving shutdown -> skip SAM entirely
7ffa8c2b: l32r a10,0x7ff831f8                    ; StrId 1206 "SYS: ShutdownReq --> SAM"
```

with `0x7ffbba61` (PROC6) the sole writer of 1/2. Ordering confirmed. The same
holds on the PFAIL side (`0x7ffa83d7` submits `0x80000006` before the deadline
watch). **Attack fails.**

**But it turns up something the established docs do not say.** The marker-5
submit jumps *straight into* `SYS: Returning shutdown completion`, and
`ShutdownReq --> SAM` is reached only afterwards. If StrId 1207 is the
host-visible `CC.SHN` completion — INFERRED, but it is hard to read it as
anything else — then **a host that waits for `CSTS.SHST = 10b` has not waited for
the System Area save.** That directly weakens mitigation step 3 in
`sn200-shutdown-path.md` §5.2 ("issue `CC.SHN` and *wait for it to complete*"):
the wait terminates before the thing you are waiting for has run. Worth settling,
because it is the single piece of operational advice everything else rests on.

---

## Attack 2 — is 25 ms the work list's budget? **This one lands.**

**PROVEN, and it is the established docs' own §2.3 taken to its conclusion.** At
expiry the PFAIL monitor:

```
7ffa8358: bltu a9,a10,0x7ffa8380     ; inside the deadline -> keep waiting
7ffa835b: l32r a10,0x7ff830cc        ; StrId 1210 "SYS: PFAIL timeout is expired"
   (then 0x7ffa83f2-0x7ffa840a submit 0x80000007, next state 0x7ffa832c = thread exits)
```

It does not abort the save, does not signal any manager, does not force a
marker, does not reset. **Nothing downstream reads the deadline.** Therefore:

- If the work takes 40 ms and the rails hold for 50 ms, PROC6 `0x7ffbba61` still
  runs and writes `0x80000002`, **overwriting** the marker-7 breadcrumb. The
  drive boots clean. Exceeding 25 ms is, by itself, harmless.
- If the rails collapse at 5 ms, the drive latches whether or not 25 ms was
  exceeded.

So the proposition *"exposure scales with workload **because** the 25 ms budget
is fixed while the work is not"* is a **non-sequitur**. The 25 ms is a
stopwatch that names the failure after the fact; it is not a constraint that
anything can violate. The real constraint is **hold-up energy**, and:

- no hold-up figure appears anywhere in the firmware;
- no hold-up figure has been measured on these drives;
- `VCAP has failed, drive is in write protect mode` (StrId 662) proves the
  firmware has a *distinct* posture for degraded hold-up, which none of these
  drives reported.

The workload-scaling **conclusion** survives if you substitute "hold-up window"
for "25 ms budget" — longer work is likelier to outlast the rails, that much is
sound. But the quantitative story collapses with the number. The only shutdown
duration ever observed on this hardware is **6.429 ms** (`SYS: Shutdown time`,
from the retrieved dump). To make 25 ms even *relevant* the work must quadruple,
and to make hold-up the limiter it must exceed an unknown that has never been
bounded. Neither has been shown.

`sn200-certainty.md` grades "**Workload scaling is PROVEN**". It is not. The
*inputs* are proven to be runtime counters; the causal link from "counters large"
to "latch" runs through an unmeasured hold-up window. **Downgrade to INFERRED.**

---

## Attack 3 — does 5/6/7 actually reach the latch? **Held. Gap closed.**

This was flagged as the weakest link: `sn200-firmware-re.md` §7 records that the
5/6/7 handler "appears to branch *past* the UNEXSTRT block" and that the edge was
"still not demonstrated instruction-by-instruction".

**It is now demonstrated. PROVEN, every step.** Boot dispatcher, PROC0:

```
7ffaaea7: l32r a10,0x7ff83230 ; = 0x80000005
7ffaaeaa: { sync ; beq a11,a10,0x7ffaaf6b }
7ffaaeb2: l32r a12,0x7ff830ec ; = 0x80000006
7ffaaeb5: { sync ; beq a11,a12,0x7ffaaf6b }
7ffaaebd: l32r a13,0x7ff830f4 ; = 0x80000007
7ffaaec0: { sync ; beq a11,a13,0x7ffaaf6b }

7ffaaf6b: l32r a15,0x7ff826b8 -> 0x7ff9ff60 ; l32i.n a15,a15,0x4   ; firmware boot mode
7ffaaf70: { sync ; bnei a15,4,0x7ffaacea }   ; NOT load-n-go -> 0x7ffaacea

7ffaacea: l32r a13,0x7ff83338 ; = 0x7fffffff
7ffaaced: l32r a12,0x7ff83438 -> 0x7ff81180  ; the marker-name StrId table
7ffaacf0: and a11,a11,a13                    ; marker & 0x7fffffff = 5, 6 or 7
7ffaacf3: { l32r a10,0x7ff8343c ; ... }      ; LOG 3044 "SYS: ERROR - %s but did not complete successfully!!"
7ffaacfb: l16ui a11,a11,0x0
7ffaacfe: call8 0x7ffb5398
7ffaad01: l8ui a14,a5,0x0                    ; a5 = 0x7ff8d200, crash-section flags byte
7ffaad04: { sync ; ball a14,mask 0x1,0x7ffaac82 }   ; bit 0 already set -> straight to mode 6
   ...  else: memset the 0x7ff8d208 staging buffer, then
7ffaad17: l32r a9,0x7ff83440 ; = 0x00020100   (STUB version)
7ffaad1a: l32r a8,0x7ff82888 ; = 0x48444300   ("HDC\0")
7ffaad4b: s32i a15,a5,0x48   ; "UNEX"         (= staging + 0x40)
7ffaad4e: s32i a14,a5,0x4c   ; "STRT"         (= staging + 0x44)
   ... yields with next state 0x7ffaad7c, which at 0x7ffaaf13 issues
       {buf 0x7ff8d208, len 256, op 0 = write, section 11 = 0x0b CLOG}

7ffaac82: { movi a11,1273 ; movi a5,6 }      ; "SYS: Post Crash startup", startup mode 6 = INVALID
```

Three things follow, and the third is new:

1. **The edge exists.** The earlier worry was a mis-identification, not a
   refutation: there are *two* separate blocks. `0x7ffaacea` is past the
   **prologue** UNEXSTRT block (`0x7ffaac53`–`0x7ffaac74`, which logs StrId 3520),
   but it lands by fall-through exactly at the head of the **stub-staging** block
   at `0x7ffaad01`. Nothing is skipped.
2. **The stub provably targets CLOG (`0x0b`)**, confirming what was previously
   only inferred from the string's wording.
3. **The crash section is not needed for the first latch.** `0x7ffaad04` reaches
   mode 6 on *both* arms. A marker of 5/6/7 yields startup mode 6 directly — the
   `ball` tests at `0x7ffaae35`/`0x7ffaae3d` are the *persistence* mechanism for
   later boots, not the initial trigger. The flowchart in
   `sn200-firmware-flow.md` §3 (`5/6/7 → UNEXSTRT → §2`) routes through a step
   that is not on the critical path. The claim is *stronger* than stated.

**Attack fails.** This is the one place where the established conclusion should
be *upgraded*.

---

## Attack 4 — the drive's own dump. **This one lands, hard.**

The claim cites the retrieved dump as supporting evidence. Read properly, the
same artefact is the strongest single piece of counter-evidence in the corpus.

### 4.1 The retrieved section is not a stub — PROVEN

Two mutually exclusive producers write a crash-section header:

| | version word | tag at `+0x40` | writer |
|---|---|---|---|
| **UNEXSTRT stub** | `0x00020100` (`0x7ff83440`) | `"UNEXSTRT"` (`0x7ffaad4b`/`0x7ffaad4e`) | PROC0 `0x7ffaad01` |
| **Full fault dump** | `0x00020200` (`0x7ff8288c`) | — | PROC0 `0x7ffa29bd`, buffer `0x7ff97df0` |

`litref.py` finds **exactly one** load of each literal in the whole image, so the
two paths are cleanly separated. The full-dump writer sits inside
`0x7ffa1e24`, called once from `0x7ffa1595`, inside the pointer-bound handler
`0x7ffa140c` — which reads the PFAIL status pair `0x82a60140`/`0x82a60148`,
stamps the `"CDI"` magic `0x49444300` at `0x7ff97df0` with size `0xf500`, and
then writes the `"HDC\0"` / `0x00020200` / `"KNGND122"` header.

`docs/sn200-dumps/nvme7-crash-128k.bin`, offset 0:

```
00 43 44 48  00 02 02 00  4b 4e 47 4e 44 31 32 32
```

Version `0x00020200`; `+0x40` is zero. **This is the full-dump writer's output,
not the stub writer's.** The drive we actually have data from did not latch via
an `UNEXSTRT` stamp — its CLOG was armed by something that produced a genuine
fault dump.

(The established docs already record `+0x40 = 0, not UNEXSTRT` and
`version = full-dump value, not the stub` in `sn200-crash-dump-field-results.md`.
Nobody appears to have carried that forward into the root-cause chain, which is
what it contradicts.)

### 4.2 The log frozen inside it shows the previous shutdown *succeeding* — INFERRED, strongly

The decoded records (`sn200-crash-dump-retrieval.md` §4.4.2) read, in order:

```
New Boot: Log restarts here
SYS: Firmware is starting
Firmware Boot Mode : COLD BOOT, EEPROM (Slot 4)
SYS: Shutdown time = 6.429 ms
SYS: PFAIL time    = 6.521 ms
SYS: PFAIL startup                     <- marker 0x80000002, handler 0x7ffaaf8d, mode 2
Scrubbing done: MLP 03470e5c..07909e86
SYS: StartupCpl from ADMIN_MGR (SysInitDone)
SYS: Inited
SysLED: Cmd - DriveReady
```

`SYS: PFAIL startup` is StrId 1265, emitted **only** by the marker-2 handler at
`0x7ffaaf8d`. Marker 2 is written **only** by PROC6 `0x7ffbba61`, at the end of a
save that ran to completion. So on the boot this dump froze on:

- the preceding power-fail shutdown **finished** — no 5/6/7 remained;
- it finished in **6.4 ms**, a quarter of the supposed budget;
- the drive then scrubbed, inited, and reached `DriveReady`;
- and the log ends shortly after.

That is a fault occurring **after a fully successful power-fail recovery, in
normal operation**. It is the opposite of "a shutdown began and did not finish".

**Limits, stated honestly.** (a) Only cores 0–3 of 16 are inside the retrievable
128 KiB, and no level-`0x20` assert record is in that window, so the firing fault
is not in hand. (b) That the dump is *frozen at fault time* rather than a rolling
buffer sampled at read time is INFERRED — but the `New Boot: Log restarts here`
marker at the head and the abrupt end after `DriveReady` both point that way.
(c) We cannot date the dump against the latch: it is possible the section was
armed by this fault long before the observed lockups.

Even with all three caveats, the claim's leg 4 is misread. This dump is not "the
mechanism working correctly on an unloaded drive". It is a record of the
mechanism working correctly **and the drive crashing anyway**.

---

## Attack 5 — the field pattern. **Lands.**

`sn200-field-evidence.md` is observation-only, and it does not say what the claim
says it says.

**Row 2 has no power event and no shutdown.** Talos partitions the drive and runs
`mkfs.xfs` (discard on) on a healthy, running drive → **LATCHED**. Row 6 is the
controlled counterpart: identical sequence with `discard_max_bytes=0` →
survived, zero controller resets.

The established model has **no path** to markers 5/6/7 without either a type-3
shutdown request (`0x7ffa8ee8` sets `[0x7ff8c7c4]`) or a PFAIL edge. Neither
occurred. So the model cannot explain the single best-instrumented latch in the
log. Row 3 further shows FLR / SBR / PERST# / NSSR / link-disable producing "no
effect", so the reset-storm re-arming story does not supply the missing first
cause either.

What *does* fit row 2: a firmware fault on the deallocate path → full crash dump
→ CLOG bit 0 → `0x7ffaae35` → marker 9 → mode 6. That is precisely the
signature found in §4.1.

**"Five drives" is not five samples.** It is an owner report of fleet-wide
incidence with no per-drive marker, section-bit, or dump data. Same model, same
purchase batch, same firmware, same rack, same host BIOS and PCIe topology, same
Ceph/Talos workload with discard enabled by default, same power infrastructure.
Shared-batch hold-up capacitor ageing — the field doc's *own* leading hypothesis,
which the root-cause docs quietly dropped — explains the same correlation without
any firmware defect at all, and is a **different root cause with different
remedies**. It has still not been measured.

---

## Attack 6 — is WD describing our bug? **Partially lands.**

Two problems with leg 2.

**It is narrower than we use it.** The quoted note requires *"both a link down
and a Pfail interrupt … at exactly the same time"*. Our model requires neither
coincidence nor a link-down. Generalising a two-event race into "shutdowns
routinely fail to finish" is not supported by the text.

**WD's own stated root cause for the crashed-drive defect is a different
mechanism.** OM-6850, quoted verbatim in `sn200-firmware-re.md` §8:

> Root Cause: With back-to-back PFails, PFails that occur in the middle of a
> 200 ms power-on window may cause **small loss of usable media. Over time, this
> leads to a crash.**

That is media attrition culminating in a **crash** — an assert that writes a full
dump — not an unfinished shutdown. It matches §4.1's full-dump header and §4.2's
"fault after a successful recovery" far better than the adopted model does.
Citing the release notes as an independent leg while passing over the root cause
WD actually wrote is selective use of the same document.

**And the four legs are not independent.** Legs 2, 3 and 4 are all *interpreted
through* leg 1. Read without that lens, leg 4 contradicts and leg 3 does not
discriminate. Two of the four "independent routes" in `sn200-certainty.md` do not
stand on their own.

---

## Attack 7 — alternatives that need no unfinished shutdown

All reachable in the same boot function, all PROVEN paths:

| Route | Instructions | Outcome |
|---|---|---|
| **Empty / unreadable System Area** | `7ffaae45: l32i.n a11,a7,0x0` / `7ffaae47: bne a11,a6` with `a6 = 0x80000000` → log 3519 → `j 0x7ffaaf08` | marker 9 → **mode 6**. No crash section, no 5/6/7, no shutdown involved. One failed SA read latches the drive. |
| **Real assert / trap** | any fault → `0x7ffa140c` → `0x7ffa1e24` full dump into CLOG → `0x7ffaae35 ball bit 0` → `0x7ffaaf08` | marker 9 → **mode 6**. This is the route the retrieved dump's own header supports. |
| **Incompatible SA** | `7ffaadaa` log 3040 → marker `0x80000003` | mode 0 = FIRST STARTUP → **wipe**, not latch |
| **Erased SysArea** | `7ffaaef1` log 3041 → marker `0x80000003` | mode 0 → wipe |
| **CellCare mismatch** | `7ffaae5e` log 1263 → marker `0x80000003` | mode 0 → wipe |

The first two need no shutdown of any kind. The empty-SA route in particular is a
clean single-point-of-failure: EEPROM wear, an SPI write failure, or NAND
read-retry exhaustion on the System Area produces an identical latch with an
identical presentation, and nothing in the evidence base distinguishes it.

Note also that the last three routes force marker 3 — a **re-init**. A drive
whose SA integrity check fails wipes itself on the next boot. Any diagnosis that
proposes touching the System Area needs to reckon with that.

---

## What would actually settle it

Cheap, read-only, decisive, in priority order:

1. **Read the reason tag and version at `+0x00`/`+0x40` of the crash section on
   the other drives.** `0x00020100` + `"UNEXSTRT"` on any of them proves the
   established mechanism fired at least once. All-zero + `0x00020200` on all of
   them refutes it fleet-wide. This is one 128 KiB read per drive and it decides
   the whole question.
2. **Probe the armed-section bits** (`0x0520` vs `0x0320`). CLOG-only on every
   drive is consistent with both stories; PFCL set anywhere is not.
3. **Lift the 128 KiB ceiling** (`tools/nvme-noreset/max_admin_xfer_ids`) and pull
   cores 4–15 for the level-`0x20` record. If the assert is a Trim/deallocate
   watchdog (StrId 3189/3190), the adopted root cause is wrong and row 2 is
   explained.
4. **Measure hold-up capacitor health.** The one measurement that separates
   "firmware defect" from "end-of-life hardware", still not taken.
5. **Confirm whether StrId 1207 precedes the SAM save on the wire** — i.e.
   whether `CSTS.SHST = 10b` is returned before the System Area is written. This
   is what the primary mitigation depends on.

## Revised grades

| Claim | Was | Should be |
|---|---|---|
| Markers 5/6/7 are written before the work list | PROVEN | **PROVEN** |
| Only PROC6 `0x7ffbba61` writes 1/2 | PROVEN | **PROVEN** |
| Markers 5/6/7 force startup mode 6 | INFERRED / gap | **PROVEN** (§3 above) |
| The UNEXSTRT stub lands in CLOG `0x0b` | INFERRED | **PROVEN** |
| 25 ms is the work list's budget | PROVEN | **refuted as a mechanism** — it enforces nothing |
| Workload scaling causes the latch | PROVEN | **INFERRED**, via an unmeasured hold-up window |
| These drives latched via an unfinished shutdown | PROVEN, "four independent routes" | **INFERRED at best; contradicted by the one hardware sample** |
| Root cause is understood well enough to act on | — | **the actions are unchanged; the diagnosis is not settled** |

The operational advice does not move: quiesce I/O, suppress discard, shut down
cleanly, keep `KNGND122`, plan for replacement. Every candidate mechanism —
unfinished shutdown, deallocate-path assert, media attrition, degraded hold-up —
recommends the same four things. That is *why* the conclusion felt safe, and it
is exactly why it was never stress-tested.
