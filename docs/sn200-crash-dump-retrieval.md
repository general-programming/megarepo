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

### 1.2 The offset lives in **CDW13**, in dwords — **PROVEN**

This was the open question. It is settled by nvme-cli's `wdc_do_dump()`, which
is the only implementation anywhere that actually chunks the `0xC6` read:

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

### 1.3 Is the `0xC6` read side-effect free? — **yes**

Three independent lines of evidence, all pointing the same way:

1. **PROVEN (firmware).** The `VUC Get Drive Log` handler occupies
   `0x30030c00..0x30031060` in PROC8's overlay bank. Its complete set of call
   targets is `0x30026fe0` (the log printf), `0x3002c1a0`, `0x300224c0`,
   `0x3002d410`, `0x30022504`, `0x3002d0d0`, `0x3002d094`, `0x3002d044`,
   `0x3002d0ac`. **Neither erase primitive appears** — not `0x30030aa0` (the
   flash erase the OAM erase family calls) nor `0x30031d10` (the EEPROM erase).
   The read path cannot erase the section because it never calls anything that
   can.
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
| `sn200_dump.py` | dump structure analysis, record framing, assert ranking |
| `tests/` | 62 offline tests, no hardware |
| `tests/fake_nvme.py` | an emulated SN200 that stands in for `nvme` |

Run the tests with any pytest:

```sh
cd tools/sn200-fw && python3 -m pytest tests/ -q      # 62 passed
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

### 4.3 Record framing in the decoder

`SN200_LOG_LAYOUT` above is what the decoder tries first. It still runs the
layout *search* as a cross-check, because the **crash-dump container is not the
same thing as the drive log** — whether the container embeds the rings verbatim
is unresolved (§4.5).

The search works because the descriptor word is self-identifying, so
consecutive records form a chain: given a candidate layout, record N+1's
descriptor sits a computable distance past record N's, since `nargs` says how
many argument words N carries. `find_frame_layout()` scores

```
[ pre ][ descriptor ][ mid ][ nargs args ][ pad ]     aligned to `align`
```

over `pre` ∈ 0..4, `mid` ∈ 0..3, `pad` ∈ 0..4, `align` ∈ {1,2,4} words by the
longest clean chain each produces.

**This is self-validating.** A wrong layout chains 1–2 records and stops; the
right one walks hundreds. Tests confirm the documented layout is recovered from
a synthetic dump built to it (`test_real_sn200_layout_is_recovered_and_preferred`),
that five other framings are each recovered from dumps built to them, that
arguments and CCOUNT come back byte-exact, and that 256 KiB of random bytes
yields no confident layout at all.

If nothing chains, the decoder falls back to an unframed descriptor scan. In
that mode **message text is still reliable and argument values are not**, and
it says so.

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

### 4.5 The container header — the one honest gap

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
- The offset is CDW13 in dwords.
- `0xC6` with cmd byte `0x20` is in the Post-Crash admin allow-list, so the whole
  retrieval procedure is issuable while the drive is latched (§1.5). It also clears
  the independent VUC-Control gate.
- The on-media log record: `0x14 + 4*nargs` bytes, descriptor at +0x08 with bit 31
  set, CCOUNT at +0x10, args at +0x14 (§4.2).
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

**INFERRED**
- The `0xE6` offset register discrepancy between `libdmi_core` (CDW11) and
  nvme-cli (CDW13) does not affect the `0xC6` path. Hence the `--offset-cdw`
  fallback and the empirical probe.
- The admin gate's cmd-selector byte at context `+0x38` is `CDW12[7:0]`. The offset
  is proven; the field identity is the only reading that fits the constants.
- No core id is stamped into a log record; the collector attributes one from which
  per-core ring the record came out of.

**SPECULATIVE**
- The drive returns the string table as a gzip stream padded to a dword boundary
  (§4.1). Eyeball the first bytes of `strtbl.bin` on the first live pull.

**NOT ESTABLISHED — handled by deriving rather than guessing**
- The crash dump's **container header**: magic, version, length, CRC, section table.
  Only bit 0 of byte 0 is proven (the header-valid flag the UNEXSTRT path clears).
  This is the one honest wall, and the reason is structural: the dump writer emits no
  log messages, so the technique that cracked everything else has no purchase (§4.5).
  The decoder derives the record framing from the data instead, and reports the chain
  length that justifies its answer.
- Whether the container embeds the log rings verbatim, and where.

**UNTESTED AGAINST HARDWARE**
- Everything. No command in this document has been run against the drive. The
  offline validation is thorough (62 tests, including full end-to-end runs of
  the real retrieval script against an emulated drive) but it is not the same
  as a live run. The first live command should be the size probe in step 1.
