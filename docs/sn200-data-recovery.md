# SN200 — getting the data off a latched drive without wiping it

Target: HGST/WDC Ultrastar SN200 `HUSMR7676BDP3Y1`, firmware `KNGND122`
(terminal revision), HHHL add-in-card and U.2. Five drives, all EOL, no vendor
support.

**Success criterion: user data read out intact. Not "drive fixed".**

Companions: `sn200-logic-escapes.md` (the escape analysis this builds on),
`sn200-readonly-startup.md` (the marker-8 disproof), `sn200-command-reference.md`,
`sn200-nondestructive-recovery.md`, `sn200-dangerous-commands.md`.

Every claim below is labelled **PROVEN** (decoded from the image, from a function
entry), **INFERRED** (consistent with the evidence, one step of reasoning), or
**SPECULATIVE** (a guess, flagged so you can price it).

---

## 0-. PRACTICALITY VERDICT — read before spending any more effort here

**This procedure is not deployable in our environment, and the pinout was never
the real blocker.** Decided 2026-08-04.

These drives live in Dell hot-swap caddies, in a server, in a colo. The caddy
encloses the drive completely and has no cable exit. So even a *perfect* pinout
buys a procedure that requires: pulling the drive, getting it onto a bench,
soldering to sub-millimetre pads, a level shifter, and keeping that rig powered
and wired for the entire read of up to 7.68 TB. There is no version of this that
happens with the drive in a server.

That makes the route **strictly dominated**. Once you have accepted "pull the
drive and put it on a bench", you are already at the effort and logistics of
sending it to a recovery lab — who have a fixture, a clean bench, and PC-3000.
Doing it ourselves at that point is the same cost with worse tooling.

**Consequence for where effort goes:** the only recovery worth building is one
that runs **over NVMe, in-situ, with the drive in the server**. That is the sole
criterion by which to judge any future lead. Keep this document as the reference
for what the SBL and boot path actually do — much of it is load-bearing for
other analysis — but stop treating the console as a recovery plan.

Retained because it is still true and still useful: `MemWrite 0x7ff9ff64 4`
**does** stick (§8.1 disproved the "SBL rewrites it at handoff" risk), so if a
drive ever *is* on a bench for another reason, the procedure below is sound.

---

## 0. Read this first — the shape of the answer changed

Three findings in this document alter the plan that `sn200-logic-escapes.md` §11
recommends. They are new, and two of them are bad news.

1. **The SBL/MBL diagnostic console has no SPI *write* command.** Its published
   help text offers `ReadSpi` (read), `EraseSystem` (erase the whole SPI EEPROM —
   catastrophic), and `EepWrDdrTrainData` (writes the DDR-calibration area only).
   There is no `WriteSpi`, no offset-addressed EEPROM write. **PROVEN** from the
   `SBLPATCH.bin` help strings; see §4.2.
   → **The "write `0x80000008` into EEPROM System-Area section 6" step has no
   known console command to execute it.**

   And worse: **on a crash-latched drive marker 8 would not work even if you could
   write it.** Whenever either crash bit is set, `0x7ffaaf02` forces the marker to
   **9** before the dispatch re-runs — an 8 is discarded on the way in. **PROVEN.**
   Marker 8 only functions in combination with something that already skips the
   crash test, i.e. with `LOAD_N_GO`, which defeats the purpose. The brief's
   preferred option is, for these drives, dead.

2. **The escape is per-boot, not a repair.** The crash-section latch is a byte the
   boot scanner fills **from media** every cold boot (`0x7ff8b4f8`, set at
   `0x7ffaac38`). `LOAD_N_GO` jumps over the test; it does not clear what is on
   media. **Every power cycle re-arms the trap, so the console procedure must be
   redone for every boot.** **PROVEN** (§2.2 of logic-escapes).
   → Plan the data copy as one long window with resumable tooling.

3. **`LOAD_N_GO` does not give you a read-only drive — it gives you a fully
   normal one.** Boot mode 4 lands in startup mode **0**, the same mode a healthy
   drive uses: writes accepted, garbage collection running, and a background
   thread that deliberately **writes the running firmware image into EEPROM slots
   1–5** (`0x7ffa376f`..`0x7ffa37a3`). **PROVEN.** Freezing the drive is then the
   *host's* job, not the firmware's.

Net: the executable route is `LOAD_N_GO` + host-enforced read-only. The
firmware-enforced read-only route (marker 8) is blocked on a write primitive
nobody has found yet, and probably needs an SPI clip and a CRC we have not
reversed.

---

## 1. What we are escaping, and why the obvious fix is the wrong one

A latched SN200 comes up in `POST CRASH Startup` (startup mode 6). Symptoms seen
in the field (`sn200-field-evidence.md`): `state=resetting`, **no namespace**,
GPT and filesystem apparently gone.

They are not gone. Mode 6 refuses to bring the namespace up; it does not touch
NAND. The L2P tables and the media are intact. **PROVEN.**

The vendor-shaped fix — OAM crash-dump erase, `0xFF` with `CDW12=0x0503` —
schedules a **Drive REINIT** on the next boot, which blanks the System-Area
directory and `memset`s both LBN translation tables. That is the wipe. It is
allow-listed while latched, so the drive *will* accept it. **PROVEN.**

Two hex nibbles away, `0xFF` / `CDW12=0x0403` is **Drive Uninit**, which is
*ungated* and selects the **FACTORY** re-init marker. Strictly worse. **PROVEN
ungated; INFERRED that it is the factory variant.**

So: nothing that clears the latch in band is non-destructive, and the two
commands that look like the fix are the wipe.

---

## 2. The physical layer — finding the UART

This is the blocker. `PROC0` genuinely cannot answer it: it has no UART MMIO and
no pinmux, and its character I/O goes through a hook table
(`SYSservices+0x3c/+0x40/+0x48`) filled in at runtime by the boot loader.
**PROVEN.**

### 2.1 What the firmware and the loader *do* tell us

| fact | source | label |
|---|---|---|
| Console runs **115200 baud**, 8N1 assumed | `0x7ffb1b7b` loads literal `0x0001C200` = 115200 and passes it to `0x7ffb4ad0` | **PROVEN** (the callee ignores the argument, so the divisor is set elsewhere — the *number* is still the compiled intent) |
| The **UART driver lives in the SBL, not the firmware** | `SYSservices` block (initial image shipped in `SBLPATCH.bin` for `0x7ff9ff60`) has putchar/getchar hooks at `0x7ffb81f8`, `0x7ffb885c`, `0x7ffb8278` — all inside the SBL code segment `0x7ffb6000`–`0x7ffbfab0` | **INFERRED** (high — 22 consecutive pointers all land inside the declared SBL range, and the offsets match the three PROC0 uses) |
| Consequently **`DiagMgr>` and the SBL console share one physical UART** | one driver, one register block, both consoles call through it | **INFERRED** (high) |
| Consequently **the line is alive from power-on**, before firmware | the MBL prints `(info): MBL: Starting ...` and the SBL prints `(info): SBL: Starting (cold boot) ...` from that same driver | **PROVEN** (strings) / **INFERRED** (same driver) |
| UART register block is at **`0x81860000`** | `MBL` code holds literals `0x81860000`, `0x81860004`, `0x81860008`; `PROC0` holds `0x81860030`; the EEPROM boot script's 4th record writes `0` to `0x8186000c` before anything else runs | **INFERRED** (medium-high) |
| There is a UART error path | string `(err): Sending suspicious value 0x%x to UART` | **PROVEN** |
| The ASIC is a **PMC-Sierra / Microsemi**-lineage part, internally "Omaha" | `SBLPATCH.bin` magic is `PMCSEEPM001`; the string table header reads `### Omaha StringTable ###`; the console exposes `CARRead/CARWrite <NodeID> <offset>` — PMC's Configuration-Access-Ring addressing, not a Marvell idiom | **PROVEN** (artefacts) / **INFERRED** (vendor lineage) |
| There is an **I²C GPIO expander** on the board | `(info): MBL: ERROR - Failed to access GPIO extender` | **PROVEN** |

**Correction to the brief:** treat "Marvell ASIC" as unsupported. The tooling and
idioms in the boot loader are PMC-Sierra/Microsemi Flashtec-family. This matters
for the search: look for Flashtec / PMC NVMe controller reference-design material
and PMC-era HGST SN100/SN150/SN200 boards, not Marvell SSD teardowns.

### 2.2 Published sources — there are none, and that is a finding

A deliberate sweep of teardowns, FCC filings, data-recovery forums, Chinese
hardware forums and commercial DR tooling produced **no pad map, no pinout, no
baud rate and no annotated PCB photo for any `HUSMR76xx` part, in any language.**
This is a confident negative, not a shallow search. Nobody has published
reverse-engineering work on this drive family. Plan on original work.

What the sweep *did* produce, in descending usefulness:

- **The SN100 2.5" teardown is the one genuinely useful source.**
  https://www.thessdreview.com/our-reviews/hgst-ultrastar-sn100-enterprise-nvme-ssd-review-3-2tb/
  opens the drive: two stacked PCBs, photos of the bare controller board, three
  debug LEDs (heartbeat / activity / fault) — and **an external USB connector on
  the drive body labelled "for factory use only".** That is the most concrete
  debug ingress documented anywhere on this family. **Look for it on the SN200
  before you reach for a soldering iron.** Caveat: the SN100 is a PMC-Sierra /
  Microsemi **Flashtec 89HF16P04CG3**, a generation earlier. Whether the SN200
  keeps the connector is **SPECULATIVE**.

  ⚠ Note how well that corroborates §2.1: the SN100 is a *confirmed Flashtec*
  part, and the SN200's boot loader is a `PMCSEEPM001` image with PMC `CAR`
  addressing. Two independent lines of evidence, same vendor lineage. Search for
  Flashtec material, not Marvell.

- **TheRetroWeb's SN200 AIC entry** —
  https://theretroweb.com/expansioncards/s/western-digital-ultrastar-dc-sn200 —
  covers `HUSMR7664BHP301` and siblings. The site is built from user-submitted
  high-resolution board scans and returns 403 to fetchers. **This is the single
  best candidate for a real SN200 PCB photo — open it in a browser.**

- **FCC internal photos are structurally unavailable.** Bare NVMe SSDs are
  unintentional radiators authorised under SDoC, which assigns no FCC ID and files
  no exhibits. HGST/Hitachi GST's only fccid.io filings are wireless products.
  Do not spend time here.

- **The closest forum datapoint is a warning.**
  https://forum.hddguru.com/viewtopic.php?t=44304 — "WD Sandisk SSD, NVMe SA5xx —
  any UART mode or something?" (May 2024). The poster found **four test points,
  all unpowered**, scoped them, got nothing, and got no replies. **WD may strap or
  fuse the UART off in production.**

- **OCP's Datacenter NVMe SSD Spec v2.5 §SEC-18 requires exactly that:** *"All
  debug ports shall be disabled before the device leaves the factory."* The spec
  contains zero occurrences of "UART", "JTAG" or "serial console".
  **INFERRED risk:** even a correctly-identified pad may be dead in production
  firmware. The counter-evidence in our favour is that this firmware's console
  task is enqueued unconditionally with no gate (**PROVEN**), so the *software*
  side is open; only the pad/strap could be closed.

- Bot-blocked but likely to contain board photos: Zhihu SN150 AIC teardown
  https://zhuanlan.zhihu.com/p/63073440, Chiphell SN260 thread
  https://www.chiphell.com/thread-1854602-1-1.html.

- **PC-3000 does not help.** ACE Lab's published SSD support is SATA-era Marvell,
  Silicon Motion and Phison; their NVMe work goes through the **NVMe protocol in
  vendor diagnostic mode, not a UART**, so a pad map was never their deliverable.
  NVMe needs Portable III. Nothing indicates Ultrastar enterprise coverage.
  https://www.acelaboratory.com/pc3000-SSD.php

- **Prior art on what an SSD debug pad looks like**, and the source of the 1.8 V
  number: VoidStar found **nine vias along the outside edge of the PCB**, measured
  **1.8 V**, and needed a level shifter — https://voidstarsec.com/blog/jtag-ssd.
  Pen Test Partners found a populated header plus a suspected-JTAG header and a
  bootloader console at **38400** baud —
  https://www.pentestpartners.com/security-blog/walkthrough-investigating-an-ssd/

- **Software-only prior art for this exact drive**, worth reading before any
  hardware: https://forum.level1techs.com/t/hgst-wdc-ultrastar-sn200-recovery-from-persistent-internal-error-diagnostic-state/250303
  and the WD Device Manager user guide
  https://documents.westerndigital.com/content/dam/doc-library/en_us/assets/public/western-digital/collateral/user-guide/user-guide-essd-device-manager.pdf

**Search order for the owner:** (1) look for the SN100-style factory USB connector
on the SN200 body; (2) TheRetroWeb photos in a browser; (3) open the drive and
photograph it — that becomes the first public artefact for this family.

### 2.3 How to find the pads without damaging anything

Order matters. Do all of this **with the drive unpowered** unless a step says
otherwise.

1. **Photograph both sides of the PCB at high resolution first.** You get one
   free non-destructive artefact and it is the thing to post if you need help.
2. **Look for a factory connector before you look for pads.** The SN100 carries an
   external USB connector on the drive body labelled *"for factory use only"*
   (§2.2). If the SN200 kept it, that is the debug ingress and no soldering is
   needed. Also note the three debug LEDs (heartbeat / activity / fault) — a
   heartbeat LED tells you the controller is alive even when NVMe is not.
3. **Find the candidate pad group.** Two shapes both occur in published SSD work:
   a row of 3–5 bare pads or an unpopulated 0.1"/2 mm header **near the
   controller** (often between the ASIC and the card bracket, one square pad for
   pin 1), **or a row of bare vias along the outside edge of the PCB** — VoidStar's
   find was nine such vias. On U.2 boards a header is frequently tucked next to
   the SFF-8639 connector. Silkscreen may read `TX/RX/GND`, `J*`, `DBG`, `UART`,
   or nothing at all. A 2×2 group is also common.
   ⚠ **Expect the possibility that the pads are dead.** The nearest published
   attempt on a WD/SanDisk NVMe found four test points, all unpowered (§2.2), and
   OCP SEC-18 requires debug ports to be disabled in production. If every
   candidate is inert at power-on, that is a real answer, not a probing mistake.
4. **Identify GND first, unpowered**, with a multimeter in continuity mode against
   the PCIe bracket / a connector ground pin / a bulk-cap negative terminal. Any
   pad that beeps to ground is GND. **Never guess GND.**
5. **Identify VCC and eliminate it.** Power the drive on a bench supply with
   nothing else attached and measure each remaining candidate pad to GND with a
   DMM. A pad that sits rock-steady at 1.8 V or 3.3 V with no activity is a
   supply rail — mark it and stay off it.
6. **Identify TX.** With the drive powered, TX **idles high** at the logic level
   and *dips* at power-on as the MBL banner goes out. A DMM on a slow average
   will read slightly below the rail for a moment at boot; a scope shows the burst
   outright. If you have a scope, this is a 10-second job: trigger on a falling
   edge at power-on, look for a ~8.68 µs bit time (115200 baud).
7. **Read the voltage off TX's idle level — this is your logic level.** ⚠ This
   step is not optional. See §2.4.
8. **RX is the remaining pad.** Do not drive it until TX has been confirmed and
   you have a level-matched adapter. RX is usually weakly pulled high or floating.
9. **Connect GND and TX only, first.** Get the banner. Only then wire RX.

**Do not** connect anything to a candidate pad while the drive is powered and you
have not yet identified GND. **Do not** use a USB-serial adapter's VCC pin at all.

### 2.4 ⚠ Logic level: UNKNOWN. Assume 1.8 V until measured.

**SPECULATIVE, and this is the sentence to take seriously.** A 2016–17 vintage
enterprise NVMe ASIC's general-purpose I/O is far more likely to be **1.8 V** than
3.3 V, and the SN200's I/O rails are not documented anywhere we found. A 3.3 V
FTDI/CP2102 adapter driving RX on a 1.8 V pin can latch up or destroy the pad, and
there is no vendor support to repair it.

The supporting evidence is indirect but consistent: the only published SSD debug
pads anyone has measured (VoidStar, §2.2) were **1.8 V and needed a level
shifter**. 3.3 V is the older HDD-era convention (Seagate/Samsung terminals), not
the NVMe-ASIC one. **Assume 1.8 V. Damaging a 1.8 V pad with a 3.3 V FTDI is the
classic way to lose this drive.**

Rules:

- **Measure the idle level of TX with a DMM before connecting anything.** Idle
  high ≈ the logic rail. 1.8 V and 3.3 V are unmistakable on a meter.
- Use an adapter with a **selectable VCCIO** (FTDI FT232H, FT2232H, or a
  Bus Pirate) and set VCCIO from the measured level — never from a jumper labelled
  "3V3" by default.
- If in doubt, put a **level shifter** (e.g. TXS0108E) in line, or at minimum a
  1 kΩ series resistor on the adapter's TX→drive-RX line, which limits fault
  current without preventing communication.
- Reading TX-only into a 3.3 V adapter is electrically safe (a 1.8 V high still
  reads as a logic 1 on most 3.3 V receivers, though it is marginal). **So do the
  RX-free listen-only test first** — it costs nothing and confirms the pinout,
  the baud, and that the console is alive on a latched drive.
- **Baud order to sweep:** `115200` first — it is the compiled constant in this
  firmware (**PROVEN**) — then `38400` (the rate on the one published SSD
  bootloader console), then `57600`, `9600`, `19200`, `921600`. HGST's own patent
  language for factory out-of-band interfaces cites 19.2 kbps–1 Mbps, so the whole
  range is plausible for the *loader*, which may not use the firmware's constant.

### 2.5 U.2 / SFF-8639 — no, and the spec forbids it

Settled, from SFF-9639 Rev 2.2 (the pinout guide; SFF-8639 itself has been
mechanical-only since Rev 1.7) and SFF-TA-1001 Rev 1.1:

- **SMBus is `E23` (SMBCLK) / `E24` (SMBDAT)** — not E5/E6, which is a common
  mis-citation. `DualPortEn# = E25`, `PERST# = E5`, `REFCLK± = E7/E8`,
  `+3.3Vaux = E3`, `PRSNT# = P10`, `PWRDIS = P3`.
- **Reserved pins are `P2`, `S15`, `E6`, `E16`. Vendor-specific pins in the
  PCIe/NVMe columns: zero.**
- SFF's own definition of Reserved is decisive: *"its actual function is set aside
  for future standardization. **It is not available for vendor specific use.**"*

So there is no standardised serial debug pair, and the reserved pins are
standards-prohibited from carrying a vendor UART. No vendor — Intel/Solidigm,
Samsung, Micron, Kioxia, Seagate, WD/HGST/SanDisk — documents one. **This route is
closed.** (EDSFF tells the same story with a named `MFG = A6` pin that vendors are
told to disable.)

**One spec-sanctioned lever, cheap to try, low expectation.** SFF-TA-1001 §4.2.4
Table 4-4: grounding **`S15` + `E16` + `E25` simultaneously** selects
**Manufacturing Mode**, defined verbatim as *"vendor specific and shall only be
used during device manufacturing. Device suppliers shall disable this mode after
manufacturing."* What it does on an SN200 is undocumented by anyone.
**SPECULATIVE**, but it is three wires on a bench adapter and it costs an
afternoon. Worth one attempt on a spare before opening a drive.

**SMBus is not a back door to the console**, and this is separately **PROVEN**:
NVMe-MI is handled by `PROC9`, whose admin tunnel forwards only
`{0x02, 0x06, 0x09, 0x0A, 0x10, 0x11}` — six standard opcodes, no vendor message
type, and its writes reach only volatile RAM and the FRU EEPROM
(`sn200-readonly-startup.md` §6.5). It is genuinely *narrower* than the PCIe path.
It is still worth wiring for a different reason: NVMe-MI works **with the PCIe
link down**, so it gives you VPD, health and telemetry from a drive that will not
enumerate. Note a plain U.2→PCIe adapter does **not** route E23/E24 — you need a
backplane wired to a BMC, or a breakout landing those two pins on a header.

**Practical consequence: work on the HHHL card.** More board area, exposed pads,
no backplane in the way — and the PCIe CEM edge connector carries **SMBus on
`B5`/`B6`** (usually actually routed on AIC platforms) and **JTAG on `A5` TCK /
`A6` TDI / `A7` TDO / `A8` TMS / `B9` TRST#** by definition. CEM JTAG is optional,
usually boundary-scan-only rather than a core debug port, often unpopulated, and
never routed to the slot by motherboards (you would need a riser or interposer) —
so treat it as a long shot, not a plan. **SPECULATIVE.** If the data is on a U.2
unit and a spare HHHL exists, rehearse everything on the HHHL first.

---

## 3. Which option is safer: `LOAD_N_GO` or marker 8?

Short answer: **marker 8 is the safer *outcome*; `LOAD_N_GO` is the only
*executable* option.** Do `LOAD_N_GO` and enforce read-only from the host.

| | boot mode 4 `LOAD_N_GO` | marker 8 `READ ONLY` |
|---|---|---|
| How it is set | `MemWrite 0x7ff9ff64 4` at the SBL console | write `0x80000008` to EEPROM SA section 6 word 0, **both copies** |
| Is there a command that does it? | **Yes** — `MemWrite` is in the SBL help text | **No known one.** No `WriteSpi` exists (§4.2) |
| Latch bypassed? | Yes — jumps over both `ball` tests *and* the empty-SA door (`0x7ffaae2d`). **PROVEN** | **No — and this is fatal.** A crash bit forces the marker to 9 at `0x7ffaaf02` before the dispatch, discarding the 8. **PROVEN** |
| Resulting startup mode | **0** (identical to a healthy drive) | **3** `READ ONLY STARTUP` |
| Writes refused by firmware? | **No.** Full read/write | **Yes**, at the admin/IO layer, with L2P restored |
| Does the drive write to itself? | **Yes** — GC/wear-levelling resume, journal updates, and the post-`LOAD_N_GO` thread writes the firmware image into EEPROM slots 1–5. **PROVEN** | Should be minimal. **INFERRED** |
| Survives a power cycle? | **No** — one boot only | **Yes** — it is EEPROM state |
| Blocked by | the latch is re-armed on media every boot, so repeat per boot | no write primitive; SA-journal CRC unreversed |
| Risk if it goes wrong | a bad `MemWrite` hangs the loader → this drive stays dead. Cannot reach user data | an inconsistent/CRC-wrong section 6 could make the drive unbootable, or trip the "Unexpected empty System Area" path |

### 3.1 Does `LOAD_N_GO` risk writes to a drive we want frozen? — Yes.

Say it plainly: **`LOAD_N_GO` un-freezes the drive completely.** Startup mode 0 is
the normal mode. Background GC will relocate blocks; the journal will be written;
the firmware will rewrite EEPROM slots 1–5. None of that targets user LBAs, and
none of it is a wipe — but it is not the "mount read-only" posture the brief
wanted, and if the NAND is genuinely marginal, GC touching it is a real (if small)
risk. **PROVEN for the writes; SPECULATIVE for whether GC can hurt you.**

Mitigations, all host-side, all cheap:

- `blockdev --setro /dev/nvmeXn1` immediately, before anything can mount it.
- Boot a minimal live environment; mask/disable udev auto-mount, LVM, mdadm,
  ZFS import and any filesystem "repair on mount" behaviour.
- Mount only with `-o ro,norecovery` (XFS) if you mount at all — prefer imaging.
- ⚠ The NVMe **Namespace Write Protect** feature (`Set Features` FID `0x84`) is
  **not** on this controller's supported FID list (1–11, 126–131, 240). There is
  no in-band way to make the drive refuse writes. **PROVEN.**

### 3.2 The `LOAD_N_GO` step that is *not* proven

`MemWrite 0x7ff9ff64 4` assumes the SBL console can write that address and that
**the SBL does not overwrite the boot-mode word when it hands off to the
firmware.** It plausibly does — the word's whole purpose is for the loader to tell
the firmware how it was loaded. The shipped initial value in `SBLPATCH.bin` is
`0` (`COLD BOOT, EEPROM`).

**This is the single most likely step to just not work.** **SPECULATIVE.**
If it fails, the symptom is benign and unmistakable: the drive boots and prints
`SYS: Post Crash startup` anyway. Nothing is damaged. See §6 for what to try next.

The clean alternative — trigger a *genuine* `LOAD_N_GO`, i.e. have the SBL load
the (unmodified, correct) `KNGND122` image from the host so the loader itself sets
mode 4 — would be strictly better and needs no memory poking at all. The SBL
brings PCIe up before firmware (`SBL: Initialize PCIe interface`,
`SBL: Waiting for link up ...`, `SBL: Host re-enabled (port %d)`), so a host-side
load channel almost certainly exists. **We have not found how to invoke it.**
**SPECULATIVE.** This is the highest-value remaining desk work (§8).

---

## 4. The procedure

Nothing here has been executed. Rehearse it end-to-end on a **spare latched
drive** before touching the one that holds data. Do not skip that.

### 4.0 Before any hardware

```bash
# Read-only triage. Two 0xC6 size probes and an Identify; emits no 0xFF/0xDD/0xD8/0xD9.
tools/sn200-fw/check-latch-state.sh /dev/nvmeN
```

Record which section is armed. A PFAIL-only latch has a documented safer clear; a
CRASH latch does not, and **`UNEXSTRT` stamps the CRASH section**, so a power-event
latch is a CRASH latch. **PROVEN.**

Also read the drive's *actual* current startup marker, non-destructively:
`0xFF` / `CDW12 = 0x0007` — `OAM READ RAW SA CMD` DMAs the System-Area journal to
the host. It is a **read**, it posts verb 42 (not the marker setter), and it is on
the post-crash allow-list. **PROVEN.** Do this: it is the only way to see the
marker rather than infer it, and the dump is exactly the EEPROM structure any
future marker-8 attempt would have to edit. Save it.

### 4.1 The `DiagMgr>` console

Physical setup: GND + TX only, 115200 8N1, no flow control, adapter VCCIO set from
the measured level (§2.4).

1. **Power on and listen.** Expect the boot loader first —
   `(info): MBL: Starting ...`, `(info): SBL: Starting (cold boot) ...` — then the
   firmware's `SYS: Firmware is starting`, then a `DiagMgr> ` prompt. Seeing the
   MBL/SBL banners alone proves the pinout and the baud. **PROVEN** that these
   strings exist; **INFERRED** that they appear on this UART.
2. **Wire RX. Press Enter.** The console echoes and prints `RV:%d` after each
   command, so it is unmistakable. **PROVEN** (line editor at `0x7ffb1abc` →
   `0x7ffb4b68`, dispatch at `0x7ffb15a4`).
3. **`Help`.** Read-only. Lists the three registered groups (`native`, `SYS`,
   `VHIST`). This alone validates that the console comes up on a latched drive,
   which is worth doing on its own even if you go no further. **PROVEN** the
   console task is enqueued unconditionally, before the startup type is computed,
   and its dispatcher has no gate.
4. **`Mode 0` — set exact name matching.** **PROVEN**, decoded from the matcher's
   entry at `0x7ffb13cc`: the matcher calls a comparator that returns 2 for an
   exact match (accepted always) and 1 for a prefix match (accepted **only when
   `*(0x7ff96f00) != 0`**). `Mode` stores its argument straight into that word
   (`0x7ffb14af`). So **`Mode 0` = exact only; nonzero = abbreviations allowed.**
   The flag lives in the console control block that `console_init` initialises, so
   the default is probably already 0 — set it anyway, it costs one line.
5. Note the matcher searches **all eight group slots**, so a bare command name
   resolves without its group prefix, and an ambiguous abbreviation is reported
   rather than guessed. **PROVEN.**

### ⛔ The two commands to never type

| command | what it does |
|---|---|
| **`I2CErase`** | fills all three I²C EEPROM shadows with `0xFFFFFFFF` and sets the dirty flags that trigger the flush. **Destroys FRU/VPD.** Does not touch user data, but it is unrecoverable and there is no vendor to reprogram it. **PROVEN** (`0x7ffa3b3c`) |
| **`LogicTrap`** | a deliberate `break.n` — crashes the drive on purpose, which is exactly the event that arms the latch you are escaping. **PROVEN** (`0x7ffa3b60`) |

Under flexible matching a bare **`I`** resolves uniquely to `I2CErase` and a bare
**`S`** resolves uniquely to `SBL`. That is why step 4 exists. **PROVEN.**

Also never type, at the SBL console: **`EraseSystem`** — "Erase SPI EEPROM". It
erases the System EEPROM, which is where the System Area, the boot marker, the
board configuration and every firmware slot live. This is the worst command in
this document.

### 4.2 Into the SBL console

```
DiagMgr> SYS SBL
```

`0x7ffa3acc` — the *only* writer of the boot-mode word `0x7ff9ff64` in the whole
`PROC0` image. It logs `SYS: Go into SBL mode`, pokes `*(0x82a60008) = 1`, calls
`SYSservices->fn[0x38]`, writes boot mode `5`, and calls the loader re-entry
callback. **PROVEN.** Whether `fn[0x38]` flushes the System Area to SPI first is
**SPECULATIVE** — if it does, that is a write to the drive, so it is the last
"harmless" thing that is not obviously harmless.

You should land at the loader's prompt (`> `). Its command set, **PROVEN
verbatim** from `SBLPATCH.bin`'s help strings:

```
SBL commands
  MBL              - Go into MBL diagnostic mode
  EraseSystem      - Erase SPI EEPROM              <-- NEVER
  ReadSpi          - Read SPI EEPROM from address
  Reset            - Hardware reset
  SBL              - Return to SBL
  EepRdDdrTrainData / EepWrDdrTrainData
  DdrConfigModify <data-type> <offset> <byte value>
  DdrConfigDisplay <data-type>
  setPll <PLL-type> <Speed>
SHARED
  MemRead  <address> <word-count>
  MemWrite <address> <data-word>
  CARRead  <NodeID> <offset>
  CARWrite <NodeID> <offset> <data-word>
DDR commands
  ddrMemRead / ddrMemWrite / ddrMemDump / ddrMemFill / ddrMemClear / ddrMemWrRdTest
  PrintDimm, PrintPhy, PrintPhyReg, PrintCtl, PrintDcsu, PrintSpdRaw, ...
```

⚠ **There is no SPI write command in that list.** `ReadSpi` reads,
`EraseSystem` erases everything, `EepWrDdrTrainData` writes only the DDR
calibration area. **PROVEN** by exhaustive string search of `SBLPATCH.bin` for
`spi|eep|erase|write`. The command *table* has not been carved yet, so an
undocumented command cannot be ruled out — but nothing in the help text offers
one. See §8.

⚠ This help text is from `KNGND110`'s `SBLPATCH.bin`. `KNGND122` does not ship an
SBL patch, so the SBL actually programmed on your drives may be older or different.
**The command set on your hardware is unverified.** Run the loader's own `Help`
first and compare.

### 4.3 The write

**Verify before you write.** Read the word back first:

```
> MemRead 0x7ff9ff60 4
```

Expect four words, the second of which (`0x7ff9ff64`) is the boot mode: `0` if
this was a cold boot from EEPROM, `5` if it came from `SYS SBL`. The shipped
initial image of that block has `+0x00 = 0x0000000b`, `+0x04 = 0`,
`+0x10 = 0x0000ffff` and function pointers from `+0x20` up into `0x7ffb6xxx` —
if you see something of that shape, the address is right. **INFERRED** (from the
`SBLPATCH.bin` initial image of `0x7ff9ff60`).

Then:

```
> MemWrite 0x7ff9ff64 4
> MemRead  0x7ff9ff64 1        # must read back 0x00000004
```

If the read-back is not 4, **stop**. Do not retry at a different address; you do
not know what you actually wrote. `MemRead` the surrounding block again, confirm
the shape, and re-derive.

Then hand control back to the firmware. `Reset` is a hardware reset and will
re-enter the loader from scratch, discarding your write — so the correct exit is
whatever returns to firmware boot (`SBL` returns to SBL; the loader then boots the
firmware). **This exit step is SPECULATIVE** — the exact command that continues
the boot rather than restarting it has not been determined from the image. Try
`SBL` first; if the banner sequence restarts from `MBL: Starting`, the write is
gone and you have learned the exit is wrong, at no cost.

**Success looks like:** `SYS: Load-n-go boot override of failed shutdown.`
(StrId 3043) or simply `SYS: First time startup` / `SYS: Normal startup`, and
**not** `SYS: Post Crash startup` (StrId 1273). The startup-type name is printed
from StrIds 303–309, so you will see `NORMAL`/`FIRST` rather than `INVALID`.
**PROVEN** that these are the strings; **INFERRED** which one you get.

### 4.4 If you get marker 8 instead (the read-only variant)

Only relevant if §8 turns up a write primitive. Requirements, all **PROVEN**:

- The record is **244 bytes (`0xF4`)**, EEPROM **System-Area section 6**.
- The marker is **word 0**.
- There are **two copies**: index 0 (RAM `0x7ff8c7ec`) and index 1 (RAM
  `0x7ff8c8e0`), loaded by the dispatcher at `0x7ffaaf3e` and `0x7ffaaf95`.
- **You must write both.** The dispatcher heals the primary from the secondary:

  ```asm
  7ffaae1e: l32i   a11,a7,0xf4    ; copy 1's marker
  7ffaae21: s32i.n a11,a7,0x0     ; -> overwrite the primary
  ```

  A half-write is silently undone.
- The bytes are `0x80000008`, little-endian on the wire: `08 00 00 80`.
- **Unresolved:** the SA journal carries a CRC (StrId 1251,
  `"Index=%d. Written Journal Entry %d. Slot %d. CRC=%08X"`). An external SPI
  edit almost certainly has to recompute it. **The polynomial and the covered
  range are not reversed.** Treat any raw-SPI marker edit as blocked on that.
  **PROVEN that a CRC exists; SPECULATIVE what it is.**
- Verify with `0xFF`/`CDW12=0x0007` (`OAM READ RAW SA CMD`) *before* and *after* —
  it is a read, and it is the only in-band way to see section 6.
- ⚠ **And it still would not help a CRASH-latched drive**: whenever either crash
  bit is set, `0x7ffaaf02` forces the marker to **9** before the dispatch re-runs,
  discarding an 8 on the way in. **PROVEN.** Marker 8 only works *in combination
  with* something that skips the crash test — i.e. with `LOAD_N_GO` — which
  defeats the point. **This is a fatal objection to marker-8-alone and it is new
  to this document.**

**So: marker 8 is not merely hard to write. On a crash-latched drive it does
nothing.** The brief's preferred option is, for these drives, dead. `LOAD_N_GO`
is the option.

---

## 5. Data egress — 7.68 TB, one unreliable window

### 5.1 What the host sees

After a successful mode-0 boot the drive should enumerate normally: `state=live`,
`/dev/nvmeXn1` present, full 7.68 TB, GPT and filesystem intact (rows 3 and 5 of
`sn200-field-evidence.md` show exactly this shape after a recovery). **INFERRED**
— the field rows were recovered by other means, but mode 0 is mode 0.

It is a normal NVMe block device. `dd`, `ddrescue`, `nvme`, `blkid`, `mount` all
work. There is no special access path.

### 5.2 The order of operations

The window is unreliable: any reset, any panic, any power blip puts you back at a
latched drive and a fresh UART session. So capture in **decreasing
value-per-second**.

```bash
DEV=/dev/nvme0n1
OUT=/mnt/rescue          # >= 8 TB, and NOT on another SN200

# 0. Freeze it, first thing, before anything can auto-mount.
blockdev --setro $DEV
lsblk -o NAME,RO,SIZE,FSTYPE $DEV     # RO must read 1

# 1. Metadata: seconds, and without it the image is much harder to use.
sgdisk --backup=$OUT/gpt.bin $DEV || true
dd if=$DEV of=$OUT/head-16M.bin bs=1M count=16 iflag=direct status=progress
SZ=$(blockdev --getsize64 $DEV)
dd if=$DEV of=$OUT/tail-16M.bin bs=1M count=16 skip=$(( SZ/1048576 - 16 )) iflag=direct
blkid $DEV; lsblk -f $DEV

# 2. Drive-side evidence while it is up (cheap, and unavailable once it relatches).
nvme id-ctrl  $DEV -H > $OUT/id-ctrl.txt
nvme id-ns    $DEV -H > $OUT/id-ns.txt
nvme smart-log $DEV   > $OUT/smart.txt
nvme error-log $DEV   > $OUT/errlog.txt
nvme fw-log   $DEV    > $OUT/fwlog.txt

# 3. The image. ddrescue, ALWAYS with a mapfile — this is what makes the window
#    resumable across relatches.
ddrescue -f -n -b 4096 -c 64 --max-read-rate=0 \
         $DEV $OUT/sn200.img $OUT/sn200.map
# then, only if pass 1 left bad areas:
ddrescue -f -r3 -b 4096 $DEV $OUT/sn200.img $OUT/sn200.map
```

Points that matter:

- **`ddrescue` with a mapfile, not `dd`.** `dd` has no memory. When the drive
  drops, `ddrescue` resumes at the right offset after you redo the UART procedure;
  `dd` makes you start over on 7.68 TB.
- **`-n` (no scraping) on the first pass.** Get the bulk off fast; retry bad areas
  later, if there are any. There probably are none — the media is not the fault.
- **`-b 4096`** matches the likely LBA size (check `nvme id-ns`; if the namespace
  is formatted 512e, use 512).
- **Destination must not be another SN200.** Obvious, and easy to get wrong when
  five of them are in the same chassis.
- **Sparse destination if the filesystem is mostly empty.** A file on XFS/ext4/ZFS
  will be sparse where `ddrescue` never wrote, but `ddrescue` *does* write zeros it
  actually read. If the source is mostly empty, `zstd`-compressing after the fact,
  or imaging to a ZFS dataset with compression on, saves a lot of space.

### 5.3 If you can only have one thing

If the window looks like minutes rather than hours, **skip the image and copy the
files**:

```bash
mkdir -p /mnt/src
mount -o ro,norecovery $DEV /mnt/src        # XFS; use ro,noload for ext4
rsync -aHAX --info=progress2 --partial /mnt/src/<the-important-thing> $OUT/files/
```

A full image is the better artefact, but files-you-actually-need beats a truncated
image every time. Decide which regime you are in before you start, not halfway.

### 5.4 Do it one drive at a time

Five drives, one UART harness, one rehearsal's worth of muscle memory. Serialise.
And keep the **healthy** drives out of the machine while you work — the trigger for
this whole failure mode is power events and large discards, and a rescue box is
exactly where those happen.

---

## 6. When a step fails — what it looks like, what to do

| step | failure mode | what it means | next move |
|---|---|---|---|
| §2.3 probing | no banner on any pad | wrong pads, wrong baud, or the UART is strapped/fused off in production (OCP SEC-18, and the hddguru datapoint of four dead pads) | sweep the baud list in §2.4; check the PCB edge vias as well as headers; re-photograph near the ASIC; then fall back to the Manufacturing-Mode strap (§2.5) and NVMe-MI over SMBus for at least health/VPD |
| §2.3 | garbage characters | baud or level mismatch | confirm 115200; check whether TX idles at 1.8 V and your adapter is at 3.3 V |
| §4.1 | banners appear but no `DiagMgr>` | firmware did not reach console init, or RX is not wired/inverted | you still have MBL/SBL output — go straight for the loader console instead |
| §4.2 `SYS SBL` | drive resets and cold-boots | `fn[0x24]` did a full reset rather than entering the console | try entering the loader console at power-on instead (an interrupt key during the SBL banner window) — **SPECULATIVE**, unknown key |
| §4.3 `MemWrite` | rejected / read-back wrong | address not writable from the console, or console memory space is not the firmware's | stop. Do not brute-force addresses. This is a re-analysis problem, not a typing problem |
| §4.3 exit | banners restart | your write was discarded by the reset | wrong exit command; try the other one, or accept that the SBL rewrites the mode at handoff (§3.2) |
| after boot | `SYS: Post Crash startup` again | the write did not take, or the SBL overwrites boot mode at handoff | the drive is unharmed. This is the expected failure of the most speculative step |
| after boot | drive hangs, no enumeration | a bad `MemWrite` wedged the loader | power cycle. The loader is in EEPROM and was not modified; a cold boot restores it |

The reassuring shape of this table: **nothing in it loses data.** The failure
modes are "no progress" and "this drive stays dead", both of which are where you
already are.

---

## 7. Honest risk list

Ranked by how likely it is to cost you something.

1. **⚠⚠ Mistyping into `I2CErase` or `EraseSystem`.** These are the only two
   commands in the whole procedure that destroy state, and one of them
   (`EraseSystem`) erases the EEPROM holding the System Area and every firmware
   slot. Mitigation: `Mode 0` (§4.1 step 4), type full names, and paste rather
   than type. **The single highest-consequence risk in this document.**
2. **⚠ Electrical damage from a level mismatch (§2.4).** Unknown logic level,
   3.3 V adapters everywhere, no repair path. Mitigation: listen-only first,
   measure before driving RX, series resistor or level shifter.
3. **⚠ `MemWrite 0x7ff9ff64 4` simply not working (§3.2).** Most likely outcome of
   the whole procedure, and the cheapest to discover. Costs a session, not a drive.
4. **⚠ `SYS SBL`'s `fn[0x38]` flushing the System Area to SPI before reset.**
   SPECULATIVE, unexamined, and it is a write to the drive by a code path we have
   not read. It is also unavoidable if you want the loader console.
5. **⚠ Host software touching the drive the instant it enumerates.** A distro that
   auto-mounts, an XFS log replay, an LVM/ZFS auto-import, a `fsck`. Mitigation:
   `blockdev --setro` in a udev rule *before* the drive appears, minimal live
   environment, no desktop.
6. **⚠ `LOAD_N_GO` un-freezing the drive (§3.1).** GC and journal writes resume,
   and the firmware rewrites EEPROM slots 1–5. Not a data-loss path in itself, but
   it is not the frozen posture the brief asked for.
7. **Any raw SPI edit for marker 8.** Unreversed CRC, two copies that must agree,
   and §4.4's fatal objection (a crash latch forces marker 9 anyway). Do not
   attempt.
8. **Not a risk, but say it anyway:** the whole route assumes the console is not
   password-gated or disabled in production firmware. `PROC0`'s dispatcher has no
   gate, so the *firmware* console is open — **PROVEN**. The *loader* console's
   entry conditions are unread. **SPECULATIVE.**

### What is inference versus fact, in one list

**PROVEN** (decoded from the image, from a function entry):
the latch predicate and the `LOAD_N_GO` bypass; startup-mode/marker enums;
`SYS SBL` is the sole writer of `0x7ff9ff64`; the `DiagMgr>` console comes up
before the startup type is computed; the eight console commands and what
`I2CErase`/`LogicTrap` do; `Mode 0` = exact matching and the matcher's global
group search; 115200 as a compiled constant; the SBL/MBL help text and the absence
of an SPI write command; marker 8's semantics and the heal-from-copy-1 behaviour;
crash bits forcing marker 9; the section 6 / 244-byte / two-copy layout; NVMe-MI
being a dead end; `0xFF/0x0503` and `0xFF/0x0403` being destructive.

**INFERRED**: the UART register block at `0x81860000`; the UART driver living in
the SBL and therefore both consoles sharing one line; the `SYSservices` block
contents; U.2 having no standard serial pins; the drive enumerating normally after
a mode-0 boot.

**SPECULATIVE**: the logic level (1.8 V vs 3.3 V); the physical pad location; the
loader console's entry and exit commands; whether the SBL overwrites the boot mode
at handoff; whether `fn[0x38]` writes to SPI; the SA-journal CRC; whether a
host-driven genuine `LOAD_N_GO` is invocable.

---

## 8. Desk work that could make the hardware unnecessary

In value order. All of it is free and touches no drive.

1. **Finish carving `SBLPATCH.bin` and disassemble the SBL.** This answers, in one
   go: whether an undocumented SPI-write command exists, what the loader console's
   entry/exit commands are, whether `MemWrite` reaches `0x7ff9ff64`, whether the
   SBL rewrites the boot mode at handoff, and how a genuine host-side `LOAD_N_GO`
   is triggered. That last one would remove the UART from the plan entirely.

   Progress made here, to save the next person the hour:

   - `SBLPATCH.bin` is a `PMCSEEPM001` EEPROM-programming image: a 0x30-byte
     header, then 16-byte records `{type, address, mask, value}` (first record
     writes `0x0203c041` to `0x82a61000`; record type `0x1004` = write,
     `0x1003` = read-modify-write).
   - Embedded in it are `.BIN`/`.SEG` containers. `segparse.py` finds them if you
     slice from the `.BIN` marker. **24 `.SEG` headers**, load map:

     | load range | size | what |
     |---|---|---|
     | `0x7ff80000`–`0x7ff88540` | 0x8540 | MBL |
     | `0x7ff98000`–`0x7ff9e064` | 0x6064 | SBL text/strings (console help lives here) |
     | `0x7ff9ff60`–`0x7ff9fffc` | 0x9c | **the `SYSservices` / boot-handoff block** |
     | `0x7ffa0000`–`0x7ffa04d0`, `0x7ffa0710` | small | SBL/MBL variables |
     | `0x7ffb6000`–`0x7ffbfab0` | 0x9ab0 | **SBL code — the console and UART driver** |
     | `0x7ffa0710`–`0x7ffad5e0` | 0xced0 | MBL code |

   - **Unsolved:** the mapping from `.SEG` `data_offset` to a file offset. Small
     segments sit at `header+0x10` (the `0x7ff9ff60` block decodes perfectly that
     way — 22 plausible code pointers into the declared SBL range). The two large
     code segments do **not**: no `entry` prologue appears at the pointer targets
     under that mapping, and no pointer to a known string is found under any base
     tried. Solve this and the SBL falls open.

2. ~~**Map `Admin_VucFlashLogicalToPhysical` and `Admin_VucFlashRead` to their
   opcode/sub-code.**~~ **CLOSED — negative.** Both sit on `0xCA`:
   `Admin_VucFlashLogicalToPhysical` is `CDW12 = 0x0000` (handler `0x30035680`),
   `Admin_VucFlashRead` is `CDW12 = 0x0001` (handler `0x30036494`, reads one LBA
   of user data through the live L2P). **Neither `0x00` nor `0x01` is in the
   twelve-entry sub-list at `0x7ffa6d76`**, so a latched drive rejects both
   `0x7C5` before the handler runs. This was not an accident of the allow-list:
   every `0xCA` sub-value it *does* admit works in physical addresses, and these
   two are the only ones that understand LBAs. Full evidence:
   `sn200-vuc-flash-read.md`. This document is still needed.

3. **Map the unmapped `0x7ffbc1xx`–`0x7ffbe6xx` region** holding the `0xFF`
   handler. `0xFF` passes the post-crash gate; its exact semantics are the
   difference between an exit and a wipe.

4. **Reverse the SA-journal CRC** (StrId 1251) — the precondition for any external
   marker edit.

---

## 9. One-paragraph summary for the owner

The data is fine; only the boot path refuses. The only escape with every firmware
link proven runs through a serial console on the board, and the pins are the one
thing the firmware cannot tell us — that is a probe-and-measure job, listen-only
first, and **measure the logic level before you drive anything**. Once in, the
realistic move is `SYS SBL` → `MemWrite 0x7ff9ff64 4` → boot, which skips the
latch and brings the drive up completely normal; the read-only variant the brief
preferred turns out to be both unwritable with any known command **and**
ineffective on a crash-latched drive, because a crash latch overwrites the marker
on the way in. Because the trap re-arms from media on every power cycle, treat the
result as a single copy window: freeze the device from the host, capture the
metadata in the first seconds, and image with `ddrescue` and a mapfile so an
interruption costs minutes and not 7.68 TB. Rehearse the whole thing on a spare
latched drive first. The failure modes are all "no progress", not "data gone" —
except the two commands in §4.1 that must never be typed.
