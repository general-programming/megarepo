# `0xEC` resolved, and the post-crash allow-list audited by *function*

Firmware `KNGND122`. Static analysis only; no drive was touched.

`sn200-marker-write.md` closed every path it could enumerate and left one door:

> "The one unresolved door remains `0xEC` — and what it would have to do is now
> exactly specified: verb 1, section 6, `+0x124 = 0x80000008`."

**That door is now open, and there is nothing behind it.** `0xEC` is
`Admin_VUC_Enable`. After its parameter validation the entire host-controlled
input space of the command is **one bit**. It builds no EEPROM request, calls no
EEPROM primitive, and its single persistent effect — one byte at `0x7ff8f1dd` —
is read at exactly two sites, **both of which are provably inert on a latched
drive**.

The second half of this document inverts the audit: instead of going
opcode-by-opcode, it starts at the gate instruction stream and enumerates every
opcode admitted while latched, then asks of each *what it does*, not *whether it
has a bug*.

**Headline results:**

- **`0xEC` = `Admin_VUC_Enable`, overlay row 10, static `0x3002b6c4`.** Two
  valid encodings, total. **PROVEN.** The "semantics UNKNOWN / pointer did not
  resolve" claims in `sn200-command-reference.md` §2 and
  `sn200-readonly-startup.md` §6.3 are superseded.
- **The vendor-band sub-gate at `0x7ffa6bed`–`0x7ffa6c0e` is dead code.** Its
  guard is `bnei a2,1`, and the **sole** caller of `Admin_CheckCmdAllowed`
  passes `a2 = 0`. Both arms of the VUC-flag test converge on the same label, so
  the flag `0xEC` writes **cannot change the gate's verdict for any opcode**.
  **PROVEN, new.** Several docs describe that arm as live; they are describing
  an unreachable path.
- **The post-crash allow-list is a *first* gate, not the only one.** A
  passing opcode falls through into the VUC / purge-phase / sanitize gates
  (`0x7ffa6bd9` onwards). Prior docs read gate 1 as terminal.
- **`0xE6` is not a marker or EEPROM path** — it is a diagnostic-dump builder
  with no sub-command structure and no host CDW reaching it. But it exposes a
  **new, genuinely unresolved hazard**: it is the only admitted opcode dispatched
  *without* requesting an overlay load, and its worker contains
  `call8 0x7ffbc18c` — into the overlay window. Only **one** of the 30 overlays
  has an `entry` at that offset. §5.3. **Do not send `0xE6` on a latched drive.**
- **The marker setter has exactly three call sites, all in PROC0** — re-proven
  here with `litref -v` (three, not "six of which three are real"). One of them
  is the firmware-download/commit path, which means the *only* admitted standard
  NVMe opcode that reaches the boot marker is the firmware family, and it writes
  `0x80000003` REINIT. Already reflected in `sn200-runbook.md`; no operator
  change.
- **The startup-type word `0x7ff87c64` has exactly two writers**, both inside
  `Admin_IBQCommandReceiver` — it is a copy pushed to PROC8 over the
  inter-processor queue. The host surface has no writer at all. **PROVEN**,
  upgrading `sn200-attack-surface.md` finding #1 from a negative search result to
  a positive mechanism.

Labels: **PROVEN** = read off correctly-decoded instructions. **INFERRED** =
short chain over proven facts. **SPECULATIVE** = neither.

---

## 0. Method note — the overlay delta, corrected arithmetic

The rule from `sn200-oam-dispatch.md` §1.1 is right; the *numbers* printed
alongside it in that document and in `sn200-c6-dispatch.md` §1 are wrong by
`0x200000`:

```
runtime = static + (0x7ffbc000 − src2)

overlay row 10 (0xEC) : src2 = 0x3002b478 → delta 0x4FF90B88   (docs said 0x4DF90B88)
overlay row 17 (0xC6) : src2 = 0x3002ea38 → delta 0x4FF8D5C8   (docs said 0x4DF915C8)
overlay row 21 (0xFF) : src2 = 0x30033338 → delta 0x4FF88CC8   (docs said 0x4DF88CC8)
```

Every *runtime* address printed in those documents is nevertheless correct — the
work was done with the right delta and the constant was mistyped when written
up. Corrected here so nobody re-derives from the printed constant.

Validation of the row-10 delta used throughout §1: **11 of 11** `callN` targets
in overlay row 10 land on an `entry` (`0x36`) byte in `PROC8_7ff80000`, and two
of them (`0x7ffafacc` allocate, `0x7ffba9d8` memset) are the *same runtime
functions* that overlays 17 and 21 reach from different static addresses under
different deltas. Three overlays, three deltas, one function set.

---

## 1. `0xEC` — complete map

### 1.1 The dispatch binding — PROVEN, single door

`PROC8@7ff80000 0x7ffa7541`–`0x7ffa7554`, inside the admin dispatcher:

```asm
7ffa753e: movi  a12,235
7ffa7541: bgeu  a12,a11,0x7ffa7587        ; opcode <= 235 -> elsewhere
7ffa7544: movi  a13,236
7ffa7547: { movi a10,11 ; bltu a13,a11,0x7ffa755c }   ; opcode > 236 -> elsewhere
7ffa754f: l32i.n a12,a6,0x0
7ffa7551: l32r  a13,0x7ffa0e2c            ; -> 0x7ffbc24c   <-- the handler
7ffa7554: { s32i a10,a12,0x20 ; j 0x7ffa6e89 }          ; overlay index 11
```

Only `opcode == 236` falls through. `litref -v 7ffbc24c` returns exactly one
site. The stored `0x20` field is the **1-based overlay number**; index 11 is
descriptor-table **row 10** at `0x7ff81ae4 + 10*0x20`:

| field | value |
|---|---|
| `dst` | `0x7ffbc000` |
| `len` | `0x380` |
| `src2` | `0x3002b478` |

So the handler body is static **`0x3002b6c4`** (`entry a1,0x20`), and the whole
overlay is 896 bytes — the handler cannot be larger than **0x134 bytes**. This
is the smallest attack surface of any vendor opcode on the drive.

> The row-21 cross-check: index 22 → row 21 → `src2 = 0x30033338`, which is
> exactly the `0xFF` overlay `sn200-oam-dispatch.md` §2 identified. The 1-based
> convention is confirmed on a known-good case.

### 1.2 Identity — PROVEN from the firmware's own strings

The handler loads two log descriptors from its own pool:

| literal | packed value | StrId | text |
|---|---|---|---|
| `0x3002b4a8` | `0x07806001` | **1920** | `ADM: Admin_VUC_Enable SUCCESSFUL. New State: %u` |
| `0x3002b4c4` | `0x077f6000` | **1919** | `ADM: Admin_VUC_Enable FAILED. Invalid input command parameters detected` |

(`StrId = word >> 16`, `nargs = word & 0xFF` — the packing `disany.py` decodes.)

### 1.3 Every valid encoding — PROVEN, and there are exactly two

The validation chain, `0x3002b74b`–`0x3002b7ab`, in order. `a2` is the command
context (`ctx+0x130 = CDW10`, pinned by the Firmware-Image-Download handler per
`sn200-command-reference.md` §1):

```asm
3002b748: l8ui a12,a10,0x38
3002b756: { addi a6,a10,-72 ; bnez a12,0x3002b7ae }   ; CDW12[7:0]  != 0 -> FAIL
3002b75e: l32i a8,a2,0x120
3002b761: { l32i a9,a2,0x124 ; bnez a8,0x3002b7ae }   ; PRP/MPTR dwords
3002b769: bnez a9,0x3002b7ae
3002b76c: l32i a11,a2,0x128 ; bnez a11 -> FAIL
3002b771: l32i a12,a2,0x12c ; bnez a12 -> FAIL
3002b776: l32i a14,a2,0x130 ; bnez a14 -> FAIL        ; CDW10 != 0 -> FAIL
3002b77b: l32i a15,a2,0x134 ; bnez a15 -> FAIL        ; CDW11 != 0 -> FAIL
3002b780: l16ui a8,a2,0x13a ; bnez a8 -> FAIL         ; CDW12[31:16] != 0 -> FAIL
3002b785: l32i a9 ,a2,0x13c ; != 0x564F4944 -> FAIL   ; CDW13 = "VOID"
3002b78e: l32i a12,a2,0x140 ; != 0x57415252 -> FAIL   ; CDW14 = "WARR"
3002b797: l32i a15,a2,0x144 ; != 0x414E5459 -> FAIL   ; CDW15 = "ANTY"
3002b7a0: l8ui a12,a10,0x39                           ; CDW12[15:8] = the state
3002b7a3: { beqi a12,1,0x3002b6dd }  ; 1 -> OK
3002b7ab: beqz a12,0x3002b6dd        ; 0 -> OK ; anything else -> FAIL
```

| | encoding |
|---|---|
| **enable VUC** | `--opcode=0xEC --cdw10=0 --cdw11=0 --cdw12=0x0100 --cdw13=0x564F4944 --cdw14=0x57415252 --cdw15=0x414E5459`, no data buffer |
| **disable VUC** | as above with `--cdw12=0x0000` |
| anything else | StrId 1919, `status |= 0x80020000`, **and the flag is cleared anyway** (§1.5) |

> **☠ Correction to `sn200-attack-surface.md` §4.8.** That section places
> `VOIDWARRANTY` in **CDW11–CDW13** and calls `ctx+0x130`/`+0x134` "PRP2 lo/hi".
> They are **CDW10 and CDW11**, and the magic is in **CDW13–CDW15**. Two dwords
> off. Anyone who typed the documented encoding would have got StrId 1919 and
> silently disabled VUC instead of enabling it.

### 1.4 What it actually does — PROVEN

Success path `0x3002b6dd`:

```asm
3002b74e: l32r a13,0x3002b4b0        ; = 0x7ff8f1c0
...
3002b6dd: s8i  a12,a13,0x1d          ; *(0x7ff8f1dd) = state (0 or 1)
3002b6e0: { l32r a11,-> StrId 1920 ; movi a10,20 }
3002b6e8: call8 0x30023b00           ; log
3002b6eb: l32i a10,a5,0x80 ; and 0xFFFFFFF0 ; or 1 ; s32i a10,a5,0x80   ; status nibble
3002b720: call8 0x30028be0           ; = 0x7ffb9768, post completion
```

**That single byte is the whole effect.** Complete store set of the handler
(`0x3002b6c4`–`0x3002b7e4`), exhaustive:

| site | store |
|---|---|
| `0x3002b6dd` | `s8i a12,a13,0x1d` — **the VUC flag** |
| `0x3002b7c4` | `s8i a9,a13,0x1d` — the same byte, cleared, on the failure path |
| `0x3002b6fb` / `0x3002b7dd` | completion-status nibble at `a5+0x80` |
| `0x3002b701`/`0x709`/`0x712`/`0x718` | completion-record fields at `a5+0x7c…0x83` |
| `0x3002b740` | `s32i a6,a4,0x12c` — masks bits 4–7 of a status word |
| `0x3002b7bc` | `s32i a10,a2,0x160` — CQE status |
| `0x3002b734` | `memset(·, 0, 24)` of a completion sub-struct |

Callee set, exhaustive, five functions, all resolved to `entry` bytes:
`0x7ffafacc` (allocate), `0x7ffba9d8` (memset), `0x7ffb4688` (log),
`0x7ffa7eb4`, `0x7ffb9768` (enqueue completion, callback `0x7ffa463c` — the
generic admin-completion coroutine, which frees the node via `0x7ffafa88`).

**No EEPROM submitter. No OAM verb field. No section id. No call to
`0x7ffb4fec` or `0x7ffb32f8`. No write to any request offset `+0x118`/`+0x11c`
or their rebased equivalents.**

### 1.5 The malformed-`0xEC`-disables-VUC behaviour — PROVEN, and now harmless

The failure path at `0x3002b7ae` writes `s8i a9,a13,0x1d` with `a9 = 0` **before**
returning Invalid Field. `sn200-attack-surface.md` flagged this as an
availability nit. Given §2 it is not even that on a latched drive: the flag is
unread there.

### 1.6 Answering the brief's two questions

**(a) Is `0xEC` in the post-crash allow-list?** **Yes. PROVEN.**
`0x7ffa6b95: movi a14,236` / `0x7ffa6b98: beq a3,a14,0x7ffa6cfb`.

**(b) Does any `0xEC` path construct verb 1 + section 6, with caller influence
over `+0x124`?** **No, on three independent grounds, any one of which is
sufficient:**

1. **Structural.** The handler contains no store to an OAM verb or section field
   and calls no EEPROM primitive (§1.4).
2. **Input space.** After validation, every host dword the command carries is
   pinned to a constant — six must be zero, three must equal
   `VOID`/`WARR`/`ANTY`, one 16-bit field must be zero, and one byte must be 0
   or 1. **The total host-controlled information content of a valid `0xEC` is
   one bit.** A 32-bit marker value cannot be smuggled through one bit.
3. **Effect.** The only durable state it reaches is `0x7ff8f1dd`, which is not
   in the System Area, is not the boot-marker record, and has exactly two
   readers (§2).

**The door is closed. `sn200-marker-write.md` §7's remaining "if `0xEC` turns out
to be a generic write-EEPROM-section passthrough" branch does not obtain.**

---

## 2. The `0xEC` flag's two readers — both inert while latched

`litref -v 7ff8f1c0` → one site (the writer, §1.4).
`litref -v 7ff8f140` → two sites; the byte is `0x7ff8f140 + 0x9d = 0x7ff8f1dd`,
the same byte.

### 2.1 Reader 1 — `Admin_CheckCmdAllowed 0x7ffa6bdc`. Both arms converge.

```asm
7ffa6bd9: l32r a8,-> 0x7ff8f140
7ffa6bdc: l8ui a8,a8,0x9d                              ; the VUC flag
7ffa6bdf: { movi a14,191 ; bnez a8,0x7ffa6c16 }        ; VUC enabled  -> 0x7ffa6c16
7ffa6be7: bnei a2,1,0x7ffa6c16                         ; a2 != 1      -> 0x7ffa6c16
7ffa6bea: bgeu a14,a3,0x7ffa6c16
7ffa6bed: movi a9,-236 ; add.n a9,a3,a9 ; bltui a9,3,0x7ffa6c0e     ; 0xEC..0xEE
7ffa6bf5: movi a8,-216 ; add.n a8,a3,a8 ; bltui a8,8,0x7ffa6c0e     ; 0xD8..0xDF
7ffa6bfd: bne  a3,a13,0x7ffa6c06                                     ; 0xC6 …
7ffa6c06: { bne a3,a12,0x7ffa6d5b }                                  ; … else 0xE6, else REJECT
```

`Admin_CheckCmdAllowed` has **one** call site in the image (`xref` →
`0x7ffa7244`), and the argument set-up immediately before it is:

```asm
7ffa7231: { l8ui a12,a13,0x38 ; movi a14,1 ; movi a10,0 }   ; a10 = 0
7ffa7239: { l16ui a14,a7,0x11a ; ?Balu sub=6 a15,a10,a14 }  ; reads a10, writes a15
7ffa7241: l8ui a13,a13,0x39
7ffa7244: call8 0x7ffa6b18          ; callee a2 = caller a10 = 0
```

**`a2 == 0` unconditionally.** So `bnei a2,1` at `0x7ffa6be7` is always taken,
and it goes to `0x7ffa6c16` — the *same* label the `bnez a8` arm goes to.
The VUC flag therefore has **no effect whatsoever on this function's verdict**,
and the entire vendor-band comparison chain `0x7ffa6bed`–`0x7ffa6c0e` is
unreachable. **PROVEN.**

Two consequences:

- `sn200-independent-re.md` §1772 and `sn200-attack-surface.md` §4.8 describe the
  `0xEC..0xEE` / `0xD8..0xDF` band as a live "gate 2". It is dead code. The
  conclusion those documents drew (`0xD8-0xDF` is not reachable while latched)
  is still correct; the reason is stronger than they gave.
- **`0xEC` cannot widen the admitted opcode set.** Not on a latched drive, not on
  a healthy one.

### 2.2 Reader 2 — the Get-Log-Page sanitize block, `0x7ffa546e`

```asm
7ffa53b8: beqi a13,2,0x7ffa541d      ; a13 = sanitize state (from 0x7ff95708+2)
7ffa53bb: beqi a13,3,0x7ffa541d
...
7ffa541d: beqi a11,3,0x7ffa5434      ; a11 = Log Page ID -> restricted
7ffa5420: beqi a11,5,0x7ffa5434      ;                   -> restricted
7ffa5423: beq  a11,a14,0x7ffa546e    ; three further LIDs -> consult the VUC flag
7ffa5426: beq  a11,a7 ,0x7ffa546e
7ffa5429: beq  a11,a10,0x7ffa546e
7ffa546e: l32r a14,-> 0x7ff8f140 ; l8ui a14,a14,0x9d
7ffa547c: { moveqz a9,a10,a14 ; j 0x7ffa5436 }   ; a9 = 1 (restricted) iff flag == 0
```

The whole block is guarded by **sanitize state ∈ {2,3}**, i.e. a sanitize
operation in progress. A latched drive at rest is not in that state, and
Sanitize (`0x84`) is not in the allow-list, so it cannot be put into it.
**Inert while latched. PROVEN on the guard; the three register-held LIDs were
not resolved (they are set on a path outside this fragment) and do not need to
be.**

---

## 3. The post-crash allow-list, read off the gate — the definitive list

`PROC8@7ff80000 Admin_CheckCmdAllowed 0x7ffa6b18`. `a3` = opcode (`ctx+0x18`
byte 0), `a4` = `CDW12[7:0]` (`ctx+0x38`), `a5` = `CDW12[15:8]` (`ctx+0x39`).

**Gate entry, PROVEN:**

```asm
7ffa6b1b: l32r a8,-> 0x7ff87c64 ; l32i.n a8,a8,0x0    ; startup type
7ffa6b28: { movi a12,230 ; movi a10,17 ; movi a11,13 }
7ffa6b30: { movi a13,198 ; bnei a8,6,0x7ffa6bd9 }     ; type != 6 -> this gate does not apply
```

**Every admitting instruction, with its address:**

| opcode | check | address |
|---|---|---|
| `0x00` Delete I/O SQ | `beqz a3` | `0x7ffa6b38` |
| `0x01` Create I/O SQ | `beqi a3,1` | `0x7ffa6b3b` |
| `0x02` Get Log Page | `beqi a3,2` | `0x7ffa6b43` |
| `0x04` Delete I/O CQ | `beqi a3,4` | `0x7ffa6b4b` |
| `0x05` Create I/O CQ | `beqi a3,5` | `0x7ffa6b53` |
| `0x06` Identify | `beqi a3,6` | `0x7ffa6b5b` |
| `0x08` Abort | `beqi a3,8` | `0x7ffa6b63` |
| `0x09` Set Features | `beq a3,a9` (`a9=9`) | `0x7ffa6b6d` |
| `0x0A` Get Features | `beqi a3,10` | `0x7ffa6b75` |
| `0x0C` Async Event Request | `beqi a3,12` | `0x7ffa6b7d` |
| `0x10` Firmware Commit | `beqi a3,16` | `0x7ffa6b85` |
| `0x11` Firmware Image Download | `beq a3,a10` (`a10=17`) | `0x7ffa6b8d` |
| `0xEC` VUC Enable | `beq a3,a14` (`a14=236`) | `0x7ffa6b98` |
| `0xFF` OAM | `beq a3,a8` (`a8=255`) | `0x7ffa6ba3` |
| `0xE6` VUC Get Diagnostic Data | `beq a3,a12` (`a12=230`) | `0x7ffa6bab` |
| `0xCA` VUC Flash | `beq a3,a9` (`a9=202`) → sub-list `0x7ffa6d76` | `0x7ffa6bb6` |
| `0xC6` VUC SCSI Ported | `bne a3,a13` (`a13=198`), then `beqi a4,32` / `beq a4,a15` (`a15=48`) | `0x7ffa6bbe`–`0x7ffa6bc9` |
| everything else | `movi a9,1 ; j 0x7ffa6d05` → reject `0x8F8A0000` | `0x7ffa6bd1` |

`0xCA` sub-list, `0x7ffa6d76`–`0x7ffa6da9`, twelve `beq`/`beqi` against `a4`:

```
{ 0x02, 0x03, 0x04, 0x08, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x13, 0x21, 0x32 }
```

**15 unconditional opcodes + 2 conditional = 17.** Matches the count in
`sn200-readonly-startup.md` and `sn200-firmware-flow.md` exactly, and the `0xCA`
sub-list matches `sn200-dangerous-commands.md` §3 exactly.

### 3.1 The `movi a11,13` bait — re-confirmed

`0x7ffa6b28` loads `a11 = 13`. **`a11` is never compared against `a3` in this
arm** — it is consumed at `0x7ffa6ca0` (`beq a3,a11`) inside the *sanitize*
gate. Namespace Management `0x0D` is **not** admitted.
`sn200-marker-write.md` §6 called this correctly; re-verified.

### 3.2 What is *not* admitted — PROVEN by absence

`0x0D` Namespace Management · `0x15` Namespace Attachment · `0x14` Device
Self-test · `0x18` Keep Alive · `0x7C`/`0x7D` Directive · `0x80` **Format NVM** ·
`0x81`/`0x82` Security Send/Receive · `0x84` Sanitize · `0xD8`–`0xDF` (incl.
`0xDD` Secure Purge) · `0xEF` MI-test · every other vendor opcode.

The brief asked specifically about **Format NVM `0x80`**: `128` appears twice in
the function (`0x7ffa6c63`, `0x7ffa6ca8`) and **both are inside the purge and
sanitize arms**, never in the post-crash arm. **Format is rejected while
latched. PROVEN.**

### 3.3 ⚠ The allow-list is gate 1 of four, and passing it is not the end

This is structural and no prior document says it:

```
allowed  -> 0x7ffa6cfb: movi a9,0 ; j 0x7ffa6d05
0x7ffa6d05: beqz a9,0x7ffa6bd9      ; a9 == 0 -> FALL INTO GATE 2
rejected -> 0x7ffa6bd1: movi a9,1 ; j 0x7ffa6d05  -> not taken -> log + 0x8F8A0000
```

An opcode that passes the post-crash allow-list is then run through the VUC-band
gate (dead, §2.1), the **purge-phase** gate (`0x7ffa6c16`, guard
`[0x7ff918ac] != 0`, StrId 1806) and the **sanitize** gate (`0x7ffa6c76`, guard
`[0x7ff95708+2] ∈ {2,3}`, StrId 3370). On an idle latched drive both guards are
false, so the allow-list is effectively terminal in practice — but a drive
latched *during* a purge or sanitize would reject more than this list.
**PROVEN mechanism, INFERRED that the guards are false in the field** (they are
not directly observable).

---

## 4. Per-opcode: has its FUNCTION been analysed for latch-release potential?

The question is not "is it memory-safe" (that audit is
`sn200-attack-surface.md` §5.1, 16 of 17). The question is: **can it release the
latch, expose the namespace, or write the boot marker?**

| opcode | what it is | function analysed? | can it touch the boot path? |
|---|---|---|---|
| `0x00` `0x01` `0x04` `0x05` | Delete/Create I/O SQ & CQ | **yes** — `sn200-attack-surface.md` §4.6, full decode of both bound checks and the queue-state guards | **No.** Queue descriptors only; gated on controller-enabled, which is clear in mode 6 |
| `0x08` | Abort | **yes** — §4.6; SQID used as a read index only | **No** |
| `0x02` | Get Log Page | **partially.** Handler decoded around the sanitize/VUC restriction (§2.2). The full LID table was **not** enumerated | **No path found.** NVMe log pages are reads; the crash sections on this drive are reached via `0xC6`, not log pages. **Named gap:** the vendor LID set is unenumerated |
| `0x06` | Identify | **yes** (read-only by construction; CNS table is a compare chain with a default reject, §5.1) | **No** |
| `0x09` | Set Features | **yes** — `sn200-readonly-startup.md` §6.3 enumerates the FID set: **1–11, 126–131, and 240 (`0xF0`) only**; no FID ≥ `0xC0` other than `0xF0`; no APST. Not re-verified in this pass | **No.** Bounded independently by §4.1 |
| `0x0A` | Get Features | **yes** — same source, same FID set, "pure read in every arm". `sn200-attack-surface.md` §5.1 lists it as its one memory-safety gap, which is a different question | **No** |
| `0x0C` | Async Event Request | **yes** — §4.6; a doubly-linked free pool, returns AER-Limit-Exceeded on exhaustion | **No** |
| `0x10` | Firmware Commit | **yes, and it is the one positive.** §4.2 below | **YES — writes marker `0x80000003` REINIT.** Already in `sn200-runbook.md` |
| `0x11` | Firmware Image Download | **yes** — §4.7 of the attack-surface doc; DDR staging only, 32-bit wrap that mirrors itself | Only by enabling a later Commit |
| `0xC6` cmd `0x20` | Get Drive Log, subs 0–8 | **yes** — `sn200-c6-dispatch.md` §4 | **No.** Subs 0–6 read firmware-owned descriptors; 7–8 mutate a DRAM counter |
| `0xC6` cmd `0x30` | SMART / drive-statistics **collection** family, 7 subs | **yes — resolved in parallel with this pass**, `sn200-c6-30-family.md`. `sn200-c6-dispatch.md` §5 (unidentified) is superseded | **No.** No sub builds an OAM/EEPROM request; PROC8 cannot even name `0x7ff8c7ec`, `0x7ff8d200`, or the marker constants. Subs 2/4/5 self-disable on startup type 6 |
| `0xCA` sub-list (12) | raw-flash family | **yes** — `sn200-dangerous-commands.md`. Includes `0x0F` block erase and `0x10` page write, both reachable while latched | **No release path.** They understand *physical* addresses; the two sub-values that understand LBAs (`0x00`, `0x01`) are excluded from the sub-list |
| `0xE6` | VUC Get Diagnostic Data | **yes, here — §5** | **No.** But see the overlay hazard, §5.3 |
| `0xEC` | VUC Enable | **yes, here — §1** | **No.** One-bit input space |
| `0xFF` | OAM | **yes** — `sn200-oam-dispatch.md`, nine encodings, exhaustive | Marker verb `0x25` only, values 3 and 4, both destructive |

**Named gaps.** With `0xC6`/`0x30` resolved by the parallel pass
(`sn200-c6-30-family.md`), the only functional gap left in the admitted surface
is `0x02` Get Log Page's vendor LID set, which was not enumerated here. `0x09`/`0x0A` were enumerated in
`sn200-readonly-startup.md` §6.3 and are **not** gaps for this question, though
`0x0A` remains the acknowledged memory-safety gap in
`sn200-attack-surface.md` §5.1 — a different audit.

### 4.1 `0x09` Set Features — the honest bound

`sn200-readonly-startup.md` §6.3 already enumerates the FID set (1–11, 126–131,
`0xF0`) and marks it as not boot-path-influencing. That was not re-verified
here. It does not need to be, because Set Features cannot write the boot marker
for a reason that is independent of its FID table:

`litref -v 7ffa84c8` — the marker setter — returns **exactly three sites, all in
PROC0**: `0x7ffa431f` (verb 37), `0x7ffa4732` (verb 1 + section 6), `0x7ffabccc`
(firmware download/commit). This is base-pointer-independent and image-wide.
Every host-originated EEPROM write must arrive at PROC0 as an OAM request, and
the only arm that writes the marker record from a request-supplied value is
`0x7ffa4709` (verb 1, section 6). **PROVEN.**

`litref -v 7ff8c7ec` (the marker's RAM shadow) returns 11 sites, **all in
PROC0** — confirming `sn200-readonly-startup.md` §5(e): no other core holds the
marker address, so no PROC8 handler can poke it directly either.

What is **not** excluded: a Set Features FID with the Save (`SV`) bit could
persist a feature block to *some* EEPROM section. That is a different section
and the section id would be firmware-chosen, so it cannot be the marker record —
but it is an unenumerated write path and should be recorded as such.
**INFERRED**, assumption: no FID handler hard-codes section 6.

### 4.2 `0x10`/`0x11` firmware family — the one admitted standard opcode that reaches the boot marker

PROC0 `0x7ffabbf0` (`+0xdc`), the firmware-download/commit request handler:

```asm
7ffabcb7: l32r a10,-> StrId 1366 "SYS: Firmware download flags %08X\n"
7ffabcba: l32i a11,a2,0x90 ; s32i a11,a2,0x78
7ffabcc3: l32i a9,a2,0x78
7ffabcc6: bbci a9,0,0x7ffabd22        ; flags bit 0 CLEAR -> skip entirely
7ffabcc9: l32r a2,-> 0x7ff8cdb8       ; the request object
7ffabccc: l32r a11,-> 0x7ffa84c8      ; THE MARKER SETTER
7ffabccf: { l32r a12,-> 0x80000003 ; movi a13,0 }     ; REINIT
7ffabcdb: { s32i a12,a2,0x18 ; mov a10,a2 }
7ffabce3: call8 0x7ffb32f8            ; submit
```

Literals verified: `0x7ff82b50 = 0x80000003`, `0x7ff82b54 = 0x7ffa84c8`,
`0x7ff82b1c = 0x7ff8cdb8`. **PROVEN.**

The value is hard-coded; the *gate* is bit 0 of the image's own flags word, not
anything the host supplies. So this is a hazard, not a lever:
**a firmware download/commit on a latched drive can schedule the same REINIT
that `0xFF`/`0x0503` does, and which image slot you target decides whether it
happens.** `sn200-runbook.md` already states this ("`--action=2` writes marker 3
gated on bit 0 of the target image's own flags word"), so **no operator change
is required** — but it is worth knowing that `0x10` and `0x11` sit in the
post-crash allow-list, i.e. a latched drive will *accept* them.

### 4.3 The startup-type word has no host writer — PROVEN, with the mechanism

`litref -v 7ff87c64` → 23 sites, all PROC8. Sweeping each for a store through
the loaded register leaves exactly **two writers**:

```asm
7ffb014a: l32r a14,-> 0x7ff87c64
7ffb0157: s32i a13,a14,0x0            ; a13 = [msg+0x10]
7ffb019c: l32r a15,-> 0x7ff87c64
7ffb01a7: s32i a14,a15,0x0            ; a14 = [msg+0x10]
```

Both are inside **`Admin_IBQCommandReceiver`** (StrIds 2049 *"Admin_IBQCommand
Receiver System Inited Done (Src Mgr = %d)"*, 2051 *"Admin_IBQCommandReceiver
Startup Req MSGID 0x%x"*), reached on inter-processor message IDs 231 and
260/261/288. **`0x7ff87c64` is a copy of PROC0's startup type pushed to PROC8
over the IBQ; nothing on the host admin path writes it.** This upgrades
`sn200-attack-surface.md` finding #1 from "no writer found" to "the two writers
are both IPC receivers".

### 4.4 Namespace re-attach — unchanged clean negative

`0x0D` and `0x15` are absent from the gate (§3.2), and per
`sn200-readonly-startup.md` §2 the namespace is missing because startup type 6
skips the System-Area read, so there is no detached namespace to attach.
Unchanged from `sn200-marker-write.md` §6.

---

## 5. `0xE6` — treated as a first-class target, as instructed

`0xE6` appears in no current dispatch document. It is now mapped.

### 5.1 Binding — PROVEN, and it is structurally unlike every other vendor opcode

```asm
7ffa759b: movi a9,230
7ffa759e: bne  a11,a9,0x7ffa75b4
7ffa75a1: l32r a11,-> 0x7ffb375c      ; the handler, IN THE MAIN IMAGE
7ffa75a4: { <mov a10,a11> ; j 0x7ffa7d2a }
7ffa7d2a: { s32i a10,a7,0x10 ; j 0x7ffa6ea3 }
```

Compare the overlay opcodes, which go to `0x7ffa6e89`:

```asm
7ffa6e89: s32i.n a13,a7,0x10          ; stash handler
7ffa6e8b: { l32r a11,<overlay-load callback> ; mov a10,a12 }
7ffa6e93: call8 0x7ffb9768            ; POST AN OVERLAY-LOAD REQUEST, yield
7ffa6ea1: l32i.n a11,a7,0x10          ; resume: reload handler
7ffa6ea3: ... call8 0x7ffb9768        ; post the handler itself
```

**`0xE6` enters at `0x7ffa6ea3`, skipping the overlay-load post entirely.**
`0xE6` requires no overlay and requests none. **PROVEN.**

### 5.2 Function — a diagnostic-dump builder, no EEPROM path

Handler `Admin_VucGetDiagnosticData 0x7ffb375c`, worker `Admin_BuildE6Entry
0x7ffb2ef0`. Callee sets (exhaustive, both functions): allocate `0x7ffafacc`,
free `0x7ffafa88`, memset `0x7ffba9d8`, log `0x7ffb45a8`, enqueue `0x7ffb9768`,
request-field setters `0x7ffba674` / `0x7ffba698` / `0x7ffba968` / `0x7ffba990` /
`0x7ffba6a8`, plus `0x7ffa7e30`, `0x7ffa7eb4`, `0x7ffb8924`, `0x7ffabfcc`,
`0x7ffa8bf4`, and `0x7ffbc18c` (§5.3).

**No EEPROM submitter, no marker setter, no verb/section construction.** The
`+0x78`/`+0x7c`/`+0x80`/`+0xac` stores in the worker that superficially resemble
the `req+0xA0` rebasing of the `0xFF`/`0x0007` handler are **not** OAM fields —
they are the overflow counters logged by StrId 2950 *"Admin_BuildE6Entry buffer
overflow: jumpTableIndex=%d, elementLen=0x%x"*, incremented in place
(`l32i / addi.n / s32i` at `0x7ffb34d8`–`0x7ffb34e2`). Checked because that
offset pattern is exactly the trap `sn200-marker-write.md` §3.3 warned about.

Corroborates `sn200-attack-surface.md` §4.8: no sub-command structure, and the
only host quantities that reach it are the data pointer and transfer length.
Its host input space is therefore also too narrow to carry a marker value.

### 5.3 ⚠ NEW, unresolved: `0xE6` calls into the overlay window without loading an overlay

```asm
7ffb3621: { l32i a11,a1,0x1c ; bnez a10,0x7ffb3636 }
7ffb3629: l32i.n a10,a1,0x14
7ffb362b: l32i a10,a10,0x78
7ffb362e: call8 0x7ffbc18c            ; <-- the overlay window
```

`0x7ffbc000`–`0x7ffbf040` is the overlay window; which function `0x7ffbc18c` is
depends on the **resident** overlay, and `0xE6` does not request one (§5.1). It
therefore inherits whatever the previous admin command left loaded — and the
host chooses that (`0xEC` loads row 10, `0xFF` loads row 21, `0xC6` loads row
17, …).

Checking offset `+0x18c` in every one of the 30 overlays for an `entry` byte:

| overlay row | bytes at `src2+0x18c` | `entry`? |
|---|---|---|
| **3** (`src2 = 0x30024078`) | `36 41 00` | **yes** |
| all other 29 rows | various / short | **no** |

**Exactly one overlay makes that call valid.** So either `0xE6` is only ever
issued in a state where overlay row 3 is resident, or the call is a latent
overlay-confusion bug in which a host can steer a fixed call into the middle of
a function it did not intend.

**Assessment — SPECULATIVE, and deliberately not pursued:**

- It is **not** arbitrary code execution. The host picks one of 30 fixed images,
  not the bytes, and cannot control the arguments.
- Landing on a non-`entry` byte means the callee runs on the caller's register
  window. The overwhelmingly likely outcome is an exception → controller reset →
  and per `sn200-section-arming.md` §4 a reset re-runs the latch path. **Negative
  expected value**, exactly like the mode-6 NULL-DMA finding.
- The call is guarded (`0x7ffba968` must return 0) and reachability was not
  established.

**Operational position: add `0xE6` to the do-not-send list for latched drives.**
Previous docs list it as "log-dump VUC, unconditional, pure read"
(`sn200-readonly-startup.md` §6.3 table, `sn200-firmware-re.md` §1591). It is a
pure read *of the host's data*, but it is not a command whose control flow is
determined by its own image.

---

## 6. Adjacency hazards found in this pass

Adding to `sn200-oam-dispatch.md` §4.4 (`0x0003`/`0x0004`, `0x0403`/`0x0503`,
`0x0303`) and `sn200-section-arming.md` §7 (`0x__20`/`0x__30`,
`0x0620`/`0x0720`):

- **`0xE6` ↔ `0xEC` ↔ `0xEF`.** All three are one hex digit apart in the same
  nibble position, and all three are handled by adjacent arms of the same
  dispatcher. `0xEC` is inert; `0xE6` is §5.3; `0xEF` is rejected while latched.
  The dangerous member is the *middle* one, which is the one that looks safest.
- **`0xEC` CDW13–CDW15 are all-or-nothing.** One wrong bit in the magic and the
  command **clears** the VUC flag instead of setting it (§1.5), while returning
  Invalid Field. The failure is silent in the sense that the status does not say
  what was changed.
- **`0x10` and `0x11` are in the allow-list.** A latched drive *accepts*
  Firmware Commit. Any tooling that "just reflashes the drive to fix it" is one
  flags-bit away from the same wipe as `0xFF`/`0x0503`.
- **`0x0F` / `0x10` in the `0xCA` sub-list vs `0x10` / `0x11` as top-level
  opcodes.** `--opcode=0x10` (Firmware Commit) and
  `--opcode=0xca --cdw12=0x0010` (raw page write) are both admitted and both
  destructive, and the literal digits `10` and `11` appear in both roles. Read
  the `--opcode` flag, not the number.

---

## 7. Verdict

| question | answer |
|---|---|
| What is `0xEC`? | **`Admin_VUC_Enable`**, overlay row 10, static `0x3002b6c4`, ≤ 0x134 bytes. **PROVEN** |
| Is `0xEC` reachable on a latched drive? | **Yes** — `0x7ffa6b98`. **PROVEN** |
| Does any `0xEC` path build verb 1 + section 6? | **No.** No verb/section store, no EEPROM callee, and the total host input space after validation is **one bit**. **PROVEN** |
| Can `0xEC` influence request `+0x124`? | **No.** It never touches an OAM request. **PROVEN** |
| Does `0xEC` widen the admitted opcode set? | **No** — its flag's gate reader has both arms converging on the same label, because the sole caller passes `a2 = 0`. **PROVEN, new** |
| Complete admitted set while latched? | 15 opcodes + `0xC6`{`0x20`,`0x30`} + `0xCA`{12}. §3, every check with its address. **PROVEN** |
| Any admitted opcode whose FUNCTION is un-analysed? | **None of substance.** `0xC6`/`0x30` was resolved in parallel (`sn200-c6-30-family.md`); the residue is `0x02`'s vendor LID set, unenumerated here |
| `0xE6`? | Diagnostic-dump builder. No EEPROM, no marker, host input too narrow. **But** it calls into the overlay window without loading an overlay (§5.3) — **do not send while latched** |
| Any admitted standard opcode that reaches the boot path? | **One: the firmware family.** `0x10`/`0x11` → PROC0 `0x7ffabccc` → marker `0x80000003` REINIT, gated on the image's own flags bit 0. **PROVEN**, already in the runbook |
| Format NVM `0x80`? | **Not admitted.** Both `128` compares are in the purge/sanitize arms. **PROVEN** |
| `0x0D` / `0x15`? | **Not admitted.** The `movi a11,13` is bait. **PROVEN** |
| Net change to the recovery position? | **None.** The last enumerable in-band door is closed. This remains a code-execution / EEPROM-write problem |

**With `0xC6`/`0x30` also resolved (`sn200-c6-30-family.md`), every opcode the
post-crash gate admits has now been read for function.** None of them releases
the latch, exposes the namespace, or writes the boot marker. The escape, if one
exists, is out-of-band code execution.
