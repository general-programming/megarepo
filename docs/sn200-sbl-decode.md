# SBLPATCH.bin: the container is solved, and what the SBL says

`SBLPATCH.bin` (269 470 B, ships only in `KNGND110.bin`) is now fully carved and
the secondary boot loader disassembles cleanly. This document records the file
format, the decoder, and what the SBL answers about data recovery.

Decoder: `tools/sn200-fw/sblpatch.py`. Tests: `tools/sn200-fw/tests/test_sblpatch.py`.
Put the image at `~/sn200fw/fw/KNGND110/SBLPATCH.bin` (sha256
`8bdb753bbc01896ad5deacd7231eac8a8afbd3aa2187db195b1d5188a1310174`) — extract it
with `tar -xf KNGND110.bin SBLPATCH.bin`.

```
python3 tools/sn200-fw/sblpatch.py ~/sn200fw/fw/KNGND110/SBLPATCH.bin --list
python3 tools/sn200-fw/sblpatch.py ~/sn200fw/fw/KNGND110/SBLPATCH.bin --extract out/
```

---

## 1. The format — PROVEN

The old model ("0x30 header, then a flat stream of 16-byte records, with `.BIN`
containers embedded") is wrong in one decisive respect: **everything after the
first 0x100 bytes is two independent streams interleaved in 0x100-byte blocks.**

```
0x0000-0x002f   PMCSEEPM001 header; u16 @0x20 = 0x0030, the record offset
0x0030-0x00ff   13 setup records, 16 bytes {type, address, mask, value}
0x0100+         block i (at 0x100 + i*0x100) belongs to stream (i & 1):
                  i even -> stream 0   i odd -> stream 1
```

Each de-interleaved stream is then the ordinary thing: optional 16-byte records,
a `.BIN` marker, and a `.SEG` chain whose `data_offset` is **relative to that
stream's `.BIN` marker**, terminated by `data_offset == 0xffffffff`.

Stream 0 is the **MBL** container. Stream 1 is a run of EEPROM setup records
followed by the **SBL** container.

That is why the small segments decoded and the large ones did not: a segment
under 0x100 bytes usually sits wholly inside one block and survives a naive
flat read, while the two large code segments are shredded by the other stream's
blocks every 256 bytes.

### Why the de-interleave is certain, not a guess

Four independent oracles, each of which would have failed on a wrong weave:

1. **Both `.SEG` chains close.** Each chain is a linked walk (`next header =
   data_offset + length`) and both land exactly on a `0xffffffff` terminator
   after 10 and 12 segments. A one-block error desynchronises immediately.
2. **A string split across a block resumes in the next same-stream block.**
   File `0xddf0` ends `...valid entries for highTemp are TRUE`; `0xde00` is
   stream-1 data; `0xdf00` resumes `|FALSE \n`.
3. **`l32r` literals resolve.** Every literal pool reference in the MBL code
   segment lands inside the MBL data segment, and 157 words in the SBL code
   segment are pointers into the SBL string segment.
4. **The console command table decodes and its handlers have `entry`
   prologues.** See §2 — eight `{name, help, brief, handler, nparams}` records
   whose handlers all land inside `0x7ffb6000-0x7ffbfab0` and all begin
   `36 41 00  entry a1,0x20`.

### Load map — PROVEN

| stream | load range | size | what |
|---|---|---|---|
| 0 | `0x7ff80000`–`0x7ff88540` | 0x8540 | MBL data / literal pool |
| 0 | `0x7ffa0000`–`0x7ffa04d0` | small | MBL variables (8 segments) |
| 0 | `0x7ffa0710`–`0x7ffad5e0` | 0xced0 | **MBL code** |
| 1 | `0x7ff98000`–`0x7ff9e064` | 0x6064 | **SBL data, strings, command tables** |
| 1 | `0x7ff9ff60`–`0x7ff9fffc` | 0x9c | `SYSservices` / boot-handoff block |
| 1 | `0x7ffa0000`–`0x7ffa0718` | small | SBL variables (9 segments) |
| 1 | `0x7ffb6000`–`0x7ffbfab0` | 0x9ab0 | **SBL code** |

---

## 2. The SBL console — PROVEN

The table at `0x7ff98078` (`SBL` group) and `0x7ff98830` (`SHARED` group) is
`{name, help, brief, handler, nparams}`:

| command | handler | notes |
|---|---|---|
| `MBL` | `0x7ffb8368` | prints `SYS: Go into MBL diagnistic mode`, then `LaunchMbl(6)` |
| `EraseSystem` | `0x7ffb837c` | `SpiErase(0x40000, 0xfc0000)` via `0x7ffbf0d0` |
| `ReadSpi` | `0x7ffb83a4` | `SpiRead(addr, buf, 256)` via `0x7ffb9088` |
| `MemRead` | `0x7ffb9fb4` | DWord-aligned only |
| `MemWrite` | `0x7ffb9fe8` | bare `s32i a3,a2,0` — **no address filter at all** |
| `CARRead` | `0x7ffba014` | `(node<<19) \| off \| 0x80000000` |
| `CARWrite` | `0x7ffba030` | same addressing |
| `Reset` | `0x7ffba048` | `SYSservices[+0x38]`, then `0x82a60020 <- 3` |

**There is no `WriteSpi` console command.** The list above is exhaustive for the
two SBL-side groups; the rest of the table is DDR debug (`PrintPhy`, `PrintCtl`,
`PrintDcsu`) and the built-ins `Help` / `Mode`.

`MemWrite` is unrestricted: it validates only DWord alignment and stores. So
`MemWrite 0x7ff9ff64 4` does reach the boot-mode word.

---

## 3. (a) Is there an SPI **write** primitive? — YES, PROVEN, but not on the console

`0x7ffb9120` is `SpiWrite(addr, buf, len)`. It is byte-for-byte parallel to the
known `SpiRead` at `0x7ffb9088` (same 512-byte argument clamp, same
`SpiSelect`/`SpiDeselect` bracket) and then does what only a program does:

* splits the transfer on a **256-byte page boundary** (`movi a13,256;
  sub a13,a13,a14; minu a13,a4,a13`) — the SPI NOR page-program rule;
* refuses when the write-protect word `[0x7ff9dcc4+0xc]` is set, returning 16;
* calls the bounds gate `0x7ffb8d88`, which permits only `addr <= 0x39fff`
  unless `[0x7ff9dcc4+0x10]` is set.

Callers: `0x7ffb9582`, `0x7ffbf084` (the "Programming MBL" path behind
`(info): SBL: Programming MBL ...` / `SBL: ERROR - Cannot program MBL`), and
opcode 10 of the script engine in §4.

**What this does *not* buy.** Marker 8 lives in System-Area section 6, which is
above the `0x39fff` gate, and reaching `SpiWrite` at all needs code execution in
the SBL — for which you already need the UART console, whose `MemWrite` is a
strictly better tool. `SpiWrite` is a fact about the firmware, not a new door.

---

## 4. (b) Is `LOAD_N_GO` host-reachable? — NO, on the evidence available

`0x7ffba13c` is a 32-entry script interpreter. It takes a step index, reads a
16-byte instruction from the array at `0x7ff9f0d0`, takes the low 5 bits of word
0 as the opcode, and jumps through a 3-byte-stride table at `0x7ffba162`:

| opcode | handler | action |
|---|---|---|
| 9 | `0x7ffba2a6` | `SpiRead` 4 bytes |
| 10 | `0x7ffba28f` | **`SpiWrite` 4 bytes** |
| 11 / 12 | `0x7ffbf40c` / `0x7ffbf3b0` | read / write a register |
| 24, 25 | `0x7ffba276`, `0x7ffba264` | erase (0x2000 / 0x20000 granularity) |
| 30 | `0x7ffba230` | hardware reset (`0x82a60020 <- 3`), then spin |
| 31 | `0x7ffba1ea` | **LOAD-N-GO** |

Opcode 31, in full:

```
7ffba1fb  l32i a8,a3,0x20        ; a3 = 0x7ff9ff60; +0x20 = the image loader
7ffba201  callx8 a8
7ffba204  bnez.n a10,0x7ffba225  ; -> "SBL: LOAD-N-GO failed ..."
7ffba206  l32r a10,...           ; "New firmware is successfully loaded via LOAD-N-GO"
7ffba20c  l32i a8,a3,0x38 ; callx8 a8
7ffba211  l32i a8,a3,0x24        ; the launcher, 0x7ffb7c64
7ffba213  movi a9,4
7ffba215  s32i.n a9,a3,0x4       ; [0x7ff9ff64] = 4
7ffba217  callx8 a8
```

So boot mode 4 is written at exactly one site in the SBL, and it is a script
opcode.

**Where the script comes from.** The executor is `0x7ffb70bc` (`PCIe_Init`): it
prints `SBL: Initialize PCIe interface`, runs `0x7ffb7104` (the PCIe/SerDes
bring-up that emits `SBL: WARNING - PBL script is mssing or malfunctioning`),
prints `SBL: Open PCIe interface`, then loops the step count held at
**board-config + 0xb5** (`0x7ff9b630`, the buffer filled by `SBL: Read Board
Configuration` from SPI EEPROM). The step array `0x7ff9f0d0` is in SBL BSS and
**no instruction in the SBL code segment stores to it** — only the two readers
above load its address. The script is therefore delivered by the PBL/board-config
path out of the SPI EEPROM, not by anything the host says.

Searching the other direction is equally negative: `PCIe_Init` is exported to the
firmware as `SYSservices[+0x5c]`, and PROC0 — which loads `0x7ff9ff60` at 30
sites — **never uses offset +0x5c**. The offsets PROC0 does use are
`0x0, 0x4, 0x8, 0xc, 0x10, 0x14, 0x18, 0x24, 0x2c, 0x30, 0x34, 0x38, 0x3c, 0x50,
0x70, 0x88, 0x108`.

**Verdict: INFERRED (high) that `LOAD_N_GO` has no NVMe-reachable trigger.** It
is a boot-time script action whose script lives in the SPI EEPROM board
configuration. It is not a mailbox, not a doorbell, and not an admin opcode.
The one residual unknown is how the board-config PBL script region gets into
`0x7ff9f0d0` (hardware PBL engine, most likely) — if that staging were somehow
host-visible on this part, the conclusion would change. Nothing in the SBL says
it is.

---

## 5. (c) Does the SBL rewrite the boot mode at handoff? — **NO on the path that matters**

This was the largest open speculation in `sn200-data-recovery.md`, and the answer
is favourable.

`0x7ff9ff64` is a **bidirectional** handshake word, not an SBL output:

* **SBL entry** (`0x7ffb6988`) *reads* it and switches:
  0 `cold boot`, 1 `warm boot`, 3 `Returning from MBL`, 5 `Returning from FW`,
  8 `Rebooting to another FW`. All five paths converge on `0x7ffb69dd` and
  **none of them writes the word**. Only the `unknown state=%d` default writes
  (a 0).
* **PROC0 writes it on the way out**: `0x7ffa3adb` does
  `movi a9,5; s32i a9,[0x7ff9ff64]` then calls `SYSservices[+0x24]`. That is the
  `SYS SBL` round trip.
* **PROC0 reads it on the way in**: `0x7ffaae28` `l32i a12,[0x7ff9ff64]` →
  `beqi a12,4,0x7ffaae53`, jumping over both crash-ball tests and the empty-SA
  door. The recovery doc's target address is confirmed correct.
  PROC0 also tests it at `0x7ffa34f6` (`beqi a8,4` skips "Updating status of
  firmware slot") — mode 4 is genuinely wired through the firmware.
* **The launcher** `0x7ffb7c64` (= `SYSservices[+0x24]`) *reads* the word and
  dispatches: mode 0/1 → arg 0; modes 2/3/5/6 → arg 3; mode 7 → it writes 1 and
  uses arg 3; **mode 4 → arg 0** — i.e. LOAD_N_GO lands in the same startup class
  as a clean cold boot, as the recovery doc claims. It does not clear the word.

Which paths *do* write it:

| site | value | when |
|---|---|---|
| `0x7ffb80a3` (`LaunchMbl`) | its argument | `2` when DDR calibration data is missing; `6` from the `MBL` console command |
| `0x7ffba215` | `4` | script opcode 31 (LOAD-N-GO) |
| `0x7ffb7cdd` | `1` | launcher, only when the incoming mode was 7 |
| `0x7ffb69db` | `0` | SBL entry, only for an unrecognised state |

And the one that matters — the mainline **boot-firmware-from-EEPROM** path:

```
7ffb6b5b  call8 0x7ffb6cf0        ; load the firmware image out of EEPROM
7ffb6b67  l32r a10,... ; call8    ; "(info): SBL: Booting new firmware"
7ffb6b6d  call8 0x7ffb8334        ; SYSservices[+0x38] pre-handoff hook
7ffb6b70  call8 0x7ffb7c64        ; the launcher -- with 0x7ff9ff64 UNTOUCHED
```

**There is no store to `0x7ff9ff64` anywhere between SBL entry and this
handoff.** So a `MemWrite 0x7ff9ff64 4` performed in the loader console, followed
by letting the SBL boot the firmware normally, survives to PROC0 and takes the
`beqi a12,4` bypass.

Two caveats that are now the real risk, replacing the old one:

1. **Sequencing.** A `SYS SBL` from the *firmware's* `DiagMgr>` sets the word to
   5 on the way out. The write must therefore happen **after** the SBL has read
   the state (it prints `Returning from FW ...`) and **before** `0x7ffb6b70`.
   That is a console-timing question, not an address question.
2. **Do not use the `MBL` command afterwards** — it calls `LaunchMbl(6)` and
   overwrites your 4 with a 6.

---

## 6. What did not turn up

* No `WriteSpi`, `SetBootMode`, or `LoadNGo` console command. The command table
  is complete and short.
* No host mailbox, doorbell, or PCIe-writable region feeding the script engine.
* No second writer of marker 8, and nothing in the SBL touches System-Area
  section 6.
* `SYSservices[+0x38]` — the hook `SYS SBL` was feared to use for an SPI flush —
  is `0x7ffb8334`. It is 0x30 bytes long: it clears a flag at `0x7ff9bb9c`,
  drains a pending queue at `+0xc` through `0x7ffb80d8`, and then makes one
  indirect call through `[0x7ff9bb9c+4]`, passing `0x7ffb80d8`. Its shape is a
  console/output drain, and it contains **no direct call to `SpiWrite`,
  `SpiErase`, or any EEPROM routine** — but the single indirect call means this
  is **INFERRED**, not proven. `Reset`, every launch path and `LOAD-N-GO` all
  call it identically, so it is on the path whatever you do. Risk 4 in
  `sn200-data-recovery.md` §7 drops from "unexamined" to "examined, one
  indirect call unresolved, no EEPROM writer in the direct body".
