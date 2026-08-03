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
does not finish still lands in the "never finished" handler. Allow time after
`unbind` and do not assume a returned ioctl means the drive is safe to cut.

**A warm reboot is not a power cycle** — it does not drop rail power. But do
not reach for `ForceOff` reflexively either: an abrupt power cut is an unclean
stop and can *cause* the latched state on drives that record shutdown
completion. On a ceph host it also takes the OSDs down — confirm the cluster
tolerates it first, `noout` alone is not sufficient.

## HGST / WDC Ultrastar SN200: "Post Crash Startup" lockup

Model `HUSMR7676BDP3Y1`, firmware `KNGND1xx`. Procedure below is what actually
worked, not what the forum posts say. Full firmware teardown:
`docs/sn200-firmware-re.md`.

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

**Suspect the drive, not the cable — this is fleet-wide.** It has been observed
on every host using an SN200, across different cables, bays and chassis. A
marginal U.2 cable can aggravate it (U.2 carries `PC12V`/`ATX12V`, monitored via
`I2C_DEVICE_VMON`, and a high-resistance connector gives both link-training
failures and I²R rail droop) but cannot explain fleet-wide incidence.

**Leading hypothesis: aged power-loss-protection capacitors.** `VCAP has failed,
drive is in write protect mode` is a distinct firmware posture. A batch of
same-age drives with degraded hold-up caps means every power event starts a
PFAIL save that cannot finish inside the shrinking budget → marker 6/7 → latch.
That fits the correlation with power events, and fits why peak-current
workloads make it worse — a weak cap sags fastest under load. **Measure VCAP
health before deciding to keep or bin.** `KNGND122` (2020) is the newest
firmware that exists, so a defect persisting there has no fix.

There is no code path from LINKDOWN/PERST to PFAIL (PFAIL lives in PROC0, PCIe
in PROC9). A disabled port does still guarantee an unclean stop, since `CC.SHN`
can never be delivered.

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

### Get the crash dump FIRST — there is a script

Before anything that changes drive state, pull the dump. It names the assert
that actually fired. Reads are PROVEN side-effect-free and `0xC6` cmd `0x20` is
in the Post-Crash admin allow-list, so this works while latched:

```sh
cd tools/sn200-fw
sudo ./pull-crash-dump.sh --section all --chunk-size 65536 /dev/nvmeN
./decode-crash-dump.py sn200-dump-*/crash.bin --string-table sn200-dump-*/strtbl.bin
```

Re-run the same command line to resume after a reset. Full procedure and
provenance: `docs/sn200-crash-dump-retrieval.md`.

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

Images are plain uncompressed tar: `FWHEADER.bin`, `PROC0..15.bin`, `FCC.bin`,
`StringTable.csv.gz`, `SECURITY.bin`. Nothing is encrypted or per-image signed
(`SECURITY.bin` is byte-identical across all revisions). Version order:
`KNGNP100` -> `KNGND100` -> `KNGND110` -> `KNGND122`. Slot targeting is a host-side
`Firmware Commit (0x10)` parameter, not encoded in the image.

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
