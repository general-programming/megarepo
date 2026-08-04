# `Admin_VucFlashRead` — the read-by-LBA VUC, and why it cannot save a latched drive

**Verdict up front: clean negative.** `Admin_VucFlashRead` is exactly the
primitive the recovery effort hoped for — read one LBA of *user data* through
the drive's own translation, no state change, no media write. It is **opcode
`0xCA`, `CDW12 = 0x0001`**. And `0x01` is **not** in the twelve-entry
Post-Crash allow-list, so a latched drive rejects it with `0x7C5` before the
handler is ever entered. Its companion `Admin_VucFlashLogicalToPhysical`
(`0xCA`, `CDW12 = 0x0000`) is excluded from the same list.

This is not an oversight in the allow-list. `0x00` and `0x01` are the **only
two** `0xCA` sub-values below `0x02`, and they are precisely the two that know
about namespaces and LBAs. Everything the allow-list *does* admit from the
flash family (`0x03` read, `0x0F` erase, `0x10` write, `0x08` V2P) is
**physical-address** diagnostics. WD let the raw-media tools through and kept
the logical-address tools out.

Labels: **PROVEN** = read off the instruction stream. **INFERRED** = follows
from proven facts plus a named assumption. **UNKNOWN** = not established.

---

## 1. The overlay mapping that makes all of this readable — PROVEN

`PROC8`'s DDR segment is `0x30022238`–`0x30040078`; its overlay window is
`0x7ffbc000`. The overlay descriptor table is at **`PROC8@7ff80000
0x7ff81ae4`**, 34 entries of `0x20` bytes:

```
+0x00 dst 0x7ff9f000   +0x04 len   +0x08 src (0x300002xx, per-overlay data)
+0x10 dst 0x7ffbc000   +0x14 len   +0x18 src (0x300xxxxx, the code)
```

So `static = src2 + (runtime − 0x7ffbc000)`.

Overlay numbering is **1-based against this table**: table entry *N* is
`OVL(N+1)`. Two independent confirmations:

- entry 30 → `src2 = 0x3003bcb8`. The `0xCA`/`0x10` handler runs at
  `0x7ffbd904` → `0x3003d5bc`, which is a function-map entry containing
  `VUC Flash_ProgNANDPageRaw MLC` (`0x3003d645`). `sn200-dangerous-commands.md`
  already called this "overlay 31". ✓
- entry 25 → `src2 = 0x30035378`. The `0xCA`/`0x03` handler runs at
  `0x7ffbdab0` → `0x30036e28`, a function-map entry, and the raw-page-read
  strings sit at `0x30037124`/`0x30037252`. That is `OVL026`, matching the
  `_OVL026` suffix on the string-table names in this range. ✓

> **Caveat, stated because it looks like corroboration and is not.** Each
> `0xCA` dispatch arm stores a small integer to `a12+0x20` that *looks* like an
> overlay id (26, 27, 28, 29, 30, 31, 32) and agrees with the arithmetic above
> for every `0xCA` arm. But the `0xC6` arm at `0x7ffa7c05` stores **200**,
> which is not an overlay index at all. So that field is **not** proven to be
> an overlay id and is **not** used as evidence anywhere below. Every
> runtime→static claim here rests on offset arithmetic plus a landing-site
> check, never on that byte.

**Landing-site check (this is what makes the base unambiguous).** A wrong base
lands on a non-instruction boundary. Under `OVL026 = 0x30035378`, every
coroutine self-resume literal inside the `Admin_VucFlashRead` handler lands on
an exact instruction start, and the first one lands on the exact return point:

| runtime literal | static | what is there |
|---|---|---|
| `0x7ffbd11c` | `0x30036494` | `entry a1,0x50` — the handler entry |
| `0x7ffbd14a` | `0x300364c2` | the instruction **immediately after** `call8 0x30028e44` at `0x300364bf` |
| `0x7ffbd1d6` | `0x3003654e` | `l32i a12,a2,0x130` |
| `0x7ffbd204` | `0x3003657c` | `l32i a8,a2,0x11c` |
| `0x7ffbd319` | `0x30036691` | `l32i a9,a2,0x48` |

---

## 2. The opcode — PROVEN

**`Admin_VucFlashRead` = NVMe admin opcode `0xCA`, `CDW12[7:0] = 0x01`,
`CDW12[15:8] = 0x00`.**

Chain of evidence, every address stated:

1. **Opcode `0xCA` reaches this dispatcher.** `PROC8@7ff80000 0x7ffa75cb`:
   `movi a8,201 / bgeu a8,a11 → 0x7ffa7bbd` (opcode ≤ 201 goes elsewhere) and
   `0x7ffa75d6`: `movi a9,202 / bltu a9,a11 → 0x7ffa7107` (opcode > 202 goes
   elsewhere). Only `a11 == 202 == 0xCA` falls through.
2. **The index is `CDW12[7:0]`.** `0x7ffa75e1`: `l32i.n a12,a1,0x24` /
   `0x7ffa75e3`: `l8ui a12,a12,0x38` — the same byte the gate reads.
   `0x7ffa75f6`: `bgeu a12,67 → 0x7ffa78e3` bounds it. `0x7ffa7601`:
   `addx2 a8,a12,a12` (×3) + `l32r a9,→0x7ffa760e`, so **the jump table base is
   `0x7ffa760e`, three bytes per entry**.
3. **Entry 1.** `0x7ffa760e + 3×1 = 0x7ffa7611`: `j 0x7ffa78d6`.
   At `0x7ffa78d6`: `l32r a13,0x7ffa0ed8` → **`0x7ffbd11c`**.
4. **`0x7ffbd11c` → `0x30036494`** by §1, and `0x30036494` is the function
   whose body carries the whole `VUC_FlashRead` / `Admin_VucFlashRead` string
   set (`0x300364b3`, `0x3003660e`, `0x30036a7a`, `0x30036ad3`).
5. **Exactly one dispatch site.** `litref.py -v 7ffbd11c` returns a single
   hit, `PROC8_7ff80000 0x7ffa78d8`. There is no second door.

**Sub-byte.** Handler entry `0x30036494`:

```asm
30036494: entry  a1,0x50
30036497: addmi  a10,a2,256          ; a2+0x100 = the parsed-command struct
3003649a: l8ui   a10,a10,0x39        ; = CDW12[15:8]
300364a8: { l32i a5,a2,0x174 ; beqz a10,0x30036515 }   ; sub 0 -> the real path
300364b0: bnei   a10,1,0x300364b9
300364b3: l32r   a10,-> StrId 1855 "flash read without ECC is not supported."
```

`CDW12[15:8] = 0` is the only value that proceeds. `1` logs "not supported";
`≥2` falls into the same error tail (`0x300364b9` sets
`status |= 0xc0040000`). **PROVEN.**

### CDW layout — PROVEN

| field | ctx offset | read at | meaning |
|---|---|---|---|
| NSID | `+0x1c` (`a2+0x11c`) | `0x30036ab2` | rejected if `0` or `> 128` (`beqz a11` / `bltu a8,a11` with `a8=128`) |
| `CDW10` | `+0x30` (`a2+0x130`) | `0x3003654e`, `0x30036751` | transfer length **in dwords**; `slli ...,2` → bytes; must match the selected format **exactly** |
| `CDW12` | `+0x38/+0x39` | dispatcher / `0x3003649a` | `0x0001` |
| `CDW14` | `+0x40` (`a2+0x140`) | `0x30036ac0` → scratch `a2+0x18c` | the **LBA** |
| `CDW15` | `+0x44` (`a2+0x144`) | `0x30036abd` → scratch `a2+0x188` | the **Data Format**, tested at `0x300366e0`: `0` = one frame, `1` = one LBA, `2` = one LBA + metadata; anything else logs StrId 1860 and fails |

One LBA (or one frame) per command. There is no multi-LBA form.

---

## 3. What it actually reads — user data via the L2P — PROVEN

Not the SPI EEPROM, not raw physical NAND. It is a **logical** read.

- NSID is validated against the namespace count and used to index a
  per-namespace geometry table: `0x3003657c`: `l32i a8,a2,0x11c` /
  `addx2 a8,a8,a8` / `addx8 a8,a8,a11` / `l8ui a8,a8,0x7e` — the LBA-size shift
  for that namespace.
- The LBA from `CDW14` is shifted by that value and added to a namespace base
  (`0x30036588`–`0x3003659e`) to form a drive-wide logical address.
- That goes through the same translation family the neighbouring commands
  expose by name: `Admin_VucFlashLogicalToPhysical` (`0x30035680`) and
  `Admin_VucFlashVirtualToPhysical` (`0x30035968`), yielding a
  `FlashLocation_t` — logged verbatim at `0x3003660e` (SLC) and `0x30036a7a`
  (MLC) as `VUC_FlashRead SLC/MLC, FlashLocation_t:0x%08x, Blockset:0x%x`.
- The media read itself is `call8 0x3002f7f4` (SLC, `0x3003663b`) or
  `call8 0x3002fa40` (MLC, `0x30036aa7`), whose failure logs
  `ERROR Flash_ReadFrames returned status 0x%x` (`0x30036648`).

So **it needs a working L2P**, which is exactly the thing a latched drive still
has and a re-initialised drive no longer does. That is what made it the right
lead. It is also why it is useless on a *healthy* drive — if the L2P is live
and the namespace is presented, an ordinary NVMe Read does the same job a
million times faster.

Its whole value was the middle case: L2P intact, namespace not presented. And
that case is exactly the one where the command is refused.

### Read-only? — INFERRED, high confidence (and moot)

> **⚠ The call-target addresses below are in the wrong address space.** Overlay
> code is linked for its *execution* address `0x7ffbc000`, so `callN`
> displacements must be resolved as
> `runtime_target = static_target + (0x7ffbc000 − overlay_src2)`. Under that
> rule 0/174 static targets in overlay 22 are function entries and 63/174
> runtime ones are; and `0x30030aa0` is **not** a "flash erase primitive"
> (runtime `0x7ffb9768`, an OAM-worker enqueue) while `0x30031d10` is **not** an
> "EEPROM primitive" (runtime `0x7ffba9d8`, `memset`). Static addresses are also
> not stable identities *across* overlays — the same word means a different
> function from each. The read-only conclusion here is probably still right, but
> the argument needs redoing. See `sn200-oam-dispatch.md` §1.1.

Every call target inside the handler extent `0x30036494`–`0x30036b41`:
`0x3002d920` (log), `0x30028e00`/`0x30028e44` (coroutine yield),
`0x300339ec`/`0x30033a10`/`0x30033a34`/`0x30032ae0` (status/completion
helpers), `0x3002d41c`/`0x3002d44c`/`0x3002d644`/`0x3001fa94` (helpers),
`0x3002f7f4`/`0x3002fa40` (flash read). **No call to the flash-erase primitive
`0x30030aa0`, the EEPROM primitive `0x30031d10`, or any program path.** The
only sub-word stores are `s8i` to `a2+0x48..0x4b` at `0x30036678`–`0x30036689`,
writing `0xff`/`0xffff`/`0xfff` sentinels into the command object, not to
media. Marked INFERRED rather than PROVEN because the helper callees were not
themselves traced to leaves.

---

## 4. The gate — PROVEN, and this is the decisive result

`Admin_CheckCmdAllowed` is `PROC8@7ff80000 0x7ffa6b18`. It runs at
`0x7ffa7244`, **before** the dispatcher at `0x7ffa75cb`.

```asm
7ffa6b1b: l32r  a8,0x7ffa09b0        ; -> 0x7ff87c64, the startup-mode word
7ffa6b30: { movi a13,198 ; bnei a8,6,0x7ffa6bd9 }   ; mode != 6 -> not gated
7ffa6bb3: movi  a9,202                              ; 0xCA
7ffa6bb6: { extw ; beq a3,a9,0x7ffa6d76 }           ; -> the sub-list
```

Re-read instruction by instruction at `0x7ffa6d76`, not taken on trust:

```asm
7ffa6d76: beqi a4,8, ok          ; 0x08
7ffa6d79: beq  a4,a10,ok         ; a10 = 17  (0x11)   [movi a10,17 @0x7ffa6b28]
7ffa6d7c: beqi a4,3, ok          ; 0x03
7ffa6d7f: movi a8,15
7ffa6d81: beq  a4,a8, ok         ; 0x0F
7ffa6d84: beqi a4,16,ok          ; 0x10
7ffa6d87: beqi a4,2, ok          ; 0x02
7ffa6d8a: beqi a4,4, ok          ; 0x04
7ffa6d8d: beq  a4,a11,ok         ; a11 = 13  (0x0D)   [movi a11,13 @0x7ffa6b28]
7ffa6d90: movi a9,14
7ffa6d92: beq  a4,a9, ok         ; 0x0E
7ffa6d95: movi a14,19
7ffa6d97: beq  a4,a14,ok         ; 0x13
7ffa6d9a: movi a8,33
7ffa6d9c: beq  a4,a8, ok         ; 0x21
7ffa6d9f: movi a9,50
7ffa6da1: { extw ; bne a4,a9,0x7ffa6d03 }   ; 0x32, else REJECT
```

Twelve values: `{0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13,
0x21, 0x32}`.

**`0x01` is absent. `0x00` is absent.** `a4` is `ctx+0x38` = `CDW12[7:0]`, set
up at the single call site `0x7ffa7231`: `l8ui a12,a13,0x38`. Rejection returns
`0x8F8A0000` → `0x7C5` on the wire.

> **PROVEN: on a latched SN200 (`*(0x7ff87c64) == 6`), `0xCA` with
> `CDW12 = 0x0001` (`Admin_VucFlashRead`) and `CDW12 = 0x0000`
> (`Admin_VucFlashLogicalToPhysical`) are both rejected `0x7C5` before the
> handler runs. Neither can read a byte of user data off a latched drive.**

The prior CTF pass audited the 16/17 allow-listed *opcodes*. This command is
not on that list at all — it is a sub-value of `0xCA` that the list excludes.

### Encoding distance to the destructive commands — read this before typing

`Admin_VucFlashRead` is `CDW12[7:0] = 0x01`. The raw NAND block erase is
`0x0F`; the raw NAND page write is `0x10`. Those are **14 and 15 away in
decimal**, not one nibble — but that is not the reassurance it sounds like,
because the read you would actually be tempted to substitute is the
*allow-listed* raw page read at `0x03`, and `0x03 → 0x0F` **is a single hex
digit**, as is `0x03 → 0x13`. The safe/lethal spacing in this family is one
keystroke wherever it matters. `CDW13` carries the physical flash address for
the entire `0x03`/`0x0F`/`0x10` family, so a mistyped command byte erases *the
block you were about to read*.

---

## 5. Full `0xCA` sub-value map established here — PROVEN unless noted

Jump table `0x7ffa760e`, 3 bytes/entry, index = `CDW12[7:0]`.

| `CDW12[7:0]` | runtime | static | identity | allow-listed while latched |
|---|---|---|---|---|
| `0x00` | `0x7ffbc308` | `0x30035680` | `Admin_VucFlashLogicalToPhysical` | **no** |
| `0x01` | `0x7ffbd11c` | `0x30036494` | **`Admin_VucFlashRead`** (read one LBA, L2P) | **no** |
| `0x02` | `0x7ffbc2a8` | OVL028 | unidentified | yes |
| `0x03` | `0x7ffbdab0` | `0x30036e28` | raw physical page read (`Flash_ReadRawData` / `Flash_ReadCacheData`), 640-byte clamp at `0x30037039` (`movi a11,640 / minu a10,a10,a11`) | yes |
| `0x04` | `0x7ffbccec` | OVL029 | unidentified | yes |
| `0x08` | `0x7ffbc5f0` | `0x30035968` | `Admin_VucFlashVirtualToPhysical` — pure `remu`/`quou`/`mull` over the geometry tables at `0x7ff821b0`/`0x7ff82110`, result to `ctx+0x54`, **no media access at all** | yes |
| `0x0D` | `0x7ffbcb48` | OVL028 | flash UID / lot-ID family | yes |
| `0x0E` | `0x7ffbcd1c` | OVL028 | `Admin_VucFlashReadStatus` | yes |
| `0x0F` | `0x7ffbdf28` | `0x3003dbe0` | ☠ raw NAND **block erase** | yes |
| `0x10` | `0x7ffbd904` | `0x3003d5bc` | ☠ raw NAND **page write / program** | yes |
| `0x11` | `0x7ffbc670` | `0x300359e8` | 26-byte stub | yes |
| `0x12` | `0x7ffbd108` | OVL030 | unidentified | no |
| `0x13` | `0x7ffbce34` | OVL028 | flash reset / status family | yes |
| `0x21` | `0x7ffbcaa8` | OVL030 | unidentified | yes |

`0x14`–`0x1F` and `0x22`–`0x2F` all jump to the invalid-command tail
`0x7ffa78e3`.

**Correction to `sn200-command-reference.md`:** the row headed "`0xC6` raw NAND
page read" was mislabelled. The raw page read is **`0xCA`**, cmd `0x03`, subs
`0`/`1`/`2` — the firmware's own log strings say so verbatim
(`ERROR Flash_ReadRawData(0xCA/0x03/0x01)` at `0x30037124`,
`Flash_ReadCacheData(0xCA/0x03/0x02)` at `0x300370ef`), and `0xC6` does not use
this table at all: opcode 198 dispatches at `0x7ffa7bf5` to a single handler at
`0x7ffbea44`. The row's own claim "reachable while latched because `0x03` is
allow-listed" only makes sense for `0xCA` — the gate admits `0xC6` **only** with
cmd byte `0x20` or `0x30` (`0x7ffa6bc1`–`0x7ffa6bc9`).

---

## 6. What is left, honestly

The only user-data read reachable on a latched drive is **`0xCA`/`0x03`, the
raw physical page read**. Assess it without optimism:

- **640 bytes per command** (`0x30037039`). 7.68 TB of user capacity, before
  over-provisioning, is ≈ 1.2 × 10¹⁰ commands. Even at an implausible 100 µs
  per admin VUC and perfect pipelining that is **~14 days of continuous
  issue**; realistically these coroutine-backed VUCs serialise and it is
  months.
- **It is a latched drive.** It fires an AEN every ~5 s and the host resets the
  controller in response. A harvest measured in weeks cannot survive a reset
  storm measured in seconds. This alone ends it.
- **You get physical pages, not LBAs.** `0xCA`/`0x00` — the one command that
  would tell you which physical address holds a given LBA — is the *other*
  command the allow-list excludes. Reconstructing the L2P from page metadata
  means reversing WD's undocumented spare-area format across the whole device,
  and then unwinding whatever compression/scrambling/XOR-parity the data path
  applies. Nothing in this teardown has established those formats.

**SPECULATIVE and not recommended:** `0x02`, `0x04`, `0x21` are allow-listed,
carry no log strings, and are unidentified. It is *conceivable* one is a bulk
read. It is equally conceivable one writes. They sit in the same opcode family
as a block erase; do not probe them on the live drive.

**The route out is unchanged.** Boot marker `8` (`READ ONLY`) still passes the
gate ungated with the L2P intact, still has no firmware writer, and still needs
the `DiagMgr` UART whose pinout does not exist in the firmware. See
`sn200-logic-escapes.md` §6 and `sn200-firmware-flow.md` §6.

### Answering the three questions this was raised under

- **Prevention.** Unchanged and already documented: stop deallocates/TRIM →
  `sync` + unmount → `CC.SHN` and *wait* → only then cut power. This is the
  only lever that works, and it is not absolute (`sn200-firmware-flow.md` §3
  defects 2 and 3 need no power event at all).
- **Mitigation without data loss.** Nothing in the NVMe command surface can do
  it. `Admin_VucFlashRead` was the last plausible candidate and it is gated
  off. The remaining candidates are all off-protocol: the UART console route,
  or a hardware data-recovery lab.
- **Patching around it.** Not via the host. Firmware images are RSA-2048
  signed with keys compiled into `PROC9`; the mode word `0x7ff87c64` has no
  writer reachable from any NVMe command; the allow-list is re-read per command
  and is not cached. A drive left powered off preserves every option — which
  remains the single most useful operational instruction in this whole
  document.
