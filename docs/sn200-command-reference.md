# SN200 (`HUSMR7676BDP3Y1`, firmware `KNGND122`) — command reference

Authoritative, single-source reference for every NVMe admin/vendor command
whose behaviour on this drive has been established by static firmware
analysis. **Meant to be acted on directly** — this owner issues commands from
this set against five live drives as part of a recovery runbook, and some of
these commands destroy drives irreversibly.

This document is a consolidation, not new research. Sources: `docs/sn200-dangerous-commands.md`,
`docs/sn200-attack-surface.md`, `docs/sn200-firmware-re.md` (particularly §13,
which re-verified earlier sections against a corrected FLIX/VLIW decoder and
overturned several of them), `docs/sn200-independent-re.md`,
`docs/sn200-nondestructive-recovery.md`, `docs/sn200-crash-dump-retrieval.md`,
`docs/sn200-firmware-flashing.md`, `docs/sn200-readonly-startup.md`,
`docs/sn200-logic-escapes.md`, `docs/sn200-field-evidence.md`,
`.claude/skills/nvme-recovery/SKILL.md`, and `tools/sn200-fw/{pull-crash-dump.sh,
check-latch-state.sh,fill-fw-slots.sh}`.

Labels: **PROVEN** (read directly off the instruction stream or off a live
drive), **INFERRED** (follows from proven facts plus one named assumption),
**SPECULATIVE** / **UNKNOWN** (not established — stated as such, never
guessed). Where sources disagree, this document states which is right and
why the other was wrong; nothing here silently picks a side.

---

## 1. The selector encoding — read this before anything else

For every command in this document: **`ctx+0x38` = `CDW12[7:0]`** (the "cmd"
byte), **`ctx+0x39` = `CDW12[15:8]`** (the "subcmd" byte). `ctx` is the
firmware's compacted command-context struct, not a verbatim 64-byte SQE:
`CDW0` sits at `ctx+0x18`, NSID at `ctx+0x1c`, four dwords of pointer/PRP
material at `ctx+0x20..0x2f`, then `CDW10..CDW15` at `ctx+0x30..0x44`.

**PROVEN**, confirmed against a command whose CDW10/CDW11 semantics are fixed
by the NVMe spec rather than by vendor convention: Firmware Image Download
(opcode `0x11`) defines `CDW10 = NUMD`, `CDW11 = OFST`. Its handler
(`PROC8@30000000 0x30025590`) reads NUMD from `ctx+0x30` and OFST from
`ctx+0x34` (`0x300257e5: l32i a10,a2,0x134` = OFST; `0x300257f1: l32i
a15,a2,0x130` = NUMD; own strings: StrId 2177 "exceeds DDR allocation", 2182
"NUMD: %d OFST: %d"). That pins `ctx+0x30 = CDW10`, so `ctx+0x38 = CDW12`
follows by simple offset arithmetic, and it is independently confirmed by the
raw-flash-read family's own log strings, which literally spell the encoding
`0xCA/0x03/0x01` and `0xCA/0x03/0x02` — `0x03` is the proven value of
`ctx+0x38`, `0x01`/`0x02` the proven values of `ctx+0x39`.

**Two earlier static readings were wrong, and both are recorded in older
notes — do not trust either if you find them:**

- **"CDW8 / PRP2" reading.** Assumed a verbatim 64-byte SQE starting at
  `ctx+0x18`, which places `ctx+0x38` at PRP2. Wrong because the struct is
  compacted, not verbatim.
- **"CDW10" reading.** Derived the SQE base from a `memset(ctx+0x100, 0,
  160)` / `memcpy(..., 64)` call length rather than from a DMA fill, and
  concluded `ctx+0x38 = CDW10`. Also wrong, for the same compaction reason —
  and this reading was actively dangerous, because if true, a command
  believed inert on "CDW12 is zero" could have carried a live selector in
  CDW10. §1 of `sn200-crash-dump-retrieval.md` and §4.4 of
  `sn200-independent-re.md` narrate the resolution in detail; treat any
  older SN200 note that lists `CDW10` as selector-bearing as superseded by
  this document.

`CDW10` on `0xCA`/`0xFF`/`0xC6` is a **length** (dwords), not a selector.
`CDW11` is an **offset** (dwords) where used. See §3 for why `CDW13` is
selector-*grade* dangerous despite not being a selector.

---

## 2. Safety classification, per command

### SAFE / read-only

| command | encoding | what it does |
|---|---|---|
| `0xFF` startup-mode probe | `CDW12 = 0x0004` | Returns `(startup_type<<8) \| flags` in CQE DW0. Pure read, no data transfer, no media touch. **The correct first command on any suspect drive.** Field-confirmed: returned `0x00000601` on a latched drive → startup type 6 = `INVALID`/diagnostic. |
| `0xFF` raw System-Area read | `CDW12 = 0x0007` (`OAM READ RAW SA CMD`) | DMAs the System-Area journal from EEPROM to host. Pure read; not in `libdmi_core`. Reads the drive's *actual* current startup marker instead of inferring it. |
| `0xC6` size probes | `CDW10=2`, `CDW12 = 0x0320` (crash) / `0x0520` (pfail) / `0x0120` (drive-log + string-table, shared: dword[0]=drive-log size, dword[1]=string-table size), `--data-len=8` | 8-byte read. Success = section armed; **failure with SC `0xC3`** = not armed (the *value* returned, `0x00320000`, is a fixed reservation and is never informative — only the status code is). |
| `0xC6` body reads | `CDW10=<dwords>`, `CDW12 = 0x0420` (crash) / `0x0620` (pfail) / `0x0220` (string table) / `0x0020` (drive log). **There is no offset field** — CDW11/13/14/15 are never read (PROVEN, `sn200-crash-dump-retrieval.md` §1.2.4), so the read must be single-shot and always starts at the section base. | Read of the section body, truncated to `min(section_size, CDW10*4)`. Side-effect-free: the arm stores only `ctx+0x28/0x2c` (source, recomputed from a firmware descriptor each call), `ctx+0x34` (length) and status; no store to the descriptor, no store to media, no erase primitive on any path. |
| `0xC6` sub 7 / sub 8 | `CDW12 = 0x0720` / `0x0820` | **DO NOT SEND.** Unidentified 71808-byte region. Unlike subs 0–6 these do not point at an existing section — they spawn a producer coroutine (`0x7ffa972c` / `0x7ffa43c0`), and `0x7ffa43c0` mutates a DRAM counter table. Not certified as a pure read. |
| `0xCA` raw NAND page read | `CDW12 = 0x0003`/`0x0103`/`0x0203` (i.e. cmd `0x03`, sub `0`/`1`/`2`), `CDW13=<flash addr>` | `Flash_ReadRawData`/`Flash_ReadCacheData`, handler `0x30036e28` (OVL026). Length clamped to 640 bytes before use (`0x30037039: movi a11,640 / minu a10,a10,a11`). Data-disclosure surface, not a write path. Reachable while latched (`0x03` is allow-listed) — this is a real, if minor, surface expansion, not a way out of the latch. **This row said `0xC6` until 2026-08-04 and that was wrong.** The firmware's own strings spell the encoding `0xCA/0x03/0x01` and `0xCA/0x03/0x02`; `0xC6` does not use this dispatch table at all (opcode 198 dispatches at `0x7ffa7bf5` to one handler at `0x7ffbea44`), and the gate admits `0xC6` **only** with cmd byte `0x20`/`0x30`, so the row's own "reachable while latched" clause was self-inconsistent. See `sn200-vuc-flash-read.md` §5. |
| `0xCA` `Admin_VucFlashVirtualToPhysical` | `CDW12 = 0x0008` | Handler `0x30035968`. Pure `remu`/`quou`/`mull` arithmetic over the geometry tables at `0x7ff821b0`/`0x7ff82110`; result to `ctx+0x54`. **No media access on any path.** Allow-listed while latched. Converts a virtual (die/blockset) address to a physical flash address — it is *not* an L2P lookup and cannot tell you where an LBA lives. |
| `nvme fw-log` | opcode `0x02`, LID `0x02` | Reads slot revisions (`frsN`) and pending-activate state (`afi`). Always safe. |
| `nvme id-ctrl` | opcode `0x06`, CNS `0x01` | Always safe; check `fr`, `frmw`, `tnvmcap`/`unvmcap`. |
| `nvme smart-log` / `error-log` | opcode `0x02` | Always safe. |
| `0xFF/0x0403` size/status reads via `0xC6`/`0x20` subs 0–8 | `VUC Get Drive Log`, all reads | All arms of this sub-dispatch (`0x30030d14`) are reads into the `.CDH`-magic reader; subs 7–8 are unidentified but structurally reads, not writes. |
| `0xC6`/`0x30` "VUC Reset Drive Stats" | cmd byte `0x30` | Zero-length internal control/handshake; four independent structural checks (§4.2 of `sn200-attack-surface.md`) agree it is not a host data-transfer feature. Writer of *statistics only* — INFERRED, not a media or boot-state write. |

### DESTRUCTIVE BY DESIGN (known, intentional, sometimes the only recovery tool available)

| command | encoding | effect |
|---|---|---|
| `0xFF` crash-dump erase | `CDW12 = 0x0503` (cmd `0x03` sub `5`) | Erases EEPROM section `0x0b` (CLOG). **On a latched drive (startup type == 6, `INVALID`) this schedules a Drive REINIT** — PROVEN gate: `bnei a14,6` on `*(0x7ff87c64)` at `0x30033709`. The REINIT is what actually destroys data: it runs `Admin_NamespaceStartup`, `memset`s both LBN translation tables, and creates namespaces fresh (`sn200-firmware-re.md` §13.7, PROVEN in code — this closed what was previously an open question). Field-observed: drive returns healthy, full capacity, **completely zeroed namespace**. Fired from an already-*normally-booted* (non-latched) drive, the same `bnei` gate makes this a plain section erase with no scheduled reinit — but reaching "normally booted" requires the section to already be clear, which is circular on a latched drive (see §3 of `sn200-nondestructive-recovery.md`). |
| `0xFF` pfail-dump erase | `CDW12 = 0x0603` (cmd `0x03` sub `6`) | Erases EEPROM section `0x0a` (PFCL). **Synchronous, no second operation, sets no boot marker.** Byte-for-byte identical handler to `0x0503` except the section-id constant. If **only** the PFAIL section is armed (probe `0x0520` non-zero, `0x0320` zero), this alone may lift the latch with **no reinit and no data loss** — but UNEXSTRT (any unclean start) arms the **CRASH** section specifically, so this case is expected to be rare after a real power event. |
| `nvme fw-commit --action=2` on a slot whose image has flags-word bit 0 set | opcode `0x10`, `CA=2` | **PROVEN in code AND in the field (sea1-hv-2, 2026-08-04):** activation writes boot marker 3 (Drive REINIT requested) via PROC0 `0x7ffabbf0`, gated on `bbci a9,0` — **bit 0 of the *target image's own* flags word**, not a property of the commit action you choose. This is why a firmware activation was seen to clear the Post-Crash latch: it reaches the *same* destructive re-init as `0x0503`, by a different door, and does so **unconditionally** (no `bnei a14,6` gate at all). Field result: drive came back healthy, **fully zeroed** (sampled 1 MiB → 1 TiB). Its only advantage over `0xFF/0x0503` is using spec-defined opcodes only — no chance of a typo landing on SBL-erase or Drive-Uninit, and no dependence on the Post-Crash allow-list. It has **no advantage in data cost.** |

### CATASTROPHIC — NEVER SEND

| command | encoding | effect |
|---|---|---|
| ☠ Raw NAND block erase | `0xCA`, `CDW12[7:0] = 0x0F`, **any** `CDW12[15:8]`, `CDW13 = <flash addr>` | PROVEN: erases a physical NAND block at the address in `CDW13`. **`CDW12[15:8]` is completely ignored on this path** — exhaustive scan of the whole erase coroutine (overlay 31, `0x3003dbe0`–`0x3003dd38`) found no `l8ui` of `ctx+0x39` anywhere in it. There is **no harmless sub-value.** Reachable on a latched drive with no unlock: `0x0F` is one of the 12 allow-listed `0xCA` sub-values. Unrecoverable data loss. |
| ☠ Raw NAND page write / program | `0xCA`, `CDW12 = 0x0010` (`Flash_WritePageRaw`, SLC/MLC selected by an internal `b0` flag, not by the host) or `0x0110` (`Flash_ProgNANDPageRaw`), `CDW10 = <len dwords>`, `CDW13 = <flash addr>`, data-out | PROVEN: writes host-supplied data, including spare/ECC bytes, directly to a NAND page. Reachable while latched (`0x10` is allow-listed). No absolute transfer-length bound was found on this path beyond a `CDW10*4 == bytes_transferred` consistency check (the raw-*read* family's 640-byte clamp does **not** apply here). |
| ⚠ (adjacent, not itself destructive but one keystroke away) | `0xCA`, `CDW12 = 0x0210` | Fetches the `Flash_ProgNANDPage` result dword. Does not program anything, but shares the same entry coroutine as `0x0010`/`0x0110`. Do not use as a "probe" — the entire `CDW12[7:0]=0x10` family should be treated as off-limits. |
| ☠ Drive Uninit | `0xFF`, `CDW12 = 0x0403` (cmd `0x03` sub `4`) | Posts re-init verb `0x25` with parameter 1 → selects the **FACTORY** re-init marker (marker 4). **No startup-type gate at all** — unlike sub 5, whose reinit-scheduling is guarded by `bnei a14,6`, sub 4 jumps straight to the post at `0x300337e3` unconditionally. Allow-listed while latched. One hex nibble from `0x0503`. |
| ☠ Erase to SBL EEPROM | `0xFF`, `CDW12 = 0x0303` (cmd `0x03` sub `3`) | Erases the secondary boot loader's EEPROM section (EEPROM section id `13`, "SBL"). **Permanent brick** — the drive will not POST again. Reached by a different code shape than the other erase arms (calls the EEPROM primitive `0x30031d10` directly, not the flash-erase primitive `0x30030aa0`). Sub `3` is allow-listed while latched. |
| ☢ Start Secure Purge | `0xDD` | Whole-drive crypto erase, fire-and-forget, **no confirmation argument**. **Rejected while latched** — it is the one command in this table that a latched drive actually refuses (`0x7C5`, via the separate *sanitize* gate at `0x7ffa6cb0`, not the Post-Crash gate). Never type it regardless; a drive that is not latched will execute it. Its status counterpart `0xDE` (`CDW10=0x0C`, 48-byte read, `-r`) is safe and read-only. |
| ☢ Multiplane Write / Multiplane Erase | `0xCA`, `CDW12[7:0] = 0x37`, sub-selector space unanalysed | Live write/erase surface on a healthy (non-latched) drive; strings confirm both write and erase live under this command byte. **Not** on the Post-Crash allow-list, so unreachable while latched — the one piece of good news in this family. Do not probe its sub-selector space; not analysed and not reachable is not the same as safe. |

### UNKNOWN — do not send

Everything not positively identified above. Explicitly, from the allow-listed
surface: `0xCA` sub-values `0x04`, `0x11`, `0x21`, `0x32` (allow-listed, carry
no destructive log strings, but `0x11` and `0x21` carry **no log strings at
all** — unaudited, not proven clean); `0xC6`/`0x20` sub-commands `7` and `8`
(route into the same `.CDH`-magic reader as the known-safe subs, but
unidentified); `0xEC` (dispatches to overlay handler `0x7ffbc24c`; semantics
unresolved — the pointer never resolved under any recovered overlay delta,
despite `0xEC` itself being allow-listed and unconditionally present in the
gate). Naming these is not an invitation — every one of them is a neighbour
of a command that destroys a block or the drive. **Never issue a command
whose full CDW10–CDW13 encoding is not in the SAFE or explicitly-accepted
DESTRUCTIVE rows above.**

---

## 3. The inert-command rule

A vendor command is **only** reliably inert when `CDW10`, `CDW11`, `CDW12`
**and `CDW13`** are all zero.

`CDW13` carries the **raw physical flash address** for the entire raw-flash
family (`0xCA` cmd `0x03`/`0x0F`/`0x10`) — PROVEN directly from the
firmware's own `%08x` log arguments (StrIds 1875–1878, 3465, all logged with
the value fetched from `ctx+0x3c`). It is not itself a *selector* (it doesn't
choose which operation runs — `CDW12` does that), but it is exactly as
dangerous as one: zeroing `CDW12[7:0] = 0x0F` still erases *a* block, just
block zero instead of the one you meant. `CDW10` carries the write length for
the raw-flash-write family and must also be zero on any command not
deliberately using it.

**The spacing between safe and catastrophic values is deliberately tight, and
this is the single most important operational fact in this document:**

- `0x0F` (block erase) is **two** decimal values from `0x11` (a benign,
  allow-listed, four-instruction stub) — and immediately adjacent to `0x0E`
  (`Admin_VucFlashReadStatus`, benign). An operator walking `CDW12[7:0]`
  through the neighbourhood of the benign flash-status commands passes
  directly over the erase and write commands. **Never iterate `CDW12[7:0]`.
  Never write a script that can emit a `0xCA` command byte it was not
  explicitly given.**
- `0x0F` and `0x10` are also `15` and `16` decimal — exactly the numbers a
  `for i in range(...)` loop counter produces early.
- `0x0403` (Drive Uninit — ungated, allow-listed, sets FACTORY reinit) is
  **one hex nibble** from `0x0503` (the crash-dump clear used in every
  documented recovery). A single mistyped digit in a shell history recall
  converts the standard recovery command into an unconditional factory wipe.
- `0x0303` (SBL EEPROM erase — permanent brick) sits directly below `0x0403`
  and two below `0x0503`/`0x0603`.

---

## 4. The Post-Crash allow-list

`PROC8 0x7ffa6b18` (`Admin_CheckCmdAllowed`) hosts **four separate gates in
series** — Post-Crash, VUC Control, purge-phase, sanitize — which is why
earlier notes describing "the" allow/deny list contradicted each other: each
was reading a different one. This section is the Post-Crash gate only.

**Location and guard, PROVEN:** `PROC8 0x7ffa6b30`–`0x7ffa6bd8`, guarded by

```asm
7ffa6b1b: l32r  a8,0x7ffa09b0        ; -> 0x7ff87c64, the startup-mode global
7ffa6b1e: l32i.n a8,a8,0x0
7ffa6b30: { movi a13,198 ; bnei a8,6,0x7ffa6bd9 }   ; mode != 6 -> gate does not apply
```

Startup type 6 is `INVALID` (per §13.8 of `sn200-firmware-re.md`, which
overturned an earlier "6 = Post Crash" naming — 6 is genuinely the diagnostic
mode value, it is just the enum's `INVALID` label, not a "Post Crash" label).
Only in this mode does the gate reject anything.

**The gate is an allow-list, not a deny-list** — this itself overturns an
earlier reading (`sn200-independent-re.md` §6.1) that had it inverted. It is
re-read every command, not cached.

```
0x00 0x01 0x02 0x04 0x05 0x06 0x08 0x09 0x0A 0x0C 0x10 0x11 0xE6 0xEC 0xFF
0xC6  only when a4 (= ctx+0x38 = CDW12[7:0]) ∈ {0x20, 0x30}
0xCA  only when a4 ∈ the 12-entry sub-list at 0x7ffa6d76:
        {0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32}
```

(`0x03`, `0x07`, `0x0B` are simply reserved/unused opcode values in NVMe;
their absence from the opcode list is not a rejection of anything.)

**The two `0xCA` sub-values the list excludes are the only two that understand
LBAs — PROVEN.** `0xCA`/`0x00` is `Admin_VucFlashLogicalToPhysical` (handler
`0x30035680`) and `0xCA`/`0x01` is `Admin_VucFlashRead` (handler `0x30036494`,
reads one LBA of user data through the live L2P: NSID validated `1..128`,
`CDW14` = LBA, `CDW15` = data format `0`/`1`/`2`, `CDW10` = length in dwords).
Both are rejected `0x7C5` on a latched drive. Everything the list *does* admit
from this family — `0x03` read, `0x08` V2P, `0x0F` erase, `0x10` write — works
in **physical** addresses. So the one command that could have pulled user data
off a latched drive with no state change is gated off, and so is the one that
could have told you where to look. Full sub-value map and the instruction-level
re-read of the sub-list at `0x7ffa6d76`: `sn200-vuc-flash-read.md`.

**Reject status:** the gate's reject path returns the literal constant
`0x8F8A0000` (the only occurrence of that word in all 17 images). Shifted per
the NVMe CQE encoding, `0x8F8A0000 >> 17 = 0x47C5` → `DNR=1, M=0, SCT=7
(vendor specific), SC=0xC5` — which is the `0x7C5` seen on the wire, and
independently confirms this is the site the field observations were hitting.

**☠ Flag this explicitly: `0x0F` and `0x10` — the raw NAND block-erase and
raw NAND page-write/program command bytes — are IN this allow-list.** A
latched drive enforces essentially no protection against destroying itself
via the raw-flash family; the allow-list was built to let diagnostic/repair
VUCs through, and the raw-flash write/erase family happens to share the same
`0xCA` opcode as the (genuinely needed) raw-flash read family.

**`0xDD` (Secure Purge) is NOT on this list** — it lives in the separate
sanitize gate (`0x7ffa6cb0`) and is rejected while latched with the same
`0x7C5`. Any note claiming `0xDD` is allowed post-crash, or that it carries
the OAM erase, is wrong; the OAM erase is under `0xFF`.

**`0x10` (Firmware Commit) and `0x11` (Firmware Image Download) are on this
list unconditionally** — this is why the firmware-activation recovery and the
firmware-slot-fill procedure both work on a latched drive.

---

## 5. The OAM erase sub-command table

> **The complete `0xFF` dispatch table is now mapped end to end** — handler
> `0x7ffbc110` = static `0x30033448` in overlay 22 — and it is **exactly three
> command ids**: `0x03` (erase family, below), `0x04` (startup probe) and
> `0x07` (read raw System Area). Every other `CDW12[7:0]` is rejected with
> `status |= 0x40040000` and no side effect. `0x04` and `0x07` never read the
> sub byte at all. See **`sn200-oam-dispatch.md`** for the full trace, the
> per-selector safety class, and the corrected `0x0503`/`0x0603` story.

`0xFF`, `CDW12 = (sub<<8) | cmd_id`, `cmd_id = 0x03` (erase family). Switch at
`PROC8@30000000 0x300336c6` (`l8ui a11,a12,0x8d`), 7 sub-commands, 0–6.
**PROVEN**, confirmed three independent ways (source ordering, the section id
each arm writes to `[req+0x11c]`, and the EEPROM section-name enum StrIds
1214–1228).

| sub | `CDW12` | EEPROM section id | what it erases | class |
|---|---|---|---|---|
| 0 | `0x0003` | `6` (System Area) | System Area 0 | DESTRUCTIVE BY DESIGN |
| 1 | `0x0103` | `3` (Bad Block list) | Bad Block table 0 | DESTRUCTIVE BY DESIGN |
| 2 | `0x0203` | `9` (BIST Script) **then** `8` (BIST Status), chained | BIST Script + Status | DESTRUCTIVE BY DESIGN |
| 3 | `0x0303` | `13` (SBL) | ☠ **SBL EEPROM — permanent brick** | CATASTROPHIC |
| 4 | `0x0403` | — (verb `37`/`0x25`, not a section id) | ☠ **Drive Uninit — FACTORY reinit, no startup-type gate** | CATASTROPHIC |
| 5 | `0x0503` | `0x0b` (CLOG, Crash Dump) | Crash Dump — schedules REINIT when startup type == 6 | DESTRUCTIVE BY DESIGN |
| 6 | `0x0603` | `0x0a` (PFCL, PFail Crash Dump) | PFail Crash Dump — **no startup-type test, no re-init, no marker** | erases the pfail dump only; costs no user data |

**☠ `0x0003` is one nibble from `0x0004`, the triage probe.** It erases EEPROM
System-Area section `6`, which holds the boot-marker record — and an empty
System Area is itself one of the three latch predicates. This is the most
dangerous adjacency in the whole command set, because `0x0004` is typed on
*every* drive, healthy ones included.

**Sub 5 and sub 6 are not "identical but for the section id."** An earlier
revision of this file said so and it was wrong. The forward arms are
near-identical; the **resume handlers** are where they part, and that is
precisely where the wipe lives:

```asm
; 0x0603 resume, 0x300335a3      ; 0x0503 resume, 0x300335ca
l32i   a13,a12,0x188             ; l32i a11,a12,0x188
beqz.n a13,<plain return>        ; beqz a11,0x30033704   -> keeps going:
                                 ;   bnei *(0x7ff87c64),6 -> plain return
                                 ;   else verb 0x25 param 0 -> marker 3 REINIT
```

`0x0603` contains no `bnei a14,6`, no second request post, and no reachable
path to verb `0x25`. **PROVEN.** It cannot blank the L2P under any drive state.

**Why there are nine erase-failure log strings (StrIds 1628–1636) for seven
sub-commands:** sub 2 is **one chained arm covering two EEPROM sections, not
two sub-commands.** It erases BIST Script (section `9`), and on success
chains into a second erase of BIST Status (section `8`) before returning —
`0x30033643 → 0x3003372c` sets verb 3 / section 8 after the section-9 pass.
This is what previously made the string-vs-subcommand count look
inconsistent; it is fully accounted for now.

`UNEXSTRT` (any start not preceded by a recorded clean shutdown, which
includes every power event and every ~5s reset while looping) stamps its
stub into EEPROM section `0x0b` — i.e. it arms exactly the section sub 5
clears. This is proven three independent ways: the TOC at `0x7ff84a70`
names id `0x0b` as `CLOG`; the producer (`0x7ffb461c`) sets flag bit 0 for
CLOG-present; the consumer (`0x7ffab010`/`0x7ffaaf2b`) writes the stub with
`movi a12,11` (section `0x0b`). **PFCL (section `0x0a`) plays no part in
sustaining a power-event latch, so `0x0603` alone cannot release one** — only
useful if the drive latched for a reason other than an unclean stop.

That exception is now worth acting on, because `0x0603` is proven to be free:
**if the `0x0320` probe says CLOG is not armed and `0x0520` says PFCL is,
`0x0603` + a cold power cycle should clear the latch with the data intact.**
Mechanism PROVEN, scope narrow, never yet tested. `sn200-oam-dispatch.md` §7.1.

---

## 6. Firmware update commands

`frmw = 0x0b` (Identify Controller byte 260) decodes to: **5 slots**, **slot
1 read-only**, **no activate-without-reset** (bit 4 clear). Confirmed three
ways: the spec decode itself, WD's own `nvmec_get_fw_num_slots`
(`sar 1; and 7` / `and 1`), and the firmware's own commit handler, which
rejects `FS=1` on the image-replacing path with `Firmware Activate Invalid
Slot`. **Writable slots are 2, 3, 4, 5.**

**`fw-download` (opcode `0x11`).** The bundle goes on the wire **raw, whole,
unpadded** — WD's own `nvmec_fw_img_dl` never parses the tar; it rounds the
file size up to a dword and sends the entire `1 762 048`-byte file starting
at `CDW11 = OFST = 0`. Offset is `CDW11`, in **dwords** — this is the
spec-fixed field that pins the whole selector encoding in §1, not `CDW13`;
do not conflate it with the `0xC6` crash-dump family's offset field, which
*is* `CDW13` (§ below and `sn200-crash-dump-retrieval.md` §1.2 — two
different opcodes, two different offset CDWs, both dwords). A short final
transfer is normal and expected: `1762048 % 4096 == 768`, and WD's own tool
sends exactly that partial final chunk. Do not pad the file — the 256-byte
trailer at EOF is (almost certainly) a signature, and padding moves it.

**`fw-commit` (opcode `0x10`) actions:**

| `CA` | meaning | status on this firmware |
|---|---|---|
| 0 | replace slot `FS`, do not activate | **implemented — the safe one.** No activation, no reset, active slot untouched. |
| 1 | replace slot `FS`, activate at next reset | implemented |
| 2 | activate existing image already in slot `FS` at next reset | implemented, **but see §2's DESTRUCTIVE table** — whether this wipes depends on the target image's own flags word, not on the action |
| 3 | activate slot `FS` immediately, no reset | **NOT implemented.** Handler extracts only 2 bits of CA (`extui a8,a10,3,2`), so `blti a8,3` rejects it with `0xC0040000` (Generic, Invalid Field / "Firmware Activate Invalid Activation Action"). |

Because only 2 bits are extracted, **CA 4/5/6 silently alias onto 0/1/2, and
CA 7 aliases onto 3.** Never pass `--bpid` or an action > 3 to this drive —
the NVMe 1.4 boot-partition actions are silently reinterpreted as ordinary
slot commits.

**`--slot` traps.** `nvme fw-commit` defaults `--slot` to `0`, and the range
check is `FS <= slot_count`, so `FS=0` (spec meaning: "controller chooses")
**passes**. Always pass an explicit slot.

**Dual-port reset requirement.** Activation on a dual-ported drive (this
SKU's U.2 configuration) returns SC `0x10` "Firmware Activation Requires NVM
Subsystem Reset" rather than the single-port SC `0x0B` "Requires Conventional
Reset". A plain `nvme reset` will **not** activate a committed image on a
dual-port drive. Since every in-band reset here is itself an unclean stop
that can re-arm the Post-Crash latch, the only activation method that should
ever be used is a clean OS shutdown (driver `unbind`, which issues real
`CC.SHN`) followed by a cold power cycle — never `nvme reset` /
`nvme subsystem-reset`.

**Port lock.** Download and commit are **locked to the PCIe port that
started them** (StrId 2970, SC `0x13` "Firmware Activation Prohibited"). On a
dual-pathed drive (check `nvme list-subsys`), every `fw-download` and
`fw-commit` for one drive must go through the same `/dev/nvmeN` node.

**Re-download before every commit** — nothing in this firmware, or the NVMe
spec generally, guarantees the download buffer survives a commit.

---

## 7. Working invocations

These are the actual encodings that have been used against real drives, or
that the tooling in `tools/sn200-fw/` is built to emit and has been exercised
against an emulated drive plus (for the read paths) the field. **They are
evidence about these specific encodings only** — do not generalize from a
working invocation to a neighbouring CDW value.

```sh
# SAFE — startup-mode read. Field-confirmed: returned 0x00000601 on a
# latched drive (startup type 6 = INVALID/diagnostic).
nvme admin-passthru /dev/nvmeN --opcode=0xff -n 0 --cdw10=0 --cdw12=0x0004 --data-len=0

# SAFE — which section is armed. Run this BEFORE firing anything destructive;
# this measurement was skipped before both field recoveries on sea1-hv-2,
# which meant 0x0503 was fired blind both times.
nvme admin-passthru /dev/nvmeN --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b | od -A d -t x4  # CRASH
nvme admin-passthru /dev/nvmeN --opcode=0xC6 -n 0 --cdw10=2 --cdw12=0x0520 --data-len=8 -r -b | od -A d -t x4  # PFCRASH

# SAFE — full crash-dump retrieval, chunked/resumable, side-effect free.
# This is the documented, tested tool; do NOT use `nvme wdc get-crash-dump`,
# which auto-fires 0xFF/0x0503 on success and wipes the namespace.
cd tools/sn200-fw
sudo ./check-latch-state.sh /dev/nvmeN                                    # read-only triage
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvmeN
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin

# DESTRUCTIVE BY DESIGN — the known-working (destructive) clear sequence,
# actually fired on sea1-hv-2. Both clears were fired blind (section-armed
# probe above was not run first), guaranteeing the destructive path.
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0603 --data-len=0   # pfail, synchronous
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 --cdw10=0 --cdw12=0x0503 --data-len=0   # crash, schedules REINIT
# then: clean OS shutdown (driver unbind, issues CC.SHN) + cold power cycle >= 90s.
# Result on sea1-hv-2: drive healthy, full capacity, namespace fully zeroed.

# DESTRUCTIVE BY DESIGN — firmware-activation recovery, field-proven on
# sea1-hv-2, 2026-08-04. Also wiped the namespace (fully zeroed, sampled
# 1 MiB -> 1 TiB) -- NOT a cheaper/safer alternative to 0x0503, just a
# different door to the same destructive re-init, and this one has NO
# startup-type gate at all.
nvme fw-log /dev/nvmeN                          # find a slot holding a good image
nvme fw-commit /dev/nvmeN --slot=5 --action=2   # CA=2 = activate EXISTING image in slot 5
# then a cold power cycle -- frmw bit4=0 means no activate-without-reset.
# A bare cold power cycle with NO commit first was tried and left the drive
# still latched -- the activation, not the power cycle, does the work.

# DESK-VERIFIED, NOT YET RUN AGAINST HARDWARE -- fill every writable slot
# with KNGND122 so no future activation can land on an older, buggier
# revision. Non-activating by construction (CA=0 only, never targets slot
# 0 or 1, never resets). Run on the already-latched drive first.
tools/sn200-fw/fill-fw-slots.sh --image KNGND122.bin --dry-run /dev/nvmeN
sudo tools/sn200-fw/fill-fw-slots.sh --image KNGND122.bin /dev/nvmeN
```
