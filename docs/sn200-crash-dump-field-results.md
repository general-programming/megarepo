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

## STILL OPEN

- **Record framing is not understood.** The decoder finds no self-consistent
  chain and falls back to a scan, which yields only 13 records across 128 KiB —
  at suspiciously regular ~`0x1000` intervals, suggesting per-page framing that
  the scan is sampling rather than parsing. Argument values are consequently
  garbage (`"FW Slot 6053912"`), exactly as the decoder warns.
- **No assert-level (level `0x20`) record appears in the retrievable portion.**
  So the actual firing assert is *not yet in hand* — it may lie beyond 128 KiB,
  or require correct framing to surface.
- **The offset mechanism itself.** Something must let WD's tooling read the
  whole section; it is not any CDW tried here.

## Honest status

This confirms the container format and the decoding pipeline end to end, and
proves retrieval is safe and repeatable on a latched drive. It does **not** yet
deliver the root-cause assert. Treat the root cause as strongly evidenced by
code and field behaviour, not as confirmed by the drive's own record.
