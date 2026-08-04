# SN200 — the raw NAND write/erase command family, and what must never be sent

Static analysis only. No drive was touched, no command was issued, and nothing in
this document is a suggestion to issue one. Firmware `KNGND122`, `PROC8`
(`~/sn200fw/flat/PROC8_7ff80000.bin` + `PROC8_30000000.bin`), tools
`tools/sn200-fw/{disany,xdis,whichfunc}.py`.

Labels follow the house convention: **PROVEN** (read directly off the
instruction stream), **INFERRED** (follows from proven facts plus one
assumption, named), **UNDETERMINED** (could not be established statically —
stated as such rather than guessed).

---

## 1. Summary / scope

`docs/sn200-attack-surface.md` §4.4 left one question open: *does any latched-drive
reachable vendor command reach a NAND page program or block erase?* It does.

**The answer is yes, and it is worse than §4.4 supposed.** The raw write/erase
family is not a hidden sub-sub value under `0xCA`/`0x03`. It is reached by two
**distinct top-level `0xCA` command bytes — `0x0F` (block erase) and `0x10`
(raw page write / NAND page program)** — and *both* are members of the 12-entry
Post-Crash allow sub-list. On a latched drive, a single well-formed admin
command erases a physical NAND block.

Three secondary results fell out:

- **The CDW-selector disagreement of §1.3 is settled: `ctx+0x38` is `CDW12[7:0]`
  and `ctx+0x39` is `CDW12[15:8]`.** WD's `libdmi` was right; both prior static
  readings (CDW8/PRP2 and CDW10) were wrong, and the reason they were wrong is
  now identifiable (§4).
- **`CDW13` carries the raw physical flash address** for the whole raw-flash
  family — PROVEN from the firmware's own `%08x` log arguments.
- A *third* raw write/erase surface exists — `0xCA` cmd `0x37`, "VUC Multiplane
  Write / Multiplane Erase" — but `0x37` is **not** allow-listed, so it is
  unreachable while latched.

### 1.1 The technique that made this tractable

Overlay code is linked at `0x300xxxxx` and executes from `0x7ffbc000`. Overlay
descriptors live at `[0x7ff8197c] = 0x7ff81af4` in **PROC8's main image**
(`PROC8@7ff80000`), 16-byte records `{load_addr, size, ddr_src, 0}`, two per
overlay (text at `0x7ffbc000`, rodata at `0x7ff9f000`). Overlay *n* is the
*n*-th text record, 1-based.

| overlay | descriptor | ddr_src | size | relocation |
|---|---|---|---|---|
| 26 | `0x7ff81e14` | `0x30035378` | `0x2940` | `static = 0x30035378 + (rt − 0x7ffbc000)` |
| **31** | `0x7ff81eb4` | `0x3003bcb8` | `0x20c0` | `static = 0x3003bcb8 + (rt − 0x7ffbc000)` |

**PROVEN.** Two independent self-checks confirm the overlay-31 constant:

1. `0x3003d603` loads literal `0x3003beb8 = 0x7ffbd93b`; relocated that is
   `0x3003bcb8 + 0x193b = 0x3003d5f3`, which is exactly the instruction the
   surrounding yield resumes at.
2. Every `call8` inside overlay 31 is PC-relative and therefore encodes a
   *runtime* target. Adding `0x7ffbc000 − 0x3003bcb8 = 0x4FF80348` to each
   naively-computed target lands all seven of them in PROC8's main image, e.g.
   `call8 0x30034260` → `0x7ffb45a8`, which is the log function called by name
   from a hundred sites in the main image. A wrong constant would scatter them.

**Consequence for anyone reading overlay code: a `call8`/`j` target printed by
`disany.py` inside overlay 31 is wrong by `−0x4FF80348` unless the callee is in
the same overlay.** §4.4's earlier attempt read those raw and got nowhere.

---

## 2. Full command family map (task 1)

### 2.1 How a `0xCA` command is routed — PROVEN

`0x7ffa75e3`, in PROC8's main image:

```asm
7ffa75e3: l8ui  a12,a12,0x38          ; ctx+0x38 = CDW12[7:0]   ("cmd" byte)
7ffa75e6: { movi a15,30 ; movi a14,29 }
7ffa75ee: { movi a11,28 ; movi a13,27 ; movi a8,67 }
7ffa75f6: { movi a10,26 ; bgeu a12,a8,0x7ffa78e3 }   ; cmd >= 67 -> reject
7ffa75fe: l32r  a9,0x7ffa0e44         ; = 0x7ffa760e, jump-table base
7ffa7601: addx2 a8,a12,a12            ; 3 * cmd
7ffa7604: add.n a8,a8,a9
7ffa7606: { movi a9,31 ; mov a0,a8 }  ; -> jx a0, 3-byte j slots at 0x7ffa760e
```

Each slot's target does two stores and jumps to the common tail:

```asm
7ffa7815: l32i.n a12,a6,0x0
7ffa7817: l32r   a13,0x7ffa0ea8       ; = 0x7ffbd904, handler PC (runtime)
7ffa781a: { s32i a9,a12,0x20 ; j 0x7ffa6e89 }   ; a9 = 31 = overlay id
```

`[obj+0x20]` is the **overlay id**, and the handler PC travels separately — the
mechanism §6.1 of the attack-surface doc already described. `0x7ffbd904`
relocated through overlay 31 is `0x3003d5bc`.

The full recovered table (67 entries, index = `CDW12[7:0]`):

| cmd | overlay | handler (static) | latched? | what it is |
|---|---|---|---|---|
| `0x00` | 26 | `0x30035680` | – | |
| `0x01` | 26 | `0x30036494` | – | `VUC_FlashRead` data-format variant |
| `0x02` | 28 | `0x30038860` | **allowed** | `Admin_VucFlashUID_OVL028` (read) |
| `0x03` | 26 | `0x30036e28` | **allowed** | raw page **READ** (StrId 1869–1872) |
| `0x04` | 29 | `0x3003a2e4` | **allowed** | (no destructive strings) |
| `0x05`,`0x06` | – | `0x7ffa789f`/`0x7ffa7894` | – | inline, no overlay |
| `0x07` | – | reject | – | |
| `0x08` | 26 | `0x30035968` | **allowed** | `Admin_VucFlashVirtualToPhysical` |
| `0x09` | 32 | `0x3003de04` | – | |
| `0x0A` | 32 | `0x3003e050` | – | |
| `0x0B` | 30 | `0x3003b8a4` | – | erase-count scan (telemetry) |
| `0x0C` | 30 | `0x3003b73c` | – | |
| `0x0D` | 28 | `0x30039100` | **allowed** | `Admin_VucFlashReset_OVL028` |
| `0x0E` | 28 | `0x300392d4` | **allowed** | `Admin_VucFlashReadStatus_OVL028` |
| ☠ `0x0F` | **31** | **`0x3003dbe0`** | **allowed** | **NAND block ERASE** (StrId 3465) |
| ☠ `0x10` | **31** | **`0x3003d5bc`** | **allowed** | **raw page WRITE / NAND page PROGRAM** (StrId 1875–1878, 3464) |
| `0x11` | 26 | `0x300359e8` | **allowed** | 4-instruction stub, `CDW13` → helper |
| `0x12` | 30 | `0x3003b680` | – | |
| `0x13` | 28 | `0x300393ec` | **allowed** | flash UID length |
| `0x14`–`0x1F` | – | reject | – | |
| `0x20` | 30 | `0x3003b25c` | – | |
| `0x21` | 30 | `0x3003b020` | **allowed** | (no log strings) |
| `0x22` | 27 | `0x30037da0` | – | |
| `0x25` | 27 | `0x30037fb4` | – | |
| `0x26` | 27 | `0x300382a0` | – | |
| `0x23`,`0x24`,`0x27`–`0x31` | – | reject | – | |
| `0x32` | 29 | `0x30039740` | **allowed** | BlockMgr calibration trigger |
| `0x33` | 28 | `0x30038c44` | – | |
| `0x34` | 29 | `0x300399fc` | – | |
| `0x35`,`0x36` | 26 | `0x30036414`,`0x30036454` | – | |
| ☢ `0x37` | 26 | `0x30035a04` | *not* allowed | **Multiplane Write + Multiplane Erase** (StrId 2958, 2959, 3330, 3454) |
| `0x38`–`0x3B` | 26 | `0x300377d8`…`0x30037b58` | – | |
| `0x3C`,`0x3D` | – | reject | – | |
| `0x3E`,`0x3F`,`0x40` | 26 | `0x30037348`,`0x3003761c`,`0x30036aec` | – | |
| `0x41` | 28 | `0x30039084` | – | |
| `0x42` | 29 | `0x30039be0` | – | |
| ≥ `0x43` | – | `bgeu a12,67` → reject | – | bounded by construction |

All of the above is **PROVEN** — the table is decoded mechanically, and the
`0x03` row independently validates the method: it lands on `0x30036e28`, whose
prologue is

```asm
30036e28: entry a1,0x50
30036e2b: l32r  a7,0x300353e4
30036e2e: l32i.n a15,a2,0x18          ; saved resume PC
30036e3b: { l32i a5,a2,0x174 ; beqz a15,0x3003726e }   ; first entry -> 0x3003726e
30036e43: jx    a15
```

i.e. the already-known raw-read dispatch at `0x3003726e`, whose own strings name
the encoding `0xCA/0x03/0x01`.

### 2.2 `0xCA` cmd `0x10` — raw page write / NAND program — PROVEN

Coroutine entry `0x3003d5bc` (overlay 31). Initial state runs at `0x3003db19`:

```asm
3003db19: addi   a6,a5,64             ; a5 = coroutine obj, so ctx = obj+0x100
3003db1c: l8ui   a12,a6,0xf9          ; obj+0x139 = ctx+0x39 = CDW12[15:8]
3003db1f: beqz.n a12,0x3003db57       ; sub 0
3003db21: beqi   a12,1,0x3003db38     ; sub 1
3003db24: beqi   a12,2,0x3003db38     ; sub 2
3003db27: l32r   a10,=0xc0040000      ; else: SCT 0 / SC 0x02 Invalid Field, DNR+M
3003db30: { s32i a9,a5,0x160 ; j 0x3003d936 }
```

After the host→DDR data-in transfer completes, the second-level split:

```asm
3003d744: l8ui a9,a6,0xf9
3003d747: { extw ; beqi a9,2,0x3003d5f3 }      ; sub 2 -> result-fetch branch
...
3003d75c: call8 0x30033f84                     ; = rt 0x7ffb42cc, acquire flash-op lock
3003d75f: beqz  a10,0x3003d80b
3003d80b: l8ui  a9,a6,0xf9
3003d80e: bnez  a9,0x3003daa4                  ; sub 1 -> ProgNANDPageRaw
                                               ; sub 0 -> falls through to WritePageRaw
```

and the four log sites, each `na=1` with the flash address as the sole argument:

| site | literal | StrId | string |
|---|---|---|---|
| `0x3003d7b6` | `0x3003becc` | 1875 | `VUC Flash_WritePageRaw SLC, phy FlashAddress: 0x%08x` |
| `0x3003d9a8` | `0x3003bef0` | 1876 | `VUC Flash_WritePageRaw MLC, phy FlashAddress: 0x%08x` |
| `0x3003dad1` | `0x3003bf04` | 1877 | `VUC Flash_ProgNANDPageRaw SLC, phy FlashAddress: 0x%08x` |
| `0x3003d645` | `0x3003bec0` | 1878 | `VUC Flash_ProgNANDPageRaw MLC, phy FlashAddress: 0x%08x` |
| `0x3003d96d` | `0x3003beec` | 3464 | `VUC Flash_ProgNANDPage result dword 0: 0x%08x` |

SLC vs MLC is chosen by the boolean register `b0` at `0x3003d7b0` / `0x3003dacb`
(`bt b0`), not by `CDW12[15:8]`.

### 2.3 `0xCA` cmd `0x0F` — NAND block erase — PROVEN

Coroutine entry `0x3003dbe0` (overlay 31):

```asm
3003dbe0: entry  a1,0x40
3003dbe3: l32i.n a9,a2,0x18                     ; resume PC
3003dbe5: movi.n a7,-1
3003dbe7: { l32i a5,a2,0x13c ; beqz a9,0x3003dd1b }   ; a5 = ctx+0x3c = CDW13 = flash address
3003dbef: jx     a9
...
3003dd1b: mov.n a10,a5
3003dd1d: call8 0x30033d00
3003dd20: { extw ; beqi a10,1,0x3003dbf2 }      ; -> do it
3003dd28: l32r  a10,=0xc0040000                 ; else Invalid Field
...
3003dbf2: addi  a6,a2,56 ; movi a10,1 ; mov a11,a6
3003dbfd: call8 0x30033f84                      ; acquire flash-op lock
3003dc2c: mov.n a10,a5 ; call8 0x30033c04 ; call8 0x30033d8c
3003dc37: l32r  a10,0x3003bf14                  ; StrId 3465, na=2
          ;   "VUC Erase BlkType:%d (1-SLC; 0-MLC), FlashAddress: 0x%08X"
3003dc47: call8 0x30034260                      ; = rt 0x7ffb45a8, the logger
```

**There is no `CDW12[15:8]` dispatch on the erase path.** An exhaustive scan of
the whole overlay-31 text for `l8ui rX,rY,{0x38,0x39,0xf8,0xf9}` finds only
`0x3003bf47`, `0x3003c4d7`, `0x3003c83c`, `0x3003d744`, `0x3003d80b`,
`0x3003db1c`, `0x3003db99` — every one of them outside `0x3003dbe0-0x3003dd38`.
**PROVEN: for cmd `0x0F`, `CDW12[15:8]` is ignored; every value of it erases.**

The only host fields the erase coroutine reads are `ctx+0x3c` (`CDW13`, flash
address), `ctx+0x1a` (CID) and `ctx+0x1c` (NSID, error path only).

### 2.4 The family table

| opcode | `CDW12[7:0]` | `CDW12[15:8]` | other selector-bearing field | StrId | class | confidence |
|---|---|---|---|---|---|---|
| `0xCA` | `0x03` | `0x00` | – | 1873 | reject (`Invalid field in cmd`) | PROVEN |
| `0xCA` | `0x03` | `0x01` | `CDW13` = flash addr | 1869/1871 | **read** (`Flash_ReadRawData`) | PROVEN (pre-existing) |
| `0xCA` | `0x03` | `0x02` | `CDW13` = flash addr | 1870/1872 | **read** (`Flash_ReadCacheData`) | PROVEN (pre-existing) |
| `0xCA` | `0x03` | ≥`0x03` | – | 1873 | reject | PROVEN |
| ☠ `0xCA` | **`0x0F`** | **ignored — any value** | `CDW13` = flash addr | 3465 | **ERASE** — NAND block erase, SLC or MLC | PROVEN |
| ☠ `0xCA` | **`0x10`** | `0x00` | `CDW10` = len (dwords), `CDW13` = flash addr, data-out | 1875/1876 | **WRITE** — `Flash_WritePageRaw` (SLC/MLC) | PROVEN |
| ☠ `0xCA` | **`0x10`** | `0x01` | `CDW10` = len (dwords), `CDW13` = flash addr, data-out | 1877/1878 | **WRITE** — `Flash_ProgNANDPageRaw` (SLC/MLC) | PROVEN |
| ⚠ `0xCA` | **`0x10`** | `0x02` | – | 3464 | **other** — fetch `Flash_ProgNANDPage` result dword | PROVEN |
| `0xCA` | `0x10` | ≥`0x03` | – | – | reject, SC `0x02` DNR | PROVEN |
| ☢ `0xCA` | `0x37` | undetermined | `CDW13` = flash addr | 2958/2959/3454 | **WRITE + ERASE** (multiplane) | PROVEN that the strings are in this handler; sub-selector not analysed |

`CDW13` = physical flash address is **PROVEN** for cmd `0x10` and `0x0F`: the
value fetched by `l32i a7,a5,0x13c` (`0x3003d5e0`) / `l32i a5,a2,0x13c`
(`0x3003dbe7`) is the *sole* `%08x` argument passed to the loggers for StrIds
1875–1878 and the second argument for 3465, and those format strings say
`phy FlashAddress`. That `obj+0x13c` is `CDW13` is **INFERRED** from the proven
`CDW10 = obj+0x130` / `CDW11 = obj+0x134` anchoring (§4) plus contiguity.

**UNDETERMINED, and recorded as such:**

- What selects SLC vs MLC on cmd `0x10` (`b0` at `0x3003d7b0`) and what the
  `BlkType` argument to StrId 3465 is read from. The setters sit in FLIX slot-B
  classes `xdis.py` renders as `?B`/`?Balu`. **Do not assume one value is
  "the safe one".**
- Whether cmd `0x10` has an absolute length clamp. The only bound observed is a
  *consistency* check, `0x3003db7f`–`0x3003db88`:
  `l32i a15,a6,0xf0 ; l32i a14,a6,0x14c ; slli a15,a15,2 ; bne a14,a15 → error`,
  i.e. `CDW10 * 4` must equal the bytes actually transferred. That is not the
  raw-read path's `minu a10,a10,640` clamp.
- The `0x37` multiplane sub-selector space. Not analysed — `0x37` is not
  latched-reachable, so it was out of scope, and it is **not** an invitation to
  go find out on hardware.

**Dead strings, corrected from §4.4:** StrIds **1879** (`VUC Erase SLC block`)
and **1880** (`VUC Erase MLC block`) have **no log descriptor word anywhere in
any of the 18 processor images** and are therefore emitted by no code path in
`KNGND122`. The live erase logger is StrId 3465. §4.4 listed 1879/1880 as part
of this family; in this revision they are vestigial.

---

## 3. Latched-drive reachability (task 2) — **verdict: reachable**

### 3.1 The gate reads the same byte the dispatcher indexes — PROVEN

`Admin_CheckCmdAllowed` is `0x7ffa6b18`. It has exactly one caller, `0x7ffa7244`,
and the argument set-up is unambiguous:

```asm
7ffa722a: l32i.n a13,a1,0x24
7ffa722e: l8ui   a11,a13,0x18                      ; -> callee a3 = CDW0[7:0]  (opcode)
7ffa7231: { l8ui a12,a13,0x38 ; movi a14,1 ; movi a10,0 }   ; -> callee a4 = ctx+0x38
7ffa7241: l8ui   a13,a13,0x39                      ; -> callee a5 = ctx+0x39
7ffa7244: call8  0x7ffa6b18
```

(`call8` shifts the window by 8: callee `a2..a5` = caller `a10..a13`.)

Inside the gate:

```asm
7ffa6b18: entry a1,0x20
7ffa6b1b: l32r  a8,0x7ffa09b0        ; = 0x7ff87c64, the mode word
7ffa6b1e: l32i.n a8,a8,0x0
7ffa6b30: { movi a13,198 ; bnei a8,6,0x7ffa6bd9 }  ; only mode 6 (Post-Crash) is gated
...
7ffa6bb3: movi a9,202                              ; 0xCA
7ffa6bb6: { extw ; beq a3,a9,0x7ffa6d76 }          ; -> the 12-entry sub-list on a4
```

and the sub-list at `0x7ffa6d76` compares **`a4`, which is `ctx+0x38`**, against
`{0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32}` —
re-verified here instruction by instruction, identical to §2.3 of the
attack-surface doc.

### 3.2 Verdict

The gate's allow-list and the dispatcher's jump-table index are **the same
byte**, `CDW12[7:0]`. Therefore:

> **PROVEN: on a latched (mode-word == 6, Post-Crash) SN200, `0xCA` with
> `CDW12[7:0] = 0x0F` reaches a raw NAND block erase, and `CDW12[7:0] = 0x10`
> reaches a raw NAND page write / page program. Neither is blocked by
> `Admin_CheckCmdAllowed`. Both take the physical flash address from `CDW13`.**

This is *not* the "some unknown sub-sub of `0xCA`/`0x03`" hazard §4.4 feared —
`0xCA`/`0x03` really is read-only, and its sub-sub space really is `{0,1,2}`
with everything else rejected. The hazard is one and two digits away in the
*command* byte, in a numeric neighbourhood the operator is otherwise encouraged
to use: `0x0D`, `0x0E`, `0x11`, `0x13` are all benign, allow-listed, and adjacent.

Reassuring parts of the same analysis:

- `0x37` (Multiplane Write / Multiplane Erase) is **not** allow-listed — the
  gate rejects it while latched. **PROVEN.**
- A per-handler sweep of every log-string literal reachable inside each of the
  twelve allow-listed handlers' code extents (bounded by the next handler entry
  *and* by the overlay boundary) finds destructive strings only under `0x0F` and
  `0x10`. `0x02`, `0x03`, `0x08`, `0x0D`, `0x0E`, `0x13` are flash **read** /
  reset / status / UID paths; `0x04`, `0x11`, `0x21`, `0x32` carry no
  destructive strings. **INFERRED, high confidence** — absence of a log string
  is weaker evidence than presence of one, and `0x11` and `0x21` contain no log
  strings at all, so they are *unaudited*, not *proven clean*.
- The `bgeu a12,67` bound at `0x7ffa75f6` runs *before* the table index is
  formed, so there is no out-of-table dispatch. **PROVEN.**

---

## 4. The CDW-selector question (task 3) — **RESOLVED: `ctx+0x38` is `CDW12[7:0]`**

§1.3 of the attack-surface doc recorded three mutually inconsistent answers and
declined to pick one. It can now be closed, and the *reason* the two static
readings were wrong can be stated.

### 4.1 The parsed-command struct starts at `ctx+0x18` and holds `CDW0` verbatim — PROVEN

Two independent anchors, two bytes apart:

```asm
7ffa725f: l32i.n a9,a1,0x24
7ffa7261: l32i.n a9,a9,0x18
7ffa7263: extui  a10,a9,14,2       ; bits 14-15 -> PSDT       (NVMe CDW0[15:14])
7ffa726e: { extui a11,a9,0,8 ; movi a12,20 }        ; bits 0-7 -> opcode
7ffa7276: { movi a14,12 ; bltui a11,128,0x7ffa7c0d } ; the 0x80 vendor split
```

and, in the erase coroutine's completion path,

```asm
3003dd28: l32r   a10,=0xc0040000    ; status, SCT/SC in bits 31..17
3003dd2b: l16ui  a9,a2,0x11a        ; ctx+0x1a, 16-bit
3003dd2e: or     a9,a9,a10
3003dd31: { s32i a9,a2,0x160 ; ... }   ; completion dword
```

`status | 16-bit field` written as one dword is CQE DW3 = `CID | P | status`, so
`ctx+0x1a` is the **CID**, which lives in `CDW0[31:16]` = SQE bytes 2–3.
`ctx+0x18` therefore *is* SQE byte 0. **PROVEN.**

### 4.2 …but the struct is **not** a verbatim 64-byte SQE — PROVEN

That is the trap. Both previous static readings assumed "CDW0 at `ctx+0x18`
⟹ SQE verbatim at `ctx+0x18`", which places `CDW10` at `ctx+0x40` and makes
`ctx+0x38` PRP2 ("CDW8"). The firmware disagrees, and a **spec-defined** command
proves it.

Firmware Image Download is opcode `0x11`; NVMe defines `CDW10 = NUMD` and
`CDW11 = OFST`. Its handler is the coroutine at `PROC8@30000000 0x30025590`
(`entry a1,0x40 ; l32i.n a11,a2,0x18 ; … ; beqz a11,0x3002578d` — same
`[obj+0x18]`-resume shape, so `a2` is the coroutine object throughout), and
`a2` is still the object at the bounds check:

```asm
300257e5: l32i  a10,a2,0x134         ; OFST
300257e8: l32r  a11,=0x00400000      ; 4 MiB
300257eb: slli  a14,a10,2
300257ee: bltu  a11,a14,0x30025820   ; -> StrId 2177 "Firmware Download Invalid size exceeds DDR allocation"
300257f1: l32i  a15,a2,0x130         ; NUMD
300257f4: { add a15,a15,a10 ; movi a8,4 }
```

Identity of the handler is nailed by its own strings: StrId 2177
(*"Firmware Download Invalid size exceeds DDR allocation"*), 2182
(*"Firmware Download Image NUMD: %d OFST: %d"*), 2183
(*"Firmware Download Host to DDR transfer failed"*).

Therefore **`obj+0x130` = `ctx+0x30` = `CDW10`, `obj+0x134` = `ctx+0x34` =
`CDW11`, and `obj+0x138` = `ctx+0x38` = `CDW12`.** **PROVEN.**

The struct is compacted: `CDW0` at `+0x18`, `NSID` at `+0x1c`, four dwords of
pointer/PRP material at `+0x20`–`+0x2f`, then `CDW10`…`CDW15` at
`+0x30`…`+0x44`. Which four dwords occupy `+0x20`–`+0x2f` is **UNDETERMINED**
and does not matter here.

### 4.3 Three further confirmations, all internal

1. The raw-read family's own strings say the encoding is `0xCA/0x03/0x01` and
   `0xCA/0x03/0x02`. §2.1 proves `0x03` is the value of `ctx+0x38` and
   `0x01`/`0x02` the values of `ctx+0x39`. WD's `libdmi` and the nvme-cli WDC
   plugin build that triple as opcode + `CDW12[7:0]` + `CDW12[15:8]`. Independent
   agreement with §4.2.
2. `0x30030a44`: `l32i a10,a2,0x130 ; slli a15,a10,2 ; minu a15,a7,a15` — a
   dword count scaled to bytes, exactly what `CDW10` is on these vendor reads.
3. `0x3003db7f`–`0x3003db88` in the write coroutine: `l32i a15,a6,0xf0` where
   `a6 = obj+0x40`, i.e. `obj+0x130` = `CDW10`, shifted left 2 and compared
   against the byte count actually DMA'd. A transfer-length field, in the field
   §4.2 says is `CDW10`.

### 4.4 What this changes

`ctx+0x38` = `CDW12[7:0]`, `ctx+0x39` = `CDW12[15:8]`. **The `CDW10`-selector
scare in §1.3 is retired**: `CDW10` on `0xCA`/`0xFF`/`0xC6` is a length, not a
selector, and a command with `CDW12 = 0` is *not* secretly carrying an OAM
sub-command in `CDW10`.

That said, **`CDW13` is now known to be selector-grade dangerous in its own
right** — it is the raw physical flash address for the whole raw-flash family.
The operational rule in §5 is therefore *changed in shape*, not relaxed.

---

## 5. Never send this (task 4)

Everything from `sn200-attack-surface.md` §7 still stands. The following is
**added**, and it is the most dangerous entry in the whole set because it
requires no crash, no unlock, and no special drive state.

| ☠/☢ | command | effect |
|---|---|---|
| ☠ | `0xCA`, **`CDW12[7:0] = 0x0F`**, any `CDW12[15:8]`, `CDW13` = anything | **NAND block erase** at the physical address in `CDW13`. Reachable on a latched drive. Unrecoverable data loss. `CDW12[15:8]` is *ignored* — there is no "harmless sub-value". |
| ☠ | `0xCA`, **`CDW12 = 0x0010`** (`CDW12[7:0]=0x10`, `[15:8]=0x00`), `CDW13` = anything | `Flash_WritePageRaw` — raw page **write**, including spare/ECC bytes as supplied by the host. Corrupts the page and its metadata. Reachable while latched. |
| ☠ | `0xCA`, **`CDW12 = 0x0110`** | `Flash_ProgNANDPageRaw` — issues the NAND **program** command. Same reachability, same consequence. |
| ⚠ | `0xCA`, **`CDW12 = 0x0210`** | Fetches the `Flash_ProgNANDPage` result dword. Does not itself program, but it is one keystroke from `0x0010`/`0x0110` and shares the entry coroutine. |
| ☢ | `0xCA`, **`CDW12[7:0] = 0x37`** | Multiplane **Write** and Multiplane **Erase** (StrIds 2958, 2959, 3454). Blocked by the Post-Crash gate, live on a healthy drive. Sub-selector unanalysed. |

**The adjacency problem, restated for this family.** `0x0F` (erase) and `0x10`
(write) sit between `0x0E` (`Admin_VucFlashReadStatus`, benign) and `0x11`
(virtual→physical translate, benign) — the exact range an operator walks while
poking at flash state. `0x0F` and `0x10` are also `15` and `16` decimal, which is
how they will appear in a loop counter. **Never iterate `CDW12[7:0]`. Never
write a script that can emit a `0xCA` command byte it was not explicitly given.**

### Which CDWs to treat as selector-bearing

Superseding the §1.3 caveat, which can now be narrowed:

| field | status |
|---|---|
| `CDW12[7:0]` | **selector** — the `0xCA` command byte, and the `Admin_CheckCmdAllowed` sub-list key. PROVEN. |
| `CDW12[15:8]` | **selector** — the sub-command, where the handler reads it. PROVEN. Note cmd `0x0F` ignores it. |
| `CDW12[31:16]` | not read by any path examined. UNDETERMINED — zero it. |
| `CDW13` | **not a selector but equally dangerous** — physical flash address for the raw-flash family. PROVEN for cmds `0x0F`/`0x10`, and for `0x03` by the pre-existing read analysis. |
| `CDW10` | **length in dwords**, not a selector. PROVEN (§4.2/§4.3). |
| `CDW11` | offset in dwords on the paths examined. Not a selector. |

**Operational rule.** A vendor command is safely inert only if `CDW10`, `CDW11`,
`CDW12` **and `CDW13`** are all zero. The old rule said `CDW10`/`CDW11`/`CDW12`;
`CDW13` is now known to carry a raw physical address into an erase path, so it
joins the list. Zeroing `CDW13` does not make `CDW12[7:0] = 0x0F` safe — it
merely erases block zero instead of a block of your choosing.

**Not a recommendation to probe anything.** This document names `0xCA` command
bytes `0x04`, `0x11`, `0x21`, `0x32` as allow-listed and unidentified, and `0x37`
as a multiplane write/erase with an unmapped sub-selector. Naming them is not an
invitation. Every one of them is a neighbour of a command that destroys a block.

---

## 6. Open questions

1. **SLC vs MLC selection on cmd `0x10`, and the `BlkType` source on cmd `0x0F`.**
   Both hinge on FLIX slot-B classes `xdis.py` cannot decode (`?B`, `?Balu`).
   `docs/sn200-attack-surface.md` §6.4 already flags this as the highest-value
   decoder fix; it is now blocking a safety-relevant detail, not just tidiness.
2. **`0xCA` cmd `0x11` and `0x21` are allow-listed and carry no log strings at
   all** — they are unaudited, not proven benign. `0x11`'s handler is four
   instructions (`l32i a10,a2,0x13c ; call8 0x3002d2c4 ; s32i a10,a2,0x154`),
   so it hands `CDW13` — a raw flash address — to a helper in the main image
   (`rt 0x7ffa9...`, unresolved) and returns a dword. Worth closing.
3. **Whether cmd `0x10` has any absolute transfer-length bound.** Only the
   `CDW10*4 == bytes_transferred` consistency check was found. The raw-*read*
   family clamps to 640 bytes; no analogue was located on the write path.
4. **The `0x37` multiplane sub-selector space.** Out of scope here because `0x37`
   is not latched-reachable, but it is a live write/erase surface on a healthy
   drive and nobody has mapped it.
5. **Four dwords at `ctx+0x20`–`ctx+0x2f`.** Presumed PRP1/PRP2; not verified.
   Nothing in this document depends on it.
6. **Why StrIds 1879/1880 exist with no referencing code.** Most likely a
   previous revision's erase logger. Harmless, but it means string-table
   presence is not evidence of a reachable code path — a lesson worth keeping.

---

## 7. Reproducing this

```sh
export SN200_FW=~/sn200fw
# overlay descriptor table (in PROC8's MAIN image, not the overlay bank)
python3 tools/sn200-fw/disany.py PROC8@7ff80000 7ff81af4 7ff81f00

# the 0xCA cmd-byte dispatcher and its 67-entry jump table
python3 tools/sn200-fw/disany.py PROC8@7ff80000 7ffa75e3 7ffa78f8

# the Post-Crash allow gate and its single call site
python3 tools/sn200-fw/disany.py PROC8@7ff80000 7ffa6b18 7ffa6db4
python3 tools/sn200-fw/disany.py PROC8@7ff80000 7ffa7200 7ffa7260

# cmd 0x10 (write/program) and cmd 0x0F (erase), overlay 31
python3 tools/sn200-fw/disany.py PROC8@30000000 3003d5bc 3003dbe0
python3 tools/sn200-fw/disany.py PROC8@30000000 3003dbe0 3003dd39

# the CDW10/CDW11 anchor: Firmware Image Download
python3 tools/sn200-fw/disany.py PROC8@30000000 30025590 300255c0
python3 tools/sn200-fw/disany.py PROC8@30000000 300257d0 30025800
```

Reminder for anyone continuing: `disany.py PROC8` with no `@base` silently loads
`PROC8_30000000.bin`. Always write `PROC8@7ff80000` or `PROC8@30000000`. And run
`whichfunc.py` before disassembling from any address — the confirmed-function map
is *incomplete* in the overlay bank (it truncates `0x3003d5bc` at `0x3003d771`,
mid-coroutine), so a "not inside any known function" answer there means
"unmapped", not "not code".
