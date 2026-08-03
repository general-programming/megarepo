# HGST/WDC Ultrastar SN200 (HUSMR7676BDP3Y1) firmware reverse-engineering notes

Target: 7.68 TB HHHL/SFF NVMe SSD, ASIC codename **Omaha**, firmware family `KNGND1xx`.
Goal: understand the "Post Crash Startup" lockup and find a way out without a chassis
cold power cycle.

Status: living document. Claims are labelled **PROVEN** (read directly out of a binary /
string table) or **INFERRED** (reasoned from structure) or **SPECULATIVE**.

Companion runbook: `.claude/skills/nvme-recovery/SKILL.md`.

---

## Summary

1. **Why the crash erase returns Success but does nothing.** It is not an erase — it is a
   *scheduling* call. It sets the `Drive REINIT requested` boot marker so the erase runs
   at the next startup (`OAM ERASE CMD: Schedule reinit after crash dump erase failed.`).
   The pfail erase (`0x0603`) is synchronous, which is why only *it* clears immediately.
   **And the scheduled reinit is what zeroes the namespace — the recovery, not the crash,
   destroys the data.** (§4)
2. **Is there a VUC that exits Post Crash mode?** **No.** All 21 dispatch tables in
   `libdmi_core.so` were enumerated: `EXIT_MODE`/`SET_MODE`/`WRITE_MARKER` exist in the
   command-id enum but in no table, and SN200's `omc_cmds` has no `RESET_TO_DEFAULTS`.
   WD's own tool returns `HDMS_SHUTDOWN_REQUIRED` after a clear. Full 30-entry VUC map
   in §10. (§10)
3. **What triggers the crash?** Large deallocate/TRIM — confirmed by WD's own release
   notes (OM-6588 "failed to restore L2P table after large deallocate and a pfail",
   OM-6850, OM-6836, OM-7044) and by a firmware watchdog dedicated to outstanding trims.
   A whole-device TRIM races the L2P journal flush. (§8)
4. **Can the AEN be suppressed?** Not by masking — Persistent Internal Error is an
   Error-class AEN and the firmware's maskable list contains only the SMART warnings and
   the two notice bits. The `0x400b` on `set-feature 0x0b` is `Invalid Namespace or
   Format`, i.e. the missing namespace, not an unsupported feature. But the AEN can be
   **starved**: bind to `vfio-pci` and never submit an Asynchronous Event Request. (§9)
5. **Is the E6 retrieval a firmware precondition for the erase?** **No — tool-side only**,
   two ints in host memory. On this exact model it fails a *second*, silent way: a
   model-string heuristic expects startup type 7 while the drive reports 6, so the
   "retrieved" flag is never committed and the clear is skipped with no message. (§11)

Newly identified and not previously documented: the **`UNEXSTRT`** mechanism (§7) — every
start not preceded by a recorded clean shutdown re-writes a stub crash header, which is
why no in-band reset ever breaks the loop.

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

Parser: `scratchpad/sn200/segparse.py` (in the session scratchpad).

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

Ghidra 12.1.2 ships an `Xtensa` processor module, so these load natively.
Flat images (gaps zero-filled) were generated per-proc into `scratchpad/sn200/flat/`.

Sixteen Xtensa cores + a separate `FCC` (Flash Channel Controller) core, connected by a
message-passing fabric (`StarMgr: MsgId, Qid, srcNode, thisNode, dstMgr, srcMgr, Handle,
cmd/rsp, dstQIdx`), matching the "Star" NoC naming throughout the string table.

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

and a parallel human-readable set at lines 1266–1275:
`SYS: Normal startup` / `SYS: PFAIL startup` / `SYS: Drive re-init` /
`SYS: Drive re-init to factory defaults` / `SYS: First time startup` /
`SYS: ERROR - Shutdown started but never finished` /
`SYS: ERROR - PFAIL started but never finished` /
`SYS: ERROR - PFAIL started but timed out before completing` /
`SYS: Read-only startup` / **`SYS: Post Crash startup`** /
`SYS: Bad startup marker (%08X)`.

**INFERRED — the marker enum and the `SYS: %s` startup-reason enum are 1:1 parallel**, with
the reason array (StrIds 1265–1274) indexed by a distinct *startup type*:

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

**Caveat, unresolved.** The `0xFF/0x0004` VUC returns a *startup type* in CDW0 byte 1, and
`libdmi_core` treats **6** as diagnostic mode on the GallantFox path but **7** on newer
Omaha firmware (`omc_resolve_device_status`). Neither 6 nor 7 is `Post Crash startup` (9)
in the table above, so the VUC's enum is **not** the same enum — it shifted between
product generations, which is exactly the discrepancy `omc_resolve_device_status` exists
to paper over. Do not assume the VUC value indexes the `SYS:` array.

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

### The answer

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
   (PROVEN, StrId 1634 `OAM ERASE CMD: Drive Uninit failed.`), sitting immediately
   adjacent to the two crash-dump erases in the same switch.
4. On sea1-hv-2 the drive came back **healthy at full capacity with a completely zeroed
   namespace** — no GPT, zeros at every offset sampled to 3 TB — while `nuse == nsze`.

A "drive re-init" rebuilds the system area and the L2P mapping from scratch. That is
exactly what produces a fully-provisioned, entirely-zero namespace on a drive with no
media damage. **The data was not lost by the original crash; it was lost by the
recovery.** The erase-then-power-cycle procedure *is* a wipe, dressed up as a dump erase.

If a future SN200 is latched and the data matters, this is the key decision point — see
"Actionable recovery options".

### Open question: the sub-command numbering is NOT confirmed, and probing it is dangerous

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

**Resolved: the string order is NOT the sub-command order.** `libdmi_core.so` proves
`gf_nvme_clear_crash_dump` = cmd 3 / sub **5** and `gf_nvme_clear_pfail_dump` = cmd 3 /
sub **6**. And the SN150 (`KMGNP131`) string table settles why the order is misleading —
it contains a *longer, differently ordered* variant of the same block:

```
Erase to system area 0 / system area 1 / bad block table 0 / bad block table 1 /
BIST script / SBL EEPROM / Received Bad Erase sub-cmd / BIST Script / BIST Status /
Crash Dump / PFail Crash Dump
```

i.e. these strings are ordered by source-file appearance across *two* generations of the
switch, not by case value. Do not derive sub-command numbers from them.

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

So the durable, high-confidence conclusion here is the **diagnosis**, not a new cure: the
loop is sustained by `UNEXSTRT`, and no in-band reset that omits `CC.SHN` can ever break
it. Whether `CC.SHN` + `CC.EN` cycling is *sufficient* remains open, and is worth one
cheap experiment (see Actionable option 1) because it costs nothing.

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

### Conclusion (**INFERRED**, high confidence)

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
| 26 | `gf_nvme_ns_status` | 0xDC | 0 | 0x0000 | none | |
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

### There is no reinit / exit-diagnostic VUC — **PROVEN negative result**

All 21 `*_cmds` dispatch tables in `.data` were enumerated and the command-id enum decoded
(`HDME_CMD_ENUMS_enum_strs` @ `0x2de280`, **value = 23000 + index**). The enum *names*
`HDME_CMD_EXIT_MODE`(23051), `SET_MODE`(23041), `GET_MODE`(23015), `WRITE_MARKER`(23046),
`ERASE_FA_DATA`, `READ_FA_DATA`, `RUN_TDD`, `TRANSLATE_L2P/P2L`, `READ/WRITE_SYSTEM_FILE`
all exist but **appear in zero dispatch tables** — dead API surface shared with other WD
tools. SN200's `omc_cmds` @ `0x2e7de0` supports only `GET_INFO, GET_STATE,
GET_STATISTICS, LOCATE, MANAGE_NAMESPACES, MANAGE_UEFI, SANITIZE, SECURE_ERASE` plus
inherited base commands, and notably **no `RESET_TO_DEFAULTS`** (that exists only on the
SN100/150 `gfc_cmds`).

Also **PROVEN red herrings**:
- "Drive uninit" in `dm-cli` (`HDMS_UNINITIALIZED`) is `nvmec_prepare_for_removal` — pure
  host-side PCIe hot-remove via sysfs, not a device command. It is unrelated to the
  firmware's `Drive Uninit` OAM erase sub-command.
- "reset controller" in the library is the Linux `NVME_IOCTL_RESET` ioctl, not a VUC.
- No `BE_TMODE`, no `kill`, no boot-marker writer, no clear-assert anywhere in
  `libdmi_core`, `libied`, `libetd`, `libe6text`, `libau_utils`, `libcu`.

The only generic escape hatch is `nvmec_raw_passthru` @ `0x524d0` /
`nvmens_raw_passthru` @ `0x5fe10`, which accept a **64-byte raw NVMe command blob** from
JSON and hand it to `nvme_send_and_check_cmd`. Not wired into any dispatch table in this
build.

After a successful clear, `cap_diags_end` returns `HDMS_SHUTDOWN_REQUIRED` (−6002) — WD's
own tooling expects a **power cycle**, consistent with there being no exit command at all.

### Status code decoding — **PROVEN**

`nvme_decode_status` @ `0x8d050`: `sc = s & 0xFF`, `sct = (s>>8) & 7`, `more = (s>>13)&1`,
`dnr = (s>>14)&1`.

**`0x7D3` (SCT=7 vendor specific, SC=0xD3) is not decoded anywhere.** `nvme_check_success`
@ `0x8d6a0` handles SCT 0/1/2 only; SCT 3–7 fall through to a generic `-3`. There is no
vendor status table in the library.

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

### The three admin rejection gates

**PROVEN** — three consecutive checks in the admin command handler, in this order:

```
StrId 1804  Admin cmd rejected due to Post Crash startup mode: 0x%x   -> status 0x7d3
StrId 1805  Admin cmd restricted by VUC Control disabled: 0x%x
StrId 1806  Admin cmd restricted by purge phase 0x%x: 0x%x
```

The Post Crash gate is first and is what returns SCT=7 / SC=0xD3.

### A clean, standard-command state probe

**PROVEN (from WD's release notes, OM-6402):**

> Added new field **"Post Crash Mode (Byte 3072)"** at the start of the Vendor Specific
> area in the Identify Controller structure.

So a plain `nvme id-ctrl` (opcode 06h, CNS=1) exposes the post-crash state at **byte
3072** of the 4096-byte Identify Controller structure — no vendor passthru needed.
Added in KNGND110.

```sh
nvme id-ctrl /dev/nvme7 -b | od -A d -t x1 -j 3072 -N 16
```

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

### A second, sharper reason `dm-cli` never clears on *this* model

**PROVEN, and this is new.** `hgst_nvmec_hitachi_block_point_chg_fw()` tests
`IDCTRL.MN[0]=='H' && ((MN[3]+0xBF) & 0xFF) >= 5`. For `HUSMR7676BDP3Y1`, `MN[3]=='M'`
(0x4D), so `(0x4D+0xBF)&0xFF = 0x0C = 12 ≥ 5` → **true**, so `expected = 7`.

But the drive reports startup type **6** (`0x00000601`). `expected != startup_type`, so
`crash_rc` is **never** overwritten and stays at the `-2008` sentinel. In
`cap_diags_end` that hits the `else if (crash_rc != -2008)` branch — which is **false** —
so the clear is skipped **silently, with no message at all**.

So `dm-cli` fails to clear this drive for two independent reasons: the 6.7 MB E6 pull
cannot finish inside the 5 s window, *and* a model/firmware heuristic mismatch means the
retrieval result is discarded even if it did. Neither is a drive-side restriction.

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

## Actionable recovery options

Ranked by confidence. **Read the data-loss warning first.**

**Recommended order of operations on a live latched drive:**
`2` (get an unlimited window) → `3` (read state + pull the crash dump) → `1` (erase, then
clean shutdown, then restart) → `4` (cold power cycle) — accepting that everything from
step 1 onward is destructive. Options 5 and 6 are research leads, not procedures.

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
nvme id-ctrl /dev/nvme7 -b | od -A d -t x1 -j 3072 -N 16   # Post Crash Mode field (KNGND110+)
nvme admin-passthru /dev/nvme7 --opcode=0xff -n 0 --cdw12=0x0004   # startup type in CDW0 byte1
```
With an unlimited window (option 2) the dump is genuinely retrievable — `dm-cli` is not
required, and neither is its broken gate:
```sh
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r   # size
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=0x4000 --cdw12=0x0420 \
     --data-len=65536 -r > crash.part                                                        # body
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0120 --data-len=8 -r   # strtbl size
nvme admin-passthru /dev/nvme7 --opcode=0xC6 -n 0 --cdw12=0x0220 --cdw10=<sz/4> --data-len=<sz> -r
```
The on-drive string table (`0x0220`) decodes the dump, and matches
`StringTable.csv` from the matching firmware image. Doing this *before* clearing would
tell you exactly which assert fired.

### 4. The known-working (destructive) sequence — **PROVEN, but it wipes the drive**
```sh
nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0603 --data-len=0  # pfail, synchronous
nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0503 --data-len=0  # crash, schedules REINIT
# then: graceful shutdown + restart (option 1), or cold power cycle >= 90 s
```
Expect a healthy drive with a zeroed namespace.

### 5. Try to steer the startup marker to READ ONLY instead of REINIT — **SPECULATIVE**
Marker 8 (`READONLY Startup requested` → `SYS: Read-only startup` → `READ ONLY STARTUP`)
exists and would plausibly bring the drive up with L2P intact and no writes. No VUC that
sets this marker has been identified. Would need the OAM command table from
`libdmi_core.so` / PROC8 disassembly to pursue.

### 6. `Read Raw Section` OAM command — **SPECULATIVE**
An OAM command named `Read Raw Section` exists (`OAM READ RAW SA CMD: Read of System Area
journal from EEPROM failed.`). If reachable in Post Crash mode it would allow dumping the
System Area / journal for offline analysis without erasing anything. Encoding unknown.

### Do NOT
- **Do not sweep the `cmd 3` sub-command space.** Adjacent sub-commands include
  `Erase to SBL EEPROM` (hard brick) and `Drive Uninit`. See the warning in §4.
- ☢ **Never issue opcode `0xDD`** (`hgst_nvme_secure_purge`). It is a bare
  fire-and-forget command with no confirmation argument and it destroys everything.
  Its status counterpart `0xDE` (CDW10=0x0C, 48-byte read) is safe and read-only.
- Do not flash firmware; KNGND122 is the newest image and the relevant defects
  (OM-6588/6697/6836/6850/7044) were already fixed in KNGND110.
- Never `nvme format` / `sanitize` / `wdc purge` / `delete-ns`.

### Prevention
The trigger is large deallocate/TRIM (§8). Until a drive is known to be on KNGND122:
- `mkfs.xfs -K` (skip the whole-device discard), `mkfs.ext4 -E nodiscard`
- mount without `discard`; avoid `fstrim` on the whole device, or run it in small chunks
- LVM: `issue_discards = 0`; ceph: avoid `bdev_enable_discard`
- Never combine heavy deallocate workloads with controller resets or power cycling.

