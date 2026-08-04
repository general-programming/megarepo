# Can the SN200 Post-Crash latch be DISABLED or RECONFIGURED?

Target: HGST/WDC Ultrastar SN200 `HUSMR7676BDP3Y1`, firmware `KNGND122`.

Recovery from the latch is nearly exhausted (marker 8, `Admin_VucFlashRead`,
host-side `LOAD_N_GO`, the `0x0603` branch). **Prevention is worth more**: a
healthy drive answers the full command set with no allow-list and no gate. This
document asks whether any host-reachable configuration can stop a healthy drive
from ever latching — and answers it.

**Verdict up front, and it is a negative.** No configuration reachable from the
host can disable the crash-dump capture, the boot-marker latch, or the
Post-Crash gate. The mechanism that forecloses it is stated in §5. **Three**
leads are still open and are named honestly in §4; none is currently actionable
and none should be attempted on hardware.

Labels: **PROVEN** = read off correctly-decoded instructions. **INFERRED** =
short chain over proven facts. **UNKNOWN** = not established.

Companion: `sn200-opcode-map.md` (the dispatch table this work fell out of),
`sn200-section-arming.md` (what arms CLOG), `sn200-shutdown-path.md` (§5 there
already covers host-side exposure reduction and is not re-derived here).

---

## 1. The latch machinery, restated as a chain — so we can ask where a switch could live

```
unclean stop
   -> shutdown does not reach the System Area save
      -> boot-marker record (EEPROM section 6) still holds marker 5/6/7
         -> next boot: marker dispatch -> UNEXSTRT stub writer (PROC0 0x7ffaad01)
            -> EEPROM section 11 (CLOG) written, 256 bytes           [ARMED]
               -> boot predicate 0x7ffaae35: ball bit0 / ball bit2
                  -> marker forced to 0x80000009
                     -> PROC0 tells PROC8 "startup type 6" over the IBQ
                        -> PROC8 *(0x7ff87c64) = 6
                           -> Admin_CheckCmdAllowed enforces the allow-list  [LATCHED]
```

There are exactly five places a "disable" switch could plausibly live. Each is
addressed below:

| # | candidate | §  | result |
|---|---|---|---|
| 1 | an NVMe Set Features FID | §2 | **negative** — 18 FIDs implemented, one vendor FID (`0xF0`), none crash-related |
| 2 | a policy/threshold deciding whether an unclean start arms CLOG | §3.1 | **negative — there is no policy input at all** |
| 3 | a non-`0xFF` writer of the startup-mode global `0x7ff87c64` | §3.2 | **negative — no host-reachable writer exists** |
| 4 | a non-`0xFF` writer of the boot-marker record (EEPROM section 6) | §3.3 | **negative for host commands** |
| 5 | a board/drive-configuration field | §3.4 | **no such field found; the section is host-writable but opaque** |
| 6 | a manufacturing / engineering / debug mode bit | §3.5 | **negative for the Post-Crash gate** |

---

## 2. Set Features (`0x09`) and Get Features (`0x0A`) — the sleeper, audited

`0x09` is allow-listed on a latched drive, standard, and had never been audited.
Dispatch (PROVEN, `sn200-opcode-map.md` §1):

```asm
7ffa7c0d: movi a8,8
7ffa7c0f: { extw ; bgeu a8,a11,0x7ffa7cbc }     ; op <= 8
7ffa7c17: bgeui a11,10,0x7ffa7c25               ; op >= 10
7ffa7c1a: l32r a11,-> 0x7ffaa628                ; op == 9  -> Set Features, RESIDENT
7ffa7c1d: { … ; j 0x7ffa7d2a }
...
7ffa7ca2: { extw ; bnei a11,10,0x7ffa75b4 }
7ffa7caa: l32i.n a12,a6,0x0
7ffa7cac: { l32r a13,-> 0x7ffbc92c ; movi a14,4 }   ; op == 10 -> Get Features, overlay 4
```

Set Features handler: **`0x7ffaa628`**, resident in `PROC8_7ff80000`.
Get Features handler: **`0x7ffbc92c` → static `0x300249a4`**, overlay 4
(`src2 = 0x30024078`).

### 2.1 Every implemented Feature ID — PROVEN

FID is `CDW10[7:0]`, read at `0x7ffaa720` (`l8ui a12,a13,0x80` with
`a13 = ctx+0x30`). The dispatch is a comparison tree, not a table; all 18 leaves
have the identical shape (load handler → `a13`, overlay index → `+0x20`, join
the tail at `0x7ffaa7ba`, enqueue via `0x7ffb9768`). Invalid FIDs exit at
`0x7ffaa6b4` with `0xC0040000` (Invalid Field).

Every one of the 18 handler pointers was resolved through the overlay
descriptor table and **18/18 landed on an `entry` (`0x36`) byte** — the same
validation test that certified the `0xC6` work.

| FID | handler (runtime → static) | ovl | what it controls | persists? |
|---|---|---|---|---|
| `0x01` | `0x7ffbc984` → `0x30023a7c` | 3 | Arbitration | **yes** |
| `0x02` | `0x7ffbc17c` → `0x30023274` | 3 | Power Management | **yes** |
| `0x03` | `0x7ffbc460` → `0x30023558` | 3 | LBA Range Type | **yes** |
| `0x04` | `0x7ffbca60` → `0x30023b58` | 3 | Temperature Threshold | **yes** |
| `0x05` | `0x7ffbcc88` → `0x30023d80` | 3 | Error Recovery | **yes** |
| `0x06` | `0x7ffbce00` → `0x30023ef8` | 3 | Volatile Write Cache — **a 36-byte stub that rejects the change** (StrId 2118 `SetFeat: VolWrCac dis. Chg rej.`) | no |
| `0x07` | `0x7ffbce24` → `0x30023f1c` | 3 | Number of Queues | no |
| `0x08` | `0x7ffbcee8` → `0x30023fe0` | 3 | Interrupt Coalescing | no |
| `0x09` | `0x7ffbc710` → `0x30024788` | 4 | Interrupt Vector Config | no |
| `0x0A` | `0x7ffbc85c` → `0x30028314` | 9 | Write Atomicity | **yes** |
| `0x0B` | `0x7ffbc904` → `0x300283bc` | 9 | Async Event Config | **yes** |
| `0x7E` | `0x7ffbc340` → `0x30027df8` | 9 | Controller Metadata (`SetFeat CtrlMdata`) | no |
| `0x7F` | `0x7ffbc340` → `0x30027df8` | 9 | Namespace Metadata — *same handler*, split by the namespace-specific attribute bit | no |
| `0x80` | `0x7ffbc9bc` → `0x30028474` | 9 | Software Progress Marker | **yes** |
| `0x81` | `0x7ffbcf38` → `0x3003f230` | 33 | Host Identifier | **yes** |
| `0x82` | `0x7ffbcbbc` → `0x30028674` | 9 | Reservation Notification Mask | no |
| `0x83` | `0x7ffbcc40` → `0x300286f8` | 9 | Reservation Persistence (PTPL) | **yes** |
| **`0xF0`** | **`0x7ffbca6c` → `0x30028524`** | **9** | **VENDOR-SPECIFIC — purpose UNKNOWN** (§2.3) | **yes** |

Everything else — `0x00`, `0x0C`–`0x7D`, `0x84`–`0xEF`, `0xF1`–`0xFF` — is
Invalid Field. **`0xF0` is the only implemented FID in the whole `0xC0`–`0xFF`
vendor range.** There is no "disable crash dump" feature, no "boot policy"
feature, no debug feature.

Get Features (`0x0A`) names nothing the setter hides: it implements exactly the
same FID set, differing only in that it *rejects* `0x06` (status `0x80040000`)
where Set Features stubs it. `SEL = CDW10[10:8]` is handled per-FID, not
globally; `SEL = 3` (supported capabilities) always routes to `0x300124ac`.

### 2.2 Persistence — how SV works, and what "saved" physically means

`0x7ffaa954`, the top of the Set Features body, gates on the save bit and then
on a per-FID saveability predicate:

```asm
7ffaa954: l32i a12,a2,0x130            ; CDW10
7ffaa95d: { s32i a13,a1,0x10 ; ball a12,mask 0x8000,0x7ffaa989 }
7ffaa965: l8ui a10,a10,0x30            ; FID
7ffaa968: call8 0x7ffba6f4             ; SetFeat_IsSaveable(FID)
7ffaa96b: bnez.n a10,0x7ffaa989
7ffaa975: l32r a10,-> StrId 2129 "SetFeat Id %d not Saveble - Cmd Abort"
```

`SetFeat_IsSaveable` (`0x7ffba6f4`) returns 1 for
**{1, 2, 3, 4, 5, 10, 11, 128, 129, 131, 240}** and 0 otherwise. A completely
separate classifier, `FeatureAttr` (`0x7ffaa434`, called at `0x7ffaa98e`),
produces bit 0 = saveable over a different comparison tree and yields the
**identical set** — two independent derivations agreeing exactly. (Its bit 1 =
namespace-specific = `{3, 5, 127, 130, 131}`, which is what splits `0x7E`
Controller Metadata from `0x7F` Namespace Metadata.)

The "saved" copies are not scattered globals: they all live inside **one
1280-byte NvConfig image based at `0x7ff8ffd8`**, signature `0x4E563031`
("NV01"), loaded at `0x3003ed4d` (StrId 2090 `Admin: SysArea NvConfig loaded`)
and written back through the System-Area transfer at `0x30028746` / `0x30029117`
with the same `{buffer = 0x7ff8ffd8, len = 1280}` descriptor.

**So `SV = 1` on a saveable FID does survive a power cycle, and the persistence
medium is the System Area** — the same EEPROM section (6) that holds the boot
marker. That is a notable adjacency but not a lever: the NvConfig image is a
distinct 1280-byte record inside the section, written through the transfer API,
and the marker bytes are not in it.

### 2.3 FID `0xF0` — the one vendor Feature ID, and it is silent

`0x30028524`, 336 bytes, **zero log strings anywhere in it**. What is
established:

- **Vendor-specific and persistent.** It is in `SetFeat_IsSaveable`'s set
  (240 = `0xF0`), so `SV = 1` commits it to the NvConfig image.
- **Per-port, 20-byte payload.** It DMAs 20 bytes host→DDR (`movi a12,20` at
  `0x30028650` and `0x3002855e`). Current copy at `0x7ff8fe28 + port*20`, saved
  copy at `0x7ff90420 + port*20`.
- **Applied live.** With `SV = 0` it takes a lock and calls `0x3001e250(port)`.
  With `SV = 1` it copies the 20 bytes into the NvConfig shadow.
- **Readable.** Get Features `0xF0` (`0x30024890`, overlay 9) allocates a buffer
  and DMAs it back — so on a *healthy* drive the current value can be read out
  non-destructively and inspected. That is the obvious next step and it is
  read-only.

**What it controls is UNKNOWN.** There is no string, no name, and nothing in
the string table describing it. `0x3001e250` is the lever that would say what
it does; decoding it, and the 20-byte structure, is unfinished work.

Is it *plausibly* the latch switch? No positive evidence either way. Against:
nothing on the arming/marker/gate path (§3) reads either
`0x7ff8fe28`-family address, so a value set here cannot reach the crash
machinery through the paths this document proved. For: it is the only
persistent vendor-configurable object in the firmware that the host can write
with a standard, allow-listed opcode. It is listed as an open lead in §4.3,
**not** as a prevention measure.

### 2.4 A decoder caveat that matters if you re-verify this

The repo disassembler renders the save-bit test as `ball a12,mask 0x8000`, but
the correlation between which branch writes the *current* table
(`0x7ff8f9b0 + port*4 + 8`, which Get Features `SEL = 0` reads) and which writes
the *saved* table (`0x7ff8ffdc`, which Get Features `SEL = 2` reads) proves the
**fall-through** is the SV-set path. Either the rendered mask or `ball`'s
r→bit-index model is wrong at these sites; `xdis.py`'s `ball` handling has only
ever been validated at `r ∈ {0, 2}`. Trust the correlation, not the mnemonic —
and treat "SV = `CDW10[31]`" as **INFERRED (strong)**, not PROVEN.

---

## 3. The configuration hunt

### 3.1 Is there any policy input to "an unclean start arms CLOG"? — NO

The arming decision is a two-instruction predicate with **no configuration
operand anywhere on the path**. PROC0 `0x7ffaae2d`:

```asm
7ffaae28: l32r a12,-> 0x7ff9ff60        ; boot-info block, written by the SBL
7ffaae2b: l32i.n a12,a12,0x4            ; a12 = BOOT MODE
7ffaae2d: { l8ui a9,a5,0x0 ; beqi a12,4,0x7ffaae53 }   ; mode 4 (LOAD_N_GO) skips both tests
7ffaae35: { extw ; ball a9,mask 0x1,0x7ffaaf02 }       ; CLOG armed -> latch
7ffaae3d: { extw ; ball a9,mask 0x4,0x7ffaaf02 }       ; PFCL armed -> latch
```

and the stub writer's own gate, PROC0 `0x7ffaad01`, is likewise a bare test of
the same flags byte ("is CLOG already armed?"), documented instruction by
instruction in `sn200-section-arming.md` §3.

**There is exactly one branch in the whole chain that is not a test of the crash
sections themselves: `beqi a12,4`.** That is boot mode 4, `LOAD_N_GO`
(StrId 86, "Firmware Boot Mode : LOAD_N_GO"). It is set by the **SBL**, from
the boot-info block at `0x7ff9ff60`, before any host command can be issued —
and it means "do not boot the resident image, wait for one to be pushed in over
UART". So even if it could be selected persistently, a drive in that mode does
not come up as a drive. It is not a prevention measure; it is the already-dead
host-side `LOAD_N_GO` lead wearing a different hat. **PROVEN negative.**

There is no threshold, no counter, no retry budget, no "arm after N unclean
starts". The capture is unconditional.

### 3.2 Writers of the startup-mode global `0x7ff87c64` — none reachable from the host

`0x7ff87c64` is referenced from **23 sites, all in `PROC8`** (`litref.py -v
7ff87c64`; no other image touches it). Sweeping every site for a following
store, **only two are writes**, and both are the same code shape:

```asm
7ffb014a: l32r a14,-> 0x7ff87c64
7ffb0148: l32i.n a13,a2,0x10                 ; a13 = message word +0x10
7ffb0157: { s32i a13,a14,0x0 ; … }           ; *(0x7ff87c64) = message word

7ffb0196: l32r a10,-> StrId 2051 "Admin_IBQCommandReceiver Startup Req MSGID 0x%x \n"
7ffb019c: l32r a15,-> 0x7ff87c64
7ffb019f: { l32i a14,a2,0x10 ; … }
7ffb01a7: { s32i a14,a15,0x0 ; … }           ; *(0x7ff87c64) = message word
```

Both live in **`Admin_IBQCommandReceiver`**, handling an *inter-processor*
"Startup Req" message. The value is copied verbatim out of the message body at
`+0x10`. The message comes from PROC0's System Manager, which computed it from
the boot marker.

**PROVEN: no host command writes the startup-mode global.** It is set once per
boot from an internal message. You cannot pre-set it to a non-6 value, and you
cannot clear it after the fact. Every one of the other 21 references is a read.

### 3.3 Non-`0xFF` writers of the boot-marker record (EEPROM section 6)

All System-Area section traffic in PROC0 goes through the submitter
`0x7ffb4fec` (33 call sites; `a11` = verb, `a12` = section id — see
`sn200-section-arming.md` §1). Sweeping all 33 for section `6`:

| site | verb | enclosing context |
|---|---|---|
| `0x7ffa74aa` | 3 erase | inside `0x7ffa71dc` (System Area manager) |
| **`0x7ffa88dd`** | **1 WRITE** | inside `0x7ffa8840` |
| `0x7ffa8b2d` | 3 erase | shutdown state machine |
| **`0x7ffa8d94`** | **1 WRITE** | shutdown state machine (same region as `0x7ffa8e64` `Shutdown Request Received`) |
| `0x7ffaaf58` | 2 read | boot marker dispatch |
| `0x7ffaafb5` | 0 probe / 2 read | boot marker dispatch |

**Two writers, both on the shutdown/System-Area-save path, neither reachable
from a host admin command.** The only host-reachable *mutation* of section 6 is
the `0xFF` `CDW12 = 0x0003` erase (OAM erase sub 0), which blanks it — and an
empty System Area is itself one of the latch predicates
(`StrId 3519 "SYS: Unexpected empty System Area."` at `0x7ffaae4a`, which falls
straight through to `0x7ffaaf08`, the marker-forcing site). So the one host door
into the marker record makes things strictly worse. **PROVEN.**

This is the same conclusion `sn200-marker-write.md` reached from the other
direction (the "marker 8" attempt); the section-6 sweep is independent
corroboration.

### 3.4 The SPI-EEPROM board/drive-configuration section

**It exists, it is read at startup, and it IS host-writable — but nothing in it
was found to govern crash or latch behaviour, and its field map is not in the
firmware.**

The EEPROM section-name enum (StrIds 1214–1228 — the same enum that gives
`System Area` = 6, `PFail Crash Dump` = 10, `Crash Dump` = 11, `SBL` = 13, all
independently confirmed elsewhere) names **section 1 = "Drive Configuration"**.

- **Read path.** PROC0 reads it (`0x7ffab8d9`, verb 2, section 1), around the
  `SYS: Read Drive Config` log (StrId 1289, `0x7ffab785`). PROC8 caches a
  "Drive Config flag" at **`0x7ff8f330`** and logs it: `0x7ffacb80`,
  StrId 2927 `"Drive Config read: flag 0x%x\n"`. The flag is used as a small
  enum, not a bitmask — the only test on it is `beqi a12,3` at `0x7ffacb8d`,
  which skips a block of table initialisation. **No crash/latch/dump branch
  reads it.** Six `l32r` sites total for `0x7ff8f330` (`litref.py -v 7ff8f330`);
  four are in the admin-startup reader, two are in DCMod.
- **Write path.** `Admin_VUC_Device_Config_Modify_OVL024` — "DCMod" — is
  **opcode `0xCC`, command byte `0x03`**, overlay 24. It is **PSID-gated**:

  ```
  StrId 1700  ADM: Admin_VUC_Device_Config_Modify_OVL024 - Access Denied. Enable access by with PSID
  StrId 1701  ADM: Admin_VUC_Device_Config_Modify_OVL024 - Access allowed, port Unlocked
  StrId 1703  DCMod no config loaded
  StrId 1705  Transfer change from host to DDR. Size in DW %d
  StrId 1707  DCMod changelist element bytecount %d, Offset %d data 0x%x
  StrId 1709  DCMod changelist error offset %d, bytecount %d, reserved area offset %d
  StrId 1711  DCMod write to SPI failed
  StrId 1712  DCMod SPI Schedule First Startup failed
  ```

  It takes an opaque **changelist** of (offset, bytecount, data) triples,
  bounds-checked against a "reserved area offset", writes it to SPI, and can
  additionally schedule a **First Startup**.

**Why this is not a prevention lever, three independent reasons:**

1. **The field map is not in the firmware.** DCMod validates offsets and byte
   counts; it does not know or name the fields. The semantic map lives in WD's
   host tool. Writing an offset you cannot name into the section that governs
   the drive's identity, capacity and boot behaviour is the definition of an
   unbounded risk, and one of the neighbouring strings is
   `"Resize: Activate failed, reboot is required"`.
2. **No crash/latch consumer.** The only firmware consumer of the config value
   found by an exhaustive `l32r` sweep is the `beqi a12,3` table-init branch
   above. Nothing on the arming, marker or gate paths reads drive config.
3. **PSID.** The unlock is a physical-security identifier, not something the
   host synthesises.

`0xCC` is **not** on the Post-Crash allow-list, so DCMod is a healthy-drive-only
command in any case — which is exactly what a prevention measure would want,
and is the one attractive property it has.

> The task brief cited `docs/sn200-sbl-decode.md` for the boot-mode step array.
> **That file does not exist in this repository.** The boot-mode value is read
> from `*(0x7ff9ff60 + 4)` (§3.1) and that block is written by the SBL before
> PROC0's marker dispatch runs; nothing further about how the SBL stages it was
> established here.

### 3.5 A manufacturing / engineering / debug mode that relaxes the gate — NO

An exhaustive string sweep for `manufactur|mfg|engineer|debug mode|diag mode|
diagnostic mode|test mode|factory mode|dev mode` over all 3616 strings returns
seven hits, and none of them is a gate relaxation:

| StrId | string | what it is |
|---|---|---|
| 406 / 2997 | `CC:/FCC: Test mode is not supported for this flash type [%d]` | NAND die test mode |
| 490 | `BGMS: Test mode command […] Status %d` | background media scan |
| 1217 | `Manufacturer Bad Block list` | EEPROM section 3's name |
| 2166 | `ADM: requesting CRYPTOMGR_SECURITY_ENGINEERING_BUILD` | build-flavour query |
| 2180 | `ADM: Engineering build` | build-flavour report |

The "test mode" family is `0xCA` sub `0x3A`/`0x3B`
(`Admin_VucFlashGet/SetTestModeRegister_OVL026`) and is a *NAND die* register,
not a controller mode. The engineering-build strings are reports, not setters.

**The one genuine mode setter in the firmware is `Admin_VUC_Enable`**, and it is
**opcode `0xEC`** (identified in `sn200-opcode-map.md` §6 by log-descriptor
cross-reference: handler static `0x3002b6c4` loads StrId 1920
`"ADM: Admin_VUC_Enable SUCCESSFUL. New State: %u"`). It controls the **VUC
Control** gate — the *second* of the four gates in `Admin_CheckCmdAllowed`,
whose reject string is StrId 1805 `"Admin cmd restricted by VUC Control
disabled: 0x%x"` (referenced from the dispatcher at `0x7ffa6d65`).

**That is a different gate from the Post-Crash gate.** The Post-Crash gate's
own reject string is StrId 1804, `"Admin cmd rejected due to Post Crash startup
mode: 0x%x"`, and its guard is the `bnei a8,6` on `*(0x7ff87c64)` — which §3.2
proves no host command can influence. Turning VUC Control *on* cannot turn the
Post-Crash gate *off*; they are serial, not alternative.

Full analysis of `0xEC` (parameter encoding, state values, persistence) is owned
by the parallel `0xEC`/allow-list work and is deliberately not duplicated here.
**If any prevention measure exists at all, `0xEC` is where it would be**, and
that is the single most valuable hand-off from this pass.

---

## 4. Two leads left open — named, not recommended

Both are **UNKNOWN**, both are on healthy drives only, and neither should be
sent to hardware on the strength of this document.

### 4.1 `0xD4`/`0xD7` command byte `0x06`/`0x07` — a host-commandable orderly power-off

`sn200-opcode-map.md` §5.1: this arm assembles and posts a message to the PCIe
manager and logs `StrId 1843 "Power Off type %d Message Sent to PCIe Mgr"`.
**PROVEN** that a host command reaches it.

Why it matters: the entire latch chain begins with "the shutdown did not reach
the System Area save". `sn200-shutdown-path.md` §5 already establishes that a
completed `CC.SHN` on an idle drive is the best available exposure reduction and
that it is not reliable under load. If this VUC drives a *different, more
deterministic* power-off sequence — one that always completes the SAM save — it
would be a genuine prevention step: issue it before every planned power removal.

Why it is not actionable: **UNKNOWN** whether the `type` argument is
host-supplied, what values are legal, and — decisively — whether this path runs
the System Area Manager save at all or simply cuts rails. A "power off" VUC that
skips the save would *cause* the exact unclean stop it was meant to avoid. The
handler is an 802-byte coroutine whose resume bodies are not yet mapped. Resolve
that before anyone types it.

### 4.2 `0xEC` `Admin_VUC_Enable`

See §3.5. Owned elsewhere. It is on the Post-Crash allow-list, which makes it
the only *mode setter* a latched drive will still accept.

### 4.3 Set Features FID `0xF0`

See §2.3. The only vendor Feature ID, persistent across power cycles via the
NvConfig image, 20-byte per-port payload, no strings, purpose UNKNOWN.

The **read** half is genuinely safe and genuinely informative: Get Features
`0x0A` with `FID = 0xF0` allocates a buffer and DMAs the current 20 bytes back
to the host with no state change. Doing that on a *healthy* drive — and
comparing the value across the fleet — costs nothing and would immediately say
whether the field is uniform (a factory constant) or varies (a real
configuration). **That is the single cheapest next experiment in this whole
investigation, and unlike everything else in this document it is a read.**
It is still not authorised here: `sn200-command-reference.md` is the document
that clears commands, and this one has not been added to it.

Do **not** write FID `0xF0` on any drive. A persistent, unnamed, per-port
20-byte configuration record applied live through an unanalysed function is
exactly the shape of thing that bricks a controller.

---

## 5. Why prevention is foreclosed — the one-sentence answer

**The crash capture has no configuration input.**

The boot predicate (`ball bit0` / `ball bit2` on the section-flags byte), the
`UNEXSTRT` stub writer (whose only gate is "is CLOG already armed?"), the
marker forcing (`marker := 0x80000009`) and the PROC8 startup-mode global
(written only from an internal IBQ message) form a closed loop with exactly one
external input — the boot mode, and its only special value is `LOAD_N_GO`, which
is not a state a working drive can be in. There is no feature ID, no drive-config
field, no mode bit and no host command anywhere on that path.

The consequence for operations is that **prevention on this drive is behavioural,
not configurational**: it reduces to never producing an unclean stop, which is
already what `sn200-shutdown-path.md` §5 and `sn200-runbook.md` §0 say. Nothing
in this pass changes what an operator should do, so **`sn200-runbook.md` is left
unmodified** — deliberately, per the brief's instruction to touch it only on a
positive result.

---

## 6. What was checked and found clean (so nobody re-runs it)

| avenue | method | result |
|---|---|---|
| Set/Get Features FIDs | full dispatch enumeration, §2; 18/18 handlers validated to land on an `entry` byte | 18 FIDs, 1 vendor (`0xF0`), none crash-related |
| policy/threshold on CLOG arming | instruction-level read of the whole predicate + stub gate | no operand exists |
| writers of `0x7ff87c64` | `litref.py -v 7ff87c64`, all 23 sites, store-sweep each | 2 writers, both internal IBQ |
| writers of EEPROM section 6 | all 33 `0x7ffb4fec` call sites, verb+section decoded | 2 writers, both shutdown-path |
| drive/board config field governing crash | section-name enum + `litref.py -v 7ff8f330` (6 sites) + DCMod strings | section is writable, no crash consumer |
| mfg/engineering/debug mode | full string-table sweep | 7 hits, none a gate relaxation |
| a second dispatcher hiding a config command | full admin dispatch enumeration (`sn200-opcode-map.md`) | none; the 7 newly-found vendor opcodes are all non-allow-listed |
