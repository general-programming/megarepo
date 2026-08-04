# Writing `KNGND122` into every writable firmware slot of an Ultrastar SN200

Target: `HUSMR7676BDP3Y1` (SN200 SFF 7.68 TB, PCI `1c58:0023`), five drives,
currently running `KNGND122`. Goal: every *writable* slot holds `KNGND122`, so
no future activation — deliberate or accidental — can land the drive on
`KNGND100`/`KNGND110`, which have the PFAIL/shutdown defect family open.

Everything below was desk-verified. **No hardware was touched.** Every claim is
labelled PROVEN (derived from the firmware image or WD's own library, verifiable
here), INFERRED (a short deduction from something proven), or SPECULATIVE.

Prerequisites: `docs/sn200-firmware-availability.md` (why `KNGND122` is
terminal), `docs/sn200-firmware-re.md` and `docs/sn200-independent-re.md` (the
firmware RE this builds on), `.claude/skills/nvme-recovery/SKILL.md`.

---

## 0. Executive summary

| question | answer |
|---|---|
| How many slots, which are writable? | **5 slots; slot 1 is read-only; slots 2, 3, 4, 5 are writable.** PROVEN two ways. |
| Send `KNGND122.bin` raw or unwrap it? | **Raw. The whole 1 762 048-byte file, byte for byte, unpadded.** PROVEN from WD's own `libdmi_core`. Do NOT extract the tar. Do NOT pad it. |
| Which commit action fills a slot without activating? | **CA=0.** PROVEN implemented on this firmware. |
| Does the fill need a reset? | **No.** CA=0 neither activates nor resets. The drive keeps running whatever it is running. |
| Can a failed commit brick a drive? | **No, not with CA=0** — the active slot and the read-only slot 1 are never written. INFERRED, high confidence. |
| Is it irreversible? | Only in that the *previous* contents of slots 2–5 are gone. The downgrade path itself stays open (unlike `+sblpatch+k`). |
| Biggest real trap | Using `KNGND110.bin` (which is secretly `+sblpatch+k`) or letting the tooling pick a slot for you (`FS=0`). |

---

## 1. The `frmw` decode — 5 slots, slot 1 read-only

The drive reports `frmw = 0x0b` in Identify Controller byte 260.

NVMe Base Specification, Identify Controller `FRMW`:

| bits | field | value here |
|---|---|---|
| 0 | first firmware slot (slot 1) is read only | `1` → **slot 1 is read-only** |
| 3:1 | number of firmware slots supported | `0b101` = **5** |
| 4 | supports firmware activation without a reset | `0` → **not supported** |

### Independent confirmation #1 — WD's own library (PROVEN)

`libdmi_core.so.0.39`, `nvmec_get_fw_num_slots` @ `0x58b60`, reading Identify
Controller byte `0x104` (= 260):

```asm
0x58bbf  movzx eax, byte [arg_104h]   ; FRMW
0x58bc6  mov   r15d, eax
0x58bc9  sar   r15d, 1
0x58bcc  and   r15d, 7                ; num_slots  = (frmw >> 1) & 7
0x58bd5  mov   byte [r12], r15b
0x58be0  and   eax, 1                 ; read_only  = frmw & 1
0x58beb  mov   byte [r13], al
```

logged as `fw %s slots %u read only %u`. Exactly the spec decode; `0x0b` → 5
slots, read-only set.

### Independent confirmation #2 — which slot is the read-only one (PROVEN)

The spec says "the first firmware slot"; two independent artefacts confirm it is
slot **1** specifically, and that read-only means *not writable*, not
*not activatable*.

Host side, `nvmec_validate_manage_firmware` @ `0x58f90`:

```asm
0x59075  call  nvmec_get_fw_num_slots
0x59082  movzx eax, byte [var_fh]     ; num_slots
0x59087  cmp   r12, rax               ; requested slot
0x5908a  ja    0x5918b                ; -> rc -1016, slot out of range
0x59090  cmp   byte [var_eh], 0       ; read_only flag
0x59095  je    ok                     ; not read-only: anything goes
0x5909b  cmp   r12, 1
0x5909f  jne   ok                     ; only slot 1 is special
0x590a5  hdm_json_obj_get_bool("load")
0x590b8  je    ok                     ; activate-only on slot 1 is allowed
         ...                          ; load into slot 1 -> rc -1004
```

`-1004` is `HDMS_FIRMWARE_SLOT_READ_ONLY` ("Firmware slot is read only").

Device side, PROC8 overlay `0x30000000` (the Firmware Commit handler):

```
30025d36: l32r  a8,0x30025464 ; l8ui a8,a8,0   ; configured slot count (BSS)
30025d3c: extui a9,a10,0,3                     ; FS = CDW10[2:0]
30025d3f: bge   a8,a9,0x30025e48               ; FS <= count -> continue
30025d47: l32r  a10,0x30025504                 ; LOG 2187 "Firmware Activate Invalid Slot"
...
30025eb1: l32i.n a11,a7,0x0
30025eb3: beqz  a11,0x30025ef7                 ; -> LOG 2189 "Invalid Image"
30025eb8: beqi  a13,1,0x30025ef7               ; -> LOG 2189 "Invalid Image"
30025ebb: extui a12,a10,0,3                    ; FS again
30025ebe: bnei  a12,1,0x30025ed6               ; FS != 1 -> proceed
30025ec6: l32r  a10,0x30025504                 ; FS == 1 -> LOG 2187 "Invalid Slot"
```

The firmware itself rejects `FS = 1` on the image-replacing path, *after* the
image has validated. So a mistaken `--slot=1` is a clean refusal, not a brick.

> **Reproduce:** `SN200_FW=~/sn200fw python3 tools/sn200-fw/logscan.py 'Firmware Activate'`
> then disassemble `0x30025c20`–`0x30025f40` in `PROC8_30000000.bin`.

**Conclusion (PROVEN): writable slots are 2, 3, 4 and 5. Slot 1 holds a
factory image that this procedure cannot touch, and that is the point.**

### Corroboration from the SPI layout (INFERRED)

PROC0 `0x7ff84a74`, the default SPI-EEPROM table of contents, contains exactly
five `FRMW` records (every other section is duplicated for redundancy; `FRMW` is
not). One physical region per firmware slot. Consistent, not by itself proof.

### `FS = 0` is a live footgun (PROVEN)

The range check is `FS <= slot_count`, so `FS = 0` **passes**. Per the NVMe spec
`FS = 0` means "the controller shall choose the firmware slot". You do not want
the controller choosing. **Always pass an explicit slot.** `nvme fw-commit`
defaults `--slot` to 0.

---

## 2. What goes on the wire — the whole bundle, verbatim

This was the highest-risk unknown. It is now settled.

### The file is a tar with a vendor CRC in each header and a 256-byte trailer (PROVEN)

`KNGND122.bin`, 1 762 048 B, sha256 `b11298346020af0f3a859e5a0d849c464eed186c9a102cf8956b3f6c44db3e70`:

```
[ustar member FWHEADER.bin        64 B]   ; "KNGND122" + 01 00 00 00
[ustar members PROC0..PROC15.bin        ]
[ustar member FCC.bin                   ]
[ustar member StringTable.csv.gz        ]
[ustar member SECURITY.bin      1 600 B ]
[512-byte zero end-of-archive block     ]   ; ONE block, not the usual two
[256-byte high-entropy trailer          ]   ; at EOF-256, entropy 7.16
```

Two non-obvious properties, both verified locally across all three revisions:

1. **Bytes 508–511 of every ustar header hold a little-endian
   CRC-32/MPEG-2 (poly `0x04C11DB7`, init `0xFFFFFFFF`, no reflection, no final
   XOR) of that member's data.** Verified: 61/61 members across `KNGND100`,
   `KNGND110` and `KNGND122` match. Standard `tar` writes zeros there. This is a
   vendor extension, and it means **the drive parses the tar** — it is not
   treating the file as an opaque blob.
2. The file does **not** end with the two zero blocks a normal tar has. It ends
   with one zero block and then 256 bytes of high-entropy data — almost certainly
   an image signature (SPECULATIVE as to its algorithm; PROVEN as to its
   position and that it is present in every revision, and different in each).

`SECURITY.bin` is byte-identical across `KNGND100`/`110`/`122`, so it is a key or
policy blob, not a per-image signature. The per-image thing is the 256-byte
trailer.

`KNGND110.bin` carries a **21st member, `SBLPATCH.bin` (269 470 B)**, that
`KNGND122.bin` does not. That single structural difference is the cleanest
machine-checkable test for the `+sblpatch+k` image — see §6.

### WD's own tool sends the file unchanged (PROVEN)

`libdmi_core.so.0.39`, `nvmec_fw_img_dl` @ `0x591a0`, the function behind
`hdm manage-firmware --load -f <file>`:

```asm
0x591cf  call  hdm_load_file(path, &buf, 4)   ; eax = file size
0x591de  add   ebp, 3
0x591eb  and   ebp, 0xfffffffc                ; round size UP to a dword
0x591ee  xor   edx, edx                       ; dwstart = 0
0x591f5  movsxd rcx, ebp
0x591fb  shr   rcx, 2                         ; dwlength = size/4
0x59201  call  [nvme_firmware_download_real]  ; ONE SHOT, whole file
```

No parsing. No member extraction. No header stripping. No 4 KiB padding. The
entire file, from byte 0, at dword offset 0.

If the one-shot transfer fails (it always will on Linux — the kernel caps admin
data transfers well below 1.7 MB), it logs `rc: %d. falling back to dl by page`
and re-sends in **4096-byte pages**:

```asm
0x592c0  xor   r8d, r8d
0x592c3  mov   edx, r12d                ; running dword offset
0x592c6  mov   ecx, 0x400               ; 0x400 dwords = 4096 bytes
0x592d1  add   r12d, 0x400
0x592d8  call  [nvme_firmware_download_real]
0x592e6  add   r13, 0x1000              ; advance buffer 4096 B
...
0x592fe  cmp   ebx, 0x3ff
0x59304  ja    0x592c0                  ; loop while >= 0x400 dwords remain
0x5930c  mov   ecx, ebx                 ; final PARTIAL chunk, whatever is left
0x59317  call  [nvme_firmware_download_real]
```

and `nvme_firmware_download_real` @ `0x8f7c0` is textbook NVMe:

```asm
0x8f8a2  lea   eax, [r12 - 1]           ; NUMD = dwlength - 1  (0-based)
0x8f8a7  mov   byte [cmd], 0x11         ; opcode 0x11, Firmware Image Download
0x8f898  mov   dword [cmd+0x2c], r14d   ; CDW11 = OFST, offset in DWORDS
0x8f8af  mov   dword [cmd+0x28], eax    ; CDW10 = NUMD
0x8f8c1  shl   rdx, 2                   ; data length = dwlength * 4
```

### Consequences

- **`nvme fw-download --fw=KNGND122.bin` is correct and sufficient.** PROVEN.
- **Do not pad.** `1762048 % 4096 == 768`, so the last page *is* a partial
  768-byte transfer — and WD's own code path sends exactly that. The
  `KNCCD122_padded.bin` floating around the ServeTheHome thread is a
  Windows/HDM storport artefact (that IOCTL wants 4 KiB-aligned buffers), not a
  device requirement. Padding would append bytes after the 256-byte trailer; if
  the drive locates the signature at `total_length - 256`, padding breaks
  validation. Never verified either way — so **do not risk it**. PROVEN that
  unpadded works for WD's Linux tool; SPECULATIVE that padded also works.
- **Do not unwrap.** Sending `PROC0.bin` alone, or the tar with the trailer
  stripped, drops the signature and the header CRCs.
- `1762048 % 4 == 0`, so `nvme-cli`'s "size must be a multiple of 4" check passes
  without help.

### Why `hdm manage-firmware --load` was reported to reject `KNGND122.bin`

Nothing in `nvmec_fw_img_dl` can reject it — it never looks inside the file.
The rejection reported on Level1Techs is host-side, upstream of the download, in
`hdm`'s own image validator (or is the read-only-slot / slot-range rejection
above, misread). **`nvme fw-download` bypasses it entirely.** INFERRED, high
confidence: the byte stream the vendor tool puts on the wire is exactly the
file, so if `nvme-cli` sends the same bytes the drive cannot tell the difference.

---

## 3. Commit semantics — CA=0 is the one you want, and it is implemented

`nvme fw-commit` CDW10: `FS` = bits 2:0, `CA` = bits 5:3, `BPID` = bit 31.

| CA | NVMe meaning | WD's own label (`HDME_MANAGE_FIRMWARE_COMMIT_ACTION_*`) | on this drive |
|---|---|---|---|
| 0 | downloaded image replaces slot FS, **not** activated | `Image Replaced but Not Activated` | **implemented** |
| 1 | downloaded image replaces slot FS, activated at next reset | `Image Replaced and Activated at Next Reset` | implemented |
| 2 | existing image in slot FS activated at next reset | `Existing Image Activated at Next Reset` | implemented |
| 3 | image in slot FS activated immediately, no reset | `Existing Image Activated Without Reset` | **NOT implemented** |

### The CA decode, verified in the firmware (PROVEN)

PROC8 overlay `0x30000000`:

```
30025e48: extui a8,a10,3,2            ; CA = (CDW10 >> 3) & 0x3   -- TWO bits, not three
30025e4b: blti  a8,3,0x30025c40       ; 0,1,2 -> real handler
30025e53: l32r  a10,0x30025518        ; else LOG 2188 "Firmware Activate Invalid Activation Action"
30025e56: call8 0x3001d9a0
30025e59: l32r  a9,0x3002551c         ; = 0xC0040000
```

`0xC0040000` is the CQE DW3; `>> 17` = `0x6002` = DNR=1, M=1, SCT=0 (Generic),
SC=`0x02` **Invalid Field in Command**.

Two things follow, both PROVEN:

- **CA=3 is unimplemented**, consistent with `frmw` bit 4 = 0. Do not use it,
  and do not read a failure of CA=3 as a drive fault.
- Because only **two** bits are extracted, **CA=4/5/6 alias onto 0/1/2** and
  CA=7 aliases onto 3. The NVMe 1.4 boot-partition commit actions are silently
  reinterpreted as ordinary slot commits. Never pass `--bpid` or `-a` > 3 to
  this drive.

The same 2-bit extraction feeds the log line
`Firmware Activate Action=%02X, Slot=%02X` (StrId 2184) at `0x30025f2c`.

### CA=0 writes without selecting (INFERRED, high confidence)

The handler's three sub-operations are named by its own strings:

| StrId | string |
|---|---|
| 2190 | `Firmware Activate System Check Image failed 0x%x` |
| 2191 | `Firmware Activate System **Write** Image failed 0x%x` |
| 2192 | `Firmware Activate System **Select** Image failed 0x%x` |

Check → Write → Select. CA=0 = check + write; CA=2 = select only; CA=1 = all
three. This matches the spec and WD's own labels exactly. I did not decode the
CA-dependent branch that skips Select (it sits inside FLIX bundles), hence
INFERRED rather than PROVEN.

### The reset requirement, and the dual-port trap (PROVEN)

Immediately inside the commit handler:

```
30025c76: l32r  a11,0x300254e0 ; l32i.n a11,a11,0    ; configured port count
30025c83: bnei  a11,2,0x30025ca2                      ; != 2 ports ?
30025c8b: l32r  a10,0x300254e4   ; LOG 2193 "Dual Port: Subsystem reset required to activate firmware"
30025c91: l32r  a13,0x300254e8   ; status 0x42200000
...
30025ca8: l32r  a10,0x300254ec   ; LOG 2194 "Conventional reset required to activate firmware"
30025cae: l32r  a8, 0x300254f0   ; status 0x42160000
...
30025cc2: l32r  a10,0x300254f4   ; LOG 2195 "Any reset required to activate firmware"
```

- `0x42200000 >> 17` = `0x2110` → SCT=1 (Command Specific), SC=`0x10`
  **Firmware Activation Requires NVM Subsystem Reset**.
- `0x42160000 >> 17` = `0x210B` → SCT=1, SC=`0x0B`
  **Firmware Activation Requires Conventional Reset**.

**On a dual-ported drive — and `HUSMR7676BDP3Y1` is the dual-port U.2 SKU — a
plain `nvme reset` will NOT activate a committed image.** It needs an NVM
subsystem reset or a power cycle. Given that every in-band reset on these drives
drops `CC.EN` without a preceding `CC.SHN` and is therefore itself an unclean
stop that can re-arm the Post Crash latch (see
`docs/sn200-nondestructive-recovery.md`), **the only activation you should ever
perform on these five drives is a clean OS shutdown followed by a cold power
cycle.**

None of that applies to the fill procedure, because CA=0 activates nothing.

### One more gate nobody has documented (PROVEN)

StrId 2970: `Firmware Commit called from wrong port. Locked To: %x, Caller: %x`,
returning `0xC2260000` (`>> 17` = `0x6113` → DNR=1, SCT=1, SC=`0x13`
**Firmware Activation Prohibited**). The download/commit sequence is **locked to
the PCIe port that started it**. If a drive is presented over both ports (two
`/dev/nvmeN` nodes for one subsystem — check `nvme list-subsys`), every
`fw-download` and `fw-commit` for a given drive must go through the *same*
device node. Do not let a multipath layer pick.

### Does the downloaded image survive a commit?

The NVMe spec does not guarantee the controller retains the download buffer
across a Firmware Commit, and nothing in this firmware promises it either.
**Re-download before every commit.** It costs ~1.7 MB and a couple of seconds
per slot and removes the entire question. This is what the script does.

---

## 4. The procedure

Run it on **one drive first** — the already-latched one, which has nothing left
to lose — and read the `fw-log` output before touching the other four. Nothing
below has been executed against real hardware.

Do all of this from a host that is **not** using the drive (unmounted, not in a
pool, no Ceph OSD on it). CA=0 does not disturb I/O, but there is no reason to
find out the hard way.

### Step 0 — authenticate the image (host-side, no drive involved)

```sh
sha256sum KNGND122.bin
# b11298346020af0f3a859e5a0d849c464eed186c9a102cf8956b3f6c44db3e70
stat -c %s KNGND122.bin
# 1762048
tar -tf KNGND122.bin | grep -c SBLPATCH   # MUST print 0
```

An image that fails any of these three must not be sent. See §6.

### Step 1 — record the starting state (read-only)

```sh
nvme id-ctrl  /dev/nvmeN -o json | jq '{mn,sn,fr,frmw,cmic,fwug}'
nvme fw-log   /dev/nvmeN -o json
nvme list-subsys                     # is this drive dual-pathed?
```

Expected: `mn` contains `HUSMR7676BDP3Y1`, `fr` = `KNGND122`, `frmw` = 11
(`0x0b`). Save the output. `fw-log` gives `afi` and `frs1`..`frs7`:

- `afi` bits 2:0 = slot currently active
- `afi` bits 6:4 = slot to be activated at next reset (0 = none pending)
- `frsN` = the 8-character revision string in slot N, or all-zero if empty

**If `fr` does not start with `KNGN`, stop.** The drive is on an OEM branch and
`KNGND122.bin` will be refused (cleanly) as incompatible — see
`docs/sn200-firmware-availability.md`.

**If `afi` bits 6:4 are non-zero, stop.** A pending activation is already
staged; resolve that first.

### Step 2 — fill each writable slot (repeat per slot, N ∈ {2,3,4,5})

Skip any slot whose `frsN` already reads `KNGND122`, and skip the currently
active slot if it is already `KNGND122` (there is nothing to gain by rewriting
the image the drive is running).

```sh
nvme fw-download /dev/nvmeN --fw=KNGND122.bin --xfer=4096
nvme fw-commit   /dev/nvmeN --slot=2 --action=0
nvme fw-log      /dev/nvmeN -o json          # frs2 must now read KNGND122
```

then again with `--slot=3`, `--slot=4`, `--slot=5`, re-downloading each time.

Expected success: `fw-commit` returns 0 with no status text. `afi` **must not
change** — CA=0 never alters the active or next-active slot. If `afi` moves, you
sent the wrong action; stop immediately.

`--xfer=4096` matches WD's own page size. Larger values may work but there is
no upside and `nvme-cli` clamps non-4096-multiples anyway. If
`nvme id-ctrl` reports a non-zero `fwug`, the transfer size must be a multiple of
`fwug` × 4 KiB; 4096 satisfies `fwug = 1`, which is the only value that would
matter here.

### Step 3 — verify

```sh
nvme fw-log /dev/nvmeN -o json
```

All of `frs2`, `frs3`, `frs4`, `frs5` read `KNGND122`. `frs1` is unchanged
(whatever the factory put there). `afi` is unchanged.

### Step 4 — activation (usually: DO NOT)

If the drives are already running `KNGND122` — which they are — **there is
nothing to activate and this step must be skipped.** Ending the procedure here
means no reset, no unclean stop, no exposure to the Post Crash latch.

Only if a drive was found running something older:

```sh
nvme fw-commit /dev/nvmeN --slot=2 --action=2   # activate slot 2 at next reset
nvme fw-log    /dev/nvmeN -o json               # afi bits 6:4 == 2
# clean OS shutdown, then COLD power cycle (off >= 90 s)
nvme id-ctrl   /dev/nvmeN -o json | jq -r .fr   # KNGND122
```

Do **not** use `nvme reset` or `nvme subsystem-reset` — see §3. Do **not** use
`--action=1`; separating "write the slot" from "activate the slot" is what makes
every intermediate state safe.

---

## 5. Residual risk, honestly

### Can a failed commit leave a drive unbootable?

**No, not with CA=0.** INFERRED, high confidence, from three independent facts:

1. CA=0 never touches the Select step, so the boot-slot record is not modified.
   The drive continues to boot the slot it was already booting.
2. The target slot is never the active slot (Step 2 skips it) and never slot 1.
   A torn write can therefore only corrupt an inactive, non-factory slot.
3. The firmware validates before it writes — `Check Image` (StrId 2190) precedes
   `Write Image` (2191), and the image carries per-member CRC-32/MPEG-2 plus a
   256-byte trailer. A truncated or corrupted download fails Check and never
   reaches Write.

The realistic worst case is a slot left holding garbage. That is only dangerous
if something later *activates* it — see below.

The genuinely dangerous actions are all excluded by construction: no CA=1, no
CA=2, no CA=3, no slot 1, no `FS=0`, no reset.

### Is the downgrade path closed afterwards?

**No — and that is a correction to the premise.** Filling slots 2–5 with
`KNGND122` removes the *pre-staged* older images, so no activate-an-existing-slot
operation can ever land on `KNGND100`/`KNGND110`. But the slots remain writable,
so anyone with `KNGND100.bin` can still `fw-download` + `fw-commit --action=1`
and downgrade. INFERRED: nothing in the commit handler blocks a lower revision;
the only compatibility check found (`0x30025ed6`) compares a branch/config bit,
not a version ordinal, and its failure string is the cross-branch refusal.

That is the crucial difference from `+sblpatch+k`, which updates the secondary
boot loader and *does* close the door.

So the accurate statement is: **this procedure eliminates accidental
regression, not deliberate regression.** Which is exactly what was wanted.

### Could slot 1 ever be activated automatically, landing the drive on ancient firmware?

Two mechanisms, neither automatic:

- **Host-initiated:** only `fw-commit --slot=1 --action=2`. That is a deliberate
  act. It is also the documented rescue for a drive that will not boot — several
  SN200s on the Level1Techs thread were recovered exactly this way — so it is a
  feature, and it survives this procedure untouched.
- **Firmware-initiated:** the boot path logs `Firmware Boot Mode : WARM BOOT,
  DDR (Slot %d)` / `COLD BOOT, EEPROM (Slot %d)`, so a slot number is read from
  the SPI `SLOT` section at every start. Whether the boot ROM falls back to slot
  1 when the recorded slot fails to validate is **SPECULATIVE** — I could not
  find that path. It is the obvious reason for a read-only first slot to exist.

Either way the outcome is a drive running the factory image, which is *old* but
*boots*. That is a recoverable state, not a lost drive, and it is strictly better
than the alternative of having no fallback at all. Slot 1 stays as-is.

### What is irreversible, and where

The single irreversible moment is each `fw-commit --action=0` — the previous
contents of that slot are gone. Nothing else in the procedure is irreversible,
and no step changes what the drive boots.

### What cannot be verified without hardware

- That the drive accepts a 768-byte final `fw-download` chunk. WD's own library
  emits exactly that, so this is as close to proven as desk work gets — but it
  has not been observed on the wire.
- That CA=0 returns success rather than a "reset required" status. Per the spec
  it should complete with no status; the reset-required statuses live on the
  activate path.
- Whether these specific drives are configured dual-port (affects §3's reset
  requirement, not the fill).
- The `fwug` value, and therefore whether 4096 is the granularity floor.
- Whether the download buffer survives a commit (sidestepped by re-downloading).

**Run on ONE drive first — the latched one — and compare `fw-log` before and
after before touching the other four.**

---

## 6. ⚠ Do not use `KNGND110+sblpatch+k` for this

`firmwares/KNGND110.bin` in `HGST-UltraStar-SN200-HHHL.zip` is **byte-identical**
to `KNGND110+sblpatch+k.bin` — both sha256
`7210283c62ef88b08ace950fa53203f97d0dc34957ecab3b43fd565c758ccff2`, both
2 009 856 B. There is no plain `KNGND110` anywhere. The innocuous filename is a
trap.

Structurally it is the same tar plus a **21st member, `SBLPATCH.bin`
(269 470 B)**, which `KNGND122.bin` does not have. That is the machine-checkable
signature, and the script asserts on it.

Why it is wrong for this job, even ignoring that it is an older revision:

- It writes **every** slot, including the read-only one, via the secondary boot
  loader — destroying precisely the factory fallback this exercise is trying to
  preserve.
- It updates the SBL, after which WD does not support downgrade. That *is* a
  one-way door.
- It gives you no per-slot control, no verification point between slots, and no
  way to skip the active slot.
- `KNGND110` only *partially* fixed the PFAIL/shutdown defect family. `KNGND122`
  closed it.

The standard `fw-download` + per-slot `fw-commit --action=0` route is preferable
on every axis: it is incremental, verifiable between every step, leaves the
active slot and the read-only slot untouched, needs no reset, and leaves the
recovery path (activate slot 1) intact.

---

## 7. Tooling

`tools/sn200-fw/fill-fw-slots.sh` implements §4. By construction it can only
ever emit `fw-commit --action=0`, never `--slot=0` or `--slot=1`, and it refuses
any image containing `SBLPATCH.bin` or whose sha256 is not `KNGND122`'s. Those
invariants are asserted in `tools/sn200-fw/tests/test_fill_fw_slots.py` — the
same posture as `pull-crash-dump.sh`'s "can never emit `0xFF`" test.

```sh
tools/sn200-fw/fill-fw-slots.sh --image KNGND122.bin --dry-run /dev/nvme7
sudo tools/sn200-fw/fill-fw-slots.sh --image KNGND122.bin /dev/nvme7
```

Activation is deliberately **not** implemented. If a drive genuinely needs a
slot activated, do it by hand, once, with your eyes on it.
