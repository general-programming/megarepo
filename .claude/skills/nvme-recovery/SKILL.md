---
name: nvme-recovery
description: Diagnose and recover a failing NVMe SSD without destroying data — read-only triage, the reset escalation ladder, and the HGST/WDC Ultrastar SN200 "persistent internal error / diagnostic mode" trap where the namespace vanishes but the data is intact. Use when a drive reset-loops, shows no /dev/nvmeXn1, or disappears from the OS.
---

# NVMe recovery: non-destructive first

**Never** run `nvme format`, `nvme sanitize`, `nvme wdc purge`, or
`delete-ns`/`create-ns` while diagnosing. A missing namespace is almost never
missing NAND. Get approval before any of them.

## Identify the device by PCI address and model, never by number

`nvmeN` enumeration is not stable across boots or resets. Resolve it every time:

```sh
lspci -nn | grep -i nvme                       # find the PCI BDF
for n in /sys/class/nvme/nvme*; do
  echo "$(basename $n) $(cat $n/model) $(cat $n/state)"
done
readlink -f /sys/class/nvme/nvme7/device       # -> /sys/.../0000:b2:00.0
```

On a host that also runs ceph OSDs, confirm which disks are OSDs before
touching anything: `lsblk -o NAME,MODEL,FSTYPE` — OSDs show
`LVM2_member -> crypto_LUKS -> ceph_bluestore`.

## Read-only triage

```sh
nvme list                                      # missing = no namespace
nvme show-regs -H /dev/nvme7                   # CSTS: RDY / CFS, CAP: NSSRS
nvme id-ctrl /dev/nvme7 | grep -E 'tnvmcap|unvmcap|nn |oacs|frmw|sanicap'
nvme smart-log /dev/nvme7
nvme error-log /dev/nvme7 -e 8
nvme list-ns -a /dev/nvme7                     # -a includes inactive NSes
nvme fw-log /dev/nvme7
lspci -vvv -s b2:00.0                          # LnkSta, DevSta, AER, LaneErrStat
cat /sys/bus/pci/devices/0000:b2:00.0/aer_dev_{correctable,fatal,nonfatal}
```

Read these two together — they decide everything:

- **`tnvmcap` > 0 and `unvmcap` == 0** → the full capacity is *still allocated
  to a namespace*. The namespace exists; the controller just won't report it.
  **Data is intact.** Do not format.
- `unvmcap` == `tnvmcap` → capacity really is unallocated (namespace deleted).

### Drive absent from the bus entirely = cabling, not firmware

`lspci` not listing the device at all is a **different fault** from the
diagnostic-mode lockup, and no amount of VUC work will fix it. Check the
host console / iDRAC before assuming firmware:

```
UEFI0067: A PCIe link training failure is observed in Bus:174 Dev:3 F:0
          and the link is disabled.
```

Bus is **decimal** — 174 = `0xAE` = the `ae:03.0` bridge. BIOS disables the port,
so the drive cannot appear no matter what the OS does. Corresponding SEL entry:
"A fatal error was detected on a component at bus 174 device 3 function 0".
Intermittent across boots ⇒ suspect the NVMe cable/riser, and note the drive may
come back on the very next power cycle, which makes it look like a firmware win.

Two consequences on a headless box:

- **Set BIOS `ErrPrompt` to Disabled** (`/redfish/v1/Systems/System.Embedded.1/Bios/Settings`,
  `{"Attributes":{"ErrPrompt":"Disabled"},"@Redfish.SettingsApplyTime":{"ApplyTime":"OnReset"}}`).
  Otherwise POST halts forever at "F1 to Continue" with no console attached.
- **F1 continues with the link still disabled** — the drive stays absent. Only a
  power cycle retries link training.

### Split the fault: link vs. controller

Clean `LnkSta: Speed 8GT/s, Width x4`, `DevSta: CorrErr- FatalErr-`, all
`aer_dev_*` counters zero, `LaneErrStat: 0` → **the PCIe layer is fine and the
fault is inside the drive.** Cabling, riser, and backplane reseating are a
waste of time in that case. Only chase the link when the width/speed is
degraded or AER counters are climbing.

Note `_OSC: platform does not support [... AER ...]` on Dell PowerEdge is
normal — firmware-first error handling, not a missing capability.

### ioctls return "Resource temporarily unavailable"

That is `EAGAIN` because `/sys/class/nvme/nvmeN/state` is `resetting`, not a
drive failure. A reset-looping controller has a live window of a few seconds
per cycle — poll for it and fire immediately:

```sh
for i in $(seq 1 400); do
  [ "$(cat /sys/class/nvme/nvme7/state)" = live ] && { nvme id-ctrl /dev/nvme7; break; }
  sleep 0.05
done
```

Wrap it in a retry loop: nvme-cli prints its **usage text** on failure instead
of a clean error, so detect failure with `grep -q Usage:`, not the exit code.

## `resetting controller due to persistent internal error`

This message is *not* the kernel giving up on a timeout. The drive itself
raises an Asynchronous Event (Error, info `03h` = Persistent Internal Error)
and the kernel reacts by resetting the controller. The drive is asserting
"my internals are broken." Loop period is ~5s.

The corresponding error-log entry has `sqid: 65535 cmdid: 0xffff` (no command
— it's the async event) and often a vendor-specific status
(`status_field: 0x7d3`, SCT=7 vendor specific, SC=0xd3).

Zeroed SMART counters (`power_cycles: 0`, `Data Units Read/Written: 0`)
alongside a plausible `power_on_hours` mean the controller cannot read its own
persistent metadata region — the same region that holds the namespace table.

## Reset escalation ladder

Do these in order. Each is non-destructive to user data.

```sh
nvme ns-rescan /dev/nvme7
nvme reset /dev/nvme7                                   # controller reset
nvme subsystem-reset /dev/nvme7                         # needs CAP.NSSRS=Yes
echo 1 > /sys/bus/pci/devices/0000:b2:00.0/reset        # FLR
echo 1 > /sys/bus/pci/devices/0000:b2:00.0/remove ; sleep 5
echo 1 > /sys/bus/pci/rescan
```

`nvme subsystem-reset` is genuinely stronger than a controller reset — you can
confirm it worked because dmesg shows the BARs being *reassigned*, i.e. the
device left and rejoined the bus. If it still fails identically afterwards, no
amount of further host-side resetting will help.

**Stop the reset thrash** to work quietly, or to let the drive run an internal
recovery undisturbed:

```sh
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/unbind
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/bind
```

**`unbind` is the only one of these that even attempts a clean stop** — it
issues `CC.SHN` and waits for `CSTS.SHST`; every other entry drops `CC.EN` or
the link without it. On firmware that latches on an unfinished shutdown (see the
SN200 section) those resets are themselves the fault condition, so escalating
the ladder can make things worse. Prefer `unbind`/`bind`.

**But `CC.SHN` is NOT sufficient, and do not rely on it as an escape.** On the
SN200 the NVMe shutdown path writes only marker 5 `Normal Shutdown STARTED`;
the CLEAN marker is written by a *different* routine once the System Area /
L2P flush actually completes. A shutdown that is acknowledged but whose flush
does not finish still lands in the "never finished" handler.

⚠ **Worse than that, and this undercuts the usual advice:** the marker-5 submit
at PROC0 `0x7ffa8dca` jumps straight into `SYS: Returning shutdown completion`,
and `ShutdownReq --> SAM` is only reached **afterwards**. If that string is the
host-visible `CC.SHN` completion — INFERRED, not yet proven — then **waiting for
`CSTS.SHST=10b` does not wait for the System Area save at all.** The standard
"issue `CC.SHN`, wait for `SHST`, then cut power" ritual would therefore *not*
guarantee the save finished. Allow real time after `unbind`, and treat a
completed shutdown handshake as necessary rather than sufficient.

⚠⚠ **And an orderly shutdown can hang outright — PROVEN.** A normal `CC.SHN`
sets internal mode **4**, but PROC11's garbage collector waits on three counters
whose *only* release is mode **5**, with no timeout and no bail-out — while its
own producers, which gate on mode 5 too, keep incrementing them. Mode 5 is
written from outside GC only. **A normal shutdown with no PFail has no escape
from that wait**, so "shut down cleanly" is not a guaranteed mitigation; it is
merely a better bet than cutting power. Full trace: `docs/sn200-shutdown-path.md`
§6a.

**A warm reboot is not a power cycle** — it does not drop rail power. But do
not reach for `ForceOff` reflexively either: an abrupt power cut is an unclean
stop and can *cause* the latched state on drives that record shutdown
completion. On a ceph host it also takes the OSDs down — confirm the cluster
tolerates it first, `noout` alone is not sufficient.

## HGST / WDC Ultrastar SN200: "Post Crash Startup" lockup

Model `HUSMR7676BDP3Y1`, firmware `KNGND1xx`. Procedure below is what actually
worked, not what the forum posts say. Full firmware teardown:
`docs/sn200-firmware-re.md`.

> 🔴 **The one fault record we have recovered says the latch was armed by a
> TRAP, not by a shutdown.** Decoded from real hardware: a fatal synchronous
> exception on **PROC9, the NVMe-MI / SMBus out-of-band management processor**,
> in an ordinary scheduler task, on a running drive, *after* a power-fail
> recovery that completed. No shutdown machinery runs on PROC9. So at least one
> arming route is `real trap → full dump → CLOG bit 0 → marker 9 → mode 6`,
> needing **no shutdown at all**. Read `docs/sn200-fault-record.md` before
> assuming the unfinished-shutdown story applies to a given drive — check the
> crash section header first: `0x00020100` + `"UNEXSTRT"` at `+0x40` is a
> shutdown stub, `0x00020200` with a zero tag is a genuine fault.
>
> Practical consequence worth testing: PROC9 is driven by the **chassis BMC**
> over SMBus/MCTP. BMC/iDRAC NVMe-MI polling is therefore a live suspect for
> provoking these traps, and is not something the host OS controls.

**One predicate, several arming routes.** Two independent teardowns agree
(`docs/sn200-firmware-re.md`, `docs/sn200-independent-re.md`). At boot, PROC0
runs two separate tests — one per section — both branching to
`SYS: Detected a CRASH or PFCRASH section.` and both forcing marker
`0x80000009` (Post Crash) at `0x7ffaaf08`. **Either section latches, and the
forced marker overrides whatever the shutdown actually recorded.**

A third route needs no crash section at all: `SYS: Unexpected empty System
Area.` jumps straight to the same forced marker. Sibling blocks force marker 3
(REINIT) for `Found an incompatible SA`, `Detected an erased SysArea` and a
CellCare mismatch.

Feeding those sections: shutdown markers **5/6/7** — `Normal Shutdown STARTED`,
`PFAIL Shutdown STARTED`, `PFAIL Shutdown TIMEOUT` — all mean *began and never
finished*, and converge on one handler at `0x7ffaaf6b`. A power loss whose
hold-up sequence **completes** writes marker 2 and boots normally, which is why
these drives do not all brick on every power event.

The state probe's byte[1] == **6** is a *different* enum — it really does mean
diagnostic mode. `7ffaac95: extui a10,a5,0,3` masks the startup type to 3 bits
(7 reachable values); the marker enum has 11. WD's own
`gf_is_diagnostic_mode` tests `== 6` → `HDMS_DEV_DIAGNOSTIC_MODE`. Do not
conflate the two enums — the marker table is indexed separately at PROC0
`0x7ff81180`.

(The `== 7` variant in `libdmi_core.so` is gated on **Firmware Revision**, not
model: `FR[0]=='H' && FR[3]>'E'`. `KNGND122` starts with 'K', so the `== 6`
path applies here.)

Two consequences that overturn the obvious readings:

- **A completed power loss is harmless.** If the hold-up sequence finishes it
  writes marker 2 and the drive boots normally. This is why SN200s do not all
  brick on power loss — only *interrupted* saves latch.
- **TRIM is a provocation, not the mechanism.** No deallocate/L2P/journal state
  is consulted in the decision anywhere. A large deallocate can *cause* a
  shutdown to not finish, which is why WD's errata (OM-6588 "…after large
  deallocate and a pfail", OM-6836/6850/7044) are real — but suppressing
  discard does not make the drive safe, it only removes one provocation.

Still worth suppressing discard on a suspect drive — `mkfs.xfs -K`,
`mkfs.ext4 -E nodiscard`, no `discard` mount option, LVM `issue_discards = 0`,
no whole-device `fstrim` — just do not mistake it for a fix.

**Root cause: a WD-acknowledged firmware defect family. Read the release notes
first — they are in a `docs/` folder inside the firmware zip.** That folder was
the single highest-value artefact in the whole teardown and is easy to miss.

WD documents this symptom by name: **"Namespace Disappears During AC Power Cycle
Testing"** — *"Power Cycling + Random Read/Write/Deallocate IO Profile Testing
results in **incomplete shutdown** … when both a link down and a Pfail interrupt
occur at exactly the same time … the Pfail interrupt may get lost."* That is the
marker-5/6/7 → UNEXSTRT → Post Crash chain, in the vendor's own words. Related
entries cover the large-deallocate/L2P race, GC deadlock during shutdown, the
System Manager never sending the shutdown message, and link-down-during-
queue-enable hangs.

**It is not the capacitors.** `capacitor`, `VCAP`, `hold-up` and `power backup`
appear **zero** times across all WD documentation for this family. A genuinely
failed hold-up subsystem produces a *different, clearly-labelled* posture —
`VCAP has failed, drive is in write protect mode` — not Post Crash. The drive
also never measures its own capacitance in the field: the PowerUp/Short/Open
tests early-return outside BIST (`VCAP: Not in BIST mode, message ignored`).

**It is not the cable either**, though a marginal one aggravates it: WD
separately documents *"PCIe uncorrectable error with a host link down → drive
hang"* with **"Drive Recovery: Unable to recover."**

### Firmware activation clears the latch — no vendor commands needed

**Proven on sea1-hv-2, 2026-08-04.** A latched drive was recovered with only
standard NVMe commands:

```sh
nvme fw-log /dev/nvme7                          # find a slot holding a good image
nvme fw-commit /dev/nvme7 --slot=5 --action=2   # CA=2 = activate EXISTING image
# then a cold power cycle -- frmw bit4=0 means no activate-without-reset
```

The controlled comparison matters: after the same latch, a **bare cold power
cycle left it still latched**. Adding the `CA=2` activation cleared it. So the
activation is doing the work, not the power cycle.

Expect this sequence during the activation:
```
controller capabilities changed, reset may be required to take effect.
Device not ready; aborting initialisation, CSTS=0x0
Disabling device after reset failure: -19
```
`state=dead` at that point is expected, not a brick — the pending activation
completes on the next power cycle.

**It is destructive, and NOT a safe pointer flip.** The media came back fully
zeroed (every offset sampled 1 MiB → 1 TiB). Why, proven in code: PROC0
`0x7ffabbf0` writes marker 3, gated at `0x7ffabcc6` on **bit 0 of the target
image's own flags word** — so whether an activation wipes is a property of the
**image sitting in that slot**, not of the commit action you chose. `--action=2`
is not "just repoint the boot slot".

It has one real advantage over `0x0503` — no vendor opcodes, so no chance of a
typo landing on `Erase to SBL EEPROM` (permanent brick) or `Drive Uninit`, and
no dependence on the VUC gate. It has **no advantage in data cost**. Pull the
crash dump before either.

Slot 1 is read-only (`frmw` bit 0), so a factory image always survives and this
cannot leave the drive without a bootable slot.

### The re-init really does destroy data — PROVEN

Markers 3 **and** 4 converge on `0x7ffaaf7d` → startup type **0 = FIRST** →
`SYS: Executing First time startup` → `0x7ffaabd8` blanks the SA directory →
PROC8 `0x7ffac7de` runs `Admin_NamespaceStartup` (type 0 only) → `memset` of
**both LBN translation tables** (`0x7ffad556`, `0x7ffad2f6/0x7ffad304`), region
map set to `0xffff`, namespaces **created fresh** (`0x7ffadac6`). V2P restore is
skipped (`0x7ffa6418`) and no from-scratch reconstruction path exists.

So this is not "namespace hidden" and not "L2P rebuilt from a journal" — the
mapping metadata is destroyed. No NAND erase loop was found, so pages are
presumably reclaimed lazily, but nothing host-visible gets them back.

**Startup type 6 is `INVALID` (the latched state); `NORMAL` is 1.** That settles
the `bnei a14,6` gate: `0x0503` schedules the wipe **only when the drive is
already latched**. Fired from a normally-booted drive it erases the crash
section without scheduling a re-init.

### Marker 8 `READ ONLY` — real, non-destructive, and **unreachable**. Closed.

Read-only startup is **not** a degraded mode. SAM `0x7ffba9dc` sets a single
flag bit (`0x80`) and falls straight into the **normal** boot path: L2P
restored, namespace present, writes refused at the admin/IO layer. That is
exactly what would be needed to get data off a latched drive.

**No host command can set it — in band or over SMBus.** The mechanism exists
(PROC0 task `0x7ffa3e48` request code 6 hands `[ctx+0x50]` verbatim to the marker
setter `0x7ffa84c8`, stored unchecked at `0x7ffa853b`), but **nothing constructs
the value `0x80000008`**: it occurs twice in 18 images, once as a comparison in
the boot dispatch and once as a NAND Event-Log tag in PROC12 — a different enum
sharing the `0x80000000|N` form. That PROC12 site is *not* a marker writer;
`sn200-firmware-re.md` §13.6 is retracted.

Refuted individually: every allow-listed opcode, `0xFF`/`0xCA`, Set/Get Features,
the firmware-download flags word (bit 0 is a yes/no gate on a hardcoded
`0x80000003`), Commit action `011b`, and the NVMe-MI tunnel (six standard opcodes,
no vendor channel). Startup type 3 and boot mode 4 (`LOAD_N_GO`) both live in
PROC0-only words that PROC8's command handlers cannot name.

So this is a **code-execution problem, not a command-encoding one** — the
remaining candidate is the `DiagMgr>` UART / SBL console, i.e. physical access.
Full analysis and the exhaustive sweeps: `docs/sn200-readonly-startup.md`.

### First thing to check on any SN200: the firmware revision

```sh
cat /sys/class/nvme/nvmeN/firmware_rev     # or: nvme id-ctrl /dev/nvmeN | grep ^fr
```

Drives of this vintage commonly still ship `KNGND100` (2017), which has **every**
defect above open. `KNGND110` and `KNGND122` are in the firmware zip already.
`KNGND122` (Feb 2021) is the last firmware ever released — and it was *still*
fixing this class (*"the PFAIL monitor thread is added again … a hang occurs
during the shutdown process"*, recovery: unable to recover). A drive already on
`KNGND122` that still latches has no fix available. Externally verified — see
`docs/sn200-firmware-availability.md` for the full search and every revision
string.

**The `FR` string encodes the OEM branch**, `K<asic><oem><branch><level>`:
`KNGN` generic WD, `KNCC` Cisco, `KNEC`/`KNEG` Dell EMC arrays, `KNGW`
unidentified, `KTGN` WD IntelliFlash. A drive on a foreign branch **rejects**
`KNGND*.bin` with `Device firmware version is not compatible with this
operation` — a clean refusal, but do not force it. Cisco's ceiling is
`KNCCD122`: same level, no extra fix, so extracting it from a HUU ISO buys
nothing. Dell EMC's ceiling is `KNECD116` — *below* the fix level, with no
`122`-level `KNEC` image ever published.

`KNGND110.bin` and `KNGND110+sblpatch+k.bin` in the zip are **byte-identical**
(`7210283c…ccff2`, 2 009 856 B). There is no plain `KNGND110`; the innocuous
name in `firmwares/` is the SBL-patching image, which writes every slot and
destroys the fallback. Do not flash it.

Also read `nvme fw-log` before condemning a drive — slots often hold different
images, and activating a good slot has recovered drives that looked dead.

### Flashing: 5 slots, slot 1 read-only, send the bundle raw

Full procedure and provenance: `docs/sn200-firmware-flashing.md`. Tool:
`tools/sn200-fw/fill-fw-slots.sh`.

- `frmw = 0x0b` → **5 slots, slot 1 read-only, no activation without reset**.
  Writable slots are **2–5**. Confirmed three ways: the spec decode, WD's
  `nvmec_get_fw_num_slots` (`sar 1; and 7` / `and 1`), and the firmware's own
  commit handler, which rejects `FS = 1` on the image-replacing path with
  `Firmware Activate Invalid Slot`.
- **`FS = 0` is accepted** — the range check is `FS <= slot_count`, and per spec
  0 means "the controller chooses". `nvme fw-commit` defaults `--slot` to 0.
  Always pass an explicit slot.
- **`KNGND122.bin` goes on the wire verbatim, whole file, unpadded.** WD's own
  `nvmec_fw_img_dl` does `hdm_load_file` → round size up to a *dword* → one
  `fw-download` at offset 0, falling back to 4096-byte pages with a short final
  transfer (`1762048 % 4096 == 768`). It never parses the bundle. Do not extract
  it; do not pad it (`*_padded.bin` is a Windows storport artefact — padding
  moves the 256-byte trailer off EOF). Any `hdm --load` rejection is host-side;
  `nvme fw-download` bypasses it.
- **Commit actions: 0, 1, 2 implemented; 3 is not.** The handler reads only two
  bits (`extui a8,a10,3,2; blti a8,3`), so CA=3 → `0xC0040000` (Generic, Invalid
  Field) and **CA 4/5/6 silently alias onto 0/1/2** — never pass `--bpid`.
  **CA=0 ("replace slot, do not activate") is the safe one**: no activation, no
  reset, active slot untouched.
- **Activation on a dual-port drive needs an NVM subsystem reset**, not a
  controller reset: the handler branches on port count and returns SC `0x10`
  (`Dual Port: Subsystem reset required`) vs SC `0x0B` (`Conventional reset
  required`). Since every in-band reset here is an unclean stop, activate by
  clean OS shutdown + cold power cycle, never `nvme reset`/`subsystem-reset`.
- **Download/commit is locked to one PCIe port** — StrId 2970 `Firmware Commit
  called from wrong port`, SC `0x13` Firmware Activation Prohibited. On a
  dual-pathed drive use the same `/dev/nvmeN` node throughout.
- Re-download before every commit; nothing guarantees the buffer survives one.

### The measurement that would settle a capacitor concern

WD's library exposes SMART attributes **`Power Backup Faults`** and **`Lifetime
Number of Power Backup Faults`**, plus `Unexpected Power Loss Count` and
`Exception and Assert Count`. Read them read-only via `dm-cli get-smart`, or
vendor log pages `0xC1/0xC2/0xC3/0xCA/0xDE` with `nvme get-log`. Compare against
healthy drives — the fleet comparison is the point. Non-zero power-backup faults
would revive the capacitor theory.

**This is why a whole-device TRIM latches it: current, not semantics.** A
whole-device deallocate is the drive's peak-current workload (map invalidate →
GC → NAND erase) — the one most likely to sag a bad connector. Suppressing
discard removes the *load*, which is why it appeared to work; it does not
exonerate discard semantics and it does not make a marginal cable safe.

**Before binning such a drive**, move it to a different bay/cable and retest
under peak load. Two measurements decide it:
- **VCAP health** — `VCAP has failed, drive is in write protect mode` is a
  distinct posture. Degraded hold-up capacitors mean it latches in *any* bay,
  which does justify binning.
- **Which section is armed** — `0xC6 cdw12=0x0320` (CLOG) vs `0x0520` (PFCL).
  PFCL-only confirms power-fail origin and may allow the harmless `0x0603`.

Symptoms, all together:

- `resetting controller due to persistent internal error` every ~5s
- `/dev/nvmeN` present, **no** `/dev/nvmeNn1`
- `nvme list-ns -a` empty, `id-ns` all zeros, but `tnvmcap` full and
  `unvmcap: 0` — capacity still allocated
- SMART partly impossible: `power_cycles: 0`, `data_units_read: 0`, while
  `power_on_hours` is plausible
- PCIe link and AER completely clean
- iDRAC drops the drive from `Storage/CPU.1` and logs a high-severity SSD fault

### What is actually happening

Not a hardware fault. The firmware boots into a restricted **Post Crash
Startup** mode and *deliberately* raises the AEN. From the firmware's own
string table (every `*.bin` is an uncompressed tar containing
`StringTable.csv.gz`):

```
Admin_NotifyHandler: Sending Persistent Internal Error async event on Post Crash Startup.
Admin cmd rejected due to Post Crash startup mode: 0x%x
SYS: Detected a CRASH or PFCRASH section.
OAM ERASE CMD: Schedule reinit after crash dump erase failed.
```

Two different vendor statuses, don't conflate them: admin commands rejected by the
Post Crash gate return **`0x7c5`** (SCT=7 SC=0xC5, `HDMS_DEV_DIAGNOSTIC_MODE`; the
firmware returns the raw constant `0x8F8A0000`). The **`0x7d3`** you see is on the
*error-log entry for the async event* (`sqid: 65535 cmdid: 0xffff`).

The drive is refusing admin commands to protect a crash dump, and **erasing
that dump is what schedules the reinit out of the mode**. `CSTS=0x1` (RDY set,
CFS clear) is correct — the controller really is "up".

**Why it is self-sustaining:** `SYS: UNEXSTRT detected, writing UNEXSTRT stub
header to crash area`. Any start not preceded by a recorded clean shutdown
**re-arms the crash section** — including every ~5s reset the kernel performs.
That is why no in-band reset works: `nvme reset`, NSSR, FLR, SBR and link-disable
all drop `CC.EN` or the link without first issuing `CC.SHN`, so each one is
another "unexpected start".

### There is NO non-destructive recovery from a power-event latch

**PROVEN.** The boot latch fires on **either** the CRASH or the PFAIL section
(bit 0 / bit 2 of a state byte). `UNEXSTRT` — any start not preceded by a
recorded clean shutdown, which includes every power event and every reset in
the ~5 s loop — stamps its stub into the **CRASH** section. Only
`0xFF`/`0x0503` clears CRASH, and on a latched drive that always schedules the
Drive REINIT that zeroes the namespace. The bits are sticky; no clean boot
releases them.

So: **the data is intact, the media is intact, and the only known release
destroys it.** A latched drive left powered down keeps every future option
open; `0x0503` closes them all. Decide deliberately, not reflexively.

Precise bit mapping, PROVEN three ways (TOC at `0x7ff84a70`, producer
`0x7ffb461c`, consumer `0x7ffab010`): **bit 0 ⇒ section `0x0b` CLOG**,
**bit 2 ⇒ section `0x0a` PFCL**. `UNEXSTRT` stamps CLOG (`0x7ffaaf2b`
`movi a12,11`), so PFCL plays no part in sustaining a power-event latch and
`0x0603` cannot release one. The flags byte lives at **`0x7ff8d200`**.

**There is no non-destructive recovery IN BAND.** The one candidate — marker 8
`READ ONLY` startup — is genuinely non-destructive (L2P restored, namespace
present, writes refused at the admin/IO layer) but **nothing ever requests it**:
the constant `0x80000008` is constructed nowhere in the firmware (two occurrences
in 18 images — a comparison in the boot dispatch, and a NAND Event-Log tag in
PROC12 that is a *different enum*, not a marker). Exhaustive disproof in
`docs/sn200-readonly-startup.md`.

Precisely: the setter PROC0 `0x7ffa84c8` is **not** value-restricted — it stores
`[req+0x18]` verbatim at `0x7ffa853b`, and its third caller (`0x7ffa4709`,
request code `[ctx+0x48] == 6`) passes an **arbitrary** word from `[ctx+0x50]`.
The two other callers hardcode markers 3/4. So marker 8 is *unwired*, not
impossible — which is exactly why code execution on PROC0 is enough to set it.

**And "no firmware writer" is not "cannot be set".** The marker is *persistent
state* — word 0 of a 244-byte record in EEPROM System-Area section 6, held in
two redundant copies (copy 1 at `+0xF4`; the dispatcher heals the primary from
the secondary, so a half-write is undone). Writing it **out of band** is a live
route, and the gate is a bare `bnei a8,6` while mode 6 is literally named
`INVALID` — so startup type **3 = `READ ONLY` sails straight through it**.

Out-of-band escape (`docs/sn200-logic-escapes.md`, every firmware link proven;
only the UART pinout is unknown): `DiagMgr>` UART at 115200 8N1 → `SYS SBL` →
SBL console → either boot mode 4 `LOAD_N_GO` (`beqi a12,4` at `0x7ffaae2d`
jumps over **both** `ball` crash tests *and* the empty-System-Area door) or
write `0x80000008` into EEPROM SA section 6, copy 0, word 0.

So: **none known in band; one plausible route with physical access.** Do not
run a destructive recovery on a drive whose contents matter without weighing
that first. In the SBL console never type `I2CErase` (destroys FRU/VPD) or
`LogicTrap` (deliberate crash), and set exact name matching — under flexible
matching a bare `S` resolves to `SBL`.

⚠ **`0xFF` CDW12 `0x0403` is a new landmine, one nibble from `0x0503`.** OAM
ERASE sub 4 ("Drive Uninit") posts the re-init verb `0x25` with parameter 1 and
— unlike sub 5, which is gated by `bnei a14,6` at `0x30033709` — has **no
startup-type gate at all** (`0x300337e3`). It is allow-listed unconditionally
while latched and sets the **FACTORY** re-init marker.

✅ **`0xFF` CDW12 `0x0007` is safe and useful:** `OAM READ RAW SA CMD` (handler
`0x30033824`) DMAs the System Area journal from EEPROM to the host. It is a pure
read, is in the Post-Crash allow-list, and is not in `libdmi_core`. Use it to
read the drive's actual startup marker instead of inferring it.

Triage which section is armed (read-only, safe):

```sh
cd tools/sn200-fw && sudo ./check-latch-state.sh /dev/nvmeN
```

⚠ **Armed-ness is the size probe's STATUS, not its value.** A section that is
not armed makes the probe **fail with SC 0xC3**; it does not return zero.
`0x00320000` is a fixed section reservation and says nothing about armed-ness.
And the probe reads different storage from the boot latch (bits 6/7 of a
hardware word vs bits 0/2 of a PROC0 byte) — strong proxy, not proof.

Full evidence and the procedure: `docs/sn200-nondestructive-recovery.md`.

**Prevention is the real fix here:** ensure power-down issues a real NVMe
shutdown (UPS-triggered orderly OS shutdown, not a delayed cut). Every unclean
stop re-arms the crash section.

### Get the crash dump FIRST — there is a script

Before anything that changes drive state, pull the dump. It names the assert
that actually fired. Reads are PROVEN side-effect-free and `0xC6` cmd `0x20` is
in the Post-Crash admin allow-list (below), so this works while latched:

```sh
cd tools/sn200-fw
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvmeN
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin
```

Re-run the same command line to resume after a reset. Full procedure and
provenance: `docs/sn200-crash-dump-retrieval.md`.

### ⚠ Commands that DESTROY a latched drive — never send these

**The selector is CDW12 — settled.** `ctx+0x38` = `CDW12[7:0]`, `ctx+0x39` =
`CDW12[15:8]`, confirmed against Firmware Image Download (`0x11`) whose
CDW10/CDW11 semantics are fixed by the NVMe spec. WD's `libdmi` was right; the
earlier CDW8 and CDW10 readings were wrong because they assumed a verbatim
64-byte SQE at `ctx+0x18` when the struct is actually **compacted**.

**Raw NAND erase and write are reachable on a LATCHED drive with no unlock.**
Both command bytes are in the 12-entry Post-Crash allow-list, and
`Admin_CheckCmdAllowed` and the `0xCA` dispatcher read the *same* byte:

| Never send | Effect |
|---|---|
| `0xCA` `CDW12[7:0]=0x0F` | **NAND block erase.** `CDW12[15:8]` is *ignored* — there is no harmless sub-value |
| `0xCA` `CDW12=0x0010` / `0x0110` | **Raw page write / program** |
| `0xFF` `CDW12=0x0403` | OAM "Drive Uninit" — **no startup-type gate at all**, sets the FACTORY re-init marker |
| `0xFF` `CDW12=0x0303` | Erase to SBL EEPROM — permanent brick |
| `0xDD` | Start Secure Purge (rejected while latched, but never type it) |

`0xCA` cmd `0x37` (multiplane write/erase) exists but is **not** allow-listed,
so it cannot be reached while latched.

**`CDW13` carries the raw physical flash address** for the whole raw-flash
family, and `CDW10` carries the write length. So a vendor command is only
reliably inert when **CDW10, CDW11, CDW12 *and* CDW13** are all zero. Nothing
about these encodings is guessable — `0x0F` erase sits two values from `0x10`
write, and `0x0403` sits one nibble from the `0x0503` used in recovery.

### ⚠ Read-only is not the same as harmless — pace admin traffic

**A latched drive is reset-looping.** Every admin command you send lands on a
controller that is cycling every ~5 s, and the driver is fighting to
re-establish queues each time. Sustained admin traffic against that will wedge
the host: on sea1-k8s-2 a 50-chunk crash-dump pull took Talos down — kernel
alive and pinging, but `apid` and `kubelet` both stopped answering, 7 OSDs
dropped and ceph went to 33% degraded. Recovery needed a Redfish
`GracefulRestart`.

The commands were individually read-only and individually safe. The *volume*
was the problem.

- **Issue the minimum number of commands.** One large read beats fifty small
  ones. If a probe tells you an offset field is ignored, you have your answer —
  do not repeat it 49 more times to be sure.
- **Set `noout` before touching a latched drive on a ceph host**, not after the
  node drops.
- Prefer a **diagnostics boot** over the production node for anything
  exploratory; that is what `tools/nvme-noreset/` exists for.

### Two safe reads — use these before inferring anything

Both are allow-listed, so they work on a latched drive, and neither changes
state:

```sh
# startup mode: is this drive latched at all?  byte[1]==6 => yes
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 \
     --cdw10=0 --cdw12=0x0004 --data-len=0

# OAM READ RAW SA CMD: read the System Area journal, i.e. the drive's ACTUAL
# marker, rather than deducing it from symptoms
nvme admin-passthru /dev/nvmeN --opcode=0xff --namespace-id=0 \
     --cdw10=0 --cdw12=0x0007 --data-len=<n> -r
```

`0x0007` is the one that ends arguments: it reads the real marker out of the SA
journal instead of inferring it. **Run it on every drive before deciding
anything** — five drives reporting their own markers beats one drive's symptoms
reasoned about at length.

### What a latched drive still accepts — PROVEN

`PROC8 0x7ffa6b18` hosts **four separate gates**, which is why earlier readings
contradicted each other — each was describing a different one. The Post-Crash
gate is the first (`0x7ffa6b30`–`0x7ffa6bd8`), guarded by `bnei a8,6` on the
startup-mode global at `0x7ff87c64`, and it is an **ALLOW-LIST**:

```
0x00 0x01 0x02 0x04 0x05 0x06 0x08 0x09 0x0A 0x0C 0x10 0x11 0xE6 0xEC 0xFF
0xC6  only when a4 ∈ {0x20, 0x30}
0xCA  with a 12-entry sub-list at 0x7ffa6d76
```

(`0x03/0x07/0x0B` are simply reserved in NVMe.) The reject path returns
`0x8F8A0000`; `>> 17` gives `0x47C5` = DNR|SCT7|SC=0xC5, which independently
confirms the `0x7C5` seen on the wire.

**Firmware Download (`0x10`) and Commit (`0x11`) are on this list** — which is
why the slot-fill and the `CA=2` activation recovery work on a latched drive.

☠ **`0xDD` is NOT permitted post-crash.** It appears exactly once in the whole
gate structure, in the **sanitize deny-list** at `0x7ffa6cb0`, so a latched
drive rejects it with `0x7C5`. Any note claiming `0xDD` is allowed, or that it
carries the OAM erase, is wrong — `0xDD` is **Start Secure Purge**, a
whole-drive crypto erase. The OAM erase is `0xFF`.

A previously-published "rejected: `0xCC, 0xD4, 0xD8–0xDF`" list was reading the
**wrong gate** — those are gate 2 (VUC Control, status `0x4001`), where
`0xD8–0xDF` are in fact the *permitted* opcodes.

### The trap

☠ **Never run `nvme wdc get-crash-dump`.** On a successful read it
automatically issues `0xFF`/`0x0503` to clear the dump (nvme-cli
`wdc_do_crash_dump()` → `wdc_do_clear_dump()`), and that schedules the REINIT
that **zeroes the namespace**. The vendor tool wipes the drive as a matter of
course. Use `pull-crash-dump.sh`, which cannot emit `0xFF` at all.

`dm-cli` / `nvme wdc get-crash-dump` **retrieve then clear**, and skip the
clear if retrieval failed (`"Crash dump not retrieved successfully, not
cleared"`). The 6.7 MB E6 pull cannot finish inside a 5s window.

That window limit is the **only** reason it fails. (An earlier note here claimed
a second silent failure via `hgst_nvmec_cap_diags_get_data`'s
`startup_type == expected` check. That was wrong: the check reads Identify
offset `0x40` = **Firmware Revision**, not Model Number, so `FR[0]=='K'` for
`KNGND1xx` leaves `expected == 6`, which *matches* the drive.)

**The clear does not need the dump.** That gate is two ints in host memory
(`HGSTNVMeController+0x40/+0x44`); the wire command is unconditional.

**You can pull the dump yourself** — `dm-cli` is not required. Opcode `0xC6`,
NSID 0: size `--cdw10=2 --cdw12=0x0320`, body `--cdw12=0x0420` with
`--cdw10=<dwords>`; pfail is `0x0520`/`0x0620`. The decoding string table is
`0x0120` (size in dword[1]) / `0x0220`.

### Recovery (proven) — but it is a wipe, decide first

**`0x0503` schedules a drive re-init, and that is what zeroes the namespace.**
The firmware's boot-marker enum has `Drive REINIT requested`; the erase path
sets it (`OAM ERASE CMD: Schedule reinit after crash dump erase failed.`) and
the next startup rebuilds the L2P. The drive came back healthy at full capacity
with zeros at every offset sampled to 3 TB. The data was not lost by the crash —
it was lost by this step. If the contents matter, pull the crash dump and try
everything else first.

Commands must land in the ~5s live window between resets. Poll for it:

```sh
cat > /root/fire.sh <<'EOF'
#!/bin/bash
for i in $(seq 1 600); do
  if nvme id-ctrl /dev/nvme7 >/dev/null 2>&1; then
    echo "[live at poll $i]"; "$@"; exit 0
  fi
done
echo "no live window in 600 polls"; exit 1
EOF
chmod +x /root/fire.sh
```

`EAGAIN`/"Resource temporarily unavailable" just means mid-reset — keep polling.
It took 175-408 polls per command in practice.

```sh
# 1. confirm the state FROM THE DRIVE: byte[1] of the result == 0x06
/root/fire.sh nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 \
  --cdw10=0 --cdw12=0x0004 --data-len=0
#    -> "result: 0x00000601"   06 = diagnostic mode

# 2. confirm a crash section is latched (non-zero = present)
/root/fire.sh bash -c "nvme admin-passthru /dev/nvme7 --opcode=0xc6 \
  --namespace-id=0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b | od -A d -t x4"
#    0x0520 for the pfail section. xxd is often absent; use od.

# 3. CLEAR. Skip the E6 capture entirely -- it cannot complete.
/root/fire.sh nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 \
  --cdw10=0 --cdw12=0x0503 --data-len=0    # crash dump
/root/fire.sh nvme admin-passthru /dev/nvme7 --opcode=0xff --namespace-id=0 \
  --cdw10=0 --cdw12=0x0603 --data-len=0    # pfail dump

# 4. Cold power cycle, OFF >=90s. There is NO host-side escape -- this was
#    established exhaustively: 28 dispatch tables, 78 command ids, and a scan
#    of every aligned u64 in .data/.rodata. EXIT_MODE / SET_MODE /
#    WRITE_MARKER are orphan names whose handlers are not in the binary, and
#    dispatch is a name-keyed hash built only from class tables, so no unlock
#    can add them. Firmware Commit is accepted while latched but CA=3
#    ("activate without reset") is unimplemented and every other activate
#    path demands a reset. The UART console has 8 commands and needs physical
#    pads. Power cycling is structural.
```

Still prefer `unbind` over the other resets when stopping the drive for any
*other* reason — it is the only one that even attempts `CC.SHN`.

**The one untested escape: NVMe-MI over SMBus.** PROC9 is a full MI/MCTP stack
on both SMBus and PCIe VDM, with an admin tunnel and `MI: Initiating an NVM
subystem reset`. SMBus survives the BIOS link-disable, so it can reach a drive
whose PCIe port is dead. Whether the tunnel passes vendor opcode `0xFF` is
unknown.

**Check WHICH section is armed before firing anything.** `0x0320` probes the
crash section, `0x0520` the pfail section. `0x0603` (pfail) clears
synchronously and appears harmless; `0x0503` (crash) schedules a **drive
re-init** that rebuilds the L2P and is the step suspected of zeroing the
namespace. If only the pfail section is armed, `0x0603` alone may recover the
drive with no re-init and no wipe. Firing both blind — as was done on
sea1-hv-2 — may destroy data unnecessarily.

**The clear returns Success but the size probe still reads non-zero.** That is
expected — the erase is *scheduled* and completes on the next startup. Do not
retry it in a loop thinking it failed.

**The two erases are not equivalent.** `0x0603` (pfail) takes effect
immediately — its size probe drops to zero while the drive is still looping.
`0x0503` (crash) does not; its probe stays at `0x00320000` until a startup
actually runs the scheduled erase. Seeing pfail clear and crash not clear is
the normal intermediate state, not a failure.

### No host-side reset substitutes for the startup

All of these were tried against a latched drive and **none** completed the
crash erase or restored the namespace — do not spend time on them again:

- FLR (`reset_method: flr`)
- Secondary Bus Reset / PERST# on the parent bridge (`setpci BRIDGE_CONTROL`
  bit 6, or `reset_method: bus`)
- `nvme subsystem-reset` (NSSR), despite `CAP.NSSRS: Yes`
- PCIe Link Disable held for 60s, then retrain
- `remove` + `rescan` in every combination with the above

Device power cannot be cut in-band either: the slot reports `SltCap: PwrCtrl-`
(no power controller, so `/sys/bus/pci/slots/*/power` is inert) and the port has
no ACPI `_PR3`, so `d3cold_allowed: 1` never becomes real power removal.

Untried and cheap: fire `0x0503`, **then** `unbind` (which issues a real
`CC.SHN` graceful shutdown and waits for `CSTS.SHST`), then `bind`. Ordering is
the point — the earlier attempts reset without a pending scheduled erase. Expect
it may still fail: `FAST STARTUP` is a distinct, lighter startup type and may not
consume the reinit marker.

**Widening the 5s window: don't mask the AEN, starve it.** Persistent Internal
Error is an *Error*-class AEN and is not maskable — the firmware's maskable list
is only the SMART warnings plus the two notice bits. But a controller can only
post an AEN if the host has an outstanding Asynchronous Event Request. Bind to
`vfio-pci` and drive the admin queue from userspace without ever submitting an
AER: no AEN, no `nvme_reset_ctrl()`, unlimited window.

Two other dead ends: `nvme get-log --log-id=0xe6` returns `0x4109` Invalid Log
Page (the E6 dump is not reachable as a log page on this drive — dm-cli pulls it
through the `0xC6` VUC), and `nvme set-feature -f 0x0b` is rejected `0x400b`
(SCT=0 SC=0x0b "Invalid Namespace or Format" — the firmware's own
`SetFeat NSID 0x%x not Attached`, i.e. a namespace failure, not an unsupported
feature).

VUC encoding, from `libdmi_core.so.0.39`: `CDW12 = (subcmd << 8) | cmd`,
opcode `0xFF`, NSID 0. Probe = cmd 4/sub 0; clear crash = cmd 3/sub 5; clear
pfail = cmd 3/sub 6. Sizes use opcode `0xC6`, `CDW10=2`, CDW12 `0x0320` crash /
`0x0520` pfail. `nvme wdc clear-assert-dump` is an SN640/SN840 opcode and
reports "unsupported device" here — not the SN200 path.

**There is no exit-diagnostic-mode command — power cycling is structural.**
Exhaustive: 28 dispatch tables, 78 command ids, and a scan of every aligned
`u64` in `.data`/`.data.rel.ro`/`.rodata` finds zero command ids outside them.
`EXIT_MODE`/`SET_MODE`/`WRITE_MARKER` are orphans whose **handler functions are
not in the binary**, and dispatch is a name-keyed hash built only from the class
tables, so no unlock can add them. Both raw-passthru functions are unreferenced
and unexported. `reset-to-defaults` *is* inherited (Omaha's parent is
GallantFox) but is refused at startup type 6 and resizes capacity anyway.
`attach-ns` is refused by the firmware itself —
`Admin_NamespaceAttachment: The LBN Translation Table is invalid.` WD's own tool
returns `HDMS_SHUTDOWN_REQUIRED` after a clear.

**The one untested avenue is NVMe-MI over SMBus.** PROC9 is a full NVMe-MI /
MCTP stack running on **both** SMBus and PCIe VDM — `MI_AdminCmdHandler`,
`MI_PCIECmdHandler` (config read/write), and `MI: Initiating an NVM subystem
reset`. SMBus is independent of the PCIe link, so this is the only path that
survives a BIOS link-disable (`UEFI0067`). Six admin opcodes are whitelisted
through the MI tunnel; whether vendor `0xFF` is among them is **UNKNOWN**. If it
were, the clears could be issued from a BMC with no PCIe link at all. The MI
NSSR is implemented but is still a reset without `CC.SHN`, so expect it to
re-arm rather than clear.

**The `DiagMgr>` UART console is a dead end.** 8 commands, physical pads only
(115200 8N1, both I/O via SBL-owned function pointers; nothing reaches the line
parser from any admin/VUC/MI path). `Load` is a compiled-out stub. The only
state-changing commands are ☠ `SBL` (hands the chip to the bootloader, drive
leaves the bus), ☠ `I2CErase` (blanks 512 B of all three I2C EEPROM copies —
plausibly the boot TOC), and `LogicTrap` (deliberately *creates* a crash).

Full VUC map, status-code tables and firmware control flow:
`docs/sn200-firmware-re.md`.

### Expect the data to be GONE

The drive came back healthy at full capacity with **a completely zeroed
namespace** — no GPT, no filesystem, zeros at every offset sampled from 1 MiB
to 3 TB. `nuse == nsze` means fully provisioned, **not** that data exists.
Plan for total loss of whatever was on it; treat any survival as a bonus.

### Do NOT

- **Do not flash firmware.** `KNGND122` is the newest image that exists; the
  deallocate/reset diagnostic-mode bugs (OM-6588, OM-6697, OM-6836, OM-6850,
  OM-7044) were fixed in KNGND110 and are already in it. No upgrade path.
- **Do not slot-cycle blindly.** Errata: activating unsupported revisions "may
  cause the drive to be non-operational". `KNGND112` (common in slots 1-3) is
  undocumented — no release notes or binary exists for it.
- **Do not use `+sblpatch+k` images.** `+k` writes *every* slot, destroying the
  fallback, and WD does not support downgrade once the secondary boot loader is
  updated.
- Never `nvme format`, `sanitize`, `wdc purge`, or `delete-ns`. On this drive
  the raw purge is opcode `0xDD`, fire-and-forget with no confirmation argument;
  `0xDE` (`--cdw10=0x0C --data-len=48 -r`) is the safe status counterpart.
- **Do not sweep the `cmd 3` sub-command space.** Valid range is 0-6 and the two
  directly below the ones you want are the dangerous ones: sub 3 ~ Erase SBL
  EEPROM (`0x0303`, hard brick) and sub 4 ~ Drive Uninit (`0x0403`). Only 5 and
  6 are safe. The `OAM ERASE CMD:` string order is *not* the case order.
- Never flash SN150 `KMGNP*` images to an SN200 — different ASIC.

### Firmware package facts

Images are plain uncompressed tar: `FWHEADER.bin` (64 B, revision string at file
offset 512), `PROC0..15.bin`, `FCC.bin`, `StringTable.csv.gz`, `SECURITY.bin` —
then **ONE** end-of-archive zero block (not the usual two) and a **256-byte
high-entropy trailer at EOF-256**, different per revision. `SECURITY.bin` is
byte-identical across all revisions, so the trailer is the per-image signature.

**Bytes 508–511 of every ustar header hold a little-endian CRC-32/MPEG-2**
(poly `0x04C11DB7`, init `0xFFFFFFFF`, no reflection, no final XOR) of that
member's data — standard tar leaves them zero. Verified 61/61 members across
`KNGND100`/`110`/`122`. The drive parses the tar; it is not an opaque blob.

`KNGND110.bin` carries a **21st member `SBLPATCH.bin`** that `KNGND122.bin` does
not. That is the machine-checkable tell for the `+sblpatch+k` image (which
writes every slot including the read-only one and updates the SBL — never flash
it). `tools/sn200-fw/fill-fw-slots.sh` refuses on it.

Version order: `KNGNP100` -> `KNGND100` -> `KNGND110` -> `KNGND122`. Slot
targeting is a host-side `Firmware Commit (0x10)` parameter, not encoded in the
image.

`PROC*.bin` are Tensilica **Xtensa** LE images (16 cores + an `FCC` core) in a
`.BIN`/`.SEG` container: 16-byte headers `{"​.SEG", abs_data_offset, len,
load_addr}` chained until `0xffffffff`. PROC8 is the Admin/host processor.

**Ghidra cannot disassemble these** — the cores use FLIX (VLIW) 8-byte bundles
and Ghidra's Xtensa module desyncs on them, so its decompiler output is garbage.
Use `tools/sn200-fw/` instead:

```sh
python3 tools/sn200-fw/unpack.py KNGND122.bin ~/sn200fw
export SN200_FW=~/sn200fw
python3 tools/sn200-fw/logmap3.py 'Post Crash'   # find code by log message
python3 tools/sn200-fw/drv.py 300336c6 300336f5  # disassemble a range
```

`StringTable.csv` line N is **StrId N-1**. Format strings are not in the images;
the firmware logs a descriptor `(StrId<<16)|(level<<8)|nargs` loaded by `l32r`.

## Sources

- <https://forum.level1techs.com/t/hgst-wdc-ultrastar-sn200-recovery-from-persistent-internal-error-diagnostic-state/250303>
- <https://virtualbytes.io/vmware-cloud-foundation-9-x-fixing-wd-hgst-ultrastar-dc-sn200-nvme-drives-stuck-in-diagnostic-mode-orange-led-blinking/>
- WD Device Manager `dm-core-2.5.1-7` (`libdmi_core.so.0.39`) and the SN200 firmware string tables
