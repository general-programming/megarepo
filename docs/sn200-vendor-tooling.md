# Vendor names for the SN200's opcode surface

Everything in `sn200-opcode-map.md`, `sn200-c6-dispatch.md` and
`sn200-oam-dispatch.md` was traced out of the firmware without a single vendor
name to check against. This document brings in the one real external source that
exists — the **WDC plugin in `nvme-cli`** — cross-references it opcode by opcode,
and says explicitly where it disagrees.

**It is a naming document, not a safety authorisation.** Nothing here clears any
command to send. `sn200-command-reference.md` remains the document you act from.

Labels: **CONFIRMED** = vendor name and our independently traced behaviour agree
on the same encoding. **ADOPTED** = name taken from the vendor source with
supporting but not conclusive firmware evidence; marked as such wherever it is
used. **REJECTED** = the vendor source names this encoding, and it does not
apply to the SN200.

---

## 1. Sources, and how much each is worth

| source | verdict |
|---|---|
| **`nvme-cli` `plugins/wdc/wdc-nvme.{c,h}`**, master (plugin 2.15.1) | **The only real source.** Fetched and read directly. |
| **`nvme-cli` git history**, commits `5902e8a0` (2017-02-28) and `7ff5acc3` (2017-10-17) | **The highest-value find.** See §2. |
| OCP Datacenter NVMe SSD spec | **Chronologically impossible.** OCP v1.0 is dated 2020-03-18; the SN200 shipped 2016–2017. Do not parse any SN200 log page with an OCP structure. |
| smartmontools `drivedb.h` / `nvmecmds.cpp` | **Clean negative.** Zero matches for SN200, HUSMR, or VID `0x1c58`; `drivedb.h` is structurally ATA-only and has no NVMe VID/DID matching at all. smartmontools decodes no vendor NVMe log page for any vendor. |
| `wdckit` User's Guide (public PDF) | Documents a `getdui` command but a crude text extraction found no SN100/SN200/Ultrastar reference. Soft negative; worth a manual read if anyone has the time. |
| `dm-cli` / HGST SDK opcode documentation | **Not found, publicly.** (Our own partial reverse of `libdmi_core` — `gf_nvme_get_defect_data_real`, `_gf_capture_hwcomp_values` — is already used in `sn200-c6-dispatch.md` §3 and remains the better source for `0xC6`.) |

### 1.1 The encoding rule that makes the cross-reference possible

```c
#define WDC_NVME_SUBCMD_SHIFT   8
cdw12 = (SUBCMD << WDC_NVME_SUBCMD_SHIFT) | CMD;
```

This is **exactly** the field split our dispatcher analysis derived from
`PROC8_7ff80000` independently: `ctx+0x138` = `CDW12[7:0]` = the command byte,
`ctx+0x139` = `CDW12[15:8]` = the sub byte. Two independent derivations of the
same wire format. It is also the reason the tables below line up at all.

---

## 2. The decisive commit

`7ff5acc3` (2017-10-17), *"nvme-cli : wdc-plugin Add support for WDC SN100 and
SN200 devices"*, is thirteen lines:

```diff
-#define WDC_NVME_GF_VID          0x1c58
-#define WDC_NVME_GF_CNTL_ID      0x0003
+#define WDC_NVME_VID             0x1c58
+#define WDC_NVME_SN100_CNTL_ID   0x0003
+#define WDC_NVME_SN200_CNTL_ID   0x0023
```

It **adds no opcodes.** The SN200 was made to work by widening a device-ID
check over the SN100 opcode set introduced in `5902e8a0` (2017-02-28). So:

> **The SN200's entire vendor command surface, as far as WD's own public tooling
> is concerned, is the frozen February-2017 set.** Every opcode added to the
> plugin after that date targets a later drive family, and naming an SN200
> encoding from one of them is a guess.

That 2017 set, verbatim:

```c
WDC_NVME_CAP_DIAG_OPCODE        0xE6      /* cdw12 = 0x0000                */
WDC_NVME_CAP_DIAG_CMD_OPCODE    0xC6      /* the generic VUC transport     */
WDC_NVME_DRIVE_LOG_OPCODE       0xC6      /* cmd 0x20 sub 0x00 -> 0x0020   */
WDC_NVME_DRIVE_LOG_SIZE_OPCODE  0xC6      /* cmd 0x20 sub 0x01 -> 0x0120   */
WDC_NVME_CRASH_DUMP_SIZE_OPCODE 0xC6      /* cmd 0x20 sub 0x03 -> 0x0320   */
WDC_NVME_CRASH_DUMP_OPCODE      0xC6      /* cmd 0x20 sub 0x04 -> 0x0420   */
WDC_NVME_PURGE_CMD_OPCODE       0xDD
WDC_NVME_PURGE_MONITOR_OPCODE   0xDE      /* cdw10 0x0000000C, len 0x2F    */
WDC_NVME_CLEAR_DUMP_OPCODE      0xFF      /* cmd 0x03 sub 0x05 -> 0x0503   */
WDC_NVME_ADD_LOG_OPCODE         0xC1      /* log page                      */
```

Master's capability grant for the SN200 (`wdc-nvme.c:1659`) has not grown:

```c
case WDC_NVME_SN200_DEV_ID:   /* 0x0023 under VID 0x1c58 */
    capabilities = (CAP_DIAG | INTERNAL_LOG | CLEAR_PCIE | DRIVE_LOG |
                    CRASH_DUMP | PFAIL_DUMP | PURGE);
    /* 0xCA and 0xC1 log pages are runtime-PROBED, not assumed */
```

Two details worth keeping:

- **SN100 and SN200 are the only two families in the whole plugin granted
  `WDC_DRIVE_CAP_PURGE`.** `0xDD`/`0xDE` are ours and nobody else's.
- **WD themselves did not know whether a given SN200 firmware exposes the `0xCA`
  and `0xC1` log pages** — the plugin probes at runtime. And the `0xCA` "CA log"
  decode is further gated on `cust_id == 0x1005`; a different customer ID yields
  garbage, not an error.

---

## 3. Cross-reference: the `0xFF` erase family — CONFIRMED, exactly

This is the strongest result in the document, because `sn200_oracle.py`
*executes* this dispatch and the vendor tool independently *emits* it.

| vendor constant | computed CDW12 | our oracle, executed |
|---|---|---|
| `CLEAR_CRASH_DUMP_CMD 0x03`, `SUBCMD 0x05` | `(0x05<<8)\|0x03` = **`0x0503`** | verb 3 (section erase), **section 11 = CLOG / crash dump** ✔ |
| `CLEAR_CRASH_DUMP_CMD 0x03`, `CLEAR_PF_CRASH_DUMP_SUBCMD 0x06` | **`0x0603`** | verb 3 (section erase), **section 10 = PFCL / PFail crash dump** ✔ |

Two encodings, two independent derivations, exact agreement on both the opcode
byte, the sub byte, *and* which dump each one erases. **CONFIRMED.**

Operationally important, and not previously written down here: **`nvme wdc
get-crash-dump` and `get-pfail-dump` issue the `0xFF` clear automatically after a
successful read.** `wdc_do_clear_dump(hdl, 0xFF, cdw12_clear)` runs on the
success path of both. The vendor tool's normal, documented behaviour is to
*destroy the dump it just retrieved.* Anyone reaching for the stock `nvme wdc`
subcommands on a latched drive instead of our own retrieval procedure should
know that before they run it once.

The other seven `0xFF` encodings the oracle enumerates (`0x0003`, `0x0004`,
`0x0007`, `0x0103`, `0x0203`, `0x0303`, `0x0403`) have **no vendor name at all**.
They are firmware-internal arms WD's host tooling never emits. That is a clean
negative and it is consistent: the plugin only ever needed to clear two dumps.

---

## 4. Cross-reference: `0xC6` command `0x20` — CONFIRMED, six for six

`sn200-c6-dispatch.md` §4 enumerated nine sub-commands of `0xC6`/`0x20` out of
the dispatcher at `0x30030d14`. The plugin names six of them:

| CDW12 | vendor constant pair | our independently traced identity |
|---|---|---|
| `0x0020` | `DRIVE_LOG_CMD 0x20` / `DRIVE_LOG_SUBCMD 0x00` | drive-log body ✔ |
| `0x0120` | `DRIVE_LOG_SIZE_CMD 0x20` / `SUBCMD 0x01` | drive-log + string-table sizes ✔ |
| `0x0320` | `CRASH_DUMP_SIZE_CMD 0x20` / `SIZE_SUBCMD 0x03` | crash-dump size / armed probe ✔ |
| `0x0420` | `CRASH_DUMP_CMD 0x20` / `SUBCMD 0x04` | crash-dump body ✔ |
| `0x0520` | `PF_CRASH_DUMP_SIZE_CMD 0x20` / `SIZE_SUBCMD 0x05` | pfail-dump size / armed probe ✔ |
| `0x0620` | `PF_CRASH_DUMP_CMD 0x20` / `SUBCMD 0x06` | pfail-dump body ✔ |

Six for six, on encodings nobody had a vendor name for when they were traced.
**CONFIRMED.** It also retroactively validates the method: the arms were
identified from log-string containment inside confirmed function extents, and
the vendor tool agrees with every one.

Unnamed by the plugin, and therefore still ours alone: `0x0220` (string-table
body) and the two 71808-byte producers `0x0720` / `0x0820`, which remain
**do-not-send / unaudited mutation** exactly as `sn200-c6-dispatch.md` §4.1 has
them. The plugin's silence is not evidence they are safe; it is evidence WD's
host tooling has no use for them.

`WDC_NVME_CAP_DIAG_SUBCMD 0x00` / `CAP_DIAG_CMD 0x00` — i.e. `CDW12 = 0x0000` —
belongs to opcode **`0xE6`**, not `0xC6`. Our dispatcher routes `0xE6` to a
resident handler at `0x7ffb375c` (`sn200-opcode-map.md` §2.2) and the plugin
calls it **Capture Diagnostics**; the E6 header read is `cdw10 = 2`, `cdw12 = 0`,
8 bytes, with the length in bytes `[4..7]` **big-endian**. `0xE6` is admitted by
the Post-Crash gate on the opcode alone.

---

## 5. Cross-reference: `0xCC` — the resize name lands, the capability grant does not

| | |
|---|---|
| vendor | `WDC_NVME_DRIVE_RESIZE_OPCODE 0xCC`, `cdw12 = (0x01<<8)\|0x03 = 0x0103`, `cdw13 = new_size` |
| ours | `0xCC` command byte `0x03` → `0x30033eb0`, "Resize + `Admin_VUC_Device_Config_Modify_OVL024` (DCMod)" (`sn200-opcode-map.md` §4) |

The opcode, the command byte and the sub byte all agree, and our arm was already
labelled a resize. **`0xCC` `CDW12 = 0x0103` = Drive Resize. ADOPTED**, and it
promotes §4's tentative "Resize +" to a named encoding with the exact sub byte.

**But note the discrepancy, because it matters for how you read this plugin:**
the SN200's capability mask grants **no** `WDC_DRIVE_CAP_RESIZE`, so
`nvme wdc drive-resize` refuses to run against our drive — yet the SN200
firmware demonstrably implements `0xCC` command `0x03`. The plugin's capability
table describes **what nvme-cli will offer you**, not **what the controller
implements.** Never read a missing capability bit as "the firmware does not have
it". `0xCC` is a live, healthy-drive-only configuration surface on this drive
regardless of what the vendor tool will let you type.

---

## 6. Cross-reference: `0xC6` command `0x22` — a name, held loosely

| | |
|---|---|
| vendor | `CLEAR_PCIE_CORR_OPCODE 0xC6`, `CMD 0x22`, `SUBCMD 0x04` → `CDW12 = 0x0422`; the SN200 **is** granted `WDC_DRIVE_CAP_CLEAR_PCIE` |
| ours | `0xC6` command `0x22` = `VUC Reset Drive Stats` (StrId 1602), sub bytes `0`–`4` implemented |

Sub `4` is inside the implemented range and the SN200 is explicitly capability-
gated for exactly this operation, so: **`0xC6` `CDW12 = 0x0422` = Clear PCIe
Correctable Error Count. ADOPTED, not confirmed** — no PCIe string was found
inside the sub-4 arm's extent (the `0x22` subtree is `jx`-dispatched and its arms
were not individually walked). The umbrella name "Reset Drive Stats" stands; the
sub-4 label is the vendor's, on circumstantial firmware support.

Same command byte, sub `6` (`DRIVE_INFO_CMD 0x22` / `SUBCMD 0x06` → `0x0622`):
**REJECTED.** Our `0x22` dispatcher implements subs `0`–`4` only, and the SN200
is granted no `WDC_DRIVE_CAP_INFO`. `vs-drive-info` is a later-family command.

`0x22` in all its forms is **rejected while latched** (`sn200-c6-dispatch.md`
§6) — the gate admits `0xC6` only with command byte `0x20` or `0x30`. This is
the "our `0x22`, rejected while latched" observation, now with a vendor name on
it and with its correct shape: `0x22` is a **sub-command of `0xC6`**, never a
top-level opcode.

---

## 7. `0xD8`: the encoding WD moved our `0xFF` to — REJECTED for the SN200, but informative

```c
WDC_NVME_CLEAR_ASSERT_DUMP_OPCODE   0xD8
WDC_NVME_CLEAR_ASSERT_DUMP_CMD      0x03
WDC_NVME_CLEAR_ASSERT_DUMP_SUBCMD   0x05     /* cdw12 = 0x0503 */
```

`0xD8` is **not implemented** on the SN200 — every value in `0xCD`–`0xD3`,
`0xD5`, `0xD6`, `0xD8` reaches "Admin cmd not supported" (`sn200-opcode-map.md`
§2.2, PROVEN). Added to the plugin 2019-01-28, three years after this drive.

It is worth recording anyway, because **`0xD8` carries the identical `CDW12`
layout our `0xFF` uses — command byte `0x03` = the erase family, sub byte = the
section selector, `0x05` = the crash dump.** WD kept the sub-command grammar and
moved it to a new opcode on later drives. That is independent corroboration, from
a completely different direction, of our reading that `0xFF` command `0x03` is a
generic *"act on EEPROM section N"* dispatcher rather than a set of unrelated
special cases — which is the whole reason `0x0303` and `0x0403` are as dangerous
as they are.

---

## 8. The unnamed opcodes: a clean negative

The six vendor opcodes `sn200-opcode-map.md` put on the map with no names, plus
the two long-known ones, checked against the entire history of the WDC plugin:

| opcode | our identity | in any WDC source? |
|---|---|---|
| `0xC8` | VCAP failure injection (sub 0 = fake vcap failure, sub 1 = clear) | **No.** Master's `0xC8` is `GET_DEV_MGMNT_LOG_PAGE_ID_C8`, a **log page id** on later drives, not an admin opcode. Different thing entirely. |
| `0xC9` | 114-byte coroutine stub, unidentified | **No.** Master's `0xC9` is `ADMIN_ENC_MGMT_SND` — **OpenFlex enclosure** management, a different product. Do not adopt. |
| `0xD4` / `0xD7` | 11-arm diagnostics / power-off / FW-slot-erase / error injection | **No.** Master's `0xD4` is an *enclosure NIC* crash-dump log id; `0xD7` is a bare log id in a directory decoder. Neither is a drive admin opcode. Do not adopt. |
| `0xD9` | alias of the `0x0D` Namespace Management handler | **Not found anywhere in nvme-cli, ever.** |
| `0xEF` | `Admin_VUC_Mi_Test_OVL022`, NVMe-MI inject / retrieve | **Not found anywhere in nvme-cli, ever.** |
| `0xEC` | `Admin_VUC_Enable` — the VUC-Control mode setter | **Not found anywhere in nvme-cli, ever.** |
| `0xCA` | the 67-entry VUC Flash family | **No.** Master's `0xCA` is `ADMIN_ENC_MGMT_RCV` (enclosure) and, separately, a *log page id* (`GET_DEVICE_INFO_LOG_OPCODE`). Our `0xCA` is an **admin opcode** and unrelated to both. |
| `0xC6` | VUC SCSI Ported Command | **Yes** — see §4. |

**`0xEC` is the important one.** It is on the Post-Crash allow-list, it is a
*mode setter*, and it has no vendor name and no vendor tooling anywhere. Nothing
external is coming to help with it.

The `0xC9` / `0xCA` / `0xD4` collisions are the trap this whole document exists
to prevent: the plugin covers enclosures and a decade of later drives in one
namespace, and three of our unnamed opcodes collide numerically with an
*enclosure* command or a *log page id*. Adopting any of those names would have
been actively wrong.

---

## 9. Not the SN200: opcodes that belong to later families

Recorded so nobody aims one at this drive on the strength of the plugin naming
it. All post-date the SN200; all are absent from its dispatcher (**PROVEN** —
each reaches `0x7ffa75b4`, "Admin cmd not supported").

| opcode | vendor name | added |
|---|---|---|
| `0xFA` | `CAP_DUI_OPCODE` — Device Unit Info capture | 2019-01-28 |
| `0xFB` | `NAMESPACE_RESIZE_OPCODE` (also a NAND-stats log id) | later |
| `0xD8` | `CLEAR_ASSERT_DUMP_OPCODE` | 2019-01-28 |
| `0xD2` | VUC log-page-directory / drive-info / clear-PCIe | later |
| `0xD1` | `PCIE_STATS_OPCODE` | later |
| `0xD0`, `0xC0`, `0xC2`, `0xC3`, `0xC4`, `0xC5`, `0xCB` | log page ids on later drives | 2020+ / OCP |

**The DUI path (`0xFA`) in particular is SN640/SN840-era. Do not aim it at an
SN200.**

Conversely, `0xC0` and `0xC2` appear in the 2017 plugin as *Drive Essentials*
**admin opcodes** (`WDC_DE_VU_READ_SIZE_OPCODE 0xC0`,
`WDC_DE_VU_READ_BUFFER_OPCODE 0xC2`) as well as log page ids elsewhere — a real
footgun in the plugin's own namespace. Our dispatcher implements **neither** as
an admin opcode (`0xC0`–`0xC5` all reach "not supported"), so the Drive
Essentials path cannot work on an SN200 either.

---

## 10. What this changed, and what it did not

**Changed:**

1. `0xFF` `0x0503` / `0x0603` and the six `0xC6`/`0x20` sub-commands are now
   **confirmed against an independent source**, name and encoding both.
2. `0xCC` `CDW12 = 0x0103` has a name — Drive Resize.
3. `0xC6` `CDW12 = 0x0422` has an adopted name — Clear PCIe Correctable Errors.
4. The `CDW12 = (sub << 8) | cmd` grammar is corroborated from outside the
   firmware, and `0xD8` on later drives shows WD carried that grammar forward.
5. We now know the SN200's *vendor-visible* surface is frozen at February 2017,
   which bounds how much more naming is available: essentially none.

**Not changed:**

- No command's safety classification moved. `sn200-command-reference.md` is
  unaffected except for the `0x0303` correction, which came from the firmware,
  not from here.
- Six of our opcodes remain unnamed and will stay that way. The public record
  does not contain them.
- **A vendor name is not a permission.** `0xFF` `0x0503` has a name, an
  encoding confirmed twice over, and a WD-supplied tool that sends it — and it
  is still classified CATASTROPHIC on a latched drive, because its resume posts
  a drive re-init when the startup mode is 6. WD's tooling was written for a
  healthy drive.
