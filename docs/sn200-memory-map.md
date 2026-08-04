# SN200 memory map — is per-core isolation real?

Firmware `KNGND122`. Static analysis only; no drive was touched.

**Verdict, up front: isolation is real.** The `0x7ff8xxxx`–`0x7ffbxxxx` range is
each core's *own* memory, self-aliased. It is not a globally routable name, so
no other core can address it — the four target words
(`0x7ff8c7ec`, `0x7ff85364`, `0x7ff85374`, `0x7ff87c64`) are unreachable from
any core but their owner. The assumption that three earlier conclusions rested
on holds, and the reasoning behind it is now structural rather than statistical.

The shared surface is real too, but it is *elsewhere*: a 512 KiB-granular grid
of SoC unit config pages in `0x80980000`–`0x82a80000`, a buffer SRAM around
`0x80020000`–`0x80240000`, PROC8's DDR window at `0x30000000`, and a separate
**64-bit** system address space that only DMA uses. None of them overlaps a
core's private RAM.

---

## 1. The unit grid — the fact everything else follows from

The SoC address map is a uniform grid of **512 KiB unit slots**. Each unit
exposes a configuration page at a fixed offset inside its slot; the registers
the firmware touches are at `+0x5fe00`, `+0x60000`, `+0x60004`, `+0x60200`,
`+0x60400`, `+0x60404`.

Every one of the 17 processor images loads the literal `+0x60200` for the *same
nine remote units*, and for its own slot. **PROVEN** by exhaustive `l32r`
literal sweep (plain and FLIX slot A) over all 18 flat images:

| unit slot base | `+0x60200` named by |
|---|---|
| `0x7ff80000` (self) | all 17 (`0x7ffe0200`) |
| `0x80980000` | 16 |
| `0x80a00000` | 16 |
| `0x81180000` | 16 |
| `0x81900000` | 16 |
| `0x82000000` | 16 |
| `0x82080000` | 16 |
| `0x82100000` | 16 |
| `0x82880000` | 16 |
| `0x82a80000` | 16 |
| `0x82a00000` | 16, but at `+0x5fe00`/`+0x60600`/`+0x60e00` |

The load-bearing observation is the **self** row. `0x7ffe0200` sits at offset
`0x60200` inside the 512 KiB slot beginning at `0x7ff80000` — the identical
offset the nine remote units use. So `0x7ff80000`–`0x7fffffff` is not a special
region: it is *one slot of the same grid*, and it is the slot the executing core
sees itself in.

That is what "core-local" means here, precisely: `0x7ff8xxxx` is a **self
alias**. It resolves to a different physical unit depending on who issues the
access. It cannot be handed to another core or to a DMA engine and mean anything.

Cross-check, PROC12 `0x7ffa32a0` (fatal path, entered at its `entry`):

```asm
7ffa3328: l32r a8,0x7ffa0998   ; = 0x7ffe0200   (self unit cfg page)
7ffa332b: l32r a15,0x7ffa099c  ; = 0x7ffefe00
7ffa332e: l32i a11,a8,0x204
7ffa3331: l32i a10,a8,0x200
...
7ffa3369: l32r a7,0x7ffa09a0   ; = 0x82a5fe00  (remote unit cfg page)
7ffa336c: l32i a2,a7,0x208
```

Same instruction shapes, same register-page semantics, self and remote side by
side, inside `rsil a9,15`. **PROVEN.**

---

## 2. What is private — PROVEN

Per-core slot layout, `0x7ff80000` + 512 KiB:

| range | size | contents |
|---|---|---|
| `0x7ff80000`–`0x7ff9ffff` | 128 KiB | **DRAM** — `.data` from the container, then BSS. Highest data address named anywhere is `0x7ff9ff60` (PROC0 crash staging buffer). |
| `0x7ffa0000`–`0x7ffbffff` | 128 KiB | **IRAM** — vectors at `0x7ffa0000`/`0x7ffa019c`/`0x7ffa01bc`/`0x7ffa01d8`, code from `0x7ffa0400` (PROC0) or `0x7ffa0710` (everyone else). |
| `0x7ffc0000`–`0x7fffffff` | 256 KiB | core-local config/peripheral space (`0x7ffe0200`, `0x7ffefe00`, `0x7fff0120`). |

The 128 KiB boundaries are not assumed — they are pinned by the images:

- Largest IRAM image is PROC6, ending `0x7ffbfc6c` — `0x394` bytes short of
  `0x7ffc0000`, and no image crosses it.
- PROC8's overlay execution window is `0x7ffbc000`, exactly `0x7ffc0000 − 0x4000`:
  the top 16 KiB of IRAM.
- No image's `.data` segment or referenced BSS address reaches `0x7ffa0000`.

### 2.1 Why these ranges cannot be one shared memory

The container gives every image the same load addresses. `segparse.py` over all
17 processors: each has a code segment at `0x7ffa0710` (PROC0 at `0x7ffa0400`),
running to somewhere in `0x7ffa97e4`–`0x7ffbfc6c`.

Byte-comparing the common region `0x7ffa0710`–`0x7ffa9000` (35056 bytes) against
PROC0:

```
PROC1  1.1% identical    PROC6  1.0%    PROC11 1.1%
PROC2  1.0%              PROC7  1.1%    PROC12 1.1%
PROC3  1.1%              PROC8  1.1%    PROC13 1.1%
PROC4  1.0%              PROC9  1.2%    PROC14 1.0%
PROC5  1.0%              PROC10 1.1%    PROC15 1.0%
```

~1% is the chance rate for random bytes. Seventeen mutually different code
images are linked to the same addresses, and they run **concurrently** — the
IBQ demonstrably carries live messages between PROC0, PROC6, PROC8 and the data
path. One physical memory cannot hold seventeen different instruction streams at
`0x7ffa0710` at once. **PROVEN.**

The same holds for data: every core has a literal pool at the same addresses
holding different values. (This is the trap already recorded in
`sn200-attack-surface.md` §"Match literals per image, never by address across
images" — it is the same fact seen from the other side.)

---

## 3. What is shared

### 3.1 SoC unit config pages — PROVEN shared

The ten remote unit slots in §1. Every core reaches them by plain `l32i`/`s32i`
through a literal base. These are hardware blocks, not processors: there are ten
of them, not eighteen, and every core names the same ten.

Identified: `0x82000000` is the PCIe/SerDes unit. `SBLPATCH.bin`'s boot register
script writes `0x820600c4` (address port) then `0x820600c8` (data port) in pairs
to reach `0xc00618e0`, `0xc00638e0`, `0xc00658e0`, … — an **indirect** window
into a `0xc0xxxxxx` space that is not directly load/store addressable. Record
format in `SBLPATCH.bin` is 16 bytes `{0x1004, addr, 0, value}` for a write and
`{0x1003, addr, mask, value}` for read-modify-write. **PROVEN** from the file.

### 3.2 Buffer SRAM at `0x80020000`–`0x80240000` — INFERRED (high confidence)

Bases `0x80020000`, `0x80040000`, `0x80080000`, `0x800c0000`, `0x80160000`,
`0x80180000`, `0x801a0000`, `0x801c0000`, `0x801e0000`, `0x80200000`,
`0x80220000` are named **only** by the five identical data-path images
(PROC2/3/4/5/10) and by PROC8 — PROC8 alone has 47 `l32r` sites for
`0x80040000` and 28 for `0x800c0000`. No control-plane core (PROC0, PROC6,
PROC11–15) names any of them.

That distribution — write-buffer cores plus the admin core, nobody else — is
what a shared staging/queue SRAM looks like. INFERRED rather than PROVEN because
the individual use sites are inside PROC8's overlay bank, where call targets are
largely unresolvable.

### 3.3 PROC8's DDR window at `0x30000000` — PROVEN, and PROC8-only

Count of `l32r`-loaded literals in `0x30000000`–`0x30100000`:

```
PROC8@30000000  11      PROC8@7ff80000  19
everyone else   1 or 2  (incidental constants)
```

The overlay bank occupies `0x30022238`–`0x30040078`. This is a DDR window in
PROC8's map; nothing else in the firmware addresses it.

### 3.4 The 64-bit DMA space — PROVEN

Bulk transfers do **not** use any core's 32-bit view. Firmware Image Download,
`PROC8@30000000` `0x30025619`–`0x30025642`:

```asm
30025619: l32r a9,0x30025404        ; -> 0x7ff827b4  (BSS: staging_base_lo)
3002561f: l32i.n a9,a9,0x0
30025632: { addx4 a14,a14,a8 ; ... }         ; low  = OFST*4 + base_lo
3002563a: { l32i a15,a1,0x14 ; bgeu a14,a12,0x30025644 }
30025642: addi.n a15,a15,1                   ; high += carry
```

An explicit high word with carry propagation means the DMA address space is
wider than 32 bits and is numbered independently of the core maps. A core-local
address like `0x7ff8c7ec` has no representation in it — it is a self alias, and
a DMA engine has no "self".

The staging limit is 4 MiB (`0x300257e8: l32r a11,=0x00400000`).

### 3.5 The inter-processor message path

`Admin_IBQCommandReceiver` (PROC8 `0x7ffb0088`) receives a message pointer in
`a2` and reads `[a2+0xc]` (MSGID) and `[a2+0x10]` (payload). The pointer is into
PROC8's own DRAM: the receiver never dereferences a foreign address. Whatever
hardware moves the message across, the *visible* contract is "a descriptor
appears in my local queue" — consistent with a mailbox/queue unit in the §3.1
grid, not with shared load/store memory. **INFERRED**; the queue unit has not
been pinned to a specific slot.

---

## 4. Can another core write the four target words?

**No.** Three independent checks.

### 4.1 Exact-value sweep — PROVEN

Every `l32r` in all 18 flat images (plain and FLIX slot A), resolved to its
literal, matched against each target value:

| address | what it is | sites | images |
|---|---|---|---|
| `0x7ff8c7ec` | System-Area startup marker | 11 | **PROC0 only** |
| `0x7ff85364` | Crash section handle | 10 | **PROC0 only** |
| `0x7ff85374` | PFail section handle | 5 | **PROC0 only** |
| `0x7ff8d200` | crash flags byte | 15 | **PROC0 only** |
| `0x7ff87c64` | startup mode word | 23 | **PROC8 only** (both banks) |

This reproduces the §1.1 result of `sn200-attack-surface.md` and extends it to
the flags byte and the mode word.

### 4.2 Congruence sweep — the test that was actually missing — PROVEN

Naming the address directly is not the only way to reach it. If core slots were
also visible at per-core global bases, PROC0's `0x7ff8c7ec` would appear
elsewhere as `B + 0xc7ec` for some 512 KiB-aligned `B`.

Swept every image for any `l32r`-loaded literal `V` with
`V mod 0x80000 ∈ {0x5364, 0x5374, 0x7c64, 0xc7ec, 0xd200}`:

- **PROC0**: `0x7ff85364`, `0x7ff85374`, `0x7ff8c7ec`, `0x7ff8d200` — its own.
- **PROC8**: `0x7ff87c64` — its own.
- **Nothing else, in any image, in any slot.**

Widening to *all* 4-aligned pool words (referenced or not) turns up only
`0x60385364`, `0x8300d200`, `0xa370d200`, `0x6fd0d200` and similar — unreferenced
constants scattered across unrelated images, not addresses. There is no alias.

### 4.3 The structural argument — PROVEN

Even granting a hypothetical alias, `0x7ff8c7ec` as written in PROC8's code
resolves to **PROC8's** DRAM. The self alias is resolved by the issuing core.
A write-anywhere primitive on PROC8 that stores to `0x7ff8c7ec` corrupts
PROC8's own BSS and cannot touch PROC0's marker.

### 4.4 The one asymmetry worth stating plainly

`0x7ff87c64` is **PROC8-local**, and PROC8 is the core the host talks to. So a
memory-write primitive on PROC8 *can* reach the mode word. Isolation was never
what protected it. What protects it is that only two instructions write it
(`0x7ffb0157`, `0x7ffb01a7`), both in the IBQ receiver, whose enclosing function
has one caller which itself has zero callers. That argument stands on its own
and is unaffected by anything here.

---

## 5. Is there a DMA path that crosses cores?

**Not to an Xtensa core. There is one to the flash-channel processors.**

The firmware does contain a cross-processor memory write: PROC13 loads the FCC
microcode. StrIds 425–429:

```
425  FCC: Clearing Flash Channel Processor IRAM and DRAM
427  FCC: Loading Microcode into Flash Channel Processor from MLP %08X, Size %08X lines
428  FCC: Firmware record %2d: Moving %5d bytes from DDR base:%08X off:%08X to FCC address %08X
429  FCC: Starting Flash Channel Processor
```

PROC13 `0x7ffacce0` is the loader coroutine (StrId 427 loaded at `0x7ffacee3`).
It carries an FCC-address bound check at `0x7ffacd88`:

```asm
7ffacd88: l32r a13,0x7ffa1220   ; = 0x0011ffff
7ffacd8b: bgeu a13,a14,0x7ffacde5
```

`0x0011ffff` is the top of FCC's IRAM region — FCC's own container loads at
`0x00100000`–`0x00104000` and `0x00120000`–`0x001275f0`. So PROC13 works in
**FCC's** address numbering and dispatches on the IRAM/DRAM split, then hands
`(address, length)` to a mover (`call8 0x7ffbd5b8`) rather than storing directly.
**PROVEN** that the facility exists; the mover itself was not decoded.

Consequences:

- The SoC *does* have a "write processor N's memory" mechanism, so isolation is
  a property of how the address space is arranged, not a hardware impossibility.
  **This is the honest limit of the verdict.**
- No image aims it at an Xtensa core. The Xtensa images are loaded by the
  primary bootloader before any of them runs; `SBLPATCH.bin` is a register
  script plus a `.SEG` payload for PROC0's own slot, not a core loader.
- SPECULATIVE, and not supported by any code in this firmware: whether the same
  mover could target an Xtensa slot. Nothing reachable from the host names such
  a target, and the FCC path is init-only.

---

## 6. The "3.2 MiB staging region" — the brief conflates two things

There is no 3.2 MiB DDR staging window. Two separate objects:

- **3.2 MiB** is the size of the NAND **System-Area crash-log section**, read
  back with a vendor command. Only the first 128 KiB is retrievable because the
  offset mechanism does not work on this drive
  (`sn200-crash-dump-field-results.md`). It is flash, not memory, and no core
  addresses it with a load/store.
- **4 MiB** is the firmware-download DDR staging window (§3.4), addressed as a
  64-bit DMA destination from a BSS pointer at `[0x7ff827b4]`.
- **`0x30022238`–`0x30040078`** is PROC8's DDR-resident overlay bank (§3.3),
  a third, distinct thing.

---

## 7. Effect on the three conclusions

| conclusion | status |
|---|---|
| **1. "No memory-safety bug matters."** The crash-section handles are unreachable from PROC8. | **SURVIVES — upgraded INFERRED → PROVEN.** §4.1–4.3. The decisive structural argument is now backed by the unit grid, not just by literal distribution. |
| **2. "Marker 8 cannot be written."** A core that never holds the marker's address cannot write it. | **SURVIVES — the address-holding argument is now valid.** §4.1–4.2 confirm PROC0 alone holds `0x7ff8c7ec` and no alias exists. Note it never needed isolation: `0x80000008` is constructed nowhere in the firmware (`sn200-readonly-startup.md` §3). |
| **3. "The mode word `0x7ff87c64` is unreachable from the host."** | **SURVIVES, but isolation was never its support.** The word is PROC8-local, so PROC8 is the relevant core. It rests entirely on the two-writer / zero-caller-chain argument, which is untouched. §4.4. |

Nothing here reopens the attack surface. The recommendation that recovering
these drives is a hardware problem rather than a software one is unaffected.

---

## 8. Method and reproduction

```sh
export SN200_FW=~/sn200fw
python3 tools/sn200-fw/segparse.py $SN200_FW/fw/KNGND122/PROC*.bin   # slot layout
python3 tools/sn200-fw/litref.py -v 7ff8c7ec                          # exact-value sweep
python3 tools/sn200-fw/disany.py PROC12 7ffa32a0 7ffa3400             # self/remote cfg pages
python3 tools/sn200-fw/logscan.py 'Clearing Flash Channel Processor'  # FCC loader
```

The congruence sweep (§4.2) and the unit-grid histogram (§1) are one-off scripts
over `litref.l32rs()`; both are a dozen lines and are cheaper to rewrite than to
maintain. Every listing above was entered at a function `entry` confirmed by
`whichfunc.py`, or at a branch target proven by a decoded branch.

Traps hit while doing this:

- Bucketing literal *values* by address window is mostly noise — ordinary
  integer constants land all over `0x8xxxxxxx`. The signal only appears after
  filtering to 4-aligned values named by ≥3 images.
- `0x80000000` is referenced 23–72 times per image. It is the constant
  `-2147483648`, not an address.
- `whichfunc.py` disagreeing with a plausible-looking address means the address
  is mid-bundle. Disassembling from it produces confident nonsense
  (`PROC13 0x7ffacca0` does this).
