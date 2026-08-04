# The SN200 fault record, decoded

Subject: `docs/sn200-dumps/nvme7-crash-128k.bin`, the `.CDI` block at file offset
`0x11000`. Drive `HUSMR7676BDP3Y1`, firmware `KNGND122`.

Everything below was re-derived from the `KNGND122` images with
`disany.py` / `litref.py` / `xref.py` / `whichfunc.py`, always disassembling from
a function entry. Labels: **PROVEN** = read out of correctly-decoded instructions
or observed bytes; **INFERRED** = short chain over proven facts; **SPECULATIVE**.

---

## 0. Bottom line

| Question | Answer | Grade |
|---|---|---|
| Which core faulted? | **PROC9 — the NVMe-MI / MCTP / SMBus out-of-band management processor** | **PROVEN**, two independent ways |
| Is `+0x04 = 6` a core index? | **No.** It is the **vector index**. The PROC6 reading is refuted. | **PROVEN** |
| `EPC1` | **Not in the dump.** The append provably starts two dwords past it. | **PROVEN absent** |
| `EXCCAUSE` | **Not in the dump**, but constrained: it is **not** 4 (Level-1 interrupt), **not** 5 (Alloca), **not** 15 (LoadStorePIFAddrError). Everything else routes to vector index 6. | **PROVEN** (constraint) |
| What was it doing? | Executing an RTOS **task body dispatched by the PROC9 scheduler**, immediately after a log emit that was pushed cross-core | **PROVEN** (frames) / **INFERRED** (naming) |
| Root-cause verdict | **Refutes "unfinished shutdown".** Supports the red team's attack 7 / attack 4: a plain firmware trap, in a subsystem with no shutdown involvement at all. | **INFERRED** |

---

## 1. The writer, traced

Two functions produce the record. Both are per-image; the ones that matter are
**PROC9's** copies, because PROC9 is the core that faulted.

### 1.1 The exception vectors — PROVEN

Each vector stashes `a1`, drops to level 15, points `a1` at the staging buffer,
saves the original `a0`/`a1`, tags itself with an index and jumps to a common
saver. PROC9, `0x7ffa01d6` onwards:

```asm
7ffa01db: wsr  a1,213                  ; park a1 in EXCSAVE5
7ffa01df: rsil a1,15
7ffa01e2: l32r a1,0x7ffa01d8           ; -> 0x7ff99960   the staging buffer
7ffa01e5: s32i.n a0,a1,0x1c            ; buf+0x1c = AR0
7ffa01e7: rsr  a0,213
7ffa01ea: s32i.n a0,a1,0x20            ; buf+0x20 = AR1
7ffa01ec: movi.n a0,2                  ; vector index
7ffa01ee: j    0x7ffa055c
```

**The staging buffer is per-image**: PROC0 `0x7ff97df0`, **PROC9 `0x7ff99960`**
(`litref.py -v 7ff99960 PROC9`). The field offsets are identical because the code
is identical.

### 1.2 The common saver `0x7ffa055c` (PROC9) — PROVEN

```asm
7ffa055c: s32i.n a0,a1,0x18     ; +0x18 vector index
7ffa055e: s32i.n a2,a1,0x24     ; +0x24.. a2..a7  (AR2..AR7)
   ...
7ffa056a: rsr a2,72   -> +0x00      SR72   (re-entrancy flag; cleared to 0 at 0x7ffa05c6)
7ffa056d: rsr a3,73   -> +0x04      SR73   (set to 1 at 0x7ffa05c3)
7ffa0570: rsr a4,230  -> +0x08      PS
7ffa0573: rsr a5,177  -> +0x0c      EPC1
7ffa0576: rsr a6,232  -> +0x10      EXCCAUSE
7ffa0583: movi.n a0,7             ; 7 further windows
7ffa0585: s32i.n a8,a1,0x3c       ; a8..a15 at +0x3c..+0x58
   ...
7ffa059c: addi a9,a1,32           ; next window's a1 = a1 + 32
7ffa059f: addi.n a8,a0,-1
7ffa05a1: rotw 2                  ; +8 physical registers
7ffa05a4: bnez a0,0x7ffa0585
```

`rotw 2` advances 8 registers; `a9→a1` advances the pointer 32 bytes. Seven
iterations after the initial 16 registers gives **AR0..AR63 at `buf+0x1c ..
buf+0x11b`**, one physical register per dword. `+0x14` is the only gap in the
whole staged area — and that is exactly where the magic goes.

### 1.3 The appender `0x7ffa2978` (PROC9) — PROVEN

Reached by `j 0x7ffa2978` at the tail of the saver.

```asm
7ffa29a1: l32r a2,0x7ffa07b8   -> 0x7ff99a60      ; = buf+0x100
7ffa29a7: rsr a12,1  ; LEND    -> s32i a12,a2,0x44   = buf+0x144
7ffa29b3: rsr a11,0  ; LBEG    -> s32i a11,a2,0x40   = buf+0x140
7ffa29b6: rsr a10,2  ; LCOUNT  -> s32i a10,a2,0x48   = buf+0x148
   ...
7ffa2aba: l32r a15,0x7ffa07dc  = 0x49444309       ; <-- magic, low byte = CORE ID
7ffa2abd: l32r a9, 0x7ffa07e0  = 0x00011000       ; <-- PROC9's fixed slot offset
7ffa2ac2: l32r a8, 0x7ffa07e4  -> 0x7ff99960      ; the staging buffer
7ffa2ac5: l32r a2, 0x7ffa07d4  -> 0x7ff99778      ; source cursor
7ffa2ab4: l32r a3, 0x7ffa07d8  -> 0x7ff998a4      ; source end
7ffa2ac8: s32i.n a9,a4,0x4                        ; a4 = 0x7ff83150, section target = 0x11000
7ffa2aca: s32i.n a15,a8,0x14                      ; magic -> buf+0x14
7ffa2acc: l32i.n a10,a4,0xc          ; loop: dword index
7ffa2ace: l32i a11,a2,0x1fc          ;   read [cursor + 0x1fc]
7ffa2ad6: s32i a11,a12,0x0           ;   append
7ffa2ade: s32i a10,a4,0xc            ;   index++
7ffa2ae6: call8 0x7ffa21d8           ;   flush every 64
7ffa2ae9: addi.n a2,a2,4
7ffa2aeb: bne a2,a3,0x7ffa2acc
```

Source range `0x7ff99778 + 0x1fc = 0x7ff99974` through `0x7ff99a9f`:
**75 dwords beginning at `buf+0x14`**, the magic word. A second append then
continues contiguously from `buf+0x140` (`LBEG`).

Three things fall out, all PROVEN:

1. **The record starts at `buf+0x14`, not `buf+0x00`.** `0x00011000` is a
   *literal* — PROC9's CDI slot is hard-coded to file offset `0x11000`. There is
   no framing before it, which is why the magic is the first word.
2. **`SR72`, `SR73`, `PS`, `EPC1`, `EXCCAUSE` sit at `buf+0x00..+0x10` and are
   never appended.** The copy starts two dwords past `EXCCAUSE`.
3. **The magic's low byte is the core id.** PROC0's literal is `0x49444300`
   (`litref.py -v 49444300 PROC0`); PROC9's is `0x49444309`. The file has
   `0x49444309`.

---

## 2. Authoritative field map

`rec` = file offset − `0x11000`; `buf` = PROC9 RAM offset from `0x7ff99960`.

| rec | buf | field | value in this dump | grade |
|---|---|---|---|---|
| — | `+0x00` | `SR72` (fault-nesting flag) | **not appended** | PROVEN absent |
| — | `+0x04` | `SR73` (fault-nesting flag) | **not appended** | PROVEN absent |
| — | `+0x08` | **`PS`** | **not appended** | PROVEN absent |
| — | `+0x0c` | **`EPC1`** | **not appended** | PROVEN absent |
| — | `+0x10` | **`EXCCAUSE`** | **not appended** | PROVEN absent |
| `+0x000` | `+0x14` | magic `"\x09CDI"` = `0x49444300 \| core` | `0x49444309` → **core 9** | PROVEN |
| `+0x004` | `+0x18` | **vector index** | `6` | PROVEN |
| `+0x008`…`+0x107` | `+0x1c`…`+0x11b` | **AR0…AR63**, physical register file, `AR[n]` at `rec+0x08+4n` | see §4 | PROVEN |
| `+0x108`…`+0x127` | `+0x11c`…`+0x13b` | **not written by this fault's handler** — residual RAM between the AR array and the loop block | `ddd32780/00000001` ×4 | PROVEN (no writer) |
| `+0x128` | `+0x13c` | scratch, `s32i.n a12,a2,0x3c` at `0x7ffa2ac0` | `0x00000000` | INFERRED |
| — | — | *(end of the 75-dword first append)* | | PROVEN |
| `+0x12c` | `+0x140` | **`LBEG`** | `0x7ffbdab8` | PROVEN |
| `+0x130` | `+0x144` | **`LEND`** | `0x7ffbdac3` | PROVEN |
| `+0x134` | `+0x148` | **`LCOUNT`** | `0x00000000` | PROVEN |
| `+0x138`… | `+0x14c`… | continuation of the second append; contents not identified | `0x18, 0x0a, 0x05, 0x28, …` | unresolved |

### The `LBEG`/`LEND` proof, and why it also names the core

`LBEG`/`LEND` are only ever written by a `LOOP` instruction, and the Xtensa
semantics are exact: for a 3-byte `loop`/`loopnez`/`loopgtz` at `P`,
`LBEG = P+3` and `LEND = P+4+imm8`. The observed pair demands
`P = 0x7ffbdab5`, `imm8 = 0x0a`.

Sweeping every image at `0x7ffbdab5`:

```
PROC6_7ff80000.bin   25 64 ff     (call8 — not a loop)
PROC9_7ff80000.bin   76 99 0a     loopnez a9,0x7ffbdac3   <== unique match
(every other image's text ends before 0x7ffbdab5)
```

`0x7ffbdab5` in PROC9 is `loopnez a9, +0x0a`, giving `LBEG = 0x7ffbdab8` and
`LEND = 0x7ffbdab5 + 4 + 0x0a = 0x7ffbdac3`. Exact, to the byte, on both fields.

This is independent of the magic-low-byte evidence and of the writer trace. Two
disjoint proofs, same answer.

`LCOUNT = 0` means the `loopnez` was entered with `a9 = 1` — a single-iteration
loop, i.e. an **8-byte cross-core word write** (see §5).

### Header fields, for completeness

The container header at file `+0x00` is staged in PROC0's buffer, which is the
same RAM as PROC0's exception staging area (`0x7ff97bf4 + 0x1fc = 0x7ff97df0`),
written by `0x7ffa29bd`:

| off | source | value |
|---|---|---|
| `+0x00` | `0x48444300` `"HDC\0"` | `0x48444300` |
| `+0x04` | version literal `0x7ff8288c` | `0x00020200` — **full fault dump** |
| `+0x08` | `"KNGN"` / `"D122"` | `KNGND122` |
| `+0x10` | `[0x7ff84bc0+0x210]` — CCOUNT stamp | `0xea2bdb72` |
| `+0x14` | `[0x7ff84bc0+0x214]` — fault counter | `0x00000100` |
| `+0x40` | reason tag (`"UNEXSTRT"` only on the stub path) | zero |

---

## 3. The faulting core: **PROC9**

**PROVEN, three ways, all independent:**

1. **Magic low byte.** The CDI magic literal is per-image and encodes the core.
   PROC9's is `0x49444309`; the file reads `0x49444309`.
2. **Slot offset.** PROC9's appender loads `0x00011000` as its section target.
   The record is at file offset `0x11000`.
3. **`LBEG`/`LEND`.** Only PROC9 has a `LOOP` instruction that produces exactly
   `0x7ffbdab8`/`0x7ffbdac3`, and the ISA formula matches to the byte.

Cross-check: every code address in the register file resolves inside a PROC9
function, and the register *contents* of the caller frame match PROC9's code
literally (§4).

**`+0x04 = 6` is the vector index, not a core index.** The PROC6 /
CLEAN-PFAIL-marker reading in `sn200-crash-dump-field-results.md` is **refuted**.

### What PROC9 is

`sn200-firmware-re.md` §: **PROC9 is the NVMe-MI / MCTP / SMBus out-of-band
management processor** — 126 MI/MCTP log descriptors, the NVMe-MI stack over both
SMBus and PCIe VDM, `PCIe_PfailShutdown` at `0x7ffaecf0`.

**Core 9's log blocks are at `0x12500 + 9*0x4000 = 0x36500`, far past the 128 KiB
retrieval ceiling.** The blocks we do have are cores 0–3 (header `+0x0c` =
`(core<<16)|flags`, confirmed: `0x0000_0003`, `0x0001_0003`, `0x0002_0007`,
`0x0003_0003`). **No log correlation with the faulting core is possible from this
dump.** Lifting the ceiling would put core 9's log in reach — that is now the
single highest-value follow-up.

---

## 4. `EPC1` and `EXCCAUSE`

### `EPC1` — NOT RECOVERABLE from this dump. PROVEN absent.

`EPC1` is staged at `0x7ff9996c` (`buf+0x0c`). The appender's source cursor
starts at `0x7ff99974` (`buf+0x14`). It is two dwords short. The value is also
copied into PROC9's persistent crash-context struct at `0x7ff83150+0x70 =
0x7ff831c0` (via `call8 0x7ffa2838`), and **that struct does not appear anywhere
in the retrieved 128 KiB** — searching the whole file for its `"KNGND122"`
signature at struct `+0x68` returns only the container header and the log-block
headers.

Do not guess it. It is not there.

### `EXCCAUSE` — NOT in the dump, but constrained. PROVEN constraint.

Same story: staged at `buf+0x10`, copied to `0x7ff83150+0xc8`, neither appended.

What *is* recoverable is the vector index, `6`, and PROC9's `EXCCAUSE` dispatch
table at `0x7ff81d38` (`0x7ffa0243: l32r a3,0x7ffa0238 -> 0x7ff81d38`;
`0x7ffa0246: rsr a2,232` / `addx4` / `jx`). Dumping all 64 entries:

| `EXCCAUSE` | handler | meaning |
|---|---|---|
| 4 | `0x7ffbb140` | Level1Interrupt — **own handler, not us** |
| 5 | `0x7ffa0010` | Alloca — **own handler, not us** |
| 15 | `0x7ffa060c` | LoadStorePIFAddrError — **own handler, not us** |
| **all other 61 values** | `0x7ffa0528` | → `movi.n a0,6` → the generic fatal path |

`0x7ffa0528` is the block that sets vector index `6`:

```asm
7ffa0528: rsil a3,15
7ffa052b: l32r a3,0x7ffa0280   -> 0x7ff99960
7ffa052e: s32i.n a0,a3,0x1c    ; AR0
7ffa0530: addi a2,a1,96        ; undo the dispatch vector's a1 -= 96
7ffa0533: s32i.n a2,a3,0x20    ; AR1
7ffa053c: movi.n a0,6
7ffa053e: j 0x7ffa055c
```

So, **decoded to Xtensa meaning**:

> The fault was a **synchronous processor exception taken through the
> `EXCCAUSE` dispatch vector and landing on the generic fatal handler**. It was
> **not** an interrupt, **not** a register-window/alloca fixup, and **not** the
> one PIF address error the firmware handles specially. It was one of:
> IllegalInstruction (0), Syscall (1), InstructionFetchError (2),
> **LoadStoreError (3)**, IntegerDivideByZero (6), Privileged (8),
> **LoadStoreAlignment (9)**, InstrPIFDataError (12),
> **LoadStorePIFDataError (13)**, InstrPIFAddrError (14), any TLB/prohibited
> cause (16–29), or a coprocessor-disabled cause (32–39).

That is a real narrowing — it rules out the benign, routinely-taken causes — but
it does **not** name the cause. Anyone who states a specific `EXCCAUSE` from this
artefact is inventing it.

`EXCVADDR` (`rsr 238` → struct `+0xcc`) is likewise absent.

---

## 5. The call chain

`AR[n]` = `rec + 0x08 + 4n`. Physical index `(WindowBase*4 + n) mod 64`, so
**lower `n` is the current frame and `n = 56` is the caller** of a `call8`
(2 window rotations back = −8 registers); higher `n` (16, 24, 40) are *forward*
rotations, i.e. **dead frames from calls that already returned**, still resident
in the circular register file.

Windowed return addresses carry the call-size increment in bits 31:30, so
`0xbffbbfa8 → 0x7ffbbfa8` (`0b10` = `call8`).

### The live chain — PROVEN

```
AR0  = 0xbffbbfa8   ret -> PROC9 0x7ffbbfa8   AR1  = 0x7ff9ff80  sp
AR56 = 0xbffa3973   ret -> PROC9 0x7ffa3973   AR57 = 0x7ff9ffb0  sp
```

**`AR56..AR63` is the caller's window, and its contents match the code literally:**

| reg | value | code |
|---|---|---|
| `AR58` = `a2` | `0x7ff91d80` | the task/queue node argument |
| `AR60` = `a4` | `0x7ff97784` | `0x7ffbbf63: l32r a4,0x7ffa0790 -> 0x7ff97784` |
| `AR61` = `a5` | `0x00000000` | `0x7ffbbf81: movi a5,0` |

That caller is **PROC9 `0x7ffbbf60`, the RTOS ready-queue dispatcher**:

```asm
7ffbbf60: entry a1,0x30
7ffbbf63: l32r a4,0x7ffa0790   -> 0x7ff97784      ; scheduler control block
   ... unlink the head task from the ready list, store it at [a4+0x00] ...
7ffbbfa1: l32i.n a8,a2,0x8                        ; task entry function pointer
7ffbbfa3: l32i.n a10,a2,0xc                       ; task argument
7ffbbfa5: callx8 a8                               ; RUN THE TASK
7ffbbfa8: l32r a14,0x7ffa20a0  -> 0x7ff81050      ; <-- AR0 points here
7ffbbfba: jx a14                                  ; switch on the task's return code
```

and its own return address `0x7ffa3973` is `0x7ffa3970: call8 0x7ffbbf60`, inside
the scheduler loop `0x7ffa38c0` (`xref.py PROC9 7ffbbf60` → three call sites,
`0x7ffa395a`, `0x7ffa3968`, `0x7ffa3970`; only `0x7ffa3970` returns to
`0x7ffa3973`). PROVEN.

So the innermost live frame is **the body of an RTOS task, entered via
`callx8` from the dispatcher**. Frame arguments (`AR2..AR7`):
`0x7ff91d78`, `0xffffc001`, `0x7ff977a8`, `3`, `1`, `4`.

**The task's identity is not statically recoverable** — the entry pointer is
`[task+0x8]` in RAM (`0x7ff91d88`), which is not in the dump. Anyone naming the
task from this record is guessing.

### The dead frames — INFERRED (structurally sound)

```
AR16 = 0xbffbaba3  ret -> 0x7ffbaba3   sp 0x7ff9fec0
AR24 = 0xbffbacfd  ret -> 0x7ffbacfd   sp 0x7ff9fea0
AR40 = 0xbffbacfd  ret -> 0x7ffbacfd   sp 0x7ff9fe60
```

- `0x7ffbaba3` is inside **`Log_Emit`** (PROC9 `0x7ffba9d8`, `entry a1,0x90`; the
  byte-identical copy documented in `sn200-firmware-re.md` §12).
- `0x7ffbacfd` is the return address of `0x7ffbacfa: call8 0x7ffbda90` in PROC9
  `0x7ffbac20` — a hardware/mailbox transmit routine (`rsr a10,234` CCOUNT,
  `l32r a14 = 0x82a5fe00`, counter at `0x7ff97000`).
- `0x7ffbda90` is a **cross-processor write helper**: it compares `PRID`
  (`rsr a8,235`) against a core number in bits 31:25 of a descriptor and either
  does a local `memcpy` (`call8 0x7ffbd8e0`) or writes the payload word-by-word
  through the loop at `0x7ffbdab5` — **the very loop whose `LBEG`/`LEND` are in
  the record**, with `LCOUNT = 0` ⇒ one iteration ⇒ an 8-byte payload.

Everything closes: the loop registers, the dead frames, and the function that
contains the loop are the same code path.

**What the drive was doing:** PROC9 had just emitted a firmware log record and
pushed it cross-core through the 8-byte remote-write loop (twice), returned to
its task body, and faulted there — inside ordinary out-of-band management work,
under the RTOS scheduler, on a running drive.

---

## 6. Verdict on the root cause

### It **refutes** the unfinished-shutdown model, and it is the strongest single
### piece of evidence yet against it.

The chain, stated plainly:

1. The container header is `0x00020200` with a **zero** reason tag. That is the
   full-fault-dump writer, not the `UNEXSTRT` stub (`0x00020100` +
   `"UNEXSTRT"`), and `litref.py` shows exactly one load of each literal, so the
   two producers are cleanly separated. Already recorded; now unavoidable.
2. The record inside it is a **genuine Xtensa exception frame**, taken through
   the `EXCCAUSE` dispatch vector to the **generic fatal handler**. Not an
   interrupt. Not a window fixup. A trap.
3. It was taken on **PROC9**, the NVMe-MI / MCTP / SMBus out-of-band management
   processor. **Nothing in the shutdown or PFAIL path runs there in the sense the
   model requires**: markers 5/6/7 are written by PROC0 (`0x7ffa8dca`,
   `0x7ffa83d7`) and markers 1/2 by PROC6 (`0x7ffbba61`).
4. The frame is a **scheduler-dispatched task body**, on a live, running drive,
   at normal priority — not a shutdown work list, not a PFAIL deadline monitor.
5. The log frozen in the same dump ends with `SYS: PFAIL startup` →
   `Scrubbing done` → `SYS: Inited` → `DriveReady`: the *preceding* power-fail
   shutdown **completed**, wrote marker 2, and the drive came up clean. The fault
   is after that.

This is exactly the second row of `sn200-red-team.md` attack 7 — *"Real assert /
trap → `0x7ffa140c` → full dump into CLOG → `0x7ffaae35 ball bit 0` →
`0x7ffaaf08` → marker 9 → startup mode 6"* — a route that **needs no shutdown of
any kind**. The red team called it "the route the retrieved dump's own header
supports". The register file now says so too.

It also fits **WD's own stated root cause** for the crashed-drive defect
(OM-6850: media loss "over time, this leads to a **crash**") far better than the
adopted model does.

### What it does *not* say

- **It does not name the bug.** No `EPC1`, no `EXCCAUSE`, no `EXCVADDR`, no task
  identity. We know it was a fatal trap in PROC9's task context; we do not know
  which trap or which task.
- **It does not date the fault against the latch.** The crash section could have
  been armed by this fault long before the observed lockups. `sn200-red-team.md`
  §4.2 caveat (c) still stands.
- **It does not rule out a second, different mechanism** on the other four
  drives. Nobody has read their headers.
- **It is one drive.** The `+0x00`/`+0x40` read on the remaining drives is still
  the one cheap test that decides the question fleet-wide.

### What it *adds* — a new, testable candidate

PROC9 is reached from outside the NVMe data path entirely: **SMBus/MCTP
NVMe-MI**, i.e. the chassis BMC. A fault in that stack is consistent with
`sn200-field-evidence.md` row 2 — a latch on a healthy running drive with **no
power event and no shutdown** — without needing the deallocate path either.

Concretely worth checking, all non-invasive:

1. Whether the hosts' BMCs poll NVMe-MI over SMBus against these drives, at what
   rate, and whether the surviving/latched split correlates with it.
2. Whether `sn200-red-team.md`'s remediation list needs a fifth item: quiesce or
   disable out-of-band NVMe-MI polling.

### Revised grades

| Claim | Was | Now |
|---|---|---|
| The retrieved section is a full fault dump, not an `UNEXSTRT` stub | PROVEN | **PROVEN** (unchanged) |
| `+0x04 = 6` implies PROC6 / the CLEAN-PFAIL marker writer | SPECULATIVE | **REFUTED** — it is the vector index |
| The faulting core | unknown | **PROC9 (NVMe-MI/MCTP/SMBus), PROVEN** |
| The record is a saved call chain of windowed return addresses | INFERRED | **PROVEN**, and the frames are resolved |
| `EPC1` / `EXCCAUSE` are in the record | assumed | **PROVEN ABSENT** — the append starts past them |
| The fault class | unknown | **fatal synchronous exception**; `EXCCAUSE ∉ {4,5,15}`, PROVEN |
| These drives latched via an unfinished shutdown | INFERRED at best | **contradicted by the decoded record** |

---

## 7. Next steps, in value order

1. **Lift the 128 KiB retrieval ceiling** (`tools/nvme-noreset/max_admin_xfer_ids`)
   and pull **core 9's log blocks at `0x36500`**. That is where PROC9's own
   level-`0x20` assert record and the surrounding MI/MCTP traffic live. This is
   now the single most valuable read.
2. **Read `+0x00`/`+0x40` on the other four drives.** One 128 KiB read each;
   decides the fleet-wide question. Unchanged from the red team's list, still
   not done.
3. **Look for a second CDI slot.** Only `0x49444309` exists in this 128 KiB. Each
   core's slot offset is a hard-coded literal (`0x7ffa07e0` in PROC9); dumping
   that literal from every image gives the full slot map and tells us whether any
   other core also faulted.
4. **Recover the crash-context struct.** PROC9 `0x7ff83150` holds `EPC1` (`+0x70`),
   `EPC2..EPC6`, `DEPC`, `PS`, `EXCCAUSE` (`+0xc8`) and `EXCVADDR` (`+0xcc`). If
   any section dumps it, that section is where `EPC1` actually is. It is not in
   the first 128 KiB.
5. **Check BMC NVMe-MI polling** against the latched-vs-survived split.
