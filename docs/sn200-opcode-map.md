# SN200 (`HUSMR7676BDP3Y1`, `KNGND122`) — the complete admin command dispatch

Every prior SN200 analysis started from an opcode we already knew (`0xFF`,
`0xCA`, `0xC6`) or from the Post-Crash gate. **This document starts from the
dispatcher.** It enumerates what the controller actually *implements*, standard
and vendor, allow-listed or not.

Labels: **PROVEN** = read off correctly-decoded instructions. **INFERRED** =
short chain over proven facts. **UNKNOWN** = not established; stated as such.

Companion documents: `sn200-command-reference.md` (safety classification — the
document you act from), `sn200-oam-dispatch.md` (`0xFF`), `sn200-c6-dispatch.md`
(`0xC6`), `sn200-vuc-flash-read.md` (`0xCA` sub-values `0x00`–`0x03`),
`sn200-vendor-tooling.md` (**vendor names for these encodings**, cross-referenced
against `nvme-cli`'s WDC plugin — and which of them do *not* apply to us).
**This document is an index, not a safety authorisation. Nothing new here is
cleared to send.**

---

## 1. Where the dispatcher is, and how it decides — PROVEN

`PROC8_7ff80000`. The command context is re-based `+0x100` inside the
dispatcher, so the fields appear at:

| dispatcher offset | field |
|---|---|
| `ctx+0x118` | `CDW0` — low byte is the **opcode** |
| `ctx+0x138` | `CDW12[7:0]` — the vendor **command byte** |
| `ctx+0x139` | `CDW12[15:8]` — the vendor **sub byte** |
| `ctx+0x13c` | `CDW13` |

Root of the decision tree, `0x7ffa725f`:

```asm
7ffa725f: l32i.n a9,a1,0x24              ; a9 = ctx+0x100
7ffa7261: l32i.n a9,a9,0x18              ; a9 = CDW0
7ffa726e: { extui a11,a9,0,8 ; movi a12,20 }        ; a11 = OPCODE
7ffa7276: { movi a14,12 ; bltui a11,128,0x7ffa7c0d } ; op < 0x80  -> low tree
7ffa727e: movi a10,128
7ffa7281: { extw ; bltu a10,a11,0x7ffa7523 }         ; op > 0x80  -> vendor tree
7ffa7289: l32i.n a12,a6,0x0                          ; op == 0x80 -> Format NVM
7ffa728b: { l32r a13,-> 0x7ffbc434 ; movi a11,6 }    ;   handler, overlay 6
```

Every arm has the same three-instruction shape: load the handler runtime
pointer into `a13`, store an **overlay index** into `[a12+0x20]`, and jump to the
common enqueue at `0x7ffa6e89`. A handful of opcodes instead take the
`j 0x7ffa7d2a` path, which stores a *resident* (non-overlay) handler pointer at
`[a7+0x10]` — those handlers live in the main image and need no overlay load.

Unimplemented opcodes converge on `0x7ffa75b4`:

```asm
7ffa75b4: l32r a10,-> StrId 1821 "Admin cmd not supported 0x%x\n"
7ffa75b7: call8 0x7ffb45a8            ; Log_Emit
7ffa75c3: { s32i a9,a7,0x160 ; j 0x7ffa6e6c }   ; status |= 0x40040000, no side effect
```

### 1.1 Resolving an overlay handler address

The dispatcher stores a **runtime** pointer in the `0x7ffbc000` overlay window
plus an overlay index. To read the code you need the static address:

```
static = src2(overlay N) + (runtime − 0x7ffbc000)
```

The overlay descriptor table is at **`0x7ff81af0`**, **16 bytes per row**,
fields `{flags, dst, len, src2}`. Rows alternate between two banks: a **code**
bank (`dst = 0x7ffbc000`) and a **data** bank (`dst = 0x7ff9f000`). The index
the dispatcher stores at `request+0x20` is **1-based into the code bank**, so:

```
code row for overlay N  =  0x7ff81af0 + (N−1)*32          (i.e. every other row)
src2                    =  word 3 of that row
```

34 overlays. This was derived independently twice in this pass (once by
scanning for `dst == 0x7ffbc000`, once from the Set/Get Features dispatch) with
identical results.

`sn200-c6-dispatch.md` §1 gives `0x7ff81ae4` as the table base with a 12-byte
stride; that reading lands on the right row for overlay 18 by coincidence of
arithmetic and is **wrong in general**. The layout above reproduces every
independently known result:

| check | expected (published elsewhere) | this table |
|---|---|---|
| ovl 18 (`0xC6`) | `src2 = 0x3002ea38`, `len = 0x3040` | ✔ |
| ovl 22, `0xFF` runtime `0x7ffbc110` | static `0x30033448` | `0x30033338 + 0x110` ✔ |
| ovl 26, `0xCA/0x03` runtime `0x7ffbdab0` | static `0x30036e28` | `0x30035378 + 0x1ab0` ✔ |
| ovl 26, `0xCA/0x01` runtime `0x7ffbd11c` | static `0x30036494` | `0x30035378 + 0x111c` ✔ |
| ovl 26, `0xCA/0x00` runtime `0x7ffbc308` | static `0x30035680` | `0x30035378 + 0x308` ✔ |

Five independent hits from three different published analyses. **PROVEN.**

---

## 2. The complete admin opcode table — PROVEN

Everything below was read out of the dispatcher. "Implemented" means the
dispatcher routes it to a handler; it says nothing about whether the handler
then rejects your parameters.

Purposes are sourced from log-string descriptors, and the **evidence class is
marked**, because these handlers are almost all coroutine trampolines:

- *unmarked* — the string is inside the handler's **own confirmed function
  extent** (`function-map.json`). Strongest.
- **¹** — the string is in the handler's **coroutine body**: the address range
  from its `entry` up to the next handler entry in the same overlay. Sound for
  a coroutine (the resume bodies immediately follow the trampoline) but it is
  an ordering argument, not a containment one. Treat as **INFERRED**.
- **UNKNOWN** — neither. Stated rather than guessed.

Never attribute by a fixed byte window from the entry: most of these stubs are
30–120 bytes and a 0x300-byte window absorbs the next two functions' strings.
An earlier draft of this table mislabelled `0xCA` `0x11` as a Multiplane Write
for exactly that reason.

### 2.1 Standard NVMe admin opcodes

| op | handler (runtime) | ovl | purpose | evidence |
|---|---|---|---|---|
| `0x00` | `0x7ffbc260` | from `[a1+0x28]` | Delete I/O SQ | spec + dispatch position, PROVEN |
| `0x01` | `0x7ffbc4a8` | ″ | Create I/O SQ | ″ |
| `0x02` | `0x7ffa4d08` | resident | Get Log Page | ″ |
| `0x04` | `0x7ffbc828` | ″ | Delete I/O CQ | ″ |
| `0x05` | `0x7ffbc9c4` | ″ | Create I/O CQ | ″ |
| `0x06` | `0x7ffab518` | resident | Identify | ″ |
| `0x08` | `0x7ffa673c` | resident | Abort | ″ |
| **`0x09`** | **`0x7ffaa628`** | **resident** | **Set Features** — see `sn200-latch-prevention.md` | PROVEN |
| `0x0A` | `0x7ffbc92c` → `0x300249a4` | 4 | Get Features | own strings: "GetFeat Interrupt Vector Out of range", "GetFeat TempThreshold…" |
| `0x0C` | `0x7ffa3818` | resident | Asynchronous Event Request | PROVEN |
| `0x0D` | `0x7ffbcf4c` → `0x3002c744` | 12 | Namespace Management | own strings `Admin_NamespaceManagement:…` |
| `0x10` | `0x7ffbc440` → `0x30025838` | 5 | Firmware Commit | ″ |
| `0x11` | `0x7ffbc198` → `0x30025590` | 5 | Firmware Image Download | ″ |
| `0x15` | `0x7ffbd948` → `0x3002d140` | 12 | Namespace Attachment | own strings `Admin_NamespaceAttachment:…` |
| `0x80` | `0x7ffbc434` → `0x300266ac` | 6 | **Format NVM** | own strings `Admin_Format:…` |

**Everything else in `0x00`–`0x7F` is NOT implemented.** Specifically absent and
falling to "Admin cmd not supported": `0x03`, `0x07`, `0x0B`, `0x0E`, `0x0F`,
`0x12`, `0x13`, `0x14` (Device Self-test), `0x16`–`0x7F` (so **no** Keep Alive
`0x18`, **no** Directive Send/Receive `0x19`/`0x1A`, **no** NVMe-MI Send/Receive
`0x1D`/`0x1E`, **no** Doorbell Buffer Config `0x7C`).

**`0x81` Security Send, `0x82` Security Receive, `0x84` Sanitize and `0x86` Get
LBA Status are NOT implemented either** — the vendor tree's first two
comparisons (`op ≤ 0xC9` → `op ≤ 0xC7` → `op == 0xC6` or reject) send all of
`0x81`–`0xC5` and `0xC7` to the unsupported path. **PROVEN.** The drive's
"secure purge" is the vendor opcode `0xDD`, not the spec Sanitize opcode.

### 2.2 Vendor opcodes `0xC0`–`0xFF` — the complete list

| op | handler (runtime → static) | ovl | identity | status |
|---|---|---|---|---|
| `0xC6` | `0x7ffbea44` → `0x3003147c` | 18 | VUC SCSI Ported Command — WDC `CAP_DIAG_CMD_OPCODE`, the generic VUC transport | documented, `sn200-c6-dispatch.md`; six of its `0x20` subs vendor-CONFIRMED |
| **`0xC8`** | `0x7ffbc180` → `0x30031bf8` | 19 | **VCAP failure injection** — sub `0` = *fake* vcap failure, sub `1` = *clear* fake vcap failure | **NEW** |
| **`0xC9`** | `0x7ffbc038` → `0x300329f0` | 20 | **UNKNOWN** — 114-byte coroutine stub, no strings of its own | **NEW** |
| `0xCA` | 67-entry jump table, §3 | various | VUC Flash family | partly documented |
| **`0xCC`** | 8-arm sub-dispatch, §4 | 12/24/25 | **`Admin_VUC_Sys_*` / `Admin_VUC_Device_Config_*` — the system/config family**. Command `0x03` sub `0x01` (`CDW12 = 0x0103`, `CDW13 = new size`) is WDC **Drive Resize** — `sn200-vendor-tooling.md` §5 | **NEW** |
| **`0xD4`** | 11-arm sub-dispatch, §5 | 21 + resident | **diagnostics / power-off / FW-slot-erase / error injection** | **NEW** |
| **`0xD7`** | *same sub-dispatch as `0xD4`* | ″ | alias of `0xD4` — the dispatcher falls through | **NEW** |
| **`0xD9`** | `0x7ffbcf4c` → `0x3002c744` | 12 | **alias of the `0x0D` Namespace Management handler** | **NEW** |
| `0xDD` | `0x7ffa76dc` | — | Start Secure Purge — WDC `PURGE_CMD_OPCODE`. SN100/SN200 are the **only** two families in the whole WDC plugin granted this | documented |
| `0xDE` | via `0x7ffa7593` → `0x7ffa7d2a` | resident | Secure Purge status — WDC `PURGE_MONITOR_OPCODE` (`CDW10 = 0x0C`, 0x2F bytes) | documented |
| `0xE6` | `0x7ffb375c` | resident | **WDC `CAP_DIAG_OPCODE` — Capture Diagnostics.** Header read: `CDW10 = 2`, `CDW12 = 0`, 8 bytes, length in bytes `[4..7]` **big-endian** | vendor-named, `sn200-vendor-tooling.md` §4 |
| `0xEC` | `0x7ffbc24c` → `0x3002b6c4` | 11 | **`Admin_VUC_Enable`** — see §6 | **NEW identification** |
| **`0xEF`** | `0x7ffbc5f4` → `0x3003392c` | 22 | **`Admin_VUC_Mi_Test_OVL022`** — NVMe-MI command inject / response retrieve | **NEW** |
| `0xFF` | `0x7ffbc110` → `0x30033448` | 22 | OAM command — WDC `CLEAR_DUMP_OPCODE`. `CDW12 = 0x0503`/`0x0603` (clear crash / pfail dump) are **vendor-CONFIRMED**, encoding for encoding, against our executed oracle | documented, `sn200-oam-dispatch.md` |

**Vendor names, and the four traps in them.** `sn200-vendor-tooling.md` carries
the full cross-reference. In short: `0xC6`, `0xE6`, `0xDD`, `0xDE`, `0xFF` and
`0xCC`/`0x0103` are corroborated by `nvme-cli`'s WDC plugin; `0xC8`, `0xC9`,
`0xD4`, `0xD7`, `0xD9`, `0xEC`, `0xEF` and our `0xCA` have **no** vendor name.
The plugin *does* use the values `0xC9`, `0xCA` and `0xD4` — for **OpenFlex
enclosure** commands and **log page ids** on other products. Those names are
numeric collisions and must not be adopted here.

Not implemented in the vendor range: `0xC0`–`0xC5`, `0xC7`, `0xCB`,
`0xCD`–`0xD3`, `0xD5`, `0xD6`, `0xD8`, `0xDA`–`0xDC`, `0xDF`–`0xE5`,
`0xE7`–`0xEB`, `0xED`, `0xEE`. **PROVEN** — each of those values reaches
`0x7ffa75b4`.

Exact dispatcher sites, for re-verification:

```asm
7ffa7523: { l32r a13,… ; movi a8,216 }
7ffa752b: { l32r a10,… ; bgeu a8,a11,0x7ffa75cb }    ; op <= 0xD8
7ffa7533: movi a9,217
7ffa7536: { extw ; bgeu a9,a11,0x7ffa7c95 }          ; op == 0xD9 -> 0x7ffbcf4c ovl 12
7ffa753e: movi a12,235
7ffa7541: bgeu a12,a11,0x7ffa7587                    ; 0xDA..0xEB
7ffa7544: movi a13,236
7ffa7547: { movi a10,11 ; bltu a13,a11,0x7ffa755c }
7ffa754f: l32i.n a12,a6,0x0                          ; op == 0xEC -> 0x7ffbc24c ovl 11
7ffa755c: movi a14,254
7ffa755f: { movi a10,22 ; bgeu a14,a11,0x7ffa7574 }
7ffa7567: l32i.n a12,a6,0x0                          ; op == 0xFF -> 0x7ffbc110 ovl 22
7ffa7574: movi a15,239
7ffa7577: bne a11,a15,0x7ffa75b4                     ; op == 0xEF -> 0x7ffbc5f4 ovl 22
7ffa7587: movi a12,221 ; bgeu a12,a11,0x7ffa75ac     ; 0xDD
7ffa758d: movi a8,222  ; bltu a8,a11,0x7ffa759b      ; 0xDE
7ffa759b: movi a9,230  ; bne a11,a9,0x7ffa75b4       ; 0xE6 -> 0x7ffb375c
7ffa75cb: movi a8,201  ; bgeu a8,a11,0x7ffa7bbd      ; op <= 0xC9
7ffa75d6: movi a9,202  ; bltu a9,a11,0x7ffa7107      ; 0xCB..0xD8
7ffa75e1:                                            ; op == 0xCA -> jump table, §3
7ffa7bbd: movi a13,199 ; bgeu a13,a11,0x7ffa7bf5     ; op <= 0xC7 -> only 0xC6 accepted
7ffa7bc3: { movi a14,200 ; movi a10,19 }             ; op == 0xC8 -> 0x7ffbc180 ovl 19
7ffa7be0: { movi a10,20 ; bne a11,a12,0x7ffa75b4 }   ; op == 0xC9 -> 0x7ffbc038 ovl 20
7ffa7107: movi a8,211  ; bgeu a8,a11,0x7ffa7aed      ; 0xCB..0xD3 -> only 0xCC accepted
7ffa7112: movi a9,212  ; bgeu a9,a11,0x7ffa7123      ; 0xD4 -> sub-dispatch
7ffa7118: movi a12,215 ; bne a11,a12,0x7ffa75b4      ; 0xD7 falls into the SAME sub-dispatch
```

---

## 3. `0xCA` — the complete 67-entry jump table, PROVEN

Bounds-checked and dispatched at `0x7ffa75e1`:

```asm
7ffa75e1: l32i.n a12,a1,0x24
7ffa75e3: l8ui a12,a12,0x38                     ; a12 = CDW12[7:0]
7ffa75f6: { movi a10,26 ; bgeu a12,a8,0x7ffa78e3 }   ; a8 = 67 -> default/reject
7ffa75fe: l32r a9,-> 0x7ffa760e                 ; table base
7ffa7601: addx2 a8,a12,a12                      ; index*3 (3-byte `j` slots)
7ffa7604: add.n a8,a8,a9
```

The table at `0x7ffa760e` holds 67 three-byte `j` instructions. **39 of the 67
are implemented** — this said 37 while listing 39 rows; the two inline arms
`0x05`/`0x06` load no overlay handler and were dropped from the count. Executed
count from `sn200_oracle.py --ca` (`sn200-ca-dispatch.md` §1.1). The rest jump
to the common default `0x7ffa78e3`.

| `CDW12[7:0]` | handler (runtime → static) | ovl | identity / evidence |
|---|---|---|---|
| `0x00` | `0x7ffbc308` → `0x30035680` | 26 | `Admin_VucFlashLogicalToPhysical` |
| `0x01` | `0x7ffbd11c` → `0x30036494` | 26 | `Admin_VucFlashRead` (LBA-aware read) |
| `0x02` | `0x7ffbc2a8` → `0x30038860` | 28 | `Admin_VucFlashUID_OVL028` — get flash UID / UID length ¹ |
| `0x03` | `0x7ffbdab0` → `0x30036e28` | 26 | raw NAND page read (`Flash_ReadRawData` / `ReadCacheData`) |
| `0x04` | `0x7ffbccec` → `0x3003a2e4` | 29 | **UNKNOWN** — 59-byte stub, no strings |
| `0x05` | *resident, immediate* | — | calls `0x7ffa915c(1)`, `0x7ffa9168`; stores result at `req+0x154`. **UNKNOWN**, paired with `0x06` |
| `0x06` | *resident, immediate* | — | identical to `0x05` but with argument `0`. **UNKNOWN** |
| `0x08` | `0x7ffbc5f0` → `0x30035968` | 26 | `Admin_VucFlashVirtualToPhysical` |
| `0x09` | `0x7ffbc08c` → `0x3003de04` | 32 | **UNKNOWN** — 123-byte stub |
| `0x0A` | `0x7ffbc2d8` → `0x3003e050` | 32 | **UNKNOWN** — 125 bytes, no strings in the function or its body |
| `0x0B` | `0x7ffbd32c` → `0x3003b8a4` | 30 | erase-count scan (own strings: "Scan Erase Count End", blockset census) |
| `0x0C` | `0x7ffbd1c4` → `0x3003b73c` | 30 | **UNKNOWN** — 360-byte body, no strings. ⚠ **Corrected 2026-08-04:** this row said "erase-count / blockset census"; all fourteen census strings are in `0x0B`'s body (`sn200-ca-dispatch.md` §2.3) |
| `0x0D` | `0x7ffbcb48` → `0x30039100` | 28 | `Admin_VucFlashReset_OVL028` ¹ |
| `0x0E` | `0x7ffbcd1c` → `0x300392d4` | 28 | `Admin_VucFlashReadStatus_OVL028` ¹ (corroborated by `sn200-command-reference.md` §3) |
| `0x0F` | `0x7ffbdf28` → `0x3003dbe0` | 31 | ☠ raw NAND **block erase** |
| `0x10` | `0x7ffbd904` → `0x3003d5bc` | 31 | ☠ raw NAND **page program** |
| `0x11` | `0x7ffbc670` → `0x300359e8` | 26 | 26-byte stub: `l32i a10,ctx+0x13c` (CDW13) → `call 0x3002d2c4` → store to `ctx+0x154`. Benign, confirms the existing "four-instruction stub" reading |
| `0x12` | `0x7ffbd108` → `0x3003b680` | 30 | `VUC_ERASE_PWR_CHAR` — **erase power characterisation; this arm performs erases** |
| `0x13` | `0x7ffbce34` → `0x300393ec` | 28 | **UNKNOWN** — 64-byte stub |
| `0x20` | `0x7ffbcce4` → `0x3003b25c` | 30 | **UNKNOWN** — 1060-byte body, no strings. ⚠ **Corrected 2026-08-04:** this row said `VUC_ERASE_PWR_CHAR` ¹; both of those strings are in `0x12`'s body, one inside `0x12`'s own confirmed extent (`sn200-ca-dispatch.md` §2.3). The method warning below predicted this exact mislabel |
| `0x21` | `0x7ffbcaa8` → `0x3003b020` | 30 | **UNKNOWN** — 108 bytes, no own strings; same overlay as `0x12` |
| `0x22` | `0x7ffbc0e8` → `0x30037da0` | 27 | soft-LDPC read histogram ¹ |
| `0x25` | `0x7ffbc2fc` → `0x30037fb4` | 27 | `Admin_VucFlashSLDPCHistoryHistogram_OVL027` |
| `0x26` | `0x7ffbc5e8` → `0x300382a0` | 27 | hard-LDPC read histogram ¹ |
| `0x32` | `0x7ffbc148` → `0x30039740` | 29 | **UNKNOWN** — 313 bytes, no own strings; sibling of `0x34` |
| `0x33` | `0x7ffbc68c` → `0x30038c44` | 28 | `Admin_VucFlashReadLotID_OVL028` — wafer lot ID (SanDisk flash only) ¹ |
| `0x34` | `0x7ffbc404` → `0x300399fc` | 29 | VUC Get Dies Status ¹ |
| `0x35` | `0x7ffbd09c` → `0x30036414` | 26 | flash read, 42-byte sibling of `0x01`/`0x36` |
| `0x36` | `0x7ffbd0dc` → `0x30036454` | 26 | flash read, 42-byte sibling of `0x01`/`0x35` ¹ |
| `0x37` | `0x7ffbc68c` → `0x30035a04` | 26 | ☢ Multiplane Write / Erase |
| `0x38` | `0x7ffbe460` → `0x300377d8` | 26 | **NAND-chip Get Features** ("VUC: Get features failed 0x%02x") — ONFI feature address, *not* NVMe Set Features |
| `0x39` | `0x7ffbe5b0` → `0x30037928` | 26 | ☢ **NAND-chip Set Features** ("VUC: Set Features addr 0x%02x: 0x%08x") |
| `0x3A` | `0x7ffbe6e8` → `0x30037a60` | 26 | `Admin_VucFlashGetTestModeRegister_OVL026` |
| `0x3B` | `0x7ffbe7e0` → `0x30037b58` | 26 | ☢ `Admin_VucFlashSetTestModeRegister_OVL026` |
| `0x3E` | `0x7ffbdfd0` → `0x30037348` | 26 | Read FuseRom / REG2SA (SanDisk flash only) ¹ |
| `0x3F` | `0x7ffbe2a4` → `0x3003761c` | 26 | Read SanDisk MT (Memory Test) information ¹ |
| `0x40` | `0x7ffbd774` → `0x30036aec` | 26 | `Flash_ReadRRShiftLevel` (read-retry shift levels) ¹ |
| `0x41` | `0x7ffbcacc` → `0x3003a0c4` | 29 | Permanent Die Offline list ¹ |
| `0x42` | `0x7ffbc5e8` → `0x30039be0` | 29 | **UNKNOWN** — 601 bytes, only the generic request-census string |

Not implemented: `0x07`, `0x14`–`0x1F`, `0x23`, `0x24`, `0x27`–`0x31`, `0x3C`,
`0x3D`, and everything `≥ 0x43`.

### 3.1 Two new one-nibble adjacency hazards in this family

- ☠ **`0x3A` (get flash test-mode register) sits one value below `0x3B` (SET
  flash test-mode register).** Writing a NAND die's test-mode register is not a
  documented, bounded operation; treat `0x3B` as potentially device-destroying.
  Neither is on the Post-Crash allow-list.
- ☠ **`0x38` (NAND get-features) sits one value below `0x39` (NAND
  SET-features).** `0x39` writes an ONFI feature address on the flash die
  itself. Same hazard shape as `0x0E`/`0x0F`/`0x10`, in a part of the table
  nobody had looked at. Neither is allow-listed.
- ⚠ **`0x12` is a `VUC_ERASE_PWR_CHAR` arm** — the strings ("is doing erasure
  now, can NOT accept `VUC_ERASE_PWR_CHAR` again") say it **erases blocks** as
  part of a power-characterisation sweep. `0x12` is *directly adjacent to
  `0x13`, which IS on the Post-Crash allow-list*. `0x20` and `0x21` sit in the
  same overlay and have no strings of their own — both unidentified, and `0x21`
  **is** allow-listed. Do not walk this neighbourhood. **Corrected 2026-08-04:**
  an earlier revision called `0x20` a second `ERASE_PWR_CHAR` arm; per-handler
  attribution puts both strings under `0x12` alone.

Cross-check against the Post-Crash allow-list `{0x02, 0x03, 0x04, 0x08, 0x0D,
0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32}`: **every one of those twelve is
implemented** — the allow-list contains no dead values. `0x21` and `0x32`, the
two the command reference calls "allow-listed, carry no destructive log strings,
unaudited", are confirmed to carry no strings *inside their own function
extents* (108 and 313 bytes respectively) — so that description is accurate, and
they remain unaudited rather than cleared.

> **Method warning, recorded because it produced a wrong draft of this table.**
> Attributing strings by disassembling a fixed byte window from a handler's
> entry is unsound: most of these handlers are 30–120-byte coroutine stubs, and
> a 0x300-byte window silently absorbs the *next two functions'* strings. An
> earlier pass mislabelled `0xCA` `0x11` as a Multiplane Write and `0x20`/`0x21`
> as `ERASE_PWR_CHAR` for exactly this reason. Every attribution above is
> confined to the confirmed function extent from `function-map.json`.

---

## 4. `0xCC` — the `Admin_VUC_Sys_*` / device-configuration family — NEW

Reached at `0x7ffa7aed`; the opcode test is `bne a11,204` so **`0xCC` is the only
implemented opcode in `0xCB`–`0xD3`**. It then switches on `CDW12[7:0]`:

| cmd | extra gate | handler (runtime → static) | ovl | identity |
|---|---|---|---|---|
| `0x03` | — | `0x7ffbc138` → `0x30033eb0` | 24 | **Resize + `Admin_VUC_Device_Config_Modify_OVL024` ("DCMod")** ¹ — see §4.1 |
| `0x04` | `CDW12[15:8] == 1` | `0x7ffbc854` → `0x300345cc` | 24 | power/PAL limits (`PalLimit=%d, PalDampingRatio=%d, PalDuration=%d`) ¹ |
| `0x05` | `CDW12[15:8] ∈ {0,1}` | `0x7ffbed18` → `0x3002e510` | 12 | **UNKNOWN** — the entry is *inside* `0x3002e460`, not its own function; the neighbouring purge strings are not attributable |
| `0x06` | — | `0x7ffbc948` → `0x300346c0` | 24 | **UNKNOWN** — 164-byte stub, no strings in function or body |
| `0x09` | — | `0x7ffbc128` → `0x300349a0` | 25 | bad-block-list migration / backend hand-off (`MIGRATE_GBB2PLIST initSpare`, `AdminMgr -> BackendMgr`) ¹ |
| `0x0C` | — | `0x7ffbc368` → `0x30034be0` | 25 | power/temperature telemetry read (`SYSMGR_CMD_GET_THROTTLE_PN`, `PwrPc12V/PwrAtx12V/TempMain/TempInlet`, `GET_ENERGY_INFO`) ¹ |
| `0x0D` | — | `0x7ffbc58c` → `0x30034e04` | 25 | `VUC_SYS_SET_THERMAL_THROTTLING_PARAMS` |
| `0x0F` | — | `0x7ffbc934` → `0x300351ac` | 25 | `Admin_VUC_Sys_Set_Fw_Download_Psid_Validation_OVL025` |
| anything else | — | — | — | status `|= 0x40040000`, no side effect |

**`0xCC` is NOT on the Post-Crash allow-list.** Every arm is healthy-drive-only.

### 4.1 The Drive Configuration EEPROM section — answering "is the board config host-writable?"

The EEPROM section-name enum (StrIds 1214–1228, the same enum that names
`System Area` = 6, `PFail Crash Dump` = 10, `Crash Dump` = 11, `SBL` = 13)
begins:

| id | name |
|---|---|
| 0 | System Table-Of-Context |
| **1** | **Drive Configuration** |
| 2 | Slot |
| 3 | Manufacturer Bad Block list |
| 4 | Firmware image |
| 5 | UEFI BIOS |
| 6 | System Area |

**EEPROM section 1 is the drive/board configuration record**, and it *is*
host-writable — but only through `0xCC`, and only after a PSID unlock:

```
StrId 1700  ADM: Admin_VUC_Device_Config_Modify_OVL024 - Access Denied. Enable access by with PSID
StrId 1701  ADM: Admin_VUC_Device_Config_Modify_OVL024 - Access allowed, port Unlocked
StrId 1703  DCMod no config loaded
StrId 1704  DCMod changelist transfer size %d larger than buffer %d
StrId 1711  DCMod write to SPI failed
StrId 1712  DCMod SPI Schedule First Startup failed
```

DCMod takes a host-supplied **changelist** (offset/bytecount/data triples,
bounds-checked against a "reserved area offset"), writes it to the SPI EEPROM,
and can additionally **schedule a First Startup**. In PROC0 the section is only
ever *read* (`0x7ffab8d9`, verb 2, section 1) — the write is done by PROC8's
DCMod directly over SPI, not through PROC0's section API. **PROVEN** that the
section exists and is host-writable via `0xCC`; **UNKNOWN** what its individual
fields mean, because the changelist is opaque offset/value pairs and the field
map lives in a host-side tool, not in the firmware.

---

## 5. `0xD4` / `0xD7` — the diagnostics family — NEW

Both opcodes fall into the *same* sub-dispatch at `0x7ffa7123`, which switches on
`CDW12[7:0]`:

| cmd | handler | ovl | identity |
|---|---|---|---|
| `0x03` | `0x7ffa7a82`, resident enqueue | — | **`VUC DIAG_GPL`** (StrId 1810). Sub byte must be `0` or `1` |
| `0x04` | `0x7ffa7a51` → `call 0x7ffa3008` | resident | **UNKNOWN**; passes `ctx+0x4c` and `CDW14` |
| `0x05` | `0x7ffbc398` → `0x30032f90` | 21 | **FW slot erase / invalidate** ("FW Erase command: Failed to update FW Table") |
| `0x06`, `0x07` | `0x7ffbc518` → `0x30033110` | 21 | **`VUC BE_TMODE` + "Power Off type %d Message Sent to PCIe Mgr"** — see §5.1 |
| `0x08` | `0x7ffa7997`, resident | — | **LED Beacon** ("Port %d requested LED Beacon state to %d") |
| `0x09` | `0x7ffbc0d0` → `0x30032cc8` | 21 | `Admin_VucTriggerAsyncEvents` — "Trigger asynchronous events — PCI-Port: %x, Type: %x, Value: %x" ¹ |
| `0x30` | `0x7ffbc208` → `0x30032e00` | 21 | **UNKNOWN** — 71-byte stub; its body range covers the FW-slot-erase and power-off code, so attribution is ambiguous |
| `0x31` | `0x7ffa7951`, resident | — | requires `CDW13[15:0] != 0`; **UNKNOWN** |
| `0x32` | `0x7ffa793e`, resident, immediate | — | ☢ **PCIe error injection** — writes `CDW12[15:8]` to `*(0x7ff861cc + 8)` and logs "VUC PCIE ERROR injected error 0x%08x" |
| `0x40` | `0x7ffa7a64` | 17 or resident | branches on the sub byte (`1` / `0` / other → DIAG_GPL). **UNKNOWN** |

`0xD4`/`0xD7` are **not** on the Post-Crash allow-list.

### 5.1 `0xD4`/`0xD7` cmd `0x06`/`0x07` — a host-commandable orderly power-off

`0x30033110` is a 802-byte coroutine. On one of its arms it assembles a message
(`movi a12,212` = message id `0xD4`), fills a byte-wise header at `[a4+0x08..0x10]`,
posts it (`call 0x300318ec`) and logs:

```asm
300332d8: call8 0x300318ec
300332db: l32r a10,-> StrId 1843 "Power Off type %d Message Sent to PCIe Mgr\n"
300332de: l32i.n a11,a1,0xc
300332e0: call8 0x3002b1a0                 ; Log_Emit
```

**PROVEN** that a host `0xD4`/`0xD7` command reaches a "Power Off type *n*"
message to the PCIe manager. **UNKNOWN**: which `type` values exist, whether the
type is host-supplied, and — critically — whether this path runs the System Area
Manager save that writes a clean shutdown marker. See
`sn200-latch-prevention.md` §4 for why this is the most interesting unexplored
lead and why it is *not* currently actionable.

---

## 6. `0xEC` = `Admin_VUC_Enable` — identification only

`0xEC`'s handler `0x7ffbc24c` resolves to static `0x3002b6c4` (overlay 11,
`src2 = 0x3002b478`), a confirmed 101-byte `entry` function. At `0x3002b6e0` it
loads the log descriptor `0x07806001` — StrId **1920**, level `0x60`, 1 argument:

```
StrId 1919  ADM: Admin_VUC_Enable FAILED. Invalid input command parameters detected
StrId 1920  ADM: Admin_VUC_Enable SUCCESSFUL. New State: %u
```

and `0x3002b7b4` loads StrId 1919's descriptor. **`0xEC` is `Admin_VUC_Enable`
— the setter for the "VUC Control" state** that the second gate in
`Admin_CheckCmdAllowed` consults (`StrId 1805 "Admin cmd restricted by VUC
Control disabled: 0x%x"`, referenced from the dispatcher at `0x7ffa6d65`).
**PROVEN** by descriptor cross-reference.

Deeper analysis of `0xEC` — parameter encoding, what state values mean, whether
the state persists, and whether it can touch the Post-Crash gate — belongs to
the parallel `0xEC` / allow-list work and is deliberately not duplicated here.
It is flagged because it is the single most prevention-relevant vendor opcode
found in this sweep: it is on the Post-Crash allow-list, and it is a *mode
setter*.

---

## 7. The NVM (I/O) command set

**There is no second dispatcher.** The admin path above is entered from the
admin submission queue only; the NVM command set (`0x00` Flush, `0x01` Write,
`0x02` Read, `0x04` Write Uncorrectable, `0x05` Compare, `0x08` Write Zeroes,
`0x09` Dataset Management, …) is handled on the data-path processors
(PROC1–PROC7, PROC10–PROC15), not by PROC8's `Admin_NvmeCmdHandler`. This sweep
did not enumerate it: the I/O opcodes are spec-fixed, carry no vendor
extensions reachable from `nvme admin-passthru`, and are not part of the latch
question. **Stated as a scope limit, not as a finding.**

---

## 8. What this changes

1. **Six vendor opcodes were previously unknown and are now on the map:**
   `0xC8`, `0xC9`, `0xCC`, `0xD4`, `0xD7`, `0xD9`, `0xEF`. None of them is on
   the Post-Crash allow-list, so none of them widens the latched-drive attack
   surface — but `0xCC` and `0xD4` are exactly the kind of *healthy-drive*
   configuration surface that a prevention measure would have to live on.
2. **The unimplemented set is now proven, not assumed.** Security Send/Receive,
   Sanitize, Device Self-test, Keep Alive, Directive Send/Receive and NVMe-MI
   Send/Receive are all absent. Any runbook step or tool that assumes one of
   them exists will fail with "Invalid Command Opcode", not do something
   surprising.
3. **Two new one-nibble hazards** in the `0xCA` table (`0x38`/`0x39`,
   `0x3A`/`0x3B`), and a re-classification of `0xCA` sub `0x21` from
   "unaudited" to "suspected erase-capable" — and `0x21` **is** allow-listed on
   a latched drive.
4. **The overlay descriptor table's stride was wrong** in
   `sn200-c6-dispatch.md` §1 (12 bytes assumed; it is 32). Any address derived
   with the old stride for an overlay other than 18 is wrong. The corrected
   rule is §1.1 above and it reproduces five independently published results.
