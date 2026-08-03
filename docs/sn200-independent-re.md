# SN200 "Post Crash Startup" — independent reverse-engineering

Target: HGST/WDC Ultrastar SN200 (HUSMR7676BDP3Y1), firmware **KNGND122**, ASIC "Omaha".
Question: what exactly puts this drive into the mode where it stops presenting a namespace
and raises a Persistent Internal Error AEN every ~5 s, and can a host get it back out?

This file was produced **without reading `docs/sn200-firmware-re.md`** until the analysis
below was complete. Divergences from that document are listed at the end.

Every claim is tagged **PROVEN** (read directly out of a binary, or traced to instructions),
**INFERRED** (strong structural argument, one link not directly decoded), or
**SPECULATIVE**.

## 0. Working set

Unpacked with `tools/sn200-fw/unpack.py`:

```
scratchpad/sn200fw/fw/KNGND122/StringTable.csv     3617 lines; StrId N == 0-based line N
scratchpad/sn200fw/flat/PROC0_7ff80000.bin         "SYS" manager  (startup/marker logic)
scratchpad/sn200fw/flat/PROC8_7ff80000.bin         "Admin" manager (NVMe admin path)
scratchpad/sn200fw/flat/PROC8_30000000.bin         Admin, second address region (OAM/VUC)
... PROC1..15, FCC_00100000.bin
```

Log descriptor word = `(StrId<<16) | (level<<8) | nargs`. I additionally required
`nargs == count of printf conversions in the string` to filter the false positives that a
naive `StrId<<16` scan produces; that alone removed ~80 % of candidate hits.

Helper scripts written for this run live in the scratchpad: `lmap.py` (descriptor + brute-forced
`l32r` xrefs), `lref.py` (all `l32r`-shaped references to one literal), `d.py` (annotated
disassembly wrapper around `tools/sn200-fw/xdis.py`).

---

## 1. The persistent startup marker — the whole mechanism hangs off this

**PROVEN.** `PROC0` contains a 16-bit lookup table at **`0x7ff81180`** whose entries are
StrIds. It is indexed by `startupState & 0x7fffffff` (see §2 for the masking instruction):

| idx | StrId | marker text |
|-----|-------|-------------|
| 0 | 3029 | `No previous marker found` |
| 1 | 3030 | `CLEAN shutdown` |
| 2 | 3031 | `PFAIL shutdown` |
| 3 | 3032 | `Drive REINIT requested` |
| 4 | 3033 | `FACTORY drive REINIT requested` |
| 5 | 3034 | `Normal Shutdown STARTED` |
| 6 | 3035 | `PFAIL Shutdown STARTED` |
| 7 | 3036 | `PFAIL Shutdown TIMEOUT` |
| 8 | 3037 | `READONLY Startup requested` |
| 9 | 3038 | `POST CRASH Startup` |
| 10 | 3039 | `Invalid marker` |

The in-RAM state word is `0x8000000N` where `N` is the index above (constants read from the
PROC0 literal pool: `0x7ff83470 = 0x80000001`, `0x7ff83474 = 0x80000009`,
`0x7ff83478 = 0x80000008`, `0x7ff82b50 = 0x80000003`, `0x7ff82b4c = 0x80000004`,
`0x7ff83230 = 0x80000005`, `0x7ff830ec = 0x80000006`, `0x7ff830f4 = 0x80000007`).

Read the semantics carefully: markers 5/6/7 are **"STARTED"**, not "finished". The drive
writes `Normal Shutdown STARTED` / `PFAIL Shutdown STARTED` when it *begins* a shutdown and
overwrites it with `CLEAN shutdown` / `PFAIL shutdown` when it *completes*. So a marker of
5/6/7 at boot means "the previous shutdown began and never finished".

The marker is also surfaced to the log as 8 ASCII "bread crumbs"
(StrId 1259 `SYS: Bread crumbs: %c%c%c%c%c%c%c%c`, emitted at `PROC0:7ffaabb4`), i.e. the
persisted form is an 8-byte ASCII token in the EEPROM/System-Area journal, not the
`0x8000000N` word.

## 2. The startup dispatcher — `PROC0`, one coroutine at `0x7ffaac30`

**PROVEN** (constants and `movi`s read from the image; branch edges via the `b12` field of
FLIX format-`0xf` bundles, which I validated against three independently-confirmed targets).

The dispatch chain runs `0x7ffaae69 … 0x7ffaaede`, comparing the state word against each
`0x8000000N` in turn. The value it selects is a StrId in `a11` which the shared tail at
`0x7ffaac8a` prints via `%s\n` (StrId 1275):

| state | handler | `movi a11,` | startup type printed |
|-------|---------|-------------|----------------------|
| 1 `CLEAN shutdown` | `7ffaaf85` | 1264 | `SYS: Normal startup` |
| 2 `PFAIL shutdown` | `7ffaaf8d` | 1265 | `SYS: PFAIL startup` |
| 3 `Drive REINIT` | `7ffaaf63` | 1266 | `SYS: Drive re-init` |
| 4 `FACTORY REINIT` | `7ffaafc2` | 1267 | `SYS: Drive re-init to factory defaults` |
| 0 `No previous marker` | `7ffaaffd` | 1268 | `SYS: First time startup` |
| **5, 6, 7** | **all three → `7ffaaf6b`** | — | see below |
| 8 `READONLY` | `7ffaaff5` | 1272 | `SYS: Read-only startup` |
| 9 `POST CRASH` | (edge not decoded) | — | `SYS: Post Crash startup` |
| anything else | `7ffaaede` | — | `SYS: Bad startup marker (%08X)` |

The exhaustive `movi a11,<1264..1275>` scan over **all 17 images** returns exactly
1264, 1265, 1266, 1267, 1268, 1272, 1273 and 3043 in PROC0 — and **never** 1269, 1270 or
1271. That is the single most informative fact in this whole analysis:

> **PROVEN: the three "ERROR – Shutdown/PFAIL started but never finished / timed out"
> startup types (StrId 1269/1270/1271) are dead strings in KNGND122. States 5, 6 and 7 do
> not get their own startup type. They are all funnelled into one common handler.**

That common handler is `0x7ffaaf6b`:

```
7ffaaf6b: l32r  a15,0x7ff826b8      ; -> global @0x7ff9ff60
7ffaaf6e: l32i.n a15,a15,0x4
7ffaaf70: <FLIX conditional branch>  ; if NOT load-n-go -> crash path
7ffaaf78: s32i.n a6,a7,0x0          ; clear the state word
7ffaaf7a: l32r  a11,0x7ff83490      ; = 3043 "SYS: Load-n-go boot override of failed shutdown."
7ffaaf7d: <FLIX> j 0x7ffaac8a       ; -> shared tail, boot normally
```

So a "shutdown started but never finished" boot has exactly **one** escape: a *load-n-go*
boot (firmware image handed to the controller rather than loaded from its own EEPROM). Absent
that, control goes to the crash path.

### 2.1 The crash path — writes an `UNEXSTRT` stub and declares Post Crash startup

**PROVEN (data), INFERRED (exact edge ordering).**

```
7ffaacea: l32r a13,0x7ff83338      ; 0x7fffffff
7ffaaced: l32r a12,0x7ff83438      ; -> 0x7ff81180  (the marker StrId table, §1)
7ffaacf0: and  a11,a11,a13         ; state & 0x7fffffff  == table index
7ffaacf3: <log StrId 3044>         ; "SYS: ERROR - %s but did not complete successfully!!"
                                   ;   %s = marker name, e.g. "Normal Shutdown STARTED"
7ffaad1a: l32r a8,0x7ff82888       ; 0x48444300  == "HDC\0"  crash-header magic
7ffaad1d: s32i.n a8,a5,0x8
7ffaad45: l32r a14,0x7ff83448      ; 0x53545254 == "STRT"
7ffaad48: l32r a15,0x7ff83444      ; 0x554e4558 == "UNEX"
7ffaad4b: s32i a15,a5,0x48         ; header[0x48..0x4f] = "UNEXSTRT"
7ffaad4e: s32i a14,a5,0x4c
   ... yield; acquire a context (call8 0x7ffa3bd8 retry loop at 7ffaad7c);
   ... submit the SPI write (block at 7ffaaf13, call8 0x7ffb4fec), yield,
   ... resume address 0x7ff83488 = 0x7ffaac53 :
7ffaac59: <log StrId 3520>         ; "SYS: UNEXSTRT detected, writing UNEXSTRT stub header
                                   ;  to crash area"
7ffaac82: movi a11,1273            ; "SYS: Post Crash startup"
7ffaac8a: -> shared tail, prints it
```

The `"UNEX"`/`"STRT"` and `"HDC\0"` literals, and the `movi a11,1273` immediately preceding
the shared tail, are all read straight out of the image — those are PROVEN. The claim that
the `0x7ffaac53` resume is reached from the state-5/6/7 handler rather than some other caller
is INFERRED: `0x7ffaac53` is stored as a resume address at exactly one site (`0x7ff83488`,
loaded at `0x7ffaaf36`), inside the only SPI-write block whose completion lands there.

### 2.2 The latch — crash section present forces state 9 on *every* subsequent boot

**PROVEN.**

```
7ffaae28: l32r  a12,0x7ff826b8 ; l32i a12,a12,0x4   ; system-area scan results
7ffaae35: <FLIX cond>  b12 -> 0x7ffaaf02            ; crash-dump section present?
7ffaae3d: <FLIX cond>  b12 -> 0x7ffaaf02            ; pfail-crash section present?
...
7ffaaf02: l32r a10,0x7ff83484 ; <log StrId 3042>    ; "SYS: Detected a CRASH or PFCRASH section."
7ffaaf08: l32r a11,0x7ff83474 ; = 0x80000009        ; force state = POST CRASH Startup
7ffaaf0b: <FLIX> j 0x7ffaae69                       ; re-enter the dispatcher
```

Two independent conditions (one per section) both jump to the same log + `0x80000009`
assignment. Section names are confirmed by 4-char magics in the PROC0 SPI-section table:
`CLOG` at `0x7ff826e4` / `0x7ff84b0c` (= StrId 1225 "Crash Dump") and `PFCL` at
`0x7ff826f4` / `0x7ff84b14` (= StrId 1224 "PFail Crash Dump"), alongside `SYSB`, `BSCR`,
`BSTA`, `BLOG`, `SLOT`, `FRMW`, `MBBB`, `UEFI`, `DRVC`, `STOC`.

**This is the latch.** Once anything non-erased sits in `CLOG` or `PFCL`, the controller
enters Post Crash startup on every boot, forever, regardless of how clean the previous
shutdown was. The unclean start writes the `UNEXSTRT` stub into that section, so the very
first unclean start arms the trap permanently.

There is one more, unrelated door into the same state (**PROVEN**):

```
7ffaae45: l32i.n a11,a7,0x0
7ffaae47: bne a11,a6,0x7ffaae69
7ffaae4a: <log StrId 3519>   ; "SYS: Unexpected empty System Area."
7ffaae50: j 0x7ffaaf08       ; -> the same 0x80000009 assignment
```

An empty/unreadable System Area also forces Post Crash startup.

Two other conditions instead force state 3 (`Drive REINIT requested`, i.e. a *recoverable*
outcome, not post-crash): StrId 3040 `SYS: Found an incompatible SA` at `0x7ffaadaa` and
StrId 3041 `SYS: Detected an erased SysArea.` at `0x7ffaaef1`, both loading `0x80000003`.

---

## 3. What Post Crash startup does to the host interface

**PROVEN.** In `PROC8` (Admin manager):

```
7ffa6d08: <log StrId 1804>   "Admin cmd rejected due to Post Crash startup mode: 0x%x"
7ffa6d10: call8 0x7ffb45a8   (log)
7ffa6d13: l32r a9,0x7ffa0da0 ; = 0x8f8a0000
7ffa6d16: or   a2,a5,a9      ; completion status
7ffa6d19: retw.n
```

`0x8f8a0000 >> 17 = 0x47c5` → NVMe CQE Status Field `DNR=1, M=0, CRD=0, SCT=7 (vendor
specific), SC=0xC5`. That is exactly the **0x7C5** the host sees, with Do-Not-Retry set —
which is why the kernel does not retry the rejected command. Independent confirmation that
this log site is the gate the field is hitting.

```
7ffa4080: l32r a10,0x7ffa09b4  ; <log StrId 1774>
          "Admin_NotifyHandler: Sending Persistent Internal Error async event on
           Post Crash Startup."
```

is the ~5 s AEN. Both live in PROC8 and both key off the same startup state pushed to the
managers by SysMgr (StrId 793 `Received STARTUP_REQ from SysMgr (startup state: %d)`).

### Is user data still there?

**INFERRED (high confidence).** Nothing on the Post Crash path touches the media. The state
is decided inside the System-Area/EEPROM processing stage in PROC0, *before* `SYS: Start SAM`
(StrId 1295) and before the block manager's startup. The only write the path performs is the
`UNEXSTRT` stub into the SPI `CLOG` section — SPI NOR, not NAND. There is no call to any
format/erase/L2P-rebuild routine on this path, and the "erased/invalid System Area" cases go
to `Drive REINIT` (state 3), not here. So Post Crash mode **hides** the namespace; it does
not rebuild or erase the L2P.

I could NOT prove a negative exhaustively (the FLIX bundles hide some calls), but the
positive evidence is strong and matches the field observation that the drive came back with
data intact after recovery.

---

## 4. Verdicts on the four candidate models

### A — "large deallocate races the L2P journal flush" (WD errata OM-6588)
**Not the mechanism; at best an upstream trigger. Effectively REFUTED as a distinct latch.**

There is no code path anywhere in the startup logic that consults deallocate state, LBA
ranges, or discard progress when deciding Post Crash startup. What a runaway deallocate can
do is hang or crash the firmware such that the *shutdown never completes* — which lands you
in marker 5/6/7 — or trip an assert that writes a real crash dump into `CLOG`. Either way the
latch is §2.2, not the discard itself. **PROVEN**: the decision inputs are (a) the persisted
marker and (b) whether `CLOG`/`PFCL` is non-erased. Nothing else.

This also explains the field result cleanly: repeating mkfs with `discard_max_bytes=0` did
nothing because discard was never the latch condition; it was only ever one way to provoke an
unclean stop.

### B — "any start not preceded by a recorded clean shutdown re-arms the crash section"
**CONFIRMED, with one important refinement.**

**PROVEN**: markers 5/6/7 (`Normal Shutdown STARTED`, `PFAIL Shutdown STARTED`,
`PFAIL Shutdown TIMEOUT`) all converge on `0x7ffaaf6b`, and the only exit that avoids the
crash path there is a load-n-go boot. **INFERRED**: that path writes the `UNEXSTRT` stub into
`CLOG` and reports `SYS: Post Crash startup`.

The refinement: "not clean" is not sufficient by itself. Marker 2 (`PFAIL shutdown`, i.e. a
power loss where the capacitor-backed PFAIL save *completed*) maps to `SYS: PFAIL startup`
(1265) and is a **perfectly normal boot**. Marker 0 (`No previous marker found`) maps to
`First time startup` (1268), also normal. What is fatal is specifically
**a shutdown or PFAIL save that was started and did not finish**.

So: an abrupt power removal on a healthy drive with healthy hold-up capacitance should land
on marker 2 and boot fine. An abrupt power removal where the PFAIL save could not complete
(weak/aged caps, rail collapse faster than the hold-up budget, or the firmware wedged during
the save) lands on marker 6 or 7 and latches.

### C — "abrupt power loss arms a distinct PFAIL section by a separate path"
**Partly confirmed, but it is NOT a separate mechanism.**

There genuinely are two sections, `CLOG` and `PFCL`, with separate detect/erase/size paths
(StrIds 1277-1282, 1607-1610, and separate OAM erase sub-operations — §6). **PROVEN**: the
startup check at `0x7ffaae35` / `0x7ffaae3d` tests them as two conditions that both jump to
the same `0x7ffaaf02` handler and the same `0x80000009`. So C is not an independent failure
mode; it is a second *producer* feeding the one latch in §2.2. B and C are the same mechanism
with two inputs.

### D — "a PCIe link drop during operation is indistinguishable from power loss"
**REFUTED as stated, but it is a real and sufficient trigger by a different route.**

**PROVEN**: PFAIL is a *power* event, not a link event. `SYS: PFAIL interrupt enabled`
(1213), `SYS: PFAIL power = % 5d mW` (1258), `SYS: Delayed SAM startup by %d ms (waiting for
VCAPOK)` (3517) — the PFAIL trigger is a voltage/capacitor monitor (VMON/VCAPOK), driven by a
hardware interrupt, entirely independent of the PCIe link. The PCIe subsystem has its own
completely separate shutdown machinery (`PCIe_RequestShutdown`, `PCIe_ShutdownThread`,
`PCIe_PfailShutdown`, StrIds 844-861, 3109-3112). A link-down does not raise PFAIL.

But: `PCIe_PfailShutdown` (3111/3112) exists, meaning the PCIe manager participates in the
PFAIL shutdown, and the NVMe-level shutdown notification (CC.SHN, StrId 697
`CC.SHN controller shutdown port %d type 0x%x`) is what starts a *normal* shutdown. If the
link drops or the port is disabled, **the host can never deliver CC.SHN**. The drive is then
guaranteed to be stopped without a clean shutdown — and if power is subsequently cut with
the PFAIL save unable to complete, you get marker 6/7.

So D's *claim* (link loss == power loss) is false; D's *consequence* (a link failure makes an
unclean stop near-certain) is true, and it is exactly what latch #2 in the field looks like.

### Reconciling the two field latches

- **Latch #1** (mkfs/whole-device discard): the discard workload wedged or crashed the
  firmware, so the subsequent stop left marker 5/6/7 or wrote a real crash dump. Model A was
  the *provocation*; model B was the *latch*.
- **Latch #2** (host `ForceOff`, then `UEFI0067 PCIe link training failure ... link is
  disabled`, flaky U.2 cable): power was removed abruptly with no CC.SHN. Either the PFAIL
  save did not complete (marker 6/7), or — more likely given the cable — the link was already
  marginal, the drive was mid-flight, and the stop was maximally hostile. Model B again.
- The controlled repeat with `discard_max_bytes=0` had **clean shutdowns throughout**, so it
  could never latch. It is not evidence that discard is safe; it is evidence that the shutdown
  path is what matters.

## 5. Root cause, plainly

> The drive keeps an 8-byte ASCII shutdown marker in its System Area and a crash-record
> section (`CLOG`) plus a power-fail crash section (`PFCL`) in its SPI NOR. If a boot finds
> the marker in any *"STARTED"* state — the shutdown or the capacitor-backed power-fail save
> began and never finished — the firmware writes an `UNEXSTRT` stub record into the crash
> section and declares `SYS: Post Crash startup`. From then on, *the presence of that record
> alone* forces Post Crash startup on every subsequent boot, because the boot path checks
> "is `CLOG` or `PFCL` non-erased?" before anything else. In that mode the Admin manager
> rejects most commands with SCT=7/SC=0xC5 and raises a Persistent Internal Error async
> event; the namespace is not presented. The user data on NAND is untouched — the mode is a
> latch in ~1 KB of SPI NOR, not a data event.
>
> DISCARD is not a cause. It is one of several ways to provoke the firmware into a stop that
> never completes. Any abrupt stop that prevents the shutdown or the PFAIL save from
> finishing — including power removal on a drive whose hold-up is marginal, or a PCIe port
> that is disabled while the drive is in flight — is sufficient on its own.

---

## 5a. Tooling caveat and confidence split

The Omaha cores are Xtensa **with FLIX/VLIW bundles**. Ghidra's Xtensa module gives bundles
the wrong length (3 bytes instead of 8), then resumes decoding *inside* the bundle and emits
fabricated instructions. I did not rely on Ghidra for any claim in this document.

The hand-rolled `tools/sn200-fw/xdis.py` gets the *length* right (op0 `0xE`/`0xF` ⇒ 8 bytes),
so its linear sweep stays in sync, but it does **not** decode bundle contents; it prints a raw
`FLIXn` line plus two heuristically-extracted fields (`s0l32r`, `b12`). Consequences:

* `l32r` literal targets, `call8` targets, and all 2/3-byte base-ISA instructions are
  trustworthy — every branch target I cite lands on an instruction boundary produced by the
  same sweep, which is the ground-truth check.
* I additionally recovered a partial bundle format: for format `0xb` (`byte0 == 0xbe`), slot 0
  is the top 20 bits of a 24-bit core instruction with `op0` implied, which is how I read
  `movi a11,<StrId>` out of bundles. Verified against `movi a11,1264/1265/1267/1268/1272/1273`
  all landing on the correct startup-type strings.
* The `b12` field (`(bundle>>36) & 0xfff`, target `pc+4+b12`) is a real branch offset for
  format-`0xf` bundles: validated on four independent edges (`7ffaaeaa/7ffaaeb5/7ffaaec0` →
  `7ffaaf6b`, `7ffaaed6` → `7ffaaff5`, both confirmed by the constants loaded immediately
  before them). It is **not** valid for every format.

Confidence split for the findings that matter:

| Finding | Rests on | Confidence |
|---|---|---|
| Marker table at `0x7ff81180`, 11 entries | raw data | **certain** |
| `0x8000000N` state encoding | literal pool | **certain** |
| `"UNEX"`/`"STRT"`/`"HDC\0"` written into a crash header | literals + `s32i` (base ISA) | **certain** |
| `CLOG`/`PFCL` section magics | raw data | **certain** |
| Reject status `0x8f8a0000` ⇒ SCT 7 / SC 0xC5, single site in the image | literal pool + exhaustive search | **certain** |
| Startup-state → startup-type mapping | `movi` in bundle slot 0 + ordering | **high** |
| 1269/1270/1271 unreachable in KNGND122 | exhaustive `movi` scan over all 17 images | **high** |
| States 5/6/7 converge on `0x7ffaaf6b`, load-n-go is their only escape | `b12` edges + adjacent constants | **high** |
| Crash-section detect forces `0x80000009` | `l32r` + log adjacency in one basic block | **high** |
| State 9 does **not** share the load-n-go override | absence of a matching `b12` | **medium** |
| Which admin opcodes are exempt from the gate | undecoded bundle operands | **UNKNOWN** |

---

## 6. Can the host get the drive out of this without a chassis power cycle?

### 6.1 The rejection gate is a deny-list, not an allow-list

**PROVEN.** Searching all PROC8 images for the constant `0x8f8a0000` returns **exactly one**
location (`0x7ffa0da0`, referenced once, at `0x7ffa6d13`). There is a single post-crash
rejection site in the firmware.

That site sits at the end of a validation routine, `entry` at **`0x7ffa6b18`**, whose first
argument chain is a run of ~18 comparisons against a register (`a3`) holding the opcode, each
branching to the same block that ends in the post-crash rejection:

```
7ffa6b18: entry a1,0x20
7ffa6b38: beqz a3,0x7ffa6cfb                 ; opcode == 0
7ffa6b3b..7ffa6bc9: 16 further FLIX compares, all b12 -> 0x7ffa6cfb
          interleaved plain constants: movi a9,9(0x09)  movi a14,236(0xEC)
                                       movi a8,255(0xFF) movi a9,202(0xCA)
7ffa6bd1: (fall through) -> 7ffa6bd9, other gates
...
7ffa6cfb: <post-crash flag test>
7ffa6d08: log 1804 "Admin cmd rejected due to Post Crash startup mode: 0x%x"
7ffa6d13: l32r a9,=0x8f8a0000 ; or a2,a5,a9 ; retw.n
```

The same routine hosts two sibling gates, each with its own status constant:

* `0x7ffa6cd9` → StrId 3370 `Admin cmd (opcode 0x%x) restricted by sanitize command SANACT
  0x%x: SSTAT 0x%x` (reached from a separate opcode list including `0xDD`, `0x81`, `0x82`)
* `0x7ffa6d65` → StrId 1805 `Admin cmd restricted by VUC Control disabled: 0x%x`,
  status `0x80020000` (SCT 0, SC 0x01)

**Structural conclusion (PROVEN):** the post-crash gate is *opcode-selective*. It applies to
an enumerated set decided by a compare chain on `a3`; opcodes outside the chain never reach
the rejection at all. The drive is therefore not "deaf" while latched.

**Polarity: UNDETERMINED — and I initially got this wrong.** I cannot tell whether the
enumerated set is the *blocked* set or the *exempt* set. The two instructions immediately
before the rejection decode as `movi.n a9,1` / `beqz a9,…`, which is dead as written, so the
linear decode has almost certainly desynced at the merge point and the real conditional lives
inside the two undecoded FLIX bundles at `0x7ffa6cf3`/`0x7ffa6cfb`. The other agent's document
reaches the same impasse and says so.

**What settles it in practice is field behaviour, not the disassembly:** the vendor clear
command (admin opcode `0xFF`, `cdw12=0x0503`) has been issued on a latched drive and returned
Success. So `0xFF` is *reachable* while latched, whatever the chain's polarity. That is the
only opcode-reachability fact I would stake anything on.

Constants visible in plain instructions inside the chain region: `0x09` (`0x7ffa6b6b`),
`0xEC` (`0x7ffa6b95`), `0xFF` (`0x7ffa6ba0`), `0xCA` (`0x7ffa6bb3`), plus range tests
elsewhere in the routine (`movi a9,-236; add; bltui a9,3` ⇒ `0xEC..0xEE`;
`movi a8,-216; add; bltui a8,8` ⇒ `0xD8..0xDF`). I will not assign these to a gate.

**One sub-list I *can* assign, and it matters (INFERRED, high confidence).** The second chain,
`0x7ffa6c80`–`0x7ffa6cc9`, ends at `0x7ffa6d4b` → `0x7ffa6cd4` → the **sanitize** rejection
log (StrId 3370), not the post-crash one. Its visible constants are `0xDD` (`0x7ffa6cb0`),
`0x81` (`0x7ffa6cbb`) and `0x82` (`0x7ffa6cc6`, with the explicit `beq a3,a15,0x7ffa6d4b` at
`0x7ffa6cc9`). `0x81`/`0x82` are Security Send/Receive, which the NVMe specification requires
to be restricted while a sanitize is in progress — and `0xDD` is **Start Secure Purge**
(confirmed independently from `libdmi_core.so`, `hgst_nvme_secure_purge` @ `0x146480`).
A sanitize-restriction list containing exactly {Secure Purge, Security Send, Security Receive}
is coherent; a post-crash exemption list containing them is not. So that chain is the sanitize
gate.

### 6.2 Firmware Commit (opcode 0x10) — the strongest standard lead, and why it still fails

**PROVEN that the machinery exists.** The firmware has a complete activate path:

| StrId | string |
|---|---|
| 2184 | `Firmware Activate Action=%02X, Slot=%02X` |
| 2188 | `Firmware Activate Invalid Activation Action` |
| 789 | `PCIe_Notify_Fw_Activate received` |
| 790 | `PCIe_Notify_Fw_Activate: Subsystem Restart Required to activate firmware` |
| 791 | `PCIe_Notify_Fw_Activate: New SBL loaded. Conventional Reset required` |
| 792 | `PCIe_Notify_Fw_Activate: Controller Restart Required to activate firmware` |
| 921 | `PCIe_SendResetRequest: Resetting for firmware activate (FWA)` (PROC9 `0x7ffacdea`) |
| 922 | `PCIe_SendResetRequest: FWA shutdown finished, perform re-init reset...` (PROC9 `0x7ffac41f`) |
| 82/83 | `Firmware Boot Mode : WARM BOOT, DDR (Slot %d)` / `COLD BOOT, EEPROM (Slot %d)` |

So a Firmware Commit **does** make the drive restart itself internally — the controller
performs its own shutdown and re-init reset with no host power involvement. That is a genuine
host-triggered firmware re-entry.

**But it does not clear this latch (INFERRED, high confidence).** Two independent reasons:

1. Whether the restart is a warm boot from DDR or a cold boot from EEPROM, PROC0 re-runs its
   System-Area processing, which begins with the crash-section test at `0x7ffaae35`/`0x7ffaae3d`.
   The `CLOG`/`PFCL` contents are in SPI NOR and are untouched by a firmware activate, so the
   test fires again and forces `0x80000009` again.
2. The one override that *does* exist — `SYS: Load-n-go boot override of failed shutdown.`
   (StrId 3043) — is reached only from `0x7ffaaf6b`, which is the convergence point of states
   **5, 6 and 7 only**. State 9 (`POST CRASH Startup`, which is what the crash-section test
   forces) has a different dispatch edge. So load-n-go overrides "the shutdown didn't finish",
   **not** "there is a crash record".

Corollary that is worth stating plainly: **a firmware commit is the right tool for the wrong
half of the problem.** It would have rescued the drive at the moment of the *first* unclean
start, before the stub was written. It cannot undo the stub.

Commit Action 0b011 ("activate immediately, no reset") specifically: this firmware's own
strings say a restart *is* required (791/792/793) and the reset is performed internally, so
0b011 is unlikely to be honoured as a true no-reset activation. **SPECULATIVE** — I did not
decode the CDW10 action parsing.

### 6.3 Other standard admin commands

| command | reachable while latched? | destructive? | assessment |
|---|---|---|---|
| Identify (0x06), Get Log Page (0x02), Get Features (0x0A) | yes — field evidence: `id-ctrl`, SMART and error log all return data | no | these are the observable channel, not a fix |
| Firmware Download (0x11) / Commit (0x10) | UNKNOWN whether gated; machinery present | no | see §6.2 — does not clear the latch |
| Format NVM (0x80) | UNKNOWN | **YES, destroys data** | do not use. Also `0x80` appears in the *sanitize* opcode list at `0x7ffa6cc6` region, so it is gated by something |
| Sanitize (0x84) | UNKNOWN | **YES, destroys data** | do not use |
| Device Self-test (0x14) | UNKNOWN | no | no code path found that would clear a crash section; nothing suggests it helps |
| Namespace Management/Attach (0x0D/0x15) | UNKNOWN | Management is destructive | the namespace is not detached — `unvmcap == 0` means it is still allocated — so re-attach is not the missing step |
| Set Features (0x09) | `0x09` appears as a plain constant in the gate routine (`movi a9,9` at `0x7ffa6b6b`) | no | **INFERRED that opcode 0x09 is one of the gated opcodes**, i.e. Set Features is likely *blocked* while latched |

**The namespace is not "detached" and does not need re-attaching.** The field triage rule
`tnvmcap` full + `unvmcap == 0` proves the capacity is still allocated to a namespace; the
controller is declining to *present* it. Nothing in the Post Crash path detaches or deletes it.

### 6.4 The vendor path that actually targets this: OAM ERASE

**PROVEN.** `PROC8_30000000` contains the OAM command worker. The erase sub-command
dispatcher is at **`0x300336c6`**:

```
300336c6: l8ui a11,a12,0x8d          ; sub-command byte
300336c9: beqz  a11,0x30033772       ; sub-cmd 0
300336cc: <cmp> b12 -> 0x30033795    ; sub-cmd 1
300336d4: <cmp> b12 -> 0x300337b8    ; sub-cmd 2
300336dc: beqi  a11,3,0x30033661     ; sub-cmd 3
300336df: <cmp> b12 -> 0x300337db    ; sub-cmd 4
300336e7: <cmp> b12 -> 0x300337fe    ; sub-cmd 5
300336ef: beqi  a11,6,0x3003374f     ; sub-cmd 6
300336f2: log 1636 "OAM ERASE CMD: Received Bad Erase sub-cmd: %d."
```

Each handler is a uniform 4-bundle block that fills three fields at `[a5+0x118]`,
`[a5+0x11c]`, `[a5+0x120]` and calls the same submit routine `0x30030aa0`. The failure
branches identify the operations available:

| StrId | operation |
|---|---|
| 1628 | Erase to system area 0 |
| 1629 | Erase to bad block table 0 |
| 1630 | Erase to BIST Script |
| 1631 | Erase to BIST Status |
| **1632** | **Erase to SBL EEPROM — permanent brick if used** |
| **1633** | **Drive Uninit — destructive** |
| 1634 | Erase to Crash Dump (`CLOG`) |
| 1635 | Erase to PFail Crash Dump (`PFCL`) |
| 2933 | Schedule reinit after crash dump erase |

**This is the mechanism that exits the mode.** `Schedule reinit after crash dump erase`
(StrId 2933) is issued in the same handler as the crash-dump erase, and the SYS side has the
matching StrId 1338 `SYS: Scheduling drive re-init on next startup` (PROC0 `0x7ffa8528`),
which writes marker state 3 (`Drive REINIT requested`) — a state the dispatcher maps to
`SYS: Drive re-init` (StrId 1266) and boots normally from.

**I deliberately do not state which numeric sub-command value corresponds to the crash-dump
erase.** I could not decode the two comparison bundles that select sub-commands 1, 2, 4 and 5,
and the dangerous operations (SBL EEPROM erase, Drive Uninit) are numerically adjacent to the
useful ones. Guessing here bricks the drive permanently. The authoritative mapping must come
from the vendor tooling (`libdmi_core.so` / the WD Device Manager CLI), not from probing.

### 6.5 Does the erase need a power cycle to take effect?

**INFERRED.** The erase itself is an SPI-NOR operation and is effective immediately and
persistently. The reinit is described by the firmware's own strings as scheduled *on next
startup*. The firmware supports several host-triggerable restarts that re-run PROC0 startup:

| reset | firmware evidence | notes |
|---|---|---|
| Controller Reset (CC.EN 1→0→1) | StrId 693 `CC.EN 0->1 Enable Controller on port %d`, 697 `CC.EN 1->0`, 901 `Controller Reset Detected` | `nvme reset` |
| NVM Subsystem Reset | StrId 701 `NVME Subsystem Reset Initiated`, 702 `NVME Subsystem Reset port %d cc 0x%x csts 0x%x`, 899 `Subsystem Reset Detected`, 900 `Subsystem Reset port %d force full link retrain.` | `nvme subsystem-reset`, needs CAP.NSSRS=1 |
| FLR | StrId 898 `FLR Function Level Reset Detected` | `/sys/.../reset` |
| Hot / Fundamental reset | StrIds 896, 897 | |
| FWA internal re-init | StrIds 921, 922 | triggered by Firmware Commit |

So *in principle* `OAM erase crash dump` followed by `nvme subsystem-reset` should be enough,
with no chassis power cycle. **I could not prove this end to end**, and there is a real
counter-argument documented in the field notes: every in-band reset drops `CC.EN` or the link
without a preceding `CC.SHN`, which is itself an unclean stop and re-arms the marker. If the
reinit is scheduled but the *next* start is itself unclean, you may simply re-latch.

That gives a concrete ordering requirement that I believe is the crux:

> Whatever restart is used after the erase must either (a) be preceded by a proper
> `CC.SHN` shutdown notification so the marker is written as `CLEAN shutdown`, or (b) be a
> restart the firmware itself initiates (the FWA re-init path), which does perform its own
> shutdown first (`FWA shutdown finished, perform re-init reset...`).
>
> A bare `nvme reset` / FLR / link toggle does neither. That, and not any property of SPI
> NOR, is the reason a chassis power cycle has appeared to be mandatory — a real power cycle
> at least lets the PFAIL path run to completion and write `PFAIL shutdown` (marker 2), which
> is a *good* marker.

**Practical, evidence-backed suggestion (INFERRED, not proven):** the clean way to stop the
drive so the marker is written correctly is to make the host issue the NVMe shutdown
notification — `nvme set-feature`-free, just unbind the driver, which makes the kernel do a
proper `nvme_shutdown_ctrl` (CC.SHN=01, poll CSTS.SHST=10):

```sh
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/unbind
```

This is the only host action I found that produces `CC.SHN`. Everything else in the reset
ladder is an unclean stop from the drive's point of view.

### 6.6 NVMe-MI over SMBus — the sideband is fully implemented

**PROVEN** from the PROC-level string table (StrIds 163–232, 3260, 3434–3435). This is not a
stub; it is a complete NVMe-MI management endpoint over MCTP/SMBus:

| capability | StrId | significance |
|---|---|---|
| **`MI: Initiating an NVM subystem reset`** | 164 | **NVMe-MI can trigger an NVM Subsystem Reset out of band** |
| `MI: NVMe-MI: Unhandled reset type %x` | 169 | a reset-type parameter is parsed, so more than one reset is supported |
| `MI_AdminCmdHandler` + `MI: unhandled admin cmd opcode %x` / `Invalid admin cmd opcode %x` | 212–216, 171, 172 | **arbitrary NVMe admin commands are tunnelled over the sideband**, with their own opcode filter |
| `Admin: NVMeMI Admin Cmd - Firmware Download Image` / `- Firmware Activate` | 2035, 2036 | firmware download *and* activate are explicitly reachable over MI |
| `MI_PCIECmdHandler` `PCIE_CONFIG_READ` / `PCIE_CONFIG_WRITE` / `ACCESS DENIED` | 217–227 | PCIe config space is readable and writable out of band |
| `MI_ReadMiDataStructureCmdHandler`, `MI_ConfigurationGet/Set` (SMB_FREQ, MCTP_TU_SIZE), VPD read/write, Health Status Poll | 191–210 | full MI command set |
| `MI: Startup complete.` / `PCIE: Unsupported Vendor Defined message, or MI not started: ignoring` | 211, 883 | MI has its own startup, independent of the PCIe data path |
| `VUC_MI_TEST_COMMAND_INJECT_CMD` / `_RETR_RESP`, `Admin_VUC_Mi_Test_OVL022` | 3368, 3369, 1547, 1548 | a vendor command specifically for injecting commands over MI |

This matters enormously for the `UEFI0067` scenario: when BIOS disables the downstream port,
the PCIe path is gone, but **MCTP-over-SMBus on the U.2 sideband (SMBus clock/data on pins
SMB_CLK/SMB_DAT) is a separate physical path** and the management endpoint is still up. On a
Dell R640 with iDRAC, that bus terminates at the BMC.

Caveats, honestly stated:

* **INFERRED, not proven:** that the MI admin-command tunnel bypasses the post-crash gate.
  StrId 3434/3435 (`MI_AdminCmdHandler: Admin mgr in reset, sleep for 200 ms`) shows MI hands
  the command to the *same* Admin manager, so the same gate at `0x7ffa6b18` almost certainly
  applies. MI does not give you a bigger command set; it gives you a *different transport*.
* **UNKNOWN:** the SMBus slave address and whether Dell's iDRAC exposes any raw MCTP/NVMe-MI
  passthrough. iDRAC does consume NVMe-MI health data (it is how the drive appears under
  `Storage`), but Dell does not document a user-facing raw MI command channel. `ipmitool raw`
  bridging to the SMBus is hardware-dependent and I did not verify it for this platform.
* The NVM Subsystem Reset that MI can initiate is, again, an *unclean* stop for the drive.

### 6.7 UART / serial console

**Not determined by me.** I found no console prompt, command-name table, or `baud`-type
strings in the KNGND122 string table. There is a CSI-escape-handling string
(StrId 39 `Skipping merrily over CSI escape sequence`) which implies a terminal-oriented
output path exists somewhere, and the whole log ABI implies a debug transport. Whether that
transport is a physical UART on the HHHL card and whether it accepts input is **UNKNOWN**
from my analysis.

---

## 7. Answers to the five specific questions

1. **Every path that writes a crash / pfail / unexpected-start record, and its guard.**
   - `UNEXSTRT` stub → written when the startup marker is 5/6/7 (`Normal Shutdown STARTED`,
     `PFAIL Shutdown STARTED`, `PFAIL Shutdown TIMEOUT`) **and** the boot is not a load-n-go
     boot. Guard proven at `0x7ffaaf6b`; write proven at `0x7ffaad45`–`0x7ffaad4e`.
   - Real crash dumps → written by the assert/exception handler into `CLOG`; PFail crash dumps
     into `PFCL`. I did not trace those handlers; their existence is proven by StrIds 1224/1225
     ("PFail Crash Dump", "Crash Dump"), 1277–1282 and the separate OAM erase operations.
   - Forced state 9 without any new record → `SYS: Unexpected empty System Area.` (StrId 3519,
     `0x7ffaae4a`).
2. **Does an unclean start alone suffice, or does it need a dirty L2P?**
   **It suffices alone.** No L2P, journal, or deallocate state is consulted anywhere on this
   path. The only inputs are the marker and the crash-section emptiness. (PROVEN for the
   decision inputs; the marker being set to a "STARTED" value is of course itself caused by
   whatever prevented the shutdown from finishing.)
3. **How is a clean shutdown detected and persisted?**
   The NVMe shutdown notification `CC.SHN` is consumed by the PCIe manager
   (StrId 696 `CC.SHN controller shutdown port %d type 0x%x`), which drives
   `PCIe_RequestShutdown` → `PCIe_ShutdownThread` → per-manager `SHUTDOWN_REQ`/`SHUTDOWN_CPL`
   (StrIds 843–860). SysMgr writes the marker: `Normal Shutdown STARTED` when it begins,
   `CLEAN shutdown` when every manager has completed. Persisted as an 8-byte ASCII breadcrumb
   in the EEPROM System-Area journal (StrIds 1236–1254 EEPROM journal, 1259 bread crumbs).
   PFAIL is the parallel path: `SYS: PFAIL is detected` (1209) → `PFAIL Shutdown STARTED` →
   `PFAIL shutdown` on completion, or `PFAIL Shutdown TIMEOUT` (StrId 1210
   `SYS: PFAIL timeout is expired`).
4. **Is PCIe link loss handled as a power fail?**
   **No.** PFAIL is driven by the voltage/capacitor monitor (`VCAPOK`, StrIds 1212 `PFAIL
   interrupt enabled`, 1258 `PFAIL power = % 5d mW`, 3516 `Delayed SAM startup by %d ms
   (waiting for VCAPOK)`). Link events go through an entirely separate PCIe reset/shutdown
   state machine (StrIds 896–901, 3108–3111). A link drop is *not* a PFAIL. It is dangerous
   for a different reason: it makes `CC.SHN` undeliverable, guaranteeing the next stop is
   unclean.
5. **Does the mode hide the namespace or rebuild/erase the L2P?**
   **It hides it.** The decision is made in SPI/EEPROM processing before the media managers
   start; the only write is ~1 KB into SPI NOR. Field corroboration: `tnvmcap` full with
   `unvmcap == 0` while latched means the capacity is still allocated to a namespace.
   (INFERRED-high; I cannot prove the negative exhaustively.)

---

## 8. Safety — what NOT to do

- **Do not probe OAM erase sub-command values.** Sub-command space `0..6` contains
  `Erase to SBL EEPROM` (permanent brick — the secondary boot loader lives there and the
  drive will not POST again) and `Drive Uninit` (destructive), adjacent to the crash-dump
  erase. I identified the *operations* but not the *numeric encodings*, and I will not guess.
- Do not run `nvme format`, `nvme sanitize`, `delete-ns`/`create-ns`, or `wdc purge`.
- Do not assume Commit Action 0b011 is a safe no-op probe; a failed activate on a drive whose
  SPI state is already suspect is not a free experiment.
- Reading is free: `nvme id-ctrl`, `smart-log`, `error-log`, `fw-log`, `get-log`, and the
  vendor E6/`GetDiagnosticData` retrieval (StrIds 1960–1963) are the intended interface in
  this mode and cannot make anything worse.

## 9. Bottom line on host-side escape

**INFERRED, medium-high confidence: a host-side escape probably exists, but it is not a
standard NVMe command — it is the vendor OAM crash-dump erase, and the restart afterwards
must be a clean one.**

Reasoning:
- The latch lives in SPI NOR (`CLOG`/`PFCL`). Only an erase clears it. **PROVEN.**
- An erase operation for exactly those two sections exists and is paired with a scheduled
  reinit. **PROVEN.**
- The post-crash gate blocks an enumerated opcode list, not everything, and the mode exists
  precisely so that diagnostic data can be pulled — so the OAM/VUC transport is essentially
  certainly still live. **INFERRED-high.**
- The firmware can restart itself internally (FWA re-init) and honours host controller /
  subsystem / FLR resets. **PROVEN.**
- Therefore power cycling is **not structurally required**; what is required is (a) clearing
  the SPI section and (b) a restart whose *preceding* stop was clean. A bare `nvme reset` is
  not clean; driver unbind (which issues `CC.SHN`) is.

**What I could not establish, and it is the deciding gap:** the exact vendor opcode +
sub-command encoding for "erase crash dump", and whether that command is on the post-crash
deny-list. Both of those live in WD's `libdmi_core.so` / Device Manager, which is the correct
and safe place to get them from.

If that encoding cannot be obtained, then in practice the answer for this owner is: **no safe
host-side escape is available to you, and a power cycle after clearing the crash section is
the only route** — with the important corollary that a power cycle *alone* will not clear it
either, so a drive that keeps returning to this state after power cycles has a crash section
that was never erased.

---

## 10. Corroborated / extended findings on the out-of-band and serial paths

A second pass (independent worker, same firmware tree, no access to the other agent's doc)
confirmed and extended §6.6 and §6.7. Summary of what is now solid:

### 10.1 NVMe-MI — PROVEN, and it runs on **two** transports

The MI stack lives in **`PROC9`**; the SMBus PHY/master is in **PROC0**. Handlers located by
descriptor + `l32r` xref: `MI_CommandRouter` (`0x7ffb25c3`), `MI_ControlPrimitiveHandler`
(`0x7ffb2a88`), `MI_GetHealthStatusCmd/CplHandler`, `MI_VpdReadWriteCmd/CplHandler`,
`MI_ReadMiDataStructureCmdHandler`, `MI_ConfigurationGet/SetCmdHandler`, `MI_AdminCmdHandler`
(`0x7ffb5fd5`), `MI_PCIECmdHandler` (`0x7ffb6646`).

MCTP runs over **SMBus and over PCIe Vendor-Defined Messages simultaneously** — the log format
is `MCTP-%s:` with the two instance names `SMB` (StrId 235) and `PCIE` (StrId 236), and
`MCTP_CommandHandler` has separate "cannot queue smb response" / "cannot queue pcie response"
arms (StrIds 289/290). So the sideband is genuinely independent of the PCIe data path.

**Out-of-band NVM Subsystem Reset: present but gated.**
StrId 164 `MI: Initiating an NVM subystem reset` (`0x7ffb105b`), but also StrId 3240
`MI: NVMe-MI: subsystem reset received for unsupported board type` (`0x7ffb26af`, inside
`MI_CommandRouter`) and StrId 169 `Unhandled reset type %x`. **UNKNOWN** whether this
board type is supported and which reset types are accepted — the compare chain is in FLIX
slots.

**MI admin-command tunnel opcode whitelist — partially PROVEN** (clean `movi` immediates in
the chain ending at StrId 171 `MI: unhandled admin cmd opcode %x`, `0x7ffb25a5`):

| addr | opcode | command |
|---|---|---|
| `7ffb2546` | `0x11` | Firmware Image Download |
| `7ffb2568` | `0x0D` | Namespace Management |
| `7ffb2572` | `0x15` | Namespace Attachment |
| `7ffb2584` | `0x81` | Security Send |
| `7ffb258f` | `0x82` | Security Receive |
| `7ffb259a` | `0xBF` | **vendor-unique, MI-specific** |

Four to five further comparisons at `0x7ffb2548`–`0x7ffb2564` are FLIX-encoded and
**UNDECODED**; `0x10` (Firmware Commit), `0x02` (Get Log Page) and `0x06` (Identify) could not
be confirmed present or absent. Note the host-side vendor opcode used throughout PROC8 is
**`0xDD`**, which is *not* in the MI whitelist — so the MI path is not a drop-in substitute for
the host VUC channel.

Also present: `MI_PCIECmdHandler` can do `PCIE_CONFIG_READ`/`PCIE_CONFIG_WRITE` out of band
(StrIds 219–223, 3258), behind an `ACCESS DENIED` check whose condition is **UNKNOWN**; and
`MI_VpdReadWriteCmdHandler` can **write** the FRU/VPD EEPROM out of band (PROC0 StrId 1143
`SMBus2: VPD write to 0x%02x, length 0x%02x`, `0x7ffaa616`).

MCTP control commands `Reset` and `Discovery` are explicitly **rejected** on the SMBus binding
(StrIds 262, 264).

**SMBus slave address: UNKNOWN.** The drive is SMBus-ARP capable (`SMBusTxState_notify_arp_master`,
which sends to host address `0x08` — proven at PROC0 `0x7ffaa82e`), and its own slave address
appears to come from BoardConfig+0xB2 rather than a firmware constant. A `0x6A` sighting at
`0x7ffaa90a` is inside a FLIX bundle and must **not** be relied on.

**No NVMe-MI Basic Management Command path found** (no `Basic Management` / block-read-at-0x00
strings anywhere). **UNDETERMINED** rather than disproven.

### 10.2 Serial console — PROVEN, and it is real

- Prompt string **`"DiagMgr> "`** at PROC0 `0x7ff822d4`, referenced from `0x7ffa1238`.
- Output goes to a UART (StrId 35 `Sending suspicious value 0x%x to UART`, ref `0x7ffb4b0e`).
- **115200 baud (INFERRED-strong)**: `0x0001C200` occurs exactly once in the whole firmware, at
  PROC0 literal `0x7ff83cc0`, and the adjacent literal `0x7ff83cc4` is the pointer to the
  console command-group table `0x7ff81710` — same init routine.
- Full ANSI line editor with arrow-key history at `0x7ffb4b58`–`0x7ffb4c40`.
- Command tables are 20-byte records `{name*, shortHelp*, longHelp*, fn*, nParams}`.

Shipped command set on PROC0:

| group | command | help |
|---|---|---|
| `native` | `Help` | `Help [<command>]` |
| `native` | `Mode` | exact vs flexible command matching |
| `native` | `Load` | `Load [<command-group>]` — **loads additional command groups at runtime** |
| `SYS` | `SBL` | `SBL - Go into SBL diagnostic mode` |
| `SYS` | `GPRS` | display GPRS registers |
| `SYS` | `I2CErase` | erase the **I2C** FRU EEPROMs (*not* the SPI crash sections) |
| `SYS` | `LogicTrap` | simulate logic trap |
| `VHIST` | `vhist` | PCIe SerDes eye histogram |

`Load` pulls in further groups from `SBL.bin` / `BIST.bin` / `UEFI.bin` / `SECURITY.bin`
(names at PROC0 `0x7ff8223c`–`0x7ff822c4`). Those images are **not** in this firmware package,
so the loadable command set — which is where any crash-section manipulation would live — is
**UNDETERMINED**. No shipped console command touches `CLOG`/`PFCL`.

**Physical pins: UNKNOWN.** No pinmux, `RS232`, or `JTAG` strings exist in any image, and
whether a UART is populated on the HHHL/U.2 card is not answerable from firmware.

### 10.3 SPI TOC — where the latch physically lives

**PROVEN.** PROC0 `0x7ff84a74` holds the default SPI-EEPROM table of contents, 8-byte records
`{tag[4], id, size, offsetLo, offsetHi}`:

```
#SBL STOC STOC DRVC DRVC UEFI UEFI MBBB MBBB
FRMW FRMW FRMW FRMW FRMW SLOT SLOT SYSB
TCG! TCG! CLOG PFCL BSCR BSCR BSTA BSTA BLOG BLOG
```

`CLOG` = section id **0x0B** (`0x7ff84b08`), `PFCL` = section id **0x0A** (`0x7ff84b10`).

### 10.4 Reading the dump before erasing it

StrId 1606 `VUC Get Drive Log SubCmd %08X` plus 1607–1610 (`SPI Crash Section is in an invalid
state`, `Get Crash Dump Size - no valid crash dump available`, and the PFail equivalents) show
a **Get Drive Log** VUC sub-command that returns crash-section size and contents. That is the
read-only, safe first step, and it also *confirms* whether a crash section is actually present
before anyone reaches for an erase.

---

## 11. The vendor command set — PROVEN encodings from `libdmi_core.so.0.39`

Independently extracted from `dm-core-2.5.1-7.x86_64.rpm`
(`opt/Western_Digital/dm-cli/lib/libdmi_core.so.0.39`, image base `0x100000`). The SN200 is
matched by `scan_factory` @ `0x167950` on PCI **1C58:0023**, class chain
`Omaha → GallantFox → HGSTNVMeController → NVMeController`.

**Transport (PROVEN, decompiled):** `gf_nvme_vuc_simple_real` @ `0x18bf90` builds a raw
64-byte admin SQE with

```
SQE.opcode = opcode
SQE.cdw10  = cdw10
SQE.cdw12  = (sub2 << 8) | sub1
```

So the `cdw12` values below decompose as `sub2:sub1`.

### The two commands that matter here

| encoding | function | meaning |
|---|---|---|
| `opcode 0xFF, cdw12 = 0x0503` | `gf_nvme_clear_crash_dump` @ `0x18b010` | **clear the CLOG crash-dump section** |
| `opcode 0xFF, cdw12 = 0x0603` | `gf_nvme_clear_pfail_dump` @ `0x18b060` | **clear the PFCL pfail-crash section** |

`sub1 = 0x03` is the OAM ERASE family; `sub2` is the erase sub-command number from the
firmware dispatcher at `0x300336c6` (§6.4). So **sub-command 5 = Crash Dump, 6 = PFail Crash
Dump** — which is exactly what the firmware's `beqi a11,5` / `beqi a11,6` arms decode to. My
§6.4 "sub-command number → target mapping is unknown" is now closed for the two that matter,
from the vendor library rather than from probing.

**This also names the adjacent hazards precisely.** The firmware error strings put
`Erase to SBL EEPROM` and `Drive Uninit` immediately before `Erase to Crash Dump`, i.e. at
sub-commands **3 and 4** → `cdw12 = 0x0303` and `0x0403`. Those are one and two digits away
from the useful values and are a permanent brick and a wipe respectively. `libdmi_core`
contains **no** encoding for either, so they cannot be reached by accident through the vendor
tool — only by hand-rolled `admin-passthru`. **Never type `0x0303` or `0x0403`.**

### Read-only state probes (all safe, PROVEN)

| encoding | function | returns |
|---|---|---|
| `0xFF, cdw12 = 0x0004`, 4 B result | `gf_nvme_sys_init_done_real` @ `0x18b0b0` | result **byte 1 == 6 ⇒ diagnostic / Post Crash mode** (consumed by `gf_is_diagnostic_mode` @ `0x142c90`) |
| `0xC6, cdw12 = 0x0320`, cdw10=2, 8 B in | `gf_nvme_get_crash_dump_size_real` @ `0x18bdf0` | crash-dump size; SC `0xC3` ⇒ no dump present |
| `0xC6, cdw12 = 0x0520`, cdw10=2, 8 B in | `gf_nvme_get_pfail_crash_dump_size_real` @ `0x18bc50` | pfail-dump size; SC `0xC3` ⇒ none |
| `0xC6, cdw12 = 0x0420`, cdw10=len/4 | `gf_cd_get_crash_dump` @ `0x13b650` | read the crash dump |
| `0xC6, cdw12 = 0x0620`, cdw10=len/4 | `gf_cd_get_pfail_crash_dump` @ `0x13b6b0` | read the pfail dump |
| `0xC6, cdw12 = 0x0120`, cdw10=2 | | sizes of binary drive log + string table |
| `0xC6, cdw12 = 0x0020` / `0x0220` | | binary drive log / string table |
| `0xE6`, cdw10=numDW, cdw11=DW offset, cdw12 = `mode \| (dumpID<<8)` | `hgst_nvmec_cap_diags_get_data` @ `0x148da0` | E6 diagnostic dump; first 8 B at offset 0 give a **big-endian** total length at bytes[4..7] |

**The single highest-value measurement on a latched drive is the pair of size probes
(`0x0320` and `0x0520`)** — they say *which* section is armed, which decides whether the
subsequent clear is the harmless one or the one that schedules a re-init.

### ☠ Destructive vendor encodings — enumerated so they can be avoided

| encoding | what it does |
|---|---|
| `0xDD` | **Start Secure Purge** — whole-drive crypto purge (`hgst_nvme_secure_purge` @ `0x146480`) |
| `0xD8` + NSID | namespace delete |
| `0xD9, cdw12 = 0x0000` / `0x0001` | namespace create-modify / resize |
| `0xCC, cdw12 = 0x0103`, cdw13 = capacity | drive resize |
| `0xDC` + NSID, cdw13 = state | namespace attach-state change — **misleadingly named `gf_nvme_ns_status`; it is a writer** |
| `0x0303` / `0x0403` on opcode `0xFF` | SBL EEPROM erase (brick) / Drive Uninit — *not* encodable from the vendor library |

### Negative results from the vendor library — PROVEN

- **There is no "exit diagnostic mode" command, no startup-marker writer, and no reinit
  command.** The enum names `HDME_CMD_EXIT_MODE`, `SET_MODE`, `WRITE_MARKER`,
  `WRITE_SYSTEM_FILE` exist as `.rodata` strings for an internal FA build but appear in **zero**
  dispatch tables and have no opcodes.
- `gfc_reset_to_defaults` @ `0x141d60` is beacon-off + threshold restore + resize-to-default +
  a plain **host-side** `NVME_IOCTL_RESET` (controller reset 5, or subsystem reset 4). Not a
  device escape.
- "Drive uninit" in `dm-cli` (`HDMS_UNINITIALIZED`) is `nvmec_prepare_for_removal` — PCIe
  hot-remove via sysfs. Unrelated to the firmware's `Drive Uninit` erase sub-command.
- After a successful clear, `hgst_nvmec_cap_diags_end` @ `0x148b20` returns
  `HDMS_SHUTDOWN_REQUIRED`. **WD's own tool expects a restart afterwards** — consistent with
  there being no in-band exit command.
- `libdmi_core` speaks **only** `ioctl`/`open` (NVMe passthru + SG_IO). **No NVMe-MI, no
  SMBus/I2C, no MCTP, no serial.** So the sideband and console paths in §10 exist in the
  *drive* but there is no vendor tool that drives them.

---

## 12. Revised answer: is there a host-side escape?

**Yes for the state change; a restart is still required, and it does not have to be a chassis
power cycle — but nothing proves an in-band restart is sufficient.**

1. The latch is `CLOG`/`PFCL` being non-erased. **PROVEN.**
2. `0xFF/0x0503` and `0xFF/0x0603` erase them, and are reachable while latched (field-observed
   Success). **PROVEN encodings; reachability empirical.**
3. There is no command that exits the mode in place. `HDMS_SHUTDOWN_REQUIRED` is WD's own
   answer. **PROVEN negative.**
4. The firmware honours controller reset, NVM subsystem reset, FLR, hot reset, and its own
   FWA re-init. **PROVEN** it detects them; **UNKNOWN** whether the post-erase reinit is
   consumed by a warm restart or only by a full boot.
5. The failure mode of every in-band reset is that it is itself an unclean stop (no `CC.SHN`),
   which re-arms marker 5/6/7 and can re-write an `UNEXSTRT` stub — putting you straight back
   in. **PROVEN mechanism, INFERRED that it actually bites in this sequence.**

Therefore the ordering that has the best chance, and the only one I would put forward:

```
1. probe:   0xC6 cdw12=0x0320   (crash size)      -> is CLOG armed?
            0xC6 cdw12=0x0520   (pfail size)      -> is PFCL armed?
            0xFF cdw12=0x0004                     -> byte1 == 6 confirms diagnostic mode
2. pull the E6 dump first (0xE6) if you want the evidence — clearing destroys it
3. clear ONLY the section(s) actually armed
4. stop the drive CLEANLY:  echo 0000:BB:DD.F > /sys/bus/pci/drivers/nvme/unbind
   (this is the only host action that issues CC.SHN + waits for CSTS.SHST=10b)
5. restart:  rebind, or a subsystem reset, or power cycle
```

Step 4 is the part everyone has skipped, and it is the difference between "the reinit runs on
a clean next boot" and "the next boot is another unexpected start". **INFERRED**, but it
follows directly from §2.

**Caveat that could invalidate the whole thing (from the other agent's work, and I have not
independently verified it):** the crash-dump clear `0x0503` is the *only* one of the eight
erase arms that does extra work on success — it branches to `0x30033704`, which reads the
PROC8 startup-type global at `0x7ff87c64` and schedules the re-init. If "Drive re-init"
(marker 3) rebuilds the System Area / L2P, then **clearing CLOG costs you the data**. I did
not trace what re-init does, but I note it is coherent: the *other* producers of marker 3 are
`SYS: Detected an erased SysArea.` and `SYS: Found an incompatible SA` — conditions where the
metadata is already gone. `0x0603` (pfail) has no such second step.

If only `PFCL` is armed, `0x0603` alone may clear the latch with no re-init and no wipe. That
is the measurement worth taking before anything else, and it has never been taken.

---

## Disagreements with `docs/sn200-firmware-re.md`

Read only after everything above was written. We converge on the core mechanism —
marker enum, `0x8000000N` encoding, the four System-Area overrides, `UNEXSTRT`, the
`0x8F8A0000` → `0x7C5` gate, the OAM erase family, the FLIX/Ghidra warning. The
disagreements below are where independent work actually diverges.

### D1. The startup-type mapping for markers 5/6/7 — **I disagree, and I have the stronger evidence**

That document (§2) asserts an INFERRED 1:1 parallel in which markers 5/6/7 map to startup
types **1269/1270/1271** (`SYS: ERROR - Shutdown started but never finished`, etc.).

An exhaustive scan for `movi <reg>, <1264..1275>` — in both the plain 3-byte encoding and the
format-`0xb` FLIX slot-0 encoding — across **all 17 flat images** finds
1264, 1265, 1266, 1267, 1268, 1272, 1273 and 3043, and **never 1269, 1270 or 1271**.
Those three strings are dead in KNGND122.

What markers 5/6/7 actually produce, at their shared handler `0x7ffaaf6b`, is either
**StrId 3043 `SYS: Load-n-go boot override of failed shutdown.`** (if the load-n-go flag at
`[0x7ff9ff60+4]` is set — in which case the marker is cleared and the boot proceeds normally)
or the crash path ending at `movi a11,1273` = **`SYS: Post Crash startup`**.

This is not a cosmetic difference. It means a "shutdown started but never finished" boot is
not merely *reported* as an error — it **is** the Post Crash path, with a load-n-go escape
hatch. That closes the gap the other document explicitly leaves open.

### D2. Model B is stronger than "PLAUSIBLE, NOT PROVEN" — **I disagree**

That document rates model B (UNEXSTRT) as unproven, with the objection: *"Cannot be
unconditional or every SN200 would brick on power loss."*

The objection is correct and is answered by the gate, which I traced: the `UNEXSTRT` path is
**not** entered on "any unclean start". It is entered on markers **5/6/7 only** — a shutdown
or PFAIL save that *started and did not finish*. A power loss whose capacitor-backed PFAIL
save **completes** writes marker 2 and boots as `SYS: PFAIL startup`, a designed, non-latching
path. That is exactly why every SN200 does not brick on power loss.

So B is confirmed, with a precondition that is narrower than "unclean" and wider than
"deallocate".

### D3. Model C "downgraded" — **framing disagreement, same facts**

That document downgrades C ("abrupt power loss") because a *clean* pfail produces marker 2.
Agreed — but C is not a separate model at all. Markers 6 and 7 (`PFAIL Shutdown STARTED`,
`PFAIL Shutdown TIMEOUT`) are *inputs to B*. B and C are one mechanism. Calling C "downgraded"
risks reading as "power events are safe"; the accurate statement is **"power events are safe
iff the hold-up sequence completes"**, which for a drive with aged capacitors is not a given.

### D4. Model D — **I refute the stated form; we agree on the consequence**

D as literally posed ("a PCIe link drop is indistinguishable from power loss to the
controller") is **false**, and the other document agrees: PFAIL is a PROC0-local brownout ISR
that PCIe code cannot reach. Where we differ is emphasis. That document routes D through
*hang → logic trap → crash dump*, backed by WD errata text I did not have. I route it through
*link down → `CC.SHN` can never be delivered → the stop is guaranteed unclean → marker 5/6/7*.
Both are real; mine needs no firmware bug, only a disabled port, which is precisely what
`UEFI0067` produced. For latch #2 mine is the simpler explanation.

### D5. The admin-gate polarity — **that document's "whitelist" is not usable, and neither was my first reading**

It reports extracted constants `0, 1, 2, 4, 5, 6, 8, 10, 10, 12, 16, 12, 128, 8, 32, 10, 32,
256` and correctly notes the duplicates prove the field extraction is wrong. I initially
concluded the chain was a deny-list; I have retracted that (§6.1) because the instructions
before the rejection decode as dead code, meaning the sweep desynced at the merge point.

Where I **do** disagree: that document (and one of my own helper passes) treats the
`0x7ffa6c80`–`0x7ffa6cc9` chain — containing `0xDD`, `0x81`, `0x82` — as post-crash-related,
and states *"the vendor command DDh is explicitly permitted while the drive is in Post-Crash
mode, which is how the OAM erase would be delivered."*

That is wrong twice over and the second error is dangerous:

1. That chain converges on `0x7ffa6d4b` → `0x7ffa6cd4` → the **sanitize** rejection log
   (StrId 3370), not the post-crash one. `{Security Send, Security Receive, Secure Purge}` is
   textbook sanitize-restriction membership.
2. **`0xDD` is not "the vendor opcode". `libdmi_core` proves `0xDD` is `Start Secure Purge`**
   (`hgst_nvme_secure_purge` @ `0x146480`) — a whole-drive crypto erase. The OAM erase is
   delivered on **`0xFF`**, not `0xDD`. Anyone acting on that sentence would destroy the drive.

### D6. "Every start not preceded by a recorded clean shutdown re-arms the crash section"

This sentence appears in that document's §7 and has propagated into
`.claude/skills/nvme-recovery/SKILL.md`. It over-generalises in the way D2/D3 describe: the
predicate is markers 5/6/7, not "not clean". Marker 2 (completed PFAIL) and marker 0 (no
previous marker) do not re-arm anything. I would rewrite it as:

> Any start whose *previous* stop began a shutdown or a PFAIL save and did not finish it
> re-arms the crash section.

The operational advice built on it ("no in-band reset works") is still right, because a bare
`nvme reset`/FLR/link toggle does leave marker 5.

### D7. "Trigger: large deallocate/TRIM, not power cycling" (the skill file)

The skill's headline is now contradicted by that document's own §8 revision and by my §4.
DISCARD is *sufficient but not necessary*; it is a provocation, not the latch. The skill
should lead with the shutdown-completion predicate.

### D8. Where I defer to that document

- Sub-command numbering (5 = crash, 6 = pfail): it had this before I did, and my independent
  `libdmi_core` extraction confirms it exactly (`cdw12 = (sub2<<8)|sub1`).
- The `0x0503`-schedules-a-re-init-and-that-re-init-wipes claim: I did not trace it and
  cannot confirm or refute it. It is the single most decision-relevant open item.
- The WD errata (OM-6588/6697/6836/6850/7044) and the AEN-starvation trick: I had neither.
- Marker 9's dispatch target `0x7ffabd01`: I read the same `b12` value and dismissed it as
  out-of-function. It decodes to a valid instruction boundary, so their reading is at least as
  good as mine. Undetermined either way.

### D9. Minor corrections in my own favour

- I originally decoded `0x8F8A0000 >> 17 = 0x47C5` as `DNR=0, M=1`. That is wrong; it is
  **`DNR=1, M=0`**, as that document states. Corrected above.
- That document says StrIds 1257/1258 (`PFAIL time`, `PFAIL power`) are compiled out. I had
  cited 1258 as evidence that PFAIL is a power measurement. The conclusion stands on StrId
  1212 (`PFAIL interrupt enabled`) and the ISR at `0x7ffa82dc`, not on 1258.

## What I could not determine

1. **The membership and polarity of the post-crash opcode gate.** Blocked by FLIX operand
   fields. This is the top item; teaching `xdis.py` the bundle formats would close it.
2. **Whether the re-init scheduled by `0x0503` destroys user data.** Decision-critical.
3. **Whether the `UNEXSTRT` stub goes into `CLOG` or `PFCL`.** The log says "crash area",
   which suggests `CLOG`; not proven. If it is `CLOG`, then clearing only `PFCL` will not
   help a drive latched by an unclean stop.
4. **Whether an in-band restart consumes the scheduled reinit**, or whether only a full
   power-on boot does.
5. **Whether NVMe-MI is usable on this platform** — SMBus slave address unknown, board-type
   gate on the MI subsystem reset unknown, and no vendor tool speaks MI at all.
6. **The MI admin-opcode whitelist** beyond the six proven entries; in particular whether
   `0x10` Firmware Commit is tunnelable.
7. **The loadable console command groups** (`SBL.bin`, `BIST.bin`) — not present in this
   firmware package. `SBL - Go into SBL diagnostic mode` is the likely gateway to a much
   larger command set.
8. **Whether a UART is physically populated** on this card.

---

# ADDENDUM — settling the `byte[1] == 6` question

Raised by the coordinator: the field drive's state probe (`0xFF`, `cdw12=0x0004`) returns
`0x00000601`, byte[1] = `0x06`. If that indexes the eleven contiguous marker strings
(CSV lines 3030–3040) zero-based, index 6 is `PFAIL Shutdown STARTED` — which would reframe
the fault as a genuine interrupted power-fail rather than a generic diagnostic mode.

**Verdict: the reframing is not supported. `6` is not a marker value.** It is a different,
smaller enum, and `6` means diagnostic / Post Crash mode. Four independent proofs follow, two
from the drive firmware and two from WD's own library. But — see §A5 — the *hardware* half of
the coordinator's hypothesis is right, and it does change the verdict on models C and D.

## A1. The marker enum IS as printed — proven from a data table, not from string order

I did not infer this from CSV order. PROC0 contains a **u16 lookup table at `0x7ff81180`**
whose entries are StrIds, indexed by `startupState & 0x7fffffff`:

```
0x7ff81180: 3029 3030 3031 3032 3033 3034 3035 3036 3037 3038 3039 0000 0000 0000 0000 0000
             ^0   ^1   ^2   ^3   ^4   ^5   ^6   ^7   ^8   ^9   ^10   <- table ends, zero-padded
```

The index arithmetic is visible in the instruction stream at PROC0 `0x7ffacea`–`0x7ffacfb`:

```
7ffaacea: l32r  a13,0x7ff83338   ; 0x7fffffff
7ffaaced: l32r  a12,0x7ff83438   ; -> 0x7ff81180   (this table)
7ffaacf0: and   a11,a11,a13      ; state & 0x7fffffff
7ffaacf3: <log StrId 3044>       ; "SYS: ERROR - %s but did not complete successfully!!"
7ffaacfb: l16ui a11,a11,0x0      ; table[index]  -> StrId
```

So marker index 6 = StrId 3035 = `PFAIL Shutdown STARTED`, exactly as printed, and the
in-RAM marker word is `0x80000006`. **PROVEN.** The coordinator's caution about string order
was warranted and the table resolves it — but in favour of the printed order.

## A2. The value the VUC returns cannot be a marker — it is masked to 3 bits

In the same PROC0 function, the *startup type* (register `a5`, the value stored to the global
that is later reported) is used as:

```
7ffaac90: s32i.n a5,a12,0x30     ; publish startup type
7ffaac95: extui  a10,a5,0,3      ; a5 & 7          <-- THREE bits
7ffaac98: slli   a10,a10,3
7ffaac9b: call8  0x7ffb11f0
```

`extui a10,a5,0,3` extracts **3 bits**, i.e. the startup type has at most **8** values (0–7).
The marker enum has **eleven** (0–10) and needs 4 bits. They are therefore **different enums**.
**PROVEN.**

This also matches my §2 result: exactly **seven** startup types are reachable in KNGND122
(StrIds 1264, 1265, 1266, 1267, 1268, 1272, 1273 — the exhaustive `movi` scan over all 17
images). Seven values fit 0–6, with `SYS: Post Crash startup` as the last. `beqz` on the same
value selects `SYS: Executing First time startup` (StrId 1276 at `0x7ffaaca5`), so **type 0 =
first startup** — which is independently corroborated by PROC8 StrId 1550 `Admin: First
Startup` being logged on the zero branch of the same global.

## A3. WD's own library says `6` means diagnostic mode — decompiled, not inferred

`libdmi_core.so.0.39` (extracted from the RPM myself; image base `0x100000`).

**`gf_nvme_sys_init_done_real` @ `0x8b0b0`** — this *is* the probe in question:

```asm
0008b0b9  mov  esi, 0xff          ; VUC opcode  0xFF
0008b0c2  mov  edx, 4             ; cdw12 = 4          <- the 0x0004 probe
0008b0b4  xor  ecx, ecx           ; cdw10 = 0
0008b0dc  lea  r9,  [var_20h]     ; 4-byte result buffer
0008b102  call [gf_nvme_vuc_simple_real_ptr]
0008b111  movzx edx, byte [var_21h]  ; result BYTE 1  ->
0008b116  mov  byte [rbp], dl        ;   arg2 = "startup type"
0008b11e  movzx edx, byte [var_20h]  ; result BYTE 0  ->
0008b123  mov  byte [rbx], dl        ;   arg3 = "sys init done"
```

So `0x00000601` decomposes as byte[0] = `0x01` (system init done) and byte[1] = `0x06`
(startup type). Confirms the coordinator's byte extraction exactly.

**`gf_is_diagnostic_mode` @ `0x42c90`:**

```asm
00042cc1  mov  rax, [gf_nvme_sys_init_done_real_ptr]
00042cc8  xor  edx, edx              ; arg3 = NULL (don't want init-done)
00042cca  lea  rsi, [var_fh]         ; arg2 = &startup_type
00042ccf  call [rax]
00042cd1  test eax, eax
00042cd3  jne  0x42ce2
00042cd5  cmp  byte [var_fh], 6      ; <-- startup_type == 6
00042cda  mov  edx, 0xfffff819       ; -2023 = HDMS_DEV_DIAGNOSTIC_MODE
00042cdf  cmove eax, edx
```

**`startup_type == 6` ⇒ diagnostic mode. PROVEN from vendor code.**

The logical closure: this drive is *demonstrably* in diagnostic mode — SCT 7 / SC 0xC5 command
rejections, the Persistent Internal Error AEN, no namespace. WD's own predicate for
"this drive is in diagnostic mode" is `== 6`, and the drive returns 6. For `6` to mean
`PFAIL Shutdown STARTED` instead, WD's diagnostic-mode function would have to be testing the
wrong enum *and* coincidentally returning the correct answer.

## A4. The `== 7` variant does not apply to this drive — it keys on firmware revision, not model

The corroboration offered was that WD gates on `expected == 7` for `HUSMR…` models. That is a
misreading of the gate. **`omc_resolve_device_status` @ `0x674b0`:**

```asm
00067544  call sym.hgst_nvmec_hitachi_block_point_chg_fw
00067549  test al, al
0006754b  je   0x6757f                    ; not "Hitachi" -> gfc_resolve_device_status (== 6)
...
0006756e  call [gf_nvme_sys_init_done_real_ptr]
00067578  cmp  byte [var_fh], 7           ; the == 7 test, only on the Hitachi branch
0006757d  je   0x67598                    ; -> device_status = 0xbbc
0006757f  call sym.gfc_resolve_device_status   ; -> gf_is_diagnostic_mode, == 6
```

and the gate itself, **`hgst_nvmec_hitachi_block_point_chg_fw` @ `0x48790`:**

```asm
00048794  add  rdi, 0x40             ; Identify Controller offset 0x40 = FIRMWARE REVISION
000487a3  mov  esi, 8                ; 8 bytes
000487b9  call hdm_struct_str
000487c7  cmp  byte [rax], 0x48      ; fr[0] == 'H'
000487ce  je   0x487e0
000487d0  xor  eax, eax              ; else return false
...
000487e0  sub  edx, 0x41             ; fr[3] - 'A'
000487e3  cmp  dl, 4
000487e6  seta al                    ; true iff fr[3] > 'E'
```

The predicate is **`FR[0] == 'H' && FR[3] > 'E'`** on the *firmware revision string*, not on
the model number. This drive runs **`KNGND122`** → `FR[0] = 'K'` → gate is **false** → the
`== 7` branch is unreachable, and control goes to the `== 6` path. **PROVEN.**

(Offset `0x40` in Identify Controller is Firmware Revision `FR`; Model Number `MN` is at
`0x18`. The other agent's document flags this same offset as a source of an earlier error of
its own.)

**Conclusion on the enum question: `byte[1] = 6` means "Post Crash / diagnostic mode". It is
not `PFAIL Shutdown STARTED`. The marker enum is a separate, 11-value, 4-bit enum that is
consumed and then cleared during boot and is never what this VUC reports.**

## A5. …but the hardware half of the hypothesis is right, and it changes C and D

Everything above only rejects the *enum* reading. The underlying physical hypothesis —
**a marginal U.2 cable causing genuine 12 V droop rather than a mere signalling fault** —
is well supported, and it is the best single explanation of the field history.

**Q3: can a LINKDOWN/PERST event reach PFAIL handling in code? No. PROVEN.**
The PFAIL monitor object lives at `0x7ff8cd80`. I scanned every literal word of all 17 flat
images for that address: it appears **once, in PROC0 only** (`0x7ff830c0`). The PCIe manager
is PROC9 and cannot address it. There is no software path from link-down to PFAIL.

**But the drive monitors two 12 V rails, and the U.2 cable carries them.** PROC0 strings
1339 `PC12V` and 1340 `ATX12V`, device `I2C_DEVICE_VMON` (StrId 1160), plus the whole
power-limit/PAL/CPA excursion machinery (StrIds 1342–1352, 3025–3026), and the hold-up
capacitor subsystem with real self-tests: `VCAP: PowerUp/Short/Open test`, discharge-time
watermarks (StrIds 1189–1198), `SYS: Delayed SAM startup by %d ms (waiting for VCAPOK)`
(3516), and — critically — **`VCAP has failed, drive is in write protect mode`** (StrId 662).

So the correct statement is not "a link drop looks like a power loss to the controller". It is:

> **A marginal U.2 cable is a single physical fault that produces *both* symptoms
> independently.** The connector carries the PCIe lanes *and* the 12 V rails. High contact
> resistance shows up as link training failures (`UEFI0067`) on the signal pins and as
> I²R-proportional rail droop on the power pins. The droop is a *genuine* brownout, and the
> firmware's brownout comparator is right to assert PFAIL.

That reframes the two models:

- **Model C is upgraded, not downgraded.** A real 12 V droop asserts PFAIL for real. If the
  droop is transient or the rail sags further while the hold-up capacitors are trying to run
  the save, the PFAIL sequence starts and does not finish → marker 6 (`PFAIL Shutdown
  STARTED`) or marker 7 (`PFAIL Shutdown TIMEOUT`). Those are precisely the markers I proved
  in §2 converge on the UNEXSTRT/Post-Crash path. **This is a complete, code-traced causal
  chain from a bad cable to the latch, with no firmware bug required at any step.**
- **Model D stays refuted as stated** (link ≠ power in code) **but is confirmed as a
  correlated symptom**: the link failure and the power failure are siblings, not cause and
  effect. That is why latch #2 was accompanied by `UEFI0067` on the same port.

**Q4: is the deallocate contribution about power rather than TRIM semantics? Partly — and it
unifies the two latches.** StrId 631, `A de-allocate command is broken during PFail from LBA
%x to %x`, is **PROVEN** to live in the *data-path* processors — PROC1, 2, 3, 4, 5, 7, 10, 12
and 15 all carry it (e.g. PROC10 `0x7ffab53a`, PROC2 `0x7ffab532`, PROC3 `0x7ffab542`). It is
in the deallocate execution engine, and it exists specifically to record that an **in-flight
deallocate was cut short by a real PFail event**. WD would not have written that message
unless deallocate-interrupted-by-power-loss were a real, observed combination.

A whole-device deallocate on 7.68 TB is also the drive's highest-current workload class: it
invalidates the entire map and unleashes garbage collection, and NAND block erases are the
peak-current operation on the device. On a healthy feed that is fine — the drive has PAL/CPA
power limiting for exactly this. On a **connector with elevated contact resistance it is the
workload most likely to pull the rail below the PFAIL threshold.** That is consistent with the
trim watchdog strings (3189/3190 `Outstanding Trim, … startTicks %d now %d`) being present in
the same processors.

This gives one mechanism for both field events:

| | latch #1 | latch #2 |
|---|---|---|
| host action | `mkfs.xfs` → whole-device deallocate | `ForceOff` of the running host |
| drive load | peak (map invalidate + GC + NAND erase) | whatever was in flight |
| 12 V behaviour on a bad connector | droop under peak current | rail removed while in flight |
| PFAIL | asserted, save could not complete | asserted, save could not complete |
| marker left | 6 or 7 | 6 or 7 |
| boot | UNEXSTRT stub → `CLOG` → Post Crash | same |

And it explains the controlled replay that "proved" discard was the trigger: with
`discard_max_bytes=0` the peak-current event never happened, so the rail never drooped. That
experiment did not exonerate the drive — **it removed the load.**

## A6. Revised verdict — is this a firmware bug or a cable fault?

**Both, in different proportions than previously recorded — and the cable is the actionable
half.**

- **The firmware is behaving as designed.** A power-fail save that starts and does not finish
  is a genuine integrity event. Refusing to present a namespace, raising a Persistent Internal
  Error, and preserving a crash record for analysis is the *correct* conservative response for
  an enterprise drive. Nothing I traced is a coding error.
- **The one arguably defective behaviour is the latch's stickiness**: the crash-section
  predicate re-fires on every subsequent boot regardless of how clean it was, and the only
  documented exit is a vendor OAM erase (§6.4/§11). That is a serviceability design decision,
  not a bug.
- **The evidence now points at the interconnect as the proximate cause of at least latch #2
  and plausibly both.** Known-flaky U.2 cable, `UEFI0067` link training failure on the same
  port, and a firmware that monitors two 12 V rails and has a hardware brownout comparator.

**Practical consequence for the keep-or-bin decision:** the drive should not be binned on this
evidence. It should be **moved to a different bay with a different cable** and re-tested. If it
latches again on known-good cabling under a peak-current workload, that is a drive fault. If it
does not, the fault was the interconnect and the drive is fine.

Two cheap measurements would settle it and neither has been taken:

1. **`nvme smart-log` → `power_cycles` and the unsafe-shutdown count**, plus the VCAP state.
   `VCAP has failed, drive is in write protect mode` (StrId 662) is a *distinct* posture from
   Post Crash; if the capacitors have degraded, the drive cannot complete a PFAIL save no
   matter how clean the rail, and it will keep latching in any bay. That would justify binning.
2. **Which section is armed** — `0xC6 cdw12=0x0320` (CLOG) vs `0x0520` (PFCL). §11. If only
   **PFCL** is armed, that is direct confirmation of a *power-fail* origin rather than an
   assert/hang origin, and it also means the harmless `0x0603` clear may suffice.

**Corrections this addendum makes to my own earlier sections:** §4's model-C verdict
("partly confirmed, not a separate mechanism") understated it — C is not merely an input to B,
it is the *likely actual input* here, via a real rail droop. §4's model-A verdict stands but
gains a second, non-semantic route: a deallocate contributes through **current draw**, not only
through L2P/journal semantics. Nothing in §1–§3, §11 or §12 changes.

---

# ADDENDUM 2 — fleet-wide incidence: the cable hypothesis is refuted, and I was wrong to promote it

New fact from the owner: **this lockup has occurred on every host they run an SN200 in** —
multiple chassis, cables, bays. A marginal U.2 cable on one host cannot explain that. Addendum
1 §A5/§A6 promoted the interconnect to proximate cause and recommended re-testing in another
bay. **That recommendation was wrong as a primary action.** Retracted below.

The mechanism I traced (§2) is unchanged. What changes is the answer to *"what makes the
shutdown fail to finish?"* — and the WD documentation shipped inside the firmware zip answers
it directly.

## B0. I had not read the vendor documentation. It was in the zip all along.

`HGST-UltraStar-SN200-HHHL.zip` contains a `docs/` directory I did not open in my first pass:

```
docs/KNGND122_Release_Notes.pdf      docs/KNGND110_Release_Notes_v2.pdf
docs/KNGND100_SN2xx_Errata.pdf       docs/KNGNP100_SN2xx_Errata.pdf
docs/80-11-80171_Ultrastar_SN200_Product_Spec_Rev_4.pdf   (+ product manuals)
```

I extracted the text myself (PDF streams → zlib → text-show operators). Everything quoted
below is verbatim from those files.

## B1. WD documents this exact failure, repeatedly, as a firmware defect family

Searching the change lists for the words that matter:

| document | `pfail` | `deallocate` | `diagnostic mode` | `crashed` | `capacitor`/`VCAP`/`hold-up`/`power backup` |
|---|---|---|---|---|---|
| KNGND100 errata | 0 | 3 | 0 | 0 | **0** |
| KNGND110 release notes | 23 | 34 | 10 | 8 | **0** |
| KNGND122 release notes | 3 | 0 | 0 | 0 | **0** |
| KNGNP100 errata | 0 | 0 | 0 | 0 | **0** |

**Zero mentions of capacitors, VCAP, hold-up, PLP or power backup in any WD document for this
family.** WD has never documented a hold-up-capacitor failure mode here. (That is not proof
the caps are fine — see §B4 — but it removes the "known batch problem" version of the theory.)

What WD *does* document is a large family of firmware defects that all end the same way. From
**KNGND110** (i.e. all of these were **open in KNGND100**), verbatim, cited by title because
the PDF's table layout makes the `ID:`↔title pairing ambiguous under text extraction — the
other agent's document flags the same hazard and is right to:

> **"Namespace Disappears During AC Power Cycle Testing"** — *Failure Scenario:* Power Cycling
> + Random Read/Write/Deallocate IO Profile Testing results in **incomplete shutdown** after
> 2000+ iterations. *Root Cause:* When both a link down and a Pfail interrupt occur at exactly
> the same time, there is a corner case in which **the Pfail interrupt may get lost.**

That is the owner's symptom, by name, with "incomplete shutdown" as the mechanism — exactly
the marker-5/6/7 → UNEXSTRT → Post Crash chain I traced.

> **"Drives failed to restore L2P table after large deallocate and a pfail"** — *Failure
> Scenario:* Heavy deallocate IO workloads during pfail could cause L2P table to become
> corrupt. *Root Cause:* metadata corruption due to a **race condition between large
> deallocate commands and internal flushing of L2P updates to NAND**. *Change Description:*
> Fixed race condition **and added counter to VU log page for tracking any future
> occurrences.**

> **"Drive in crashed state following Power Cycle, Controller Reset, and Deallocate Test"** —
> *Root Cause:* With back-to-back PFails, PFails that occur in the middle of a 200 ms power-on
> window may cause small loss of usable media. **Over time, this leads to a crash.**

> **"GC stuck during shutdown"** — a page-relocation request received by GC Mgr after
> Shutdown creates a **deadlock**; the drive hangs and cannot complete the shutdown request.

> **"Drive faulted during firmware download"** — firmware download followed by a shutdown
> could cause memory corruption and lead to drive in **crash/diagnostic mode**.

> **"Queue Engine state change error handling"** — when a link down occurs between the Queue
> Manager and Queue Engines enable sequence, no check ensured enable completed, **resulting in
> a hang** → crashed/diagnostic mode.

> **"2x Drives Crashed during REFCLK Short Testing"** — if REFCLK goes away around the same
> time as a Link Down/Link Training occurs, both clocks become invalid.

plus a race where the **System Manager never sends the shutdown message**, and an admin
manager that gets **stuck saving CellCare data during shutdown**.

And from **KNGND122** — the *last* firmware that exists for this product, February 2021 — the
same class of bug was still being fixed:

> **Dual Port Shutdown / Severity High / Drive Recovery: Unable to recover** — *"When a
> shutdown is issued, internally the firmware will invoke a thread to monitor PFAIL (power
> fail) during shutdown. Due to a logic error in the firmware, if there is another shutdown
> triggered from the other port during this time, the PFAIL monitor thread is added again to
> the thread execution list… the pointers to the execution list becomes broken and **a hang
> occurs during the shutdown process**."*

> **Reset / Severity High / Drive Recovery: Unable to recover** — *"A race condition exists
> when a PCIE uncorrectable error occurs with a host link down that causes the Completion
> Queue messages to go into autodisable mode. The firmware timeouts waiting for the response
> from the hardware and **leads to a drive hang**."*

> **Link Error Handling** — *"A hang condition may occur during a race condition… when a host
> link down is combined with a PCIe error… Also, the timeout condition would no longer cause
> the drive to go into debug mode."*

> **Shutdown / Severity High / Drive Recovery: Unable to recover** — *"A Format with Secure
> Erase for user data option interrupted by an NVMe shutdown causes the drive to crash."*

Note how many entries carry **`Drive Recovery: Unable to recover`**. WD's own position is that
once this happens, the drive does not come back by any host action.

## B2. Re-ranked causes for a fleet-wide pattern

| rank | cause | verdict | basis |
|---|---|---|---|
| **1** | **Firmware defect family: the shutdown/PFAIL/reset path hangs or drops the PFAIL interrupt, leaving the save unfinished** | **CONFIRMED — this is the root cause** | WD's own change lists, ~10 distinct entries across KNGND110 and KNGND122, several titled with the exact observed symptom, most marked "Unable to recover". Matches my code trace exactly. |
| 2 | Usage pattern common to every host (whole-device discard on mkfs, heavy write, abrupt reboots) acting as the **trigger** for those defects | CONFIRMED as trigger, not cause | WD's failing test profiles are literally "Power Cycling + Random Read/Write/**Deallocate**" and "Power Cycle, Controller Reset, and **Deallocate**". |
| 3 | Aged hold-up capacitors | **UNPROVEN, and now unlikely as the primary** | Zero mention in any WD document; and the firmware would report it via a *different* posture (write-protect, §B4) rather than Post Crash. Still worth measuring — §B3. |
| 4 | Marginal U.2 cable / interconnect | **DEMOTED to aggravator on one host** | Cannot explain fleet-wide incidence. Retains explanatory value for latch #2's `UEFI0067` only, and note that several WD defects are specifically triggered *by* link-down events — so a flaky cable makes a firmware bug fire, it is not itself the fault. |

**Retraction.** Addendum 1 §A6 said "the drive should not be binned on this evidence; move it
to a different bay". The bay swap is still worth doing for the one host with `UEFI0067`, but it
is no longer the primary action and it will not fix the fleet.

## B3. How to read power-backup / PLP health non-destructively — the fleet-wide measurement

**There is no capacitance readout. There is a fault counter, and it is the right measurement.**

**PROVEN** from `libdmi_core.so.0.39`: the SMART attribute display tables at file offsets
`0xe4598…` and `0xe4728…` contain, among ~80 attributes:

| attribute | JSON key | library symbol |
|---|---|---|
| **`Power Backup Faults`** (`0xa6b8f`) | `power_backup_faults` | `HDME_SMART_ATTR_NAME_POWER_BACKUP_FAULTS` |
| **`Lifetime Number of Power Backup Faults`** (`0xa91b8`) | — | `HDME_SMART_ATTR_VALUE_LIFETIME_NUMBER_OF_POWER_BACKUP_FAULTS` |
| `Unexpected Power Loss Count` (`0xa6b5c`) | — | `HDME_SMART_ATTR_NAME_UNEXPECTED_POWER_LOSS_COUNT` |
| `Surprise Power Loss Events` (`0xa6dbd`) | — | `HDME_SMART_ATTR_VALUE_SURPRISE_POWER_LOSS_EVENTS` |
| `Exception and Assert Count` (`0xa6d88`) | — | — |

"Power backup" is this firmware's name for the hold-up subsystem — corroborated on the drive
side by StrId 2162 `Flush Admin_isPowerBackupFailed = %d` (PROC8 `0x300292e6`).

**How to read it, in priority order:**

1. **`dm-cli get-smart`** on each SN200. The RPM the owner already has provides the tool
   (`opt/Western_Digital/dm-cli/bin/dm-cli`; `get-smart`, `get-state`, `get-statistics`,
   `get-log-page` are all in its command table). This is the only path that decodes the
   attributes by name. **Read-only.**
2. Raw vendor log pages, if `dm-cli` is unavailable — `om_get_log_page_data` @ `0x67e70`
   accepts page ids **`0xC2`, `0xC3`, `0xCA`, `0xDE`**, and `_gf_capture_vu_log_page` @
   `0x3c2d0` dumps **`0xC1`** (it names the artefact `L_LOGXC1`), 64 KiB:
   ```sh
   for p in c1 c2 c3 ca de; do
     nvme get-log /dev/nvmeX --log-id=0x$p --log-len=4096 -b > sn200-lp-$p.bin
   done
   ```
   Get Log Page is admin opcode `0x02` — **read-only, cannot change drive state.**
3. `nvme smart-log` for the standard `unsafe_shutdowns` counter as a cross-check.

**What to look for.** On a healthy fleet, `Power Backup Faults` / `Lifetime Number of Power
Backup Faults` should be **0**. Non-zero on multiple drives would resurrect the VCAP theory and
would be decisive. Also compare `Unexpected Power Loss Count` / `unsafe_shutdowns` against
`Exception and Assert Count`: if asserts track the latches, the cause is the firmware hang
family (rank 1); if power-backup faults track them, it is the capacitors (rank 3).

**Do this on the HEALTHY drives too** — they are not gated, every command works, and the
comparison across the fleet is the entire point.

There is one more counter worth pulling: the KNGND110 fix for the deallocate/L2P race
explicitly *"added counter to VU log page for tracking any future occurrences"*. If the fleet
is on KNGND110 or KNGND122, that counter exists in one of the pages above and directly reports
whether the deallocate×pfail race has fired.

## B4. Is there a periodic VCAP self-test? **No — and that is important**

**PROVEN.** PROC0 has a VCAP test suite — `VCAP: Start Test` (1188), `PowerUp started`
(1189), `PowerUp test succeeded/failed` (1190/1191), `Short test succeeded/failed`
(1192/1193), `Open test started` (1194), and the one that actually measures capacitance:

```
1195  VCAP: Open test complete in %ums, Watermarks={%ums,%ums}
1196  VCAP: Open test failed: Time is too short  / Discharge completed in %ums …
1197  VCAP: Open test failed: Time is too long   / Discharge not completed in %ums …
```

The "Open test" times a discharge against two watermarks — that is a real RC/capacitance
measurement, and a degraded cap would fail it "too short".

**But it is gated to BIST mode.** The VCAP message handler at PROC0 `0x7ffacf58` is a
three-instruction early return that logs StrId 1199 **`VCAP: Not in BIST mode, message
ignored`** and returns 1:

```
7ffacf58: entry a1,0x20
7ffacf5b: { l32r a10,0x7ff837b0 ; movi a8,0x0 }   ; a10 = LOG 1199 descriptor
7ffacf63: s32i.n a8,a3,0x0
7ffacf65: call8 0x7ffb5398                        ; log "Not in BIST mode, message ignored"
7ffacf68: movi.n a2,1
7ffacf6a: retw.n
```

So **the drive never measures its own hold-up capacitance in the field.** The only runtime
signal is a *binary* VCAP status from hardware, consumed by `Admin_VCapStatusHandler`
(PROC8 `0x7ffb0740`, StrIds 2954/2955), which on failure logs StrId 2061 `Received notify that
the VCAP has failed`, raises an async event (StrId 3267 `VCAP failure async event detected but
masked, discarded`), and puts the drive into **write-protect mode** — StrId 661
`VCAP has failed, drive is in write protect mode` (PROC2/3/4/5/10 `0x7ffa4baa`, `0x7ffa5033`).

**This is the strongest single argument against the VCAP theory.** A drive whose hold-up
subsystem has failed *detectably* goes to **write-protect**, not Post Crash — a different,
clearly-labelled posture. The field drives show Post Crash. So either the caps are fine, or
they are degraded in the narrow band that still passes the hardware's crude threshold. The
`Power Backup Faults` counter in §B3 is what distinguishes those.

(Related and observable: StrId 3516 `SYS: Delayed SAM startup by %d ms (waiting for VCAPOK)` —
the drive waits for the caps to charge before starting the media manager. A growing delay
across the fleet would be a degradation signal, but it is only visible in the internal log, not
to the host.)

## B5. The PFAIL budget, and why it is workload-dependent

**PROVEN.** The PFAIL monitor's literal pool in PROC0 is self-documenting:

```
0x7ff830c0 = 0x7ff8cd80   ; the PFAIL monitor object (referenced nowhere else in any image)
0x7ff830e0 = 0x000061a8   ; 25000  -- the deadline
0x7ff830e8 = 0x7ffa838b   ; handler
0x7ff830ec = 0x80000006   ; marker 6  PFAIL Shutdown STARTED
0x7ff830f0 = 0x7ffa8380   ; handler
0x7ff830f4 = 0x80000007   ; marker 7  PFAIL Shutdown TIMEOUT
```

Markers 6 and 7 are literals **inside the PFAIL monitor's own pool**, next to its deadline.
That closes the loop: the PFAIL monitor writes marker 6 when the save starts and marker 7 when
the deadline expires, and both feed the `0x7ffaaf6b` convergence point I traced in §2. I could
not determine the time unit of 25000 (`SYS: PFAIL time = % 5u.%03u ms`, StrId 1256, has no
reference in any image — compiled out), so the absolute budget is **UNKNOWN**.

**The work is workload-dependent — PROVEN from the counters the firmware keeps:**
`PfailRspPth: flush WB` / `flush %d entries in WB` / `wait for %d writes to complete`
(StrIds 1500–1504, 3068), `PFail: Flushed %d WB frames` (1518), then BlockMgr saving valid
counts, erase counts and CellCare tables (2733–2736), then the System Area save. Plus explicit
statistics `ForcedWritesAfterPFail`, `WritesAfterPFailCM`, `WritesAfterPFailOthers`
(StrIds 2360, 2489–2492).

So the coordinator's point 3 holds: **the amount that must be flushed scales with dirty write
buffer and dirty L2P**, which is why a peak-load workload is the one that runs out of budget —
whether the budget is short because the caps are weak *or* because a firmware hang is eating
it. Both hypotheses predict "fails under load"; the counter in §B3 is what separates them.

## B6. Plain verdict

> **These drives are not failing because of your cables, and they are not (on the evidence
> available) failing because their capacitors have aged out. They are failing because the
> SN200's firmware has a documented family of race conditions in its shutdown, reset and
> power-fail paths that leave the power-loss save unfinished — after which the drive latches
> itself into Post Crash Startup permanently. Western Digital found, and progressively fixed,
> about ten of these between 2017 and 2021, several titled with your exact symptom, and marked
> most of them "Drive Recovery: Unable to recover."**
>
> **KNGND122 (February 2021) is the last firmware that will ever exist for this product. The
> SN200 was discontinued. If a drive on KNGND122 still latches, there is no fix coming.**

### What to do, in order

1. **Check the firmware revision on every SN200 you own** — `nvme id-ctrl /dev/nvmeX | grep fr`.
   This is the highest-value action and it is free. Drives of this vintage very commonly still
   ship **KNGND100 (Oct 2017)**, which has **every** defect in §B1 open, including "Namespace
   Disappears During AC Power Cycle Testing" and the deallocate/L2P race. **KNGND110 and
   KNGND122 are both in the zip you already have.** If any drive is below KNGND122, update it —
   after a clean shutdown, not while dirty, and note that "firmware download followed by a
   shutdown" was itself a KNGND110 bug, so update from a quiesced drive.
2. **Pull `dm-cli get-smart` from every SN200** and compare `Power Backup Faults`,
   `Lifetime Number of Power Backup Faults`, `Unexpected Power Loss Count` and
   `Exception and Assert Count` across the fleet (§B3). This is the measurement that decides
   firmware-hang vs. capacitor-aging, and nobody has taken it.
3. **Suppress the trigger permanently, fleet-wide** — no whole-device discard, ever:
   `mkfs.xfs -K`, `mkfs.ext4 -E nodiscard`, mount without `discard`, LVM `issue_discards = 0`,
   no whole-device `fstrim`, and Talos/ceph configured likewise. WD's own failing test profiles
   are deallocate profiles.
4. **Always stop these drives cleanly.** Unbind the nvme driver
   (`echo 0000:BB:DD.F > /sys/bus/pci/drivers/nvme/unbind`) before a reboot or power-off — it
   is the only host action that issues `CC.SHN` and waits for `CSTS.SHST=10b`. Every other
   stop, including a plain reboot, risks the marker being left at "STARTED".

### Keep or bin?

**Keep, conditionally, and plan to replace.** Concretely:

- If the fleet is on **KNGND100 or KNGND110**: update to KNGND122 and re-evaluate. There is a
  real, documented, applicable fix. Do not bin anything before doing this.
- If the fleet is **already on KNGND122** and drives still latch: you are running the final
  firmware for a discontinued product with a known-unfixed defect family whose vendor-stated
  recovery is "unable to recover". These are not trustworthy for anything you cannot afford to
  rebuild — no ceph OSDs without replicas you would actually rely on, no single-copy data.
  That is a retirement decision driven by unsupportability, not by any individual drive being
  bad.
- Either way, **`Power Backup Faults` non-zero on multiple drives flips this to "bin"** — that
  would mean the hold-up hardware is degrading and no firmware can help.

### Corrections to earlier sections of this document

- **Addendum 1 §A5/§A6 is retracted** in its ranking: the interconnect is an aggravator on one
  host, not the fleet's root cause. §A5's *code* findings (PFAIL is a PROC0-local brownout ISR
  unreachable from PCIe; U.2 carries 12 V; the deallocate×PFail string is in the data-path
  processors) all still stand, and note that WD's errata make link-down events a *trigger for
  firmware hangs*, which is a better fit than rail droop.
- **§4 model A** is upgraded: WD's "Drives failed to restore L2P table after large deallocate
  and a pfail" is a real, named defect with a real fix, so deallocate is a genuine sufficient
  cause on pre-KNGND110 firmware, not merely a provocation.
- **On errata IDs:** my text extraction cannot reliably pair `ID:` values with titles — the
  PDF table columns interleave, and both possible readings are self-consistent. The commonly
  quoted `OM-6588 = deallocate/L2P` mapping may be off by one entry. **Cite these by title.**
  The titles and root causes are unambiguous; the IDs are not.
