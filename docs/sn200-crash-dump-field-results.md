# Crash dump retrieval — what actually happened on hardware

First real retrieval, from the latched drive on `sea1-k8s-2`, 2026-08-04.
Read-only throughout; the drive was latched before and after.

Artifact: `docs/sn200-dumps/nvme7-crash-128k.bin` (131072 B,
sha256 `3feb7258c8291c3a…`).

## CONFIRMED empirically

**The header format derived by RE is exactly right.** Offset 0 reads:

```
00 43 44 48  00 02 02 00  4b 4e 47 4e 44 31 32 32
^^ magic     ^^ version   ^^ "KNGND122" at +0x08
```

- Magic is `00 43 44 48`, i.e. LE u32 `0x48444300` = `"HDC\0"`. Both prior
  descriptions were right and merely differed in byte order — `od` renders the
  leading `\x00` as `.`, which is why it also reads `.CDH`.
- Version `0x00020200` is the **full-dump** value, not the `0x00020100` stub.
- `+0x40` reason tag is **zero**, not `UNEXSTRT` — consistent with a genuine
  recorded fault rather than an unexpected-start stub.

**The string table maps correctly.** `StringTable.csv` from the matching
`KNGND122` image decodes real messages: `"New Boot: Log restarts here"`,
`"SYS: FW Slot %d"`, `"Data_AdminCmdForwarder command CDW0, 1, 10, 11 = …"`,
`"SuBQ deleted subQ %d"`, `"Update BDF MSGID 0x%x"`.

## REFUTED — the offset mechanism

**`CDW13` does not work.** This was previously recorded as PROVEN, derived from
nvme-cli's `wdc_do_dump()`. On this drive it is simply ignored: 50 chunks pulled
at 64 KiB stride were **byte-identical**, all returning the first block.

Also tested and ignored: `CDW11` (any value), `CDW13` at other values, and
repeated sequential reads (no auto-advance).

**What does work:** length in `CDW10` (dwords), no offset at all — the whole
read must be issued as one command. A 128 KiB request returns 128 KiB with a
genuinely different second half; 160 KiB and above return nothing.

**Consequence: only the first 128 KiB of the 3.2 MiB section is reachable.**
Content runs to the final byte (`0x1fffc`), so the dump is **truncated, not
complete**. `mdts` reports 0.

## SOLVED SINCE

- **Record framing is understood.** See `sn200-crash-dump-retrieval.md` §4.3:
  0x18-byte block headers on a 0x1000 grid, 8-byte records, an 8-bit per-block
  record index that overlaps the low nibble of the level byte, and bit 31 as a
  stale-terminator rather than a set constant. The "13 records at ~`0x1000`
  intervals" was the walk dying at record 16 of every block. Correct framing
  yields **733 records** from the same bytes, with arguments intact.
- **The dump is a per-core grid**: 4 blocks per core, `0x4000` per core,
  core N at `0x12500 + N*0x4000`. 128 KiB reaches cores 0–3 of 16.

## STILL OPEN

- **No assert-level (level `0x20`) record exists in the retrievable portion.**
  All 733 records are level `0x60`. The firing assert is not in hand and is
  almost certainly on a core above 3, i.e. past `0x22500`.
- **The offset mechanism itself.** Something must let WD's tooling read the
  whole section; it is not any CDW tried here.

## Honest status

This confirms the container format and the decoding pipeline end to end, and
proves retrieval is safe and repeatable on a latched drive. It does **not** yet
deliver the root-cause assert. Treat the root cause as strongly evidenced by
code and field behaviour, not as confirmed by the drive's own record.

## The fault record IS in our 128 KiB — `.CDI` at `0x11000`

Found 2026-08-04 by searching for the 3-char tag with a **variable lead byte**,
which is why earlier sweeps for `\x00CDI` missed it. The container is:

| offset | tag | contents |
|---|---|---|
| `0x00000` | `\x00CDH` | header: version `0x00020200` (full fault dump), `"KNGND122"` at `+0x08`, reason tag at `+0x40` **zero** |
| `0x00100` | `\x00MMAP` | one region only: `0x60000000`–`0x60003ff0` (16368 B), count 1 |
| `0x11000` | `\x09CDI` | **the fault context** |
| `0x12500` | — | per-core log blocks, 4 × `0x1000` per core |

### The `.CDI` record, raw

```
+000: 49444309 00000006 bffbbfa8 7ff9ff80
+010: 7ff91d78 ffffc001 7ff977a8 00000003
+020: 00000001 00000004 00000000 7ff941d4
+030: 00000000 7ff9ff60 00000024 7ff941d4
+040: 00000000 00000000 bffbaba3 7ff9fec0
+060: 00000001 ffffffff bffbacfd 7ff9fea0
+0e0: 7ff97784 00000000 bffa3973 7ff9ffb0
+120: ... 7ffbdab8
+130: 7ffbdac3 ...
```

**The `0xbffb****` values are Xtensa windowed return addresses** — bits 31:30
are the call-size increment, so `0xbffbbfa8` → `0x7ffbbfa8`. Paired with an
adjacent `0x7ff9f***` stack pointer each time, this is a **saved call chain**:

```
0x7ffbbfa8   sp 0x7ff9ff80
0x7ffbaba3   sp 0x7ff9fec0
0x7ffbacfd   sp 0x7ff9fea0
0x7ffa3973   sp 0x7ff9ffb0
```

plus two direct code addresses at `+0x12c`/`+0x130`: `0x7ffbdab8`, `0x7ffbdac3`.

> **Superseded.** The record is now fully decoded from its writer — see
> **`docs/sn200-fault-record.md`**. The core is **PROC9** (NVMe-MI/MCTP/SMBus),
> `+0x04 = 6` is the **vector index** (not a core index, and not PROC6), the
> record begins at staging-buffer `+0x14`, and **`EPC1`/`EXCCAUSE` are provably
> not appended**. The section below is kept only to show what was believed
> before the writer was traced; every claim in it about the core is refuted.

### Why this does not yet name the core — INFERRED, needs work

Per-core address spaces are **self-aliased**, so `0x7ffb****` resolves inside
*every* image; `whichfunc.py` returns 2–15 candidates per address. The record
must identify its own core.

`+0x04 = 6` is the obvious candidate for a core index, and it would be
significant if true: **PROC6 is the core that writes the CLEAN/PFAIL completion
marker** at `0x7ffbba61`. Under that reading `0x7ffbdab8`/`0x7ffbdac3` land
inside PROC6 `0x7ffbd0f8` (a 3072-byte function). But `+0x04` may equally be a
record version, and the field layout is **not proven** — the offsets above are
positional guesses, not a decoded structure.

**Do not build on this until the layout is traced from the writer.** The
exception vectors stage into `0x7ff97df0` and push 76 dwords through the
section-append primitive; that writer defines the truth.
