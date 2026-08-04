# SN200 firmware: how it actually works

How the HGST/WDC Ultrastar SN200 (`HUSMR7676BDP3Y1`, firmware `KNGND122`)
boots, shuts down, latches, and refuses to come back — as a single narrative.

The detail lives in sibling docs; this is the map that makes them navigable:

| Doc | Covers |
|---|---|
| `sn200-memory-map.md` | what is core-local vs shared, and why isolation holds |
| `sn200-xtensa-isa.md` | instruction encodings, FLIX slots, decoder corrections |
| `sn200-command-reference.md` | every command, encoding, gating, safety class |
| `sn200-firmware-re.md` / `sn200-independent-re.md` | the two independent teardowns |
| `sn200-shutdown-path.md` | why the shutdown fails to finish |
| `sn200-readonly-startup.md` | marker 8, and why nothing writes it |
| `sn200-logic-escapes.md` | the one route out (UART) |
| `sn200-dangerous-commands.md` | commands that destroy a drive |
| `sn200-field-evidence.md` | what was actually observed on hardware |

Everything below is PROVEN from code unless marked otherwise.

---

## 1. The machine

Not one CPU. **18 Tensilica Xtensa images**, each its own core with its **own
address space** — which matters more than it sounds: a memory-write primitive on
one core cannot reach another's state. That single fact closed most of the
offensive avenues, and it is **PROVEN** — `0x7ff80000` is a self-aliased 512 KiB
slot of the SoC unit grid, not a shared window (`sn200-memory-map.md`).

| Image | Role |
|---|---|
| `PROC0` | system manager, boot/startup, PFAIL ISR, crash sections, `DiagMgr>` UART |
| `PROC6` | System Area save — writes the CLEAN/PFAIL "finished" marker |
| `PROC8` | admin command dispatch, gates, namespace startup, firmware update |
| `PROC9` | PCIe, NVMe-MI/MCTP, RSA public keys |
| `PROC12` | NAND event log |
| `PROC1-5,7,10,11,13-15` | data path — write buffer, V2P, GC, NAND scheduling |
| `FCC` | uses the **call0 ABI**, not windowed — no `entry` prologues at all |

Code is delivered as `.BIN` containers of `.SEG` segments. `PROC8` additionally
has an **overlay bank**: linked at `0x300xxxxx`, executed from `0x7ffbc000`, so
`static = ddr_src + (runtime − 0x7ffbc000)`. Some overlay pages are not in the
dump at all, which is why ~99% of that bank's call targets are unresolvable.

---

## 2. The one predicate that ruins everything

At boot, PROC0 runs two tests — one per crash section — and **either one** forces
the same outcome:

```mermaid
flowchart TD
    A[power on] --> B{crash section bit 0<br/>CLOG set?}
    B -- yes --> F[force marker 0x80000009]
    B -- no --> C{pfail section bit 2<br/>PFCL set?}
    C -- yes --> F
    C -- no --> D{System Area empty?}
    D -- yes --> F
    D -- no --> E[normal startup]
    F --> G[startup type 6 = INVALID<br/>'Post Crash Startup']
    G --> H[no namespace presented]
    G --> I[admin gate restricts commands]
    G --> J[AEN every ~5s ⇒ host resets controller]
    J --> K[every reset is another unclean start]
    K --> B
```

Sites: `0x7ffaae35` and `0x7ffaae3d` both branch to `0x7ffaaf08`, which writes
`0x80000009`. Bit mapping proven three ways (TOC `0x7ff84a70`, producer
`0x7ffb461c`, consumer `0x7ffab010`): **bit 0 ⇒ section `0x0b` CLOG**, **bit 2 ⇒
section `0x0a` PFCL**. Flags byte at `0x7ff8d200`.

**The loop is self-sustaining.** The AEN makes Linux reset the controller every
~5 s; each reset is itself an unclean start; each unclean start re-arms the
section. The host is helping hold the drive down.

**Startup type 6 is literally named `INVALID`.** `NORMAL` is 1. That naming
matters — see §6.

---

## 3. Shutdown: markers are breadcrumbs, not verdicts

The most-misread part of this firmware. A shutdown writes **"STARTED" first**,
then runs a work list, and only *afterwards* writes "FINISHED".

```mermaid
flowchart TD
    A[CC.SHN from host<br/>or PFAIL from VMON] --> B[write STARTED marker<br/>5, 6 or 7]
    B --> C[work list:<br/>flush write buffer, L2P/journal,<br/>quiesce GC, save CellCare]
    C --> D{finished inside<br/>25 ms?}
    D -- yes --> E[PROC6 0x7ffbba61 writes<br/>marker 1 CLEAN or 2 PFAIL]
    E --> F[next boot: normal]
    D -- no --> G[thread exits.<br/>STARTED marker is what remains]
    G --> H[next boot sees 5/6/7<br/>= 'began, never finished']
    H --> I[write UNEXSTRT stub into CLOG]
    I --> J[latched — see §2]
```

So markers **5** `Normal Shutdown STARTED`, **6** `PFAIL Shutdown STARTED` and
**7** `PFAIL Shutdown TIMEOUT` are not error codes. They are the opening
breadcrumb, left behind when the rails collapse mid-list.

**The budget is 25 ms** — `0x7ff830e0 = 25000`, scaled by cycles-per-µs against
CCOUNT, units pinned by `SYS: PFAIL time = %5u.%03u ms`. At expiry the supervisor
does **not** force completion: it writes marker 7 and **exits**. It is a
stopwatch that labels the failure, not a hold-up guarantee.

### Why the list doesn't finish — three independent defects

1. **No admission control.** The work simply exceeds 25 ms.
2. **The lost-PFAIL race.** `PCIe_PfailShutdown` finds the port already claimed
   by link-down's bottom half and logs *"already shut down or in the process of
   shutting down - Do nothing"*. This is WD's documented race — and it is **not**
   interrupt masking: PFAIL is PROC0-only, so PROC9 only ever sees a message.
3. **Re-arming mid-shutdown.** The System Manager re-enables PFAIL monitoring
   when SAM hasn't yet published completion, and the deadline is anchored to the
   **latest PFAIL edge, not the start of the shutdown**.

Plus two *unconditional* silent exits where the monitor logs "PFAIL is detected",
exits, and initiates no shutdown and no marker at all.

**Exposure scales with workload — provably.** Fixed 25 ms budget against live
counters: outstanding NAND writes, write-buffer entries, in-use command
contexts, pending V2P, broken deallocates, plus a CellCare save. A busy drive
latches; an idle one doesn't. This is why a whole-device TRIM appeared causal —
it is the peak-current, peak-dirty-state workload, not a special code path.

**`CC.SHN` is necessary but not sufficient.** The NVMe shutdown path writes only
marker 5; CLEAN comes from a *different* routine after the flush completes. An
acknowledged shutdown whose flush doesn't finish latches exactly like a power cut.

---

## 4. Recovery, and why it costs the data

```mermaid
flowchart TD
    A[latched drive] --> B[0xFF CDW12=0x0503<br/>clear CLOG]
    B --> C{startup type<br/>already 6?}
    C -- yes, i.e. latched --> D[schedule marker 3<br/>Drive REINIT]
    C -- no --> E[erase only, no re-init]
    D --> F[next boot: startup type 0 = FIRST]
    F --> G[blank SA directory]
    G --> H[memset BOTH LBN translation tables<br/>region map to 0xffff]
    H --> I[namespaces created fresh<br/>V2P restore skipped]
    I --> J[drive healthy, media zeroed]
```

The gate is `bnei a14,6`: **`0x0503` schedules the wipe only when the drive is
already latched.** Fired from a normally-booted drive it erases the section
harmlessly — which is circular and therefore useless, since a set bit forces the
latch which forces mode 6.

`0x0603` (PFCL) is synchronous and schedules nothing — but `UNEXSTRT` stamps
**CLOG**, so it cannot release a power-event latch.

**Firmware activation is not a safe alternative.** Marker 3 is written gated on
**bit 0 of the target image's own flags word**, so whether an activation wipes is
a property of the image in that slot, not of the commit action. `--action=2` is
not a pointer flip. Its only genuine advantage is using no vendor opcodes.

---

## 5. The admin gate

```mermaid
flowchart LR
    A[admin command] --> B{startup mode<br/>== 6?}
    B -- no --> Z[normal dispatch]
    B -- yes --> C{opcode in<br/>allow-list?}
    C -- yes --> Z
    C -- no --> D[reject 0x8F8A0000<br/>→ 0x47C5 → 0x7C5 on the wire]
```

`PROC8 0x7ffa6b18` hosts **four** gates — Post-Crash, VUC Control, sanitize, and
one more. That is why two independent teardowns disagreed for hours: each was
reading a different gate.

Post-Crash gate (`0x7ffa6b30`–`0x7ffa6bd8`) is an **allow-list**:
`0x00 0x01 0x02 0x04 0x05 0x06 0x08 0x09 0x0A 0x0C 0x10 0x11 0xE6 0xEC 0xFF`,
plus `0xC6` when `a4 ∈ {0x20,0x30}`, plus `0xCA` with a 12-entry sub-list.

Two consequences:

- **Firmware Download and Commit are permitted while latched** — which is why
  slot-filling and activation work on a dead-looking drive.
- **So are raw NAND erase (`0xCA` `0x0F`) and raw page write (`0xCA` `0x10`).**
  A latched drive will destroy itself on one well-formed command. See
  `sn200-dangerous-commands.md`.

---

## 6. Why there is no software way out

Four independent walls, each proven separately:

| Wall | Evidence |
|---|---|
| **Firmware images are signed** | RSA-2048 over SHA-256; three public keys compiled into PROC9. `SECURITY.bin` is a *copy of the key blob*, not a signature — which is why it is revision-invariant |
| **The mode word is unwritable** | `0x7ff87c64` has two writers, both on an inter-processor message path with zero callers from any NVMe command |
| **No memory-safety bug** | 16/17 allow-listed opcodes audited clean — and both targets worth writing are in *other cores' address spaces* anyway |
| **Marker 8 has no writer** | the sole marker setter emits only 3 and 4 |

Marker **8 `READ ONLY`** is the tantalising one. It is *not* a degraded mode: SAM
sets one flag bit and falls into the **normal** boot path — L2P restored,
namespace present, writes refused at the admin/IO layer. Exactly what you'd want
to rescue data. And because the gate is a bare `bnei a8,6`, startup type 3
**sails straight through it**.

But no firmware code writes it. The marker is **persistent state** — word 0 of a
244-byte record in EEPROM System-Area section 6, two redundant copies, the
dispatcher healing the primary from the secondary.

### The one route out (INFERRED where noted)

```mermaid
flowchart TD
    A[DiagMgr UART<br/>115200 8N1] --> B[SYS SBL]
    B --> C[SBL console]
    C --> D[boot mode 4 LOAD_N_GO]
    C --> E[write 0x80000008 to<br/>EEPROM SA section 6 copy 0 word 0]
    D --> F[beqi a12,4 at 0x7ffaae2d<br/>jumps over BOTH ball tests<br/>and the empty-SA door]
    E --> G[startup type 3 READ ONLY<br/>ungated, L2P intact]
    F --> H[drive boots]
    G --> H
```

Every firmware link is proven; **only the UART pinout is unknown** — PROC0 has no
UART MMIO or pinmux, so the firmware cannot tell you the pins.

In that console: never type `I2CErase` (destroys FRU/VPD) or `LogicTrap`
(deliberate crash), and set exact name matching — under flexible matching a bare
`S` resolves to `SBL`.

---

## 7. What this means operationally

- **A latched drive still has its media.** The metadata is destroyed only by the
  re-init, and that is *our* action, not the fault. A drive left powered down
  keeps every option open.
- **Prevention beats recovery, but is not absolute.** Orderly shutdowns collapse
  the dominant term (workload scaling) but defects 2 and 3 and both silent exits
  sit on the shared path, and `CC.SHN` is downgraded to a PFAIL shutdown if the
  rails sag mid-run. Best lever order: stop deallocates/TRIM → `sync` + unmount →
  `CC.SHN` and **wait** → only then cut power.
- **`KNGND122` is terminal.** Nothing newer exists anywhere, and it was still
  fixing this defect family when it shipped. WD marks the family
  *"unable to recover"*.
- **A latched drive is harmless to its host** provided nothing targets it — 21
  controller resets were logged while `udevd` and `kubelet` stayed healthy. It
  only wedged the node when a volume manager tried to partition it.
