# `0xC6` — the complete "VUC SCSI Ported Command" dispatch table

Opcode `0xC6` is the **other** vendor family that survives the Post-Crash gate
(admitted only with command byte `0x20` or `0x30`). We have used it for years
for crash-section reads without ever enumerating it. This document enumerates
every command byte it accepts, every sub-command underneath the two that matter,
and settles whether any of it reads user media or the L2P.

**Verdict up front:**

- The `0xC6` surface is **seven** command bytes — `0x20`, `0x21`, `0x22`,
  `0x23`, `0x30`, `0xB7`, `0xCD` — and nothing else. **PROVEN.**
- **Only `0x20` and `0x30` are reachable while latched.** The other five are
  rejected by the gate before they reach the handler.
- **No `0xC6` selector reads user data or the L2P.** The `0xCA` analysis missed
  nothing. `0x30` cannot even carry data to the host.
- `0x0720` / `0x0820` are **`0xC6` command `0x20`, sub-commands 7 and 8** — the
  71808-byte producer arms. The runbook's "never send" row attributed them to
  `0xFF`; that was already known to be wrong, and this document says what they
  are.
- **New hazard:** the safe read family `0x_20` is one nibble from the
  unidentified action family `0x_30`, and both pass the gate.

Labels: **PROVEN** = read off correctly-decoded instructions. **INFERRED** =
short chain over proven facts. **SPECULATIVE** = neither.

---

## 1. Address-space groundwork — and a validated correction

`0xC6` handlers live in **overlay 18** (descriptor-table row 17 at
`0x7ff81ae4`: `dst = 0x7ffbc000`, `len = 0x3040`, `src2 = 0x3002ea38`). Every
overlay in this bank loads at the *same* runtime address `0x7ffbc000`, so a
static label means a different function depending on which overlay is resident.
For overlay 18:

```
runtime = static + 0x4DF915C8        (i.e. 0x7ffbc000 − 0x3002ea38)
static  = runtime − 0x7ffbc000 + 0x3002ea38
```

**This resolves the "unvalidated call-set" caveat in
`sn200-crash-dump-retrieval.md` §1.3.** That note said the nine call targets of
the crash-dump handler "cannot currently be validated" because the static
addresses land in a zero hole. Applying the runtime rule instead, **17 of 17**
call targets taken from the `0x30` subtree and the `0x20` handler land on an
`entry` (`0x36`) byte in `PROC8_7ff80000`:

| static | runtime | identity |
|---|---|---|
| `0x30026fe0` | **`0x7ffb45a8`** | `Log_Emit` — the known PROC8 log function |
| `0x3002c1a0` | **`0x7ffb9768`** | enqueue on the OAM worker list |
| `0x30022504` | `0x7ffafacc` | allocate |
| `0x300224c0` | `0x7ffafa88` | free |
| `0x3002d410` | `0x7ffba9d8` | `memset` |
| `0x3002d0ac` | `0x7ffba674` | request-field setter (zeroes status) |
| `0x3002d0d0` | `0x7ffba698` | request-field setter |
| `0x3002d3c8` | `0x7ffba990` | request-field setter |
| `0x3002d72c` | `0x7ffbacf4` | mailbox / hardware transmit |
| `0x3001e83c` / `0x3001e900` | `0x7ffabe04` / `0x7ffabec8` | (cmd `0x30` only) |
| `0x3002cb80` `0x3002a018` `0x3002c400` `0x3002c430` `0x30016dcc` `0x3001bb68` | `0x7ffba148` `0x7ffb75e0` `0x7ffb99c8` `0x7ffb99f8` `0x7ffa4394` `0x7ffa9130` | |

17/17 landing on `entry` is not a coincidence, and `0x30026fe0 → 0x7ffb45a8`
independently reproduces the exact interlock the `0xFF` teardown used on overlay
22. **PROVEN.** The old "none of the nine lands on an `entry`" observation was an
artefact of resolving in static space, exactly as `sn200-oam-dispatch.md` §1.1
warned.

**Consequence: the callee set now *is* usable as evidence.** It contains no
erase and no program primitive on any `0x20` path.

---

## 2. Reaching the handler — PROVEN

`PROC8@7ff80000`, inside the admin dispatcher:

```asm
7ffa7bf5: movi a15,198
7ffa7bf8: { extw ; bne a11,a15,0x7ffa75b4 }    ; a11 = opcode; not 198 -> elsewhere
7ffa7c00: l32i.n a12,a6,0x0
7ffa7c02: l32r  a13,0x7ffa0f34                 ; -> 0x7ffbea44   <-- the handler
7ffa7c05: { s32i a14,a12,0x20 ; j 0x7ffa6e89 }
```

`0x7ffbea44` → static **`0x3003147c`**, and that byte is `0x36` (`entry a1,0x40`).

### The gate — unchanged, restated

`Admin_CheckCmdAllowed` `0x7ffa6b18` admits `0xC6` **only** when the command
byte (`ctx+0x38` = `CDW12[7:0]`) is `0x20` or `0x30`
(`sn200-crash-dump-retrieval.md` §1.5). Both the Post-Crash gate and the
VUC-Control gate apply the same two-value test.

---

## 3. The command-byte dispatch — PROVEN, and it is complete

`0x3003147c` is a coroutine (`l32i.n a11,a2,0x18 ; jx a11`). On first entry it
validates, logs StrId 1617 `"VUC SCSI Ported Cmd %08X"`, and dispatches:

```asm
30031542: { l8ui a10,a10,0x38 ; movi a12,1 }        ; a10 = ctx+0x138 = CDW12[7:0]
30031550: { s32i a12,a5,0x160 ; beqi a10,32,0x30031641 }    ; 0x20
30031558: movi.n a13,33      ; beq a10,a13,0x30031656       ; 0x21
30031562: movi.n a14,34      ; beq a10,a14,0x3003166b       ; 0x22
3003156c: movi a15,183       ; beq a10,a15,0x30031680       ; 0xB7
30031577: movi.n a8,35       ; beq a10,a8,0x30031695        ; 0x23
30031581: movi a9,205        ; beq a10,a9,0x300316aa        ; 0xCD
3003158c: movi.n a11,48      ; beq a10,a11,0x3003162c       ; 0x30
30031596: l32r a10,-> StrId 1618 "VUC SCSI Ported Cmd not supported"
3003159f: or a12,a12,a7      ; s32i a12,a5,0x160            ; status |= 0x40040000
```

Every arm has the same body — load a handler function pointer, `mov a12,ctx`,
`call8 0x3002c1a0` (= `0x7ffb9768`, enqueue on the OAM worker list), stash a
resume PC, yield. **There is no eighth command byte.**

| `CDW12[7:0]` | handler runtime / static | reads sub byte? | identity |
|---|---|---|---|
| `0x20` | `0x7ffbdeec` / `0x30030924` | **yes**, 9 subs | `VUC Get Drive Log` — §4 |
| `0x21` | `0x7ffbe3f4` / `0x30030e2c` | no | Get hardware-component values, ≤68 bytes |
| `0x22` | `0x7ffbd5f4` / `0x3003002c` | **yes**, 5 subs | `VUC Reset Drive Stats` |
| `0x23` | `0x7ffbe940` / `0x30031378` | no | reads a firmware buffer via descriptor `0x7ff82644`; **unidentified** |
| `0x30` | `0x7ffbd400` / `0x3002fe38` | **yes**, 7 subs | **unidentified action family** — §5 |
| `0xB7` | `0x7ffbe4d0` / `0x30030f08` | **yes**, = list section | Read Defect Data (bad-block list) |
| `0xCD` | main image `0x7ffb9208` | no | overlay-manager / register command; **unidentified** |
| anything else | — | — | StrId 1618, `status |= 0x40040000`, no side effect |

`0xB7` is the SCSI `READ DEFECT DATA(12)` opcode, which is what the family name
"SCSI Ported Command" means, and it matches `libdmi_core`'s
`gf_nvme_get_defect_data_real` encoding `(section << 8) | 0xB7` exactly. `0x21`
matches `_gf_capture_hwcomp_values`' `0x0021`. Both are **PROVEN** encodings,
independently corroborated.

### The length rule — PROVEN, and a useful interlock

Before dispatching, `0x3003170a` cross-checks `CDW10` against the command byte:

```asm
3003170a: l32i a13,a2,0x130                    ; CDW10
3003170d: { l8ui a11,a14,0x38 ; bnez a13,0x3003171d }
30031715: beq a11,a12,0x3003171b               ; a12 = 0x22
30031718: bne a11,a10,0x30031723               ; a10 = 0x30
30031723: l32r a10,-> StrId 1616 "VUC SCSI Ported Command Invalid Length = %x"
```

**`CDW10 == 0` is rejected for every command byte except `0x22` and `0x30`.**
Those two are the no-host-data families; every other `0xC6` command must carry a
transfer length. This is a clean structural fact and it is corroborated by every
working command we send (`CDW10 = 2` for the size probes, `0x8000` for the body
read).

**It also settles the media question for `0x30` on its own: a family that is
required to have a zero transfer length cannot return user data.**

---

## 4. Command `0x20` — `VUC Get Drive Log`, subs 0–8

Fully enumerated in `sn200-crash-dump-retrieval.md` §1.2.4 (dispatcher
`0x30030d14`, arms 0–8, StrId 1611 for anything ≥ 9). Not repeated here except
for the safety column and the two arms the runbook still lists as unresolved.

| sub | `CDW12` | what | latched | class |
|---|---|---|---|---|
| 0 | `0x0020` | drive-log body | yes | read-only (PROVEN) |
| 1 | `0x0120` | drive-log + string-table sizes | yes | read-only |
| 2 | `0x0220` | string-table body | yes | read-only |
| 3 | `0x0320` | crash-dump size / armed probe | yes | read-only |
| 4 | `0x0420` | crash-dump body | yes | read-only |
| 5 | `0x0520` | pfail-dump size / armed probe | yes | read-only |
| 6 | `0x0620` | pfail-dump body | yes | read-only |
| **7** | **`0x0720`** | 71808-byte region, producer `0x7ffa972c` | yes | **do not send** |
| **8** | **`0x0820`** | 71808-byte region, producer `0x7ffa43c0` | yes | **do not send** |
| ≥9 | — | rejected, StrId 1611 | yes | inert |

### 4.1 `0x0720` and `0x0820`, resolved as far as they can be

Both arms build a job (`+0x11c = 7` request type, `+0x12c = 1122` = the length
in 64-byte units, `1122 × 64 = 0x11880 = 71808`, `+0x128` from the descriptor at
`0x7ff82904`), **spawn a worker coroutine**, and then DMA a fixed
71808-byte buffer. They are the only `0x20` arms that produce data rather than
pointing at an existing firmware-owned section.

The workers, read at the instruction level:

- **`0x7ffa972c` (sub 7).** Fills byte fields `+0x44`…`+0x4b` and words
  `+0x110`/`+0x114` of the job, calls `0x7ffbacf4` (the mailbox/hardware
  transmit routine), then reads a result word and stores it at `job+0x188`.
  Contains three-byte opcodes in the `op0 = 0` custom space that this repo's
  decoder does not resolve.
- **`0x7ffa43c0` (sub 8).** Indexes a DRAM table at
  `0x7ff879f8 + (idx << 4) + 0x1f0` and **conditionally zeroes that word**
  (`bnei a8,256 ; movi a8,0 ; s32i a8,a6,0x1f0`). That is a state mutation, in
  firmware DRAM, on a path the host can trigger while latched.

**Class: reads-with-a-side-effect, not certified pure.** No erase and no program
primitive is reachable from either — the resolved callee set (§1) contains
none — so they are not *destructive*. But `0x7ffa43c0` demonstrably writes
firmware state, and both contain undecoded custom opcodes. **Keep them on the
do-not-send list; the reason is "unaudited mutation", not "suspected wipe".**

What the 71808-byte region *is* remains **unidentified**. It is not a crash
section (no `.CDH` magic path), not the string table and not the drive log — the
arms take a different descriptor and a different length constant from all of
those.

---

## 5. Command `0x30` — the other gate survivor, and the biggest remaining unknown

Handler `0x3002fe38` (`entry a1,0x20`). Sub byte at `ctx+0x139`, same field the
`0x20` family uses:

```asm
3002fe70: addmi a7,a2,256 ; addi a7,a7,-16
3002fe76: l8ui a9,a7,0x49                 ; = ctx+0x139 = CDW12[15:8]
3002fe79: beqz  a9,   0x3002ffae          ; sub 0 -> handler 0x3002f908
3002fe7c: beqi  a9,1, 0x3002ffc3          ; sub 1 -> handler 0x3002fac4 (guarded)
3002fe84: beqi  a9,2, 0x30030011          ; sub 2 -> handler 0x3002f610
3002fe8c: beqi  a9,3, 0x3002ffd2          ; sub 3 -> handler 0x3002ef44
3002fe94: beqi  a9,4, 0x3002ffe7          ; sub 4 -> handler 0x3002f9a8
3002fe9c: beqi  a9,5, 0x3002fffc          ; sub 5 -> handler 0x3002f9fc
3002fea4: beqi  a9,6, 0x3002ff66          ; sub 6 -> handler 0x3002f700
3002feac: (default) call8 0x3001e83c -> 0x7ffabe04
```

Seven sub-commands, `0x0030` … `0x0630`, plus a default arm. **PROVEN
enumeration.**

What is known about them, honestly:

- **They transfer no host data** (§3 length rule). Whatever they do, they do it
  inside the drive.
- Sub 1 is guarded by a state check on `[0x7ff8f46c]` and diverts if it reads 1.
- Sub 4 spawns main-image worker `0x7ffa97f4`, which composes a 64-bit address
  against `0x82180000 & 0x0003ffff` and hands it to `0x7ffbacf4` — a DMA/mailbox
  descriptor build.
- Sub 5 indexes a table at `0x7ff81410` and compares an entry against 53.
- Sub 6 touches `0x7ff96b04` (the OAM worker list head) and `0x7ff80490`, and
  logs StrId 111 `"SYS: ERROR - OCP interrupt dispatch table is full"` on one
  path — i.e. it registers something with the interrupt dispatcher.
- The default arm builds a record containing the literal bytes `0xC9`, `0xC6`,
  `0x30` and the sub byte and hands it to `0x7ffabec8`, which has the shape of a
  forwarded/ported command.
- **No erase or program primitive appears anywhere in the resolved callee set of
  the `0x30` subtree** (§1). That is a genuine negative and it is the only
  reassuring thing here.

**`0x30` is NOT "VUC Reset Drive Stats".** `sn200-command-reference.md` and
`sn200-attack-surface.md` §4.2 attribute that name to command byte `0x30`. It
belongs to command byte **`0x22`**: the "Reset Drive Stats" dispatcher at
`0x30030918` (`l8ui a11,a15,0x39 ; l32r a10,-> StrId 1602
"VUC Reset Drive Stats SubCmd %08X"`) sits inside `0x3003002c`, which is the
`0x22` arm's handler pointer. **PROVEN, corrected here.** The "zero-length
internal control/handshake" observation was right; the label on it was not.

**Operational position: `0x30` is unidentified, state-mutating in at least two
arms, and reachable on a latched drive. Do not send any `0x__30` encoding.**
This is the single largest un-audited surface that survives the gate, and it is
one nibble from the command we type on every drive.

---

## 6. Commands `0x21`, `0x22`, `0x23`, `0xB7`, `0xCD` — rejected while latched

All five fail the gate's `a4 ∈ {0x20, 0x30}` test and return `0x8F8A0000` →
SCT 7 / SC `0xC5` `HDMS_DEV_DIAGNOSTIC_MODE`. Recorded for completeness and for
anyone working on a *healthy* drive:

| cmd | sub byte | notes |
|---|---|---|
| `0x21` | none | Reads ≤ 68 bytes (`movi a12,68 ; minu a12,a12,a13` against `CDW10`) from a descriptor at `0x7ff82904`. `libdmi_core`'s `_gf_capture_hwcomp_values`. Read-only, INFERRED. |
| `0x22` | 0–4 | `VUC Reset Drive Stats`. Sub 2 contains a startup-mode test (`l32r a14,-> 0x7ff87c64 ; beqi a14,6`). Resets SMART/PE-cycle counters — StrId 3474 `"Reset PE cycles greater than current - reset smart Aborted"`. **State-mutating by design.** |
| `0x23` | none | Reads a firmware buffer via descriptor `0x7ff82644` into the host DMA path. Unidentified. |
| `0xB7` | = defect-list section | SCSI `READ DEFECT DATA(12)`. Walks the bad-block list; StrId 1613 `"End-of-list marker not found in the bad block list"`. Physical addresses only — **not** an L2P and **not** user data. |
| `0xCD` | none | Dispatches into the main image at `0x7ffb9208` with `[ptr+0x20] = 18`. Reads `[a2+0x20]`/`[a2+0x24]` as a descriptor pair. Unidentified. |

---

## 7. The media / L2P hunt — clean negative

The brief asked specifically whether any `0xC6` selector reads media or the
mapping table, since `0xC6` is a *read* family that survives the gate.

| candidate | verdict |
|---|---|
| `0x20` subs 0–6 | Every source address is recomputed from a firmware-owned descriptor on every call (`sn200-crash-dump-retrieval.md` §1.2.4). Log, string table, crash sections. **No LBA, no L2P.** |
| `0x20` subs 7–8 | Fixed 71808-byte producer output. Not media; the producers touch a DRAM counter table, not the mapping table. |
| `0x30` subs 0–6 | **Cannot return data at all** — the length rule forces `CDW10 == 0`. |
| `0xB7` | Bad-block list: *physical* block addresses. Not user data, and rejected while latched anyway. |
| `0x21`, `0x23`, `0xCD` | Firmware buffers, rejected while latched. |

**No `0xC6` encoding understands an LBA.** The only commands in the whole vendor
surface that do are `0xCA/0x0000` (`Admin_VucFlashLogicalToPhysical`) and
`0xCA/0x0001` (`Admin_VucFlashRead`), and neither is in the allow-list
(`sn200-vuc-flash-read.md`). **The `0xCA` analysis missed nothing, and there is
no recovery path hiding in `0xC6`.**

---

## 8. Per-encoding safety summary

| `CDW12` | reachable while latched | class |
|---|---|---|
| `0x0020` `0x0120` `0x0220` `0x0320` `0x0420` `0x0520` `0x0620` | yes | **read-only** — the retrieval procedure |
| `0x0720` `0x0820` | yes | **do not send** — unaudited producers, one mutates DRAM state |
| `0x0920`…`0xFF20` | yes | inert (StrId 1611) |
| `0x0030`…`0x0630` | **yes** | **do not send** — unidentified action family |
| `0x0730`…`0xFF30` | yes | falls to the default arm — **do not send** |
| `0x__21` `0x__22` `0x__23` `0x__B7` `0x__CD` | no | rejected, SC `0xC5` |
| any other command byte | no (rejected by the gate first) | — |
