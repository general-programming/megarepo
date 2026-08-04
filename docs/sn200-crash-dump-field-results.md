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
