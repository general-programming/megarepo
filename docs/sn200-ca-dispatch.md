# `0xCA` — the VUC Flash family, executed

`0xCA` is where the genuinely destructive SN200 commands live: a 67-entry jump
table on `CDW12[7:0]`, twelve of whose command bytes a **latched** drive still
accepts, two of which destroy a physical NAND block on one well-formed command.
Until now every statement about it was somebody's read of a byte stream.

This document is the result of **running PROC8's own dispatch**, the way
`sn200-oam-dispatch.md` was re-derived for `0xFF`. Everything in §2–§5 is
produced by `sn200_oracle.py --ca` and re-derived by `tests/test_oracle.py` on
every run:

```sh
SN200_FW=~/sn200fw .venv/bin/python tools/sn200-fw/sn200_oracle.py --ca
SN200_FW=~/sn200fw .venv/bin/python tools/sn200-fw/sn200_oracle.py --ca --danger
SN200_FW=~/sn200fw .venv/bin/pytest tools/sn200-fw/tests/test_oracle.py -q
```

**No hardware was touched.** Static analysis and p-code emulation only.

Labels: **PROVEN** = executed, or read off correctly-decoded instructions.
**INFERRED** = short chain over proven facts, with the assumption named.
**UNKNOWN** = not established, and said so.

Companions: `sn200-pcode-toolchain.md` (what the lifter can and cannot do),
`sn200-opcode-map.md` (the whole admin dispatch), `sn200-dangerous-commands.md`
(the original hand teardown of `0x0F`/`0x10`), `sn200-vuc-flash-read.md`
(`0x00`–`0x03`), `sn200-command-reference.md` (**the document you act from**).

---

## 1. How the family is dispatched, executed — PROVEN

`0x7ffa75e1` reads `ctx+0x38` (`CDW12[7:0]`), bounds it against 67, forms
`base + 3*cmd` over a table of three-byte `j` slots at `0x7ffa760e`, and jumps.
Each arm loads a **runtime** handler pointer into the `0x7ffbc000` overlay
window, stores an **overlay index** at `request+0x20`, and falls into the
common enqueue at `0x7ffa6e89`.

`ca_dispatch()` runs exactly that, once per command byte, and reads the answer
back out of the store and the register the arm left it in. Three things the
executed version gets right that a hand read repeatedly did not:

- **The indirect jump is supplied, not guessed.** The `jx` sits in a FLIX slot
  the spec does not decode, so the target is taken from `a0` — which the
  *executed* `addx2`/`add.n`/`l32r` sequence computed — and then entered. The
  oracle asserts `a0 == 0x7ffa760e + 3*cmd` rather than assuming it.
- **The handler register is not fixed.** Arms `0x22`, `0x25`, `0x26` and `0x32`
  park the pointer in `a9` where the other 33 use `a13`. Naming a register
  would have silently lost four rows.
- **A runtime pointer does not identify code.** `0x33` and `0x37` load the
  *same* pointer `0x7ffbc68c` and differ only in the overlay index — 28 versus
  26, i.e. the wafer-lot-ID reader versus the multiplane write/erase handler.
  Both halves have to be read, and the static address resolved as
  `src2(overlay) + (runtime − 0x7ffbc000)`.

### 1.1 Two counts that were wrong

**39 of 67 entries are implemented, not 37.** `sn200-opcode-map.md` §3 says 37;
its own table lists 39 rows. The two extra are `0x05` and `0x06`, the inline
arms that load no overlay handler at all and were dropped from the count.

**The twelve gate-surviving command bytes are all implemented** —
`{0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32}`,
executed against `ca_table()`. The allow-list contains no dead value.

---

## 2. The complete family, executed

`latched` = admitted by `Admin_CheckCmdAllowed` on a Post-Crash drive. `class`
is defined in §2.1. `body` is the range attributed to the handler, from its
entry to the next handler entry in the same overlay (§2.2).

| `CDW12[7:0]` | latched | class | identity | ovl | runtime → static |
|---|---|---|---|---|---|
| `0x00` | no | READ-ONLY | `Admin_VucFlashLogicalToPhysical` | 26 | `0x7ffbc308` → `0x30035680` |
| `0x01` | no | READ-ONLY | `Admin_VucFlashRead` — one LBA through the L2P | 26 | `0x7ffbd11c` → `0x30036494` |
| `0x02` | **yes** | READ-ONLY | `Admin_VucFlashUID_OVL028` — flash UID / UID length | 28 | `0x7ffbc2a8` → `0x30038860` |
| `0x03` | **yes** | READ-ONLY | raw NAND page read, **clamped to 640 bytes** | 26 | `0x7ffbdab0` → `0x30036e28` |
| `0x04` | **yes** | **UNKNOWN** | no strings; **takes the flash-op lock** | 29 | `0x7ffbccec` → `0x3003a2e4` |
| `0x05` | no | UNKNOWN | inline: `0x7ffa915c(1)`, `0x7ffa9168`, result → `req+0x154` | — | resident `0x7ffa761d` |
| `0x06` | no | UNKNOWN | inline: same with argument `0` | — | resident `0x7ffa7620` |
| `0x08` | **yes** | READ-ONLY | `Admin_VucFlashVirtualToPhysical` — pure arithmetic | 26 | `0x7ffbc5f0` → `0x30035968` |
| `0x09` | no | UNKNOWN | no strings | 32 | `0x7ffbc08c` → `0x3003de04` |
| `0x0A` | no | UNKNOWN | no strings | 32 | `0x7ffbc2d8` → `0x3003e050` |
| `0x0B` | no | READ-ONLY | erase-**count** / blockset census (14 strings) | 30 | `0x7ffbd32c` → `0x3003b8a4` |
| `0x0C` | no | **UNKNOWN** | no strings — *not* the census, see §2.3 | 30 | `0x7ffbd1c4` → `0x3003b73c` |
| `0x0D` | **yes** | MUTATES | `Admin_VucFlashReset_OVL028` | 28 | `0x7ffbcb48` → `0x30039100` |
| `0x0E` | **yes** | READ-ONLY | `Admin_VucFlashReadStatus_OVL028` | 28 | `0x7ffbcd1c` → `0x300392d4` |
| ☠ `0x0F` | **yes** | **DESTRUCTIVE** | **raw NAND BLOCK ERASE**, `CDW12[15:8]` ignored | 31 | `0x7ffbdf28` → `0x3003dbe0` |
| ☠ `0x10` | **yes** | **DESTRUCTIVE** | **raw NAND PAGE WRITE / PROGRAM**, no length bound | 31 | `0x7ffbd904` → `0x3003d5bc` |
| `0x11` | **yes** | **UNKNOWN** | 28-byte stub; hands `CDW13` to `0x7ffb3f4c` (§4.4) | 26 | `0x7ffbc670` → `0x300359e8` |
| ⚠ `0x12` | no | **DESTRUCTIVE** | `VUC_ERASE_PWR_CHAR` — this arm erases blocks | 30 | `0x7ffbd108` → `0x3003b680` |
| `0x13` | **yes** | **UNKNOWN** | no strings; **takes the flash-op lock** | 28 | `0x7ffbce34` → `0x300393ec` |
| `0x20` | no | **UNKNOWN** | no strings — *not* `ERASE_PWR_CHAR`, see §2.3 | 30 | `0x7ffbcce4` → `0x3003b25c` |
| `0x21` | **yes** | **UNKNOWN** | no strings; single callee `0x7ffb3dd0` | 30 | `0x7ffbcaa8` → `0x3003b020` |
| `0x22` | no | READ-ONLY | soft-LDPC read histogram | 27 | `0x7ffbc0e8` → `0x30037da0` |
| `0x25` | no | READ-ONLY | `Admin_VucFlashSLDPCHistoryHistogram_OVL027` | 27 | `0x7ffbc2fc` → `0x30037fb4` |
| `0x26` | no | READ-ONLY | hard-LDPC read histogram | 27 | `0x7ffbc5e8` → `0x300382a0` |
| `0x32` | **yes** | **UNKNOWN** | no strings in 700 bytes | 29 | `0x7ffbc148` → `0x30039740` |
| `0x33` | no | READ-ONLY | `Admin_VucFlashReadLotID_OVL028` (SanDisk only) | 28 | `0x7ffbc68c` → `0x30038c44` |
| `0x34` | no | READ-ONLY | VUC Get Dies Status | 29 | `0x7ffbc404` → `0x300399fc` |
| `0x35` | no | UNKNOWN | 64-byte sibling of `0x01`/`0x36`, no calls | 26 | `0x7ffbd09c` → `0x30036414` |
| `0x36` | no | UNKNOWN | 64-byte sibling of `0x01`/`0x35`, no calls | 26 | `0x7ffbd0dc` → `0x30036454` |
| ☢ `0x37` | no | **DESTRUCTIVE** | **Multiplane Write + Multiplane Erase** | 26 | `0x7ffbc68c` → `0x30035a04` |
| `0x38` | no | READ-ONLY | NAND-chip (ONFI) **Get** Features | 26 | `0x7ffbe460` → `0x300377d8` |
| ☢ `0x39` | no | **CATASTROPHIC** | NAND-chip (ONFI) **SET** Features | 26 | `0x7ffbe5b0` → `0x30037928` |
| `0x3A` | no | READ-ONLY | `Admin_VucFlashGetTestModeRegister_OVL026` | 26 | `0x7ffbe6e8` → `0x30037a60` |
| ☢ `0x3B` | no | **CATASTROPHIC** | `Admin_VucFlashSetTestModeRegister_OVL026` | 26 | `0x7ffbe7e0` → `0x30037b58` |
| `0x3E` | no | READ-ONLY | Read FuseRom / REG2SA (SanDisk only) | 26 | `0x7ffbdfd0` → `0x30037348` |
| `0x3F` | no | READ-ONLY | Read SanDisk MT (Memory Test) information | 26 | `0x7ffbe2a4` → `0x3003761c` |
| `0x40` | no | READ-ONLY | `Flash_ReadRRShiftLevel` | 26 | `0x7ffbd774` → `0x30036aec` |
| `0x41` | no | **UNKNOWN** | Permanent Die Offline list — read *or* write, §2.3 | 29 | `0x7ffbcacc` → `0x3003a0c4` |
| `0x42` | no | UNKNOWN | `AdminVucDebugVddDroopGetTransferLenth` only | 29 | `0x7ffbc5e8` → `0x30039be0` |

Not implemented, and every one of these reaches the default reject arm
`0x7ffa78e3`: `0x07`, `0x14`–`0x1F`, `0x23`, `0x24`, `0x27`–`0x31`, `0x3C`,
`0x3D`, and everything `≥ 0x43` (bounded before the index is formed).

### 2.1 What the classes mean here, and how they are weaker than `0xFF`'s

For `0xFF` the oracle walks the handler to the OAM enqueue and reads the EEPROM
verb and section the firmware itself wrote. **There is no equivalent for
`0xCA`**: these handlers do not post a request object, they call flash
primitives in the main image whose leaves are not traced. So the classes here
rest on the firmware's own log strings, which name what it is about to do:

| class | rule |
|---|---|
| DESTRUCTIVE | a string in the handler body names an erase or a raw media write (`VUC Erase`, `Multiplane Erase/Write`, `WritePageRaw`, `ProgNANDPage`, `erasure`) |
| CATASTROPHIC | a string names a write of persistent **NAND die configuration** — `Set Features addr`, `SetTestModeRegister`. INFERRED: these are not documented, bounded operations, and nothing establishes a die survives an arbitrary one |
| MUTATES | names a flash **reset** |
| READ-ONLY | names only reads, and no erase/write string appears in the body. **INFERRED, and weaker than the `0xFF` READ-ONLY**: absence of a destructive string is not proof of absence of a destructive path |
| UNKNOWN | no string in the body attributes anything. Not "safe" — see §4.4 |

`is_read_only()` deliberately **does not model `0xCA`**. Nothing in this family
is cleared to send, so there is no encoding for a tool to argue past.

### 2.2 The attribution rule, and why the obvious ones are wrong

These handlers are 26-to-120-byte coroutine trampolines whose resume bodies
follow them. Two failure modes are already on record:

- A **fixed byte window** from the entry absorbs the next two functions'
  strings. That is how a published table came to call `0xCA/0x11` a Multiplane
  Write.
- The **confirmed function extent** from `function-map.json` is far too small,
  and in this bank it is not even sound: it truncates `0x3003d5bc` at
  `0x3003d771` mid-coroutine, and it gives `0x3003dbe0` an end of `0x3003ddd2`
  which is *past the end of overlay 31*.

So the body used here is **[entry, next handler entry in the same overlay)**,
clamped to the overlay end. That is an ordering argument, not a containment
one: treat every string attribution as **INFERRED**. The sweep keeps byte
alignment across the ranges SLEIGH cannot decode by falling back to the
`op0`-determined length, the same rule `funcmap.py` relies on.

### 2.3 Three published identities that do not survive this

| claim | where | what the executed attribution says |
|---|---|---|
| `0x20` = `VUC_ERASE_PWR_CHAR` | `sn200-opcode-map.md` §3 | **wrong.** Both `VUC_ERASE_PWR_CHAR` strings are in `0x12`'s body (`0x3003b680`–`0x3003b73c`), one of them inside `0x12`'s own 44-byte confirmed extent. `0x20`'s 1060-byte body carries no string at all. `0x20` is unidentified. |
| `0x0C` = erase-count / blockset census | `sn200-opcode-map.md` §3 | **wrong.** All fourteen census strings are in `0x0B`'s body. `0x0C`'s 360-byte body carries none, and its only reachable calls are the completion helpers. |
| `0x32` = BlockMgr calibration trigger | `sn200-dangerous-commands.md` §2.1 | **unsupported.** 700 bytes, no strings. `sn200-opcode-map.md` already says UNKNOWN; that is the correct answer. |

`sn200-opcode-map.md` §3's own method warning predicted exactly these two
mislabels and then left them in the table. They are fixed here.

---

## 3. The two commands that destroy a drive — CONFIRMED

Both hand decodes we have been relying on are **correct**. Neither is wrong in
either direction. The executed versions are stronger than the reads were.

### 3.1 `0x0F` — raw NAND block erase, and there is no harmless sub-value

> **PROVEN by execution.** All **256** values of `CDW12[15:8]` produce a
> byte-identical instruction trace through the erase coroutine
> (`ca_erase_ignores_sub_byte()`), and a mechanical scan of the entire handler
> body finds the sub byte is never even addressed — no instruction anywhere in
> `0x3003dbe0`–`0x3003dd78` materialises the displacement `0x39` or `0xf9`
> (`ca_reads_sub_byte(0x0f) is False`). The single hit on the constant `0x38`
> is `addi a6,a2,0x38`, an address computation for the completion area, not a
> load of the command byte.

The hand result in `sn200-dangerous-commands.md` §2.3 — an `l8ui` scan of the
whole of overlay 31 — reached the same conclusion by a different route. Two
independent methods, same answer: **every well-formed `0xCA` with
`CDW12[7:0] = 0x0F` erases the NAND block whose physical address is in
`CDW13`.** There is nothing to type in the high byte that makes it safe.

The executed path also confirms the shape: the entry reads `CDW13` from
`ctx+0x3c`, calls the address-validity helper `0x7ffb3f4c`, requires it to
return `1`, acquires the flash-operation lock `0x7ffb42cc`, and — on the
lock-acquired arm — calls `0x7ffb3f4c` again and emits StrId 3465,
`VUC Erase BlkType:%d (1-SLC; 0-MLC), FlashAddress: 0x%08X`. When the lock is
busy it yields with the resume PC `0x7ffbdf3a` → static `0x3003dbf2`, i.e. it
retries the acquire. **A busy drive does not refuse the erase; it queues it.**

### 3.2 `0x10` — raw NAND page program, and `0x0210` is not a probe

> **PROVEN by execution.** The first-entry sub dispatch at `0x3003db1f` sends
> `CDW12[15:8] = 0` to `0x3003db57`, **1 and 2 to the same arm `0x3003db38`**,
> and everything `≥ 3` to the invalid-field tail `0x3003d936`
> (`ca_write_sub_arms()`).

That middle result is the operationally important one, and it is stronger than
what `sn200-dangerous-commands.md` §5 says. `0x0210` was described as "fetches
the result dword… but it is one keystroke from `0x0010`/`0x0110` and shares the
entry coroutine". Executed, it is worse than that: **sub 2 takes the same
first-entry arm as sub 1**, sets up the same host→DDR data-in transfer, and
only parts company at `0x3003d744` — *after* the transfer has happened. Its own
branch then acquires the flash-operation lock and calls a flash helper
(`0x7ffb5e04`) before storing a dword to `req+0x154`. "Merely fetches a result
dword" is true of where it ends up and false of everything it does on the way.
Do not use `0x0210` as a safe probe of this command byte.

> **PROVEN by execution: there is no absolute transfer-length bound on the
> write path.** The clamp idiom `minu` does not occur anywhere in `0x10`'s
> 1572-byte body, nor anywhere in `0x0F`'s. The only bound is the
> `CDW10*4 == bytes_transferred` consistency check at `0x3003db7f`. This
> closes open question 3 of `sn200-dangerous-commands.md` §6: the answer is
> "none", not "not found".

The six strings in the body place the encodings beyond doubt: `0x0010` →
`Flash_WritePageRaw` (SLC `0x3003d7b6` / MLC `0x3003d9a8`), `0x0110` →
`Flash_ProgNANDPageRaw` (SLC `0x3003dad1` / MLC `0x3003d645`), `0x0210` →
`Flash_ProgNANDPage result dword 0` (`0x3003d96d`). SLC versus MLC is still
chosen by the internal boolean `b0`, not by the host — **UNKNOWN**, unchanged.

### 3.3 `0x03` — the 640-byte clamp, executed

> **PROVEN by execution.** `ca_rawread_clamp(request=0x10000)` runs the bundle
> that materialises the bound and the `minu` that applies it, and gets **640**.
> The clamp is at `0x30037039` exactly as documented, and `0x03` is the only
> command byte in the family whose body contains the idiom at all.

The recovery arithmetic in `sn200-vuc-flash-read.md` §6 — ~1.2 × 10¹⁰ commands
for 7.68 TB — therefore stands on an executed number rather than a read one.

---

## 4. What is still UNKNOWN, stated as such

The discipline that resolved `0xFF`/`0x0303` was refusing to guess. Five
command bytes a latched drive accepts carry **no log string in their handler
body**, and they are reported UNKNOWN rather than "probably fine".

### 4.1 `0x04` and `0x13` acquire the flash-operation lock

Both are allow-listed, both carry no strings, and both call `0x7ffb42cc` — the
lock the erase and program handlers take before touching media. Whatever they
are, they are **flash operations, not DDR table reads**. `0x13` additionally
calls the `CDW13` address helper `0x7ffb3f4c` and `0x7ffb7184`; `0x04` calls
`0x7ffbab2c`. Neither leaf has been traced. Do not send either.

*(Note the call sets are a **lower bound**: these are coroutines that resume
through `jx`, and the walk does not follow an indirect jump. Presence of a
callee is evidence; absence is not.)*

### 4.2 `0x21` and `0x32`

`0x21` is 572 bytes with one reachable callee, `0x7ffb3dd0` — which is also one
of `0x0B`'s callees, the erase-count census. That makes a blockset/counter query
the natural guess and it is **only a guess**; nothing attributes a string to
`0x21`. `0x32` is 700 bytes, no strings, and its callees (`0x7ffb8ba8`,
`0x7ffb99c8`) are the generic transfer helpers eight other arms also use, so
they identify nothing. Both remain **unaudited, not cleared** — the same
verdict `sn200-command-reference.md` already gives them, now with the evidence
attached.

### 4.3 `0x41` is not established as a reader

`Permanent Die Offline` sounds like a list you read; the strings
(`PERMANENT_OFFLINE doesn't exist in DDR`, `Wrong. The permanent dieoffline
length is %d bytes`) are equally consistent with a setter that validates a
host-supplied list. It is **not** allow-listed, so it is a healthy-drive
question only, and it is left UNKNOWN deliberately rather than classed
READ-ONLY on the strength of the word "Offline".

### 4.4 `0x11` hands `CDW13` to the helper the erase path uses

`0x11`'s whole handler is:

```asm
300359e8: entry  a1,0x20
300359eb: l32i.n a9,a2,0x18          ; resume PC; 0 on first entry
300359ef: jx     a9
300359f2: l32i   a10,a2,0x13c        ; CDW13 -- a raw physical flash address
300359f5: call8  0x3002d2c4          ; = runtime 0x7ffb3f4c
300359f8: { s32i a10,a2,0x154 ; movi a2,0 }
```

`0x7ffb3f4c` is **the same helper the block-erase arm calls** at `0x3003dc2e`
and `0x3003dc4c` with the same `CDW13` value, and the same one `0x03`, `0x10`,
`0x13`, `0x37`, `0x38` and `0x39` call. It is almost certainly a flash-address
decode or validity check, and `0x11` returning its result in `req+0x154` is
consistent with that. **INFERRED, not proven** — the helper's own body has not
been traced. This is as far as open question 2 of
`sn200-dangerous-commands.md` §6 can be taken without walking the main image.

### 4.5 Everything the lifter cannot see

`sn200-pcode-toolchain.md` §4 applies unchanged, and two of its limits bite
here specifically:

- **The `skip` opacity policy.** Every result above that comes from `Emu` was
  produced with `on_opaque="skip"`. The `0x0F` traces step over one undecoded
  slot; the sub-byte-independence result is unaffected because it compares
  *whole traces*, and a skipped slot is skipped identically in all 256 runs.
- **Slot-B ALU sub-opcodes.** Still undecoded, still the reason `b0` — the SLC
  versus MLC selector on `0x10` and the `BlkType` argument to StrId 3465 — is
  UNKNOWN. **Do not assume one value of it is the safe one.**

---

## 5. The adjacency table — enumerated, not discovered

We have been finding these one incident at a time: `0xFF`/`0x0003` next to the
`0x0004` probe, `0xC6` `0x__20` next to `0x__30`. `ca_neighbours()` enumerates
them mechanically over two relations:

- **nibble** — one hex digit differs. The mistyped-command case.
- **±1** — consecutive integers. The loop-counter case. `0x0F` and `0x10` are
  15 and 16.

`sn200_oracle.py --ca --danger` prints the whole thing. The operator-relevant
part, restricted to the twelve command bytes a **latched** drive accepts:

| latched-reachable | class | destructive neighbour, one typo away |
|---|---|---|
| `0x02` UID read | READ-ONLY | `0x0F` erase (nibble), `0x12` erase-pwr-char (nibble) |
| `0x03` raw page read | READ-ONLY | ☠ **`0x0F` erase (nibble)** — and `CDW13` already holds the block you were about to read |
| `0x04` unidentified | UNKNOWN | `0x0F` erase (nibble) |
| `0x08` V2P translate | READ-ONLY | `0x0F` erase (nibble) |
| `0x0D` flash reset | MUTATES | `0x0F` erase (nibble) |
| `0x0E` read status | READ-ONLY | ☠ **`0x0F` erase (nibble)** — adjacent in the literal sense too |
| `0x0F` **block erase** | DESTRUCTIVE | `0x10` page program (±1) |
| `0x10` **page program** | DESTRUCTIVE | `0x0F` erase (±1), `0x12` erase-pwr-char (nibble) |
| `0x11` address helper | UNKNOWN | `0x10` program (nibble), `0x12` erase-pwr-char (nibble) |
| `0x13` unidentified | UNKNOWN | `0x10` program (nibble), `0x12` erase-pwr-char (nibble) |
| `0x21` unidentified | UNKNOWN | **none** — the only allow-listed byte with no destructive neighbour |
| `0x32` unidentified | UNKNOWN | `0x12` (nibble), `0x37` multiplane (nibble), `0x39` NAND set-features (nibble), `0x3B` test-mode write (nibble) |

Eleven of the twelve command bytes a latched drive accepts are a single hex
digit from something that destroys media. `0x32` is one digit from **four**.

On a healthy drive the two newly-flagged pairs matter as well, and both are
confirmed by their own strings:

- **`0x38` (ONFI Get Features) → `0x39` (ONFI SET Features)**, one value apart.
  `0x39` writes a feature address on the NAND die itself
  (`VUC: Set Features addr 0x%02x: 0x%08x`).
- **`0x3A` (get flash test-mode register) → `0x3B` (SET test-mode register)**,
  one value apart. `0x3B`'s own string enumerates
  `flash addr / reg addr / mask / originalValue / newValue / update reg val` —
  a read-modify-write of a die test register.

Neither writer is on the Post-Crash allow-list. Both are live on a healthy
drive. Neither is a documented, bounded operation.

---

## 6. What this changes

1. **Both destructive hand decodes are CONFIRMED**, by a method independent of
   the one that produced them. `0x0F` really does ignore `CDW12[15:8]`; `0x10`
   really does take host data with no absolute length bound.
2. **`0x0210` is downgraded from "adjacent" to "part of the write path".** It
   shares the first-entry arm with `0x0110` and diverges only after the host
   data transfer. It is not a probe.
3. **Two identities in the published tables are wrong** — `0x20` is not
   `VUC_ERASE_PWR_CHAR` and `0x0C` is not the erase-count census. Both strings
   sets belong to their neighbours (`0x12` and `0x0B`).
4. **The implemented count is 39, not 37.**
5. **Five allow-listed command bytes are UNKNOWN, and two of them take the
   flash-operation lock** (`0x04`, `0x13`). "Carries no destructive string" was
   already known to be weak evidence; "takes the media lock" makes it weaker.
6. **The adjacency table is now mechanical.** Eleven of twelve latched-reachable
   command bytes have a destructive neighbour one keystroke away.

### Still open

- The leaves: `0x7ffb3f4c`, `0x7ffb42cc`, `0x7ffb5e04`, `0x7ffb7184`,
  `0x7ffbab2c`, `0x7ffb3dd0`. Walking those in the main image is what would
  turn `0x04`, `0x11`, `0x13`, `0x21` and `0x32` from UNKNOWN into answers.
- `b0`, the SLC/MLC selector, still needs the slot-B ALU decode.
- `0x37`'s sub-selector space, unanalysed. Not latched-reachable, and not an
  invitation.
