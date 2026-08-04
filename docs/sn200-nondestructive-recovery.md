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

**No. For these five drives, `0x0503` is unavoidable — and the reason is
UNEXSTRT.** The detail and the one narrow exception are below.

1. The latch fires on **either** the CRASH section or the PFAIL section, tested
   independently. **PROVEN.**
2. Clearing PFAIL (`0x0603`) is a bare erase of one EEPROM section and
   schedules nothing. Clearing CRASH (`0x0503`) is what arms the REINIT.
   **PROVEN.**
3. So if only PFAIL were armed, `0x0603` alone would lift the latch with no
   re-init and no data loss. **But that case does not arise after a power
   event** — see 4.
4. **`UNEXSTRT` arms the CRASH section, not PFAIL. PROVEN (§5).** Every start
   not preceded by a recorded clean shutdown stamps a stub into EEPROM section
   `0x0b` (Crash Dump). A power event is exactly such a start. So a
   power-event latch sets **bit 0**, and only `0x0503` clears it.
5. **The armed bits are sticky. INFERRED, high confidence (§5.1).** No clean
   startup releases them; the section-state manager only ever *sets* bits.
6. **The crucial correction to your triage:** armed-ness is signalled by the
   size probe's **status code**, not by the value it returns. `0x00320000` is a
   fixed section reservation. A section that is *not* armed makes the probe
   **fail** with SC `0xC3` (§1).
7. **But the size probe is NOT the boot latch.** They read different storage
   with different bit numbering. Boot latch: PROC0 RAM byte, **bit 0 = CRASH,
   bit 2 = PFAIL**. Size probe: hardware word at `0x82a60008`, **bit 6 =
   CRASH, bit 7 = PFAIL**. Nothing traced connects them. Treat the probe as a
   strong proxy, never as an equivalence (§2).

> ### ⚠ The one narrow exception, and why it is probably not reachable
>
> **PROVEN:** the reinit is not unconditional. The crash-erase schedules it
> only when the global at `0x7ff87c64` equals 6 — the latched/diagnostic mode:
>
> ```asm
> 30033709: { sync/extw ; bnei a14,6,0x300335bf }   ; not 6 -> plain success tail
> ```
>
> So `0x0503` fired from a **normally booted** drive erases the crash section
> **without scheduling the reinit**.
>
> **But that is circular and I do not believe it is reachable.** A set crash
> bit forces the boot latch, which forces marker `0x80000009`, which is what
> writes mode 6. To boot normally you must first clear the crash bit; to clear
> it safely you must first boot normally. The `bnei` exists to avoid scheduling
> a reinit on a drive that was never latched — not to give you a quiet clear.
>
> The only crack: the boot latch reads its byte from `0x7ff8b4f8` while the
> section-state manager writes `0x7ff8d200`. **Nothing proves those two are
> kept in sync.** If they can diverge, a drive could boot normally with the
> manager's crash bit still set, and then `0x0503` would be non-destructive.
> That is **SPECULATIVE** and must not be planned around.
>
> **Nothing here has been run against hardware.** Steps 1–3 of §6 are read-only
> and safe today; step 4 is not.

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

### The bits — **PROVEN**

The `ball` operands are **single-bit masks, not registers**: for `ball`/`bany`
the `r` field is an immediate and the mask is `1 << r`. (An earlier reading of
this document had them as registers, which is what made the masks look like
nonsense. `tools/sn200-fw/xdis.py` now decodes them correctly.)

Evidence: every genuine register-operand branch avoids `a0` (return address)
and `a1` (SP) — `beq` (n=888), `bgeu` (n=621), `bltu`, `bne`, `bnone` all show
`r` in 2..15 and **never** 0 or 1. `ball` (n=180) and `bany` (n=171) have `r=0`
as their commonest value, `r=1` next. A field that constantly names a0/a1 is
not a register field.

Confirmed semantically in PROC0's section-state manager (`0x7ffab010`), which
does the same thing with plain 3-byte bit ops — visible in one line:

```asm
7ffab015: { l32r a6,0x7ff829a8 ; movi a9,4    }   ; a9 = 4 = bit 2
7ffab025: { movi a11,251       ; beqz a10,... }   ; a11 = 0xFB = ~(bits 2,3)
7ffab03a: and a10,a10,a11                         ; clear the PFail state bits
7ffab03d: { or a10,a10,a9 ; movi a11,1280 }       ; SET bit 2, and StrId 1280 =
                                                  ; "PFail Crash Dump section is erased"
```

and the crash-side block at `0x7ffab181` uses `and 0xFE ; or 1` (**bit 0**)
with StrId 1277 `"Crash Dump section is erased"` and EEPROM id `0x0b`.

So the boot latch reads:

```asm
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }
7ffaae35: { sync/extw ; ball a9,mask 0x1,0x7ffaaf02 }   ; bit 0 -> CRASH armed
7ffaae3d: { sync/extw ; ball a9,mask 0x4,0x7ffaaf02 }   ; bit 2 -> PFCRASH armed
```

Bits 1 and 3 are the second bit of each section's 2-bit state — the
erased/detected/invalid trichotomy of StrIds 1277–1282.

### ⚠ The size probe is a proxy, NOT the same storage

```asm
30030d7e: { l32r a10,0x3002ee1c ; bnei a9,3,0x30030da4 }   ; *(0x3002ee1c) = 0x82a60008
30030d86: l32i.n a14,a10,0x0
30030d88: { sync/extw ; ball a14,mask 0x40,0x30030ba9 }    ; crash = bit 6
30030da6: { sync/extw ; ball a8, mask 0x80,0x30030bca }    ; pfail = bit 7
```

Both read the **same word at `0x82a60008`**, a hardware/SPI-window address —
**not** the PROC0 RAM byte, and with **different bit numbering** (6/7 vs 0/2).
No code was found that propagates one into the other.

**Consequence, and it is a real limitation:** "the probe says EMPTY" is strong
evidence the latch will not fire, but it is **not proof**. In particular,
whether an UNEXSTRT-induced *invalid* state would trip the boot latch while the
probe reports SC `0xC3` could **not be determined**. Since the boot latch is a
single-bit test on bit 0 and the section state is 2 bits, an invalid state
(bit 0 clear) should *not* trip it — but that is INFERRED from the bit layout,
not traced.

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
latched state is exactly when it bites.

**INFERRED, and well corroborated.** Three independent facts agree that 6 is
the latched/diagnostic mode:

1. the admin gate at `0x7ffa6b18` opens with `bnei a8,6,<skip the gate>` — the
   Post-Crash restriction applies *only* when the global is 6;
2. the crash-erase reinit is likewise conditional on it being 6;
3. the field observation from a latched drive: `0xFF`/`0x0004`
   (`gf_nvme_sys_init_done`, a read) returned `0x00000601`, i.e. **startup type
   6** (`sn200-firmware-re.md` §2, §11).

Note this cuts against any hope of a loophole: there is no reachable state in
which the drive is latched *and* `0x0503` skips the reinit. The `bnei` exists
to avoid scheduling a reinit on a drive that was never latched, not to give you
a quiet clear.

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

## 5. UNEXSTRT arms the CRASH section — **PROVEN**

This was the hinge, and the answer is the unwelcome one.

**(a) The gate that guards the stub write is the CRASH predicate.** Reached only
from `0x7ffaaecb` (`beq a11,a14,0x7ffaad01` with a14 = `0x80000009`):

```asm
7ffaad01: l8ui a14,a5,0x0
7ffaad04: { sync/extw ; ball a14,mask 0x1,0x7ffaac82 }   ; mask 0x1 = bit 0 = CRASH
```

Branch-taken goes to "log `SYS: Post Crash startup`, set mode 6, do not stamp".
**Fall-through writes the stub — i.e. the stub is written precisely when the
crash bit is CLEAR. Its purpose is to make the crash bit true.**

**(b) The write goes through the CRASH section handle.** `0x7ffaad51` loads
`0x7ff825f8` → **`0x7ff85364`**. That is the same global the crash-side error
handler pairs with **section id `0x0b`** (`0x7ffab23a`, then
`{ l32i a14,a14,0x4 ; movi a12,0xb }`). The PFail handle is a *different*
global, `0x7ff82674` → `0x7ff85374`, paired with id `0x0a` — and it is **not
referenced anywhere in the UNEXSTRT block**.

**(c) The stub-write failure path reports section `0x0b`.** The wait loop is
`0x7ffaad7c`; its error arm `0x7ffaaf13` ends in `call8 0x7ffb4fec` with the
section-id register holding `0x0b`.

**(d)** StrId 3520 says "to crash area", consistent with (a)–(c).

Stub contents (**PROVEN**): magic `0x48444300` (`"HDC\0"`) at `[buf+0x08]`,
version `0x00020100` at `[buf+0x0c]`, CCOUNT at `[buf+0x18]`, `"UNEX"`/`"STRT"`
at `[buf+0x48]`/`[buf+0x4c]`, with `buf = 0x7ff8b4f8`.

### Why this kills the hypothesis

A power event is a start not preceded by a recorded clean shutdown. It stamps
an UNEXSTRT stub into the **Crash Dump** section, setting **bit 0**. The boot
latch's first test is exactly bit 0. So:

- the latch fires because of the CRASH bit, whatever PFAIL is doing;
- `0x0603` clears bit 2 and cannot help;
- clearing bit 0 requires `0x0503`, which on a latched drive (mode 6) always
  schedules the reinit.

Worse, it is self-perpetuating: each of the ~5 s controller resets while
latched is itself another unclean start.

### 5.1 The bits are sticky — **INFERRED, high confidence**

The whole section-state manager (`0x7ffab010..0x7ffab290`), which is what runs
on the normal boot paths, contains **no bit-clearing operation** other than
read-modify-write pairs that immediately re-set the same bit
(`and 0xFE ; or 1`, `and 0xFB ; or 4`, …). It only ever **sets**. There is no
"clear after successful read" anywhere in the crash-dump read paths.

The one genuine clear is `0x7ffaac53..0x7ffaac71` — read byte, `AND 0xFE`,
`AND 0xFD`, store — which zeroes the CRASH section's 2-bit state and is
immediately followed by the UNEXSTRT log. **That is a re-arm, not a release.**

So a clean startup does not release the bits. Only the OAM erase does.

## 6. What to actually do

The non-destructive sequence this document set out to find **does not exist for
a power-event latch**. What follows is the best available procedure given that.

### Step 1 — triage all five (read-only, safe, do this now)

```sh
cd tools/sn200-fw
sudo ./check-latch-state.sh /dev/nvmeN
```

Run it on every drive and keep the output. It costs nothing and modifies
nothing, and the pattern across five drives is worth more than any single
result. Two outcomes matter:

- **CRASH armed** (the expected case after a power event) — there is no
  non-destructive path. Continue to step 2, then accept the loss or park the
  drive.
- **PFAIL armed, CRASH empty** — the one case where `0x0603` alone might
  suffice. Given §5 this should be rare; if you see it, say so, because it
  means the drive latched for a reason other than an unclean stop.

Remember §2: a clean triage is strong evidence, not proof.

### Step 2 — get the dumps off, always (read-only, safe)

```sh
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvmeN
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin
```

Do this on all five **before** anything else, and especially before any clear.
With five drives the cross-drive pattern is the real prize: if they all name
the same assert, that is a far stronger case to WD (or for a controlled
workaround) than any single dump. Reads are side-effect-free and resumable.

### Step 3 — copy everything off the machine

Everything after this changes drive state.

### Step 4 — accept the cost, or park the drive

If CRASH is armed, the only known release is:

```sh
# ☠ IRREVERSIBLE AND DESTRUCTIVE. Schedules Drive REINIT -> rebuilds the L2P
# -> the namespace comes back FULLY PROVISIONED AND ENTIRELY ZERO.
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 \
     --cdw10=0 --cdw12=0x0503 --data-len=0
```

**This is the point of no return.** There is no partial or recoverable form of
it. If the data on a drive is worth more than the drive, leave it latched and
powered down — the media is intact, the data is intact, and only the boot path
refuses. A future firmware-level or vendor-side recovery remains possible in a
way it does not once the reinit has run.

If a drive's data is expendable, `0x0603` first (synchronous, provably
side-effect-free, and it removes the pfail record) then `0x0503`, then step 5.

### Step 5 — a CLEAN stop, then start

The latch is self-sustaining: **any** start not preceded by a recorded clean
shutdown stamps a fresh UNEXSTRT stub into the crash section (§5). So the
restart must be a real NVMe shutdown, not a reset.

```sh
BDF=0000:xx:00.0
echo $BDF > /sys/bus/pci/drivers/nvme/unbind   # issues CC.SHN=01b, polls CSTS.SHST
sleep 10
echo $BDF > /sys/bus/pci/drivers/nvme/bind     # drives CC.EN 0->1
```

`unbind` goes through `nvme_dev_disable(shutdown=true)` and is the **only**
in-band stop that produces a clean-shutdown marker. `nvme reset`, NSSR, FLR,
SBR and link-disable all drop `CC.EN` or the link without `CC.SHN` and are each
themselves another unclean start that re-arms the section.

Whether the lighter FAST_RESTART path consumes the state or a true cold power
cycle is still required is **unresolved** — but a cold cycle following a clean
`unbind` is strictly safer than one following a reset.

### The prevention that actually matters here

Five drives, five power events. Since every unclean stop re-arms the crash
section, the fix is upstream of the drives:

- make sure the host issues a real NVMe shutdown on power-down — a UPS that
  triggers an orderly OS shutdown, not just a delayed cut;
- never yank power or hard-reset a chassis with these drives live;
- keep them off the deallocate/TRIM workloads in `sn200-firmware-re.md` §8
  (`mkfs -K`, no `discard` mount option, no whole-device `fstrim`).

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
  **Bit 0 = CRASH, bit 2 = PFAIL** in the PROC0 state byte.
- `ball`/`bany` take a single-bit immediate mask, not a register operand.
- Sub 5 → EEPROM section `0x0b` (Crash Dump), sub 6 → `0x0a` (PFail Crash
  Dump), read from the section id each handler writes. This retires the
  "sub-command mapping" item `sn200-firmware-re.md` §4 called its biggest
  blocker, and confirms the full table (sub 0→6 System Area, 1→3 Bad Block
  list, 2→9/8 BIST Script+Status, 4→Drive Uninit).
- The pfail erase has no second operation and writes no marker.
- **UNEXSTRT stamps its stub into the CRASH section (`0x0b`).**
- The crash-erase's reinit is conditional on `*(0x7ff87c64) == 6`, and 6 is the
  latched/diagnostic mode.
- The size probe reads a different object (`0x82a60008`, bits 6/7) from the
  boot latch (PROC0 byte, bits 0/2).

**INFERRED**
- The armed bits are sticky: nothing but the OAM erase clears them.
- On a latched drive `0x0503` therefore always schedules the reinit.

**COULD NOT DETERMINE**
- Whether an UNEXSTRT-induced *invalid* state trips the boot latch while the
  size probe reports SC `0xC3`. The two predicates read different objects and
  the code propagating one to the other was not found.
- Whether the boot latch's byte (`0x7ff8b4f8`) and the section manager's byte
  (`0x7ff8d200`) are kept in sync. If they can diverge, the §-summary exception
  becomes reachable — but this is SPECULATIVE.
- What verb `0x25` (the crash path's second call) actually does. It is *not* an
  EEPROM erase: it writes no section id, and it targets a different request
  block. Consistent with "write the reinit boot marker", not proven. The erase
  primitive `0x30030aa0` itself could not be disassembled (not `entry`-aligned
  in the flat overlay image), so its verb dispatch is unresolved.
- Whether a clean `unbind`/`bind` cycle suffices or a cold power cycle is
  mandatory.

**UNTESTED AGAINST HARDWARE**
- All of it. Steps 1–3 of §6 are read-only and safe to run today on all five
  drives.
