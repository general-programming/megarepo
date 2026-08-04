# Can a modified SN200 firmware image be made to load?

Target: HGST/WDC Ultrastar SN200 (`HUSMR7676BDP3Y1`), firmware `KNGND122`, five
owner-operated drives, EOL, no vendor fix will ever exist. The goal is **repair** —
patch out the Post-Crash latch described in `docs/sn200-independent-re.md` — not
attack.

**Short answer: no, not without WD's private key.** The image is signed with
**RSA-2048 over SHA-256**, the three public keys are compiled into the running
firmware, verification is invoked from the image-processing path, and **no PSID,
TCG or security-level setting removes the RSA step.** A modified image is refused
cleanly at Firmware Commit ("Invalid Image"); it does not brick anything.

The rest of this document records what *is* now settled, because most of it is
newly proven and several prior claims are overturned:

- the exact container recompute recipe (useful the moment a key ever appears),
- a **one-byte** patch that fixes the defect, fully specified, ready to apply,
- two ways the patch can be *wrong* for a given drive, both detectable read-only,
- the boot-time slot fallback, which materially lowers the risk of the whole area.

Claims are labelled **PROVEN** (read directly out of a binary), **INFERRED**
(reasoned from structure) or **SPECULATIVE**. Everything here is static analysis.
**No hardware was touched and none should be.**

Companion documents: `docs/sn200-firmware-flashing.md` (the flashing runbook,
which this corrects in five places — see §8), `docs/sn200-firmware-re.md`,
`docs/sn200-independent-re.md`, `docs/sn200-shutdown-path.md`,
`docs/xtensa-flix-decoding.md`.

> **Method note.** Ghidra's decompiler was not used and must not be. Its FLIX
> bundles are opaque 8-byte pseudo-ops and slot B is a branch, so it silently
> omits roughly half the conditional flow. Everything below was decoded with
> `tools/sn200-fw/disany.py` / `xdis.py` (slots A, B and C) and reproduced by
> hand where it mattered.

---

## 1. Is image authentication enforced? **Yes.**

### 1.1 The validation pipeline spans three processors — PROVEN

| stage | processor | what happens |
|---|---|---|
| accept bytes | PROC8 (ADM/NVMe front end) | buffers the download, locks it to the PCIe port |
| Check / Write / Select Image | PROC8 → IPC → PROC0 | PROC8 does **no** validation itself |
| TAR walk: header checksum, octal size, member name, per-member CRC-32/MPEG-2, required-member mask | PROC0 (System Manager) | `0x7ffab900`–`0x7ffac500`, `0x7ffb4a3c` |
| SHA-256 + RSA-2048 signature, OEM/customer compat | PROC9 (CryptoMgr) | `0x7ffb06e8`–`0x7ffb0f58` |
| slot selection with automatic fallback | SBL (not in the corpus) | handoff struct `0x7ff9ff60` |

PROC8's three commit phases are all IPC round-trips, which is why the strings say
"System":

```
30025b92: LOG 2190 "Firmware Activate System Check Image failed 0x%x"
30025b07: LOG 2191 "Firmware Activate System Write Image failed 0x%x"
30025aa5: LOG 2192 "Firmware Activate System Select Image failed 0x%x"
```

each preceded by `call8 0x30018ec4` (alloc message) … `call8 0x30018e80`
(dispatch + wait) … `l32i a11,a5,0x80 ; beqz` (completion status).

### 1.2 There is real asymmetric crypto, and it is RSA-2048 over SHA-256 — PROVEN

All in `PROC9_7ff80000`:

| address | function |
|---|---|
| `0x7ff810b0` | SHA-256 round-constant table K — **the only copy in all 18 images** |
| `0x7ffbce10` | SHA-256 compression (sole user of K) |
| `0x7ffbcdd4` / `0x7ffbe038` / `0x7ffbe12c` | `sha256_init` / `_update` / one-shot |
| `0x7ffbdd6c` | RSA public operation (modexp) |
| `0x7ffbdf80` | bignum bit length |
| `0x7ffbcc74` | signature-block verifier — padding walk + `memcmp(digest,32)` at `0x7ffbcd2e` |
| `0x7ffbcbbc` | `getPublicKey(n)` |
| `0x7ffbe14c` | crypto self-test (SHA-256 + three RSA vectors) |

The modulus size is asserted explicitly:

```
7ffb0d9d: call8 0x7ffbdf80        ; bitlen(key)
7ffb0da0: movi  a11,-2041
7ffb0da3: add   a12,a10,a11
7ffb0da5: bgeui a12,8,<reject>    ; bitlen must be 2041..2048  =>  RSA-2048
```

No MMIO crypto accelerator is involved — it is software bignum on the Xtensa core.
The image is hashed in 256-byte chunks (`0x7ffb0e43`, `movi a13,256; minu`), with
the running address in `ctx+0x160` and the remaining length in `ctx+0x164`.

Three keys are tried in turn:

```
7ffb0c75: call8 0x7ffbcc74        ; verify padding + digest
7ffb0c78: beqz  a10,0x7ffb0d46    ; 0 = verified
7ffb0c7b: l32i  a7,a2,0x168       ; key index
7ffb0c7e: addi.n a7,a7,1
7ffb0c80: { s32i a7,a2,0x168 ; bltui a7,3,0x7ffb0d63 }   ; up to THREE keys
```

**Absence-of-evidence is not the finding here. The crypto is present, complete,
self-tested at boot, and reachable. Say so plainly.**

> **A tempting bypass at `0x7ffb0c8a`, and why it is not one. REFUTED, PROVEN.**
> After all three keys fail, control reaches
> `7ffb0c88: l32i.n a9,a2,0x28 ; 7ffb0c8a: beqz a9,0x7ffb0d48`, and `0x7ffb0d48` is
> the **success** logger (StrId 3403, `"Digital Signature is successfully
> verified"`). That reads like "if some flag is clear, report success anyway".
> It is not. `[a2+0x28]` is the **result word**, and it is preset to `1` (fail) in
> the unconditional slot A of the very bundle whose slot B branches on the security
> level:
>
> ```
> 7ffb0dcc: l32i.n a13,a2,0x2c                      ; security level
> 7ffb0dce: movi.n a9,1
> 7ffb0dd0: { s32i a9,a2,0x28 ; beqz a13,0x7ffb0df0 }   ; VERDICT := 1, unconditionally
> ```
>
> Both FLIX slots retire together, so the store runs on every path into the
> verifier. An exhaustive narrow+wide scan of every access to `[a2+0x28]` across
> `0x7ffb0600`–`0x7ffb1100` finds the only clearing store at `0x7ffb0d46`, and
> `0x7ffb0d46` has exactly one predecessor: `0x7ffb0c78 beqz a10`, taken only when
> the padding walk and the 32-byte digest `memcmp` at `0x7ffbcd2e` both succeed.
> Reaching `0x7ffb0c8a` with `a9 == 0` after three genuine failures is impossible;
> the `beqz` is a defensive re-check on a shared log tail.
>
> `disany.py` mis-frames `0x7ffb0dcc` because `0x7ffb0dca`–`0x7ffb0dcb` is `00 00`
> padding — force alignment there.

### 1.3 `SECURITY.bin` is the public-key blob — PROVEN, and it settles the premise

The lead that motivated this investigation was that `SECURITY.bin` is byte-identical
across `KNGND100`/`110`/`122`. It is — sha256
`8e8f86db99e6e55688b7a2ead9b03f617958f4f6292efbb88ae36936289604a9`, verified here
between `KNGND110+sblpatch+k` and `KNGND122`. The inference drawn from that
("a per-image signature cannot be identical, so authentication may be absent") was
reasonable and is now **refuted**, because the file is not a signature at all:

- `SECURITY.bin` = 0x40-byte header (`01 00 00 00`, eight zero bytes,
  `01 00 00 00`, zeros) + **1536 bytes**.
- Those 1536 bytes are **byte-identical to PROC9 image bytes
  `0x7ff823c0 … 0x7ff829c0`** — verified directly.
- The live key array is at **`0x7ff820c0`**, 768 B = **3 × 256 B RSA-2048 moduli**,
  and equals the first 768 bytes of the `SECURITY.bin` body with each 32-bit word
  byte-swapped — verified directly (`key0` = `bf7eb2a0…`, `SECURITY` block 0 =
  `a0b27ebf…`).
- `getPublicKey` is a bare index computation, which is why exactly three keys exist:

  ```
  7ffbcbbc: entry a1,0x20
  7ffbcbbf: { l32r a3,=0x7ff820c0 ; bgeui a2,3,0x7ffbcbce }
  7ffbcbc7: slli  a2,a2,8 ; add.n a2,a3,a2     ; key = 0x7ff820c0 + (n << 8)
  7ffbcbce: movi.n a2,0                        ; n >= 3 -> NULL
  ```

- Blocks 3–5 of the blob are not touched by `getPublicKey`; a separate literal
  `0x7ffa2138 = 0x7ff826c0` points exactly at block 3. **INFERRED:** Montgomery R²
  precomputes for the three keys. Not proven.
- A key-blob descriptor with a version field sits at `0x7ff81f78`
  (`78 1f f8 7f  78 1f f8 7f  01 00 00 00` = empty list head + version 1), which is
  what `"CRYPTO: Incompatible public keys (Version=%d)"` (`0x7ffb08d8`) tests.
- The drive also caches the blob in EEPROM: key-load coroutine `0x7ffb06e8`, with
  `"CRYPTO: Loading Public keys"` (`0x7ffb093a`), `"Cannot write Public keys to
  EEPROM"` (`0x7ffb075a`), `"Public keys are loaded from EEPROM"` (`0x7ffb08b2`).
- **`SECURITY.bin` is not a required tar member** (§3.4). It is redundant with the
  compiled-in copy. That is exactly why it never changes between revisions.

**So the per-image artefact is the 256-byte trailer, and it is an RSA-2048
signature.** Its size matches the modulus size exactly; it is high-entropy
(169 distinct byte values in 256, consistent with uniform random); and it is not
SHA-256, SHA-1, SHA-512, MD5 or CRC-32 of the preceding bytes in either
endianness — all tested and excluded.

### 1.4 The signature failure is a real, reachable status — PROVEN

PROC0's TAR engine issues an asynchronous request and latches the verdict:

```
7ffac224: call8 0x7ffb58a0                     ; dispatch to CryptoMgr
7ffac227: s32i.n a4,a2,0x24                    ; result := -1
7ffac229: l32i.n a9,a2,0x24
7ffac22b: { movi a10,1 ; bnei a9,-1,0x7ffac239 }   ; spin/yield until answered
7ffac23e: l32i.n a13,a2,0x24
7ffac240: { l32r a11,0x7ff836dc ; beqz a10,0x7ffac253 }   ; verified -> skip
7ffac248: { s16i a11,a2,0x34 ; movi a12,1 }    ; status := 3395
7ffac250: s32i a12,a2,0x288                    ; error flag
```

`*(0x7ff836dc) = 3395` — verified — and StringTable line 3396 (StrId 3395) is
**`failed (invalid digital signature)`**. The full TAR status vocabulary, stored as
a StrId in `ctx+0x34` and reported by `SYS: TAR <%s> command %s` (StrId 3401,
`0x7ffac41f`):

| StrId | literal | string |
|---|---|---|
| 3387 | `0x7ff83630` | `failed (CRC mismatch)` |
| 3393 | — | `failed (cannot write)` |
| **3395** | `0x7ff836dc` | **`failed (invalid digital signature)`** |
| 3396 | `0x7ff8372c` | `completed successfully` |
| 3397 | `0x7ff836f0` | `failed (damaged header)` |
| 3398 | — | `failed (unknown command)` |
| 3399 | `0x7ff836f4` | `failed (no firmware)` |
| 3400 | `0x7ff836f8` | `failed (empty TAR file)` |

> **Contested point, resolved.** One analysis pass concluded that
> "invalid digital signature" is only ever a *default* status set in the
> coroutine's entry block and therefore does not prove a verification call. That is
> **wrong**: `0x7ffac248` is a genuine failure store, guarded by a `beqz` on an IPC
> result, immediately after the dispatch at `0x7ffac224`. The status may *also* be
> used as an initial value elsewhere; that does not make `0x7ffac248` disappear.
> The off-by-one that produced the error was `0x0d44` read as 3395 when it is 3396.

### 1.5 The PSID / "security disabled" lead — a genuine dead end. PROVEN (negative)

This looked like the way in, and it is not. Recording it in full so nobody spends
the day on it again.

The strings promise a great deal:

```
2173  ADM: PSID active; FW download limited based on security critera.
2174  ADM: PSID not active; FW download allowed, security disabled.
1730  ADM: Admin_VUC_Sys_Set_Fw_Download_Psid_Validation_OVL025
1735  ADM: PSID not active; VUC has no effect; return good status
```

The predicate is `PROC8@7ff80000 0x7ffa9db8` (one caller, `0x7ffaccbb`):

```
7ffa9db8: entry a1,0x40
7ffa9dbb: l32r a2,=0x7ff9137c                  ; ADM security context
7ffa9dc0: { s32i a8,a2,0xc  ; movi a11,255 }   ; a8 = 0
7ffa9dc8: { s32i a8,a2,0x10 ; mov a10,a1 ; movi a12,32 }   ; default = 0
7ffa9dd0: call8 0x7ffba9d8                     ; memset(buf, 0xFF, 32)
7ffa9dd3: { l32r a11,=0x7ff8f3c0 ; mov a10,a1 ; movi a12,32 }
7ffa9ddb: call8 0x7ffba968                     ; memcmp(buf, g_psid, 32)
7ffa9dde: beqz.n a10,0x7ffa9df5                ; all-FF -> PSID NOT active
7ffa9de0: { l32r a11,->StrId 3347 ; movi a9,1 }
7ffa9de8: { s32i a9,a2,0x10 ; movi a10,20 }    ; g_fwdlSecState := 1
7ffa9df5: { l32r a11,->StrId 2173 ; movi a10,20 }   ; "security disabled"
```

So `g_fwdlSecState = *(0x7ff9137c + 0x10)`: `0` = PSID never programmed
(`g_psid` at `0x7ff8f3c0` is 32 × `0xFF`), `1` = PSID active, `2` = "Free FW
Download" as set by the VUC at `PROC8@30000000 0x300351ac`. That VUC is a strict
1↔2 toggle, PSID-authenticated, refuses outright when the state is 0, and writes
only DRAM — **per-boot, never persisted**.

**And `+0x10` is never read on the download or commit path.** A whole-fleet literal
scan finds exactly seven references to `0x7ff9137c`: the writer at
`PROC8@7ff80000 0x7ffa9dbb`, and six in the PROC8 overlay — `0x30025861`
(the FW handler), `0x300330c4`, `0x30033f8c`, `0x3003438f`, `0x300345a1`,
`0x300351b1`. Only `0x300351b1` (the VUC itself) touches `+0x10`. The FW handler
uses `+0x00`, `+0x04`, `+0x08`, `+0x0c`, `+0x14` and never `+0x10`:

```
300258f7 s32i.n a6,a7,0x18   300258fb s32i.n a5,a7,0xc
30025b98 s32i.n a6,a7,0x8    30025b9a s32i.n a6,a7,0x4
30025bc7 s32i.n a6,a7,0xc    30025c26 l32i.n a10,a7,0x4
30025eb1 l32i.n a11,a7,0x0   30025eb6 l32i.n a13,a7,0x8   30025ed9 l32i.n a11,a7,0x14
```

The struct's real job on that path is bookkeeping: `+0x00` = "an image is present"
(set to 1 at `0x30025723` when the host-to-DDR transfer completes), `+0x08` = "the
image failed Check" (set to 1 at `0x300255d2` / `0x30025b98`), `+0x14` = the PCIe
port the session is locked to.

**Verdict: "PSID not active; FW download allowed, security disabled" does not
disable signature checking. It is a mode variable that a vendor command validates
against itself.**

### 1.6 The security level relaxes the *compat* check, not the signature — PROVEN

There is a genuine bypass, and it is in the wrong place to help. `PROC9 0x7ffb0a40`:

```
7ffb0a42: l8ui a11,a6,0xb0                          ; a6 = 0x7ff83580, running image descriptor
7ffb0a45: { beqi a11,2,0x7ffb0b12 }                 ; level 2 = engineering -> alternate path
7ffb0a4d: l32i.n a13,a2,0x2c                        ; requested security level
7ffb0a52: { l8ui a12,a6,0x82 ; beqz a13,0x7ffb0b08 }  ; level 0 -> SKIP the comparison
7ffb0a5a: l8ui a10,a5,0x82                          ; new rev[2]
7ffb0a5d: l8ui a11,a5,0x83                          ; new rev[3]
7ffb0a60: { l8ui a14,a6,0x83 ; bne a12,a10,0x7ffb0a78 }   ; rev[2] mismatch -> FAIL
7ffb0a68: { movi a15,69 ; beq a14,a11,0x7ffb0b08 }        ; rev[3] equal -> pass
7ffb0a70: beq a10,a15,0x7ffb0af9                          ; new rev[2] == 'E' -> relaxed
7ffb0af9: movi.n a12,67 ; beq a11,a12 -> pass             ;   rev[3] == 'C' ok
7ffb0afe: movi.n a14,71 ; bne a11,a14 -> fail             ;   rev[3] == 'G' ok, else fail
7ffb0a78: -> LOG 3405 "CRYPTO: Firmware header verification failed (%s, security level %d)"
                       with %s = "customer mismatch", then 3406 "  current %c%c new %c%c"
```

The security level does one *other* thing, found late and worth recording: it
selects the **starting key index** for the RSA loop.

```
7ffb0dd0: { s32i a9,a2,0x28 ; beqz a13,0x7ffb0df0 }    ; a13 = security level
7ffb0dd8: { s32i a9,a2,0x168 ; movi a7,1 }             ; level != 0 -> start at key 1
7ffb0df0: { s32i a7,a2,0x168 ; j 0x7ffb0de0 }          ; level == 0 -> start at key a7 (0)
```

So **level 0 widens the accepted key set** — key 0 looks like an engineering key
admitted only at level 0 — rather than skipping anything. That is the opposite of a
bypass: it is a key-eligibility control, and it still requires a valid signature
under whichever key is tried. **INFERRED** (`a7`'s incoming value on the
`0x7ffb0df0` arm is set in a resume block that resists framing).

The compat check itself compares **characters 2 and 3 of the 8-character revision
string** between the
running image and the downloaded `FWHEADER.bin` (`KNG`**`N`**`D122` → `'G'`,`'N'`).
It is the OEM-branch gate. It is **identity, not ordinal** — which is why nothing
blocks a downgrade to `KNGND100`.

`beqz a13` at `0x7ffb0a52` means **security level 0 skips this check entirely**. But
the header stage only gates *entry* to the hash/RSA stage; on success it falls
through to `0x7ffb0b08 → 0x7ffb0df8` (SHA-256) → `0x7ffb0d63` (RSA). **Relaxing the
header check does not relax the signature.** PROVEN.

### 1.7 The verdict path, and the one honest gap left

**The verdict path is now traced end to end. PROVEN.** In CryptoMgr's reply helper
`0x7ffb0960` the result word is copied straight into the outgoing message:

```
7ffb0990: { l32i a12,a2,0x28 ; ... }                    ; the result word
7ffb099d: { s32i a12,a2,0x3c ; ... ; movi a12,12 }      ; into the reply body
7ffb09a5: call8 0x7ffbda90                              ; post 12-byte reply
```

`[a2+0x28]` → `[a2+0x3c]` → reply. That is exactly the value PROC0 tests with
`beqz a10` at `0x7ffac240` before latching `failed (invalid digital signature)`
(§1.4). Since `[a2+0x28]` is preset to `1` unconditionally (§1.2) and cleared only
by the digest match at `0x7ffb0d46`, **a tampered image produces a non-zero reply,
and PROC0 turns that into a TAR failure status.** No security level, PSID state or
key-index setting alters this.

**What is still not proven:** that PROC0's dispatch at `0x7ffac224` is addressed to
*CryptoMgr command 0* specifically. The cross-processor service-ID table was not
resolved, so the two halves are joined by shape rather than by an identifier:

- PROC0 dispatches a request and consumes a reply whose zero/non-zero meaning is
  "signature ok / not ok" (§1.4).
- PROC9 produces exactly such a reply, from exactly such a request (an
  `(address, length)` pair at `msg+0xc` / `msg+0x10`, streamed as `ctx+0x160` /
  `ctx+0x164`).
- The commit handler logs StrId 2196 `"Unable to retrieve TAR Vector and FW Public
  Key from System Manager"` at `0x300258d0`, on the failure arm of
  `0x300258b5: { s32i a8,a2,0x180 ; bnez a8,0x300258d0 }` — the commit path
  demonstrably wants a public key, and an `(address, length)` pair *is* a "TAR
  Vector".

**INFERRED, high confidence: the RSA check runs on the firmware image path.**
Closing it fully means resolving the message service-ID table — pure desk work, no
hardware.

**One loose end, flagged rather than smoothed over.** The success store is
`0x7ffb0d46: s32i.n a7,a2,0x28`, and `a7` is the *key index*
(`0x7ffb0d63: mov.n a10,a7` → `getPublicKey(a7)`). Read literally, the verdict
equals the index of the matching key, so only key 0 would yield a zero verdict.
Either `a7` is reloaded to 0 in the resume block at `0x7ffb0c42`–`0x7ffb0c78`
(heavily FLIX-bundled, could not be framed), or there is a latent restriction to
key 0. **Either reading makes the check stricter, not weaker**, so it does not
disturb the conclusion.

---

## 2. What is signed, and over what range

**INFERRED, well-supported.** The verifier hashes **one contiguous (address, length)
range** supplied by the requester, streamed in 256-byte chunks (`ctx+0x160` =
address, `ctx+0x164` = length, loop at `0x7ffb0e43`). CryptoMgr sees a flat buffer —
it has no knowledge of the tar, of `FWHEADER.bin`, or of a scatter list. The
"TAR Vector" of StrId 2196 is therefore that single descriptor handed down by the
System Manager, not a multi-entry table.

The recovered RSA value is compared against the 32-byte SHA-256 digest by
`memcmp(...,32)` at `0x7ffbcd2e`, after a padding walk.

**Not resolved:** where `total_length - 256` is computed. Every
`addmi rX,rY,-256` / `movi rX,-256` in PROC0 and PROC9 was swept and none sits on
the FW path; the length arrives over IPC from a producer neither pass identified.
So the *position* of the trailer is proven (it is the last 256 bytes of the file,
after a single 512-byte zero block) but the *code* that locates it is not.

Operational consequence, unchanged and now reinforced: **do not pad the image.**
`1762048 % 4096 == 768`, WD's own library emits exactly that short final chunk, and
if the split is `total_length - 256` then padding moves the signature and
verification fails.

---

## 3. What would have to be recomputed

This section is written for the case where a private key ever becomes available —
or where someone wants to prove to themselves that the container is not the
obstacle. **The container is fully solved. The signature is the only blocker.**

### 3.1 Container layout — PROVEN, re-verified here

Re-parsed directly from `KNGND110+sblpatch+k.bin` (2 009 856 B, 21 members):

```
[ustar member FWHEADER.bin          64 B]
[ustar members PROC0..PROC15.bin        ]
[ustar member FCC.bin                   ]
[ustar member StringTable.csv.gz        ]
[ustar member SECURITY.bin        1 600 B]
[ustar member SBLPATCH.bin              ]   ; KNGND110 only
[ONE 512-byte zero block                ]   ; not the usual two
[256-byte RSA-2048 signature            ]
```

The walker consumes exactly one zero block (`0x7ffac444`: `movi a9,512`,
state ← 2), matching the file.

### 3.2 The two per-member fields — PROVEN, both re-verified on all 21 members

1. **Vendor CRC at ustar offset 508..511**, little-endian **CRC-32/MPEG-2**
   (poly `0x04C11DB7`, init `0xFFFFFFFF`, no reflection, no final XOR) over that
   member's data. Standard `tar` writes zeros there.

   The drive computes exactly this. **PROC0 `0x7ff81310` holds a complete
   256-entry non-reflected CRC-32 table** — regenerated from the polynomial and all
   256 entries match, and it is the **only** image of the eighteen that has it.
   The routine is PROC0 `0x7ffb1238`:

   ```
   7ffb1238: entry a1,0x20
   7ffb123b: { l32r a7,=0x7ff81310 ; j 0x7ffb1259 }
   7ffb1243: l8ui  a5,a3,0x0
   7ffb1246: xor   a5,a5,a6            ; a6 = crc >> 24
   7ffb1249: addx4 a5,a5,a7
   7ffb124c: l32i.n a5,a5,0x0          ; tbl[(crc>>24) ^ byte]
   7ffb124e: slli  a2,a2,8
   7ffb1251: { xor a2,a5,a2 ; <a3++> }
   7ffb125b: { extui a6,a2,24,8 ; bnei a4,-1,0x7ffb1243 }
   ```

   and the member coroutine at PROC0 `0x7ffabb0c` CRCs in 64-byte slices and
   compares against `ctx+0x284`, which is the header buffer (`ctx+0x88`) plus
   `0x1FC` = **offset 508**. Mismatch → StrId 3387 `failed (CRC mismatch)`.

2. **Standard POSIX tar header checksum at offset 148**, six octal digits. The
   parser is PROC0 `0x7ffb4a3c`: it saves the 8-byte field, writes eight spaces
   (`0x20202020` twice at `+0x94`/`+0x98`), sums **all 512 bytes** as unsigned,
   restores the field, and compares.

   **This checksum therefore covers bytes 508..511.** Verified host-side: excluding
   those four bytes makes the checksum fail on all 21 members, including them makes
   it pass on all 21. **So changing the vendor CRC obliges you to recompute the
   standard checksum too** — a detail easy to miss.

### 3.3 What is *not* checked — PROVEN, including one negative

| item | status |
|---|---|
| ustar magic `"ustar"` at offset 257 | **NOT checked.** The literal string `ustar` appears in none of the 18 flat images, and the parser contains no such comparison. |
| octal size field (11 digits) | Checked. Parser `0x7ffb5eb8` rejects any byte outside `'0'`..`'7'`. |
| header checksum at 148 | Checked (above). |
| member names | Checked against a fixed table (below). |
| version / anti-rollback ordinal | **None exists.** The only identity check is the two-character OEM/branch comparison of §1.6. |
| total-length equality | None. The walker keeps a 64-bit running offset (`ctx+0x58/0x5c`) against a 64-bit limit (`ctx+0x60/0x64`) and aborts at `0x7ffac0ee` if it would overrun. |

### 3.4 Member names and the required-member mask — PROVEN

Classifier PROC0 `0x7ffab948`, name-pointer array at `0x7ff84400`:

| idx | name | idx | name |
|---|---|---|---|
| 0 | `FWHEADER.bin` | 6 | `BIST.bin` |
| 1 | `SBL.bin` | 7 | `SECURITY.bin` |
| 2 | `UEFI.bin` | 8 | `SBLPATCH.bin` |
| 3 | `StringTable.csv.gz` | 9 | `DCPATCH.bin` |
| 4 | `FCC.bin` | 10 | `DCVUCPATCH.bin` |
| 5 | `DriveConfig.bin` | | |

plus `PROC<n>.bin` → section `12 + n` (`n < 16`), `OM_SBL_<oem>.bin` → 1, anything
else → 28.

Required mask `0x0FFFF819` (`0x7ff836c8`), accumulated at `0x7ffac1af` and compared
for exact equality at `0x7ffac3e2`. Bits 0, 3, 4, 11 and 12–27:

> **required = `FWHEADER.bin` + `StringTable.csv.gz` + `FCC.bin` + all 16
> `PROC<n>.bin`.**

`SECURITY.bin`, `SBL.bin`, `UEFI.bin`, `SBLPATCH.bin`, `DriveConfig.bin` are
**optional** — which is exactly why `KNGND110` can carry `SBLPATCH.bin` and
`KNGND122` can omit it. Bit 11 has no name-table entry (`PROC<n>` starts at 12);
**SPECULATIVE** that it is a synthesised "signature present" section.

`FWHEADER.bin` itself is 64 bytes: 8 ASCII revision chars, a `u32` at `+8`
(`0` for `KNGND122`, `2` for `KNGND110+sblpatch+k`), a `u32` = 1 at `+12`, zeros.

### 3.5 The recompute recipe, and a proof that it round-trips

For an in-place, same-size byte patch to one member:

1. Patch the bytes inside the member payload.
2. Recompute the member's **CRC-32/MPEG-2** and store it little-endian at
   **ustar header offset 508**.
3. Recompute the **standard tar checksum** over the full 512-byte header with
   bytes 148..155 treated as spaces, and write it at 148 as `"%06o\0 "`.
4. Re-sign: **SHA-256 over the image range, RSA-2048 with WD's private key, result
   placed in the final 256 bytes.** *This is the step that cannot be done.*

Steps 1–3 were validated end to end in memory against the real container (patch a
byte, recompute the two fields, re-validate every member): **21/21 members pass both
checks afterwards and the file size is unchanged.** No file was written.

Nothing else needs touching:

- `PROC<n>.bin` and `FCC.bin` have **no internal checksum**. Layout is
  `".BIN"` + 12 zero bytes, then 0x10-byte `.SEG` records
  (`<4s I I I>` = magic, data offset, length, load address), terminated by a record
  with data offset `0xFFFFFFFF`. Byte accounting for `PROC0.bin` is exact:

  ```
  0x00000  ".BIN" + 12 zero bytes
  0x00010  .SEG data=0x000020 len=0x00004bb4 load=0x7ff80000
  0x04bd4  .SEG data=0x004be4 len=0x000001c4 load=0x7ffa0000
  0x04da8  .SEG data=0x004db8 len=0x000000c8 load=0x7ffa01d8
  0x04e80  .SEG data=0x004e90 len=0x00015b20 load=0x7ffa0400   <- the patch site
  0x1a9b0  .SEG data=0xffffffff len=0 load=0                   (terminator)
  0x1a9c0  = 108992 = EOF, exactly
  ```

  There is no spare field to hold a CRC.
- The tar member ordering, sizes and names are unchanged by an in-place patch.

**SPECULATIVE, worth noting, not worth relying on:** the firmware also carries
DDR-overlay integrity strings — `"ERROR, Check overlay %d text signature fail,
signature in DDR: %x"` (StrId 18/19) and `"Error!! The overlay text section-%d,
signature: 0x%X"` (StrId 21). These guard *overlays* loaded into DDR at runtime,
e.g. PROC8's `0x30000000` bank. A patch to a base image such as `PROC0.bin` does not
go near them, but **a patch to overlay-resident code would need whatever those
"signatures" are, and they were not decoded.** Prefer base-image patch sites.

---

## 4. The minimal defect-fixing patch

The defect, in one line: two single-bit tests at PROC0 `0x7ffaae35` / `0x7ffaae3d`
detect an armed CRASH or PFCRASH section and force startup marker `0x80000009`,
which maps to startup type 6 and hides the namespace. The section bits are sticky —
**nothing in any of the 18 images ever clears bit 0 or bit 2** — so once armed the
drive latches on every boot forever.

### 4.1 The chosen patch — ONE BYTE

Kill the *source* of the flag byte rather than the two consumers. Slot A of the
bundle at `0x7ffaae2d` is the `l8ui` that loads it; turn it into `movi a9,0`.

| | |
|---|---|
| virtual address | `0x7ffaae2e` (byte 1 of the 8-byte bundle at `0x7ffaae2d`) |
| original bundle | `9f 05 00 42 2c 02 00 04` |
| patched bundle | `9f a0 00 42 2c 02 00 04` |
| byte change | `0x05` → `0xA0` |
| offset in `PROC0.bin` | **`0xF8BE`** (bundle starts at `0xF8BD`) |
| offset in `flat/PROC0_7ff80000.bin` | `0x2AE2E` |

```
BEFORE 7ffaae2d: 9f 05 00 42 2c 02 00 04 { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }
AFTER  7ffaae2d: 9f a0 00 42 2c 02 00 04 { movi a9,0      ; beqi a12,4,0x7ffaae53 }
       7ffaae35: (unchanged)             { sync/extw ; ball a9,mask 0x1,0x7ffaaf02 }
       7ffaae3d: (unchanged)             { sync/extw ; ball a9,mask 0x4,0x7ffaaf02 }
```

**Why the encoding is safe.** Format-`0xF` bundle field map:

| bits | field | byte |
|---|---|---|
| 0–3 | format selector (`0xF`) | b0 low nibble |
| 4–7 | slot A `t` | b0 high nibble |
| 8–11 | slot A `s` | **b1 low nibble** |
| 12–15 | slot A `r` | **b1 high nibble** |
| 16–23 | slot A `imm8` | b2 |
| 24–27 | slot A `op0` | b3 low nibble |
| 28–31 | slot B `r` (mask index) | b3 high nibble |
| 32–35 | slot B `s` | b4 low nibble |
| 36–53 | slot B displacement | b4 high / b5 / b6 low 6 |
| 55–63 | slot B opcode `k` | b6 high / b7 |

`b1` is the only byte carrying slot A's `s` and `r` and nothing else. `0x05` →
`r=0` (`l8ui`), `s=5` (`a5`). `0xA0` → `r=0xA` (`movi`), `s=0` (immediate high
nibble). `op0` stays 2, `t` stays 9 (`a9`), `imm8` stays 0, the format nibble stays
`0xF`, and **slot B's entire encoding is byte-untouched**, so
`beqi a12,4,0x7ffaae53` still works. Format `0xF` has no slot C.

Not a guess: `movi` in slot A of a format-`0xF` bundle occurs **2373 times** across
the 18 images, and `0x7ffa0f31: 9f a0 00 b2 76 00 80 05 { movi a9,0 ; bgeu … }` is
a byte-for-byte precedent for those exact first two bytes.

`a9` is dead between `0x7ffaae2d` and its next definition at `0x7ffaae8c`
(`l32r a9`) on every reachable path, and every `call8` in between rotates the
register window. `a5` is untouched, so the struct pointer stays live for the later
`l32i a11,a5,0x8`. `l8ui` on SRAM has no side effect.

### 4.2 Alternatives considered and rejected

**(a) Neutralise the two `ball` branches.** Retarget each to its own fall-through
(`disp = 4`): `0x7ffaae35` `ff 20 00 00 99 0c 80 00` → `ff 20 00 00 49 00 80 00`
(offset `0xF8C5`), `0x7ffaae3d` `ff 20 00 20 19 0c 80 00` → `ff 20 00 20 49 00 80 00`
(offset `0xF8CD`). Derived twice independently and correct. Rejected only because
§4.1 is smaller, single-site, involves no branch arithmetic, and cannot let the two
tests drift out of sync. **Equally sound — use it if you prefer not to re-encode a
slot-A opcode.**

**(b) Un-gate the admin allow-list.** The gate is `PROC8 0x7ffa6b18`,
`0x7ffa6b30: { movi a13,198 ; bnei a8,6,0x7ffa6bd9 }` — one nibble. **Rejected:** it
does not fix the defect. The namespace is hidden by separate `== 6` / `== 0` tests
at `0x7ffa4069`, `0x7ffa98a6`, `0x7ffa99de`, `0x7ffa9aaa`, `0x7ffab4f9`,
`0x7ffac9aa`, `0x7ffae2f0`, `0x7ffb2518`, `0x7ffad933`, `0x7ffadb00`. Opening the
gate yields a live controller with no namespace. That is six-plus patches, not one.

**(c) Clear the crash section at boot.** **Rejected:** no clearing code exists in
any of the 18 images — the section-state machinery lives in the SBL, which is not in
the corpus. The only reachable clear is the OAM erase, and per
`docs/sn200-firmware-re.md` §13.3 the crash-erase arm schedules the destructive
re-init when the startup type is 6. Would require writing new code.

**(d) Redirect the forced marker to READ-ONLY startup — the interesting one.**
`0x7ffaaf08: b1 5b 61 = l32r a11,0x7ff83474` (`= 0x80000009`). Changing the middle
byte to `0x5c` retargets it at `0x7ff83478` (`= 0x80000008`), verified both ways
(`target = ((pc+3) & ~3) + ((imm - 0x10000) << 2)`; `0x615b` → `0x7ff83474`,
`0x615c` → `0x7ff83478`). One byte, `PROC0.bin` offset `0xF999`. Marker 8 reaches
`0x7ffaaff5: { movi a11,1272 "SYS: Read-only startup" ; j 0x7ffaac8a ; movi a5,3 }`,
i.e. **startup type 3**, on the identical common tail as a normal startup — only
`a5` differs. The admin gate only restricts type 6, so it would not engage.
The literal `0x7ff83478` is referenced only by the dispatcher compare at
`0x7ffaaed3`, so there is no aliasing.

**Status: attractive but NOT recommended.** `docs/sn200-firmware-re.md` §13.6
establishes that marker 8 has **no producer anywhere in the shipped firmware**, so
type 3 is a state the vendor has never exercised in this configuration. Two honest
qualifications in both directions:

- The argument that killed it in one pass — an assert at `PROC8 0x7ffac468`
  (`AdminMgr: Unexpected startup state 0x%08x for resize 0x%08x` → `break.n`, with
  only `{1,2}` accepted) — **is overstated, and I checked.** That block is entered
  only when `[0x7ff89674] != 0` (`0x7ffac444: beqz.n a12,0x7ffac470`), i.e. only
  when a drive resize is pending; the word is zero in the image. The decisive
  argument is simpler: **a latched drive is in type 6, which is also not in
  `{1,2}`, and latched drives do not assert.** So that path is not on the
  unconditional startup route.
- Nonetheless, SAM (`PROC6 0x7ffba940: beqi a12,3`) and BlockMgr
  (`PROC6 0x7ffa66e8: beqi a9,3`, StrId 2671 `"BlockMgr: Read Only Startup"`) *do*
  handle type 3, and type 3 is not type 0, so `Admin_NamespaceStartup` — the
  destructive rebuild — does **not** run. The mode looks genuinely implemented.

If §4.1 turns out not to fix a given drive, (d) is the next thing to study. It is
not the thing to flash first.

### 4.3 Side effects — brutally honest

**The patch does not skip a metadata rebuild.** PROVEN: the Post-Crash arm performs
no rebuild. Type 6 is a *lockout* — nothing on that path touches the L2P/LBN tables
(`docs/sn200-firmware-re.md` §13.7(a)) — and the only rebuild in the firmware
(`Admin_LbnTransTblInit` `0x7ffad2f0`, `Admin_NamespaceStartup` `0x7ffad364`) runs
**only** on startup type 0 and is destructive. Suppressing the latch removes a
*lock*, not a *repair*.

Three real risks:

**Risk 1 — it may not be sufficient, and this is the most important caveat.**
Markers 5/6/7 reach Post-Crash by a *different road* that the patch does not touch:

```
7ffaaf6b: l32r a15,[0x7ff826b8] ; l32i.n a15,a15,0x4
7ffaaf70: { sync/extw ; bnei a15,4,0x7ffaacea }   ; not LOAD_N_GO -> 0x7ffaacea
7ffaacea..7ffaacfe: log the marker name from the u16 table at 0x7ff81180
7ffaad01: <falls through into the UNEXSTRT / Post-Crash arm>
```

`0x7ffaacea` has exactly one predecessor (full-image branch-target scan:
`0x7ffaaf70` only) and no terminator before `0x7ffaad01`. **So a drive whose
persisted marker is 5/6/7 latches without either `ball` firing.** And markers 5/6/7
— "Shutdown started but never finished", "PFAIL started but never finished",
"PFAIL started but timed out" — are precisely the leading field hypothesis in
`docs/sn200-field-evidence.md`.

> **This corrects `docs/sn200-firmware-re.md` §13.8**, which states markers 5/6/7 →
> `0x7ffaaf6b` → startup type 0. That holds only for boot mode 4 (LOAD_N_GO).

**Risk 2 — it can unmask a marker-3 write and convert "latched, data intact" into
"REINIT, data destroyed".** Three earlier blocks in the same function write marker 3
and are *currently overwritten* by the forced 9:

| site | StrId | string |
|---|---|---|
| `0x7ffaadb0` | 3040 | `SYS: Found an incompatible SA` |
| `0x7ffaae64` | 1263 | `SYS: Detected CellCare mismatch` |
| `0x7ffaaef7` | 3041 | `SYS: Detected an erased SysArea.` |

With the patch, marker 3 survives → startup type 0 → `Admin_LbnTransTblInit` →
**the drive wipes itself on the next boot.** Today the latch is accidentally
protecting you from that.

**Risk 3 — the diagnostic interlock is gone for good.** The drive will no longer
refuse to mount while a crash/PFail dump is armed. That interlock exists so a field
engineer can retrieve the dump before it is overwritten. **Pull the crash dump
first** — `docs/sn200-crash-dump-retrieval.md`; that whole path uses `0xC6`/cmd
`0x20`, which is allow-listed in Post-Crash mode.

Deliberately left intact: `0x7ffaae50 → 0x7ffaaf08` (empty System Area still
latches). If the SA is genuinely gone, latching is correct.

### 4.4 The mandatory, free, read-only pre-flight

Before flashing anything, pull the drive log (`0xE6`, or `0xC6` with cmd byte
`0x20` — both survive the latch) and read the boot lines:

| what you see | meaning | action |
|---|---|---|
| StrId 3042 `SYS: Detected a CRASH or PFCRASH section.` and **no** marker name | latch via the `ball` tests | the patch would work |
| a marker name: `Shutdown started but never finished` / `PFAIL started but never finished` / `PFAIL started but timed out` | latch via `0x7ffaaf6b → 0x7ffaacea` (Risk 1) | the patch changes nothing — do not flash |
| StrId 3040 / 3041 / 1263 present | Risk 2 is live | **do not flash** — the patch would trigger a wipe |

There is no safe second patch for the 5/6/7 road: forcing `0x7ffaaf70`'s branch
not-taken sends those markers to startup type 0, which is first-time startup, which
is the wipe.

---

## 5. Brick risk

### 5.1 Slot 1 is read-only and no commit action can write it — PROVEN

```
30025ebe: { s32i a15,a1,0x24 ; bnei a12,1,0x30025ed6 }   ; a12 = FS = CDW10 & 7
30025ec6: LOG 2187 "Firmware Activate Invalid Slot"      ; FS == 1 -> refused
```

This is reached **after** the image validates and **before** any write, on both
CA=0 and CA=1. CA=2 (activate an existing image) is unaffected, so **slot 1 remains
activatable — just not writable.** The factory fallback survives everything in this
document.

Note the neighbouring check at `0x30025ed6` is the **port lock**, not a
compatibility check:

```
30025ed6: l32i  a12,a5,0x4c        ; caller context
30025ed9: l32i.n a11,a7,0x14       ; port this download is locked to
30025edb: extui a12,a12,8,1        ; caller port, 1 bit
30025ede: beq   a12,a11,0x30025c26 ; same port -> proceed
30025ee6: LOG 2970 "Firmware Commit called from wrong port. Locked To: %x, Caller: %x"
30025eec: status 0xC2260000        ; SCT=1 SC=0x13 Firmware Activation Prohibited
```

### 5.2 A bad image cannot reach a slot — PROVEN

The Check verdict is latched, and commit re-tests it:

```
30025eb1: l32i.n a11,a7,0x0
30025eb3: beqz  a11,0x30025ef7     ; no image at all -> LOG 2189 "Invalid Image"
30025eb6: l32i.n a13,a7,0x8
30025eb8: beqi  a13,1,0x30025ef7   ; image marked BAD  -> LOG 2189 "Invalid Image"
```

`[+0x08]` is set to 1 both when the host-to-DDR transfer fails (`0x300255d2`) and
when Check Image fails (`0x30025b98`). **A download that fails Check can never be
committed.** The host sees a clean error, not a bad write. This is what makes the
whole experiment safe to *attempt*: an unsigned image is rejected before Write.

### 5.3 There IS automatic boot fallback — PROVEN mechanism, INFERRED loop

This is new, and it materially lowers the risk of the entire firmware area.

**(a)** The handoff routine PROC0 `0x7ffb401c` writes *both* a "slot to load" and a
"slot requested" field to the same value:

```
7ffb402e: l32r a2,=0x7ff9ff60             ; BootInfo
7ffb4059: l32r a9,=0x7ff97974             ; slot-order table
7ffb405e: s8i  a11,a2,0x12                ; boot mode = 3
7ffb4061: l8ui a9,a9,0x4                  ; slot_order[0]
7ffb4064: s8i  a9,a2,0x10                 ; slot to LOAD
7ffb406d: s8i  a9,a2,0x11                 ; slot REQUESTED
7ffb40de: { s8i a10,a2,0x12 ; movi a9,255 }   ; unknown-slot path -> both := 0xFF
```

**(b)** The post-boot banner PROC0 `0x7ffb478c` reads them back and compares:

```
7ffb49b0: LOG 83 "Firmware Boot Mode   : COLD BOOT, EEPROM (Slot %d)"   ; = BootInfo+0x10
7ffb49b9: l8ui a11,a2,0x11     ; requested
7ffb49bf: beq  a11,0xFF,0x7ffb486e   ; LOG 85 "Default firmware slot is unknown"
7ffb49c7: l8ui a12,a2,0x10     ; actually loaded
7ffb49ca: beq  a12,a11,0x7ffb4874    ; equal -> nothing to report
7ffb49d2: LOG 84 "Error recovery       : Failed to load firmware from Section %d"
```

Since (a) sets them equal, **the only way they can differ at (b) is if something
between reset and firmware start substituted a different slot.** That something is
the SBL, and StrId 84 is literally named *"Error recovery"*.

**(c)** The preference list is a **5-entry ordered table** at `0x7ff97974`, printed
by `SYS: Firmware slot order %d, %d, %d, %d, %d` (StrId 1138, PROC0 `0x7ffa35ef`),
of which the handoff uses only element 0. The remaining four exist for one purpose.

**(d)** Every slot is independently CRC-protected in EEPROM — `EEPROM: Bad CRC
(%08X!=%08X)` (StrId 1242) at PROC0 `0x7ffa4d2b` and `0x7ffa5062` — so the SBL has a
cheap local way to reject a bad slot without parsing it.

**(e)** A runtime pre-flight exists too: PROC0 `0x7ffa3f4e` reads back the slot it
intends to warm-boot from and logs `SYS: Preload reading good firmware image`
(1122) or `SYS: Preload reading bad firmware image (result = %d)` (1123) **before**
the reset.

**Answer to the question as posed:** a bad image committed to a slot and then
activated does **not** require a manual `fw-commit --slot=1 --action=2`. The SBL
walks the persisted order list, checks each candidate's EEPROM CRC, boots the first
good one, and records the substitution, which the running firmware prints as
`Error recovery: Failed to load firmware from Section %d`. **PROVEN** that the
handoff struct carries requested-vs-loaded and that divergence is an expected,
logged, recovered condition. **INFERRED (high)** that the walk covers all five
entries in order — the SBL binary is not in the corpus.

Corollaries for the runbook:

1. After any activation, read the **debug log**, not just `nvme fw-log`'s `afi`.
   `Firmware Boot Mode : COLD BOOT, EEPROM (Slot N)` says which slot actually
   booted; StrId 84 says whether a fallback happened.
2. The one state with no automatic recovery is `Default firmware slot is unknown`
   (StrId 85, requested `== 0xFF`), reached when the SLOT section itself is
   unreadable. Filling slots 2–5 with CA=0 never touches the SLOT section.

### 5.4 Residual risk of *attempting* an unsigned image

Low, and bounded:

- The image fails Check → `[+0x08] = 1` → commit returns "Invalid Image". No write.
- CA=0 never alters the active or next-active slot.
- Slot 1 is refused outright.
- No reset is required or should be performed.

The realistic worst case of the *attempt* is a rejected command. The realistic worst
case of a *successful but wrong* patch is Risk 2 of §4.3 — a wipe — which is why the
§4.4 pre-flight is mandatory rather than advisory.

---

## 6. Verdict, and confidence

**Would a patched image load, or brick?**

**Neither. It would be cleanly refused.** Confidence that a modified image is
rejected: **~95%.**

The remaining doubt is one specific, honest gap: PROC0's dispatch at `0x7ffac224`
has not been shown by identifier to target CryptoMgr command 0 (§1.7). If that
request turned out to reach some *other* service, and the RSA path were driven only
by a different consumer — a secure-boot check of overlays, a TCG flow, manufacturing
— then the firmware-image path might reduce to CRC-only. Everything else points the
other way, and the verdict plumbing is now traced end to end:

- the crypto is present, complete, self-tested at boot, and three RSA-2048 keys are
  compiled into the running firmware and cached in EEPROM;
- the result word is preset to **fail** unconditionally and cleared only by a
  32-byte digest match, then copied verbatim into the reply PROC0 reads (§1.2, §1.7);
- the image processor has a dedicated `failed (invalid digital signature)` status
  and a real store to it, guarded by that reply (§1.4);
- the commit handler complains about being unable to retrieve a "TAR Vector and FW
  **Public Key**";
- the trailer is exactly 256 bytes — the modulus size — high-entropy, per-revision,
  and is not any plain digest of the file;
- **all three** candidate bypasses — PSID state, security level, and the
  `beqz a9` at `0x7ffb0c8a` — were chased to the instruction and are provably not
  bypasses. The security level only chooses which keys are eligible; level 0
  *widens* the set rather than skipping the check.

**Confidence that the one-byte patch is the correct fix, *given* a signed image:
high (~85%)** — the encoding is proven by 2373 in-corpus precedents and a
byte-exact one, the register is dead, and the failure modes are enumerated. The 15%
is Risk 1 of §4.3 (a drive latched via markers 5/6/7 would be unaffected), which the
read-only pre-flight in §4.4 resolves per drive at zero cost.

**Recommended next steps, in order, none requiring hardware writes:**

1. Run the §4.4 log pre-flight on the latched drive. It is read-only, it uses only
   allow-listed opcodes, and it tells you whether the patch is even the right shape
   for these drives. **This is the only step with any expected value.**
2. Close §1.7 by resolving the cross-processor message service-ID table — the one
   question between "high confidence" and "proven". Pure desk work.
3. Only if (2) somehow showed the signature is *not* on the image path would §3.5
   become relevant. Do not build a patched container on the current evidence.

**Do not attempt to defeat the signature.** There is no weakness here to exploit:
the keys are public keys, the private key is WD's, and the check is a standard
RSA-2048/SHA-256 verify with a proper padding walk. The realistic paths to a fixed
drive are the operational ones already documented, not a forged image.

---

## 7. What is still unknown

- **The IPC service-ID mapping** from PROC0's dispatch at `0x7ffac224` to CryptoMgr
  command 0. The single most valuable open question. The sender writes command `0`
  to `msg+0x8`, an address to `msg+0xc`, a length to `msg+0x10`; the reply is
  12 bytes carrying the verdict from crypto context `+0x3c`.
- **Whether `a7` is reloaded to 0** before the success store at `0x7ffb0d46`
  (§1.7 loose end). Resume block `0x7ffb0c42`–`0x7ffb0c78` defeats the decoder.
  Both readings make the check stricter, so this is tidiness, not risk.
- **`[a2+0x2c]`'s provenance** (the security level). The only writer is
  `0x7ffb0974`, reading a byte from a table at `0x7ff80900` indexed by `[a2+0x34]`
  — but `0x7ffb0973`/`74`/`75` overlap (`ba b9 b2 0b 00` frames two ways) and the
  framing was not settled. It is definitely **not** `FWHEADER.bin+12`, and is
  distinct from the image descriptor's security byte at `0x7ff83580+0xb0`.
- **Whether the optional patch members carry independent, weaker checks.**
  `SBLPATCH.bin` / `DCPATCH.bin` / `DCVUCPATCH.bin` (classifier indices 8/9/10) are
  handled by "Process special modules" (`PROC0 0x7ffabbf0`, TAR cmd 2, reached from
  `0x7ffac4ba`). If CryptoMgr's single contiguous range spans the whole staged image
  they are inside the digest — but that is the same unresolved IPC hop.
  **Not examined. Absence of a finding here is not a finding.**
- **Where `total_length - 256` is computed.** No such arithmetic on the FW path in
  PROC0 or PROC9. The crypto object's length arrives over IPC from an unidentified
  producer.
- **The writer of the running image descriptor's security-level byte**
  (`0x7ff83580 + 0xb0`). `0x7ff83580` is BSS and most of its references sit inside
  FLIX bundles the scanner cannot attribute.
- **Required-mask bit 11.** No name-table entry maps to it.
- **The SBL.** Not in `~/sn200fw/flat/` — only `SBL.bin` / `OM_SBL_*.bin` as tar
  members. The slot-fallback *loop* of §5.3 is therefore inferred from its
  observable effects, not disassembled. The crash-section clearing code is almost
  certainly in there too.
- **Blocks 3–5 of `SECURITY.bin`** (768 bytes). Not used by `getPublicKey`.
- **The overlay "text/ROdata signature" mechanism** (StrIds 18/19/21). Only matters
  if someone patches overlay-resident code.
- **Whether the EEPROM key cache is compared byte-for-byte against the compiled-in
  copy.** The key-load coroutine's resume blocks are heavily FLIX-bundled and
  `disany.py` mis-decodes several.

---

## 8. Corrections to existing documents

`docs/sn200-firmware-flashing.md`:

1. §2 — the 256-byte trailer's algorithm is no longer speculative. It is
   **RSA-2048 over SHA-256**, verified in PROC9 at `0x7ffb0d63`, keys at
   `0x7ff820c0`.
2. §2 — `SECURITY.bin` is the **public-key blob**, byte-identical to a copy
   compiled into PROC9 at `0x7ff823c0`, which is why it never changes between
   revisions. It is also **not a required member**.
3. §3 — the check at `0x30025ed6` is the **port lock**, not a
   compatibility/branch check. The compatibility check is `PROC9 0x7ffb0a40` and
   compares revision characters [2] and [3].
4. §3 — "CA=0 writes without selecting" can be upgraded from INFERRED: the three
   phases are IPC round-trips at `0x30025b92` / `0x30025b07` / `0x30025aa5`, and
   the commit handler body starts near `0x30025a20`, not `0x30025c20`.
5. §4 Step 0 — a better host-side pre-flight than `tar -tf | grep -c SBLPATCH` is
   the drive's own required set: `FWHEADER.bin` + `StringTable.csv.gz` +
   `FCC.bin` + `PROC0..15.bin` (mask `0x0FFFF819`).
6. Add §5.3 of this document (boot fallback) — it lowers the risk profile of the
   whole procedure.

`docs/sn200-firmware-re.md`:

7. §13.8 — markers 5/6/7 funnel to startup type 0 **only in boot mode 4**. On a
   normal cold boot `0x7ffaaf70`'s `bnei a15,4` sends them to `0x7ffaacea`, which
   falls through into the Post-Crash arm at `0x7ffaad01`. See §4.3 Risk 1.
8. §13.6 — the read-only-startup option is still open, but the assert at
   `PROC8 0x7ffac468` sometimes cited against it is gated on a pending resize
   (`0x7ffac444: beqz.n a12,0x7ffac470`, and `*(0x7ff89674) == 0` in the image), so
   it is not on the unconditional startup path. See §4.2(d).
