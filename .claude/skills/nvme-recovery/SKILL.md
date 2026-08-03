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

Model `HUSMR7676BDP3Y1`, firmware `KNGND1xx`. Triggered by hard power cycles.
**Recovered on sea1-hv-2, 2026-08-03** — procedure below is what actually
worked, not what the forum posts say.

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
Admin cmd rejected due to Post Crash startup mode: 0x%x      <- this is status 0x7d3 / SC=0xD3
SYS: Detected a CRASH or PFCRASH section.
OAM ERASE CMD: Schedule reinit after crash dump erase failed.
```

The drive is refusing admin commands to protect a crash dump, and **erasing
that dump is what schedules the reinit out of the mode**. `CSTS=0x1` (RDY set,
CFS clear) is correct — the controller really is "up".

### The trap

`dm-cli` / `nvme wdc get-crash-dump` **retrieve then clear**, and skip the
clear if retrieval failed (`"Crash dump not retrieved successfully, not
cleared"`). The 6.7 MB E6 pull cannot finish inside a 5s window, so the vendor
tooling never clears, and the drive re-enters the mode every boot. Self-
sustaining.

**The clear does not need the dump.** That gate is tool-side policy, not
drive-side — verified in `libdmi_core.so`: nothing on that path checks drive
health, namespace presence, or capture status at the controller.

### Recovery (proven)

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

VUC encoding, from `libdmi_core.so.0.39`: `CDW12 = (subcmd << 8) | cmd`,
opcode `0xFF`, NSID 0. Probe = cmd 4/sub 0; clear crash = cmd 3/sub 5; clear
pfail = cmd 3/sub 6. Sizes use opcode `0xC6`, `CDW10=2`, CDW12 `0x0320` crash /
`0x0520` pfail. `nvme wdc clear-assert-dump` is an SN640/SN840 opcode and
reports "unsupported device" here — not the SN200 path.

### Expect the data to be GONE

The drive came back healthy at full capacity with **a completely zeroed
namespace** — no GPT, no filesystem, zeros at every offset sampled from 1 MiB
to 3 TB. `nuse == nsze` means fully provisioned, **not** that data exists.
Plan for total loss of whatever was on it; treat any survival as a bonus.

### Do NOT

- **Do not flash firmware.** `KNGND122` is the newest image that exists; the
  diagnostic-mode bugs (OM-6850, OM-7059, OM-6697) were fixed in KNGND110 and
  are already in it. There is no upgrade path.
- **Do not slot-cycle blindly.** Errata: activating unsupported revisions "may
  cause the drive to be non-operational". `KNGND112` (common in slots 1-3) is
  undocumented — no release notes or binary exists for it.
- **Do not use `+sblpatch+k` images.** `+k` writes *every* slot, destroying the
  fallback, and WD does not support downgrade once the secondary boot loader is
  updated.
- Never `nvme format`, `sanitize`, `wdc purge`, or `delete-ns`.
- Never flash SN150 `KMGNP*` images to an SN200 — different ASIC.

### Firmware package facts

Images are plain uncompressed tar: `FWHEADER.bin`, `PROC0..15.bin`, `FCC.bin`,
`StringTable.csv.gz`, `SECURITY.bin`. Nothing is encrypted or per-image signed
(`SECURITY.bin` is byte-identical across all revisions). Version order:
`KNGNP100` -> `KNGND100` -> `KNGND110` -> `KNGND122`. Slot targeting is a host-side
`Firmware Commit (0x10)` parameter, not encoded in the image.

## Sources

- <https://forum.level1techs.com/t/hgst-wdc-ultrastar-sn200-recovery-from-persistent-internal-error-diagnostic-state/250303>
- <https://virtualbytes.io/vmware-cloud-foundation-9-x-fixing-wd-hgst-ultrastar-dc-sn200-nvme-drives-stuck-in-diagnostic-mode-orange-led-blinking/>
- WD Device Manager `dm-core-2.5.1-7` (`libdmi_core.so.0.39`) and the SN200 firmware string tables
