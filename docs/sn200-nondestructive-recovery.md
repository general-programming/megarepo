# SN200: is there a non-destructive way out of the Post-Crash latch?

Context: five Ultrastar SN200s, all five latched into Post Crash Startup during
power events. The standard recovery (`0xFF` CDW12 `0x0503`) schedules a Drive
REINIT that rebuilds the L2P and **zeroes the namespace**. This document asks
whether the latch can be lifted without that.

Companions: `docs/sn200-firmware-re.md` (firmware RE), and
`docs/sn200-crash-dump-retrieval.md` (getting the dump off first).

Claims are labelled **PROVEN** / **INFERRED** / **SPECULATIVE**.

---

## The short answer

**It depends on which section is armed, and that is cheaply and safely
testable — but the test has been being read wrong.**

1. The latch fires on **either** the CRASH section or the PFAIL section, tested
   independently. **PROVEN.**
2. Clearing PFAIL (`0x0603`) is a bare erase of one EEPROM section and
   schedules nothing. Clearing CRASH (`0x0503`) is what arms the REINIT.
   **PROVEN.**
3. **So if only PFAIL is armed, `0x0603` alone should lift the latch with no
   re-init and no data loss.** — **INFERRED**, and the remaining uncertainty is
   named in §5.
4. **The crucial correction:** armed-ness is signalled by the size probe's
   **status code**, not by the value it returns. `0x00320000` is a fixed
   section reservation. A section that is *not* armed makes the probe **fail**
   with SC `0xC3`. See §1 — this is the part that has been misread.

> **Nothing in this document has been run against hardware.** The
> non-destructive sequence in §6 is **NOT YET VERIFIED**. Do not run it on a
> drive whose data matters until §5's open question is closed.

---

## 1. Armed vs allocated — the misread

**PROVEN.** The crash/pfail size probe handler is at PROC8 overlay
`0x30030d7b`. Both sub-commands share it (`bnei a9,3` selects pfail):

```asm
30030d7b: l32r a11,0x3002ee18          ; = 0x0f860000  <- the "no data" status
30030d7e: { l32r a10,0x3002ee1c ; bnei a9,3,0x30030da4 }   ; sub 3 crash / sub 5 pfail
; --- crash ---
30030d86: l32i.n a14,a10,0x0           ; state word for the CRASH section
30030d88: ball a14,a6,0x30030ba9       ; ARMED -> success path, returns the size
30030d90: <log StrId 1608 "Get Crash Dump Size - no valid crash dump available">
30030da1: j 0x30030a23                 ; returns 0x0f860000
; --- pfail ---
30030da4: l32i.n a8,a10,0x0            ; state word for the PFAIL section
30030da6: ball a8,a7,0x30030bca        ; ARMED -> success path
30030dae: <log StrId 1610 "... no valid pfail crash dump available">
```

`0x0f860000 >> 17 = 0x7C3` → SCT 7, SC `0xC3`. Corroborated host-side:
`gf_nvme_get_crash_dump_size_real` @ `0x8bdf0` in `libdmi_core.so` explicitly
special-cases SC `0xC3` → `-2008` `HDMS_DEV_NO_DATA`, message
*"Crash dump unavailable"*.

So:

| probe result | meaning |
|---|---|
| **succeeds** (any value) | that section **IS armed** |
| **fails, SC `0xC3`** | that section is **NOT armed** |
| fails, SC `0xC5` | rejected by the admin gate, not an armed-ness answer |

**The returned value tells you nothing about armed-ness.** `0x00320000` is a
fixed 3.27 MB section reservation; it does not count down and is not a
progress indicator. Reading "the crash section is armed" off that constant is
exactly the trap — but note the *inverse* of the worry: a probe that
**succeeded** really did mean armed. The value was uninformative; the success
was not.

Do this triage first, on every drive. It is completely read-only:

```sh
cd tools/sn200-fw && sudo ./check-latch-state.sh /dev/nvmeN
```

It runs the two probes, classifies each section, and states plainly whether a
non-destructive path is even possible on that drive.

---

## 2. The latch predicate — either section arms it

**PROVEN.** PROC0 `0x7ffaac30` is the startup-state decision function.

```asm
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }   ; a9 = section-state byte
7ffaae35: { sync/extw ; ball a9,a0,0x7ffaaf02 }        ; condition A
7ffaae3d: { sync/extw ; ball a9,a2,0x7ffaaf02 }        ; condition B
...
7ffaaf02: l32r a10,<LOG 3042 "SYS: Detected a CRASH or PFCRASH section.">
7ffaaf05: call8 0x7ffb5398
7ffaaf08: l32r a11,0x7ff83474          ; = 0x80000009  <- POST CRASH Startup
7ffaaf0b: { s32i a11,a7,0x0 ; j 0x7ffaae69 }
```

Two independent `ball` (branch-if-all-bits-set) tests on the **same state
byte**, with different masks, both jumping to the same forced state
`0x80000009`. That is `if (armed_crash || armed_pfail) -> POST CRASH`.

**Consequence:** clearing one section lifts the latch **only if the other was
never armed**. That is precisely what `check-latch-state.sh` determines.

The same `ball`-on-a-state-word shape gates the size probes (§1), which is why
the probes are a faithful proxy for the boot predicate rather than a
coincidence.

---

## 3. `0x0603` really is the benign one — PROVEN

The two erase sub-command bodies are byte-for-byte identical **except for one
constant**, and that constant is the EEPROM section id:

```asm
; sub 5  (CDW12 0x0503, CRASH)          @0x300337fe
  s32i a7,a5,0x120
  s32i a10,a5,0x118
  movi a9,0xb                  ; <-- section 11
  s32i a9,a5,0x11c
  call8 0x30030aa0             ; the erase primitive

; sub 6  (CDW12 0x0603, PFAIL)          @0x3003374f
  s32i a7,a5,0x120
  s32i a10,a5,0x118
  movi a9,0xa                  ; <-- section 10
  s32i a9,a5,0x11c
  call8 0x30030aa0
```

Cross-referenced against the EEPROM section-name array (StrIds 1214–1228,
`sn200-firmware-re.md` §2): **idx 10 = `PFail Crash Dump`, idx 11 = `Crash
Dump`**. Exact match.

> This **settles the sub-command → target mapping**, which
> `sn200-firmware-re.md` §4 had flagged as INFERRED-from-string-order and
> called "the main remaining blocker". Sub 5 = Crash, sub 6 = PFail is now
> PROVEN from the section id the handler writes, not from string ordering.
> It also confirms sub 3 (SBL EEPROM) and sub 4 (Drive Uninit) are *not* what
> these two touch.

Sub 6 does nothing else. No second call, no marker write, no conditional.

### The crash-only second operation, and its condition

**PROVEN, and the condition is new.** On success the crash path branches to
`0x30033704`:

```asm
30033704: l32r a14,0x30033350          ; -> global 0x7ff87c64
30033707: l32i.n a14,a14,0x0
30033709: { sync/extw ; bnei a14,6,0x300335bf }   ; <-- if mode != 6, SKIP
30033711: { s32i a7,a12,0x128 ; movi a15,0x25 }
30033719: { s32i a15,a12,0x118 ; mov a11,a6 }     ; verb 0x25, not a section id
30033721: call8 0x30030aa0                        ; SECOND operation
          ; failure here logs StrId 2933 "Schedule reinit after crash dump erase failed"
```

The reinit scheduling is **conditional on the global at `0x7ff87c64` being 6**.
That global is also read at the top of the admin gate (`0x7ffa6b1b`,
`bnei a8,6,...` — see `sn200-crash-dump-retrieval.md` §1.5), where 6 is the
latched/diagnostic state.

**So on a latched drive the condition is satisfied and `0x0503` will schedule
the reinit.** There is no "clear the crash dump quietly while latched" — the
latched state is exactly when it bites. (**INFERRED**, high confidence: it
follows if 6 is the latched mode, which the admin gate strongly implies.)

Note `0x25` (37) is passed where the erase bodies pass a section id, and the
EEPROM section enum only runs to 14 — so the second call is a different verb on
the same primitive, not an erase of a 38th section.

---

## 4. What the safe clear does *not* touch

**PROVEN (negative).** `0x0603` reaches `0x30030aa0` with section id `0x0a` and
nothing else. It sets no boot marker: the only marker-writing path in the OAM
erase family is the crash path's second call, and the pfail path has no second
call. The `Drive REINIT requested` marker (boot-marker 3) is what rebuilds the
L2P, and only the crash path arms it.

Field observation is consistent: after `0x0603` the pfail size probe stopped
returning a size **immediately**, while the drive was still latched — i.e.
synchronous erase, no scheduled work, and the latch persisted because the
*crash* section was still armed.

> A note on that observation. It was recorded as "the probe read zero". Per §1
> a non-armed section makes the probe **fail** with SC `0xC3` rather than
> succeed with a zero value, so what was most likely seen is a failed command
> whose value field was reported as 0. Both readings mean the same thing here
> (pfail no longer armed), but it is worth knowing which, because "succeeds
> with 0" and "fails with 0xC3" are different states and only the second is
> the firmware's documented not-armed signal. `check-latch-state.sh`
> distinguishes them explicitly.

---

## 5. THE OPEN QUESTION — which section does an UNEXSTRT stub arm?

This is the hinge, and it is **not yet settled**.

**PROVEN** — the UNEXSTRT stub writer, PROC0 `0x7ffaad01`:

```asm
7ffaad01: l8ui a14,a5,0x0
7ffaad04: { sync/extw ; ball a14,a0,0x7ffaac82 }
7ffaad17: l32r a9,0x3002... ; = 0x00020100        ; version
7ffaad1a: l32r a8,...       ; = 0x48444300        ; "HDC\0"
7ffaad1d: s32i.n a8,a5,0x8                        ; [buf+0x08] = magic
7ffaad1f: s32i.n a9,a5,0xc                        ; [buf+0x0c] = version
7ffaad21: rsr a12,234                             ; CCOUNT
7ffaad43: s32i.n a12,a5,0x18                      ; [buf+0x18] = timestamp
7ffaad45: l32r a14,... ; = 0x53545254             ; "STRT"
7ffaad48: l32r a15,... ; = 0x554e4558             ; "UNEX"
7ffaad4b: s32i a15,a5,0x48                        ; [buf+0x48] = "UNEX"
7ffaad4e: s32i a14,a5,0x4c                        ; [buf+0x4c] = "STRT"
```

with `a5 = 0x7ff8b4f8` (a RAM staging buffer) set at function entry. Earlier in
the same function, at `0x7ffaac53`, a state byte is read, ANDed with `0xFE`
then `0xFD` (**clearing bits 0 and 1**) and written back — evidently the
"header valid/complete" flags whose clearing produces the *invalid* third state
(StrIds 1279/1282).

**StrId 3520 says "writing UNEXSTRT stub header to crash area."** If "crash
area" means the CRASH section (EEPROM id `0x0b`), then **every unclean stop
arms the section that only the destructive clear can release**, and for these
five drives a non-destructive recovery is impossible. If it lands in PFAIL,
`0x0603` suffices.

The string is suggestive but a string is not a section id. **This is being
traced now and this document must not be acted on until it is resolved.**

Why it matters so much here: the drives latched during *power events*. A power
event produces both a plausible PFAIL record **and** an unclean stop. If
UNEXSTRT arms CRASH, the PFAIL section being armed is a red herring and
`0x0603` will never lift the latch.

---

## 6. Candidate non-destructive sequence — **UNVERIFIED, DO NOT RUN YET**

Stated so it can be reviewed, not so it can be executed. Steps 1–3 are
read-only and safe today. Step 4 is the unverified one.

### Step 1 — triage (read-only, safe)

```sh
cd tools/sn200-fw
sudo ./check-latch-state.sh /dev/nvmeN
```

**If it says the CRASH section is armed, stop.** There is no known
non-destructive path; go to `sn200-crash-dump-retrieval.md` and treat any
further step as accepting total data loss.

### Step 2 — get the dumps off first (read-only, safe)

```sh
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvmeN
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin
```

Reads are PROVEN side-effect-free and `0xC6` cmd `0x20` is inside the
Post-Crash allow-list, so this works while latched and can be resumed. Do this
even if you intend to stop afterwards: the dump names the assert, and with five
drives the pattern across them is worth more than any single one.

### Step 3 — copy everything off the machine

Everything after this point changes drive state.

### Step 4 — the safe clear — **NOT YET VERIFIED**

Only if step 1 said **PFAIL armed, CRASH empty**, and only once §5 is closed:

```sh
# ⚠ UNVERIFIED. Erases the PFail Crash Dump EEPROM section (id 0x0a).
# It is irreversible: the pfail dump is gone. It should NOT schedule a reinit.
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 \
     --cdw10=0 --cdw12=0x0603 --data-len=0
```

**This is the first irreversible step.** It destroys the pfail dump (which is
why step 2 comes first) but should not touch the L2P, the system area, or any
boot marker.

Then re-run step 1. If PFAIL now reads EMPTY and CRASH still reads EMPTY, the
latch predicate should no longer fire on the next startup.

### Step 5 — a CLEAN stop, then start

The latch is self-sustaining: `SYS: UNEXSTRT detected` means **any start not
preceded by a recorded clean shutdown re-arms a crash section**. So the restart
must be a real NVMe shutdown, not a reset.

```sh
BDF=0000:xx:00.0
echo $BDF > /sys/bus/pci/drivers/nvme/unbind   # issues CC.SHN=01b, polls CSTS.SHST
sleep 10
echo $BDF > /sys/bus/pci/drivers/nvme/bind     # drives CC.EN 0->1
```

`unbind` goes through `nvme_dev_disable(shutdown=true)` and is the **only**
in-band stop that produces a clean-shutdown marker. `nvme reset`, NSSR, FLR,
SBR and link-disable all drop `CC.EN` or the link without `CC.SHN` and are each
themselves another unclean start. (`sn200-firmware-re.md` §6, §7.)

Whether the lighter FAST_RESTART path consumes the state, or whether a true
cold power cycle is still required, is **unresolved** — but a cold cycle
following a clean `unbind` is strictly safer than one following a reset.

### What NOT to do

| command | effect |
|---|---|
| ☠ `0xFF` CDW12 `0x0503` | schedules Drive REINIT → **rebuilds L2P, zeroes the namespace** |
| ☠ `0xFF` CDW12 `0x0303` | erase SBL EEPROM → **permanent brick** |
| ☠ `0xFF` CDW12 `0x0403` | Drive Uninit |
| ☢ `0xDD` | secure purge, irreversible, no confirmation argument |
| ☠ `nvme wdc get-crash-dump` | reads the dump then **automatically fires `0x0503`** |

`0x0303` and `0x0403` are adjacent to `0x0503`/`0x0603`. Do not sweep, do not
typo, and do not let a shell history search pick the wrong one.

---

## 7. Honest status

**PROVEN**
- Armed-ness is the size probe's status code (success vs SC `0xC3`), not its
  value. `0x00320000` is a fixed reservation.
- The boot latch tests CRASH and PFAIL independently; either arms it.
- Sub 5 → EEPROM section `0x0b` (Crash Dump), sub 6 → `0x0a` (PFail Crash
  Dump). This also retires the long-standing "sub-command mapping" blocker.
- The pfail erase has no second operation and writes no marker.
- The crash erase's reinit scheduling is conditional on the global at
  `0x7ff87c64` being 6.

**INFERRED**
- 6 is the latched/diagnostic mode, so on a latched drive `0x0503` always
  schedules the reinit.
- If only PFAIL is armed, `0x0603` alone lifts the latch.

**UNRESOLVED — blocks the whole procedure**
- Which section an UNEXSTRT stub arms (§5). If CRASH, non-destructive recovery
  is impossible for power-event latches.
- Whether the armed bits are sticky until explicitly erased, or cleared by a
  successful clean startup.
- Whether a clean `unbind`/`bind` cycle is sufficient or a cold power cycle is
  mandatory.

**UNTESTED AGAINST HARDWARE**
- All of it. Steps 1–3 of §6 are read-only and safe to run today on all five
  drives; running step 1 across all five and comparing is itself valuable
  evidence and costs nothing.
