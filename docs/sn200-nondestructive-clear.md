# Clearing the Post-Crash latch without the wipe — three attacks, three results

Firmware `KNGND122`. **No hardware was touched.** Static analysis plus p-code
execution (`tools/sn200-fw/pcode.py`, `sn200_oracle.py`).

The brief: `0xFF`/`0x0503` erases CLOG (EEPROM section 11) and its *resume*
handler posts re-init verb `0x25` — but only when `*(0x7ff87c64) == 6`
(`bnei a14,6` at `0x30033709`). The wipe is one conditional in one resume
handler, not an intrinsic cost of clearing the latch. Break the circularity.

**Result up front:**

| attack | result |
|---|---|
| 1 — another host path to "verb 3, section 11" | **Clean negative, and now exhaustive.** Every producer of an EEPROM/OAM request in `PROC8` is enumerated by construction (11 sites, from a pointer that exists as 6 words in 20 images). Exactly one builds verb 3 + section 11: the `0x0503` arm. The only other verb-3 producer hardcodes section **4** and its opcode is rejected by the Post-Crash gate. |
| 2 — dodge the mode-6 test | **Clean negative in band.** The gate re-reads the global at resume time (PROVEN by execution across modes 0–9), so it is not frozen at submit — but the global has exactly **three** writers, none host-reachable, and `0x0503` is single-stage so there is no inter-stage window. One new writer found that prior docs missed. |
| 3 — is the re-init survivable | **The re-init destroys nothing at runtime — it is a 4-byte record value.** The wipe happens on the *next boot*. Both reachable marker values (3, 4) route to startup type 0, so there is no lighter variant. **There is no redundant L2P**; a wiped mapping table is not reconstructible from media. The live consequence is that the pending re-init is *cancellable* by any later marker write in the same power-on — and one host-reachable later writer exists that nobody has examined. §4. |

Labels: **PROVEN** = read off correctly-lifted instructions or produced by
executing them. **INFERRED** = short chain over proven facts. **SPECULATIVE** =
neither.

---

## 1. Attack 1 — every constructor of an EEPROM request, by construction

`sn200-marker-write.md` §3.3 flagged, honestly, that its verb-1 sweep was *not*
exhaustive: handlers re-base the request pointer (`0x0007` writes `+0x118`…
`+0x12c` through `req+0xA0`), so a struct-offset scan misses them. Repeating
that method for verb 3 would inherit the same hole. This section uses a
different anchor that has no hole.

### 1.1 The anchor: one request executor, one pointer, six words — PROVEN

The `0xFF` erase coroutine loads its work-item callback once, at entry:

```asm
3003353c: entry a1,0x30
30033543: l32r a6,0x30033364        ; = 0x7ffa972c
...
30033806: { s32i a10,a5,0x118 ; movi a9,11 }      ; verb 3 (a10), section 11
3003380e: { s32i a9,a5,0x11c  ; mov a12,a5 }
30033816: call8 0x30030aa0                        ; = runtime 0x7ffb9768, enqueue(node, a6, req)
```

`0x7ffb9768` is **not** an OAM-specific call — it is a generic list append
(`s32i a3,a2,0x8` callback, `s32i a4,a2,0xc` arg, link into `[0x7ff96b04+0x10]`)
with **195** call sites in the resident image alone. The specificity is the
**callback**, `0x7ffa972c`: a coroutine whose `a2` is the request object (it
writes `+0x110`, `+0x114`, `+0x44`…`+0x4b`, and the completion status `+0x188`).
Every EEPROM/System-Area request in `PROC8` is posted with this callback.

A raw byte sweep of all 20 flat images for the word `0x7ffa972c` returns
**six** occurrences, all 4-aligned, all literal-pool entries:

```
PROC8_30000000  30023160 (ov3)   3002ebbc (ov18)  30032c58 (ov21)
                30033364 (ov22)  3003e49c (ov33)
PROC8_7ff80000  7ffa1d78 (resident)
```

**No jump table, dispatch table or descriptor anywhere contains it**, and
`movi` cannot synthesise a value that wide. So the only way to obtain the
pointer is an `l32r` of one of those six words, and enumerating those `l32r`s
enumerates *every* producer — regardless of how the handler re-bases the request
afterwards.

> **Overlay `l32r` must be resolved in runtime space, and `litref.py` does not.**
> The displacement arithmetic is delta-invariant (`((pc+3)&~3)` and the delta are
> both 4-aligned), so the *static* literal address litref prints is right — but
> the *value* must be read at `literal_static + overlay_delta`. Two consequences:
> `litref -v 7ffa972c` reports a phantom hit at `PROC8 0x3003dab5` (ov31 code
> whose literal resolves into resident space, not into ov3's pool), and it could
> in principle *miss* an overlay `l32r` that loads the resident pool word
> `0x7ffa1d78` — which is inside the ±256 KiB `l32r` reach of the overlay window.
> `/tmp`-scratch sweep re-run overlay-correctly (per-overlay delta, value read
> from the overlay window when the runtime literal falls inside it and from the
> resident image otherwise) gives **9 overlay sites + 2 resident sites**, drops
> the ov31 phantom, and finds no resident-pool loader in any overlay.

### 1.2 The complete producer table — PROVEN

Verb/section immediates below are read off `pcode.py`'s lift (the SLEIGH spec),
not off `xdis.py`.

| producer (static / runtime) | ov | verb `+0x118` | section `+0x11c` | what it is |
|---|---|---|---|---|
| `0x300234de` / `0x7ffbc3e6` | 3 | **10** | — | |
| `0x3002fdfa` / `0x7ffbd3c2` | 18 | **40** (`0x28`) | — | |
| `0x30030c5f` / `0x7ffbe227` | 18 | reg `a6` = **2** at entry | **7** | *Get Crash Dump Size* (1122-byte read) |
| `0x30030cab` / `0x7ffbe273` | 18 | reg `a6` = **2** at entry | **7** | *Get PFail Crash Dump Size* |
| `0x3003144a` / `0x7ffbea12` | 18 | **38** (`0x26`) | 0 | |
| `0x30033073` / `0x7ffbc47b` | 21 | **21** (`0x15`) | — | FW-table update |
| **`0x300330a9`** / `0x7ffbc4b1` | 21 | **3 — ERASE** | **4** | **FW Erase command**, §1.3 |
| `0x30033757`…`0x3003380e` | 22 | 3 / 1 / `0x25` | 6, 3, 9, 8, 13, **11**, 10 | the `0xFF` erase family |
| `0x300338c4` / `0x7ffbc58c` | 22 | **42** (`0x2a`) read | 6 | `0xFF`/`0x0007` |
| `0x3003ef2c` / `0x7ffbcc34` | 33 | **10** | — | |
| `0x7ffb1903` | resident | **10** | — | `Admin_Sanitize` |
| `0x7ffb1987` | resident | **10** | — | `Admin_Sanitize` |

**Exactly one site in all of `PROC8` constructs verb 3 with section 11:
`0x30033806`/`0x3003380e`, the `0xFF`/`CDW12=0x0503` arm.** There is no second
door, and this time the claim does not rest on an offset scan.

Note the shape of this result: it is **opcode-independent**. It does not matter
that `0xE6` has never been walked, or what the concurrent `0xCA` work finds —
whatever those handlers do, they can only reach the EEPROM through one of the
twelve rows above, and none of the other eleven can name section 11.

### 1.3 The near-miss, and why it is not a way in — PROVEN

The one other verb-3 producer is worth spelling out because it is the only
thing in the firmware that looks like a second eraser:

```asm
300330a1: { s32i a6,a5,0x120  ; movi a12,3  ; ... }   ; a6 = CDW-derived slot, 3 bits
300330a9: { s32i a12,a5,0x118 ; movi a11,4  ; mov a12,a5 }   ; verb 3 ERASE
300330b1: { s32i a11,a5,0x11c ; mov a11,a7  ; mov a10,a2 }   ; section 4
300330b9: call8 0x30030360                                    ; post
```

The **section is a `movi` immediate, 4** (the firmware-slot section), and the
only host-influenced field is `+0x120`, masked to three bits
(`extui a6,a6,0,3` at `0x30032fa3`). There is no encoding of that command that
names section 11.

It is reached from the admin dispatcher at `0x7ffa714a`
(`beqi a12,5,0x7ffa7a2d`, `a12 = CDW12[7:0]`) on **opcode `0xD4` or `0xD7`**
(`0x7ffa7115`/`0x7ffa711b`). Executed against the gate:

```
0xd4: admitted in post-crash mode = False
0xd7: admitted in post-crash mode = False
```

So on a latched drive it is not merely wrong-section, it is unreachable.
(Recorded here mostly as a hazard note: on a *healthy* drive `0xD4`/`0xD7` with
`CDW12[7:0] = 5` erases a firmware slot.)

### 1.4 The `PROC0` side, re-derived — PROVEN

For completeness, the other half of the plumbing. All System-Area section
traffic inside `PROC0` goes through the submitter `0x7ffb4fec(req, verb, section)`.
Sweeping all **33** call sites and recovering the `a11`/`a12` immediates:

| verb | sections seen | sites |
|---|---|---|
| 0 probe | 6, 10, 11 | `0x7ffaafea`, `0x7ffab1e9`, `0x7ffab204` |
| 1 write | 2, 4, 6, **11** | `0x7ffa3921`, `0x7ffa39cd`, `0x7ffa83e7`, `0x7ffa840a`, `0x7ffa88dd`, `0x7ffa8d94`, `0x7ffa8db7`, `0x7ffa8dda`, **`0x7ffaaf33`** |
| 2 read | 1, 2, 3, 5, 6, 10, 11 | `0x7ffa3a25`, `0x7ffaaf58`, `0x7ffaafb5`, `0x7ffab22f`, `0x7ffab25a`, `0x7ffab883`, `0x7ffab8ae`, `0x7ffab8d9` |
| **3 erase** | **6, 10, 11** | `0x7ffa74aa` (6), `0x7ffa8b2d` (6), `0x7ffa399b` (**10**), **`0x7ffa3a40` (11)** |
| 21/30/31/32 | misc | the firmware-slot arms |

The single `PROC0`-internal erase of section 11 is **`0x7ffa3a40`**, inside the
**Background Thread** `0x7ffa33e8` (StrIds 1127/1139 *"SYS: Background Thread
started/finished"*). Its arm is guarded by a read of the flags byte
`0x7ff8d200` at `0x7ffa37cd` and is preceded in the same region by the
LOAD-N-GO firmware-persist calls and StrIds 1128/1129 *"SYS: LOAD-N-GO Firmware
image is corrupted"* / *"LOAD-N-GO failed to save firmware"*.

So the firmware **does** contain a non-destructive CLOG erase — it clears the
section and then rewrites the flags byte (`& 0xFE` then set bit 1 "evaluated")
with no marker and no re-init. It is on the **boot-mode-4 LOAD-N-GO path**,
which `sn200-logic-escapes.md` §2.3 already proved has no in-band trigger:
nothing in the running firmware writes 4 to `0x7ff9ff64`, and Firmware Commit
rejects commit action `011b` outright (`blti a8,3` at `0x30025e4b`). This is a
sixth sighting of the same closed door, not a new one.

`0x7ffa3a40` also has no host-reachable caller: the function has **zero** `callN`
sites and its address appears as a literal exactly once (`0x7ffa3ab6`), where it
is bound as a thread body.

---

## 2. Attack 2 — the mode-6 test

### 2.1 It is read fresh at resume, not captured at submit — PROVEN by execution

```asm
30033704: l32r   a14,-> 0x7ff87c64
30033707: l32i.n a14,a14,0x0
30033709: { extw ; bnei a14,6,0x300335bf }
```

The value is loaded from the global inside the resume handler. Nothing copies it
into the request at submit time. Executed (`sn200_oracle.ff_resume_posts_reinit`
with the global forced to each value):

```
mode 0..5: sub5 posts reinit = False    sub6 = False
mode 6   : sub5 posts reinit = True     sub6 = False
mode 7..9: sub5 posts reinit = False    sub6 = False
```

So the gate is genuinely dynamic — **if** the global could be made anything but
6 between the erase completing and the resume running, `0x0503` would be a plain
section erase. That is the whole of the opportunity.

### 2.2 Three writers, all internal — PROVEN (one of them new)

`litref -v 7ff87c64` gives 23 `l32r` sites in `PROC8`; lifting forward from each
finds **three** that store:

| site | value written | context |
|---|---|---|
| **`0x7ffacd20`** | **`0x80`** (`movi a12,128`; `s32i.n a12,a11,0x0`) | inside `0x7ffaccf0`, a task body with no `callN` callers — PROC8's own init. It also writes `+0x4 = 0`, `+0x8 = 4`, and zeroes `0x7ff88018`. |
| `0x7ffb0157` | `[msg+0x10]` | arm of `0x7ffb0088` |
| `0x7ffb01a7` | `[msg+0x10]` | arm of `0x7ffb0088` |

`sn200-marker-write.md` and `sn200-firmware-flow.md` record **two** writers.
The third (`0x7ffacd20`, the init constant `0x80`) is new here, and it is the
interesting one: `0x80` is exactly the "not ready" value the `0x0004` probe
reports specially (`bnei a10,128` → status `0x81800000`, `0x30033505`). A drive
whose mode word reads `0x80` fails `bnei a14,6` and would take the plain
completion tail.

`0x7ffb0088` is reached only from `0x7ffb0608`, a poll loop
(`call8 0x7ffabde0` dequeue → `call8 0x7ffb0088` handle) — the IBQ receiver.
Its input is an inter-processor message from `PROC0` carrying the startup type
at `+0x10`. **No NVMe command writes the word, and no NVMe command constructs
that message**; `PROC0` computes the startup type once at boot and sends it once.

### 2.3 No coroutine window — PROVEN

`sn200-oam-dispatch.md` §4.1 and the oracle agree that `0x0303` is the **only**
two-stage arm in the family. `0x0503` builds its request in the first entry and
yields exactly once, to its resume. There is no stage-1/stage-2 seam to insert
anything into.

### 2.4 What is left, and it is not a procedure — SPECULATIVE

The only way the mode word becomes something other than 6 during a power-on is
`PROC8` re-running its init (`0x7ffaccf0`) — i.e. a controller-level reset of
`PROC8`. The drive already does this every ~5 s under the AEN reset loop. In
principle a reset landing between the EEPROM erase committing (in `PROC0`) and
the resume running (in `PROC8`) would commit the erase and never post the
re-init.

**Do not attempt this, and do not present it to an operator.** Three reasons:

- It is a race with no observable, no confirmation, and no way to retry
  distinguishably.
- After `PROC8` re-inits, `PROC0` re-sends the Startup Req with 6 again, so the
  window is bounded by an interval nothing here can measure.
- Even if it won, the drive would come up with CLOG clear but PFCL still armed
  and the stored marker still 5/6/7 → the `UNEXSTRT` stub writer re-stamps CLOG
  (`sn200-section-arming.md` §3). You would be back where you started, minus
  the crash dump.

This is recorded as the shape of the remaining gap, not as a lead.

---

## 3. Attack 3 — what the re-init actually does

### 3.1 At runtime, verb `0x25` destroys nothing — PROVEN

`PROC0`'s verb-37 arm `0x7ffa4306` loads two literals from its own pool
(`0x80000004` FACTORY, `0x80000003` REINIT), selects between them on
`[req+0x54]`, stores the result into the marker setter's value field
`[a5+0x18]`, and submits. The setter `0x7ffa84c8` writes RAM
`*(0x7ff8c7ec)` and immediately persists a **244-byte, section-6, op-2 write**
(`sn200-marker-write.md` §2.5). That is the complete runtime effect: **four
bytes in an EEPROM record.**

Nothing is queued, no table is invalidated, no memset runs, the namespace is
not touched. The drive that has just executed `0xFF`/`0x0503` on a latched
controller has **lost nothing**. The loss happens on the *next boot*.

### 3.2 There is no lighter marker — PROVEN

`+0x128 ∈ {0, 1}` is the entire host-side input, and `0x7ffa4306` maps it onto
two literals. Markers 3 and 4 both route to **startup type 0**
(`sn200-logic-escapes.md` §1), and startup type 0 is the only type that runs
`Admin_NamespaceStartup` (`0x7ffac7de`: `bnez a9 → "Normal Startup"`;
zero → `"First Startup"` → spawn `0x7ffad364`). So the 3-vs-4 polarity question
is decorative: there is no third value and no lighter path. The dispatch at
`0x7ffaae69` was executed for all 16 marker values in
`sn200-pcode-toolchain.md` §3.3 — nothing else reaches a re-init.

### 3.3 Is there a redundant / journalled L2P? — **No.** PROVEN

Asked plainly by the brief, answered plainly:

- `Admin_NamespaceStartup` **does** have a keep-it branch: `0x7ffad54e` compares
  `*(0x7ff8803c)` against `'VNPK'` and, if it matches, jumps past both memsets
  (`0x7ff8803c` 0xa1c bytes, `0x7ff88a58` 0xc18 bytes). But on startup type 0 the
  first-time-startup routine `0x7ffaabd8` has already rewritten the marker word
  and filled the in-RAM System-Area directory arrays with `0xFFE` (invalid), so
  the header is never restored and the signature never matches.
- `Admin_LbnTransTblInit` `0x7ffad2f0` then re-zeroes both tables and rewrites
  the 1024-entry region map to `0xffff` = free.
- `PROC12`'s V2P restore is gated on a System-Area field and logs StrId 1404
  *"JournalMgr: Skipping V2P read as it never saved into Flash"* when it is gone.
- Event-log replay exists (`PROC12 0x7ffa70c0`/`0x7ffa7340`, `PROC10 0x7ffab510`,
  `PROC15 0x7ffb2c82`) but replays **into** a restored V2P image.

**There is no path anywhere in the firmware that reconstructs a V2P/L2P table by
scanning media.** (`sn200-firmware-re.md` §13.7, re-checked here; nothing in this
pass contradicts it.) A drive that has been through a marker-3 boot is zeroed
and stays zeroed — "rebuild afterwards" is not an option.

### 3.4 The consequence that does matter

Because the pending re-init is *only a record value*, it is **overwritable**.
`sn200-marker-write.md` §5 established this for a UART marker-write primitive.
The in-band version of the question is: **what host-reachable action writes that
record later in the same power-on?**

Two candidates exist, and only one is unexamined:

- **Firmware Commit** (`0x7ffabccc`) writes the hardcoded `0x80000003` — the same
  value. Useless.
- **The shutdown "FINISHED" marker.** `PROC6 0x7ffbba61` writes `0x80000001`
  CLEAN or `0x80000002` PFAIL into `[+0x3c]` of the System-Area image it is about
  to save. Marker 1 or 2 on the next boot is a **normal startup with the L2P
  intact** — precisely the target state. And `CC.SHN` is a register write, not an
  admin command, so `Admin_CheckCmdAllowed` never sees it.

See §4.

---

## 4. The one lead left, stated with its risks — OPEN, do not run

The full requirement for a non-destructive recovery is now exactly:

> CLOG clear **and** PFCL clear **and** System Area non-empty **and** the stored
> marker ∈ {1, 2, 8} at the next cold boot.

`0x0503` gives the first (at the price of scheduling marker 3), `0x0603` gives
the second (at the price of the PFail dump), and nothing host-reachable gives
the fourth — **unless the shutdown path does.**

What is established:

- `PROC6 0x7ffbba61` is the **only** writer of markers 1/2
  (`sn200-shutdown-path.md` §1.5), and it is reached only through the System-Area
  save. **PROVEN.**
- The save is gated on the global `0x7ff8c7c4`. `PROC0 0x7ffa8c22`
  (`beqz a15, 0x7ffa8bb2`) jumps straight to completion when it is zero —
  no SAM, no marker. **PROVEN.**
- `0x7ff8c7c4` is set to 1 only by *Shutdown Request Received* `0x7ffa8e64`, and
  only when the request type `[ctx+0x3c] == 3` **and** `[req+0x10] != 2`
  (`0x7ffa8ed8`/`0x7ffa8ee0`). **PROVEN.**

What is **not** established, and is exactly the next piece of work:

1. **Does a host `CC.SHN` produce shutdown-request type 3?** `0x7ffa8e64` has one
   caller, `0x7ffa48b5`; the type reaches it in the request object and was not
   traced to the NVMe register write in this pass.
2. **Is the SAM save safe on a latched drive?** This is the real hazard. Startup
   type 6 never *reads* the System Area (`sn200-readonly-startup.md` §2) — SAM was
   never populated. A shutdown that runs the SA *save* on that state could write a
   blank System Area over the good one, which is the same wipe by a different
   route. This must be settled before anyone types `nvme shutdown` on a drive they
   care about.
3. **Ordering.** Both the verb-`0x25` marker write and the shutdown marker write
   are asynchronous submissions to the same engine. The shutdown must be issued
   long enough after `0x0503` that the re-init's 244-byte section-6 write has
   already landed, or it is a coin flip.

If (1) and (2) both come back clean, the procedure would be
`0x0603` → `0x0503` → wait → clean `CC.SHN` shutdown → cold power cycle, and its
**failure mode is benign**: a shutdown that starts and does not finish leaves
marker 5/6/7, which on the next boot routes to the `UNEXSTRT` stub writer and
re-arms CLOG — latched again, data still intact, retryable. That asymmetry is
what makes it worth finishing.

**Until (2) is answered, this is not a procedure and `sn200-runbook.md` is
unchanged.**

---

## 5. Scoreboard

| claim | label |
|---|---|
| `0x7ffa972c` is the EEPROM/OAM request executor and exists as exactly 6 words in 20 images | PROVEN |
| 11 producer sites total; no jump table holds the pointer; overlay `l32r` resolved in runtime space | PROVEN |
| Exactly one site builds verb 3 + section 11 — the `0x0503` arm | PROVEN |
| The only other verb-3 producer is `0x300330a9`, section **4**, opcode `0xD4`/`0xD7`, rejected by the gate | PROVEN (gate result executed) |
| `PROC0`'s only internal section-11 erase is `0x7ffa3a40`, on the LOAD-N-GO path, no caller | PROVEN |
| The mode-6 test re-reads `0x7ff87c64` at resume time | PROVEN by execution, modes 0–9 |
| `0x7ff87c64` has **three** writers (not two); the new one is `0x7ffacd20`, constant `0x80`, PROC8 init | PROVEN |
| No host command writes `0x7ff87c64` or constructs the message that does | PROVEN |
| `0x0503` is single-stage; no inter-stage window | PROVEN |
| Verb `0x25` destroys nothing at runtime — it writes 4 bytes into the section-6 record | PROVEN |
| Markers 3 and 4 both route to startup type 0; no lighter re-init exists | PROVEN |
| No redundant/journalled L2P; no media-scan rebuild path anywhere | PROVEN |
| The pending re-init is cancellable by a later marker write in the same power-on | INFERRED (mechanism PROVEN in `sn200-marker-write.md` §5) |
| A host shutdown could supply that later marker write | **OPEN** — §4 |
| A reset landing between erase-commit and resume would dodge the gate | SPECULATIVE — §2.4, do not attempt |

Five leads died before this one; two more died here (a second host verb-3 path,
and any dodge of the mode test). The reframing in §3 — that the re-init is a
record value and not an action — is the only thing that got *better*, and §4 is
where it has to be cashed in or buried.
