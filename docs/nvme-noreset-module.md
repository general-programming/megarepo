# nvme-noreset: suppressing the persistent-internal-error controller reset

Source: `tools/nvme-noreset/`. Operational instructions live in that
directory's `README.md`; this document is the background, the evidence, and
the decision.

## The problem

An HGST/WDC Ultrastar SN200 (`HUSMR7676BDP3Y1`, PCI `1c58:0023`) boots into
firmware "Post Crash Startup" diagnostic mode. It presents no namespace and
raises an NVMe Asynchronous Event — Error class, subtype `03h` Persistent
Internal Error — roughly every 5 seconds. Linux answers every one of them with
a controller reset, so:

- every diagnostic or vendor command must complete inside a ~5 s window, which
  a 6.7 MB E6 crash-dump pull cannot;
- worse, the firmware records each abrupt reset as `UNEXSTRT` — an unexpected
  start — which **re-arms the crash section**. The kernel's own reset loop is
  plausibly perpetuating the fault it is reacting to.

Full firmware analysis: `docs/sn200-firmware-re.md`. Field evidence:
`docs/sn200-field-evidence.md`. Recovery procedure: the `nvme-recovery` skill.

## The code, as actually shipped in 7.0.2-6-pve

Verified against the exact source the running kernel was built from, not from
memory (`drivers/nvme/host/core.c`):

```c
static void nvme_handle_aer_persistent_error(struct nvme_ctrl *ctrl)
{
	dev_warn(ctrl->device,
		"resetting controller due to persistent internal error\n");
	nvme_reset_ctrl(ctrl);
}
```

called from `nvme_complete_async_event()`:

```c
	case NVME_AER_ERROR:
		/*
		 * For a persistent internal error, don't run async_event_work
		 * to submit a new AER. The controller reset will do it.
		 */
		if (aer_subtype == NVME_AER_ERROR_PERSIST_INT_ERR) {
			nvme_handle_aer_persistent_error(ctrl);
			return;
		}
		fallthrough;
```

That comment is the lever. The AEN loop only continues because the reset
resubmits the AER. **Suppress the reset and nothing resubmits it**, so the host
has no outstanding Asynchronous Event Request and the controller cannot post
another AEN. One log line, then silence, and an unbounded command window — the
in-kernel equivalent of the "starve the AER" `vfio-pci` trick, but keeping
`/dev/nvmeN` and nvme-cli.

## What was built

`tools/nvme-noreset/` — an out-of-tree rebuild of **`nvme-core.ko` only**,
DKMS-packaged, adding three opt-in PCI-scoped module parameters:

| Parameter | Default | Effect for matching devices |
|---|---|---|
| `nvme_core.persist_err_noreset_ids` | `""` | Log rate-limited instead of calling `nvme_reset_ctrl()` |
| `nvme_core.zero_discard_ids` | `""` | `nvme_config_discard()` sets `max_hw_discard_sectors = 0` at namespace setup |
| `nvme_core.max_admin_xfer_ids` | `""` | The **admin queue only** gets `max_hw_sectors`/`max_segments` raised to 8192 sectors (4 MiB) in `nvme_set_ctrl_limits()` |

Entries are exact `vendor:device` (`1c58:0023`) or exact PCI address
(`0000:b2:00.0`). **There is no wildcard**, deliberately: this host has seven
Intel SSDPE2KX020T8 carrying live ceph OSDs on the same driver, and a
mis-scoped build would put the storage cluster at risk. All three parameters
are `0444`, so they cannot be changed at runtime and the matcher cannot race a
sysfs write.

The delta is small on purpose — `patches/nvme-noreset.patch` is three hunks in
`core.c` plus one `#include` and one init call; everything else lives in a new
self-contained `src/noreset.c`. This is what makes re-vendoring for another
kernel version cheap.

### Where the admin transfer ceiling actually lives, and why it's raisable here

The blocker: a 3.2 MiB single-shot vendor admin read fails the host-side
ioctl with `EINVAL` above 128 KiB (256 sectors / 32 pages), even though `mdts`
reports 0 (unbounded) and no `nvme`/`nvme_core` module parameter changes it.

Traced through the vendored 7.0.2-6-pve source:

1. `ctrl->max_hw_sectors` is first set in `nvme_pci_alloc_dev()`
   (`drivers/nvme/host/pci.c`, **`nvme.ko`, not vendored here**):
   `min(NVME_MAX_KB_SZ << 1, dma_opt_mapping_size(dev) >> 9)`.
2. `dma_opt_mapping_size()` is an IOMMU **optimal**-mapping-size hint
   (`iommu_dma_opt_mapping_size()` → `iova_rcache_range()` = `32 * PAGE_SIZE`
   on a 4K-page host = 128 KiB), not a hardware maximum. That 32-page number
   is exactly the observed cliff.
3. `nvme_init_identify()` in `core.c` (vendored, ours to patch) combines this
   with `mdts` via `min_not_zero(ctrl->max_hw_sectors, mdts_derived)`. With
   `mdts == 0`, `mdts_derived = UINT_MAX`, so this combination is a no-op —
   it can only ever lower the value, never raise it.
4. That same function then commits the result into `ctrl->admin_q`'s block
   layer queue limits via `nvme_set_ctrl_limits(ctrl, &lim, true)` — **this
   commit point is nvme-core's own code**, and it is what
   `nvme_map_user_request()` → `blk_rq_map_user_io()` checks the ioctl's
   `bufflen` against, producing the observed `EINVAL`.

So although the *value* 128 KiB originates in `nvme.ko` (out of scope — a
different module entirely, and touching it forfeits the CRC guarantee), the
*commit* of that value onto the admin queue happens inside `nvme-core.c`,
in a function this module already patches. `max_admin_xfer_ids` overrides the
commit for allow-listed devices, `is_admin == true` only:

```c
if (is_admin) {
	u32 admin_sectors = nvme_noreset_max_admin_sectors(ctrl->dev);
	if (admin_sectors) {
		lim->max_hw_sectors = admin_sectors;
		lim->max_segments = min_t(u32, USHRT_MAX,
			min_not_zero(admin_sectors /
				(NVME_CTRL_PAGE_SIZE >> SECTOR_SHIFT) + 1,
				ctrl->max_segments));
	}
}
```

Two things keep this safe:

- **`ctrl->max_hw_sectors` itself is never touched**, only the local
  `queue_limits` struct used for the admin queue's own commit. Namespace disk
  queues call the same function with `is_admin = false` and are byte-for-byte
  unaffected — this is genuinely admin-queue-scoped, not a global change.
- **`lim->max_segments` is still hard-clamped against `ctrl->max_segments`**
  (`NVME_MAX_SEGS = 128` in the vendored `pci.c`, `nvme.ko`'s own fixed-size
  `struct scatterlist` allocation). The override can raise the *byte* ceiling
  arbitrarily, but it can never ask the transport driver to build more DMA
  segments than the memory it already allocated for that purpose — so this
  cannot overrun anything in `nvme.ko`, even though `nvme.ko` is completely
  unmodified and unaware of this parameter.

8192 sectors (4 MiB) was chosen to comfortably cover the 3.2 MiB dump with
headroom, while staying well inside `NVME_MAX_KB_SZ << 1` (16 MiB) —
`nvme_pci.c`'s own hard ceiling before the `dma_opt_mapping_size()` clamp —
so the chosen value has never been rejected by anything upstream of
nvme-core.

**The remaining ceiling — segments, not bytes.** Raising the byte limit does
not by itself guarantee 3.2 MiB lands in one DMA transfer: a userspace buffer
backed by scattered 4 KiB anonymous pages can still exhaust the 128-segment
cap before the byte cap. `blk_rq_map_user_io()` coalesces *physically
contiguous* page runs into one segment, so a buffer backed by a small number
of large contiguous allocations — 2 MiB hugetlbfs/`MAP_HUGETLB` pages
coalesce a 3.2 MiB buffer into as few as 2 segments — reaches the ceiling
comfortably; an ordinary `malloc()` may not. This is the honest remaining
constraint, not fixable from nvme-core: it is a property of the userspace
buffer's physical layout and the transport's fixed segment count, not
something a module parameter can override further.

The in-driver discard suppression is more robust than a udev rule because it
runs at namespace setup, before the block device exists, so it cannot lose a
race against early I/O. A large deallocate is the confirmed trigger for this
firmware bug (WD errata OM-6588).

## Build and verification results

Built in a Debian 13 / amd64 container against the real
`proxmox-headers-7.0.2-6-pve`. Reproduce with
`tools/nvme-noreset/build-test.sh`.

- **Compiles clean**, no warnings. `vermagic: 7.0.2-6-pve SMP preempt
  mod_unload modversions`, `depends: nvme-auth,nvme-keyring` — both identical
  to the stock module.
- **All 72 exported symbol CRCs are byte-identical to the stock kernel's
  `Module.symvers`.** This is the important result: stock `nvme.ko`,
  `nvme-fabrics`, `nvme-tcp`, `nvme-rdma`, `nvme-fc` load against the
  replacement unchanged. `check-crc.sh` performs this comparison and
  `install.sh` aborts before `dkms install` if it ever fails.
- All three parameters appear in `modinfo`.

### The vendoring trap this exposed

Building from the upstream kernel.org `linux-7.0.2` tarball produced **51 of 72
CRCs different** and one missing export. Cause: the Proxmox kernel's Ubuntu
base (`Ubuntu-7.0.0-18.18`) un-statics and exports
`nvme_keep_alive_work_period`, which changes `struct nvme_ctrl`'s DWARF and
therefore every CRC that transitively involves it. Loading that build would
have made `nvme.ko` refuse to load — **no NVMe on the host at all**, every OSD
down.

`src/` is therefore vendored from the exact tree the kernel was built from:
`git.proxmox.com` `mirror_ubuntu-kernels` tag `Ubuntu-7.0.0-18.18`
(`69bb061d…`), resolved via `pve-kernel.git` commit `87f22e5` ("update ABI file
for 7.0.2-6-pve"). Proxmox itself carries no nvme patches. Provenance is
recorded in `src/VENDORED-FROM`; `fetch-sources.sh` automates the resolution
for other releases.

Never assume the distro kernel's driver source equals upstream. Run
`check-crc.sh` every single time.

### Not verified

The module has **not been loaded on real hardware**. The drive is in a
datacenter and hardware work is paused. Compile, vermagic, dependency list and
full symbol-CRC equality are proven; runtime behaviour is not.

## Alternatives considered

### 1. kprobe no-op of the handler — ruled out by inlining

`nvme_handle_aer_persistent_error()` is `static` with exactly one caller, so
GCC inlines it. Confirmed empirically against the shipped binary: `nm` on
`/usr/lib/modules/7.0.2-6-pve/kernel/drivers/nvme/host/nvme-core.ko` finds no
symbol matching `persistent`. There is nothing to attach a kprobe to.
`nvme_config_discard()` is inlined for the same reason.

**Variant that does work:** kprobe `nvme_reset_ctrl` (a real global, `T` at
`0x4350`), filter on the `struct nvme_ctrl *` in `%rdi`, and skip the body by
pointing `regs->ip` at a bare `ret` — the mechanism `override_function_with_return()`
uses. The kernel supports it (`CONFIG_KPROBES=y`, `KPROBES_ON_FTRACE=y`,
`FUNCTION_ERROR_INJECTION=y`, `BPF_KPROBE_OVERRIDE=y`, and
`CONFIG_X86_KERNEL_IBT` is off so a bare-ret trampoline is safe).

Honest assessment: it has a genuinely smaller blast radius — nothing is
replaced and `rmmod` reverts it instantly — but

- filtering by PCI ID needs `offsetof(struct nvme_ctrl, dev)` from a private,
  config-dependent header, so it needs the same vendored source anyway;
- it blocks *every* reset of that device, including a deliberate `nvme reset`;
- returning success from a reset that never happened lies to callers;
- `override_function_with_return()` is not exported and is not a supported
  module API — you are reimplementing an error-injection hack;
- it does nothing about DISCARD, so you still need the udev rule.

Not implemented, because the situations where it would be worth its fragility
are the ones where the recommendation below says to use a diagnostics boot
instead.

### 2. Kernel livepatch

`CONFIG_LIVEPATCH=y` on this kernel, and livepatch can patch functions in a
loaded module (`klp_object.name = "nvme_core"`). Operationally it is the
nicest story: no initramfs, no reboot, atomic enable/disable via
`/sys/kernel/livepatch/*/enabled`.

But the function you must replace is `nvme_complete_async_event()`, and it
calls static symbols (`nvme_handle_aen_notice`, the tracepoint, `nvme_wq`).
Reaching those from a livepatch module requires KLP relocations
(`.klp.sym.*`), which in practice means adopting `kpatch-build` — a larger
toolchain dependency than DKMS, on a kernel it has never been tested against.
More work than the DKMS module, not less. Rejected on cost, not on merit.

### 3. `vfio-pci` / no kernel driver attached

Binding the SN200 to `vfio-pci` means the kernel never submits an AER, so the
controller can never post the AEN, so there is no reset — the same starvation
this module achieves, with **zero kernel modification and zero risk to the
other seven drives**:

```sh
echo vfio-pci > /sys/bus/pci/devices/0000:b2:00.0/driver_override
echo 0000:b2:00.0 > /sys/bus/pci/drivers/nvme/unbind
echo 0000:b2:00.0 > /sys/bus/pci/drivers_probe
```

The cost is that there is no block device and no `/dev/nvmeN`, so `nvme-cli`
cannot be used at all; the admin queue must be driven from userspace
(`libvfn`, or SPDK configured not to register an AER). Every VUC in the
recovery runbook — `0xFF`/`cdw12=0x0004` probe, `0xC6` size probes,
`0x0503`/`0x0603` clears — then has to be reimplemented against that API.

Plain `unbind` with no `vfio-pci` also stops the thrash, but leaves no way to
send any command. It is a way to make the drive quiet, not a way to work on it.

`initcall_blacklist` does not apply: `nvme`/`nvme_core` are modules here, not
built-in initcalls. The module-level equivalent, `modprobe.blacklist=nvme`,
would take out all seven ceph OSD drives too.

### 4. Do nothing in the kernel; mitigate in userspace

The only thing production actually needs is that routine use never issues a
large DISCARD to this drive. A udev rule achieves that with no kernel risk:

```
# /etc/udev/rules.d/60-sn200-nodiscard.rules
ACTION=="add|change", SUBSYSTEM=="block", ATTRS{model}=="HUSMR7676BDP3Y1*", \
  ATTR{queue/discard_max_bytes}="0"
```

plus the belt-and-braces already in the runbook: `mkfs.xfs -K`,
`mkfs.ext4 -E nodiscard`, no `discard` mount option, LVM `issue_discards = 0`,
no whole-device `fstrim`. This is the same mitigation shipping on the Talos
node via `machine.udev.rules`.

## Recommendation

### (a) Throwaway diagnostics OS — use this module

This is what it is for. It is the only option that gives an unlimited command
window *and* keeps `nvme-cli` working, so the entire existing recovery runbook
applies unchanged, without the polling loop. Nothing else on that boot matters
if the module misbehaves.

Build it for the ISO's kernel first — `./fetch-sources.sh --upstream <ver>` for
a mainline-based live image, `--pve <rel>` for a Proxmox one — then
`./check-crc.sh`. If the live ISO's kernel headers are unobtainable, fall back
to `vfio-pci` + `libvfn`.

Second choice on a diagnostics boot, if a matching build is impossible:
`vfio-pci`, accepting the userspace reimplementation cost.

### (b) Production Proxmox host — do not install it

Recommendation is **no**, and it is not a close call:

- Seven live ceph OSDs share `nvme-core`. The failure mode of getting this
  wrong is "host has no NVMe on next boot". The CRC gate makes that very
  unlikely, but "very unlikely" against "all OSDs on the node" is a bad trade
  for a *diagnostic convenience*.
- Arming it requires an initramfs rebuild and a reboot, which on this host
  means an OSD outage anyway — at which point you could just as well have
  booted diagnostics.
- `AUTOINSTALL="no"` is deliberate, so every kernel upgrade either silently
  reverts to stock (confusing) or demands a manual re-vendor (a standing
  maintenance tax). Neither is acceptable for something that is not needed
  during normal operation.
- Production does not need reset suppression. Production needs the drive not to
  be destroyed by routine use, and the udev rule in §4 already does that.

Production posture instead: keep the udev rule; if the drive is thrashing, take
it out of the kernel driver's hands with `unbind` (or `driver_override` to
`vfio-pci` so nothing rebinds it on rescan); do the actual recovery from a
diagnostics boot.

If reset suppression is ever genuinely needed on the production host without a
reboot, the kprobe shim in §1 is the right shape for it — reversible with
`rmmod`, nothing replaced — and should be built then, with its limitations
understood.

### Secure Boot note

`CONFIG_MODULE_SIG_FORCE` is not set on this kernel, so an unsigned DKMS module
loads (tainting the kernel). Under Secure Boot, lockdown still requires a
signed module — enrol a MOK and let DKMS sign, or the module will be rejected
with `Key was rejected by service`.
