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

**A warm reboot is not a power cycle.** It does not drop rail power, so it will
not clear a latched internal error. Only a true `ForceOff` → wait ≥30s → `On`
does. On a ceph host this takes the OSDs down — confirm the cluster tolerates
it first, `noout` alone is not sufficient.

## HGST / WDC Ultrastar SN200: "Post Crash Startup" lockup

Model `HUSMR7676BDP3Y1`, firmware `KNGND1xx`. Procedure below is what actually
worked, not what the forum posts say. Full firmware teardown:
`docs/sn200-firmware-re.md`.

**Trigger: large deallocate/TRIM, not power cycling.** WD's own release notes
name it — OM-6588 "Drives failed to restore L2P table after large deallocate
and a pfail", OM-6850 "Drive in crashed state following Power Cycle, Controller
Reset, and Deallocate Test", OM-6836, OM-7044. A whole-device TRIM (e.g.
`mkfs.xfs` on 7.68 TB) races the L2P journal flush. **Prevent it:** `mkfs.xfs -K`,
`mkfs.ext4 -E nodiscard`, mount without `discard`, LVM `issue_discards = 0`, no
whole-device `fstrim`.

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

### The trap

`dm-cli` / `nvme wdc get-crash-dump` **retrieve then clear**, and skip the
clear if retrieval failed (`"Crash dump not retrieved successfully, not
cleared"`). The 6.7 MB E6 pull cannot finish inside a 5s window.

It fails a second way on this model, silently. `hgst_nvmec_cap_diags_get_data`
only records "retrieved" if `startup_type == expected`, where `expected` is 7
when `IDCTRL.MN[0]=='H' && ((MN[3]+0xBF)&0xFF) >= 5` — true for `HUSMR…`. The
drive reports 6, so the flag stays at its `-2008` sentinel and `cap_diags_end`
takes neither branch: no clear, **no message**.

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

# 4. cold power cycle, OFF for >=90s. A warm reboot NEVER clears this.
```

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

**There is no exit-diagnostic-mode command.** All 21 dispatch tables in
`libdmi_core` were enumerated: `EXIT_MODE`, `SET_MODE`, `WRITE_MARKER` exist in
the command-id enum but are in no table, and SN200's `omc_cmds` has no
`RESET_TO_DEFAULTS`. WD's own tool returns `HDMS_SHUTDOWN_REQUIRED` after a
successful clear — it expects a power cycle.

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
