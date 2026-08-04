# HGST/WDC Ultrastar SN200 (HUSMR7676BDP3Y1) firmware reverse-engineering notes

Target: 7.68 TB HHHL/SFF NVMe SSD, ASIC codename **Omaha**, firmware family `KNGND1xx`.
Goal: understand the "Post Crash Startup" lockup and find a way out without a chassis
cold power cycle.

Status: living document. Claims are labelled **PROVEN** (read directly out of a binary /
string table) or **INFERRED** (reasoned from structure) or **SPECULATIVE**.

Companion runbook: `.claude/skills/nvme-recovery/SKILL.md`.
Companion documents: `docs/sn200-crash-dump-retrieval.md` (getting the dump off a
latched drive) and `docs/sn200-nondestructive-recovery.md` (whether the latch can be
lifted without wiping the namespace).

---

## Summary

1. **Why the crash erase returns Success but does nothing.** Proven in the disassembly:
   the Crash Dump case is the *only* one of the eight that does extra work on success — it
   branches forward to `0x30033704` and issues a **second** operation whose failure logs
   `Schedule reinit after crash dump erase failed.` The PFail case has no second step,
   which is why only *it* clears immediately. So `0x0503` is a *scheduling* call: it arms
   the `Drive REINIT requested` boot marker and the erase happens at the next startup.
   **And that scheduled reinit is what zeroes the namespace — the recovery, not the crash,
   destroys the data.** (§4)
2. **Is there any host-side escape from Post Crash mode? No — power cycling is
   structural.** Exhaustive: **28** dispatch tables, **78** command ids, and a scan of
   every aligned `u64` in `.data`/`.data.rel.ro`/`.rodata` finds **zero** command ids
   outside them. `EXIT_MODE`/`SET_MODE`/`WRITE_MARKER` are orphans whose **handler
   functions are not in the binary**, and dispatch is a name-keyed hash built only from
   the class tables — so no unlock can add them. Both raw-passthru functions are
   unreferenced and unexported. Namespace re-attach is refused by the firmware itself
   (`The LBN Translation Table is invalid.`). No `Admin_Vuc*` writes a marker or changes
   mode. **The one untested avenue is NVMe-MI over SMBus** (PROC9), which survives a dead
   PCIe link; whether its admin tunnel passes opcode `0xFF` is **UNKNOWN**. (§10)
3. **What triggers the latch?** ⚠ **Revised after a second field latch.** There is one
   boot predicate — `SYS: Detected a CRASH or PFCRASH section.` → force startup marker 9
   (`POST CRASH Startup`), **PROVEN** at PROC0 `0x7ffaaf02`/`0x7ffaaf08`. **Either**
   section latches the drive, and it **overrides whatever the previous shutdown
   recorded**. Large deallocate/TRIM (WD's OM-6588/6836/6850/7044) arms the **CRASH**
   section; an unclean power-off or a link/power glitch arms the **PFCRASH** section. A
   deallocate is therefore *sufficient but not necessary* — latch #2 had none. (§6a, §8)
4. **Can the AEN be suppressed?** Not by masking — Persistent Internal Error is an
   Error-class AEN and the firmware's maskable list contains only the SMART warnings and
   the two notice bits. The `0x400b` on `set-feature 0x0b` is `Invalid Namespace or
   Format`, i.e. the missing namespace, not an unsupported feature. But the AEN can be
   **starved**: bind to `vfio-pci` and never submit an Asynchronous Event Request. (§9)
5. **Is the E6 retrieval a firmware precondition for the erase?** **No — tool-side only**,
   two ints in host memory (`HGSTNVMeController+0x40/+0x44`); the wire command is
   unconditional. The single reason `dm-cli` never clears is that the 6.7 MB E6 pull
   cannot finish inside the ~5 s window. (§11 — note an earlier "second silent reason"
   claim in this document has been **retracted**; it rested on misreading Identify offset
   `0x40` as Model Number when it is Firmware Revision.)

6. **Is the namespace suppressed or wiped?** **Suppressed** — Post Crash Startup is a
   quarantine posture, not a rebuild: type 6 *skips* the System Area step (`0x7ffb2518`)
   and no namespace-invalidate path is keyed to it. But that does **not** mean the data
   survived: the originating crash may have corrupted the L2P, `0x0503` schedules the
   rebuild, and while latched nothing on the media is observable at all. (§10a)

**The single most actionable consequence:** before clearing anything, probe *which* section
is armed (`0xC6 cdw12=0x0320` vs `0x0520`). `0x0603` (pfail) is synchronous and schedules
nothing; `0x0503` (crash) schedules the destructive re-init. A drive latched only by
PFCRASH may be recoverable **without a wipe**. Both sea1-hv-2 recoveries fired `0x0503`
blind, which forces the destructive path regardless. See "Actionable recovery options"
step 0.

Newly identified and not previously documented:
- The startup marker is `0x8000000N` and is **overridden at boot** by System-Area state —
  crash/pfcrash → 9, erased SA / incompatible SA / CellCare mismatch → 3 (§6a).
- The **`UNEXSTRT`** mechanism (§7) — every start not preceded by a recorded clean
  shutdown re-writes a stub crash header, which is why no in-band reset breaks the loop.
- The admin rejection status is **`0x7C5`** (SCT=7, SC=0xC5 = "diagnostic mode"), **not
  `0x7D3`**. `0x7D3` is the status on the *async-event error-log entry*, a different
  thing. (§10)
- The cores use **FLIX (VLIW) bundles** and **Ghidra's Xtensa module cannot decode them**
  — every decompilation from it on these images is garbage. (§1)
- The firmware **log-call ABI**: `descriptor = (StrId<<16) | (level<<8) | nargs`, loaded
  by `l32r` into a10 before `call8`. This makes any handler findable by its log
  message. (§1)
- The OAM erase switch has **seven sub-commands (0–6)**, and **sub 3 ≈ Erase SBL EEPROM**
  / **sub 4 ≈ Drive Uninit** sit directly below the two you want. (§4)

---

## 1. Artifact inventory and container formats

### Firmware package
`HGST-UltraStar-SN200-HHHL.zip` ships three images (all plain uncompressed **tar**):

| image | date | notes |
|---|---|---|
| `KNGND100.bin` | 2017-10-11 | 20 members |
| `KNGND110.bin` | 2018-06-26 | + `SBLPATCH.bin` |
| `KNGND122.bin` | 2020-09-17 | latest; analysed here |

Members: `FWHEADER.bin` (64 B, just the ASCII rev + a u32 = 1), `PROC0..PROC15.bin`,
`FCC.bin`, `StringTable.csv.gz`, `SECURITY.bin` (1600 B signature blob).

**PROVEN — `.BIN`/`.SEG` container.** Each `PROC*.bin` / `FCC.bin` starts with ASCII
`.BIN` padded to 0x10, then a chain of 16-byte segment headers:

```
struct seg { char magic[4]="​.SEG"; u32 file_offset_of_data; u32 data_len; u32 load_addr; };
/* next header sits at file_offset_of_data + data_len; chain ends with 0xffffffff */
```

Parser: `tools/sn200-fw/segparse.py`; `tools/sn200-fw/unpack.py` turns an image into flat
per-processor memory images in one step.

Segment layout (KNGND122):

| proc | segments (load → len) |
|---|---|
| PROC0 | 7ff80000/0x4bb4, 7ffa0000/0x1c4, 7ffa01d8/0xc8, 7ffa0400/0x15b20 |
| PROC1..7,9..15 | 7ff80000/vectors, 7ffa0000, 7ffa019c, 7ffa01bc, 7ffa01d8, 7ffa0400, **7ffa0710/big code** |
| PROC8 | additionally **0x30000000/0x40078** (tagged `OVB` = overlay bank) |
| FCC | 0x00100000, 0x00102c00 (1 KB of zeros = bss), 0x00120000, 0x00120100, 0x00120180/0x66c8, 0x00126d00 |

`0x7ff80000` is a table of 32-bit vectors pointing into `0x7ffa____`; `0x7ffa0710`+ is
the bulk of code + rodata.

### CPU architecture
**PROVEN — Tensilica Xtensa, little-endian, windowed ABI.** Evidence in PROC6's
0x7ffa0710 segment (128 KB):
- `retw.n` (`1d f0`) appears **1331** times — by far the dominant 2-byte pattern after
  `90 c0` and `00 00`.
- The 4-byte-aligned pattern `00 00 00 36` (261 hits) = `entry` opcode at function
  starts after alignment padding.
- No ARC (`push_s blink`/`pop_s blink`/`j_s [blink]` ≈ 1 hit each = noise), no
  ARM Thumb (`bx lr` = 0 hits), no MIPS.

Sixteen Xtensa cores + a separate `FCC` (Flash Channel Controller) core, connected by a
message-passing fabric (`StarMgr: MsgId, Qid, srcNode, thisNode, dstMgr, srcMgr, Handle,
cmd/rsp, dstQIdx`), matching the "Star" NoC naming throughout the string table.

#### ⚠ The cores use FLIX (VLIW), and Ghidra cannot decode them

**PROVEN — do not trust Ghidra on these images.** Ghidra 12.1.2 ships an `Xtensa` module
and will happily load them, but that module treats `op0 = 0xE` as a 3-byte `FLIX`
pseudo-op and then **desynchronises the entire instruction stream**. Decompiler output is
garbage (`FUN_7ffa6b18` → `flix(); halt_baddata();`).

Correct instruction-length model, validated statistically against `retw.n` and `entry`
anchors across both PROC8 images:

| model | `retw.n` landings, 0x30000000 image | 0x7ff80000 image |
|---|---|---|
| all-3-byte (what Ghidra does) | 61/194 (0.31) | 333/578 (0.58) |
| **op0 ∈ {0xE, 0xF} ⇒ 8-byte bundle** | **194/194 (1.000)** | **561/578 (0.971)** |

So: `op0` 0x8–0xD = 2-byte density instructions, other 0x0–0xD = 3-byte core,
**0xE/0xF = 8-byte FLIX bundle**.

FLIX field layout — **now fully recovered and implemented in `xdis.py`**. Both formats
are `[fmt:4][slot A:24][slot B…]`.

**Slot A (both formats) is an ordinary 24-bit core instruction with its `op0` field
relocated to bits 24–27.** Everything else stays where the core ISA puts it:

```
slotA = ((q>>24)&0xF) | (((q>>4)&0xF)<<4) | (((q>>8)&0xF)<<8)
                      | (((q>>12)&0xF)<<12) | (((q>>16)&0xFF)<<16)
        i.e.  op0@24-27, t@4-7, s@8-11, r@12-15, imm8@16-23
```

Verified on `movi`, `l32i`, `s32i`, `l8ui` and `l32r` (the last resolves through the log
ABI, which is a strong independent check).

**Format 0xF slot B is a conditional branch (36 bits):**

```
r@28-31   s@32-35   disp = SIGNED 18-bit @36-53   opcode k = bits 55-63
target = bundle_addr + 4 + disp
```

`k` is a 1-based index into the branch mnemonics sorted alphabetically — the 20 that carry
an `r` field first, then the four compare-against-zero forms:

```
1 ball  2 bany  3 bbc  4 bbci  5 bbs  6 bbsi  7 beq  8 beqi  9 bge  a bgei  b bgeu
c bgeui d blt  e blti  f bltu 10 bltui 11 bnall 12 bne 13 bnei 14 bnone
15 beqz 16 bgez 17 bltz 18 bnez
```

`r` is the `t` register for the register forms and a **B4CONST index** for the immediate
forms. B4CONST has no 9, which is exactly why the compiler emits `movi a9,9; beq a3,a9` —
and that pattern is what produced the phantom duplicate constants in the earlier,
superseded whitelist reading.

**Format 0xE slot B**, with `bits 46-47` selecting the class:

```
= 3  -> j, signed 18-bit disp @28-45
= 2, bits 40-45 == 0x00 -> movi a{28-31}, imm8@32-39
= 2, bits 40-45 == 0x23 -> mov  a{28-31}, a{36-39}
bits 48-63 = a third 16-bit slot; 0xC090 is its nop
```

> Superseded: an earlier pass described the branch displacement as **12 bits at 36–47**.
> It is **18 bits at 36–53**; bits 48–53 are the sign extension, which is why the 12-bit
> model appeared to work on forward branches and failed on backward ones. The
> "~20-bit field at bits 28–47 for unconditional jumps" was likewise the format-0xE `j`
> at 28–45.

A working hand-rolled disassembler and a log cross-referencer live in
**`tools/sn200-fw/`**. `capstone` 5.0.9 has no Xtensa support and capstone 6 is not on
PyPI, so hand decoding was the only route.

```sh
python3 tools/sn200-fw/unpack.py KNGND122.bin ~/sn200fw     # tar + flat memory images
export SN200_FW=~/sn200fw
python3 tools/sn200-fw/drv.py 300336c6 300336f5             # disassemble a range
python3 tools/sn200-fw/logmap3.py 2933                      # find code by StrId
python3 tools/sn200-fw/logmap3.py 'Post Crash'              # ...or by regex on the text
```

`drv.py` annotates every `l32r` with the literal it loads, decoding log descriptors
inline; for FLIX bundles it prints the raw 64 bits plus the candidate `s0l32r=` and
`b12=` targets.

### The log-call ABI — **PROVEN**

StrIds are neither `movi` immediates nor bare 32-bit literals, which is why naive scans
find nothing. They are packed into a 32-bit **log descriptor** in the `l32r` literal pool:

```
descriptor u32 = (StrId << 16) | (level << 8) | nargs
```

- `level` measured over all 18 images, counting only descriptors an `l32r` genuinely loads:
  **`0x60` ×1782** (admin/main code), **`0x20` ×398**, **`0x40` ×397** (overlay/OAM code),
  `0x00` ×372, everything else ≤13 and noise.
  **`0x20` is the ASSERT/FATAL level** — see §12.
  **Exclude `0x00` when scanning**: a descriptor with level 0 and nargs 0 is just a small
  aligned integer, and admitting it drops nargs-vs-format agreement from 99.87% to 98.51%
  with every added mismatch being a level-0 hit.
- `nargs` = printf vararg count, and **the emitter masks it to 4 bits** (`extui a9,a9,0,4`),
  so the maximum is 15 — which is exactly the widest format string in the table
  (StrId 3189/3190, `Outstanding Trim …`).

**Cross-validated**: across all 18 images, `nargs` agrees with the printf conversion count
of the string its StrId resolves to for **1584 of 1586** referenced descriptors (99.87%).
Regression test: `tools/sn200-fw/tests/test_strtab.py::test_nargs_matches_format`.

Call convention: `l32r a10, <descriptor>` then `call8 <logfn>`, extra args in a11, a12, …

⚠ **An *emitted* record's descriptor has bit 31 set** (`descriptor | 0x80000000`); the
literal in the constant pool does not. Reusing the firmware-image decode on dump data
therefore yields StrId `0x8000|N` and finds nothing. See §12.

Log functions: **`0x7ffb45a8`** (main image), **`0x3002b8e0`** (overlay bank).

This is the key that makes "find any handler by its log message" mechanical.

### String table
**PROVEN.** `StringTable.csv.gz` → 195 545 bytes, 3617 lines for KNGND122.
Line 1 is a header: `VERSION=1 NUMRSVD=16 FWREV=KNGND122 HASHVAL=0xa1e928ab
### Omaha StringTable (0x85c41d83) ###`.
Lines 2..16 are `# StrId N reserved`. **`StrId N` == CSV line `N+1`.**
So `StrId 16` = line 17 = `EEPROM: Warning. Failed to set GPIO...`.

The firmware logs by StrId, not by pointer — the printf-style format strings are *not*
in the PROC images at all (only ~1650 short literal strings are, mostly a debug CLI).

---

## 2. Boot-marker state machine (the heart of the problem)

**PROVEN — the startup-marker enum.** StringTable lines 3030–3040 are a contiguous
string array used by `SYS: %s` at boot:

| idx (INFERRED, order) | StrId | text |
|---|---|---|
| 0 | 3029 | `No previous marker found` |
| 1 | 3030 | `CLEAN shutdown` |
| 2 | 3031 | `PFAIL shutdown` |
| 3 | 3032 | **`Drive REINIT requested`** |
| 4 | 3033 | **`FACTORY drive REINIT requested`** |
| 5 | 3034 | `Normal Shutdown STARTED` |
| 6 | 3035 | `PFAIL Shutdown STARTED` |
| 7 | 3036 | `PFAIL Shutdown TIMEOUT` |
| 8 | 3037 | `READONLY Startup requested` |
| 9 | 3038 | **`POST CRASH Startup`** |
| 10 | 3039 | `Invalid marker` |

and a parallel human-readable set at StrIds 1264–1274:
`SYS: Normal startup` / `SYS: PFAIL startup` / `SYS: Drive re-init` /
`SYS: Drive re-init to factory defaults` / `SYS: First time startup` /
`SYS: ERROR - Shutdown started but never finished` /
`SYS: ERROR - PFAIL started but never finished` /
`SYS: ERROR - PFAIL started but timed out before completing` /
`SYS: Read-only startup` / **`SYS: Post Crash startup`** /
`SYS: Bad startup marker (%08X)`.

**INFERRED — the marker enum and the `SYS: %s` startup-reason enum are 1:1 parallel**, with
the reason array (StrIds 1264–1273, with 1274 `SYS: Bad startup marker (%08X)` as the
out-of-range case) indexed by a distinct *startup type*:

| marker | → startup type | `SYS:` text |
|---|---|---|
| 1 CLEAN shutdown | 0 | Normal startup |
| 2 PFAIL shutdown | 1 | PFAIL startup |
| 3 Drive REINIT requested | 2 | Drive re-init |
| 4 FACTORY drive REINIT | 3 | Drive re-init to factory defaults |
| 0 No previous marker | 4 | First time startup |
| 5 Normal Shutdown STARTED | 5 | ERROR - Shutdown started but never finished |
| 6 PFAIL Shutdown STARTED | 6 | ERROR - PFAIL started but never finished |
| 7 PFAIL Shutdown TIMEOUT | 7 | ERROR - PFAIL started but timed out |
| 8 READONLY Startup requested | 8 | Read-only startup |
| 9 POST CRASH Startup | 9 | Post Crash startup |

**RESOLVED — the VUC's value is a different enum, and it is a real global.** The
`0xFF/0x0004` VUC returns a *startup type* in CDW0 byte 1, read from the PROC8 global
**`0x7ff87c64`** (literal `0x7ffa09b0`). **`6` is Post Crash / diagnostic mode** — it is
the only constant the firmware ever compares that global against, at
`0x7ffa98a6`, `0x7ffa9aaa`, `0x7ffac9aa`, `0x7ffb2518`, plus the admin gate `0x7ffa6b1b`.
`0` is First Startup (`0x7ffac7d9: bnez a9` → else log StrId 1550 `Admin: First Startup`).
`libdmi_core`'s `gf_is_diagnostic_mode_trace` @ `0x42d10` tests `startup_type == 6`.

So this enum is **not** the `SYS:` startup-reason array (where Post Crash is index 9). Do
not index one with the other. The `== 7` variant in `omc_resolve_device_status` applies
only to Hitachi-branded firmware (`FR[0]=='H'`), not to `KNGND1xx` — see §11.

**PROVEN — the crash-section detector.** StrId 3042: `SYS: Detected a CRASH or PFCRASH
section.` sits immediately after `SYS: Found an incompatible SA` and
`SYS: Detected an erased SysArea.`, and immediately before
`SYS: Load-n-go boot override of failed shutdown.`

**PROVEN — the per-section state reports** (StrIds 1277–1282):

```
SYS: Crash Dump section is erased
SYS: Crash Dump is detected
SYS: Crash Dump section is in invalid state
SYS: PFail Crash Dump section is erased
SYS: PFail Crash Dump is detected
SYS: PFail Crash Dump section is in invalid state
```

So each of the two SPI/EEPROM dump sections has **three** states:
`erased` / `detected` / `invalid`. This trichotomy is the crux — see §4.

**PROVEN — the EEPROM section-name array** (StrIds 1214–1228, contiguous):

| idx (INFERRED) | name |
|---|---|
| 0 | System Table-Of-Context |
| 1 | Drive Configuration |
| 2 | Slot |
| 3 | Manufacturer Bad Block list |
| 4 | Firmware image |
| 5 | UEFI BIOS |
| 6 | System Area |
| 7 | BIST Log |
| 8 | BIST Status |
| 9 | BIST Script |
| 10 | **PFail Crash Dump** |
| 11 | **Crash Dump** |
| 12 | TCG |
| 13 | SBL |
| 14 | Invalid |

and the EEPROM operation array (StrIds 1228–1235):
`Invalid, Read, Read FW Header, Write, Erase, Update, Copy, Check`.
Used by `EEPROM: %s. Section %s (%d)` and `EEPROM: Section %s (%d) - %s (%d)`.

Note the **PFail Crash Dump comes before Crash Dump** in the EEPROM section enum, which
is the *reverse* of the VUC subcommand order (sub 5 = crash, sub 6 = pfail).

---

## 3. The OAM erase command family

**PROVEN — the OAM command and erase failure strings**, in this contiguous order:

```
1626  OAM CMD: Received Unsupported Command 0x%08x.
1627  OAM CMD: Unepxected reach end of Admin_OamCmd function.
1628  OAM ERASE CMD: Erase to system area 0 failed.
1629  OAM ERASE CMD: Erase to bad block table 0 failed.
1630  OAM ERASE CMD: Erase to BIST Script failed.
1631  OAM ERASE CMD: Erase to BIST Status failed.
1632  OAM ERASE CMD: Erase to SBL EEPROM failed.
1633  OAM ERASE CMD: Drive Uninit failed.
1634  OAM ERASE CMD: Erase to Crash Dump failed.
1635  OAM ERASE CMD: Erase to PFail Crash Dump failed.
1636  OAM ERASE CMD: Received Bad Erase sub-cmd: %d.
```

The existence of `Received Bad Erase sub-cmd: %d.` proves the erase target is selected by
a numeric sub-command with a validated range. The mapping of numbers to targets is
discussed in §4 — it is **not** safely derivable from string order alone.

**PROVEN — the "schedule reinit" string.** StrId 2933:
`OAM ERASE CMD: Schedule reinit after crash dump erase failed.`

Read together with the boot-marker enum (§2), this means: after the crash-dump erase the
OAM handler **writes the `Drive REINIT requested` startup marker** so the *next* boot
comes up in re-init rather than Post-Crash mode. The message is logged when that marker
write fails. **INFERRED but strongly supported**: the erase path is
`erase section → set REINIT marker → (host must reset/power-cycle) → normal boot`.
i.e. the erase is **not** self-completing; a controller restart is part of the design.

---

## 4. Why the crash erase "succeeds" but does nothing — and what it really does

### The answer — now **PROVEN** in the disassembly

The OAM erase handler is at **`0x3003353c`** in PROC8's overlay bank (`entry a1,0x30`,
extends to `0x30033821`; literal pool `0x3003334c..0x30033440`). The
`OAM READ RAW SA CMD` handler is at **`0x30033824`**. The whole `0x30033xxx` block is the
`Admin_VUC_*` overlay family.

**The Crash Dump path is the only one that does extra work on success:**

```asm
300335ca: l32i   a11,[a12+0x188]
300335cd: beqz   a11, 0x30033704      ; success jumps FORWARD, not to the common exit
300335d0: l32r   a10, <LOG 1634 "Erase to Crash Dump failed">
...
30033704: l32r   a14, 0x30033350      ; -> global 0x7ff87c64
30033707: l32i.n a14, a14, 0
30033709: <FLIX x3>
30033721: call8  0x30030aa0           ; SECOND operation
30033724: <FLIX>
          ; falls through to a status check whose failure logs StrId 2933
          ; "OAM ERASE CMD: Schedule reinit after crash dump erase failed."
```

So: **erase crash dump → on success perform a second operation ("schedule reinit") → if
*that* fails, log 2933.** The PFail path (check block `0x300335a3`) has **no** second
step. That is the asymmetry, and it confirms the model below.

The eight status-check blocks, each `l32i aX,[a12+0x188]; beqz aX,<ok>; l32r a10,<LOG>;
call8 0x3002b8e0; j 0x3003357d` (**PROVEN**):

| addr | StrId | message | success → |
|---|---|---|---|
| `0x30033571` | 1628 | Erase to system area 0 failed | `0x300335bf` (common) |
| `0x300335a3` | 1635 | Erase to PFail Crash Dump failed | `0x300335bf` |
| `0x300335b1` | 2933 | **Schedule reinit after crash dump erase failed** | `0x300335bf` |
| `0x300335ca` | 1634 | Erase to Crash Dump failed | **`0x30033704`** |
| `0x300335d9` | 1633 | Drive Uninit failed | `0x300335bf` |
| `0x300335e8` | 1632 | Erase to SBL EEPROM failed | `0x300335bf` |
| `0x30033634` | 1631 | Erase to BIST Status failed | `0x300335bf` |
| `0x30033643` | 1630 | Erase to BIST Script failed | **`0x3003372c`** (chains to a 2nd erase) |
| `0x30033652` | 1629 | Erase to bad block table 0 failed | `0x300335bf` |

**Still not established:** whether the completion status is taken from the erase result or
forced to Success. The common tail at `0x3003357d` ORs a constant
(`a9 = 0x7ffbc221`, from pool `0x30033368`) into the completion status word, and the
success and failure paths converge there — *consistent with* "returns Success regardless",
but not proven.

### Why it looks like a no-op

**INFERRED, high confidence.** The two erases are not the same kind of operation:

- **PFail Crash Dump erase (`CDW12 0x0603`) is synchronous.** It erases the SPI section
  there and then; the size probe drops to zero immediately, while the drive is still
  reset-looping. This matches the field observation exactly.
- **Crash Dump erase (`CDW12 0x0503`) is asynchronous — it is a *scheduling* call.**
  The handler does not erase the section inline. It sets the `Drive REINIT requested`
  startup marker (§2) so that the *next* startup performs the erase as part of a drive
  re-initialisation. The only thing that can fail synchronously is the marker write,
  which is precisely what StrId 2933 reports:

  ```
  OAM ERASE CMD: Schedule reinit after crash dump erase failed.
  ```

  Hence: `Success` returned, size probe unchanged at `0x00320000`, and the section only
  actually goes away once a real startup runs.

`0x00320000` is a fixed 3.27 MB section reservation, not a live dump length — it does not
count down and is not a progress indicator.

### The consequence nobody documents: **the crash-dump erase destroys user data**

**INFERRED, high confidence — and corroborated by the 2026-08-03 field outcome.**

Chain of evidence:
1. The crash-dump erase path schedules a **reinit** (StrId 2933, PROVEN).
2. The startup-marker enum has `Drive REINIT requested` (3) and `FACTORY drive REINIT
   requested` (4) (PROVEN), whose startup handlers are `SYS: Drive re-init` and
   `SYS: Drive re-init to factory defaults` (PROVEN).
3. The OAM erase family also contains a sub-command literally named **`Drive Uninit`**
   (PROVEN, StrId 1633 `OAM ERASE CMD: Drive Uninit failed.`), in the same switch as the
   two crash-dump erases — re-initialisation is a first-class operation of this command.
4. On sea1-hv-2 the drive came back **healthy at full capacity with a completely zeroed
   namespace** — no GPT, zeros at every offset sampled to 3 TB — while `nuse == nsze`.

A "drive re-init" rebuilds the system area and the L2P mapping from scratch. That is
exactly what produces a fully-provisioned, entirely-zero namespace on a drive with no
media damage. **The data was not lost by the original crash; it was lost by the
recovery.** The erase-then-power-cycle procedure *is* a wipe, dressed up as a dump erase.

If a future SN200 is latched and the data matters, this is the key decision point — see
"Actionable recovery options".

### Sub-command numbering: 5 and 6 are confirmed, the rest is unknown and probing is dangerous

The OAM erase failure strings are contiguous in this order (**PROVEN**, StrIds 1628–1636):

```
1628 Erase to system area 0 failed
1629 Erase to bad block table 0 failed
1630 Erase to BIST Script failed
1631 Erase to BIST Status failed
1632 Erase to SBL EEPROM failed
1633 Drive Uninit failed
1634 Erase to Crash Dump failed
1635 Erase to PFail Crash Dump failed
1636 Received Bad Erase sub-cmd: %d.
```

If that order were the sub-command order starting at 0, then crash = 6 and pfail = 7 —
**off by one from the values that are known to work** (5 and 6).

**Resolved: there are seven sub-commands (0–6) but eight failure messages.** The switch is
at `0x300336c6` (**PROVEN**):

```asm
300336c6: l8ui a11, a12, 0x8d      ; a11 = erase sub-command
300336c9: beqz a11,      0x30033772   ; sub 0
300336cc: beqi a11, 1,   0x30033795   ; sub 1
300336d4: beqi a11, 2,   0x300337b8   ; sub 2
300336dc: beqi a11, 3,   0x30033661   ; sub 3  <- DIFFERENT shape, different primitive
300336df: beqi a11, 4,   0x300337db   ; sub 4
300336e7: beqi a11, 5,   0x300337fe   ; sub 5
300336ef: beqi a11, 6,   0x3003374f   ; sub 6
300336f2: l32r a10, <LOG 1636 "Received Bad Erase sub-cmd: %d">
```

Cases 0, 1, 2, 4, 5, 6 are structurally identical 0x23-byte blocks (3 FLIX bundles of arg
setup + `call8 0x30030aa0` — the erase primitive + 1 FLIX bundle). Case 3 is different: it
calls `0x30031d10` twice, a *second* erase primitive (EEPROM rather than flash).

**INFERRED (moderate confidence) — the mapping.** From the string order, the fact that one
sub-command must cover both BIST Script *and* BIST Status (the `0x30033643` → `0x3003372c`
chain), and that sub 3 uses the EEPROM primitive:

| sub | CDW12 | target |
|---|---|---|
| 0 | 0x0003 | system area 0 |
| 1 | 0x0103 | bad block table 0 |
| 2 | 0x0203 | BIST Script **+** BIST Status (chained) |
| 3 | 0x0303 | ☠ **SBL EEPROM** — *different primitive; erasing this bricks the drive* |
| 4 | 0x0403 | ☠ **Drive Uninit** |
| **5** | **0x0503** | **Crash Dump** ✔ agrees with the field result |
| **6** | **0x0603** | **PFail Crash Dump** ✔ agrees with the field result |

This agrees with `libdmi_core` (`gf_nvme_clear_crash_dump` = cmd 3 / sub 5,
`gf_nvme_clear_pfail_dump` = sub 6) and with the observed behaviour.
**It is *not* the case that Drive Uninit is sub 5.**

*Caveat:* the mapping is inferred from ordering, not from a decoded control-flow edge —
each case body's trailing FLIX bundle decoded to a constant target for all bodies, so
either they converge on a shared post-erase dispatcher or the 20-bit jump field reading is
wrong. Each body does `l32r` a distinct pointer immediately after its erase call
(`sub0→0x7ffbc239`, `sub1→0x7ffbc31a`, `sub2→0x7ffbc30b`, `sub4→0x7ffbc2a1`,
`sub5→0x7ffbc292`, `sub6→0x7ffbc26b`, plus `0x30033709→0x7ffbc279`,
`0x3003372c→0x7ffbc2fc`). **Those addresses lie outside every PROC8 segment** (PROC8 ends
at `0x7ffbb064`) — the same `0x7ffbc1xx–0x7ffbe6xx` range that appears as "handler
pointers" in the E6 descriptor table. Resolving that region would settle the mapping
definitively; it is the main remaining blocker.

For contrast, the SN150 (`KMGNP131`) string table has a *longer, differently ordered*
variant of the same block (`system area 0/1`, `bad block table 0/1`, …), confirming these
strings are ordered by source appearance across two generations, not by case value.

> **SAFETY.** Do **not** sweep the `cmd 3` sub-command space looking for other functions.
> Adjacent sub-commands include **`Erase to SBL EEPROM`** (erases the secondary boot
> loader — a hard brick) and **`Drive Uninit`**. Only use sub-commands that are known-good
> (5 and 6) or that have been positively identified in `libdmi_core.so`.

### Alternative reading (kept, lower confidence)

**SPECULATIVE.** Each dump section has three states, not two — `erased` / `detected` /
`invalid` (PROVEN, StrIds 1277–1282), plus `SPI Crash Section is in an invalid state`
(1607) and `SPI PFail Crash Sections is in an invalid state` (1609). It is possible the
crash section is *invalid* rather than *valid*, the erase path finds no valid dump header
and returns success without doing work, while boot detection (`SYS: Detected a CRASH or
PFCRASH section.`) uses a looser predicate that still trips. This would produce the same
observable symptom. The scheduling explanation above is preferred because it is directly
supported by StrId 2933.

---

## 5. Startup and shutdown type enums

**PROVEN** — two more contiguous string arrays (StrIds 303–314):

```
startup types:   FIRST STARTUP | NORMAL STARTUP | RECOVERY STARTUP | READ ONLY STARTUP |
                 FIRMWARE UPDATE STARTUP | FAST STARTUP | INVALID
shutdown types:  NORMAL SHUTDOWN | FAST SHUTDOWN | POWER FAIL | LOGIC TRAP | RESTART
```

Two entries matter:
- **`READ ONLY STARTUP`** (paired with marker 8 `READONLY Startup requested` and handler
  `SYS: Read-only startup`) — a startup mode that brings the drive up without writing.
  **SPECULATIVE but worth pursuing:** if the startup marker could be set to 8 instead of
  3, the drive would plausibly come up with its L2P intact and the namespace readable,
  *without* the data-destroying reinit. No VUC to set this marker has been identified yet.
- **`FAST STARTUP` / `RESTART`** — see next section.

## 6. FAST_RESTART — an in-band substitute for the cold power cycle

**PROVEN — the strings:**

```
StrId 688   PCIe_NvmeEnable: Issue fast restart to sysmgr. csts 0x%x
StrId 1200  Waiting for CC.EN (FAST_RESTART) from PcieMgr
StrId 1201  Received FAST_RESTART request from PcieMgr
StrId 1202  SYS: StartupReq FAST -> [%d]
StrId 1203  SYS: StartupCpl from [%d] FAST
StrId 1204  SYS: Inited FAST
StrId 2682  BlockMgr: Fast-Restart must be preceded by Shutdown!
StrId 3206  BlockMgr: Fast-Restart should be preceded by Shutdown! %d
```

**INFERRED.** A host write of `CC.EN` (0→1) makes `PcieMgr` request a **FAST_RESTART** from
the system manager — an internal restart with no power cycle. It is **only valid if a
proper shutdown preceded it**; otherwise BlockMgr complains and (per §7) the boot is
classified `UNEXSTRT`.

This explains cleanly why the reset ladder fails. `nvme reset`, NSSR, FLR, SBR and
link-disable all drop `CC.EN` or the link **without** first issuing `CC.SHN`, so every one
of them is an "unexpected start": each re-arms the crash section rather than clearing it.

**Important caveat, and why this is *not* being sold as the fix.** `echo 1 > .../remove`
*does* go through `nvme_remove()` → `nvme_dev_disable(dev, shutdown=true)` →
`CC.SHN=01b` + poll `CSTS.SHST`, i.e. it **is** a graceful shutdown, and the
`nvme-recovery` skill records that `remove` + `rescan` was tried against a latched drive
and did not help. Two readings, both consistent:

1. `FAST STARTUP` is a *lighter* startup path than a cold boot (it is a distinct entry in
   the startup-type enum, §5). The scheduled `Drive REINIT requested` marker is
   plausibly only consumed by a **full** startup, which is why only a true power cycle
   ever completes the erase. **INFERRED — this is the more likely reading and it is
   consistent with all field evidence.**
2. The earlier `remove`/`rescan` attempts may not have been made *after* firing the
   `0x0503` scheduling VUC, in which case there was no pending reinit to run.

So the durable, high-confidence conclusion here is the **diagnosis**, not a cure.

> **⚠ Superseded in part by §6a.** `CC.SHN` is **necessary but not sufficient**. The CLEAN
> marker is written by the System Area writer (PROC6 `0x7ffbba61`) only when the SA/L2P
> save *completes* — not by the NVMe shutdown handler, which writes only marker 5
> (`Normal Shutdown STARTED`). A shutdown that returns while the flush is still in flight
> leaves marker 5, and markers 5/6/7 all land in the same "never finished" handler.
> In Post Crash Startup the System Area may not even be loaded, so the flush that would
> write CLEAN plausibly cannot run at all. Treat unbind→bind as a cheap experiment, not
> as a fix.

## 6a. The startup-marker machinery, traced in code (PROC0)

*Added after the second field latch. This section supersedes guesswork in §2.*

### Marker representation — **PROVEN**

Startup markers are the constants **`0x8000000N`**, where `N` is the index into the marker
enum of §2. All ten exist as literals in PROC0's pool:

| marker | value | literal | meaning |
|---|---|---|---|
| 0 | `0x80000000` | `0x7ff825a0` | No previous marker found |
| 1 | `0x80000001` | `0x7ff83470` | CLEAN shutdown |
| 2 | `0x80000002` | `0x7ff83218` | PFAIL shutdown |
| 3 | `0x80000003` | `0x7ff82b50` | Drive REINIT requested |
| 4 | `0x80000004` | `0x7ff82b4c` | FACTORY drive REINIT requested |
| 5 | `0x80000005` | `0x7ff83230` | Normal Shutdown STARTED |
| 6 | `0x80000006` | `0x7ff830ec` | PFAIL Shutdown STARTED |
| 7 | `0x80000007` | `0x7ff830f4` | PFAIL Shutdown TIMEOUT |
| 8 | `0x80000008` | `0x7ff83478` | READONLY Startup requested |
| 9 | `0x80000009` | `0x7ff83474` | POST CRASH Startup |

(PROC12 `0x7ffa7d70..0x7ffa801c` loads all ten in sequence — that is the marker→string
lookup, not a decision point. Ignore it.)

### The evaluator — **PROVEN**, PROC0 `0x7ffaad80..0x7ffaaf3e`

Two-slot marker state, base register `a7`: **`[a7+0]` is the effective marker**,
**`[a7+0xf4]` is a pending/override slot**. At `0x7ffaae1e`:

```asm
7ffaae1e: l32i   a11, a7, 0xf4
7ffaae21: s32i.n a11, a7, 0x0      ; pending -> effective
```

The dispatch chain is `0x7ffaae69..0x7ffaaed3`: it loads marker 1, 4, 5, 6, 7, 9, 8 in
turn and compares, falling through at `0x7ffaaede` to
`SYS: Bad startup marker (%08X)` (StrId 1274).

### ⚠ The marker written at shutdown is **OVERRIDDEN at boot** — this is the crux

**PROVEN.** Four sibling blocks in the same function each log a System-Area condition and
then immediately load a marker constant to store:

| addr | logged condition | StrId | marker loaded | evidence |
|---|---|---|---|---|
| `0x7ffaadaa` → `0x7ffaadb0` | `SYS: Found an incompatible SA` | 3040 | **3** REINIT | store is in the FLIX at `0x7ffaadb3` |
| `0x7ffaae5e` → `0x7ffaae64` | `SYS: Detected CellCare mismatch` | 1263 | **3** REINIT | **explicit `s32i.n a11,a7,0x0` at `0x7ffaae67`** |
| `0x7ffaaef1` → `0x7ffaaef7` | `SYS: Detected an erased SysArea.` | 3041 | **3** REINIT | store is in the FLIX at `0x7ffaaefa` |
| `0x7ffaaf02` → `0x7ffaaf08` | **`SYS: Detected a CRASH or PFCRASH section.`** | 3042 | **9 POST CRASH** | store is in the FLIX at `0x7ffaaf0b` |

The CellCare block proves the *shape* (`l32r <marker>` immediately followed by
`s32i.n a11,a7,0x0`); the other three are byte-for-byte the same shape with the store
inside an undecoded FLIX bundle. **INFERRED, very high confidence** that all four store.

**Consequence — and this is the answer to the contradiction:**

> The presence of a crash section forces `POST CRASH Startup` **regardless of what the
> previous shutdown recorded**. A clean shutdown does not protect you. And the predicate is
> a *single* test covering **both** sections — the firmware's own wording is
> "a CRASH **or** PFCRASH section".
>
> **Therefore the PFCRASH (pfail) section alone is sufficient to latch the drive into
> Post Crash Startup. No deallocate is required. No crash dump is required.**

`SYS: ERROR - Previous shutdown failed to save System Area` (StrId 1262) is gated
separately at `0x7ffaae13` (`l32i.n a10,a14,0x4; beqi a10,4,skip`) — a state field must
equal 4 for the previous shutdown to count as having saved the System Area. So "shut down
cleanly" and "saved the System Area" are **two different conditions**.

### ⚠ How a CLEAN shutdown is actually recorded — **PROVEN, and it corrects §6**

The CLEAN marker has **exactly one producer in the whole firmware**, and it is the System
Area writer (PROC6 = SAM/BlockMgr), *not* the NVMe shutdown handler:

```asm
7ffbba52: l32i   a8, a2, 0x68        ; shutdown type
7ffbba55: l32r   a13, 0x7ffa2280     ; = 0x80000002  PFAIL shutdown
7ffbba58: l32r   a14, 0x7ffa2278     ; -> 0x7ff8bbd0 System Area header
7ffbba5b: addi   a8, a8, -2
7ffbba5e: moveqz a13, a15, a8        ; a13 = (type == 2) ? 0x80000001 : 0x80000002
7ffbba61: s32i.n a13, a14, 0x3c      ; <-- the persisted SysAreaMarker
```

`a15` was loaded with `0x80000001` (CLEAN) by the FLIX at `0x7ffbba48`. So **markers 1
(CLEAN) and 2 (PFAIL shutdown) are written by the same instruction**, and both mean the
same thing: *the System Area save completed*. They differ only in which shutdown type got
there.

Meanwhile PROC0's shutdown state machine (`0x7ffa8c58..0x7ffa8e60`) writes only
**marker 5 = `Normal Shutdown STARTED`** (`0x7ffa8dca` loads `0x7ff83230`, `call8
0x7ffb4fec` at `0x7ffa8dda`). SAM overwrites 5 with 1 only once the save finishes — which
is exactly what "started but never finished" means.

> **Correction to §6.** `CC.SHN` + `CSTS.SHST=10b` is **necessary but NOT sufficient** for
> a clean boot. The drive must also complete the System Area / L2P flush. An NVMe shutdown
> that returns promptly while the flush is still in flight leaves marker 5, which the boot
> path reports as `SYS: ERROR - Shutdown started but never finished`.
>
> Markers **5, 6 and 7 all dispatch to the same handler body at `0x7ffaaf6b`.**

WD corroborates that every graceful shutdown arms the PFAIL monitor: *"When a shutdown is
issued, internally the firmware will invoke a thread to monitor PFAIL (power fail) during
shutdown."* (KNGND122 release notes.)

### Marker → handler dispatch — **PROVEN** (b12 branch targets)

| marker | handler |
|---|---|
| 1 CLEAN shutdown | `0x7ffaaf85` |
| 2 PFAIL shutdown | `0x7ffaaf8d` |
| 3 Drive REINIT requested | `0x7ffaaf63` |
| 4 FACTORY REINIT | `0x7ffaafc0` |
| **5 / 6 / 7** | **all → `0x7ffaaf6b`** (shared "never finished / timed out" body) |
| 8 READONLY Startup | `0x7ffaaff5` |
| 9 POST CRASH Startup | `0x7ffabd01` |
| else | `0x7ffaaede` → `SYS: Bad startup marker (%08X)` + `break.n` |

### Two more overrides nobody had considered — **PROVEN**

```asm
7ffaae45: l32i.n a11, a7, 0x0
7ffaae47: bne    a11, a6, 0x7ffaae69
7ffaae4a: l32r   a10, <LOG 3519 "SYS: Unexpected empty System Area.">
7ffaae50: j      0x7ffaaf08          ; -> FORCED to marker 9, POST CRASH
```

**An empty System Area alone produces Post Crash Startup**, with no crash section, no
power event and no link event. And a CellCare mismatch (`0x7ffaae5e`→`0x7ffaae64`→
`s32i.n a11,a7,0x0`) **silently arms marker 3 — the data-destroying REINIT** — on its own.

Note also: `SYS: PFAIL time = ...` (StrId 1257) and `SYS: PFAIL power = ... mW` (1258)
have **no reference in any image** — compiled out. Do not expect them in a dump.
`Unable to get context to submit breadcrumbs for PFAIL shutdown` (StrId 3580) is **not**
in the PFAIL path at all; it lives in `PCIe_PfailShutdown` (PROC9 `0x7ffaed23`) — another
case where string adjacency would have misled.

### Operational consequence: probe *which* section is armed before clearing

The two clears are not equivalent (§4): `0x0603` (pfail) is synchronous and schedules
nothing; `0x0503` (crash) schedules the destructive re-init. If a latch was caused by the
**pfail** section only, then clearing pfail alone should clear the boot predicate — with
**no re-init and no wipe**.

```sh
nvme admin-passthru /dev/nvmeX --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b | od -A d -t x4  # CRASH
nvme admin-passthru /dev/nvmeX --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0520 --data-len=8 -r -b | od -A d -t x4  # PFCRASH
```

**This is the single highest-value measurement available on a latched drive**, and it was
not taken before either recovery. See "Actionable recovery options".

## 6b. REFUTED: "startup type 6 == boot marker 6 == PFAIL Shutdown STARTED"

A hypothesis was raised that the `0xFF/CDW12 0x0004` probe's `byte[1] = 0x06` is an index
into the **boot-marker** name table (where index 6 is `PFAIL Shutdown STARTED`), which
would mean the drive is reporting an interrupted power-fail rather than "diagnostic mode".

**REFUTED from code. PROVEN.** Three independent lines:

**1. A `BEQI` cannot compare against a marker constant.** The readers of the startup-type
global `0x7ff87c64` compare it with `beqi`:

```
7ffa98a6: 81 a5 e7   l32r   a13, 0x7ffa09b0     ; -> 0x7ff87c64  (startup type)
7ffa98a9: d8 0d      l32i.n a13, a13, 0
7ffa98ab: 26 6d d8   beqi   a13, 6, 0x7ffa9887  ; r=6 s=13 imm8=-40
```

Xtensa `BEQI` takes its comparand from **B4CONST** = `{-1,1,2,3,4,5,6,7,8,10,12,16,32,64,
128,256}` — the field is a 4-bit *index*, and the largest representable value is **256**.
Boot markers are **`0x80000001..0x80000009`** (§6a, literals located). **It is structurally
impossible for these instructions to be testing a boot marker.** Same result at
`0x7ffa9aaf` (`beqi a14, 6, 0x7ffa9a8b`) and `0x7ffac9af` (`beqi a10, 6, 0x7ffac95c`).

Decode validated on a known answer: `0x7ffa6b38: 16 f3 1b` decodes as
`beqz a3, 0x7ffa6cfb` — and `0x7ffa6cfb` is exactly the independently-established
convergence point of the admin gate's opcode chain.

**2. `0` in this enum is First Startup, not "No previous marker found".**
`0x7ffac7d9: l32r a9,->0x7ff87c64; bnez a9, 0x7ffac82f; l32r a10,<LOG 1550 "Admin: First
Startup">`. A Post Crash boot takes the non-zero branch and logs
`Admin: Normal Startup 0x6`.

**3. The claimed corroboration was my own error.** The "`expected == 7` for `HUSMR…`"
argument rested on reading Identify offset `0x40` as Model Number. It is **Firmware
Revision** (MN is offset `0x18`). For `FR = KNGND1xx`, `FR[0] != 'H'`, so `expected`
stays **6** and *matches* the drive. See the retraction in §11.

**INFERRED — what enum 6 actually is.** The `STARTUP` string array (StrIds 303–309) is
`FIRST STARTUP(0), NORMAL STARTUP(1), RECOVERY STARTUP(2), READ ONLY STARTUP(3),
FIRMWARE UPDATE STARTUP(4), FAST STARTUP(5), INVALID(6)`. Index 0 = `FIRST STARTUP` matches
observation 2 exactly, which makes **6 = `INVALID`** — i.e. the drive never established a
valid startup type. `libdmi_core` labels that state "diagnostic mode"
(`gf_is_diagnostic_mode` @ `0x42c90` tests `== 6`). Three enums are in play and must not be
conflated:

| enum | values | where |
|---|---|---|
| **boot marker** (persisted) | `0x8000000N`, N = 0..10 | SA header `+0x3c`, PROC0 `[a7+0]` |
| **`SYS:` startup reason** (log only) | 0..9, StrIds 1264–1273 | PROC0 banner |
| **startup type** (what the VUC returns) | small ints, 0 = FIRST … 6 = INVALID | PROC8 global `0x7ff87c64` |

### What survives — and it matters

The **physical** half of the hypothesis is untouched by this refutation, and is
independently *strengthened* by §8: PFAIL is a genuine **hardware brownout interrupt**
(PROC0 ISR `0x7ffa82dc`, IRQ bit 16), fed by the VMON rail monitor, not by anything on the
PCIe side. A U.2 connector carries **12 V and the PCIe lanes in one plug**, so a marginal
cable produces real rail droop and therefore **real PFAIL events** — no firmware bug
required. And a whole-device deallocate is a massive parallel NAND operation, i.e. a peak
current event, which is a coherent reason a `mkfs` in particular would trip a marginal
supply. That reframes the deallocate correlation as **power**, not TRIM semantics.

What the code does *not* support is the final step: markers 5/6/7 all dispatch to the same
"never finished" handler at `0x7ffaaf6b`, **not** to the Post Crash handler `0x7ffabd01`
(§6a). So an interrupted PFAIL by itself still does not produce Post Crash Startup — it
must additionally leave a crash/pfcrash section or an unsaved System Area behind. The
routes in §8 remain as documented.

**Bottom line: the enum-index argument is wrong, but the cable indictment stands** — on
better evidence than the hypothesis used. See "Keep or bin?".

## 7. The self-sustaining mechanism: UNEXSTRT

**PROVEN — StrId 3520:**

```
SYS: UNEXSTRT detected, writing UNEXSTRT stub header to crash area
```

This is the single most important string in the whole table. `UNEXSTRT` = *unexpected
start*. On any power-up / controller start that was **not** preceded by a recorded clean
shutdown, the firmware **writes a stub crash-dump header into the crash area**.

Consequences (**INFERRED**, but this string admits no other reading):
- Every abrupt controller restart — including the ~5 s NVMe controller resets that Linux
  performs in response to the Persistent Internal Error AEN — re-arms the crash section.
- This is exactly the reported field behaviour: "a cold power cycle recovers it, but the
  drive re-latches the crash section as soon as anything writes to it."
- Therefore **erasing the crash dump is not sufficient**. The very next startup must be a
  *clean* one, i.e. the host must perform a proper NVMe shutdown (write `CC.SHN=01`,
  poll `CSTS.SHST` until `10b`) before power is removed, so the boot marker becomes
  `CLEAN shutdown` rather than `No previous marker found`/`UNEXSTRT`.

### Where the UNEXSTRT write lives — **PARTIAL**

**PROVEN.** The log descriptor for StrId 3520 is at `0x7ff83420` in PROC0. The only
credible reference under standard `l32r` PC-rounding is the FLIX bundle at
**`0x7ffaac59`**, and the log call fires at **`0x7ffaac74`**. That sits inside the
function `entry a1,0x20` @ **`0x7ffaac30`** — which is the *same* function that contains
the whole marker-override chain out to `0x7ffaaf3e` (confirmed with
`xref.py PROC0 7ffaaf02 --fn`). So the UNEXSTRT stamp and the crash-section marker
override are two blocks of one master startup-evaluation routine.

**PROVEN — the write is a byte-level header edit**, immediately before the log:

```asm
7ffaac53: l8ui a8, a12, 0x0     ; read a header byte
7ffaac56: movi a11, 254         ; 0xFE  = ~1  -> clear bit 0 ?
7ffaac59: <FLIX>                ; slot0 l32r -> 0x7ff83420 (the UNEXSTRT log descriptor)
7ffaac61: <FLIX>
7ffaac69: <FLIX>
7ffaac71: s8i  a8, a12, 0x0     ; write it back
7ffaac74: call8 0x7ffb5398      ; log "SYS: UNEXSTRT detected, writing UNEXSTRT stub header to crash area"
```

**UNKNOWN — the gate.** Three FLIX bundles at `0x7ffaac3b`, `0x7ffaac43`, `0x7ffaac4b` sit
between the function entry and the byte edit, and their branch fields are not reliably
decoded. It cannot be unconditional: if every unexpected start stamped the crash area,
**every** SN200 that ever lost power would latch permanently, which is plainly not the
case in the field.

**Position matters, though — PROVEN.** The block sits at `0x7ffaac53`, only `0x23` bytes
past the function's `entry` at `0x7ffaac30`, i.e. in the **prologue**, reached by
fall-through *before* the marker dispatch chain at `0x7ffaae69`. So UNEXSTRT is evaluated
on its own predicate up front, not from inside a per-marker handler.

**Convergent finding, with one caveat.** An independent teardown
(`docs/sn200-independent-re.md`) concludes that markers **5/6/7** — the three
"started but never finished" states — are what drive the UNEXSTRT stub write. That fits
the semantics exactly and is very likely right. The one observation I could not reconcile:
the 5/6/7 handler at `0x7ffaaf6b` (`l32r a15,0x7ff826b8` + FLIX) appears to branch back to
**`0x7ffaacea`**, which is *past* the UNEXSTRT block, not into it. My `b12` field
extraction for these bundles is unvalidated, so this is **not** a refutation — but the
control-flow edge from "unfinished shutdown" to "stamp the crash area" is **still not
demonstrated instruction-by-instruction.** Treat the mechanism as strongly supported and
the exact edge as open.

**INFERRED (from the firmware's own wording, high confidence) — UNEXSTRT targets the
CRASH section, not PFCRASH.** The string says "writing UNEXSTRT stub header **to crash
area**", and the EEPROM section named `Crash Dump` (index 11) is the crash area — the same
section `CDW12 0x0503` clears. Not yet confirmed at instruction level.

**PROVEN — version attribution.** `UNEXSTRT` appears in **KNGND110 and KNGND122 but not
KNGND100**. It was introduced alongside the OM-6850 fix ("Added check startup to
gracefully handle media in these scenarios"), i.e. it is a deliberate new
unexpected-start bookkeeping mechanism, not an accident. Everything else in this
section — `Schedule reinit`, `Post Crash startup`, `Drive REINIT requested`,
`READONLY Startup requested`, `FAST_RESTART` — is present identically in all three
revisions.

Related **PROVEN** strings: `SYS: Unexpected empty System Area.`,
`SYS: Load-n-go boot override of failed shutdown.`,
`SYS: ERROR - Shutdown started but never finished`.

---

## 8. Trigger analysis — what actually crashed the drive

The DISCARD/deallocate hypothesis is **correct, and WD documented it themselves.**

### PROVEN — from WD/HGST's own release notes and errata shipped in the package

`docs/KNGND110_Release_Notes_v2.pdf` (verbatim, WD issue IDs):

> **OM-6850** — Likelihood: Low, Severity: High
> **Title: Drive in crashed state following Power Cycle, Controller Reset, and Deallocate Test**
> Failure Scenario: **Drive in Diagnostic Mode** during specific Power Cycle, Controller
> Reset, and Deallocate FIT testing.
> Root Cause: With back-to-back PFails, PFails that occur in the middle of a 200 ms
> power-on window may cause small loss of usable media. Over time, this leads to a crash.

> **OM-7044** — Severity: High
> **Title: Drive Crash - Solid Amber while running SGL Write/Deallocate with Reset Test**
> Root Cause: Incoming Reset may terminate on-going write command … miscalculation in the
> number of LBAs remaining … This case may result in a timeout and **eventually
> diagnostic mode because the Write command cannot be terminated in 15 seconds.**

> **OM-6836** — Severity: High
> **Title: Drive hang during Read/Write/Deallocate/Reset testing**
> Failure Scenario: Drive hang would occur while running mixed SGL IO and lead to a
> **crashed/diagnostic mode**. Root Cause: … DMA error during data command processing …
> frees the entire allocated set … leading to corruption.

> **OM-6588** — **Title: Drives failed to restore L2P table after large deallocate and a pfail**
> Failure Scenario: **Heavy deallocate IO workloads** during pfail could cause L2P table
> to become corrupt. Root Cause: metadata corruption due to a **race condition between
> large deallocate commands and internal flushing of L2P updates to NAND**.

> **OM-6697** — **Title: Namespace Disappears During AC Power Cycle Testing**
> Failure Scenario: Power Cycling + Random Read/Write/**Deallocate** IO Profile Testing
> results in incomplete shutdown after 2000+ iterations.

`docs/KNGND100_SN2xx_Errata.pdf`:

> A **race condition** was discovered with **large deallocate commands** and the internal
> firmware **journaling updates for the Flash Translation Table**. This could cause
> **system area corruption** during garbage collection as Flash wears out.
> Significance: Medium. Workaround: Issue a subsystem reset or a graceful power cycle to
> rebuild the Flash Translation Table.

> When a Write precedes a Deallocate, a Read immediately following the Deallocate for the
> same address range can result in a data validation error. Significance: High.

`docs/KNGND122_Release_Notes.pdf` adds a **"Drive Recovery:"** field per issue. Observed
values across the 24 entries: `Unable to recover` (7×), `Power Cycle Required`,
`Power Cycle`. WD's own position is that most diagnostic-mode crashes are **not**
host-recoverable.

### PROVEN — corroborating firmware strings

- StrId 631 `A de-allocate command is broken during PFail from LBA %x to %x` — a
  *journal-replay* record type: an interrupted deallocate is a first-class persisted state.
- StrId 317/318 `BackendMgr: Received Deallocate request for LBNs 0x%x - 0x%x` /
  `... complete with status %d`
- StrId 2881 `Deallocate requested for invalid/system blockset %d from manager %d`
- The journal-replay error family: `DM> Invalid Log event 0x%08x - 0x%08x found at %d in
  record %d`, `Log Replay failed as End Marker not found at %d-%d`,
  `LBN 0x%x is larger than drive capacity 0x%x`,
  `Open-close blockset events should not be present in this zone`.
- StrId 3189/3190 `Outstanding Trim, Port %d cmdID %d … numOfRange %d
  NumOfHostLbaRemained %d … startTicks %d now %d` — a **timeout watchdog specifically for
  trim/deallocate**, printed when a trim outlives its deadline.

### ⚠ Second field latch (2026-08-03) refutes "deallocate is necessary"

Timeline, same drive, same day:

1. Recovered by `0x0603` + `0x0503` + cold power cycle held 126 s. Came back `live`,
   full 7.68 TB, `nuse == nsze`, 0 media errors, **empty namespace**.
2. `queue/discard_max_bytes=0` set via udev, then the *exact* original trigger replayed:
   `sgdisk` + `mkfs.xfs` + 512 MiB write + remount + md5 verify.
   **Zero controller resets. mkfs finished in 1 second.** Drive stayed live.
3. Talos booted with the volume enabled; node healthy and Ready.
   (Talos most likely *adopted* the pre-created filesystem rather than running its own
   mkfs — **not confirmed**, and it matters: if Talos never issued a mkfs, step 3 is not
   evidence that discard suppression works under Talos.)
4. `ForceOff` on the **running** system, then power on.
5. POST: `UEFI0067: A PCIe link training failure is observed in Bus:174 Dev:3 F:0 and the
   link is disabled`, halt at F1. Bus 174 = `0xAE` = the `ae:03.0` downstream port.
   iDRAC SEL: "A fatal error was detected on a component at bus 174 device 3 function 0".
6. After a further power cycle: latched again, `state=resetting`, no namespace.

**Latch #2 involved no mkfs and no large DISCARD.** The bay's U.2 cable is known flaky.

### Verdict on the four competing models

| model | verdict | basis |
|---|---|---|
| **A. DISCARD-triggered** | **REFUTED as necessary; retained as one sufficient cause** | Latch #2 had no deallocate. But WD's OM-6588/6836/6850/7044 are real and match latch #1. Step 2 also shows that with discard suppressed the identical workload is harmless — 1-second mkfs, zero resets. |
| **B. UNEXSTRT** | **PLAUSIBLE, NOT PROVEN** | The mechanism exists (StrId 3520, code at `0x7ffaac30`) but its gate is undecoded and **which section it stubs is UNKNOWN** (§7). Cannot be unconditional or every SN200 would brick on power loss. |
| **C. PFAIL / unclean power-off** | ⚠ **DOWNGRADED — a clean pfail does NOT produce Post Crash** | See below. A `ForceOff` that completes its hold-up sequence writes marker **2 = `PFAIL shutdown`** and boots as `SYS: PFAIL startup`. That is a *normal, designed* path, not the latched state. |
| **D. Link loss during operation** | ✅ **CONFIRMED as a route — but via a HANG, not via PFAIL** | PROVEN that PCIe cannot reach the PFAIL object. But WD documents link-down → hang → diagnostic mode repeatedly, and marks it **"Drive Recovery: Unable to recover."** |
| **E. System-Area condition, no external event** | **NEW — PROVEN mechanism, previously unconsidered** | `SYS: Unexpected empty System Area.` also forces marker 9, and `SYS: Detected CellCare mismatch` silently arms marker 3 (REINIT). Neither needs a power or link event. |

### Why model C is downgraded — **PROVEN**

PFAIL is a **hardware brownout interrupt**, and it has exactly one producer in the entire
firmware. PROC0 `0x7ffa8428` (`SYS: Enable PFAIL monitoring`, StrId 1211) installs an ISR
at **`0x7ffa82dc`** into MMIO vector slot `0x82A60148` and enables **IRQ bit 16**
(`0x00010000`) on `0x82A70000`. The ISR only latches:

```asm
7ffa82dc: entry a1,0x20
7ffa82e7: l32r  a11,0x7ff830c0    ; PFAIL monitor object @ 0x7ff8cd80
7ffa82ec: s32i.n a13,a11,0x2c     ; pfailPending  = 1
7ffa82ee: s32i.n a13,a11,0x20     ; pfailAsserted = 1
7ffa82f0: s32i.n a12,a11,0x1c     ; startTicks
```

The monitor thread (`0x7ffa8313..0x7ffa8425`) then runs a deadline of
`0x7ff830e0 = 0x61A8 = 25000` units, and writes:
- **marker 6** `PFAIL Shutdown STARTED` at `0x7ffa83e7` (loads `0x7ff830ec`, `call8 0x7ffb4fec`)
- **marker 7** `PFAIL Shutdown TIMEOUT` at `0x7ffa840a` (loads `0x7ff830f4`), after logging
  `SYS: PFAIL timeout is expired` at `0x7ffa835b`

and on success, **SAM** writes marker **2**.

**None of markers 2/6/7 is `POST CRASH Startup`.** So an unclean power-off, by itself,
produces `SYS: PFAIL startup` — a designed, non-latching outcome. For a `ForceOff` to
latch the drive it must *additionally* leave a crash/pfcrash section or an empty System
Area behind.

### Why model D is confirmed — **PROVEN (code) + WD (docs)**

`PCIe_PfailShutdown` (PROC9 `0x7ffaecf0`) is a **consumer** of a PFail event, not a
producer: its single caller is PROC9 `0x7ffa443d`, it takes no voltage/link input, and its
only decisions are on per-port shutdown state. Every PCIe attention handler in PROC9
(`Link down detected` `0x7ffa4e45`, `PerstLinkDown = TRUE` `0x7ffa4ebc`, `FRSTA`
`0x7ffa4fc1`, `Fundamental Reset` `0x7ffa50c5`, `Hot Reset` `0x7ffa521d`) only sets flags
and bumps counters. **None can write the PFAIL object** — it is PROC0-local at
`0x7ff8cd80`, reachable only from the ISR. WD confirms the two are independent sources:
OM-6697 says *"when both **a link down and a Pfail interrupt** occur at exactly the same
time…"*.

But link loss reaches diagnostic mode by hanging the drive. WD, KNGND122, category
"Reset", Severity High, found at Customer:

> *"A race condition exists when a **PCIe uncorrectable error occurs with a host link
> down** that causes the Completion Queue messages to go into autodisable mode. The
> firmware timeouts waiting for the response from the hardware and **leads to a drive
> hang**."* — **Drive Recovery: Unable to recover.**

plus *"When a **link down occurs between the Queue Manager and Queue Engines enable
sequence** … resulting in a hang … **crashed/diagnostic mode**"*, the REFCLK entry
(*"if the REFCLK goes away around the same time as a Link Down/Link Training occurs, both
clocks become invalid"*), *"In the reset path for **Link Down Reset caused by PERST**, the
reset handler never gets to the point where CSTS is cleared"*, and the KNGND100
**Will Not Fix**: *"The device may not link up on systems that fail to provide proper
PERST signaling."*

A hang → logic trap → crash dump written → **CRASH** section → marker 9.

*(Caveat on issue IDs: the release-note PDFs interleave `ID:` and `Title:` across block
boundaries and two independent extractions disagree on some ID↔title pairings — notably
REFCLK, read as OM-6613 by one and OM-6386 by the other. **Cite these by title, not by
ID.** The quoted text itself is verbatim in both extractions.)*

### The unifying mechanism — **PROVEN (§6a)**

The boot-time predicate is `SYS: Detected a CRASH or PFCRASH section.` → force marker 9.
**Two separate tests** (PROC0 `0x7ffaae35` and `0x7ffaae3d`, one per section) branch to the
**same** log and the **same** forced marker. So the models are not rivals — they are
different ways of arming *one of two* sections that feed *one* predicate:

- Model A arms **CRASH** (a firmware assert writes a real crash dump).
- Model D arms **CRASH** (a hang → logic trap → crash dump).
- Model C arms **PFCRASH**, but only when the pfail sequence *fails to complete*.
- Model B arms one of them with a stub header (which, UNKNOWN).
- Model E bypasses the sections entirely: `SYS: Unexpected empty System Area.` at
  `0x7ffaae4a` **jumps directly to `0x7ffaaf08`** — the same forced marker 9.

**This is why the two clears behave differently and why it matters clinically:** clearing
PFCRASH (`0x0603`) is synchronous and harmless; clearing CRASH (`0x0503`) schedules the
destructive re-init. A drive latched only by PFCRASH should be recoverable **without a
wipe** — nobody has ever checked which section was armed before firing both.

### Conclusion on the original trigger (**INFERRED**, high confidence)

`mkfs.xfs` on a 7.68 TB namespace issues a whole-device Dataset Management / Deallocate.
On this ASIC that is the exact workload class WD's own FIT tests
("Read/Write/**Deallocate** + Reset") drove into diagnostic mode, via an L2P-journal race
whose watchdog is the 15-second command timeout (`Outstanding Trim …`). Once the
watchdog fires the firmware logic-traps, writes a crash dump, and sets the
`POST CRASH Startup` marker.

**The trigger is real and is a firmware defect, not host misconfiguration.** It is also
partly *fixed* in later firmware: OM-6588, OM-6836, OM-6850, OM-6697, OM-7044 are all
listed as fixed in **KNGND110**, and further reset/shutdown hardening landed in
**KNGND122**. A drive still on KNGND100 is running firmware with all of these open.

---

## 9. AEN behaviour — why the 5 s window, and can it be widened

### Why the AEN fires
**PROVEN.** StrId 1774: `Admin_NotifyHandler: Sending Persistent Internal Error async
event on Post Crash Startup.` — emitted from `Admin_NotifyHandler`, unconditional in this
mode. The neighbouring strings show the notify handler's cases:
`CC_ENABLED`, `CC_DISABLED`, `DBERR`, `Persistent Internal Error`, `Default`.

### Why Linux resets the controller
**PROVEN (Linux side, from the observed dmesg).** The AEN is type 0 (Error) with Event
Information `03h` = *Persistent Internal Error*. The kernel logs
`resetting controller due to persistent internal error` — that string exists only in
`nvme_complete_async_event()` in `drivers/nvme/host/core.c`, which special-cases this
event and calls `nvme_reset_ctrl()`. The ~5 s cycle is the firmware's AEN re-arm period
plus the host's reset turnaround. Nothing about the reset is a timeout or a host-side
misdiagnosis: the drive asks for it and Linux obliges.

### Why `nvme set-feature -f 0x0b` fails with 0x400b
**PROVEN.** `0x400b` = DNR(bit 14) | SCT 0 (Generic) | SC `0x0b` = *Invalid Namespace or
Format*. Confirmed by `libdmi_core`'s own decode table `CSWTCH.117` @ `0xc59a0`: index
`0x0B` → `-2002` → `HDMS_NAMESPACE_OR_FORMAT_INVALID`, message literally
`"Invalid namespace or format"`. Note `libdmi_core` **reads** feature 0x0B during
capture-diagnostics (it is in `gf_dump_feature_simple_info` @ `0x2e0c60`) but always with
**NSID 0**, and never writes it — the library has no `set-features 0x0B` call anywhere.

The firmware has a matching message, StrId 2941:

```
SetFeat NSID 0x%x not Attached. port %d
```

In Post Crash Startup mode the namespace is not attached, so the Set Features handler's
namespace validation rejects the command before it ever reaches the AEC logic. This is a
namespace-attachment failure, **not** a "this feature is unsupported" failure.

### Is the AEN maskable at all?
**PROVEN — no.** The firmware's complete list of maskable async events is a contiguous
string block (StrIds 3263–3269):

```
Available spare space async event detected but masked; discarded
Temperature threshold async event detected but masked, discarded
NVM subsystem reliability async event detected but masked, discarded
Media read only async event detected but masked, discarded
VCAP failure async event detected but masked, discarded
Namespace Attribute Notices detected but masked, discarded
Firmware Activation Notices detected but masked, discarded
```

Those are the SMART/Health critical-warning bits plus the two notice bits — i.e. exactly
the bits Feature 0x0B defines. **"Persistent Internal Error" is an Error-class AEN and
appears nowhere in the mask list.** Per NVMe spec the Error class is not maskable by
Feature 0x0B either. So there is no feature and no VUC that suppresses it.

### But the window CAN be made unlimited
**INFERRED, high confidence.** A controller may only *post* an asynchronous event if the
host has an **outstanding Asynchronous Event Request** command. Linux's `nvme` driver
submits AERs automatically and unconditionally. If the device is instead bound to
`vfio-pci` and driven from userspace (SPDK, or a small libvfio/uio program), you simply
**never submit an Asynchronous Event Request** — the firmware then has nowhere to deliver
the AEN, nothing triggers `nvme_reset_ctrl()`, and the admin queue stays up indefinitely.

This is the correct answer to "can the AEN be suppressed": not by masking it, but by
starving it of a completion slot, which requires taking the kernel driver out of the path.

---

## 10. VUC command map

### Encoding — **PROVEN** from `libdmi_core.so.0.39`

Only four functions in the whole library build a raw NVMe command for HGST devices:
`gf_nvme_vuc_simple_real` @ `0x8bf90`, `gf_nvme_vuc_real` @ `0x8c150`,
`hgst_nvme_log_dump_real` @ `0x8c4f0`, and a dead twin pair at `0x8c660`/`0x8c820`.
(Ghidra image base `0x100000`; file VA = Ghidra VA − `0x100000`.)

```c
// gf_nvme_vuc_simple_real
cmd = {0};                            // 64 bytes -> NSID = 0
cmd[0x00] = opcode;
cmd[0x28] = cdw10;
cmd[0x30] = (subcmd << 8) | cmd_id;   // CDW12
```

Confirms the field encoding exactly: **NSID = 0, `CDW12 = (subcmd << 8) | cmd`.**
`gf_nvme_vuc_real` additionally lets callers pre-seed CDW12[31:16], NSID, CDW11, CDW13.

SN200/SN260 are the **"Omaha"** class (`om_*`/`omc_*`), derived from `HGSTNVMeController`;
SN100/SN150 are **"GallantFox"** (`gf_*`). Both share the `gf_nvme_vuc*` transport.
Model table `om_models` @ `0x2e0d00` lists `HUSMR76*BDP3Y1` etc.

### Complete VUC table — every VUC this library can emit (**PROVEN**)

NSID is always 0 unless noted.

| # | library symbol | op | CDW10 | CDW12 | dir/len | notes |
|---|---|---|---|---|---|---|
| 1 | `gf_nvme_clear_crash_dump` | 0xFF | 0 | **0x0503** | none | cmd 3 / sub 5 |
| 2 | `gf_nvme_clear_pfail_dump` | 0xFF | 0 | **0x0603** | none | cmd 3 / sub 6 |
| 3 | `gf_nvme_sys_init_done` | 0xFF | 0 | **0x0004** | none | CDW0 byte1 = **startup type** |
| 4 | `gf_nvme_get_binary_drive_log_size` | 0xC6 | 2 | 0x0120 | r 8 | size = dword[0] |
| 5 | `gf_nvme_get_string_table_size` | 0xC6 | 2 | 0x0120 | r 8 | size = dword[1] |
| 6 | `gf_cd_get_binary_drive_log` | 0xC6 | len/4 | 0x0020 | r len | |
| 7 | `gf_cd_get_string_table` | 0xC6 | len/4 | 0x0220 | r len | pulls the on-drive StringTable |
| 8 | `gf_nvme_get_crash_dump_size` | 0xC6 | 2 | **0x0320** | r 8 | |
| 9 | **`gf_cd_get_crash_dump`** | 0xC6 | len/4 | **0x0420** | r len | **read the crash dump** |
| 10 | `gf_nvme_get_pfail_crash_dump_size` | 0xC6 | 2 | **0x0520** | r 8 | |
| 11 | **`gf_cd_get_pfail_crash_dump`** | 0xC6 | len/4 | **0x0620** | r len | **read the pfail dump** |
| 12 | `_gf_capture_hwcomp_values` | 0xC6 | 0x10 | 0x0021 | r 0x40 | |
| 13 | `gf_nvme_get_defect_data` | 0xC6 | 0x40000 | `(sec<<8)\|0xB7` | r 1 MiB | only `sec=0x30` used |
| 14 | `gf_nvme_get_drive_capacity` | 0xCC | 0 | 0x0003 | none | result in CDW0 |
| 15 | `gf_nvme_drive_resize` | 0xCC | 1 | 0x0003 | none | CDW13 = new size |
| 16 | `gf_calc_power_consumption` | 0xCC | n*5 | 0x0005 | r n*20 | n=0x14 on Omaha |
| 17 | `hgst_nvme_get_stats_power` | 0xCC | 8 | 0x000C | r 0x20 | |
| 18–20 | `omc_locate` off/on/query | 0xD4 | 0 | 0x0008 / 0x0108 / 0x0208 | none | locate LED |
| 21 | `gf_nvme_set_smart_threshold` | 0xD4 | 0 | `(attr<<8)\|0x30` | none | CDW11 = threshold |
| 22 | `gf_nvme_clear_smart_threshold` | 0xD4 | 0 | `0x01000000\|(attr<<8)\|0x30` | none | |
| 23 | `gf_nvme_ns_delete` | 0xD8 | 0 | 0x0000 | none | NSID = target |
| 24 | `gf_nvme_ns_create_modify` | 0xD9 | 0x80 | 0x0000 | w 0x200 | |
| 25 | `om_nvme_ns_resize` | 0xD9 | 4 | 0x0001 | w 0x10 | |
| 26 | ☠ `gf_nvme_ns_status` — **misnomer, it is ns ATTACH/DETACH** | 0xDC | 0 | 0x0000 | none | NSID = target, **CDW13 = 0 attach / 1 detach**. Do not fire exploratively. See §10a. |
| 27 | ☢ `hgst_nvme_secure_purge` | **0xDD** | 0 | 0x0000 | none | **irreversible full wipe** |
| 28 | `hgst_nvme_get_secure_purge_state` | 0xDE | 0x0C | 0x0000 | r 0x30 | safe, read-only |
| 29 | `hgst_nvme_log_dump` (size) | **0xE6** | 2 | `(mode<<8)` | r 8 | size = **big-endian** u32 at bytes 4..7 |
| 30 | `hgst_nvme_log_dump` (data) | **0xE6** | ndw | `(mode<<8)` | r ndw*4 | **CDW11 = dword offset**, 0x400 dw chunks, start offset 2 |

For SN200 `hgst_nvmec_dump_list` contains exactly one E6 mode: **MODE_0x00**.

Non-VUC vendor reads on the same devices: Get Log Page **0xC1** (TLV subpage walker),
**0xC2** (2-phase, 8-byte size header then ≤64 KiB), **0xCA** (0x88 bytes).

### Reading the crash dump directly (bypasses the whole `dm-cli` trap)

```sh
# size
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r
# body, 64 KiB at a time (CDW10 = dwords)
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=0x4000 --cdw12=0x0420 --data-len=65536 -r
# the drive's own string table, for decoding the dump
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0120 --data-len=8 -r
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=<sz/4> --cdw12=0x0220 --data-len=<sz> -r
# E6 dump, mode 0: 8-byte header (size is BIG-endian at [4..7]), then 4 KiB chunks
nvme admin-passthru /dev/nvme7 --opcode=0xE6 -n 0 --cdw10=2     --cdw11=0 --cdw12=0 --data-len=8    -r
nvme admin-passthru /dev/nvme7 --opcode=0xE6 -n 0 --cdw10=0x400 --cdw11=2 --cdw12=0 --data-len=4096 -r
```

### There is no reinit / exit-diagnostic VUC — **PROVEN negative, now exhaustive**

**28 dispatch tables** (not 21), entry stride **56 bytes**
`{u64 cmd_id, u64, fn *validate, u64, u64, fn *schema/exec, u64}`, each registered by
exactly one `*_class_ctor`. Command ids come from `HDME_CMD_ENUMS_enum_strs` @ `0x2de280`
(size `0x270` = **78 entries**, ids 23000–23077), with JSON names in
`HDME_CMD_ENUMS_desc_strs` @ `0x2e4940`.

**Exhaustiveness proof:** every 4-byte-aligned `u64` in `.data`, `.data.rel.ro` and
`.rodata` was scanned for values in `[23000, 23078)`. **Zero hits outside the 28 tables.**
There is no unparsed table.

**28 of 78 ids are dispatched; 50 are orphans** — and each orphan id occurs **zero** times
anywhere in `.text`/`.rodata`/`.data`/`.data.rel.ro`, and appears in no other binary
(`dm-cli`, `etd`, `libied`, `libetd`, `libcu`, `libe6text`, `libau_utils`). So
`EXIT_MODE`(23051, JSON `"exit-mode"`), `SET_MODE`(23041), `WRITE_MARKER`(23046),
`GET_MODE`(23015) are **dead API surface: the handler functions are not in the binary.**

**They cannot be unlocked.** `base_dev_cmd_map_build` @ `0x810f0` / `base_dev_get_cmd` @
`0x81bc0`: dispatch is a `GHashTable` keyed by command *name*, populated **only** by
walking each class's `cmds[]` up the inheritance chain (`class+0x08` = table,
`class+0x10` = count, both set in the class ctor). There is no second insertion site, no
runtime registration, and no flag that adds entries.

**Correction — SN200 inherits `gfc_cmds`.** Walking the type structs
(`type+0x00` name, `type+0x08` parent):
`OmahaController@0x2e7d60 → GallantFoxControllerType@0x2e5f00 → HGSTNVMeControllerType@0x2e6c20 → NVMeControllerType@0x2e7740 → BaseDeviceType@0x2e8d80`.
So SN200's effective set is 22 commands and **does** include `reset-to-defaults`(23032),
`resize`(23033), `secure-purge`(23039). An earlier claim here that `RESET_TO_DEFAULTS` is
SN100/150-only was **wrong**. It changes nothing, because:

### The tool-side diagnostic-mode lock — **PROVEN**

`gf_is_diagnostic_mode` @ `0x42c90` issues `0xFF`/CDW12 `0x0004` and returns `-2023`
(`HDMS_DEV_DIAGNOSTIC_MODE`) iff **startup type == 6**. Seven callers hard-refuse on it:
`gfc_validate_configure_smart`, `gfc_validate_get_statistics`, `gfc_mng_ns_validate`,
`gfc_validate_reset_to_defaults`, `gfc_validate_resize`, `omc_mng_ns_validate`,
`gfc_resolve_device_status`. **`capture-diagnostics` is the only command with no
diag-mode gate** — which is exactly why it is the only one that still runs.

And `gfc_reset_to_defaults` @ `0x41d60` would not help anyway: locate-LED off → Get/Set
Feature 0x04 → `clear_smart_threshold` → `nvmec_mng_pwr_reset` →
**`gf_nvme_drive_resize(default_capacity)`** (`0xCC`/`0x0003`) → `NVME_IOCTL_RESET`.
**No marker write, no mode change, and the resize step makes it mildly destructive.**
(`issue_nvme_reset_real` @ `0x87520`: type 1/5 → `NVME_IOCTL_RESET` `0x4E44`,
type 4 → `NVME_IOCTL_SUBSYS_RESET` `0x4E45`, type 2 → deliberate no-op. Both are resets
without `CC.SHN` ⇒ `UNEXSTRT`, §7.)

### Raw passthru — **PROVEN unreachable in this build**

- `nvmens_raw_passthru` @ `0x5fe10` is the real JSON escape hatch — schema
  `{"command": <blob, exactly 0x40 bytes>, "timeout": int, "data": <blob>,
  "response_size": int}` → `{"response","status","dword0"}`; wrong length → `-1003`. It is
  in **no** dispatch table, **no** vtable, and has **zero** code references.
- `nvmec_raw_passthru` @ `0x524d0` **is** installed into `NVMeControllerType+0x238`
  (at `0x516b1`) but **no site ever calls `*0x238`**. Dead slot.
- `.dynsym` exports only 102 symbols (`dmi_run`, `dmi_ctx_*`, `dmi_json_*`, `hdm_*`,
  miniz). Neither is exported, so `dlsym` cannot reach them either.

### Auth / unlock — nothing that helps (**PROVEN**)

- `psid_revert` @ `0x30290` has exactly one caller, `atad_configure_security` — and
  `CONFIGURE_SECURITY`(23072) appears **only in `atad_cmds`**, i.e. ATA devices.
  Not reachable for any NVMe device.
- `nvme_security_send_real` @ `0x8fe90` has **zero callers**;
  `nvme_security_receive_real` @ `0x8fd40` is used only for read-only TCG discovery.
- `Admin_VUC_Enable` / "VUC Control disabled" are firmware-side only, and that gate
  (StrId 1805) is **separate** from the Post-Crash gate (StrId 1804) in the same checker.
  Un-gating VUCs would not lift the Post-Crash rejection.

### Namespace re-attach — reachable, but the firmware refuses. **High-value negative.**

`omc_mng_ns` @ `0x6b010` dispatches `list/create/attach/delete/detach/resize`; `attach` →
`nvmec_mng_ns_attach` @ `0x5b220` → `nvme_ns_atchmt_real` @ `0x8f940`:
opcode **`0x15`**, NSID = target, `CDW10 = sel & 0xF` (0 attach / 1 detach), 4096-byte
controller-ID list data-out. Blocked host-side by `gf_is_diagnostic_mode`.

Bypassing the tool does not help: `Admin_NamespaceAttachment`'s own errors (StrIds
2007–2020) include **`The LBN Translation Table is invalid.`** (2008),
`The Device is in Read-Only mode` (2007), `NS %d not allocated.` (2012).
**INFERRED, high confidence:** the namespace is not merely detached — it is
*un-attachable* because the LBN translation table is invalid, which is the definition of
the crash state. `attach-ns` is safe but will not work.

### Firmware side — the complete admin/VUC surface

All 82 `Admin_*` symbols in the KNGND122 string table were enumerated. **There is no
`Admin_Vuc*` that exits diagnostic mode, writes a startup marker, requests a startup type,
or forces a re-init.** The only writer of `Drive REINIT requested` is the second call
inside the crash-dump erase case (`0x30033704`–`0x30033724`), and it is scheduling-only.
`Admin_SanitizeExitFailureMode` is the only "exit … mode" in the whole firmware and it is
sanitize-specific.

After a successful clear, `cap_diags_end` returns `HDMS_SHUTDOWN_REQUIRED` (−6002) — WD's
own tooling expects a **power cycle**.

> ### Verdict: power cycling is structural
> There is no host-sendable command in this stack that exits Post Crash Startup, sets or
> clears a startup marker, or forces a synchronous re-init. The escape hatch was compiled
> out of the shipped library, and the drive's own attach handler explains why the
> namespace cannot come back.

### ⚠ The one untested avenue: NVMe-MI out-of-band (PROC9)

**PROVEN — the library has none of this; the firmware has all of it.** No MCTP/SMBus/I²C/
VDM/MI code exists in `libdmi_core`, `dm-cli`, `etd`, `libetd`, `libcu`, `libe6text`,
`libau_utils` (`libied` is an offline log-page decoder; its `i2c_*` hits are OpenSSL
ASN.1 helpers).

**PROC9 is the NVMe-MI / MCTP / SMBus processor** — 126 MI/MCTP log descriptors versus
≤26 in any other core. Confirmed literals in its pool:

| literal | StrId | text |
|---|---|---|
| `0x7ffa1708` | 164 | **`MI: Initiating an NVM subystem reset`** |
| `0x7ffa1728` | 211 | `MI: Startup complete.` |
| `0x7ffa1790` | 172 | `MI: Invalid admin cmd opcode %x` |
| `0x7ffa179c` | 171 | `MI: unhandled admin cmd opcode %x` |
| `0x7ffa1a94` | 212 | `MI: NVMe-MI: MI_AdminCmdHandler signaled` |
| `0x7ffa1b1c` | 224 | `MI: NVMe-MI: MI_PCIECmdHandler ACCESS DENIED` |

Handlers present: `MI_ControlPrimitiveHandler`, `MI_AdminCmdHandler`,
`MI_GetHealthStatusCmdHandler`, `MI_ReadMiDataStructureCmdHandler`,
`MI_ConfigurationGet/SetCmdHandler`, `MI_VpdReadWriteCmdHandler`, `MI_PCIECmdHandler`
(PCIe config read/write), plus a full MCTP control stack. The `MCTP-%s:` format strings are
fed by the literals **`SMB`** and **`PCIE`** — **MCTP runs on both SMBus and PCIe VDM.**

Why this matters here: **SMBus is independent of the PCIe link.** After `UEFI0067`
disabled the link, MI is the only path that could still reach the controller.

- `MI: Initiating an NVM subystem reset` is a real out-of-band controller restart.
  **Confidence it exits Post Crash: low** — it is a reset without `CC.SHN`, i.e. another
  `UNEXSTRT` (§7). Non-destructive.
- **`MI_AdminCmdHandler` is an admin-command tunnel with an opcode filter**
  (`unhandled` vs `Invalid`). Per the NVMe-MI spec the tunnel allows only a defined
  subset, and vendor opcodes like `0xFF` are normally **not** in it. **The actual
  whitelist is UNKNOWN** — the PROC9 log call sites did not resolve with the
  PROC8-calibrated `l32r` formula. **If `0xFF` were permitted, `0x0503`/`0x0603` could be
  issued over SMBus with no PCIe link at all.** This is the single best remaining research
  target.
- `MI_PCIECmdHandler` can read/write PCIe config space out-of-band — the only way to
  inspect the drive's own config registers while the link is down.

Practical requirement: a BMC or SMBus master on the drive's SMBCLK/SMBDAT
(PCIe edge connector for an HHHL card). Chassis-dependent; unverified for this host.

### Status code decoding — **PROVEN**

`nvme_decode_status` @ `0x8d050`: `sc = s & 0xFF`, `sct = (s>>8) & 7`, `more = (s>>13)&1`,
`dnr = (s>>14)&1`.

**SCT=7 statuses are not decoded by the main path.** `nvme_check_success` @ `0x8d6a0`
handles SCT 0/1/2 only; SCT 3–7 fall through to a generic `-3`. There is no vendor status
table in the library.

Vendor status codes *are* recognised in `gf_nvme_check_status` @ `0x8aef0`, which ignores
SCT and switches on SC alone:

| SC | internal | meaning |
|---|---|---|
| 0xC0 | −2009 | namespace ID conflict |
| 0xC1 | −2004 | insufficient capacity for namespace |
| **0xC5** | **−2023** | **`HDMS_DEV_DIAGNOSTIC_MODE`** |
| 0xC3 | −2008 | `HDMS_DEV_NO_DATA` ("Crash dump unavailable") |

Generic (SCT=0) table `CSWTCH.117` @ `0xc59a0`: `0x01`→unsupported argument,
`0x02`→arg invalid, `0x06`→internal error, **`0x0B`→`HDMS_NAMESPACE_OR_FORMAT_INVALID`**,
`0x0C`→busy, `0x1D`→sanitize in progress.

Status-code numbering key: `HDMS_x = −(base + index)`, bases `ARG_ERR`=1000, `DE`=2000,
`SE`=5000, `QS`=6000, `IE`=7000.

### Firmware-side VUC families (from the string table)

**PROVEN** that they exist; encodings not recoverable from the string table alone:

- `VUC Get Drive Log SubCmd %08X` — the family that contains the crash/pfail size probes
  and the E6 dump retrieval (`SPI Crash Section is in an invalid state`,
  `Get Crash Dump Size - no valid crash dump available`).
- `VUC Reset Drive Stats SubCmd %08X`
- `VUC SCSI Ported Cmd %08X`
- `ADM: Admin_VUC_Enable ... New State: %u` — there is a **VUC enable/disable gate**; a
  third admin rejection reason is `Admin cmd restricted by VUC Control disabled: 0x%x`.
- `Admin_VucTriggerAsyncEvents`, `Issue Vendor Specific Async Event: PCIe Port[0x%x],
  EventInfo[%d], Associated Log Page[0x%x]`, `fake vcap failure vuc`,
  `clear vcap failure vuc`
- `VUC BE_TMODE`, `VUC_ERASE_PWR_CHAR`, `VUC_FlashRead`, `VUC Flash_WritePageRaw`,
  `VUC Multiplane Erase/Write/Read`, `VUC Get Dies Status`,
  `VUC Enable Die Offine Access`, `VUC_SYS_SET_THERMAL_THROTTLING_PARAMS`,
  `NVM_ADMIN_CMD_VUC_INV_FW_SLOT` (PSID-gated)
- Overlay-resident VUCs (loaded on demand into PROC8's `0x30000000` overlay bank):
  `Admin_VUC_Mi_Test_OVL022`, `Admin_VUC_Device_Config_Modify_OVL024`,
  `Admin_VUC_Sys_Get_Drive_Cfg_OVL024`,
  `Admin_VUC_Sys_Set_Fw_Download_Psid_Validation_OVL025`,
  `Admin_VucFlashGet/SetTestModeRegister_OVL026`,
  `Admin_VucFlashSLDPCHistoryHistogram_OVL027`, `Admin_VucFlashReset_OVL028`,
  `Admin_VucCellCareUpdateLKTMRS_OVL031`
- OAM commands (non-erase): `Read Raw Section` (with
  `OAM READ RAW SA CMD: Read of System Area journal from EEPROM failed.`) and `Kill`.

### The admin rejection gate — **PROVEN, located at `0x7ffa6b18`**

There is a **single unified** "is this admin command allowed right now" checker in the
main image, handling every restriction reason (StrIds 1804 Post Crash, 1805 VUC Control
disabled, 1806 purge phase, 3370 sanitize):

```asm
7ffa6b18: entry a1, 0x20
7ffa6b1b: l32r  a8, <ptr 0x7ff87c64>; l32i.n a8, a8, 0   ; global startup/mode state
7ffa6b38: beqz  a3, 0x7ffa6cfb                           ; a3 = admin opcode
7ffa6b3b..7ffa6bc9: long chain of `beqi a3, K, 0x7ffa6cfb`
                    interleaved with movi a9,9 / movi a14,236 / movi a8,255 / movi a9,202
...
7ffa6cfb: <converged label>
7ffa6d05: beqz  a9, 0x7ffa6bd9
7ffa6d08: l32r  a10, 0x7ffa0d9c        ; = LOG StrId 1804 (Post Crash reject)
7ffa6d10: call8 0x7ffb45a8             ; Log_Printf_StrIdDesc
7ffa6d13: l32r  a9,  0x7ffa0da0        ; = 0x8F8A0000
7ffa6d16: or    a2,  a5, a9            ; return status
7ffa6d19: retw.n
```

#### ⚠ Correction: the Post Crash rejection status is **0x7C5**, not 0x7D3

**PROVEN.** The returned constant is `0x8F8A0000`. In the CQE, DW3 carries the status
field in bits 17..31, so `0x8F8A0000 >> 17 = 0x47C5`: **DNR=1, SCT=7 (vendor specific),
SC=0xC5**.

This ties out perfectly with `libdmi_core`, whose only vendor status decoder
(`gf_nvme_check_status` @ `0x8aef0`) maps **SC `0xC5` → `HDMS_DEV_DIAGNOSTIC_MODE`**.

The previously recorded `0x7d3` comes from the **error-log entry for the async event**
(`sqid: 65535, cmdid: 0xffff`), which is a different thing from the admin-command
rejection. Both are SCT=7; the SCs differ (`0xD3` for the AEN, `0xC5` for the gate).

Other rejection constants proven in the same function:
- purge phase (LOG 1806 @ `0x7ffa6c55`) returns `0x00180000` → SCT=0, SC=0x0C (Busy).
- VUC Control disabled (LOG 1805) descriptor is at `0x7ffa0da4`, in the same function.

#### The whitelist — **RESOLVED, and it is an ALLOW-list**

Superseded reading: an earlier pass extracted the constants `0, 1, 2, 4, 5, 6, 8, 10, 10,
12, 16, 12, 128, 8, 32, 10, 32, 256` and could not settle the polarity. Those constants
were wrong (the duplicates came from a truncated displacement field) **and the polarity was
inverted**. The correct decode is below; see also `docs/sn200-crash-dump-retrieval.md` §1.5.

A matched opcode jumps to `0x7ffa6cfb`, which sets `a9 = 0` and jumps *past* the
`movi.n a9,1`; `beqz a9` then continues to the next gate. Anything that falls off the end
of the chain reaches `0x7ffa6bd1` (`a9 = 1`) → log StrId 1804 → `0x8F8A0000`.
**Matching means allowed.**

**PROVEN — the complete exempt set while latched in Post Crash Startup:**

| opcode | name | condition |
|---|---|---|
| 0x00, 0x01, 0x04, 0x05 | Delete/Create I/O SQ, CQ | unconditional |
| 0x02 | Get Log Page | unconditional |
| 0x06 | Identify | unconditional |
| 0x08 | Abort | unconditional |
| 0x09 / 0x0A | Set / Get Features | unconditional |
| 0x0C | Async Event Request | unconditional |
| 0x10 / 0x11 | Firmware Commit / Image Download | unconditional |
| **0xC6** | crash-dump / string-table / drive-log VUC | **only if cmd byte ∈ {0x20, 0x30}** |
| 0xCA | vendor | only for sub-values `2,3,4,8,0x0D,0x0E,0x0F,0x10,0x11,0x13,0x21,0x32` |
| 0xE6 | log-dump VUC | unconditional |
| 0xEC | vendor | unconditional |
| 0xFF | clear-dump / sys-init-done VUC | unconditional |

Everything else returns `0x8F8A0000` → SCT 7, SC 0xC5, DNR. Rejected includes `0xCC`,
`0xD4`, `0xD8`–`0xDF` (so `0xDD` secure purge cannot even be issued while latched),
`0x0D`/`0x15` namespace management, `0x80` Format, `0x81`/`0x82` Security, `0x84` Sanitize.

So **the whole crash-dump retrieval path survives the latch** — every command it uses is
`0xC6` with CDW12 low byte `0x20`. Conversely **`0xFF` is exempt and unconditional**: the
commands that wipe the drive are fully reachable, with no firmware safety net.

The gate byte is read from the command context at `+0x38`. That offset is PROVEN; that it
is `CDW12[7:0]` is **INFERRED** (it is the only reading consistent with `{0x20, 0x30}` and
with every real `0xC6` VUC in `libdmi_core`).

There are **four independent gates in series**: Post Crash → VUC Control (StrId 1805,
`byte [0x7ff8f140+0x9d]`, returns `0x80020000` = SC 0x01) → purge phase (StrId 1806,
`0x00180000` = SC 0x0C) → sanitize (StrId 3370). `0xC6` cmd `0x20` passes the VUC-Control
gate as well, by the same `{0x20, 0x30}` test at `0x7ffa6bdf`.

**Firmware Commit is not an escape hatch — PROVEN.** `0x10` is accepted while latched, but
Commit Action `0b011` (activate immediately without reset) is unimplemented: the overlay
handler does `extui a8,a10,3,2` (only 2 bits) then `blti a8,3,<handler>`, so CA=3 falls
straight through to LOG 2188 `Firmware Activate Invalid Activation Action` and returns
`0xC0040000`. The activate path's own strings (790/791/792 `Subsystem Restart Required` /
`Conventional Reset required` / `Controller Restart Required to activate firmware`,
`FwActivateNextStartup`) all demand a reset.

### A clean, standard-command state probe

WD's release note OM-6402 says:

> Added new field **"Post Crash Mode (Byte 3072)"** at the start of the Vendor Specific
> area in the Identify Controller structure.

**UNKNOWN whether this is actually populated on KNGND122.** No StrId mentions it, the only
`3072` immediates in PROC8 (`0x7ffa350e`, `0x7ffad451`) are false positives inside FLIX
bundles, and `libdmi_core` **never reads offset `0xC00`** — its only Identify-Controller
vendor reads are `0xC09`, `0xC60`–`0xC67` and `0xC68`–`0xC6F`. Still worth one command:

```sh
nvme id-ctrl /dev/nvme7 -b | od -A d -t x1 -j 3072 -N 32
```

The reliable state probe remains the VUC `0xFF / CDW12 0x0004`, whose CDW0 byte 1 is the
startup-type global `0x7ff87c64` (**6 = diagnostic**, §2).

## 10a. Is the namespace SUPPRESSED or WIPED in Post Crash Startup?

**Answer: SUPPRESSED. INFERRED, moderate-to-high confidence.** Post Crash Startup is a
*quarantine posture*, not a rebuild. Every startup-type-keyed branch found for the
Post Crash value is a **skip**; every rebuild path is keyed to *other* startup types.

**PROVEN — there is no post-crash namespace teardown.** AdminMgr startup is one large
continuation-passing function at **`0x7ffac398`**. Its startup-type branch:

```asm
7ffac7d9: l32r a9, ->0x7ff87c64     ; startup type
7ffac7de: bnez a9, 0x7ffac82f       ; anything non-zero (including 6) -> "normal"
7ffac7e1: l32r a10, <LOG 1550 "Admin: First Startup">
7ffac82f: l32r ..., <LOG 1552 "Admin: Normal Startup 0x%x">
```

A Post Crash boot therefore logs **`Admin: Normal Startup 0x6`** and follows the ordinary
chain — there is no special namespace destruction.

**PROVEN — `Admin_NamespaceStartup` = `0x7ffad364`** (StrIds 1967–1975 and 2951 all live in
`0x7ffad364..0x7ffadb1f`; its address is taken at `0x7ffac3ce`, `0x7ffac7e9`, `0x7ffafd0f`,
the first of which is *unconditional*, before the type branch). It loads and validates the
persistent **LBN Translation Table** header, invalidates it only on specific
self-consistency failures (StrIds 1968/1969/1970/1972), and **creates** namespaces
(StrId 1975 @ `0x7ffadac6`) only down paths gated on startup type **0**
(`beqz` at `0x7ffad938` and `0x7ffadb05`) or a pending resize. **None of the
invalidate/recreate paths is keyed to type 6.**

**PROVEN — the one real Post-Crash early-out is a skip**, in the Admin System Area family
at `0x7ffb247c`:

```asm
7ffb2518: l32r  a12, ->0x7ff87c64
7ffb251b: l32i.n a12, a12, 0
7ffb251d: beqi  a12, 6, 0x7ffb24f1   ; Post Crash -> jump straight to the tail
7ffb2522: call8 0x7ffb1fe4           ; the System Area work, SKIPPED in mode 6
```

**PROVEN — capacity/geometry change is gated to two startup types and traps otherwise**
(`0x7ffac43f`): on a pending resize, the code compares the startup type against exactly two
constants and otherwise logs StrId 3283 `AdminMgr: Unexpected startup state 0x%08x for
resize 0x%08x` followed by `break.n` (a trap). **INFERRED** (the constants are inside
undecoded FLIX bundles) those two are the re-init types. This is direct structural support
for "re-init is the mode that rebuilds the namespace table and capacity, and Post Crash is
not".

### ⚠ But "suppressed" does not mean "your data is intact"

Three separate reasons to be careful:

1. The **crash that caused the latch** may itself have corrupted the L2P — OM-6588 is
   literally *"Drives failed to restore L2P table after large deallocate and a pfail …
   metadata corruption"*. Suppression says nothing about what happened before it.
2. **`0x0503` schedules the re-init** (§4), and re-init *is* the rebuild mode. Once fired,
   the question is moot.
3. **While latched you cannot observe the media at all.** The reported "GPT + XFS are
   GONE" after latch #2 is **not** established — with no namespace presented there is
   nothing to read. The correct statement is "no namespace is presented". Whether the
   filesystem survived is **UNKNOWN** and only observable after a recovery that does not
   run a re-init.

### `unvmcap == 0` — the evidence you hoped for is **UNKNOWN**

**PROVEN:** the Identify Controller response is a **pre-built 4096-byte RAM buffer**,
`memcpy`'d wholesale by the Identify handler `0x7ffab518` at `0x7ffab638..0x7ffab649`
(`l32r a12, 0x7ffa1268 = 0x1000`; src/dst from `[a5+0x84]`/`[a5+0x80]`;
`call8 0x7ffba674`). So `TNVMCAP`/`UNVMCAP` are patched into that buffer elsewhere, not
computed per command. **The writer was not found.**

**PROVEN:** the *Identify Namespace* builder `0x7ffaaf90` (StrIds 2021/2022 at
`0x7ffab113`/`0x7ffab1e4`) writes only offsets `0x00`–`0x7c` — NSZE/NCAP/NUSE, NGUID at
`0x68`, EUI64 at `0x78` — computed live from the namespace record.

**INFERRED, not proven:** `unvmcap` most likely derives from the LBN Translation Table's
`numFreeRegions × regionSz` (StrId 605 `hdr: signature… numFreeRegions:%d numValidRegions:%d
regionSz:0x%x…` is the only representation of unallocated space in this firmware). If so,
`unvmcap == 0` means `numFreeRegions == 0`, i.e. the regions are **still booked to a
namespace record** — the namespace table survived. Treat as a hypothesis. To resume: find
the writer of the identify buffer via the pointer pair `[cmdctx+0x80]`/`[cmdctx+0x84]` at
`0x7ffab63b`, or bracket the AdminMgr startup step `0x7ffb2538` (literal `0x7ffa1520`,
loaded at `0x7ffac3b0`/`0x7ffac83d`).

### Read-only probes worth running (cheap, non-destructive)

```sh
nvme id-ctrl /dev/nvmeN -b | od -A d -t x1 -j 3072 -N 32   # OM-6402 "Post Crash Mode" field
nvme id-ctrl /dev/nvmeN -b | od -A d -t x2 -j 516  -N 2    # NN (number of namespaces)
nvme admin-passthru /dev/nvmeN --opcode=0x06 --cdw10=0x10 --data-len=4096 -r   # CNS 0x10 allocated NSID list
nvme id-ns /dev/nvmeN -n 1 -b | od -A d -t x8 -j 392 -N 16 # WD's "record exists" qword
```

The Identify handler `0x7ffab518` does dispatch CNS `0x10`–`0x13` (`movi.n a10,16` @
`0x7ffab5c6`, `17` @ `0x7ffab8c7`, `18` @ `0x7ffab8e1`, `19` @ `0x7ffab8eb`), so the
allocated-NSID list is reachable. **`unvmcap: 0` and an empty allocated-NSID list are
mutually inconsistent** — whichever is stale is highly informative.

Caveats: **byte 3072 is UNKNOWN on this drive** — no StrId mentions it and `libdmi_core`
never reads offset `0xC00` (its only Identify-Controller vendor reads are `0xC09`
`_gf_get_vendor_serial` @ `0x3e170`, `0xC60`–`0xC67` `gfc_get_uefi_version` @ `0x3a540`,
`0xC68`–`0xC6F` `gfc_get_sbl_version` @ `0x3a4e0`). And **`id-ns` byte 392 is a
positive-only test**: the SN200 builder writes only `0x00`–`0x7c`, so a zero there proves
nothing; only a non-zero value is informative.

### ☠ `gf_nvme_ns_status` (opcode 0xDC) is misnamed — do NOT fire it

**PROVEN.** `gf_nvme_ns_status_real` @ `0x8b1b0` sends opcode `0xDC`, NSID = target,
CDW12 = 0, **CDW13 = 0 (attach) / 1 (detach)**, and is called only from `gfc_ns_attach`
@ `0x400a0` and `gfc_ns_detach` @ `0x3fef0`. It is a namespace **attach/detach** command,
not a status query, and it returns no flags. Correcting §10's table accordingly.

The real status discriminator is host-side, `gfc_get_ns_status_internal` @ `0x3fab0`:
`NCAP != 0` → `24001 Active`; else `*(u64*)(idns+0x188) != 0` → `24002 Inactive` (exists,
detached); else → `24000 Invalid` (does not exist).

## 11. Is retrieving the E6 dump a firmware precondition for the erase?

**No — it is tool-side policy only. Now PROVEN in full, with the exact control flow.**

1. **`hgst_nvmec_cap_diags_start`** @ `0x48760` seeds both "dump retrieved" slots with a
   no-data sentinel:
   ```c
   hgstctx->crash_rc = 0xfffff828;   // -2008 = HDMS_DEV_NO_DATA
   hgstctx->pfail_rc = 0xfffff828;
   ```
2. **`hgst_nvmec_cap_diags_get_data`** @ `0x48da0` probes the startup type
   (`0xFF/0x0004`), pulls the E6 dump in 4 KiB chunks, then:
   ```c
   expected = 6;
   if (is_OmahaController && hgst_nvmec_hitachi_block_point_chg_fw())  expected = 7;
   if (expected == startup_type) {            // <-- the whole gate
       hgstctx->crash_rc = rc;  hgstctx->pfail_rc = rc;
   }
   ```
3. **`hgst_nvmec_cap_diags_end`** @ `0x48b20`:
   ```c
   if (diag->rc == 0 && diag->clear_diag_data /* CLI JSON flag */) {
     if (hgstctx->crash_rc == 0)          gf_nvme_clear_crash_dump(dev);   // 0xFF/0x0503
     else if (hgstctx->crash_rc != -2008) trace("Crash dump not retrieved successfully, not cleared");
     ... same for pfail -> 0x0603 ...
     return -6002;   // HDMS_SHUTDOWN_REQUIRED
   }
   ```

The gate is entirely `HGSTNVMeController+0x40`/`+0x44` in **host memory**. The wire
command is a bare `(*vuc_simple)(dev, 0xFF, 3, 5, 0,0,0,0,0,0)` — no preceding read, no
token, no sequence number. `nvme admin-passthru --opcode=0xFF --cdw12=0x0503` is byte-for-
byte what the tool sends.

### ⚠ RETRACTED: the claimed "second silent failure reason" was wrong

An earlier revision of this document claimed `hgst_nvmec_hitachi_block_point_chg_fw()`
tests the **Model Number** and therefore forced `expected = 7` on `HUSMR7676BDP3Y1`,
silently discarding the retrieval result. **That was incorrect.** Decompiled directly:

```c
bool hgst_nvmec_hitachi_block_point_chg_fw(long idctrl) {
  hdm_struct_str(idctrl + 0x40, 8, &s, &len, 0);      // offset 0x40, length 8
  if (*s == 'H') return 4 < (byte)(s[3] + 0xbf);
  return false;
}
```

Identify Controller offset **`0x40`, length 8, is `FR` (Firmware Revision)** — not `MN`
(which is offset `0x18`, length 40). This drive's `FR` is `KNGND1xx`, so `FR[0] == 'K'`,
the test returns **false**, `expected` stays **6**, and the drive reports **6** — the gate
**matches**. (The predicate itself means "Hitachi-branded firmware, revision letter ≥ F".)

**Corrected conclusion: there is exactly ONE reason `dm-cli` fails to clear — the 6.7 MB
E6 pull cannot complete inside the ~5 s window.** The retrieval returns a non-zero `rc`,
that `rc` *is* committed to `crash_rc`, and `cap_diags_end` then takes the
`else if (crash_rc != -2008)` branch and prints
`"Crash dump not retrieved successfully, not cleared"` — the message operators actually
see. Simpler than the retracted story, and consistent with the field reports.

`omc_resolve_device_status` @ `0x674b0` likewise falls through to
`gfc_resolve_device_status` @ `0x3b0b0` for this drive, which tests `startup_type == 6`
→ `HDMS_DEV_DIAGNOSTIC_MODE` (3004). Types 6 and 7 collapse to one bucket; there is no
device-level "hidden vs destroyed" distinction anywhere in the library.

### Firmware-side corroboration

The OAM erase handler's only failure
messages are `Erase to Crash Dump failed` and `Schedule reinit after crash dump erase
failed` — there is **no** "dump not retrieved" message anywhere in the string table, and
no state string implying a retrieved/not-retrieved flag. The three per-section states are
`erased` / `detected` / `invalid` only.

`dm-cli` / `nvme wdc get-crash-dump` retrieve-then-clear and skip the clear if retrieval
fails ("Crash dump not retrieved successfully, not cleared"). The 6.7 MB E6 pull cannot
finish inside the ~5 s window, so the vendor tooling never reaches the clear. That is the
entire trap, and it is in the tool, not the drive.

Note also that the E6 dump is **not** reachable as log page 0xE6 on this drive
(`nvme get-log --log-id=0xe6` → `0x4109` Invalid Log Page); `dm-cli` pulls it through the
`0xC6` VUC. The firmware's E6 manifest at `0x7ff80570` in PROC8 confirms this: the
`CRSHDMP `, `PFCRDMP `, `DRVLOG  ` and `STRTBL  ` entries have **null handler pointers and
zero sizes**, unlike the `L_LOGX*`/`L_FEAX*`/`L_IDEN*` entries which carry real NVMe
opcode descriptors — i.e. the dump sections are gathered by a dedicated VUC path, not by
replaying a standard NVMe command.

---

## 12. The log record on media, and what an "assert" actually is

Companion document: **`docs/sn200-crash-dump-retrieval.md`** — the retrieval + decode
tooling, the full `0xC6` encoding, and the safe operating procedure.

### The record — **PROVEN** from `Log_Emit`

`Log_Emit` is per-image: PROC8 `0x7ffb45a8`, with byte-identical copies at PROC6
`0x7ffbc738`, PROC9 `0x7ffba9d8`, PROC13 `0x7ffb9700`, PROC14 `0x7ffaf470`. The overlay
bank's `0x3002b8e0` is a thunk. PROC0's copy (`0x7ffb0d80`) is the readable one — it uses
plain 3-byte encodings where PROC8 hides the same stores in FLIX bundles.

```asm
7ffb45f4: s32i.n a9,a1,0x8      ; record+0x08 = descriptor
7ffb45f6: rsr    a11,234        ; CCOUNT, the Xtensa cycle counter
7ffb461a: s32i   a11,a1,0x10    ; record+0x10 = timestamp
7ffb463d: loop   a0,0x7ffb4674  ; vararg copy, trip count = nargs
7ffb4665: s32i.n a8,a12,0x14    ; args from record+0x14, stride 4
7ffb4669: extui  a9,a9,0,4      ; nargs is FOUR bits

; PROC0, in the clear:
7ffb0dcf: l32r   a10,0x7ff825a0 ; 0x80000000
7ffb0dd2: or     a10,a3,a10
7ffb0dd5: s32i.n a10,a1,0x8     ; descriptor | 0x80000000
```

```c
struct fw_log_record {
    u32 hdr_a;      /* 0x00  pre-filled, content unknown            */
    u32 hdr_b;      /* 0x04  pre-filled, content unknown            */
    u32 desc;       /* 0x08  0x80000000 | (StrId<<16) | (level<<8) | nargs */
    u32 hdr_d;      /* 0x0c  pre-filled, content unknown            */
    u32 timestamp;  /* 0x10  raw CCOUNT -- CYCLES, NOT WALL TIME    */
    u32 arg[nargs]; /* 0x14  raw 32-bit words, no type tags         */
};                  /* length = 0x14 + 4*nargs  -- VARIABLE         */
```

No core id is stamped — there is no `rsr … PRID` anywhere in `Log_Emit`. **INFERRED**: the
collector attributes a core from *which per-core ring* the record came out of. The ring
writer (PROC8 `0x7ffb4868`) has 1023 slots; the rings are BSS, so runtime addresses and
sizes are not statically recoverable.

### `%s` arguments are StrIds — **PROVEN**

The firmware cannot put a string in a log record. Where it needs one it emits a `%s`
format and passes **the StrId as the argument word**. StrIds 1277–1282 (the per-section
state trichotomy of §2) appear as descriptors *nowhere in any image*; they are reached at
runtime as `1277 + 3*section + state` and printed through StrId 1275 (`%s`). Same trick
for the boot-marker names (3029–3039) and the shutdown types (310–314).

### There is no assert record type — **PROVEN**

The assert idiom is:

```asm
l32r  a10, <log descriptor>
call8 <Log_Emit>
break.n                        ; 2d f0 -- Xtensa BREAK, becomes "LOGIC TRAP"
```

Exhaustive scan of all 18 images: **520 `break.n` sites, 418 (80%) immediately preceded by
a `callN`**, and wherever the target resolved it was that image's `Log_Emit` — 24/24 in
PROC8, 21/21 in PROC0. Examples:

```
PROC8 7ffa6ed9  StrId 1821 lvl 0x20  This is a generated logic trap
PROC8 7ffac46b  StrId 3283 lvl 0x20  AdminMgr: Unexpected startup state 0x%08x for resize 0x%08x
PROC0 7ffaaee1  StrId 1274 lvl 0x20  SYS: Bad startup marker (%08X)
PROC0 7ffb3f3a  StrId   48 lvl 0x20  STK: Overflow detected
```

So **`level == 0x20` is the assert level, and the StrId of that record is the entire
assert identity.** There is no `__FILE__`/`__LINE__` mechanism and no assert format string
in the table. `LOGIC TRAP` (StrId 313) is the shutdown-cause byte the exception path
writes *after* the BREAK, not a distinct record format.

### Breadcrumbs — **PROVEN**

Reader at PROC0 `0x7ffaab28`: **24 slots × 8 raw ASCII bytes** (192 B) at `0x7ff8c8f4`,
printed via StrId 1259 `SYS: Bread crumbs: %c%c%c%c%c%c%c%c`; FCC's slot is dumped
separately in hex (StrId 1260). Boot/shutdown context at `0x7ff8c7ec`: `+0x00` effective
startup marker `0x8000000N`, `+0x0c` PFAIL duration µs, `+0x14` userCapacityGB, `+0xf4`
pending/override marker, `+0xfc` shutdown duration µs, `+0x108` the breadcrumb array.

### The E6 section-descriptor table — **PROVEN**

PROC8 `0x7ff80570`, 40 entries of stride **0x24**, walked by `Admin_BuildE6Entry`
(overflow logs StrId 2950). Fields: `char tag[8]` · `u8 rsvd[3]` · `u8 source_id` ·
`u32 handler` · `u8 flagA/flagB/flagC` · `u8 cmd_class` (1=Identify 2=GetFeatures
3=GetLogPage 4=ReservationReport 0=internal) · `u32 length` (bytes) · `u32 cdw10` ·
`u32 elem_size` · `u32 code`. Verified: `L_LOGX01` len `0x4000`, cdw10 `0x0fff0001` →
LID 1, NUMD 0xFFF = 0x1000 dwords = 0x4000 B ✓.

The four blobs have **null handler and zero length**, confirming a dedicated VUC path:

```
0x7ff80570  "STRTBL  "  handler=0  len=0  code=0x06
0x7ff80594  "CRSHDMP "  handler=0  len=0  code=0x04
0x7ff805b8  "PFCRDMP "  handler=0  len=0  code=0x05
0x7ff805dc  "DRVLOG  "  handler=0  len=0  code=0x0a
```

> Correction: the firmware tag is **`PFCRDMP `**. `PCRSHDMP` is the *host-side* E6 entry
> name `libdmi_core` writes. Different names, same section.

### The armed-section bitfield and UNEXSTRT — **PROVEN**

Settles §4's "sub-command numbering" blocker and §7's UNEXSTRT question. Full write-up:
`docs/sn200-nondestructive-recovery.md`.

- The boot latch (PROC0 `0x7ffaae35`/`0x7ffaae3d`) tests a state byte with two single-bit
  masks: **bit 0 = CRASH armed, bit 2 = PFCRASH armed**; either forces `0x80000009`.
  Bits 1 and 3 are the second bit of each section's 2-bit erased/detected/invalid state.
- `ball`/`bany` in a FLIX slot take a **single-bit immediate mask (`1 << r`), not a
  register**. `xdis.py` prints them correctly now; the earlier register reading is what
  made these masks look like nonsense.
- The OAM erase sub-commands write an EEPROM section id into `[req+0x11c]`, which settles
  the mapping from decoded operands rather than string order: sub 0→6 (System Area),
  1→3 (Bad Block list), 2→9 then 8 (BIST Script, Status), 4→Drive Uninit,
  **5→`0x0b` Crash Dump**, **6→`0x0a` PFail Crash Dump**.
- **UNEXSTRT stamps its stub into the CRASH section (`0x0b`)**, not PFail: the gate at
  `0x7ffaad01` is `ball a14,mask 0x1` (bit 0), the write goes through the crash handle
  `0x7ff85364`, and the failure path reports id `0x0b`. So every unclean start arms the
  section only the destructive clear can release.
- The crash-erase's reinit scheduling is conditional on `*(0x7ff87c64) == 6` (the latched
  mode), so a latched drive always gets the reinit.
- The armed bits are **sticky**: the section-state manager (`0x7ffab010..0x7ffab290`) only
  ever sets them. No clean startup releases them.

### What is still unknown

The crash dump's own **container header** — magic, version, length, CRC, section table.
Only one bit is proven: the UNEXSTRT path (`0x7ffaac43`) edits a staging buffer at
`0x7ff9ff60`, clearing **bit 0 of byte 0** (`movi a11,254`; `s8i`), evidently the
"header valid/complete" flag whose clearing produces the *invalid* third state.

The reason it resisted analysis is worth recording: **the crash-dump writer emits no log
messages at all.** The only crash-dump strings are erase failures, state reports, size
probes and the UNEXSTRT stub — consistent with the dump being written by the PFAIL/trap
handler with logging disabled. The string-table-to-code technique that cracked everything
else has no purchase on a path that logs nothing. An exhaustive 8-char-ASCII scan of all
18 images found **no dump eyecatcher** outside the E6 manifest.

**The `libied.so` ELF/NOTE assert-dump format does NOT apply to the SN200** — it belongs
to an A53+R5 product (its CPU names are `FTP-0/1/2`/`HIP`/`FM-x`, it parses ARM CP15
`dfsr`/`ifsr`/`dfar`/`ifar`, its E6 entry names have zero overlap with Omaha's, and
`ied_decode_assert_dump` has zero call sites anywhere in the dm-cli package). Do not write
an ELF parser for SN200 data.

---

## 12. Re-verification with corrected FLIX decoding (2026-08-03)

Everything in §1–§11 above was written against a **broken disassembler**. Ghidra's stock
`flix.sinc` modelled a FLIX bundle as 3 bytes when it is **8**, so Ghidra resumed decoding
5 bytes inside every bundle and emitted plausible-but-fabricated instructions; ~50% of
executable bytes were affected. The fix is `tools/sn200-fw/ghidra/install.sh`, and
`tools/sn200-fw/xdis.py` now decodes bundle slots A and B directly.

This section re-derives the load-bearing claims from a **synced** instruction stream. Where
a conclusion changes, it is flagged **⚠ OVERTURNS**.

### 12.0 The fix is active, and the old fabrication is reproducible — **PROVEN**

Ghidra now emits `flix.8` (length 8) at `0x30033546`. Forcing a decode at every byte offset
in that range reproduces the old fabrications exactly:

```
30033546  flix.8  0xc7008178, 0xb1ff862e   len 8   <- the real instruction
30033549  l32r    a11, 0x30013b2c          len 3   } all three are
3003354d  bnall   0x30033576, a15, a12     len 3   } bundle payload,
30033553  add.s   f8, f0, f0               len 3   } not instructions
```

**⚠ OVERTURNS:** the `bnall` at `0x3003354d`, previously cited as evidence, is bytes
`c7 cf 25` straddling the tail of the bundle at `0x30033546` and the head of the next
bundle at `0x3003354e`. It never existed. The `add.s` beside it is a floating-point op in
integer control code — the classic tell. Any prior claim resting on an instruction inside
`0x30033547..0x3003354d` is void.

Independent sanity check: `disany.py PROC0 7ffaac30 …` now decodes the entire startup
function with **no address gaps**, every `l32r` landing on a real literal, and every branch
target landing on a real boundary.

### 12.1 The boot latch — **PROVEN**, and the masks are now read directly

Disassembled from the enclosing `entry` at `0x7ffaac30` (starting mid-function desyncs the
stream — this is how the earlier reading went wrong):

```asm
7ffaae28: l32r a12,0x7ff826b8            ; -> 0x7ff9ff60   (boot-state struct)
7ffaae2b: l32i.n a12,a12,0x4             ; a12 = BOOT MODE
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }   ; a5 = 0x7ff8b4f8; mode 4 -> SKIP
7ffaae35: { sync/extw ; ball a9,mask 0x1,0x7ffaaf02 }  ; bit 0 armed -> Post Crash
7ffaae3d: { sync/extw ; ball a9,mask 0x4,0x7ffaaf02 }  ; bit 2 armed -> Post Crash
7ffaae45: l32i.n a11,a7,0x0
7ffaae47: bne a11,a6,0x7ffaae69
7ffaae4a: l32r a10,0x7ff83468            ; LOG 3519 "SYS: Unexpected empty System Area."
...
7ffaaf02: l32r a10,0x7ff83484            ; LOG 3042 "SYS: Detected a CRASH or PFCRASH section."
7ffaaf08: l32r a11,0x7ff83474            ; = 0x80000009
7ffaaf0b: { s32i a11,a7,0x0 ; j 0x7ffaae69 }
```

Confirms: two single-bit `ball` tests, **bit 0** and **bit 2** (not bit 1), both forcing
marker 9. The `ball`/`bany` immediate-mask reading is correct.

**⚠ OVERTURNS a supporting argument (not the conclusion).** The bit→section naming in
`docs/sn200-nondestructive-recovery.md` §2 was derived from the section-state manager at
`0x7ffab010`. Those are **different objects**:

| literal | value | used by |
|---|---|---|
| `*(0x7ff829a8)` | **`0x7ff8b4f8`** | the boot latch (`0x7ffaac38` → a5) |
| `*(0x7ff826d8)` | **`0x7ff8d200`** | the section-state manager (`0x7ffab01d` → a5) |

So "bit 0 = CRASH, bit 2 = PFCRASH" is **INFERRED** (an inference carried across two
distinct byte-sized objects), not PROVEN. It remains the best reading — see §12.2 — but it
should not be quoted as proven.

### 12.2 The latch is self-sustaining, and marker 9 is what sustains it — **PROVEN**

The marker-9 dispatch edge is `0x7ffaaecb: { sync/extw ; beq a11,a14,0x7ffaad01 }` with
`a14 = 0x80000009`. **`0x7ffaad01` is the UNEXSTRT stub writer.**

```asm
7ffaad01: l8ui a14,a5,0x0                                  ; same byte, a5 = 0x7ff8b4f8
7ffaad04: { sync/extw ; ball a14,mask 0x1,0x7ffaac82 }      ; bit 0 SET -> skip the stub
...                                                          ; bit 0 CLEAR -> write it
7ffaad1a: l32r a8,0x7ff82888   ; = 0x48444300  ("HDC\0", BE)
7ffaad17: l32r a9,0x7ff83440   ; = 0x00020100  (version)
7ffaad45: l32r a14,0x7ff83448  ; = 0x53545254  ("STRT", BE)
7ffaad48: l32r a15,0x7ff83444  ; = 0x554e4558  ("UNEX", BE)
7ffaad4b: s32i a15,a5,0x48     ; "UNEX"
7ffaad4e: s32i a14,a5,0x4c     ; "STRT"
7ffaad51: l32r a10,0x7ff825f8  ; -> 0x7ff85364  (the CRASH section handle)
```

So the loop is closed: **marker 9 → if the crash section is empty, stamp an `UNEXSTRT`
stub into it → next boot's bit-0 test fires → marker 9 again.** A bare power cycle cannot
break it, which is exactly what the field control showed.

Note `0x7ff8b4f8` serves both roles — byte `+0` is the section-state byte the latch tests,
and `+0x08 … +0x4c` is the crash-header staging buffer the stub is built in. That resolves
the apparent contradiction between §5 and §5.1 of the non-destructive-recovery doc: it is
one struct, not two.

### 12.3 The erase dispatcher, re-read cleanly — **PROVEN**

Sub-command switch at `0x300336c6` (`l8ui a11,a12,0x8d`). Each arm writes an EEPROM
section id into **`[req+0x11c]`**:

| sub | arm | `[req+0x11c]` |
|---|---|---|
| 0 | `0x30033772` | 6 (System Area) |
| 1 | `0x30033795` | 3 (Bad Block list) |
| 2 | `0x300337b8` | 9 (BIST Status) |
| 3 | `0x30033661` | — (SBL EEPROM, inline block) |
| 4 | `0x300337db` | — (Drive Uninit) |
| **5** | `0x300337fe` | **`0x0b` Crash Dump** |
| **6** | `0x3003374f` | **`0x0a` PFail Crash Dump** |

Confirms sub 5 → `0x0b` and sub 6 → `0x0a`.

Only the **crash** arm does extra work on success:

```asm
300335cd: beqz a11,0x30033704            ; status == 0 (success) -> schedule
300335d0: l32r a10,0x3003337c            ; LOG 1634 "Erase to Crash Dump failed."
...
30033704: l32r a14,0x30033350            ; -> 0x7ff87c64  (startup type)
30033707: l32i.n a14,a14,0x0
30033709: { sync/extw ; bnei a14,6,0x300335bf }   ; != 6 -> skip the schedule
30033711: { s32i a7,a12,0x128 ; movi a15,37 }
30033719: { s32i a15,a12,0x118 ; mov a11,a6 }     ; [req+0x118] = 37 = 0x25
30033721: call8 0x30030aa0
```

Confirms the `bnei a14,6` gate. **Refines a prior hedge:** `0x25` goes into **`[req+0x118]`
(the verb field)** and `[req+0x11c]` (the section-id field) is **never written** on this
path — so `0x25` is definitively *not* an erase of a 38th section. It is a distinct verb.

### 12.4 The boot-marker setter and its producers — **PROVEN, new**

`0x7ffa84c8` is the **boot-marker setter**. It takes the requested marker in `[a2+0x18]`:

```asm
7ffa84dd: { l32r a11,0x7ff82b50 ; mov a0,a9 }        ; a11 = 0x80000003 REINIT
7ffa851b: l32i.n a10,a2,0x18                          ; requested marker
7ffa851d: { l32r a12,0x7ff82b4c ; beq a10,a11,0x7ffa8528 }   ; a12 = 0x80000004 FACTORY
7ffa8525: bne a10,a12,0x7ffa8535
7ffa8528: l32r a10,0x7ff83130   ; LOG 1338 "SYS: Scheduling drive re-init on next startup"
7ffa852e: movi.n a13,1
7ffa8530: s8i a13,a7,0x0        ; a7 = *(0x7ff82ba0) = 0x7ff8cdb4 — "reinit pending" flag
```

**Marker 3 and marker 4 are treated identically by the setter** — both log StrId 1338 and
both set the same pending flag. Whatever distinguishes REINIT from FACTORY REINIT happens
at boot, not at scheduling time.

Two producers post to it (both via `l32r a11,0x7ff82b54` → `0x7ffa84c8`, then
`call8 0x7ffb32f8`):

- **`0x7ffa4306`** (inside `0x7ffa3e48`) — selects between `0x80000004` and `0x80000003`
  from a request parameter at `[a2+0x54]`, then `s32i a9,a5,0x18`. This is the OAM/VUC
  "schedule reinit" service, i.e. the far end of the `0x25` verb from §12.3.
- **`0x7ffabccf`** — see §12.5.

A rate-limit gate exists at `0x7ffa46b1`: while the startup type is 6, if the pending flag
at `0x7ff8cdb4` is already set the request is rejected.

### 12.5 ⚠ Why a firmware activation clears the latch — **PROVEN, and it OVERTURNS the prior prediction**

`docs/sn200-independent-re.md` §6.2 predicted, "INFERRED, high confidence", that a firmware
commit **cannot** clear the latch ("It cannot undo the stub"). The field result refuted
that. Here is the code.

**(a) The firmware-download/activate handler writes boot marker 3.** PROC0 `0x7ffabbf0`:

```asm
7ffabcb7: l32r a10,0x7ff8365c            ; LOG 1366 "SYS: Firmware download flags %08X"
7ffabcc3: l32i a9,a2,0x78                ; the flags word
7ffabcc6: bbci a9,0,0x7ffabd22           ; flags bit 0 clear -> skip
7ffabccc: l32r a11,0x7ff82b54            ; -> 0x7ffa84c8  (the marker setter)
7ffabccf: { l32r a12,0x7ff82b50 ; movi a13,0 }   ; a12 = 0x80000003  REINIT
7ffabcdb: { s32i a12,a2,0x18 ; mov a10,a2 }      ; requested marker = 3
7ffabce3: call8 0x7ffb32f8
```

**A firmware activation schedules a Drive REINIT.** That is the single most important
sentence in this section.

**(b) A LOAD_N_GO boot bypasses the crash-section test entirely.** The boot-mode enum is
**PROVEN** at `0x7ffb4850`:

```asm
7ffb4850: l32i.n a11,a2,0x4
7ffb4852: { sync/extw ; beqi a11,1,0x7ffb49a4 }   ; 1 -> "Firmware Boot Mode : WARM BOOT, DDR (Slot %d)"
7ffb485a: beqz a11,0x7ffb49b0                     ; 0 -> "... COLD BOOT, EEPROM (Slot %d)"
7ffb485d: { sync/extw ; beqi a11,4,0x7ffb499b }   ; 4 -> "... LOAD_N_GO"
7ffb4865: l32r a10,0x7ff83f18                     ; else "... Unknown state (%d)"
```

`[struct+4]` here is the same field the startup path reads at `0x7ffaae2b`
(`*(0x7ff826b8) = 0x7ff9ff60`, offset `+4`). Therefore:

- `0x7ffaae2d`'s `beqi a12,4,0x7ffaae53` — **boot mode 4 (LOAD_N_GO) jumps past both `ball`
  tests and past the empty-System-Area test.** The latch does not fire.
- `0x7ffaaf6b` (the markers 5/6/7 handler) does `bnei a15,4,0x7ffaacea` on the same field,
  and the mode-4 arm is StrId 3043 **"SYS: Load-n-go boot override of failed shutdown."**

So there *is* a boot path that ignores the crash sections, and it is the one a firmware
activation takes.

**The reconstructed sequence** (INFERRED from (a)+(b), consistent with every field
observation):

1. `fw-commit --slot=5 --action=2` → the handler at `0x7ffabbf0` writes boot marker **3**,
   then the controller performs its own FWA shutdown and re-init reset (StrIds 921/922).
   The host sees `controller capabilities changed`, `CSTS=0x0`, `state=dead`.
2. That internal restart comes up **LOAD_N_GO (mode 4)** → the crash/pfcrash predicate is
   skipped → the stored marker 3 is honoured → the drive performs a **Drive REINIT**, which
   reinitialises the System Area and mapping structures and leaves the crash sections
   erased.
3. The cold power cycle then boots normally (mode 0, COLD BOOT/EEPROM). Bit 0 and bit 2 are
   now clear, so nothing forces marker 9. `state=live`, namespace present, `afi 0x44→0x55`.
4. The media is zeroed — **because step 2 ran the re-init**, not because of the power cycle.

This explains the control cleanly: a **bare** cold power cycle is boot mode 0 with no
marker 3 and no bypass, so it changes nothing. Adding the activation supplies both.

**Consequence:** firmware activation is **not** a gentler alternative to `0xFF/0x0503`. It
reaches the *same* destructive re-init by a different door — and it does so
**unconditionally**, without the `bnei a14,6` gate that at least in principle limits
`0x0503`. Its only genuine advantages are the ones already recorded in the skill: it uses
spec-defined commands, so there is no chance of a typo landing on `Erase to SBL EEPROM` or
`Drive Uninit`, and it does not depend on the Post-Crash allow-list.

### 12.6 ⚠ Boot marker 8 "READONLY Startup requested" is dead code — **PROVEN**

Scanned all 18 flat images for every literal-pool word equal to a marker constant and every
reference to it, counting **both** plain 3-byte `l32r` and `l32r` hidden in FLIX slot A
(bits 8–23, base-ISA formula) — the latter is what a naive byte scan misses.

| marker | literal | references |
|---|---|---|
| CLEAN (1) | PROC0 `0x7ff83470` | `0x7ffaae69` (dispatch) · PROC6 `0x7ffb5a72`, `0x7ffbba48` (**writer**) |
| REINIT (3) | PROC0 `0x7ff82b50` | `0x7ffa430c`, `0x7ffabccf` (**writers**) · `0x7ffaadb0`, `0x7ffaae64`, `0x7ffaaef7` (**boot forcers**) · `0x7ffa84dd`, `0x7ffaae74` (compares) |
| FACTORY (4) | PROC0 `0x7ff82b4c` | `0x7ffa4306` (**writer**) · `0x7ffa851d`, `0x7ffaae8c` (compares) |
| POSTCRASH (9) | PROC0 `0x7ff83474` | `0x7ffaaf08` (**boot forcer**) · `0x7ffaaec8` (compare) |
| **READONLY (8)** | PROC0 `0x7ff83478` | **`0x7ffaaed3` only** — the dispatch comparison |

`0x80000008` has **exactly one reference in the entire firmware**, and it is the `beq` in
the dispatch chain that decides whether to branch to the handler at `0x7ffaaff5`. There is
no producer anywhere. (The only other candidate, PROC12 `0x7ffa7d70`, is a false positive:
`0x7ffa7d6d` is a `retw.n` and the bytes after it are a literal pool, not code.)
`0x80000008` is also outside `movi`'s immediate range, so it cannot be materialised without
a literal load.

**Verdict: marker 8 is unreachable.** The handler at `0x7ffaaff5` is bookkeeping only
(`{ movi a11,1272 ; j 0x7ffaac8a }`, StrId 1272 `SYS: Read-only startup`). **There is no
read-only-with-L2P-intact startup path to reach**, and the "SPECULATIVE but worth pursuing"
note in §6 of this document — that setting marker 8 might give a read-only recovery — is
now closed as **not pursuable**.

### 12.7 What is still NOT proven about data destruction

Stated plainly, because it is the decision-critical gap and it is *narrower* than before
but still open:

- **PROVEN:** marker 3 is scheduled by both `0xFF/0x0503`-on-a-latched-drive and by a
  firmware activation.
- **PROVEN:** PROC0's marker-3 boot handler (`0x7ffaaf63` → `0x7ffaaf7d` → `0x7ffaac8a`) is
  **pure bookkeeping** — it records a startup reason and continues. The destructive work,
  if any, is downstream of PROC0's startup function and was not located in this pass.
- **NOT PROVEN:** the specific routine that reinitialises the L2P / mapping tables. No
  `Format`/`Uninit`/L2P-rebuild call has been traced to the marker-3 boot path.
- **Field-established (not code-established):** the media comes back **fully zeroed** after
  both routes. Two independent recoveries, sampled 1 MiB → 3 TB.

So the honest verdict is: **the re-init destroys user data — treat it as certain
operationally — but the destruction is proven from the field, not yet from the code.** The
code proves the *scheduling*, the *trigger conditions* and the *bypass*; it does not yet
show the erase itself. Do not describe the destruction as "PROVEN in the disassembly".

---

## Actionable recovery options

Ranked by confidence. **Read the data-loss warning first.**

**Recommended order of operations on a live latched drive:**
**`0` (find out WHICH section is armed — do this first, always)** → `2` (get an unlimited
window) → `3` (read state + pull the crash dump) → `1` (erase, then clean shutdown, then
restart) → `4` (cold power cycle) — accepting that everything from step 1 onward is
destructive. Options 5–7 are research leads, not procedures.

### 0. Determine which section is armed — **PROVEN encodings, do this before anything else**

§6a proves the boot predicate is `CRASH **or** PFCRASH`, and §4 proves the two clears are
not equivalent: `0x0603` (pfail) is **synchronous and schedules nothing**, `0x0503` (crash)
**schedules the destructive re-init**. So:

```sh
nvme admin-passthru /dev/nvmeX --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b | od -A d -t x4  # CRASH
nvme admin-passthru /dev/nvmeX --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0520 --data-len=8 -r -b | od -A d -t x4  # PFCRASH
```

| result | meaning | action |
|---|---|---|
| PFCRASH non-zero, **CRASH zero** | latched by a power-fail event only | fire **only `0x0603`**. It is synchronous, schedules no re-init — **a non-destructive recovery may be possible.** |
| CRASH non-zero | a real assert (or an UNEXSTRT stub) is latched | clearing it requires `0x0503`, which schedules the re-init → **expect a wipe** |
| both non-zero | both | clear pfail first, re-probe, and decide |

**This measurement was never taken before either recovery on sea1-hv-2.** Both times
`0x0503` was fired blind, which guarantees the destructive path even if only PFCRASH was
armed. It is the single cheapest, highest-information action available.

> ### ⚠ The standard "recovery" is a wipe
> Firing `CDW12 0x0503` schedules a **drive re-init** (§4). On sea1-hv-2 this returned the
> drive to full health with a **completely zeroed namespace**. If the data on a latched
> drive matters, options 1–2 below are the ones to try *before* option 4. Once the reinit
> runs, the L2P is rebuilt and the data is gone.

### 1. Erase, *then* graceful shutdown → restart — **INFERRED, cheap, worth one attempt**
Ordering is the point. Fire the scheduling VUC **first**, then produce a shutdown the
firmware records as clean, then restart. A driver unbind (or `remove`) issues a real
`CC.SHN=01b` and waits for `CSTS.SHST=10b`; a re-bind drives `CC.EN` 0→1.

```sh
BDF=0000:b2:00.0
# 1. schedule the erase (accepts the data loss - see the warning above)
nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0503 --data-len=0
# 2. graceful shutdown, not a reset
echo $BDF > /sys/bus/pci/drivers/nvme/unbind
sleep 10
# 3. restart
echo $BDF > /sys/bus/pci/drivers/nvme/bind
```

Expectation is honestly **mixed** — see §6. `FAST STARTUP` may not consume the reinit
marker, in which case a cold power cycle is still required. But it costs nothing and the
ordering (erase → clean shutdown → start) has not been demonstrated to have been tried.
If it fails, fall through to option 4.

### 2. Take the kernel out of the path to get an unlimited command window — **INFERRED, high confidence**
The AEN cannot be masked (§9). But the controller can only *post* it if the host has an
outstanding Asynchronous Event Request. Linux submits AERs unconditionally; a userspace
driver need not.

```sh
modprobe vfio-pci
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/unbind
echo 1c58 0023 > /sys/bus/pci/drivers/vfio-pci/new_id
# then drive the admin queue from SPDK / a small vfio program, and never submit an AER
```

No AER outstanding ⇒ no Persistent Internal Error AEN ⇒ Linux never calls
`nvme_reset_ctrl()` ⇒ the admin queue stays up indefinitely. This is what makes a 6.7 MB
E6 dump retrieval feasible, and it removes all the 5-second-window gymnastics.

### 3. Read state, and actually retrieve the crash dump — **PROVEN encodings**
```sh
nvme admin-passthru /dev/nvme7 --opcode=0xff -n 0 --cdw12=0x0004   # startup type in CDW0 byte1; 6 = diagnostic
nvme id-ctrl /dev/nvme7 -b | od -A d -t x1 -j 3072 -N 32           # OM-6402 field; may be unpopulated
nvme id-ctrl /dev/nvme7 -b | od -A d -t x2 -j 516  -N 2            # NN
nvme admin-passthru /dev/nvme7 --opcode=0x06 --cdw10=0x10 --data-len=4096 -r  # CNS 0x10 allocated NSIDs
```
The crash dump's own `SYS:` line distinguishes *why* it latched — deallocate assert vs
hang vs empty System Area — which no host-side symptom can. Pull it before clearing.
**This is now a script, and option 2 is no longer a prerequisite.** The body read takes a
dword offset in **CDW13** (§10), so the dump can be pulled in chunks small enough to finish
inside the 5 s window, and reads are PROVEN side-effect-free so a resumable pull is exactly
as safe as a single-shot one:

```sh
cd tools/sn200-fw
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvme7   # stock kernel
sudo ./pull-crash-dump.sh --section all --single-shot     /dev/nvme7   # unlimited window
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin
```

> ☠ **Do not use `nvme wdc get-crash-dump`.** PROVEN from nvme-cli
> `wdc_do_crash_dump()`: on a successful read it automatically issues `0xFF`/`0x0503` to
> clear the dump — which schedules the REINIT that wipes the namespace. `dm-cli`'s
> capture-diagnostics flow does the same. The script above cannot emit `0xFF` at all.

Full encoding, provenance, the recovered log-record format and the step-by-step procedure
are in **`docs/sn200-crash-dump-retrieval.md`**. The on-drive string table (`0x0220`)
decodes the dump and is guaranteed to match the running firmware; prefer it over the
image's copy, since StrIds are not stable across revisions.

### 4. The known-working (destructive) sequence — **PROVEN, but it wipes the drive**
```sh
nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0603 --data-len=0  # pfail, synchronous
nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0503 --data-len=0  # crash, schedules REINIT
# then: graceful shutdown + restart (option 1), or cold power cycle >= 90 s
```
Expect a healthy drive with a zeroed namespace.

### 4a. Things that will NOT work — **PROVEN, do not spend time on these**
| candidate | why not |
|---|---|
| `EXIT_MODE` / `SET_MODE` / `WRITE_MARKER` VUCs | handler functions are **not in any shipped binary**; cannot be unlocked (§10) |
| `dm-cli reset-to-defaults` | refused by `gfc_validate_reset_to_defaults` at startup type 6; and it *resizes capacity* — mildly destructive |
| `nvme attach-ns` (opcode `0x15`) | firmware refuses: `Admin_NamespaceAttachment: The LBN Translation Table is invalid.` Safe, but useless |
| `nvme-cli` raw passthru via `dm-cli` JSON | `nvmens_raw_passthru` is in no table, no vtable, unexported |
| PSID revert / Security Send | PSID path is **ATA-only**; `nvme_security_send_real` has zero callers |
| NSSR / FLR / SBR / link-disable | resets without `CC.SHN` ⇒ `UNEXSTRT` (§7) |

### 4b. NVMe-MI over SMBus — **the one untested avenue, SPECULATIVE**
PROC9 implements a full NVMe-MI/MCTP stack on **both** SMBus and PCIe VDM, including
`MI: Initiating an NVM subystem reset` and an `MI_AdminCmdHandler` tunnel. **SMBus is
independent of the PCIe link**, so this is the only path that survives the `UEFI0067`
link-disable. Whether the tunnel passes vendor opcode `0xFF` is **UNKNOWN**. Requires a
BMC or SMBus master on the card's SMBCLK/SMBDAT. Worth investigating before binning the
drive, because it is the only remaining unknown.

### 5. Try to steer the startup marker to READ ONLY instead of REINIT — **SPECULATIVE**
Marker 8 (`READONLY Startup requested` → `SYS: Read-only startup` → `READ ONLY STARTUP`)
exists and would plausibly bring the drive up with L2P intact and no writes. No VUC that
sets this marker has been identified. Would need the OAM command table from
`libdmi_core.so` / PROC8 disassembly to pursue.

### 6. UART debug console — **SPECULATIVE, hardware access required**
**PROVEN that it exists.** PROC0 (the boot/SBL processor) carries a full interactive
command shell with the prompt `DiagMgr> `, built-in commands
`Help`, `Mode <mode>`, `Load [<command-group>]` ("Makes a new group of commands
available"), `GPRS`, `I2CErase`, `LogicTrap`, `SBL` ("Go into SBL diagnostic mode"),
`Channel Info - PCIE`, plus a UART driver (`Sending suspicious value 0x%x to UART`,
ANSI/CSI escape handling). PROC0 also holds the EEPROM section tags `FRMW`, `DRVC`,
`SLOT`, `CLOG` and the image names `SBL.bin`, `SBLPATCH.bin`, `DCPATCH.bin`,
`DCVUCPATCH.bin`, `BIST.bin`, `SECURITY.bin`, `DriveConfig.bin`.

This is the natural place to set a boot marker or force a read-only startup — the
`Load` command implies whole additional command groups beyond the eight built-ins. But it
needs physical UART pins on the HHHL card, which are not documented in the product
manuals shipped in the package.

### 7. `Read Raw Section` OAM command — **SPECULATIVE**
An OAM command named `Read Raw Section` exists (`OAM READ RAW SA CMD: Read of System Area
journal from EEPROM failed.`). If reachable in Post Crash mode it would allow dumping the
System Area / journal for offline analysis without erasing anything. Encoding unknown.

### Do NOT
- ☠ **Do not sweep the `cmd 3` sub-command space.** Valid range is 0–6 and the two
  immediately below the ones you want are the dangerous ones: **sub 3 ≈ Erase SBL EEPROM**
  (`CDW12 0x0303` — erases the secondary boot loader, hard brick) and **sub 4 ≈ Drive
  Uninit** (`0x0403`). Only 5 and 6 are safe-ish. See §4.
- ☢ **Never issue opcode `0xDD`** (`hgst_nvme_secure_purge`). It is a bare
  fire-and-forget command with no confirmation argument and it destroys everything.
  Its status counterpart `0xDE` (CDW10=0x0C, 48-byte read) is safe and read-only.
- Do not flash firmware; KNGND122 is the newest image and the relevant defects
  (OM-6588/6697/6836/6850/7044) were already fixed in KNGND110.
- Never `nvme format` / `sanitize` / `wdc purge` / `delete-ns`.

### Open questions / where to dig next

Ranked by value. All are now *mechanical* rather than exploratory — the ISA and the log
ABI are cracked (§1).

0. **The PROC9 NVMe-MI admin-command whitelist.** The only unexplored path that could work
   with the PCIe link down. Find `MI_AdminCmdHandler`'s opcode filter (the sites logging
   StrIds 171/172, literals `0x7ffa179c`/`0x7ffa1790`) and determine whether vendor opcode
   `0xFF` passes. If it does, `0x0603`/`0x0503` become issuable over SMBus. **Promoted to
   the top.**
1. **The unmapped `0x7ffbc1xx–0x7ffbe6xx` region.** Nothing in any PROC8 segment covers
   it (PROC8 ends at `0x7ffbb064`), yet the erase case bodies and the E6 descriptor table
   both point into it. It is either shared DDR at runtime or lives in another firmware
   member. Resolving it settles the sub-command → target mapping definitively. **Biggest
   blocker.**
2. ~~**The Post Crash admin whitelist.**~~ **DONE** — it is an allow-list, fully decoded
   in §10, and the FLIX branch model is now implemented in `xdis.py` (§1). `0xC6` with
   cmd byte `0x20` is exempt, so crash-dump retrieval works while latched.
3. **The reinit marker write.** The call site is known (`0x30033704`–`0x30033724`, the
   second `call8 0x30030aa0`), but not the marker value or its storage. The global at
   **`0x7ff87c64`** is the prime suspect — it is read both here (compared `bnei a10,128`
   at `0x30033500`) and at the top of the admin gate (`0x7ffa6b1b`). Finding the write
   would show whether marker 8 (`READONLY Startup requested`) can be set instead of 3.
4. **Whether the completion status is forced to Success** regardless of erase result
   (the common tail at `0x3003357d`).
4a. **The crash dump's container header** — magic/version/length/CRC/section table (§12).
   The one place the log-message-to-code technique cannot reach, because the dump writer
   emits no log messages. Needs a correct Tensilica TIE/FLIX config for these cores.
   The decoder in `tools/sn200-fw/` works around this by deriving the record framing from
   the data rather than assuming a header.
5. **Whether `FAST STARTUP` consumes the `Drive REINIT requested` marker** — decides
   whether a cold power cycle is genuinely mandatory (§6).

Working environment: PROC8 is loaded in Ghidra as `Xtensa:LE:32:default` —
`/sn200/PROC8_7ff80000.bin` @ `0x7ff80000` and `/sn200/PROC8_30000000.bin` @
`0x30000000` (the overlay bank with the `Admin_VUC_*_OVL0xx` functions). Renamed:
`0x7ffa6b18` → `Admin_CheckCmdAllowed_gate`, `0x7ffb45a8` → `Log_Printf_StrIdDesc`.
**But use the hand-rolled disassembler, not Ghidra** (§1). Ghidra MCP tools silently
ignore `program_path` and act on the *current* program — `switch_program` first.
The E6 section-descriptor table is at `0x7ff80570`, 0x24-byte entries of
`char tag[8]` + 7 u32.

### Keep or bin? — the engineering verdict

**The drive is probably not the primary fault. The bay is — and the evidence for that got
*stronger*, not weaker, once the PFAIL path was traced.**

Evidence, in order of strength:
1. **PROVEN (§6a):** the boot predicate is `CRASH or PFCRASH` (two tests, one outcome) →
   force `POST CRASH Startup`, overriding whatever the shutdown recorded.
2. **PROVEN (§8):** an unclean power-off *by itself* does **not** produce this state. PFAIL
   is a designed hold-up sequence whose outcomes are markers 2 / 6 / 7 — none of which is
   Post Crash. So the `ForceOff` alone does not explain latch #2.
3. **PROVEN (§8):** a PCIe link drop cannot reach the PFAIL object — but WD documents
   link-down → hang → **crashed/diagnostic mode** four separate ways, and marks the
   flagship case **"Drive Recovery: Unable to recover."** That is the mechanism that fits.
4. **Field:** POST reported an actual link-training failure on that exact port
   (`UEFI0067`, bus 174 = `0xAE` = `ae:03.0`), and iDRAC logged a fatal component error
   there. The owner already knows the cable is flaky.
5. **Field:** with discard suppressed, the original trigger workload ran clean —
   1-second mkfs, zero resets.

Putting 2 and 3 together: the `ForceOff` is probably *not* what latched it. The marginal
link is. That reframes the fix from "stop doing unclean shutdowns" to "**fix the
interconnect**".

So: **replace the U.2 cable / move the drive to a known-good bay before writing the drive
off.** If it latches again on good cabling with discard suppressed, then bin it. Until
then the observations are fully explained by a marginal interconnect repeatedly inducing
pfail events on a drive whose firmware treats any pfail residue as a reason to hide the
namespace.

Cost asymmetry favours testing: the drive has 0 media errors and full capacity, the cable
is cheap, and the failure mode is non-destructive to hardware.

**Caveat, stated plainly:** the firmware offers no host-side way to make this drive
*tolerant* of unclean power loss. If the deployment cannot guarantee clean shutdowns and
a solid interconnect, this model will keep doing this — it is a design property, not a
defect that can be configured away.

### Prevention
The trigger is large deallocate/TRIM (§8) **and** unclean power/link loss (§8, models
C/D). Until a drive is known to be on KNGND122:
- `mkfs.xfs -K` (skip the whole-device discard), `mkfs.ext4 -E nodiscard`
- mount without `discard`; avoid `fstrim` on the whole device, or run it in small chunks
- LVM: `issue_discards = 0`; ceph: avoid `bdev_enable_discard`
- Never combine heavy deallocate workloads with controller resets or power cycling.
- Suppress discard at the block layer so *nothing* can issue one, rather than relying on
  per-tool flags — a udev rule setting `ATTR{queue/discard_max_bytes}="0"` covers
  filesystems, LVM, ceph and anything else in one place. **Verified effective in the
  field:** the workload that caused latch #1 ran clean afterwards.
- Treat unclean power-off as a hazard, not an inconvenience: always `nvme` shutdown or at
  minimum unbind the driver (which issues `CC.SHN`) before cutting power, and never
  `ForceOff` a running node with this drive attached.
- Fix marginal U.2 cabling *before* deploying data on it. A flaky bay is not a
  performance problem on this drive, it is a data-availability problem.

