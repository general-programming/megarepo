# SN200 crash dump: retrieval and decoding

Target: HGST/WDC Ultrastar SN200 `HUSMR7676BDP3Y1`, firmware `KNGND122`, latched
in **Post Crash Startup**.

Goal: read the crash dump the drive is holding, so the assert that actually
fired is known rather than inferred from firmware control flow.

Companion documents:
- `docs/sn200-firmware-re.md` — the firmware RE this builds on (VUC map, log
  ABI, boot-marker state machine, why the erase is a wipe).
- **`docs/sn200-nondestructive-recovery.md`** — whether the latch can be lifted
  WITHOUT the namespace-wiping clear, and the read-only triage
  (`check-latch-state.sh`) that decides it per drive. Read that first if the
  data matters.
- `.claude/skills/nvme-recovery/SKILL.md` — the recovery runbook.

Claims are labelled **PROVEN** (read directly out of a binary or source),
**INFERRED** (reasoned from structure), or **SPECULATIVE**.

---

## 0. The one thing to read before touching the drive

> ### ☠ Do NOT run `nvme wdc get-crash-dump`
>
> **PROVEN** from nvme-cli `plugins/wdc/wdc-nvme.c`. `wdc_do_crash_dump()`
> ends with:
>
> ```c
> ret = wdc_do_dump(hdl, opcode, crash_dump_length, cdw12, file, crash_dump_length);
> if (!ret)
>         ret = wdc_do_clear_dump(hdl, WDC_NVME_CLEAR_DUMP_OPCODE, cdw12_clear);
> ```
>
> On success it **automatically** issues opcode `0xFF` with CDW12 `0x0503`.
> Per `sn200-firmware-re.md` §4 that command schedules a **Drive REINIT**,
> and the reinit **zeroes the namespace**. The vendor tool destroys the data
> as a matter of course, immediately after reading the dump.
>
> The same applies to WD's `dm-cli` capture-diagnostics flow
> (`hgst_nvmec_cap_diags_end` → `gf_nvme_clear_crash_dump`).
>
> `tools/sn200-fw/pull-crash-dump.sh` exists precisely to do the read **without**
> the clear. It cannot emit `0xFF`, `0xDD`, `0xD8` or `0xD9` at all.

---

## 1. The vendor command — fully resolved

### Encoding — **PROVEN**, three independent sources in exact agreement

| source | what it gave |
|---|---|
| `libdmi_core.so.0.39` (`dm-core-2.5.1-7`) | `gf_nvme_vuc_simple_real` @ `0x8bf90` builds the SQE: `cmd[0x00]=opcode`, `cmd[0x28]=CDW10`, `cmd[0x30]=(subcmd<<8)\|cmd_id`, rest zero ⇒ **NSID 0** |
| `nvme-cli` `plugins/wdc/wdc-nvme.c` | the `WDC_NVME_*` defines and `wdc_do_dump()` / `wdc_dump_length()` |
| SN200 firmware, PROC8 overlay `0x30030d14` | the `VUC Get Drive Log SubCmd %08X` dispatcher |

```
opcode  = 0xC6
NSID    = 0
CDW10   = transfer length in DWORDS
CDW12   = (subcmd << 8) | cmd_id          ; cmd_id 0x20 = "Get Drive Log" family
CDW13   = transfer offset in DWORDS       ; <-- see §1.2
direction = from device (read)
```

| section | tag | size probe CDW12 | body CDW12 | size lands in |
|---|---|---|---|---|
| Crash dump | `CRSHDMP ` | `0x0320` | **`0x0420`** | dword[0] |
| PFail crash dump | `PFCRDMP ` | `0x0520` | `0x0620` | dword[0] |
| String table | `STRTBL  ` | `0x0120` | `0x0220` | **dword[1]** |
| Drive log | `DRVLOG  ` | `0x0120` | `0x0020` | dword[0] |

The size probe is `CDW10=2`, `--data-len=8`, and returns two little-endian
u32s. `0x0120` is a **shared** probe: dword[0] is the drive-log size and
dword[1] is the string-table size (**PROVEN** — `gf_nvme_get_binary_drive_log_size_real`
@ `0x8b6e0` reads the first word, `gf_nvme_get_string_table_size_real` @
`0x8b5f0` reads `var_24h`, i.e. buffer+4).

If no valid dump exists the probe fails with **SC `0xC3`** = `HDMS_DEV_NO_DATA`
(**PROVEN**, `gf_nvme_get_crash_dump_size_real` @ `0x8bdf0` special-cases it,
and the firmware logs StrId 1608/1610 `Get Crash Dump Size - no valid crash
dump available`).

The four sections are a table in `libdmi_core`: `gf_dump_internal_info` @
`0x2e2260`, four 0x20-byte entries of
`{ get_size_fn, get_data_fn, post_fn, const char tag[9] }` — the same 8-char
tags the firmware uses in its E6 manifest at PROC8 `0x7ff80570`.

### 1.2 The offset — **there is none, in any dword. PROVEN both ways.**

> **This section's original claim was wrong.** It is kept below because the
> host-side reasoning is still accurate; what changed is that the drive does
> not implement it. §1.2.1 is what the drive does, §1.2.3 is why, from the
> firmware. `sn200-crash-dump-field-results.md` has the run.

**The handler has no steering wheel, only a throttle.** It reads exactly two
things out of the command — `CDW10` (length in dwords) and `CDW12[15:8]` (the
sub-command) — and clamps the transfer to the *section size*, 3.2 MiB. There is
no offset field, no seek sub-command and no cursor. Which means the good news
buried in §1.2.3: **the firmware is willing to return the entire 3.2 MiB in a
single command**, and the 160 KiB cliff is very likely ours, not the drive's.

#### 1.2.1 What the drive actually does — **PROVEN on hardware**

On the latched `nvme7`, with `cdw12 = 0x0420`:

| field | effect |
|---|---|
| `CDW10` | transfer length in **dwords**. Honoured. |
| `CDW11` | ignored at any value |
| `CDW13` | **ignored at any value** — 50 chunks at 64 KiB stride came back byte-identical |
| repeat reads | no auto-advance; every read starts at byte 0 |

`mdts` reports 0 (no limit advertised). A single command with
`CDW10 = 0x8000` returns 128 KiB whose second 64 KiB genuinely differs from the
first — so the transfer itself is not capped at 64 KiB. `CDW10 ≥ 0xA000`
(160 KiB) returns nothing.

**Working retrieval today is therefore: one command, `CDW10 = 0x8000`, no
offset, and you get the first 128 KiB of the section and nothing else.**

Why this matters concretely: the log area is laid out as **4 blocks of 0x1000
per core, `0x4000` apart** (§4.3), starting at file offset `0x12500`. 128 KiB
reaches only through **core 3 of 16**. Cores 4–15 begin at `0x22500` and run to
about `0x52500` — every byte of them is past the cliff. That is the single
reason no assert record is in hand (§4.4.2).

#### 1.2.2 The original CDW13 derivation — retained, and wrong for this drive

Derived from nvme-cli's `wdc_do_dump()`, which is
the only implementation anywhere that actually chunks the `0xC6` read:

```c
admin_cmd.opcode   = opcode;              /* 0xC6 */
admin_cmd.data_len = curr_data_len;
admin_cmd.cdw10    = curr_data_len >> 2;  /* length, dwords */
admin_cmd.cdw12    = cdw12;               /* (subcmd<<8)|cmd */
admin_cmd.cdw13    = curr_data_offset;    /* first iteration, offset 0 */
/* ... loop ... */
admin_cmd.cdw10    = curr_data_len >> 2;
admin_cmd.cdw13    = curr_data_offset >> 2;   /* OFFSET, IN DWORDS */
```

CDW11 is never set (the struct is `memset` to zero).

**Why `libdmi_core` looks like it disagrees, and does not.** WD's own library
never chunks the `0xC6` read: `_gf_capture_internal_logs` @ `0x3baf0` does
`get_size` → `malloc(size)` → `gf_cd_get_crash_dump(dev, buf, size)`, one
command for the whole section, so it leaves CDW13 at zero. Its
`gf_nvme_vuc_simple_real` transport has **no offset parameter at all**. That is
consistent with CDW13-as-offset, not evidence against it.

**Caveat worth knowing (INFERRED).** For the *`0xE6`* capture-diagnostics dump
the two sources genuinely disagree: `hgst_nvme_log_dump_real` @ `0x8c4f0` writes
its offset argument to `cmd[0x2c]` = **CDW11**, while nvme-cli's
`wdc_do_dump_e6()` uses **CDW13**. Either the firmware accepts both, or one of
them is wrong for some product generation. This does not affect the `0xC6`
path, but it is why `pull-crash-dump.sh` has an `--offset-cdw 11` fallback and
why it verifies the offset empirically before trusting it (§3.2).

#### 1.2.3 The handler, from the firmware — **PROVEN**

Entry `0x30030924` in `PROC8_30000000.bin` (entry byte `0x36` = `entry a1,0x90`),
running to `0x30030e29`. The sub-command dispatcher is at `0x30030d14`:

```asm
30030d14: b2 0f 39  l8ui a11,a15,0x39      ; the ONLY command byte read
30030d17: a1 3c f8  l32r a10,0x3002ee08    ; = 0x06466001 -> StrId 1606, 1 arg
30030d1a: 65 2c f6  call8 0x30026fe0       ; "VUC Get Drive Log SubCmd %08X"
      ... beqz a9,0x30030dc2               ; sub 0  drive log body      0x0020
      ... beqi a9,1,0x30030e01             ; sub 1  strtbl/drvlog size  0x0120
      ... beqi a9,2,0x30030bec             ; sub 2  string table body   0x0220
      ... beqi a9,3,0x30030d7b             ; sub 3  crash size          0x0320
      ... beqi a9,5,0x30030d7b             ; sub 5  pfail size          0x0520
      ... beqi a9,4,0x30030ddf             ; sub 4  CRASH DUMP BODY     0x0420  <-- us
      ... beqi a9,6,0x30030e0e             ; sub 6  pfail body          0x0620
      ... beqi a9,7,0x30030b18
      ... beqi a9,8,0x30030ae7
30030d64: a1 2b f8  l32r a10,0x3002ee10    ; = 0x064b6000 -> StrId 1611, 0 args
30030d6a: d1 2a f8  l32r a13,0x3002ee14    ; = 0x40040000  status
```

The three literals decode to StrId 1606 `"VUC Get Drive Log SubCmd %08X"`,
1611 `"VUC Get Drive Log SubCmd not supported"`, and the unsupported-command
status — and the other arms land on 1608 `"Get Crash Dump Size - no valid crash
dump available"` and 1610, its pfail twin. The dispatch table is self-checking:
every arm's log string names the sub-command that arm handles.

The `0x0420` arm and the transfer setup:

```asm
30030ddf: l32i.n a9,a12,0x0        ; a12 = section descriptor (literal 0x7ff824d4)
30030dea: l32i a15,a1,0x40         ; source base, 64-bit
30030ded: l32i a8,a1,0x44
30030df0: l32i a10,a2,0x130        ; <-- CDW10, the only command dword read
30030df5: s32i.n a15,a2,0x28       ; ctx+0x28/+0x2c = source address
30030df7: l32i.n a9,a12,0x4        ; section size, in 64-byte units
30030df9: slli a9,a9,6             ; -> bytes = 0x00320000
...
30030a49: e0 fa 11  slli a15,a10,2 ; CDW10 * 4 = requested BYTES
30030a4c: f0 f7 63  minu a15,a7,a15 ; CLAMP to min(section_size, requested)
30030a4f: f9 d2     s32i.n a15,a2,0x34
30030a51:           call8 <transfer>
```

**The clamp is against 3.2 MiB, not 128 KiB.** And it is a `minu` — it
*truncates*, it does not reject.

##### The command-dword map — pinned by an ASCII interlock

The context struct is decoded unambiguously by a different VUC at
`0x3002b771`, which gates itself on a magic phrase:

```asm
3002b776: l32i a14,a2,0x130   ; must be 0                  <- CDW10
3002b77b: l32i a15,a2,0x134   ; must be 0                  <- CDW11
3002b780: l16ui a8,a2,0x13a   ; must be 0                  <- CDW12[31:16]
3002b788: l32i a9,a2,0x13c    ; == 0x564f4944  "VOID"      <- CDW13
3002b791: l32i a12,a2,0x140   ; == 0x57415252  "WARR"      <- CDW14
3002b79a: l32i a15,a2,0x144   ; == 0x414e5459  "ANTY"      <- CDW15
```

`CDW13|CDW14|CDW15 == "VOID WARRANTY"` leaves no room for interpretation.
(Independently corroborated at `0x30022a60`, a Delete-Queue handler that reads
`ctx+0x130` and bounds-checks it as `CDW10[15:0]` = QID.)

| ctx offset | field |
|---|---|
| `+0x28`/`+0x2c` | source address, 64-bit — **written by the handler, never read from the command** |
| `+0x34` | transfer length in bytes, the clamped value |
| `+0x130` | **CDW10** |
| `+0x134` | CDW11 |
| `+0x138` | CDW12 (`+0x139` = sub-command byte, `+0x13a` = upper half) |
| `+0x13c` / `+0x140` / `+0x144` | CDW13 / CDW14 / CDW15 |
| `+0x160` | NVMe status word (SC<<17) |

An exhaustive enumeration of every ctx-relative access in
`0x30030924..0x30030e29` yields:

```
0x18 0x28 0x2c 0x34 0x11a 0x11c 0x130 0x14c
0x160 0x16c 0x174 0x180 0x184 0x190 0x194 0x198 0x1a8 0x1ac
```

`0x134`, `0x13a`, `0x13c`, `0x140`, `0x144` **do not appear at all**. CDW11,
the upper half of CDW12, CDW13, CDW14 and CDW15 are never read. The PRP/DPTR is
never touched by the handler — the transport consumes it generically.

**No seek, no cursor either.** The `0x_20` dispatch is closed at sub-commands
0–8; everything else falls to StrId 1611. Every arm is a read. The `0x0420` arm
performs no store to the section descriptor and no store to any ctx field that
could persist as a position — `+0x28`/`+0x2c` are recomputed from the descriptor
on every call. Every invocation restarts at the section base, which is exactly
the observed "repeated reads are byte-identical".

#### 1.2.4 The 160 KiB cliff is probably **ours** — SPECULATIVE, and cheap to settle

No length clamp other than the 3.2 MiB one exists in the handler. Searches for
the obvious constants came up empty: `0x00020000` in PROC8 has three references
and all three are status-word construction (`SC<<17`), not lengths;
`0x00028000` has exactly one reference in all 18 images and it is a pool-size
slot in PROC0's memory-map table; there is no `bltui`/`bgeui` against 32768 in
the admin path; no `minu` in PROC8 clamps a transfer to `0x20000`.

**The symptom is the argument.** A firmware clamp is a `minu` — a 160 KiB
request would complete successfully and return 128 KiB of data plus padding.
What we saw was **nothing at all**. That is a command rejected before it reaches
the controller: the Linux passthru path refusing to map the buffer
(`blk_rq_map_user` against the admin queue's `max_hw_sectors` / `max_segments`),
which is precisely what `mdts = 0` leaves to kernel defaults.

Three read-only, zero-risk ways to settle it, none of which touch the drive's
state:

1. **Is the failure an ioctl `errno` or an NVMe status code?** This decides it
   outright. Host-side rejection never reaches the drive.
2. **Binary-search the boundary between 129 KiB and 159 KiB.** A firmware clamp
   lands on a round byte count; a kernel limit lands on a page/segment boundary.
3. **Read `/sys/block/nvmeXn1/queue/max_sectors_kb`** and compare.

If it is host-side, then per the firmware the whole dump comes back in **one
command**:

```
opcode 0xC6, cdw12 = 0x0420, cdw10 = 0xC8000    /* 819200 dwords = 3.2 MiB */
```

`0xC8000 * 4 = 0x320000`, exactly the section size, so the `minu` is a no-op and
the transfer is the entire section. Still a pure read — the `0x0420` arm stores
nothing to media (§1.3, §1.4). Raising the host limit is the enabling step:
`nvme_admin_cmd` via a path that does not go through `blk_rq_map_user`, or the
unlimited-window setup already described in §5.5.

**This is the single highest-value thing left to try.** It is the difference
between having cores 0–3 and having all 16 — which is the difference between
not having the assert and having it (§4.4.2).

### 1.3 Is the `0xC6` read side-effect free? — **yes**

Three independent lines of evidence, all pointing the same way:

1. **PROVEN (firmware), but not by the argument originally given here.** The
   `0x0420` arm of the handler (entry `0x30030924`, §1.2.3) performs **no store
   outside the command context**: it writes `+0x28`/`+0x2c` (source address,
   recomputed each call), `+0x34` (this transfer's length), `+0x160` (status)
   and three bookkeeping words. It never writes to the section descriptor and
   never writes to media. That is a direct structural proof and it stands.

   > **Downgraded.** The original wording claimed the handler's "complete set of
   > call targets" — `0x30026fe0`, `0x3002c1a0`, `0x300224c0`, `0x3002d410`,
   > `0x30022504`, `0x3002d0d0`, `0x3002d094`, `0x3002d044`, `0x3002d0ac` —
   > excludes both erase primitives (`0x30030aa0` flash, `0x30031d10` EEPROM).
   > **Those target addresses cannot currently be validated.** Bytes
   > `0x30000abc..0x30022238` of `PROC8_30000000.bin` are all zero — a 137 KiB
   > hole — and several of the listed targets land in it. None of the nine lands
   > on an `entry` (`0x36`) byte. Only 14 of 184 functions in that flat image are
   > confirmed by a call site, against 231/430 in `PROC8_7ff80000`. The region's
   > base is right (`l32r` resolution there produces log StrIds that match their
   > dispatch arms exactly), so the callees are simply in banks this image does
   > not contain. Treat the call-set argument as unvalidated; the no-stores
   > argument above, plus points 2 and 3, carry the conclusion on their own.
2. **PROVEN (nvme-cli).** `wdc_do_crash_dump()` must issue a *separate,
   explicit* `0xFF/0x0503` to clear the dump after reading it. If reading
   consumed the dump, that command would be redundant.
3. **PROVEN (libdmi_core).** The section's `post_fn`, `gf_post_dump_crash` @
   `0x3b740`, does exactly one thing: `*(int*)(dev+0x40) = rc` and a trace
   line `"Crash dump retrieval complete rc %d"`. It is pure host-side
   bookkeeping.

Corroborating negative evidence: the firmware's string table has **no**
"retrieved"/"consumed" state for a dump section. The only three states are
`erased` / `detected` / `invalid` (StrIds 1277–1282).

**Conclusion (PROVEN): reads are non-destructive and idempotent, so a
resumable chunked pull is exactly as safe as a single-shot pull.** Partial
reads, repeated reads and re-reads of the same range carry no risk of marking
the dump consumed or eligible for auto-erase. This is what makes the chunked
path the *preferred* one rather than a compromise.

### 1.4 Does any `0xC6` sub-function write? — **no**

**PROVEN.** Every `0xC6` call site in `libdmi_core.so.0.39` (13 of them, found
by scanning for `mov esi, 0xC6`) resolves to one of:

| function | CDW12 | direction |
|---|---|---|
| `gf_cd_get_string_table` | 0x0220 | read |
| `gf_cd_get_binary_drive_log` | 0x0020 | read |
| `gf_cd_get_crash_dump` | 0x0420 | read |
| `gf_cd_get_pfail_crash_dump` | 0x0620 | read |
| `gf_nvme_get_{string_table,binary_drive_log,crash_dump,pfail_crash_dump}_size_real` | 0x0120 / 0x0320 / 0x0520 | read |
| `_gf_capture_hwcomp_values` | 0x0021 | read |
| `gf_nvme_get_defect_data_real` | `(sec<<8)\|0xB7` | read |
| `sb_get_vs_log_page_data`, `sb_nvme_get_stats_temp`, `sb_nvmec_clear_fw_act_hist` | — | SkyBolt product path, not SN200 |

The transfer-direction argument (`xfer` in the library's own trace string
`"Enter. opcode:0x%02X, cmd:0x%02X, subcmd:0x%02X, xfer:%d, ndt:0x%08X, buf_sz:%u"`)
is **2 = from-device** on every one of them. There is no write variant of the
`0x_20` family anywhere in the library.

The destructive commands live under **different opcodes** — `0xFF` (erase
family), `0xDD` (secure purge), `0xD8`/`0xD9` (namespace delete/create) — which
is why `pull-crash-dump.sh` can guarantee safety simply by never emitting
anything but `0xC6`.

---

## 1.5 Will the drive even accept these commands while latched? — **yes, PROVEN**

Everything above is worthless if the latch rejects `0xC6`. It does not. The
admin gate `Admin_CheckCmdAllowed` @ `0x7ffa6b18` (PROC8's main image — the
constant `0x8F8A0000` occurs at exactly one address in all 17 images) has now
been fully decoded, and it is an **allow-list**, not a block-list. The previous
reading in `sn200-firmware-re.md` §10 was inverted and had wrong constants;
both are corrected there.

```asm
7ffa6b30  { movi a13,0xC6 ; bnei a8,6,0x7ffa6bd9 }   ; mode != 6 -> gate does not apply
7ffa6b38  beqz  a3,     0x7ffa6cfb                   ; allow-list; a3 = admin opcode
...
7ffa6bbe  bne   a3,a13, 0x7ffa6bd1                   ; not 0xC6 -> REJECT
7ffa6bc1  beqi  a4,32,  0x7ffa6cfb                   ; 0xC6 with cmd byte 0x20  <-- us
7ffa6bc9  beq   a4,a15, 0x7ffa6cfb                   ; 0xC6 with cmd byte 0x30
7ffa6bd1  { movi a9,1 ; j 0x7ffa6d05 }               ; fall through = REJECT
7ffa6cfb  { movi a9,0 ; j 0x7ffa6d05 }               ; allow -> continue to the next gate
7ffa6d08  { l32r a10,LOG 1804 ; mov a11,a3 }         ; logs the OPCODE
7ffa6d13  l32r  a9,0x8F8A0000                        ; -> status 0x7C5, DNR
```

**Exempt while latched in Post Crash Startup (PROVEN):**

| opcode | what | condition |
|---|---|---|
| 0x00,0x01,0x04,0x05,0x08 | Delete/Create I/O SQ+CQ, Abort | unconditional |
| **0x02** | Get Log Page | unconditional |
| **0x06** | Identify | unconditional |
| 0x09 / 0x0A | Set / Get Features | unconditional |
| 0x0C | Async Event Request | unconditional |
| 0x10 / 0x11 | Firmware Commit / Image Download | unconditional |
| **0xC6** | **our VUC** | **only if cmd byte ∈ {0x20, 0x30}** |
| 0xCA | vendor | only 12 listed sub-values |
| 0xE6 | log-dump VUC | unconditional |
| 0xEC | vendor | unconditional |
| 0xFF | clear-dump / sys-init-done VUC | unconditional |

Everything else returns `0x8F8A0000` → SCT=7 SC=0xC5 DNR
(`HDMS_DEV_DIAGNOSTIC_MODE`). Notably rejected: `0xCC`, `0xD4`, `0xD8`–`0xDF`
(including `0xDD` secure purge), Format `0x80`, Security `0x81`/`0x82`,
Sanitize `0x84`, namespace management `0x0D`/`0x15`.

**What this means for us.** Every command in the retrieval procedure uses
`0xC6` with CDW12 low byte `0x20` — `0x0020`, `0x0120`, `0x0220`, `0x0320`,
`0x0420`, `0x0520`, `0x0620` all qualify. **The entire procedure is inside the
allow-list.** (The two `0xC6` VUCs that use a different cmd byte —
`_gf_capture_hwcomp_values` at `0x0021` and `gf_nvme_get_defect_data` at
`…B7` — are rejected, which is a nice consistency check: the gate discriminates
exactly where you would expect.)

**One honest gap.** The gate reads its cmd byte from the command context at
`+0x38`. That offset is PROVEN; the *identity* of the field is not. The context
has CDW0 at `+0x18` and NSID at `+0x1c`, and under a raw-SQE mapping `+0x38`
would be CDW8/PRP2, which makes no sense as a selector. `CDW12[7:0]` is the
only reading consistent with the observed constants `{0x20, 0x30}` and with
every real `0xC6` VUC in `libdmi_core`. **INFERRED, high confidence.**

Two consequences worth stating plainly:

- **`0xFF` is exempt and unconditional.** The commands that wipe the drive are
  fully reachable in the latched state. There is no firmware safety net between
  a typo and a destroyed namespace. This is why the retrieval script has no
  code path that can emit `0xFF` at all.
- The gate is one of **four independent gates evaluated in series**: Post Crash
  → VUC Control (StrId 1805, returns SC 0x01) → purge phase (StrId 1806, SC
  0x0C) → sanitize (StrId 3370). Passing one does not exempt you from the next.
  **`0xC6` cmd `0x20` clears the VUC-Control gate too** — its whitelist at
  `0x7ffa6bdf` applies the same `{0x20, 0x30}` test — so retrieval works even
  if VUC Control happens to be disabled. (PROVEN.)

### 1.5.1 Firmware Commit is not an escape hatch — **PROVEN**

Worth recording because it is an obvious idea and it does not work. Firmware
Commit `0x10` *is* accepted while latched, and so is Firmware Image Download
`0x11`, so a full download+commit is reachable. But **Commit Action 0b011
("activate image in slot immediately, without reset") is not supported by this
firmware**:

```asm
30025e48  extui a8,a10,3,2          ; CA -- only TWO bits are extracted
30025e4b  blti  a8,3,0x30025c40     ; CA 0/1/2 -> the real handler
                                    ; CA 3 falls through to:
          l32r  a10, LOG 2188 "Firmware Activate Invalid Activation Action"
                                    ; returns 0xC0040000 (SC 0x02, DNR)
```

Because only 2 bits are extracted, CA 4–7 alias onto 0–3. The activate path's
own strings are all reset-demanding — StrId 790/791/792
`Subsystem Restart Required` / `Conventional Reset required` /
`Controller Restart Required to activate firmware`, plus
`PCIe_SendResetRequest: Resetting for firmware activate (FWA)` and
`FwActivateNextStartup`. Commit sets a pending-activate flag; it does not
re-enter init in place. **INFERRED (high confidence): no host-side escape from
the lockup via commit.**

---

## 2. The tooling

All under `tools/sn200-fw/`.

| file | what it is |
|---|---|
| `pull-crash-dump.sh` | the retrieval script — chunked+resumable and single-shot |
| `decode-crash-dump.py` | the decoder CLI |
| `sn200_vuc.py` | vendor-command encodings, single source of truth |
| `sn200_strtab.py` | string table loader + log-descriptor ABI |
| `sn200_dump.py` | framing *derivation* — the fallback for a blob with no container magic |
| `sn200_container.py` | the real CDH container: blocks, records, core grid, coverage (§4.3) |
| `tests/` | 159 offline tests, no hardware |
| `tests/fake_nvme.py` | an emulated SN200 that stands in for `nvme` |

Run the tests with any pytest:

```sh
cd tools/sn200-fw && python3 -m pytest tests/ -q      # 159 passed
```

### 2.1 What the tests actually prove

These are not smoke tests. Two of them are real evidence:

**`test_nargs_matches_format`.** Takes every log descriptor that the firmware
genuinely loads with an `l32r`, across all 18 KNGND122 processor images, and
checks that the `nargs` field packed into the descriptor equals the number of
printf conversions in the string its StrId resolves to.

> **1584 of 1586 agree — 99.87%.**

Nothing makes that true by construction. If the StrId↔CSV-line indexing were
off by one, or the descriptor bit layout wrong, or the format parser broken, it
would collapse to noise. It is the strongest available offline confirmation
that the decode path is correct.

That measurement also produced a **correction to `sn200-firmware-re.md` §1**,
which lists `0x00` among the observed `level` values. It does occur, but a
descriptor with `level=0` and `nargs=0` is just a small aligned integer and is
indistinguishable from an ordinary constant. Admitting it drops agreement from
99.87% (1584/1586) to 98.51% (1589/1613), and *every* additional mismatch is a
level-`0x00` hit. `KNOWN_LEVELS` therefore excludes it, and
`test_level_zero_is_excluded_because_it_is_noise` pins the reason.

Level histogram over all referenced descriptors: `0x60` ×1782, `0x20` ×398,
`0x40` ×397, `0x00` ×372, everything else ≤13 (noise).

**`test_offset_probe_rejects_a_drive_that_ignores_the_offset`.** Runs the real
`pull-crash-dump.sh` against `fake_nvme.py` configured to ignore the offset
register, and asserts the script refuses rather than writing a file that is
chunk 0 repeated N times. That failure mode is silent and produces a plausible
looking dump, so the guard matters more than the happy path.

The rest cover: exact reassembly, single-shot ≡ chunked, resume after a
mid-transfer failure, the `--offset-cdw 11` fallback, empty sections, the
string-table-size-is-dword[1] trap, and that the dry run emits nothing but
`0xC6` reads.

Also measured, and folded back in: the widest printf in the string table takes
**15** arguments — StrId 3189/3190, the `Outstanding Trim ...` watchdog, which
is precisely the record most likely to explain this drive. An earlier
`MAX_NARGS = 12` silently dropped it and broke record-chain following at exactly
the interesting record.

---

## 3. Retrieval

> **What actually works on `nvme7` (2026-08-04).** The offset probe below did
> its job: it caught that CDW13 is ignored, and it aborted rather than writing a
> plausible-looking file of repeated chunk 0. The only read that returns data is
> a **single command, `CDW10 = 0x8000`, no offset**, which gives 128 KiB.
>
> **Chunked mode can never work on this drive** — there is no offset field to
> chunk with, in any dword (§1.2.3). Single-shot is the *only* mode, and the
> job is therefore to make the single shot bigger: the firmware will hand back
> all 3.2 MiB for `CDW10 = 0xC8000`, and the 160 KiB ceiling looks host-side
> (§1.2.4). Settle that first — it is three read-only checks — and the rest of
> this section's chunking machinery becomes moot.

### 3.1 Two modes, same code path

```sh
# PRIMARY: stock kernel, ~5 s command window, chunked and resumable
./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvme7

# FAST PATH: unlimited window (vfio-pci with no AER, or the patched nvme driver)
./pull-crash-dump.sh --section all --single-shot /dev/nvme7
```

Chunked is the primary mode because it needs nothing installed and no driver
changes, and because §1.3 establishes that repeated/partial reads are free of
side effects. Single-shot is strictly a speed optimisation.

`--chunk-size 65536` is a starting point. The script times every command and
warns above 3000 ms, which is the point at which you are approaching the ~5 s
AEN-driven controller reset. If you see that warning, halve the chunk size and
re-run — completed chunks are skipped.

### 3.2 The offset probe — why it runs first

Before pulling anything the script spends **three read-only commands**
establishing that the offset field actually works:

```
A = read(len = 2P, offset = 0)
B = read(len =  P, offset = 0)
C = read(len =  P, offset = P)
```

- `B != A[0:P]` → the drive is not returning a stable image. Abort.
- `C == A[P:2P]` → offset honoured. Proceed.
- `C == B`      → **offset silently ignored**. Abort loudly.
- anything else → inconclusive. Abort.

This is the single safe probe. It uses only the `0x0420` read that WD's own
tool issues, differing solely in `CDW10`/`CDW13`; it writes nothing and cannot
reach the erase family. Without it, an ignored offset yields a full-size file
of the correct length that is chunk 0 repeated — which looks fine and decodes
to garbage.

Belt and braces: after reassembly the script reports chunk diversity (distinct
SHA-256s among the chunks) and fails if every chunk is identical.

**This probe has now earned its keep.** On `nvme7` it hit the `C == B` case and
aborted. Keep it, and keep it first: the failure mode it prevents — a
correctly-sized file that is chunk 0 repeated 50 times — is indistinguishable
from success without it.

### 3.3 Output

```
sn200-dump-<serial>-<UTC>/
├── pull.log            full transcript
├── id-ctrl.bin/.json   Identify Controller; byte 3072 = "Post Crash Mode" (OM-6402, KNGND110+)
├── error-log.json  smart-log.json
├── crash.bin           the reassembled dump
├── crash/size.bin      raw 8-byte size reply
├── crash/manifest.tsv  offset / length / sha256 per chunk
└── crash/chunks/*.bin  individual chunks, kept for resume and audit
```

Resume is automatic: re-running the identical command line skips any chunk
whose file length and SHA-256 already match the manifest.

---

## 4. Decoding

```sh
# with the firmware's own string table
./decode-crash-dump.py crash.bin --string-table ~/sn200fw/fw/KNGND122/StringTable.csv

# better: with the table pulled off the drive itself, guaranteed to match
./decode-crash-dump.py crash.bin --string-table sn200-dump-*/strtbl.bin

./decode-crash-dump.py crash.bin --fw-dir ~/sn200fw --asserts-only --json out.json
```

Always prefer the drive's own `STRTBL` blob. StrIds are **not** stable across
firmware revisions; decoding with the wrong table produces messages that look
entirely plausible and are wrong. The decoder compares the table's `FWREV`
header against `--rev` and warns on a mismatch.

### 4.1 The string table

**PROVEN.** `StringTable.csv` line 1 is a header
(`VERSION=1 NUMRSVD=16 FWREV=KNGND122 HASHVAL=0xa1e928ab ### Omaha StringTable (0x85c41d83) ###`),
lines 2..17 are `# StrId N reserved` placeholders, and **`StrId N` == CSV line
`N+1`**. 3617 lines for KNGND122.

The CSV stores escapes *literally*: a format string ends with the two
characters `\` and `n`, not a newline. `sn200_strtab.unescape()` handles it;
forgetting to is a good way to get `\n` in your output.

The loader sniffs its container, so it takes the firmware's `.csv`, the
`.csv.gz`, or the raw blob off the drive. Note that a gzip member stores the
original filename in its header, so raw gzip bytes contain the literal text
`StringTable.csv` — the sniffer must check the gzip magic *before* any
"looks like a string table" heuristic, or it will try to parse compressed bytes
as CSV.

**What the drive actually returns is UNRESOLVED — SPECULATIVE.** `dm-cli`
retrieves the `STRTBL` blob and stores it byte-for-byte in its output container
without ever inspecting byte 0: no gunzip, no inflate, no magic check, no hash
validation. There is no `VERSION=`, `NUMRSVD`, `HASHVAL`, `FWREV` or
`Omaha StringTable` string anywhere in the entire dm-cli package — nothing in
it parses the CSV, so there is no mismatch error path because there is no
check. One hint: `CDW10 = len >> 2` with no round-up implies the drive-reported
length is dword-aligned, but the firmware's own `StringTable.csv.gz` is 53718
bytes and `53718 % 4 == 2`. The likely explanation is that the drive returns
the gzip stream padded to a 4-byte boundary and consumers rely on gzip's
self-terminating framing to ignore the pad. `StringTable.from_blob()` handles
gzip, plain CSV, and a gzip member starting at a nonzero offset, so it should
cope either way — but the first live retrieval should be eyeballed before it is
trusted.

For reference the firmware-side file is a textbook gzip:
`1f 8b 08 08 f1 23 63 5f 02 03` + `"StringTable.csv\0"` (deflate, FNAME set,
MTIME 0x5f6323f1, OS=3), deflate stream at offset 0x1a, expanding to 195545
bytes / 3617 lines. It is not really CSV — no commas, no quoting, one record
per line.

### 4.2 The log record — **PROVEN**, recovered from `Log_Emit`

> **Scope.** This is the record as `Log_Emit` builds it **in RAM**. The
> crash-dump container stores a *different, tighter* record: 8 bytes of header,
> not 0x14, with bit 31 cleared rather than set. See §4.3 — use that one for
> anything read off the drive.

`Log_Emit` is per-image. PROC8's is at `0x7ffb45a8`, with byte-identical copies
at PROC6 `0x7ffbc738`, PROC9 `0x7ffba9d8`, PROC13 `0x7ffb9700`, PROC14
`0x7ffaf470`; the overlay bank's `0x3002b8e0` is a thunk. PROC0's copy
(`0x7ffb0d80`) is the useful one, because it emits plain 3-byte instructions
where PROC8 hides the same stores inside FLIX bundles:

```asm
7ffb45f4: s32i.n a9,a1,0x8      ; record+0x08 = descriptor
7ffb45f6: rsr    a11,234        ; CCOUNT -- the Xtensa cycle counter
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

Three details each of which silently breaks a decoder that misses it:

1. **Bit 31 is set in an emitted record.** The firmware's literal pool holds
   the bare descriptor; `Log_Emit` ORs in `0x80000000` on the way into the
   ring. A decoder that reuses the firmware-image decode verbatim computes
   `word >> 16` = `0x8000|StrId` and finds **nothing at all**.
   (`test_emitted_descriptor_has_bit31_and_still_decodes`)
2. **The timestamp sits between the descriptor and the arguments.** Arguments
   start at descriptor + 12 bytes, not descriptor + 4. Read them at +4 and
   argument 0 is the CCOUNT.
   (`test_args_are_read_after_the_timestamp_not_immediately`)
3. **`nargs` is masked to 4 bits**, so the maximum is 15 — which is exactly the
   widest format string in the table.

No core/CPU id is stamped: there is no `rsr … PRID` anywhere in `Log_Emit`.
**INFERRED** — the collector attributes a core from *which per-core ring* a
record came out of. The ring writer (PROC8 `0x7ffb4868`) has 1023 slots; the
rings live in BSS, so their runtime addresses and sizes are not statically
recoverable.

### 4.2.1 `%s` arguments are StrIds — **PROVEN**

The firmware has no way to put a string into a log record. Where it needs one,
it emits a `%s` format and passes **the StrId as the argument word**. StrIds
1277–1282 (the per-section state trichotomy) appear as descriptors *nowhere in
any image*; they are reached at runtime as `1277 + 3*section + state` and
printed through StrId 1275 (`%s`). The same trick covers the boot-marker names
(3029–3039) and the shutdown types (310–314).

So `render()` resolves a `%s` argument recursively through the table:

```
StrId 1275 "%s" with arg 1279  ->  "SYS: Crash Dump section is in invalid state"
StrId 1275 "%s" with arg 1281  ->  "SYS: PFail Crash Dump is detected"
```

Treat it as a pointer and you get `<ptr 0x000004ff>` instead of the answer.

### 4.3 Record framing — **SOLVED, PROVEN against the real dump**

The framing derived from `Log_Emit` (§4.2) is the **in-RAM** record. What the
crash-dump container holds is a different, tighter thing. It is now fully
recovered — from the real file, and confirmed line-by-line against the writer
in PROC0 (§4.3.3).

Do not use `0x14 + 4*nargs` on a dump. That was the in-RAM stride and it is
what made the old decoder fail to chain.

#### 4.3.1 Container layout

```
0x00000  CDH   container header  (magic 00 43 44 48, version, FWREV at +8)
0x00100  MMAP  section descriptor   ("MMAP", then a memory range)
0x01000  ...   ~64 KiB of unidentified binary capture, no log framing
0x11000  CDI   header of the log section  (magic 09 43 44 49)
0x12500  log block area
```

The log area is a fixed grid: **one block per 0x1000, four blocks per core,
`0x4000` per core.**

```
core N block area  =  0x12500 + N*0x4000
block slot         =  block_index mod 4          (a 4-deep ring per core)
```

Observed exactly: core0 `0x12500`, core1 `0x16500`, core2 `0x1a500`, core3
`0x1e500`, with block indices 0–3, 0–1, 4–7 and 4–5 landing in slots
`idx mod 4` without exception. Core3's slots 2 and 3 would be at `0x20500` and
`0x21500` — past the 128 KiB cliff, which is where the retrievable data stops.

#### 4.3.2 Block header — 0x18 bytes

```c
struct dump_log_block {          /* 0x1000 bytes, one per 4 KiB */
    char fwrev[8];   /* +0x00  "KNGND122"                                  */
    u32  index;      /* +0x08  block index in this core's stream, monotonic */
    u32  stream;     /* +0x0c  (core_id << 16) | flags   flags: 3 or 7      */
    u32  serial;     /* +0x10  shared monotonic counter sampled at block open */
    u32  hash;       /* +0x14  0xa1e928ab -- StringTable HASHVAL            */
    /* records from +0x18 */
};
```

`hash` is the decisive field: `StringTable.csv`'s own header line reads
`VERSION=1 NUMRSVD=16 FWREV=KNGND122 HASHVAL=0xa1e928ab`. **A decoder should
refuse to render a block whose `hash` does not match its table.** That is the
firmware handing you table-matching for free, and it is the reason the header
carries FWREV twice over.

`serial` (+0x10) is **not** a record count — core2 block 4 and core3 block 4
both carry `0x98`, and core2 block 5 and core3 block 5 both carry `0xed`.
It is a counter shared across cores, sampled when the block is opened
(INFERRED; the writer loads it from its caller's context, §4.3.3).

#### 4.3.3 Record — 8 bytes, not 0x14

```c
struct dump_log_record {         /* 8 + 4*nargs bytes */
    u32 desc;                    /* +0x00 */
    u32 ccount;                  /* +0x04  raw CCOUNT, cycles not wall time */
    u32 arg[nargs];              /* +0x08 */
};
```

and the descriptor is **not** laid out the way the in-RAM one is:

```
bits 31       stale flag -- SET means not committed; STOP HERE
bits 30..16   StrId
bits 15..12   level >> 4          (2 = assert, 4, 6 = informational)
bits 11..4    record index within this block, 8-bit, reset to 0 per block
bits  3..0    nargs
```

The two traps that break a decoder:

1. **The low nibble of the level byte is not part of the level.** It is the top
   half of the 8-bit per-block record index. Read `level = (desc >> 8) & 0xFF`
   and every record after the 16th in a block looks like a corrupt level and
   the chain dies. This is exactly the bug that produced the old
   "13 records at suspiciously regular 0x1000 intervals" result: the walk was
   dying at record 16 of each block and the fallback scanner was re-finding one
   descriptor per page. Reading `level = ((desc >> 12) & 0xF) << 4` and
   `index = (desc >> 4) & 0xFF` recovers **733** records from the same bytes.
2. **Bit 31 is the terminator, not a constant.** `Log_Emit` sets it in RAM; the
   dump writer *clears* it on commit. A descriptor that still has bit 31 set is
   leftover from an earlier generation of the ring — it is where valid data
   ends. Every partially-filled block in the real dump ends on exactly one such
   word (its rendered text is visibly garbage: `Data_Reset port 882159656`),
   and blocks that end on a zero word are simply blocks whose page was clean.

Walk rule: start at `block + 0x18`; stop on `desc == 0`, on `desc & 0x80000000`,
or when the record index stops incrementing by one. The index is a free integrity
check — a wrong framing desynchronises it within two records.

#### 4.3.4 The writer — PROC0 `0x7ffaf10c`, **PROVEN**

Found by `litref -a 0x7ff83a30`, the literal holding `0xa1e928ab`; PROC0 is the
only image that carries it. Every field above appears in the clear (a3 = the
per-core log context, header at `a3+0x60`):

```asm
7ffaf1c0: movi  a11,24                ; header size
7ffaf27b: and   a13,a10,a4            ; a4 = 0x7fffffff -- CLEAR the stale bit
7ffaf27e: extui a11,a13,0,4           ; nargs = low 4 bits
      ...  movi a12,8                 ; record = 8 + 4*nargs
7ffaf29f: s8i   a4,a3,0x58            ; per-block record index := 0
7ffaf2cd: s32i  a10,a3,0x50           ; bytes-used := 24
7ffaf2d0: l32r  a8,0x7ff82664         ; = 0x4e474e4b "KNGN"
7ffaf2d8: l32r  a9,0x7ff82668         ; = 0x32323144 "D122"
7ffaf2db: s32i  a9,a3,0x64            ; hdr+0x04
7ffaf2de: s32i  a8,a3,0x60            ; hdr+0x00
7ffaf2e1: s32i  a15,a3,0x6c           ; hdr+0x0c  stream/flags
7ffaf2ea: l32r  a15,0x7ff83a30        ; = 0xa1e928ab
7ffaf2f0: s32i  a13,a3,0x70           ; hdr+0x10  serial (from caller a2+0x98)
7ffaf2f3: s32i  a15,a3,0x74           ; hdr+0x14  HASHVAL
7ffaf330: and   a12,a12,a14           ; a14 = *0x7ff83a00 = 0xfffff00f
7ffaf338: or    a12,a12,a13           ; merge the 8-bit index into bits 11..4
7ffaf34b: s8i   a13,a3,0x58           ; index++
7ffaf34e: s32i.n a12,a11,0x0          ; store descriptor
7ffaf390: s32i.n a11,a13,0x0          ; store CCOUNT  (from a2+0x14)
7ffaf3a9: addx4 a11,a11,a2            ; args from a2+0x18, stride 4
7ffaf3b4: s32i.n a11,a13,0x0
```

The mask `0xfffff00f` is the whole story: **eight bits, 11..4**, are cleared and
replaced by the per-block index. That is what pushes the counter into the low
nibble of the level byte.

Two more constants fall out of the same function and pin the geometry:

- `0x7ff8367c = 0x00001000` — loaded, then `free = 0x1000 - bytes_used`, then
  `bgeu free, reclen` decides whether to open a new block. **Block size is
  0x1000, PROVEN.**
- `0x7ff82630 = 0x00004000` — the per-core span, matching the observed grid.

**One honest gap in this function.** Between the descriptor store and the
CCOUNT store there is a conditional (`ball a12, mask 0x8000, 0x7ffaf385`) that,
on one path, appends an extra word taken from the caller's `+0x98` — the same
serial that lands in the block header. Whether that path can fire, and on which
records, cannot be settled here: the surrounding FLIX bundles at `0x7ffaf355`
and `0x7ffaf388` have undecoded slots that may reassign `a12`, and a
half-decoded bundle is exactly the thing that has produced retracted findings in
this project. **It does not affect decoding**: all 733 records in the real dump
chain at exactly `8 + 4*nargs` with a monotonic index and monotonic CCOUNTs, so
whatever that branch does, it did not fire for any level-`0x60` record. Treat it
as a known unknown that could matter for a level-`0x20` record we have never
seen.

### 4.4 What the decoder reports

1. **Container sniff** — ELF / `E6LG` / section tags / gzip / all-zero /
   all-`0xFF` (erased flash).
2. **Hexdump** of the first 256 bytes.
3. **Derived record framing** and the chain length that justifies it.
4. **Candidate root cause** — records ranked by keyword tier.
5. **All records**, rendered through the string table.
6. Optional ASCII runs and JSON.

### 4.4.1 What "the assert" actually is — **PROVEN**

There is **no assert record type**. The firmware's assert idiom is:

```asm
l32r  a10, <log descriptor>
call8 <Log_Emit>
break.n                        ; 2d f0 -- Xtensa BREAK, becomes "LOGIC TRAP"
```

Found by exhaustive scan of all 18 images: **520 `break.n` sites, 418 (80%)
immediately preceded by a `callN`**, and wherever the target was resolvable it
was that image's `Log_Emit` — 24/24 in PROC8, 21/21 in PROC0. Examples:

```
PROC8 7ffa6ed9  StrId 1821 lvl 0x20  This is a generated logic trap
PROC8 7ffac46b  StrId 3283 lvl 0x20  AdminMgr: Unexpected startup state 0x%08x for resize 0x%08x
PROC0 7ffaaee1  StrId 1274 lvl 0x20  SYS: Bad startup marker (%08X)
PROC0 7ffb3f3a  StrId   48 lvl 0x20  STK: Overflow detected
```

So:

- **`level == 0x20` IS the assert level.** Informational chatter uses 0x40/0x60.
- **The StrId of that record is the entire assert identity.** There is no
  `__FILE__`/`__LINE__` mechanism and no assert format string anywhere in the
  table (`grep -i assert` yields two hits, both in a test thread).
- `LOGIC TRAP` (StrId 313) is the shutdown-cause byte the exception path writes
  *after* the BREAK, not a distinct record format.

**A decoder needs `StringTable.csv` for the matching FWREV and nothing else.**
That is the whole answer to "which assert fired": find the level-0x20 record,
read its StrId. `rank_asserts()` ranks assert-level records above every
keyword match, and `test_assert_level_outranks_a_mere_keyword_match` pins it.

The keyword tiers remain as a fallback, and tier 2 is deliberately
SN200-specific: per `sn200-firmware-re.md` §8 the documented trigger is a large
deallocate/TRIM racing the L2P journal flush, and the thing that fires is the
15-second `Outstanding Trim ...` watchdog (StrId 3189/3190). That string
contains **no** generic assert keyword — it says "Trim", not "trap" — so
without the SN200 tier the single most probable root-cause record ranks below
routine noise. `test_assert_ranking_surfaces_the_trim_watchdog` pins that.

### 4.4.2 What the real dump actually says — **733 records, no assert**

`docs/sn200-dumps/nvme7-crash-128k.bin`, decoded with the framing of §4.3
against `KNGND122`'s `StringTable.csv`:

| block | core | idx | serial | records |
|---|---|---|---|---|
| `0x12500` | 0 | 0 | 36 | 119 |
| `0x13500` | 0 | 1 | 46 | 4 |
| `0x14500` | 0 | 2 | 63 | 150 |
| `0x15500` | 0 | 3 | 64 | 3 |
| `0x16500` | 1 | 0 | 36 | 2 |
| `0x17500` | 1 | 1 | 252 | 9 |
| `0x1a500` | 2 | 4 | 152 | 72 |
| `0x1b500` | 2 | 5 | 237 | 2 |
| `0x1c500` | 2 | 6 | 252 | 280 |
| `0x1d500` | 2 | 7 | 254 | 16 |
| `0x1e500` | 3 | 4 | 152 | 73 |
| `0x1f500` | 3 | 5 | 237 | 3 |

**733 records. Every single one is level `0x60` — informational. There is no
level-`0x20` record, and no level-`0x40` record either.** A word-aligned scan
of all 131072 bytes for a level-`0x20` descriptor finds only coincidental hits,
none of them inside a validated chain.

The text is clean and unambiguous, which is what confirms the framing:

```
core0 blk2 #0    New Boot: Log restarts here
core0 blk2 #1    SYS: Firmware is starting
core0 blk2 #5    Revision KNGND112             <- SBL
core0 blk2 #7    Revision KNGND122             <- firmware
core0 blk2 #13   Firmware Boot Mode   : COLD BOOT, EEPROM (Slot 4)
core0 blk2 #56   SYS: Shutdown time    = 6.429 ms
core0 blk2 #57   SYS: PFAIL time       = 6.521 ms
core0 blk2 #58.. SYS: Bread crumbs: HAH-e aq / uvw BHAH / -rPIRQ-1 / ...
core0 blk2 #82   SYS: PFAIL startup
core0 blk2 #84   Scrubbing done: MLP 03470e5c..07909e86 4495618K
core0 blk2 #103  SYS: Number of slots: 5 / Default slot: 4 / Active slot: 4
core0 blk2 #130  SYS: StartupCpl from ADMIN_MGR (SysInitDone)
core0 blk2 #131  SYS: Inited
core0 blk3 #2    SysLED: Cmd - DriveReady (0x00000000)
```

The `%c%c%c%c%c%c%c%c` bread-crumb records render — `HAH-e aq`, `-rPIRQ-1`,
`SESFP2P3`, `M2M3M4S7` — which is a strong end-to-end check that arguments are
being read at the right offset, since each of the eight args must independently
land on a printable byte.

**What this tells us, and what it does not.** The retrievable window contains a
*power-fail event and a successful recovery* — `Shutdown time = 6.429 ms`,
`PFAIL time = 6.521 ms`, bread crumbs, `PFAIL startup`, scrub, full manager
startup, `SYS: Inited`, `DriveReady`. It is the ordinary informational log of
cores 0–3. It is **not** the fault.

Two independent reasons the assert is not here:

1. **Coverage — and this one is probably fixable.** Only cores 0–3 of 16 are
   inside 128 KiB (§1.2.1). Whichever core executed the `break.n` is
   overwhelmingly likely to be one of the other twelve — the SN200's admin and
   back-end work lives on PROC8 and up. The firmware will return the whole
   3.2 MiB in one command (§1.2.3); the 160 KiB cliff appears to be a host-side
   buffer-mapping limit, not the drive (§1.2.4). Lift that and the assert should
   be reachable.
2. **Stream.** Every block in range carries flags `3` or `7` in
   `stream & 0xFFFF` and every record in them is level `0x60`. Whether
   higher-severity records are filtered into different blocks, or simply never
   occurred on cores 0–3, cannot be decided from this sample (SPECULATIVE).

Also unparsed: `0x1000`–`0x10FFF`, roughly 64 KiB of binary with no log
framing, introduced by the `MMAP` descriptor at `0x100`. It contains large
regularly-strided tables (0x1cc stride around `0x3c00`–`0x6000`) that are
plainly context/control blocks, not records. Register state, if the dump has
any, is in there.

One thread worth pulling on if that region ever matters: the `MMAP` descriptor
reads

```
"MMAP" 00040000  0000001c  00000092  07909e86  60000000  60003ff0  00000001  07909e86
```

and `0x07909e86` is **the same value the scrubber logs** —
`Scrubbing done: MLP 03470e5c..07909e86 4495618K`. So `MMAP` is a *media* map,
not a memory map, and it embeds a value that the log independently confirms.
`0x60000000..0x60003ff0` alongside it is an address range in the ASIC's register
aperture. INFERRED, from two data points; worth ten minutes if the region is
ever needed.

### 4.5 The container header — mostly closed by the real dump

> **Update.** This section was written when no dump existed. Most of the "not
> settled" part is now settled from `nvme7-crash-128k.bin` — see §4.3.1 for the
> container layout and the two magics (`00 43 44 48` "CDH" at 0, `09 43 44 49`
> "CDI" at `0x11000`). What follows is still correct and still worth keeping;
> the residual gap is now only the `MMAP` region at `0x1000`–`0x10FFF`.
>
> Note the eyecatcher scan below did **not** miss anything: `CDH`/`CDI` are
> 3-char tags with a binary byte in the fourth position, so an 8-char-ASCII
> sweep could never have found them. That is why the container header held out
> against static analysis for so long.

Two things are settled, one is not.

**Settled (PROVEN): the `libied.so` ELF assert-dump format does NOT apply to
the SN200.** It was a promising lead and it is a dead end. `libied.so` ships an
`assert_dump_decoder.c` that parses an ELF32 `ET_CORE` file with PT_NOTE
segments (`is_elf_valid`, `populate_notes`, `get_notes_of_type`, note types
`0x10001`/`0x10003`/`0x10004`/`0x10005`/`0x10007`/`0x10008`/`0x10009`/`0x1000a`),
and `libe6text.so` has the matching vocabulary. It belongs to a different
product. Six independent proofs:

1. Its CPU names are `FTP-0/1/2`, `HIP`, `FM-0/1 Slice0/1` — not 16 Xtensa
   cores plus an FCC.
2. Its `where_from` table names `A53 C0/C1/C2` and `R5 FM0.0/FM0.1/HIP` —
   **Cortex-A53 + Cortex-R5**.
3. It parses ARM CP15 fault registers (`dfsr`, `ifsr`, `dfar`, `ifar`) and its
   NT_PRSTATUS offsets are ARM32's exactly.
4. Its E6 entry names (`FA-EVLOG`, `BL-ASRT1`, `FS-STATS`, …) have **zero
   overlap** with Omaha's (`STRTBL  `, `CRSHDMP `, `PFCRDMP `, `DRVLOG  `).
5. **Nothing routes SN200 data to it.** `_gf_capture_internal_logs` pushes the
   four blobs into the host container as opaque blobs and never calls any
   `ied_*` decoder. `ied_decode_assert_dump` and `ied_decode_event_log` are
   imported by nothing in the package and have zero real call sites.
6. No ELF magic and no ELF-related strings exist anywhere in the SN200
   firmware images or its string table.

Its event-log record format differs from the SN200's in every field — 2-byte
`02 02` magic, nanosecond u64 timestamp, u32 uid key, fixed-position parameter
count — which is further confirmation they are different products.

**Also settled (PROVEN): the E6 section-descriptor table** at PROC8
`0x7ff80570` is 40 entries of stride 0x24, walked by `Admin_BuildE6Entry`
(overflow logs StrId 2950). The four blobs of interest have **null handler and
zero length**, confirming they are collected by a dedicated VUC path rather
than by replaying a standard NVMe command:

```
0x7ff80570  "STRTBL  "  handler=0  len=0  code=0x06
0x7ff80594  "CRSHDMP "  handler=0  len=0  code=0x04
0x7ff805b8  "PFCRDMP "  handler=0  len=0  code=0x05
0x7ff805dc  "DRVLOG  "  handler=0  len=0  code=0x0a
```

> Correction to `sn200-firmware-re.md`: the firmware tag is **`PFCRDMP `**.
> `PCRSHDMP` is the *host-side* E6 entry name `libdmi_core` writes. Different
> names for the same section.

**NOT settled: the crash dump's own on-media header** — magic, version, length,
CRC, section table. Only one bit of it is proven: the UNEXSTRT path
(`0x7ffaac43`) edits a staging buffer at `0x7ff9ff60`, clearing **bit 0 of byte
0** (`movi a11,254`; `s8i`), which is evidently the "header valid/complete"
flag whose clearing produces the *invalid* third state (StrIds 1279/1282).

Why it could not be recovered, stated plainly: **the crash-dump writer emits no
log messages at all**. The only crash-dump strings in the table are erase
failures, state reports, size probes and the UNEXSTRT stub — consistent with
the dump being written by the PFAIL/trap handler with logging disabled. The
string-table-to-code technique that cracked everything else has no purchase on
a code path that logs nothing, and the overlay size-probe path
(`0x30030bf0..0x30030ce0`) is ~17 undecoded 8-byte FLIX bundles out of ~40
instructions. Cracking it needs a correct Tensilica TIE/FLIX configuration for
these cores.

An exhaustive 8-char-ASCII scan of all 18 images found **no dump eyecatcher**
outside the E6 manifest — only the 40 E6 tags, `KNGND122`, `FWHEADER`,
`SECURITY`, `SBLPATCH`, `OMAHA   `, `EPGI10MS`, `M0EA0EVB` and CLI parameter
names.

**This is why the decoder derives the framing instead of assuming it (§4.3).**
The gap is real, it is bounded, and the tooling is built to work around it
rather than to paper over it. Also useful if the container turns out to embed
them: breadcrumbs are **24 slots × 8 raw ASCII bytes** at `0x7ff8c8f4`, printed
via StrId 1259 `SYS: Bread crumbs: %c%c%c%c%c%c%c%c`.

### 4.6 What it looks like when it works

Decoding a synthetic dump built to the recovered format — an unknown 256-byte
container header followed by a plausible failure sequence — with no hints given
to the decoder:

```
-- record framing ----------------------------------------------------
  derived layout: pre=2 desc=1 mid=2 args=nargs pad=0 align=1 words
  longest self-consistent record chain: 5 records

-- candidate root cause ---------------------------------------------
  ASSERT @0x0000018c ccount=1359087696 StrId 48   lvl 20 | STK: Overflow detected
         @0x000001a0 ccount=1359102115 StrId 1774 lvl 60 | Admin_NotifyHandler: Sending Persistent Internal Error async event on Post Crash Startup.
         @0x00000124 ccount=1359000067 StrId 3189 lvl 40 | Outstanding Trim, Port 0 cmdID 42 sqID 1 state 3 Opcode 9 numOfRange 16384 NumOfHostLbaRemained 30000000 ...
         @0x00000108 ccount=1358954496 StrId 317  lvl 60 | BackendMgr: Received Deallocate request for LBNs 0x0 - 0x1c9c380
```

The framing was derived past an unknown header, the level-0x20 record is named
as the assert, the 15-argument trim watchdog rendered every field, and the
`SYS: %s` record resolved its StrId argument to
`SYS: Crash Dump is detected`. That is exactly the shape of answer the exercise
is after. `test_realistic_dump_names_the_assert` runs this.

Building that example also caught two real defects: StrId 0 was passing as a
valid call site (lines[0] is the CSV *header*, so any word `0x0000LLNN` decoded
to it), and the chain-length threshold rejected a short-but-complete chain and
fell back to scan mode. Both are fixed and both have regression tests.

---

## 5. The procedure

Run in this order. The ordering is the point: **everything is read-only until
you deliberately choose otherwise, and the dump is captured before anything
that could disturb it.**

### Step 1 — capture state, non-destructively

```sh
DEV=/dev/nvme7
nvme id-ctrl $DEV -b | od -A d -t x1 -j 3072 -N 16    # "Post Crash Mode", KNGND110+
nvme admin-passthru $DEV --opcode=0xff -n 0 --cdw12=0x0004   # startup type in CDW0 byte 1
```

`pull-crash-dump.sh` does the Identify capture itself; the two lines above are
for a quick look before committing.

> `0xFF/0x0004` is `gf_nvme_sys_init_done`, a **read** — it returns the startup
> type in CDW0 and transfers no data. It is the only `0xFF` sub-command in this
> document that is not destructive. `pull-crash-dump.sh` still refuses to emit
> it, because a script that can emit `0xFF` at all is a script that can emit
> `0x0503` after a typo.

### Step 2 — pull the dumps FIRST, before anything else

```sh
cd tools/sn200-fw
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 $DEV
```

Order within the section list matters less than the fact that this happens
before steps 3+. Pull `strtbl` alongside the dumps — it is what decodes them,
and it is guaranteed to match the running firmware.

If a command times out or the controller resets mid-pull, **re-run the exact
same command line**; completed chunks are skipped. Reads have no side effects
(§1.3), so this is free.

If the offset probe reports `CDW13 IS BEING IGNORED`:
1. retry with `--offset-cdw 11`;
2. failing that, get an unlimited window (§5.5) and use `--single-shot`.

### Step 3 — decode, offline, with the drive untouched

```sh
D=sn200-dump-*/
./decode-crash-dump.py $D/crash.bin --string-table $D/strtbl.bin --json $D/crash.json
./decode-crash-dump.py $D/pfail.bin --string-table $D/strtbl.bin --json $D/pfail.json
```

Nothing here talks to the drive. Iterate as much as you like.

### Step 4 — copy the results off the machine

Before any step that changes drive state, get `sn200-dump-*/` somewhere else.
Steps 5+ are one-way.

### Step 5 — only now consider recovery

Everything from here is covered by `sn200-firmware-re.md` "Actionable recovery
options". Restating only the safety-critical part:

| command | effect |
|---|---|
| `0xFF` CDW12 `0x0603` | erases the **pfail** dump, synchronously. Destroys evidence. |
| ☠ `0xFF` CDW12 `0x0503` | "clear crash dump" — schedules a **Drive REINIT**. **THIS WIPES THE NAMESPACE.** |
| ☠ `0xFF` CDW12 `0x0303` | erase SBL EEPROM — **permanent brick** |
| ☠ `0xFF` CDW12 `0x0403` | Drive Uninit |
| ☢ `0xDD` | secure purge — irreversible, no confirmation argument |

`0x0303` and `0x0403` sit *directly adjacent* to the two values you want. Do
not sweep the sub-command space; do not typo.

### 5.5 Getting an unlimited command window

Only needed if the chunked path cannot make progress.

```sh
modprobe vfio-pci
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/unbind
echo 1c58 0023   > /sys/bus/pci/drivers/vfio-pci/new_id
# drive the admin queue from userspace and never submit an Asynchronous Event Request
```

The controller can only *post* the Persistent Internal Error AEN against an
outstanding AER. Linux submits AERs unconditionally; a userspace driver need
not, so the AEN is starved, `nvme_reset_ctrl()` is never called, and the admin
queue stays up indefinitely. (`sn200-firmware-re.md` §9.)

A patched in-tree `nvme` driver that suppresses the reset for this PCI ID
achieves the same thing while keeping `/dev/nvmeN` and therefore
`nvme admin-passthru` working — which is the more convenient path for this
tooling, since `--single-shot` then just works.

---

## 6. Status: what is verified and what is not

**PROVEN**
- The complete `0xC6` encoding, all four sections, both size-probe dword slots.
- The `0x0420` handler: entry `0x30030924`, dispatcher `0x30030d14`, sub-commands
  0–8 closed, transfer clamped by `minu` against the **3.2 MiB section size** at
  `0x30030a4c` — it truncates, it never rejects (§1.2.3).
- The NVMe command-context map, pinned by the `"VOID"/"WARR"/"ANTY"` interlock at
  `0x3002b771`: `CDW10` at `+0x130` through `CDW15` at `+0x144` (§1.2.3).
- `0xC6` with cmd byte `0x20` is in the Post-Crash admin allow-list, so the whole
  retrieval procedure is issuable while the drive is latched (§1.5). It also clears
  the independent VUC-Control gate.
- The **in-RAM** log record: `0x14 + 4*nargs` bytes, descriptor at +0x08 with bit 31
  set, CCOUNT at +0x10, args at +0x14 (§4.2).
- The **on-media** container framing (§4.3), verified both ways — recovered from
  the real dump and confirmed instruction-by-instruction in the writer at PROC0
  `0x7ffaf10c`. Block = 0x1000 with an 0x18 header carrying FWREV, block index,
  `(core<<16)|flags`, a shared serial, and `HASHVAL 0xa1e928ab`. Record =
  8 + 4*nargs, descriptor `StrId<<16 | (level>>4)<<12 | blockidx<<4 | nargs`,
  CCOUNT at +4, args at +8. Bit 31 set = stale = end of valid data.
- The log grid: 4 blocks per core, `0x4000` per core, core N at
  `0x12500 + N*0x4000` (§4.3.1).
- `%s` arguments are StrIds, not pointers (§4.2.1).
- `level == 0x20` is the assert level, and the StrId of that record is the entire
  assert identity — there is no separate assert structure (§4.4.1).
- The `libied.so` ELF/NOTE assert-dump format belongs to a different (ARM) product
  and does not apply here (§4.5).
- Firmware Commit is accepted while latched but CA=3 is unimplemented, so it is not
  a host-side escape from the lockup (§1.5.1).
- `0xC6` has no write sub-function; every use is a from-device read.
- Reading the dump has no effect on the stored crash section, so chunked and
  resumable retrieval is exactly as safe as single-shot.
- `nvme wdc get-crash-dump` and `dm-cli` capture-diagnostics both clear the
  dump automatically, and clearing it wipes the namespace.
- The string table indexing and the log descriptor ABI (99.87% agreement over
  1586 real call sites).
- `level` 0x00 must be excluded from descriptor scanning.
- `nargs` reaches 15.

**REFUTED**
- **The offset is CDW13.** It is not. Refuted twice over: ignored on `nvme7`,
  and never read by the firmware. This was previously listed as PROVEN on the
  strength of nvme-cli's source (§1.2.1, §1.2.3).
- **There is an offset at all.** The `0x0420` handler reads exactly `CDW10` and
  `CDW12[15:8]`. CDW11, CDW12's upper half, CDW13, CDW14 and CDW15 are never
  accessed, there is no seek sub-command, and no cursor persists between calls.
- **The handler's call-target set proves the read cannot erase.** The target
  addresses are unvalidatable in the flat overlay image (§1.3). The conclusion
  survives on other evidence.
- **The container does not embed the log rings verbatim.** It re-frames them:
  0x18-byte block headers, 8-byte records, bit 31 inverted in meaning (§4.3).

**INFERRED**
- The `0xE6` offset register discrepancy between `libdmi_core` (CDW11) and
  nvme-cli (CDW13) does not affect the `0xC6` path.
- The admin gate's cmd-selector byte at context `+0x38` is `CDW12[7:0]`.
- No core id is stamped into an in-RAM record — but the **container** stamps one,
  in `stream >> 16` of the block header (§4.3.2), so attribution off a dump is
  direct and does not depend on knowing which ring a record came from.
- The block header's `serial` at +0x10 is a counter shared across cores, sampled
  at block open. It is not a record count.
- Per-core ring depth is 4 blocks / 16 KiB: `slot = block_index mod 4` holds
  without exception across all 12 observed blocks, and PROC0 carries a `0x4000`
  literal in the same function.

**SPECULATIVE**
- The drive returns the string table as a gzip stream padded to a dword boundary
  (§4.1). Eyeball the first bytes of `strtbl.bin` on the first live pull.
- The `stream & 0xFFFF` flags (`3` and `7` observed) select a severity class, and
  assert-level records land in blocks with a different flag value. Every block in
  the retrievable window is `3` or `7` and every record in them is level `0x60`,
  which is consistent with this but does not establish it.

**NOT ESTABLISHED**
- The `MMAP` region, `0x1000`–`0x10FFF`: ~64 KiB of unframed binary announced by
  a `MMAP` descriptor at `0x100`. Register state, if the dump has any, is here.
- **Where the 160 KiB ceiling comes from.** It is not in the handler, and no
  candidate constant exists in the firmware. The leading hypothesis is host-side
  buffer mapping (§1.2.4), supported by the symptom: the drive would have
  *truncated*, and instead we got nothing. Three read-only checks settle it.
  This is the highest-value open item — it is what stands between us and cores
  4–15, and therefore between us and the assert.

**HARDWARE STATUS**
- Retrieval and decode are confirmed end to end on a latched drive, read-only,
  with the drive still latched afterwards (`sn200-crash-dump-field-results.md`).
- What is **not** confirmed is the root cause. 733 records came back and all of
  them are routine informational chatter from cores 0–3, including a clean
  PFAIL-and-recover cycle ending in `DriveReady`. The firing assert is not in the
  retrievable window (§4.4.2). Treat the root cause as strongly evidenced by code
  and field behaviour, **not** as confirmed by the drive's own record.
- **The next move is not more RE.** The framing is solved and the handler is
  fully mapped. What is left is one host-side measurement (§1.2.4) and, if it
  goes the way the evidence points, one bigger read.
