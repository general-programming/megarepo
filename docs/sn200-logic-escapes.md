# SN200 — logic escapes from the Post Crash Startup latch

Scope: is there a sequence of **legal, unmodified-firmware, documented-command**
operations that walks an HGST/WDC Ultrastar SN200 out of the `POST CRASH Startup`
latch without the destructive drive re-init?

Companion documents: `sn200-firmware-re.md`, `sn200-independent-re.md`,
`sn200-shutdown-path.md`, `sn200-nondestructive-recovery.md`,
`sn200-firmware-availability.md`, `sn200-firmware-flashing.md`. §3 here is a
summary of `sn200-readonly-startup.md`, which carries the long-form marker-8
disproof.

Two sibling investigations cover (a) patched firmware images and (b) memory-safety
bugs in reachable command handlers. **This document deliberately contains neither.**
Everything below is a *legal* state transition.

Static analysis only. No hardware was touched. All disassembly is from
`tools/sn200-fw/xdis.py` (FLIX slots A/B/C), never Ghidra's decompiler.

---

## Verdict

**One escape route survives every check, and every link in it is now proven
except the physical wiring.**

> Serial console (`DiagMgr>`, reachable while latched) → `SYS SBL` → the SBL/MBL
> diagnostic console → write the boot-mode word or EEPROM System-Area section 6 →
> a boot that never consults the crash sections.

The pieces, each PROVEN separately and now joined:

1. **The `DiagMgr>` console comes up on a latched drive.** The console task is
   enqueued unconditionally, before the startup type is even computed, and its
   dispatcher has no gate. The `0x7C5` rejection applies only to NVMe admin
   commands. (§7.3)
2. **`SYS SBL` is one of its eight commands**, and it is the *sole writer of the
   firmware boot-mode word `0x7ff9ff64` in the entire `PROC0` image*. It hands off
   to the boot loader. (§7.2)
3. **The boot loader has its own documented console** — `SBLPATCH.bin` inside
   `KNGND110.bin` is the MBL/SBL image with plaintext help text for `MemWrite
   <address> <data-word>`, `MemRead`, `CARRead/CARWrite`, `ReadSpi`,
   `EraseSystem`, `Reset`. (§2.5)
4. **Firmware boot mode `4` (`LOAD_N_GO`) skips the crash-section latch
   entirely** — both `ball` tests *and* the empty-System-Area door — and clears a
   stale 5/6/7 marker instead of converting it to `UNEXSTRT`. It is a deliberate
   manufacturing bypass, wired into the same predicate twice. (§2.2)
5. **Alternatively, the startup marker is word 0 of a 244-byte record in EEPROM
   System-Area section 6**, with a redundant second copy. Writing `0x80000008`
   there yields startup type 3, `READ ONLY STARTUP`, which the admin gate does not
   reject and which restores the L2P. (§3.1, §3.3)

The unknown is the physical layer: **`PROC0` contains no UART MMIO and no pinmux**
— the driver is installed at runtime from pointers outside the image — so the pins
must come from the board, not the firmware. (§7.4)

**Everything else is closed.** The headline negatives, so nobody re-derives them:

- **NVMe-MI over SMBus is a dead end.** It is a different transport onto the *same*
  queue; the gate function has exactly one call site and MI is upstream of it. Its
  tunnel forwards only `{0x02,0x06,0x09,0x0A,0x10,0x11}` — every one of which is
  already permitted while latched — and MI's own command set cannot name an SPI
  section at all. Its reset is a plain NSSR, i.e. another `UNEXSTRT`. (§6)
- **No firmware path writes marker 8.** The `PROC12` site that looked like the
  writer emits a NAND *Event Log record type*, a different object that merely
  shares the `0x8000000N` numbering; `PROC0` never reads that log. `PROC0`'s
  generic marker setter has one caller supplying one constant. (§3.2)
- **Mode 6 has exactly one source: marker 9.** `PROC8`'s copy is a mirror with a
  single writer, fed from `PROC0`'s dispatcher. No desynchronisation trick. (§1.1)
- **The `0x7ff8b4f8` / `0x7ff8d200` divergence is real but useless** — two copies
  with identical bit semantics, and the latch reads the one the scanner fills from
  media moments earlier. (§8)
- **Downgrading to `KNGND100` does not unlatch a latched drive**, and it re-opens
  three High-severity defects `KNGND110` fixed — one of which can trap the drive
  into diagnostic mode *during the flash itself*. (§4.2, §4.3.1)
- **The `DiagMgr` `Load` command is a stub** (`entry; movi a2,0; retw`). The
  loadable-command-group feature was compiled out. (§7.1)

And the one genuine blind spot: **opcode `0xFF` — the section-clear command —
passes the post-crash gate, and its handler at `0x7ffbc110` is in an unmapped
region `PROC8`'s flat image does not cover.** (§5.2)

## 1. The startup-mode map — complete, PROVEN

This is the piece that was missing from the earlier passes, and it reframes
everything. In `PROC0` the marker dispatcher does not merely log a string: each
handler loads a **startup-mode value into `a5`**, and the shared tail stores it:

```
7ffaac8a: l32r a12,0x7ff82b9c        ; -> 0x7ff8c788   (system state block)
7ffaac8d: l32r a10,0x7ff83428        ; LOG id=1275 "%s\n"
7ffaac90: s32i.n a5,a12,0x30         ; *(0x7ff8c788+0x30) = startup mode
```

`*(0x7ff8c7b8)` is the PROC0 master copy of the startup mode; `0x7ff87c64` in
`PROC8` is the copy the admin gate tests. Enumerating every handler:

| Marker (`*(0x8000000N)`) | Handler | StrId logged | **mode written to `a5`** |
|---|---|---|---|
| 1 `CLEAN shutdown` | `7ffaaf85` | 1264 `SYS: Normal startup` | **1** |
| 2 `PFAIL shutdown` | `7ffaaf8d` | 1265 `SYS: PFAIL startup` | **2** |
| 3 `Drive REINIT requested` | `7ffaaf63` → `7ffaaf7d` | 1266 `SYS: Drive re-init` | **0** |
| 4 `FACTORY REINIT` | `7ffaafc0` → `7ffaaf7d` | 1267 | **0** |
| 0 `No previous marker` | `7ffaaffd` → `7ffaaf7d` | 1268 `First time startup` | **0** |
| 5/6/7 **with** load-n-go | `7ffaaf6b` → `7ffaaf7d` | 3043 `Load-n-go boot override…` | **0** |
| 5/6/7 **without** load-n-go | `7ffaacea` (UNEXSTRT) → `7ffaac82` | 1273 | **6** |
| **8 `READONLY Startup requested`** | `7ffaaff5` | 1272 `SYS: Read-only startup` | **3** |
| 9 `POST CRASH Startup` | `7ffaac82` | 1273 `SYS: Post Crash startup` | **6** |

Raw evidence (`movi a5,N` is in FLIX slot C and is invisible in Ghidra):

```
7ffaaf7d: { movi a5,0    ; j 0x7ffaac8a }      ; markers 3, 4, 0, and load-n-go 5/6/7
7ffaaf85: { movi a11,1264; j 0x7ffaac8a ; movi a5,1 }
7ffaaf8d: { movi a11,1265; j 0x7ffaac8a ; movi a5,2 }
7ffaaff5: { movi a11,1272; j 0x7ffaac8a ; movi a5,3 }   <-- marker 8, READONLY
7ffaac82: { movi a11,1273; movi a5,6 }                   <-- marker 9, POST CRASH
```

The mode numbers are a named enum, StringTable 303–309 (**PROVEN**):

| value | name | reached from marker |
|---|---|---|
| 0 | `FIRST STARTUP` | 3, 4, 0, and load-n-go 5/6/7 |
| 1 | `NORMAL STARTUP` | 1 |
| 2 | `RECOVERY STARTUP` | 2 |
| **3** | **`READ ONLY STARTUP`** | **8** |
| 4 | `FIRMWARE UPDATE STARTUP` | *no marker produces it* |
| 5 | `FAST STARTUP` | *no marker produces it* |
| **6** | **`INVALID`** | **9, and 5/6/7 without load-n-go** |

Worth stating plainly: the gated state is not called "post-crash" internally — it
is called **`INVALID`**. The admin gate is a refusal to operate in an
unrecognised startup type, which is why it is a bare `!= 6` test and why every
other type, including `READ ONLY`, sails through it.

**Consequences, all PROVEN:**

1. **Mode 6 is produced by marker 9 and by nothing else.** The gate at `PROC8`
   `0x7ffa6b30` (`bnei a8,6,…`) therefore fires if and only if the boot latched.
2. **Marker 8 gives mode 3, and mode 3 is not gated.** A `READONLY` boot comes up
   with the admin interface fully open and the L2P restored. See §3.
3. **A load-n-go boot of a drive whose shutdown never finished writes mode 0** —
   i.e. it is treated as an ordinary first-time boot, with the marker cleared
   (`s32i.n a6,a7,0x0` at `0x7ffaaf78`, `a6 == 0`).

### 1.1 How the mode reaches the gate — one hop, one writer

`PROC8`'s `0x7ff87c64` is a **mirror**, not an independent state variable. Its
literal-pool entries (`0x7ffa09b0`, `0x30033350`, `0x3002ead0`, `0x3003fbd4`) are
referenced from 21 sites; all but one are loads. The single store:

```
7ffb014a: l32r a14,0x7ffa09b0        ; -> 0x7ff87c64
7ffb014d: l32i.n a15,a2,0x18
7ffb014f: { s8i a15,a3,0x8c }
7ffb0157: { s32i a13,a14,0x0 }       ; *(0x7ff87c64) = value carried in the message
7ffb015f: call8 0x7ffba990
```

— a message handler taking the value from its argument block. **INFERRED (high
confidence):** it carries the mode `PROC0` computed at `0x7ffaac90`.

So there is exactly one decision point in the whole controller, and §1's table is
it. There is no second, independent route into mode 6, and correspondingly no
"desynchronise the mirror" trick: `PROC8` believes whatever `PROC0`'s dispatcher
concluded.

`KNGND100` has a byte-for-byte equivalent map (`0x7ffaa525` mode 0, `0x7ffaa52d`
mode 1, `0x7ffaa535` mode 2, `0x7ffaa59d` mode 3, `0x7ffaa230` mode 6). Verified.

---

## 2. Escape B — the `LOAD_N_GO` boot mode. **PROVEN bypass. The way in is the UART.**

### 2.1 What the word at `0x7ff9ff64` is

The struct at `0x7ff9ff60` is the **secondary-boot-loader handoff block**: slot
number at `+0x10`, default slot at `+0x11`, and loader callbacks at `+0x24`,
`+0x38`, `+0x3c`. Offset `+0x4` is the **Firmware Boot Mode**, and the firmware
prints it by name:

```
7ffb484d: l32r  a2,0x7ff826b8            ; -> 0x7ff9ff60
7ffb4850: l32i.n a11,a2,0x4
7ffb4852: { beqi a11,1,0x7ffb49a4 }      ; "Firmware Boot Mode : WARM BOOT, DDR (Slot %d)"
7ffb485a: beqz a11,0x7ffb49b0            ; "Firmware Boot Mode : COLD BOOT, EEPROM (Slot %d)"
7ffb485d: { beqi a11,4,0x7ffb499b }      ; "Firmware Boot Mode : LOAD_N_GO"
7ffb4865: l32r  a10,0x7ff83f18           ; "Firmware Boot Mode : Unknown state (%d)"
```

| value | name (StrId) |
|---|---|
| 0 | `COLD BOOT, EEPROM (Slot %d)` — 83 |
| 1 | `WARM BOOT, DDR (Slot %d)` — 82 |
| **4** | **`LOAD_N_GO`** — 86 |
| other | `Unknown state (%d)` — 87 |

### 2.2 The bypass — the latch is *skipped*, not merely overridden

```
7ffaae28: l32r   a12,0x7ff826b8          ; -> 0x7ff9ff60
7ffaae2b: l32i.n a12,a12,0x4             ; a12 = firmware boot mode
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }    ; <== LOAD_N_GO -> jump clear
7ffaae35: { ball a9,mask 0x1,0x7ffaaf02 }               ; CRASH section  -> force marker 9
7ffaae3d: { ball a9,mask 0x4,0x7ffaaf02 }               ; PFCRASH section-> force marker 9
7ffaae45: l32i.n a11,a7,0x0
7ffaae47: bne a11,a6,0x7ffaae69
7ffaae4a: <log 3519 "SYS: Unexpected empty System Area."> ; -> j 0x7ffaaf08 (force marker 9)
7ffaae53: <CellCare check> ; -> 0x7ffaae69 dispatcher with the STORED marker
```

`a5 = 0x7ff8b4f8` (set at `0x7ffaac38`), the boot-time system-area scan-result
byte; bit 0 = crash section present, bit 2 = pfail-crash section present.

**If firmware boot mode == 4, control jumps over all three forcing routes** — both
`ball` crash tests *and* the `Unexpected empty System Area` route. The dispatcher
then runs on the stored marker, whatever it is.

The same predicate appears a second time, as the failed-shutdown override:

```
7ffaaf6b: l32r   a15,0x7ff826b8
7ffaaf6e: l32i.n a15,a15,0x4
7ffaaf70: { bnei a15,4,0x7ffaacea }      ; not load-n-go -> UNEXSTRT / post-crash path
7ffaaf78: s32i.n a6,a7,0x0               ; load-n-go: CLEAR the marker
7ffaaf7a: l32r   a11,0x7ff83490          ; = 3043 "SYS: Load-n-go boot override of failed shutdown."
7ffaaf7d: { movi a5,0 ; j 0x7ffaac8a }   ; startup mode 0
```

So a `LOAD_N_GO` boot:

- never consults the crash sections at all;
- never takes the empty-System-Area door;
- **clears** a stale 5/6/7 marker instead of converting it to `UNEXSTRT`;
- comes up in mode 0, with the admin gate wide open and the L2P untouched.

That is a complete, non-destructive escape. It is the single strongest finding
here. **PROVEN.**

Corroboration that mode 4 is exactly "the host handed me this image": after a
`LOAD_N_GO` boot the background thread persists the running image into the
writable slots and logs on failure:

```
7ffa376f..7ffa37a3: five calls to 0x7ffa32e0 with slot = 1,2,3,4,5
7ffa37a9: <log 1129 "SYS: LOAD-N-GO failed to save firmware">
7ffa37f6: <log 1128 "SYS: LOAD-N-GO Firmware image is corrupted">
```

and the boot-mode-4 branch in the same thread takes a distinct init path
(`0x7ffa3583`: `bnei a9,4` → resume vector `0x7ffa3419`, phase 9).

### 2.3 Why it is hard to trigger in band

**The firmware never writes 4 to `0x7ff9ff64`.** Every reference to the handoff
block was enumerated (24 `l32r` sites via the two literal-pool entries `0x7ff825d0`
and `0x7ff826b8`); exactly one is a store to `+0x4`:

```
7ffa3adb: l32r   a2,0x7ff826b8
...
7ffa3aed: l32i.n a8,a2,0x24            ; loader callback
7ffa3aef: movi.n a9,5
7ffa3af1: s32i.n a9,a2,0x4             ; boot mode := 5  ("Unknown state (5)")
7ffa3af3: callx8 a8                    ; re-enter the loader
```

Mode 5 is a *request code the firmware passes back to the loader*, not a boot
mode. **INFERRED:** the loader consumes it and re-launches, choosing the next boot
mode itself. Therefore the value 4 originates in the SBL, before `PROC0` starts,
and is not settable by anything the running firmware exposes.

**And the obvious NVMe route is closed.** The Firmware Commit handler validates
the commit action with a *two-bit* extract and rejects anything ≥ 3:

```
30025e48: extui a8,a10,3,2             ; a10 = CDW10; CA[1:0]
30025e4b: { blti a8,3,0x30025c40 }      ; 0,1,2 -> valid
30025e53: l32r a10,0x30025518           ; "Firmware Activate Invalid Activation Action"
```

Commit action `011b` — *activate immediately, no reset*, the natural "load and go"
of the NVMe spec — is **rejected**. PROVEN. The controller supports only
CA = 0 (stage), 1 (stage + activate on next reset), 2 (activate slot on next
reset), all of which produce a cold or warm boot, i.e. boot mode 0 or 1, both of
which run the latch.

### 2.4 The SBL is on disk, and it confirms `LOAD_N_GO` is a real loader path

`KNGND110.bin` ships a 21st bundle member, `SBLPATCH.bin` (269 470 B), that
`KNGND100` and `KNGND122` do not. It is the **MBL + SBL boot-loader image**, and
unlike the `PROC*` images its log strings are stored in the clear. Extracted with
`strings`, **PROVEN**:

```
(info): SBL: New firmware is successfully loaded via LOAD-N-GO. Restarting ...
(info): SBL: LOAD-N-GO failed ...
(info): SBL: New firmware is successfully loaded from EEPROM. Restarting ...
(info): SBL: Starting (cold boot) ...
(info): SBL: Starting (warm boot) ...
(info): SBL: Returning from MBL ...
(info): SBL: Returning from FW ...
(info): SBL: Rebooting to another FW ...
(info): SBL: Starting (unknown state=%d) ...
```

Two things this settles:

- **`LOAD_N_GO` is an SBL firmware-load source**, parallel to "from EEPROM". The
  SBL loads an image that did not come out of a slot, and hands the running
  firmware boot mode 4. This matches the `PROC0` side exactly (§2.1–2.2), and
  matches the post-boot "save myself into slots 1–5" behaviour.
- **The `SBL: Rebooting to another FW` / `Starting (unknown state=%d)` pair is the
  other half of the mode-5 handshake** at `PROC0` `0x7ffa3aef`. INFERRED, but the
  fit is exact.

### 2.5 The SBL/MBL diagnostic console — an arbitrary-memory-write escape

The same image contains a **full command console with help text**, in three
groups (`SBL commands`, `DDR commands`, `SHARED`). Verbatim, **PROVEN**:

```
SBL commands
  MBL             - Go into MBL diagnostic mode
  EraseSystem     - Erase SPI EEPROM
  ReadSpi         - Read SPI EEPROM from address
  Reset           - Hardware reset
  SBL             - Return to SBL
  EepRdDdrTrainData / EepWrDdrTrainData
  DdrConfigModify <data-type> <offset> <byte value>
  DdrConfigDisplay <data-type>
  setPll <PLL-type> <Speed>
SHARED / Shared commands
  MemRead  <address> <word-count>  - Read from local memory space
  MemWrite <address> <data-word>   - Write to local memory space
  CARRead  <NodeID> <offset>       - Read from CAR
  CARWrite <NodeID> <offset> <data-word> - Write to CAR
DDR commands
  ddrMemRead / ddrMemWrite / ddrMemDump / ddrMemFill / ddrMemClear / ddrMemWrRdTest
  PrintDimm, PrintPhy, PrintPhyReg, PrintCtl, PrintDcsu, PrintSpdRaw, …
```

`MemWrite <address> <data-word>` writes a 32-bit word anywhere in "the diagnostic
processor memory address space", which is the same `0x7ffx_xxxx` space the boot
struct lives in. **INFERRED, high confidence:** from this console, one
`MemWrite 0x7ff9ff64 4` before the firmware dispatcher runs would set boot mode to
`LOAD_N_GO` and disarm the latch by the proven path in §2.2 — no image
modification, no memory-safety bug, using a command the vendor documented in its
own help text.

Caveats, all real:

- This is the `KNGND110` SBL. The SBL actually programmed on the subject drives is
  unknown and may differ; `SBLPATCH.bin` is a *patch* image shipped only with 110.
- The console is on the loader's UART, not the firmware's `DiagMgr>` UART. Pinout,
  baud and the entry sequence are unestablished.
- Winning the race — the SBL console must accept the write *before* it hands off to
  the firmware — is unproven. `Reset` and `SBL - Return to SBL` suggest the console
  is interactive and holds control, which is the needed property.
- `SBLPATCH.bin` is **not** in the `.SEG` container format the rest of the bundle
  uses. It is a `PMCSEEPM001` EEPROM-programming image built from 16-byte
  address/value records (first record: value `0x0203c041` → address `0x82a61000`).
  `tools/sn200-fw/segparse.py` and `unpack.py` return zero segments on it. A new
  parser is needed before the console code itself can be disassembled.

**The entry sequence is now known** — see §7.2: the firmware-side `DiagMgr>`
console's `SYS SBL` command writes boot-mode `5` and hands off to the loader, and
that console is reachable on a latched drive (§7.3). So the route into the SBL
console does not require an SBL-level host download or a manufacturing strap; it
requires the UART.

Two in-band candidates remain **SPECULATIVE** and would remove the need for
physical access entirely: an SBL-level host download over PCIe before the firmware
starts, and a manufacturing strap sampled by the boot ROM.

---

## 3. Escape A — marker 8 `READONLY Startup requested`. **Payoff proven. No firmware path writes it. But the marker's real home is now known, and that is the opening.**

This section was rewritten twice. The first pass said "dead code" on a constant
scan; the second said "alive, `PROC12` writes it". Both were wrong. Here is the
settled position.

### 3.1 The payoff, if it could be induced — PROVEN end to end

- The marker-8 and marker-1 handlers are *the same instruction bundle* with three
  immediates changed:
  ```
  7ffaaf85: be a4 f0 12 d0 ff 01 c5  { movi a11,1264 ; j 0x7ffaac8a ; movi a5,1 }  ; CLEAN
  7ffaaff5: be a4 f8 12 c9 ff 03 c5  { movi a11,1272 ; j 0x7ffaac8a ; movi a5,3 }  ; READONLY
  ```
- Type **3** = `READ ONLY STARTUP`. The admin gate is a bare `bnei a8,6` — **type 3
  is not gated at all.**
- Type 3 skips the first-time/re-init body at `0x7ffaabd8` (only type **0** runs it).
- It is not a stub. `PROC6` `0x7ffba940` (SAM) treats type 3 as *normal plus one
  flag*: it sets bit `0x80` and **falls through into the type-1 body**. `PROC6`
  `0x7ffa66dc` (`InitBlocksetNormal`) logs `BlockMgr: Read Only Startup` and jumps
  to the identical blockset init as Normal/PFail/Fast. `PROC8` `0x7ffb2518` skips
  the System-Area step only for type **6**, so on type 3 **the L2P is restored**.

A marker-8 boot is a normal boot with a write-inhibit flag. Exactly the outcome
wanted.

### 3.2 Nothing in the firmware writes it — PROVEN, and the near-miss explained

Exhaustive 4-aligned scan for the word `0x80000008` across all 18 flat images
returns two sites, and neither is a boot-marker write:

| image | address | what it is |
|---|---|---|
| `PROC0` | `0x7ff83478` | the dispatcher's own compare at `0x7ffaaed3` — a **read** |
| `PROC12` | `0x7ffa0d94` | written at `0x7ffa7d70` — but into a **different object** |

The `PROC12` site looked like the writer and is not. **PROVEN:**

- `PROC12` `0x7ffa7a68` is the **Journal Manager**, and `[ctx+0x54]` is an
  **Event Log record type**, committed as an 8-byte `{tag, payload}` pair into a
  NAND Event Log unit (errors `Journal Manager: LBN %d execeeded Event Log unit
  end` at `0x7ffa7c5a`, `Invalid Log event 0x%08x - 0x%08x found at %d in record
  %d` at `0x7ffa7818` — note *two* words per record).
- Its replay parser reads those tags back from NAND using the *same literal pool
  entries* used to write them (`0x7ffa7714` compares against `0x7ffa0d0c` =
  `0x80000004`, the identical literal written at `0x7ffa7ce8`).
- Tag `0x8000000E` (`0x7ffa0d74`) is written and never compared against the boot
  enum — the tag space is a superset because it is a *different* space.
- `PROC0` contains **no reader of the NAND Event Log at all.**
- And the semantics do not line up: event 5 emits tags `0x80000005` then
  `0x80000006`; event 6 emits `0x80000007` then `0x80000008`. As boot markers
  that is nonsense. It is a started/finished record pair in a shared
  "high-bit type code" convention.

The journal request block is also built *inside* `PROC12` (`[blk+0x08] =
0x7ffa7a68` stored at `0x7ffa2e49`, the only reference to that literal anywhere;
event field written at `0x7ffa2db7`), with event codes hardcoded per dispatcher
case at `0x7ffa28c0`. The sender does not choose the code. So even a host that
could drive journal event 6 would write a NAND log tag, not a boot marker.

`PROC0`'s generic marker setter `0x7ffa84c8` is likewise a dead end: its address
appears once in all 17 images (`0x7ff82b54`), with **one** `l32r` reference
(`0x7ffabccc`), supplying the **constant** `0x80000003`.

### 3.3 What we learned instead — where the marker actually lives

This is the durable result, and it reframes the whole problem. **PROVEN:**

The boot marker is **word 0 of a 244-byte (`0xF4`) "Drive Data" record in EEPROM
System-Area section 6**, held in RAM in **two redundant copies**: index 0 at
`0x7ff8c7ec`, index 1 at `0x7ff8c8e0`. The boot dispatcher loads them itself,
before it reads the marker, through the section accessor `0x7ffb4fec`:

```
7ffaaf3e: { s32i a7,a10,0x24 ; movi a13,244 ; movi a11,2 }   ; a7 = 0x7ff8c7ec
7ffaaf48: { s32i a13,a10,0x28 ; movi a12,6 ; movi a14,0 }    ; section 6, copy 0
7ffaaf58: call8 0x7ffb4fec
7ffaaf95: { l32r a8,0x7ff83138 ; movi a9,244 }               ; a8 = 0x7ff8c8e0
7ffaafad: { s32i a8,a10,0x24 ; movi a13,1 ; movi a11,2 }     ; section 6, copy 1
```

and it will heal the primary from the secondary:

```
7ffaae1e: l32i a11,a7,0xf4     ; copy 1's marker
7ffaae21: s32i.n a11,a7,0x0    ; -> primary
```

Every firmware writer of the marker goes through the same section-6 accessor:

| site | value written | copy | context |
|---|---|---|---|
| `0x7ffa8d94` | `0x80000000` (clear) | 0 | — |
| `0x7ffa83e7` | `0x80000006` | 1 | `SYS: PFAIL is detected` |
| `0x7ffa88dd` | `0x80000007` | 1 | `SYS: PFAIL timeout is expired` |
| `0x7ffa84c8` (coroutine) | `[ctx+0x18]` | 0 | one caller, constant `0x80000003` |
| `0x7ffaaf3e/95/ca` | 244-byte load/store | 0 / 1 | the boot dispatcher |

**So: `0x80000008` is never written by any firmware path, but the marker is a
32-bit word at a known offset in a named, addressable EEPROM section.** Anything
that can write EEPROM System-Area section 6 — the SBL console's SPI commands
(§2.5), a chip-off/SPI-clip read-modify-write, or an as-yet-unfound host path
into the section-6 accessor `0x7ffb4fec` — induces a `READONLY` boot directly.

That is the concrete target this section produces, and it is a *legal* state
transition: the marker value is one the firmware's own dispatcher handles, with a
correct handler, on a supported path.

### 3.4 One thread still worth pulling — the firmware-download flags

The one caller of the marker setter is gated on **bit 0 of a host-supplied
firmware-download flags word**:

```
7ffabcb7: l32r a10,0x7ff8365c   ; "SYS: Firmware download flags %08X\n" (id 1366)
7ffabcba: l32i a11,a2,0x90      ; the flags
7ffabcc6: bbci a9,0,0x7ffabd22  ; bit 0 -> schedule drive re-init (marker 3)
7ffabd22: extui a15,a9,1,1      ; bit 1 -> elsewhere ([ctx+0x3c])
```

**A host-influenced word already steers a marker write.** The coroutine is
`PROC0` `0x7ffabbf0` (firmware image acceptance, ids 1364/1365), posted from
`0x7ffac4ba`; the flags are `[parent+0xA8]`, and the writer of that field was
**not** located — an honest gap. **INFERRED, unproven:** they originate in
`PROC8` `Admin_FwDownloadSendSysMsg` (`0x30025f3f`, ids 2164/2165), which builds
its message from `[a2+0x17c/0x180/0x184]` and has a
`DriveConfig VUC patch is found. Mlp Address=%08X, Mlp Size=%08X` branch (id 3346)
— a vendor-unique-command-influenced path. If any flag bit selects a
caller-supplied marker rather than the constant, escape A is available in band.
This is the last unexplored in-band lead in the main firmware.

### 3.5 A methodological correction worth keeping

`sn200-firmware-re.md` §6a instructs the reader to *"ignore"* `PROC12`
`0x7ffa7d70..0x7ffa801c` as "the marker→string lookup, not a decision point".
That is wrong on both counts — it is neither a string lookup nor one decision
point — and it should be deleted. §13.6's later claim that `PROC12` "writes marker
8" is the opposite error: it writes an Event Log tag that merely shares the
numbering.

The general lesson, which cost two wrong conclusions here: **a constant scan
enumerates only producers that use a constant, and a name match on a constant
proves nothing about which object it is written into.** Both questions need the
store's destination traced, not just its value.

---

## 4. Escape C — activate an older firmware revision. **Partial. Cheap. Worth doing.**

This is the item that is uniquely cheap to test in reality, so it is worth being
precise about what it does and does not buy.

### 4.1 The revision-history evidence

All three generic images are in hand
(`~/Downloads/HGST-UltraStar-SN200-HHHL.zip`, `firmwares/`):

| image | sha256 (head) | date |
|---|---|---|
| `KNGND100.bin` | `134d67c9…` | 2017-10-11 |
| `KNGND110.bin` | `7210283c…` (is really `KNGND110+sblpatch+k`) | 2018-06-26 |
| `KNGND122.bin` | `b1129834…` | 2020-09-17 |

String-table diff of the crash/startup family:

| string | `KNGND100` | `KNGND110` | `KNGND122` |
|---|---|---|---|
| `SYS: Detected a CRASH or PFCRASH section.` | ✅ 3042 | ✅ 3042 | ✅ 3042 |
| `SYS: Load-n-go boot override of failed shutdown.` | ✅ 3043 | ✅ 3043 | ✅ 3043 |
| `READONLY Startup requested` / `POST CRASH Startup` | ✅ | ✅ | ✅ |
| **`SYS: Unexpected empty System Area.`** | ❌ **absent** | ✅ 3510 | ✅ 3519 |
| **`SYS: UNEXSTRT detected, writing UNEXSTRT stub header to crash area`** | ❌ **absent** | ✅ 3511 | ✅ 3520 |

**PROVEN: the `UNEXSTRT` bookkeeping was added in `KNGND110`.** `KNGND100`
predates it entirely.

### 4.2 …but the latch itself is unchanged

`KNGND100` `PROC0` `0x7ffaa1d8`, the same function, same shape:

```
7ffaa3d6: l32r   a12,0x7ff821d0         ; -> 0x7ff9ff60
7ffaa3d9: l32i.n a12,a12,0x4
7ffaa3db: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaa3fb }    ; LOAD_N_GO bypass — present in 2017
7ffaa3e3: { ball a9,mask 0x1,0x7ffaa4aa }
7ffaa3eb: { ball a9,mask 0x4,0x7ffaa4aa }
7ffaa3f3: l32i.n a11,a7,0x0
7ffaa3f5: bne a11,a6,0x7ffaa411
7ffaa3f8: j 0x7ffaa4b0                  ; empty SA -> force marker 9 (silently; no log)
7ffaa4aa: <log 3042> ; 7ffaa4b0: l32r a11 = 0x80000009 ; s32i a11,a7,0x0
```

Note the correction to an earlier inference: the empty-System-Area door **exists in
`KNGND100` too**, it simply has no log string. Absence of the string was not
absence of the code.

And the erase command's conditional re-init is identical:

```
KNGND122  PROC8 30033704: l32r a14,0x30033350 -> 0x7ff87c64 ; l32i ; { bnei a14,6,0x300335bf }
KNGND100  PROC8 30032234: l32r a14,0x30031e80 -> 0x7ff87cf4 ; l32i ; { bnei a14,6,0x300320ef }
```

Same instruction sequence, same global (relocated), same `6`.

### 4.3 What the downgrade is actually worth

**It does not unlatch a latched drive.** A drive with a crash section on media will
latch on `KNGND100` exactly as it does on `KNGND122`.

**It converts a self-re-arming trap into a one-shot one.** On `KNGND122`, every
unclean start writes a fresh `UNEXSTRT` stub into the crash section — so even if
the section were cleared, the next abrupt reset re-arms it. On `KNGND100` that
writer does not exist. That makes `KNGND100` the correct image to be running
**during** any recovery attempt and **on any surviving drive** you want to stop
falling into the trap in the first place.

**It is legal while latched.** The post-crash allow-list (§5) explicitly permits
Firmware Image Download (`0x11`) and Firmware Commit (`0x10`). PROVEN.

### 4.3.1 …and it re-opens three defects that CAUSE this state. Read this before flashing.

The `KNGND110` release notes list, as **fixed in 110**, three defects that are
therefore **open in `KNGND100`** (verbatim from
`docs/KNGND110_Release_Notes_v2.pdf`):

- **OM-6697, severity High — "Drive is in diagnostic mode after firmware download
  in dual port mode."** *"Drive logic trapped after issuing Firmware Activate with
  NSSR in dual port mode. Root Cause: In the event that port 0 is never enabled
  and port 1 is, the code will incorrectly issue a subsystem shutdown on each
  port."* **This is a hazard of the downgrade procedure itself** — the very act of
  activating `KNGND100` on a dual-ported U.2 backplane can trap the drive into
  diagnostic mode.
- **OM-6850, severity High — "Namespace Disappears During AC Power Cycle
  Testing."** A lost PFail interrupt when a link-down and a PFail coincide.
- **severity High — "Drive in crashed state following Power Cycle, Controller
  Reset, and Deallocate Test."** Back-to-back PFails cause media loss that
  *"over time, this leads to a crash."*

The same notes confirm the mechanism's provenance: **OM-6402, "Added Post Crash
Mode field to Identify Controller"** — byte 3072 of the Vendor Specific area —
**is a `KNGND110` addition.** The whole post-crash apparatus, including
`UNEXSTRT`, was formalised in 110. `KNGND100` predates it.

**Net honest assessment of the downgrade:** you trade the `UNEXSTRT` re-arming
for three re-opened High-severity defects, one of which fires during the flash
itself. That is a bad permanent trade and a defensible *short, controlled*
one — flash it, do the recovery, flash back. Do not leave a production drive on
`KNGND100`, and do not do it at all on a dual-ported backplane without first
confirming port 0 is enabled.

### 4.4 The `KNGND110` trap

`firmwares/KNGND110.bin` is byte-identical to `KNGND110+sblpatch+k.bin` and carries
a 21st bundle member `SBLPATCH.bin` (269 470 B) that rewrites the secondary boot
loader. `KNGND110` also already contains `UNEXSTRT`, so it gains you nothing over
`KNGND122` on this axis. **Use `KNGND100`, not `KNGND110`.** See
`sn200-firmware-flashing.md` §6.

---

## 5. What is actually permitted while latched — PROVEN

The gate, `PROC8` `0x7ffa6b30`:

```
7ffa6b1b: l32r a8,->0x7ff87c64 ; l32i.n a8,a8,0
7ffa6b30: { bnei a8,6,0x7ffa6bd9 }     ; not post-crash -> normal validation
     ...  ; post-crash: allow-list on the admin opcode in a3
7ffa6cfb: { movi a9,0 }                ; allowed
7ffa6d08: <log 1804 "Admin cmd rejected due to Post Crash startup mode: 0x%x">
7ffa6d13: l32r a9,0x7ffa0da0           ; = 0x8f8a0000  -> host sees 0x7C5, DNR=1
```

Decoding every `beq`/`beqi` between `0x7ffa6b38` and `0x7ffa6bc9`, the opcodes
that survive post-crash mode are:

| opcode | command |
|---|---|
| `0x00` | Delete I/O Submission Queue |
| `0x01` | Create I/O Submission Queue |
| `0x02` | Get Log Page |
| `0x04` | Delete I/O Completion Queue |
| `0x05` | Create I/O Completion Queue |
| `0x06` | Identify |
| `0x08` | Abort |
| `0x09` | Set Features |
| `0x0A` | Get Features |
| `0x0C` | Asynchronous Event Request |
| **`0x10`** | **Firmware Commit** |
| **`0x11`** | **Firmware Image Download** |
| `0xC6` | vendor-unique — **only** when the byte in `a4` is `0x20` or `0x30` |
| `0xCA` | vendor-unique — diverted to a separate handler at `0x7ffa6d76` |
| `0xE6`, `0xEC`, `0xFF` | vendor-unique |

### 5.1 The VUC surface that stays open while latched — an unexplored data route

Two of the permitted opcodes are vendor-unique and carry their own sub-command
allow-lists, decoded from the gate prologue (`movi a15,48`, `movi a12,230`,
`movi a10,17`, `movi a11,13`, `movi a13,198`):

- **`0xC6`** — permitted only when the sub-byte in `a4` is `0x20` or `0x30`.
  **INFERRED, and it fits exactly:** `a4` is the low byte of CDW12. The read-only
  size probes in `check-latch-state.sh` use `--cdw12=0x0320` (crash) and
  `0x0520` (pfail) — low byte `0x20`, permitted — while the destructive clears
  use `0x0503`/`0x0603`, low byte `0x03`, *not* permitted under `0xC6`. The
  vendor deliberately left the read probes open and the `0xC6` writes shut.
- **`0xCA`** — routed to its own list at `0x7ffa6d76`, which allows
  `a4` ∈ {2, 3, 4, 8, 13, 14, 15, 16, 17, 19, 21, 33, 50} — matching the 67-entry
  jump table at `0x7ffa760e` — and falls through to
  `0x7ffa6da9` → `movi a9,0` → **allowed**.

That is a dozen vendor sub-commands still reachable on a latched drive. **PROVEN.**

This matters because `PROC8` contains raw-flash read paths that do not depend on
the namespace being up:

```
30035830: <log 1848 "Admin_VucFlashLogicalToPhysical">
30036ad3: <log 1856 "Admin_VucFlashRead: NSID 0x%x out of range">
```
plus StrIds 1857–1859 (`VUC_FlashRead: Length must be one frame/LBA exactly…`),
1849–1853 (LBA → physical translation, `Snapshot_GetImageOffset`, `ddrAddr`).

**SPECULATIVE but cheap to settle:** if `Admin_VucFlashLogicalToPhysical` and
`Admin_VucFlashRead` sit on `0xCA` at one of the twelve permitted sub-codes, the
data can be pulled off a latched drive **one LBA at a time without unlatching it
at all** — no boot-mode trick, no marker, no firmware change. Mapping those two
handlers back to their opcode/sub-code is a small, well-defined job and should be
done before any of the riskier escapes.

Two things follow.

1. **The firmware-slot escape (§4) is a legal move from inside the latch.** No
   special access, no PSID, no image modification.
2. **The OAM erase command is permitted too.** `tools/sn200-fw/check-latch-state.sh`
   records the section clear as opcode `0xFF` sub `0x0503` (crash) / `0x0603`
   (pfail), and the read-only size probe as `0xC6`. Both `0xFF` and `0xC6` are on
   the allow-list above — so the drive will happily accept the erase while
   latched. That is precisely the trap: from mode 6 it also schedules the
   destructive re-init.

```
300335ca: { l32i a11,a12,0x188 ; beqz a11,0x30033704 }   ; erase of Crash Dump succeeded
30033704: l32r a14,-> 0x7ff87c64 ; l32i.n a14,a14,0
30033709: { bnei a14,6,0x300335bf }                       ; mode != 6 -> skip the re-init
30033711: { s32i a7,a12,0x128 ; movi a15,37 }             ; SysMgr message 0x25
30033719: { s32i a15,a12,0x118 ; mov a11,a6 }
30033721: call8 0x30030aa0                                ; -> schedule re-init
```

This is the circularity the brief names, and §1 now proves it is airtight:
mode 6 comes only from marker 9, and marker 9 is forced by the crash section, and
the crash section is what the erase would clear. **There is no legal ordering that
breaks it from inside a latched boot.** The only openings are the load-n-go
bypass (§2) and the marker-overwrite race (§7).

---

### 5.2 Opcode `0xFF` passes the gate — and its handler is not in the image

`0x7ffa6ba0: movi a8,255 ; beq a3,a8,0x7ffa6cfb` — **opcode `0xFF` is exempt from
the post-crash rejection**, and `0xFF` is the section-clear command
(`check-latch-state.sh`: `0xFF` CDW12 `0x0503` = crash, `0x0603` = pfail).

Its handler is at `0x7ffbc110`, which lies in the **unmapped
`0x7ffbc1xx`–`0x7ffbe6xx` region**: `PROC8`'s `0x7ff80000` flat image ends at
`0x7ffbb064`. That region is loaded from somewhere the current unpacking does not
reach. **It is the single biggest blind spot in the whole effort** — the command
that manipulates the crash sections is reachable while latched and its code is
not on disk in a form we have parsed.

---

## 6. NVMe-MI over SMBus — **a dead end. PROVEN.**

MI survives a BIOS PCIe link-disable, which made it a promising independent
transport. It is not one: it is a *different transport onto the same queue*, and
the gate is downstream of the merge.

**The tunnel converges on the gated dispatcher.** `PROC9` forwards a tunnelled
admin command to `PROC8` as IPC message id **201 (`NVME_MI_ADMIN_CMD`)`**. In
`Admin_ForwardedCommandReceiver` (`0x7ffafe68`), msgid **129** (host admin) and
msgid **201** (MI admin) branch to the *same* target:

```
7ffafe8b: beq a11,a9(129),0x7ffafedd            ; host admin
7ffafe8e: { movi a12,156 ; beq a11,a6(201),0x7ffafedd }   ; MI admin — SAME TARGET
7ffaff2a: { l32r a11,0x7ffa18b0 ; ... }         ; literal = 0x7ffa6db4
7ffaff32: call8 0x7ffb9768                      ; enqueue(ctx, fn=0x7ffa6db4)
```

`0x7ffa6db4` is the dispatcher that carries the gate, and the gate function
`0x7ffa6b18` has **exactly one call site in the entire image** (`0x7ffa7244`).
There is no ungated route.

**MI has no independent copy of the check** — `PROC9` contains zero references to
`0x7ff87c64` and zero occurrences of `0x8f8a0000`. It inherits `PROC8`'s.

**The MI admin whitelist is strictly weaker than in-band.** Decoded twice
independently (`PROC9` `0x7ffb2531`, `PROC8` `0x7ffafb40`), the tunnel forwards
exactly `{0x02, 0x06, 0x09, 0x0A, 0x10, 0x11}` and rejects everything else,
including `0x0D`, `0x15`, `0x80`, `0x81`, `0x82`. **Vendor opcodes `0xC6`, `0xCA`,
`0xE6`, `0xEC`, `0xFF` are all unreachable over MI.** Every one of the six that
*is* tunnelable is already on the post-crash allow-list — so MI grants nothing
extra, and blocks nothing extra either.

⚠ This corrects `sn200-independent-re.md` §10.1, whose MI whitelist table lists
the **rejects** (`0x11/0x0D/0x15/0x81/0x82/0xBF`) as accepts.

**MI's own command set cannot name a system-area section.** All twelve SPI
section magics (`CLOG PFCL SYSB BSCR BSTA BLOG SLOT FRMW MBBB UEFI DRVC STOC`)
appear **only in `PROC0`** — zero hits in `PROC9`, zero in `PROC8`. MI Config Set
is limited to SMBus frequency and MCTP transmission-unit size; VPD Write reaches
a single-byte-offset FRU EEPROM, not the SPI system area.

**MI Reset is double-gated and useless anyway.** Only reset type 0 is accepted,
and only when `BoardConfig[0xB2] ∈ {2,3,5}` (a runtime EEPROM byte, unknown from
the image). The reset it performs carries the literal `0x4E564D65` = `"NVMe"` —
a plain NVM Subsystem Reset, i.e. exactly the unclean stop that stamps another
`UNEXSTRT`.

MI does come up in post-crash mode (`PROC9` `0x7ffb1218` indexes a per-type table
at `0x7ff80940` = `00 08 00 …`; index 6 is `0`, not the `-1` abort sentinel).
INFERRED-strong. It is simply of no use.

---

## 7. The `DiagMgr>` console — **reachable while latched, and it is the way in**

### 7.1 The eight commands, read byte-for-byte — PROVEN

Group header is 16 bytes `{name*, desc*, hook1*, hook2*}`, then 20-byte records
`{name*, shortHelp*, longHelp*, fn*, nParams}`, NULL-name terminated. Only 3 of
the 8 registry slots at `0x7ff96f30` are used.

| group | command | handler | what it does |
|---|---|---|---|
| `native` `0x7ff81710` | `Help` | `0x7ffb1454` | read-only |
| | `Mode` | `0x7ffb14ac` | sets exact/flexible name matching at `*(0x7ff96f00)` |
| `SYS` `0x7ff80cf0` | **`SBL`** | **`0x7ffa3acc`** | **"Go into SBL diagnostic mode"** — see §7.2 |
| | `GPRS` | `0x7ffa3afc` | read-only register dump |
| | `I2CErase` | `0x7ffa3b3c` | ⚠ **destructive** — fills the three I2C EEPROM shadows with `0xFFFFFFFF` and sets the dirty flags that trigger the flush. Destroys FRU/VPD. |
| | `LogicTrap` | `0x7ffa3b60` | ⚠ deliberate crash (`break.n`) |
| `VHIST` `0x7ff81020` | `vhist` | `0x7ffa7cc8` | SerDes eye histogram; no NVM |
| | `Load` | `0x7ffb50ec` | ⚠ **a stub**: `entry a1,0x20 ; movi.n a2,0 ; retw.n` |

Two corrections to `sn200-independent-re.md`: `Load` is in `VHIST`, not `native`,
and it is a no-op. The hope that "the loadable command set is where crash-section
manipulation would live" is retired — the feature was compiled out. The
`SBL.bin`/`BIST.bin`/`SECURITY.bin` strings belong to the firmware-package
component table at `0x7ff84400`, not to `Load`.

**`Mode` sets flexible matching, under which a bare `S` resolves to `SBL`.** Know
that before typing anything at this prompt.

**No console command clears a section, writes a startup marker, or forces a
normal boot.** PROVEN for all eight handlers' resolvable call sets.

### 7.2 `SYS SBL` — the console command that writes the boot-mode word

This is the same code found in §2.3 from the other direction. It is the **sole
writer of `0x7ff9ff64` in the entire `PROC0` image** (26 load sites of the base
literal `0x7ff826b8`; 25 read `+0x4`, one writes it):

```
7ffa3acf: l32r a10,-> 0x7ff81c4c     ; "SYS: Go into SBL mode"
7ffa3ad8: l32r a10, = 0x82a60008     ; GPRS/reset MMIO block
7ffa3adb: l32r a2, -> 0x7ff9ff60
7ffa3ae6: s32i.n a9,a10,0x0          ; *(0x82a60008) = 1
7ffa3ae8: l32i.n a8,a2,0x38 ; callx8 a8    ; SYSservices->fn[0x38]  (quiesce?)
7ffa3aef: movi.n a9,5
7ffa3af1: s32i.n a9,a2,0x4           ; *(0x7ff9ff64) = 5
7ffa3af3: callx8 a8                  ; SYSservices->fn[0x24]  (reset into SBL)
```

Both indirect targets are function pointers `PROC0` never writes — supplied by
the boot loader. **SPECULATIVE:** whether `fn[0x38]` flushes the system area to
SPI before the reset.

### 7.3 It is reachable while latched — PROVEN

```
boot task 0x7ffa71dc --(first state)--> 0x7ffa7525 : enqueue(fn 0x7ffab338)
init task 0x7ffab338 --(first state)--> 0x7ffab8f4 : straight-line, no branches
  0x7ffab922: { l32r a11,-> 0x7ffa3b78 ; mov a10,a12 }   ; console task
  0x7ffab92a: call8 0x7ffb32f8                            ; enqueue
console task 0x7ffa3b78: console_init, group registration, poll loop
  -- NO startup-type test anywhere
```

The startup type is computed later (`0x7ffaac30` → `*(0x7ff8c788+0x30)`) and
first *used* by the init task at `0x7ffab370` — **after the console already
exists.** `0x7ffb4560` also calls `console_init` unconditionally right after
`SYS: Firmware is starting`. The dispatcher `0x7ffb1612` has no gate; the `0x7C5`
rejection applies to NVMe admin commands only.

**So on a post-crash-latched SN200 the serial console should still come up, and
`SYS SBL` should still execute.** The only unproven edge is who enqueues
`0x7ffa71dc` (started from outside `PROC0`); everything downstream is
unconditional.

### 7.4 Physical layer — the one thing firmware cannot tell us

- **115200 baud** is a compile-time constant with one reference at `0x7ffb1b7b`,
  and the callee `0x7ffb4ad0` **ignores its argument**. `PROC0` does not program a
  divisor.
- **No UART MMIO and no pinmux in `PROC0`.** All character I/O goes through a
  two-entry hook table at `0x7ff848c8` whose static contents are `entry; retw`
  no-ops; the real hooks are installed at runtime from `SYSservices+0x40/+0x48`,
  getchar from `+0x3c`. **The physical UART driver is outside this image.**
- RX *is* enabled and polled every scheduler pass (`0x7ffb1abc` → `0x7ffb4b68`),
  with a full ANSI/CSI line editor, echo, CR → tokenise (`0x7ffb14bc`) → dispatch
  (`0x7ffb15a4`) → print `RV:%d`.
- Only UART-shaped constant: `0x81860030` (literal `0x7ff83cec`), single
  misaligned reference near `0x7ffb58e8`. **SPECULATIVE.**

**Pinout remains UNKNOWN from firmware.** It has to come from the board.

---

## 8. The `0x7ff8b4f8` / `0x7ff8d200` divergence — resolved, not an escape

The earlier note flagged that the boot latch reads `0x7ff8b4f8` while the
section-state manager writes `0x7ff8d200`, and that nothing proved they stay in
sync. Both halves are now confirmed as *real distinct objects*:

```
7ffaac38: l32r a5,0x7ff829a8    ; -> 0x7ff8b4f8   the boot-time SA scan result byte
7ffa37bf: l32r a7,0x7ff826d8    ; -> 0x7ff8d200   the runtime section-state byte
7ffa37cd: l8ui a9,a7,0x0 ; { bany a9,mask 0x1,0x7ffa3749 }   ; same bit-0 meaning
```

They are two copies with identical bit semantics. **But it is not an escape.**
`0x7ff8b4f8` is populated by the system-area scanner *during* boot, immediately
before the dispatcher reads it, from the media itself. Divergence at runtime is
irrelevant — the latch never reads the runtime copy. Downgrade this line of
inquiry to closed. **PROVEN.**

---

## 9. Escape D — overwrite the scheduled re-init marker with a clean shutdown. **SPECULATIVE.**

Sketched here because it is the only remaining ordering trick, not because it is
recommended.

`sn200-shutdown-path.md` §1.5 establishes that a completed shutdown writes the
final marker from `PROC6` `0x7ffbba61`:

```
7ffbba48: l32r a15,0x7ffa227c        ; = 0x80000001  CLEAN shutdown
7ffbba55: l32r a13,0x7ffa2280        ; = 0x80000002  PFAIL shutdown
7ffbba5e: moveqz a13,a15,a8
```

So *if* the OAM crash-dump erase (a) actually clears the section on media before
(b) merely *scheduling* a re-init as a next-boot marker, then a clean NVMe
shutdown issued afterwards would overwrite marker 3 with marker 1, and the next
cold boot would find an empty crash section and a clean marker.

The whole thing hinges on whether SysMgr message `0x25` writes marker 3 for the
next boot or performs the re-init live. `0x30030aa0` is a generic message-send
helper; the consumer was not traced. **Do not act on this without tracing the
`0x25` handler.** If it re-inits live, the data is gone the instant the command
completes.

---

## 10. Ranked escapes

| # | Escape | Status | Destructive? | What it costs |
|---|---|---|---|---|
| 1 | **`DiagMgr>` → `SYS SBL` → SBL console → boot mode 4 or EEPROM section 6** | every firmware link **PROVEN**; UART pinout **unknown**; SBL console code **unparsed** | no | physical UART + a `PMCSEEPM001` parser |
| 2 | **Raw-flash VUC read on a still-latched drive** (§5.1) | VUC surface open **PROVEN**; opcode mapping **unresolved** | no | small RE job — *do this first* |
| 3 | **`0xFF` section-clear semantics** (§5.2) | reachable **PROVEN**; handler **not in the image** | unknown | map the `0x7ffbc1xx` region |
| 4 | **Firmware-download flags → marker write** (§3.4) | one flag bit **PROVEN** to steer a marker write; provenance **untraced** | no | trace `[parent+0xA8]` |
| 5 | **Downgrade to `KNGND100`** | **PROVEN** legal; **does not unlatch**; re-opens 3 High defects | no, but the flash itself can trap the drive | trivial, reversible, *not* free |
| 6 | Controller-restart probe after a commit | **SPECULATIVE** | no | free |
| 7 | Erase + clean-shutdown marker race (§9) | **SPECULATIVE**, possibly catastrophic | maybe | do not attempt |
| — | Marker 8 via any firmware path | **PROVEN dead** (§3.2) | — | — |
| — | NVMe-MI tunnel | **PROVEN dead** (§6) | — | — |

Rows 2, 3 and 4 are desk work that could make row 1 unnecessary. Row 1 is the only
one proven to actually break the latch.

## 11. Recommended plan

Nothing below has been executed. **Do not touch a drive until step 0 is
finished** — two of the open questions are settleable from files already on disk,
and either could make every hardware step unnecessary.

### 0. Desk work first — no hardware, no risk, highest expected value

1. **Map `Admin_VucFlashLogicalToPhysical` (`PROC8` `0x30035830`) and
   `Admin_VucFlashRead` (near `0x30036ad3`) to their opcode and sub-code** (§5.1).
   If either sits on `0xCA` at one of the thirteen permitted sub-codes, the data
   comes off a **latched drive with no state change whatsoever**. Cheapest
   possible win; do it before anything else.
2. **Map the unmapped `0x7ffbc1xx`–`0x7ffbe6xx` region** that holds the `0xFF`
   handler (§5.2). `0xFF` passes the gate and is the section-clear command; its
   exact semantics are the difference between a clean exit and a wiped drive.
3. **Trace the firmware-download flags** back from `PROC0` `[parent+0xA8]` to
   `PROC8` `Admin_FwDownloadSendSysMsg` (`0x30025f3f`) (§3.4). Bit 0 already
   steers a marker write. If any bit selects a caller-supplied marker, marker 8
   becomes reachable in band and the whole hardware plan is moot.
4. **Write a `PMCSEEPM001` parser** for `SBLPATCH.bin` (16-byte address/value
   records; `segparse.py` returns zero segments on it), then disassemble the SBL
   console: confirm `MemWrite` reaches the `0x7ffx_xxxx` space, confirm the
   console holds control *before* the firmware handoff, and find how `SYS SBL`'s
   boot-mode `5` is consumed.
5. Run `tools/sn200-fw/check-latch-state.sh` on each drive — genuinely read-only
   (two `0xC6` size probes and an Identify). A PFAIL-only latch has a documented
   safer clear; a CRASH latch does not.

### The best escape, and its exact steps

If step 0 does not produce a cheaper answer, this is the route with every
firmware link proven:

1. **Find the UART.** `PROC0` cannot tell you the pins — it has no UART MMIO and
   no pinmux (§7.4). This is a board-level job: probe the U.2 test pads / the
   card-edge debug header for a 115200 8N1 line that emits `SYS: Firmware is
   starting` at power-on. The console echoes and prints `RV:%d` after each
   command, so it is unmistakable once found.
2. **Confirm the prompt on a latched drive.** Expect `DiagMgr> `. Type `Help` —
   it is read-only and lists the three groups. This alone validates §7.3 on real
   hardware and is worth doing on its own.
3. **Do not type anything else yet.** `I2CErase` destroys the FRU/VPD and
   `LogicTrap` deliberately crashes the drive; with `Mode` set to flexible
   matching a bare `S` can resolve to `SBL` (§7.1). Set exact matching first if
   the syntax allows.
4. **`SYS SBL`.** This writes boot-mode `5` and resets into the loader (§7.2).
5. **In the SBL console**, take whichever of the two proven predicates the console
   can actually reach:
   - `MemWrite 0x7ff9ff64 4` — boot mode `LOAD_N_GO`, which skips the latch
     entirely (§2.2); or
   - a read-modify-write of **EEPROM System-Area section 6, copy 0, word 0** to
     `0x80000008` — startup type 3, `READ ONLY STARTUP`, ungated, L2P intact
     (§3.3). Remember copy 1 at `+0xF4`: the dispatcher heals the primary from the
     secondary (`0x7ffaae1e`), so a half-write may be undone.
6. **Boot, then copy the data off immediately.** Do not attempt to "fix" the drive
   before the data is safe.

**Risk.** The console itself is read/write access to a live controller with no
undo. `MemWrite` to a wrong address can hang or corrupt the running loader — but
it cannot reach user data, and the drive is already unusable, so the downside is
bounded at "this drive stays dead". The genuine hazards are mistyping into
`I2CErase` (destroys FRU/VPD, does not touch user data) and any write to the
System Area that leaves the two section-6 copies inconsistent. **The data-bearing
risk is low; the brick-the-controller risk is real.** Do it on a spare latched
drive first, and never on the drive still holding data until the sequence has been
rehearsed end to end.

### Fallback — downgrade a spare to `KNGND100` (only after step 0, and it is *not* a cure)

Slot 1 is read-only; slots 2–5 are writable. Firmware Download (`0x11`) and
Firmware Commit (`0x10`) are both on the post-crash allow-list, so this works from
inside the latch.

⚠ **This deliberately contradicts `tools/sn200-fw/fill-fw-slots.sh`**, which
exists to fill every writable slot with `KNGND122` so no future activation can
land on an older revision. It hard-codes Commit Action 0 and refuses any image but
`KNGND122`. It will not perform this step and should not be modified to. Its
reasoning is still right for the *healthy* drives; this is a different trade for
an *already latched* one.

```
# 1. Extract the image (do NOT use KNGND110 — see §4.4)
unzip -j ~/Downloads/HGST-UltraStar-SN200-HHHL.zip \
      'HGST-UltraStar-SN200-HHHL/firmwares/KNGND100.bin' -d /tmp/sn200
sha256sum /tmp/sn200/KNGND100.bin
# expect 134d67c992f8938a59b67ce0a1788bf04fddf3dd5b56fe8a8897c2b518203309

# 2. Confirm the slot layout. Never let the tooling pick a slot.
nvme fw-log /dev/nvmeN

# 3. Stage into an explicitly named writable slot (2..5), activate on next reset.
nvme fw-download /dev/nvmeN --fw=/tmp/sn200/KNGND100.bin --xfer=4096
nvme fw-commit   /dev/nvmeN --slot=3 --action=1

# 4. Cold power cycle, then re-read the latch state.
tools/sn200-fw/check-latch-state.sh /dev/nvmeN
```

**Expected:** the drive *still latches*. That is the prediction, not a failure —
§4.2 shows `KNGND100` carries the identical latch. The only thing gained is that
an abrupt reset no longer stamps a fresh `UNEXSTRT`, so the trap stops re-arming
while you work.

**Risks, in order.** (a) `KNGND100` has **OM-6697** open: a Firmware Activate with
NSSR in dual-port mode can logic-trap the drive into diagnostic mode — the flash
can *cause* the failure you are escaping. Confirm the backplane is single-port or
that port 0 is enabled, and prefer a controller reset over NSSR where the tooling
allows. Note the vendor's own instruction that *"Controller Reset cannot be used
to activate firmware in a dual port configuration"* and that downgrading below
`KNGND110` is explicitly outside the documented procedure. (b) `KNGND100` also has
OM-6850 and the back-to-back-PFail crash defect open (§4.3.1) — do not leave a
drive on it. (c) `CA=1`/`CA=2` are the only accepted commit actions (`CA=3` is
rejected — §2.3); slot 1 will refuse. A bad commit leaves the previous slot intact
and the drive still boots.

**Free probe, do it during step 4.** `PROC0` StrIds 790–793 name three activation
reset levels — Controller Restart, Subsystem Restart, Conventional Reset. Only a
conventional/cold reset is known to re-run the system-area scan that fills
`0x7ff8b4f8`. **SPECULATIVE:** a controller-level restart (`CC.EN` 1→0→1) after a
commit might re-enter the dispatcher without a fresh scan. Costs nothing to watch.

### Do not attempt

- Any OAM crash-dump erase from a latched drive (§5) — it schedules the
  destructive re-init by design, and `0xFF` is on the allow-list so the drive
  *will* accept it.
- The marker-overwrite race (§9), until the SysMgr `0x25` handler is traced. If it
  re-inits live rather than deferring to the next boot, the data is gone the
  instant the command completes.
