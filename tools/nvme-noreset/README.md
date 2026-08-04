# nvme-noreset

A modified `nvme-core` kernel module that can be told, **per PCI device**, to
stop resetting a controller that raises the NVMe "persistent internal error"
asynchronous event, to refuse to advertise DISCARD for that device, and to
raise the admin queue's transfer ceiling for a single large vendor command.

Built for the HGST/WDC Ultrastar SN200 (`HUSMR7676BDP3Y1`, PCI `1c58:0023`)
stuck in firmware "Post Crash Startup" diagnostic mode. See
`docs/nvme-noreset-module.md` for the background and the full decision writeup.

---

## ⚠️ Blast radius — read this first

There is one `nvme-core` module on a host and **every NVMe device uses it.**
Installing this replaces that module for all of them.

On `sea1-hv-2` that includes **seven Intel SSDPE2KX020T8 carrying live ceph
OSDs**. If the replacement module fails to load, the host has no NVMe at all
on the next boot — every OSD on it goes down.

Two things reduce that risk to near zero, and both are enforced by the tooling:

1. **Default behaviour is byte-identical to stock.** All three knobs are empty
   allow-lists. With no module parameters, no code path changes for any device.
   There is no wildcard: an entry must be an exact `vendor:device` or an exact
   PCI address, so it is not possible to accidentally match all NVMe.
2. **`check-crc.sh` proves ABI compatibility before install.** It compares all
   72 symbol CRCs the rebuilt `nvme-core` exports against the stock kernel's
   `Module.symvers`. They are identical, so stock `nvme.ko`, `nvme-fabrics`,
   `nvme-tcp`, `nvme-rdma` and `nvme-fc` still load unchanged — only
   `nvme-core.ko` is replaced. `install.sh` aborts if this ever stops holding.

**The honest recommendation is still: do not install this on the production
Proxmox host.** Use it from a diagnostics boot. Reasoning in
`docs/nvme-noreset-module.md` § Recommendation.

---

## What it changes

Three hunks in `drivers/nvme/host/core.c` (`patches/nvme-noreset.patch`), plus
a new self-contained `src/noreset.c` holding the parameters and the matcher.

| Parameter | Default | Effect |
|---|---|---|
| `nvme_core.persist_err_noreset_ids` | `""` | For matching devices, an Error-class AEN with subtype `03h` logs a rate-limited warning instead of calling `nvme_reset_ctrl()`. |
| `nvme_core.zero_discard_ids` | `""` | For matching devices, `nvme_config_discard()` sets `max_hw_discard_sectors = 0` at namespace setup, so the block layer never issues a DSM/deallocate. |
| `nvme_core.max_admin_xfer_ids` | `""` | For matching devices, the **admin queue only** gets `max_hw_sectors`/`max_segments` raised to 8192 sectors (4 MiB), so one large vendor admin command doesn't need chunking. Namespace I/O queues are untouched. |

All three take a comma-separated list; each entry is either `vendor:device`
(`1c58:0023`) or a PCI address (`0000:b2:00.0`, or `b2:00.0`). All are
`0444` — set them at modprobe or on the kernel command line, not at runtime,
so a concurrent sysfs write can never race the matcher.

A marker line is printed by `nvme_core_init()` at every load:

```
nvme-noreset: patched nvme-core active (persist_err_noreset_ids="1c58:0023" zero_discard_ids="1c58:0023" max_admin_xfer_ids="1c58:0023")
```

### Where the 128 KiB admin ceiling actually comes from

`ctrl->max_hw_sectors` starts life in `nvme_pci.c` (`nvme.ko`, not part of
this module): `min(NVME_MAX_KB_SZ << 1, dma_opt_mapping_size(dev) >> 9)`.
`dma_opt_mapping_size()` is an IOMMU **optimal**, not maximum, mapping-size
hint (`iova_rcache_range()`, `32 * PAGE_SIZE` = 128 KiB on a 4K-page host) —
that's the real origin of the 256-sector / 32-page cliff. On this hardware
`mdts` reports 0 (unbounded), so nvme-core's own mdts combination in
`nvme_init_identify()` (`min_not_zero(ctrl->max_hw_sectors, UINT_MAX)`) never
lowers that 128 KiB value, but it never raises it either — nvme-core just
commits whatever `nvme_pci.c` handed it into `ctrl->admin_q`'s queue limits,
and that commit is what the failing ioctl's `blk_rq_map_user_io()` checks
against.

That commit point (`nvme_set_ctrl_limits()`, called with `is_admin = true` for
the admin queue only) *is* in nvme-core, so `max_admin_xfer_ids` overrides it
there — for the admin queue only, never touching `ctrl->max_hw_sectors`
itself, so namespace disk queues are unaffected. The override is still hard
-clamped against `ctrl->max_segments` (the low-level driver's real, fixed-size
scatterlist allocation, `NVME_MAX_SEGS`), so it can never ask the transport to
build more segments than it has memory for — the same safety property the
existing two parameters have.

**What still bounds a real transfer even with this set:** the number of DMA
segments the buffer decomposes into. A userspace buffer backed by scattered
4 KiB pages can still hit the segment cap before the byte-size cap. Back the
destination buffer with a few large physically-contiguous chunks — hugetlbfs
or an aligned `mmap(..., MAP_HUGETLB, ...)` of 2 MiB pages coalesces a 3.2 MiB
buffer into as few as 2 segments — to actually reach it in one command.

### Why suppressing the reset also stops the AEN flood

Stock `nvme_complete_async_event()` deliberately returns *without* requeueing
`async_event_work` for a persistent internal error, because the reset it just
scheduled will resubmit the AER. Suppress the reset and nothing resubmits it —
so the host has no outstanding Asynchronous Event Request and the controller
physically cannot post another AEN. You get one log line, not one every 5s,
and the admin-command window becomes unbounded. This is the in-kernel
equivalent of the "starve the AER" trick that otherwise requires `vfio-pci`,
but it keeps `/dev/nvmeN` and the block device.

---

## Build

`src/` is a copy of `drivers/nvme/host` from **exactly** the kernel it targets,
recorded in `src/VENDORED-FROM`. It currently holds `7.0.2-6-pve`
(Ubuntu-7.0.0-18.18). It will not compile against a different kernel series.

On the target host (needs `build-essential dkms libelf-dev libdw-dev dwarves`
and the matching `proxmox-headers-*`):

```sh
make                       # builds against /lib/modules/$(uname -r)/build
make KDIR=/usr/src/linux-headers-7.0.2-6-pve   # or an explicit tree
./check-crc.sh 7.0.2-6-pve
```

From a non-Linux workstation, the whole thing (fetch headers, compile, ABI
check) runs in a throwaway container:

```sh
./build-test.sh 7.0.2-6-pve
```

### Targeting a different kernel

```sh
./fetch-sources.sh --pve 7.0.2-7-pve     # resolves via git.proxmox.com
./fetch-sources.sh --upstream 6.12.34    # kernel.org stable, for a live ISO
make KDIR=... && ./check-crc.sh ...
```

`fetch-sources.sh` re-vendors `src/` and re-applies
`patches/nvme-noreset.patch`. The patch is 2 small hunks against `core.c` plus
one `#include` and one init call, so it survives version drift well — but
`check-crc.sh` is the arbiter, not hope.

---

## Install (DKMS)

```sh
sudo ./install.sh --yes-replace-nvme-core
```

which is, unrolled:

```sh
sudo cp -a . /usr/src/nvme-noreset-1.0
sudo dkms add    -m nvme-noreset -v 1.0
sudo dkms build  -m nvme-noreset -v 1.0 -k 7.0.2-6-pve
sudo ./check-crc.sh 7.0.2-6-pve /var/lib/dkms/nvme-noreset/1.0/build/src/Module.symvers
sudo dkms install -m nvme-noreset -v 1.0 -k 7.0.2-6-pve --force
```

`dkms install` moves the stock `nvme-core.ko` aside into
`/var/lib/dkms/nvme-noreset/original_module/` — that copy is what `dkms
uninstall` puts back.

`AUTOINSTALL="no"` on purpose: this must never be silently rebuilt for a kernel
whose sources it was not vendored from. After every kernel upgrade you re-run
`fetch-sources.sh` + `install.sh` deliberately, or you leave it uninstalled.

### Arm it

Nothing changes until a parameter is set *and* the module is reloaded.

```sh
echo 'options nvme_core persist_err_noreset_ids=1c58:0023 zero_discard_ids=1c58:0023 max_admin_xfer_ids=1c58:0023' \
  | sudo tee /etc/modprobe.d/nvme-noreset.conf
sudo update-initramfs -u -k all
sudo reboot
```

`nvme-core` is in the initramfs on any NVMe-rooted host, so `update-initramfs`
is mandatory — otherwise the early-boot copy is the stock one and the
parameters are ignored for the devices probed there. A kernel command-line
`nvme_core.persist_err_noreset_ids=1c58:0023` works too and needs no initramfs
rebuild.

If no NVMe is in use at all (diagnostics boot), you can skip the reboot:

```sh
sudo rmmod nvme nvme_core
sudo modprobe nvme_core persist_err_noreset_ids=1c58:0023 zero_discard_ids=1c58:0023 max_admin_xfer_ids=1c58:0023
sudo modprobe nvme
```

---

## Verify it is active

```sh
# 1. the patched module is the one loaded
modinfo -F filename nvme_core          # -> /lib/modules/.../updates/dkms/nvme-core.ko*
modinfo nvme_core | grep -c persist_err_noreset_ids   # -> 1

# 2. the load marker
dmesg | grep 'nvme-noreset: patched nvme-core active'

# 3. the parameters took
cat /sys/module/nvme_core/parameters/persist_err_noreset_ids
cat /sys/module/nvme_core/parameters/zero_discard_ids
cat /sys/module/nvme_core/parameters/max_admin_xfer_ids

# 4. discard really is off for the target device only
lsblk -o NAME,MODEL,DISC-MAX            # 0B on the SN200, unchanged elsewhere

# 5. the admin ceiling took: there is no sysfs file for the admin queue's own
# max_hw_sectors, so the proof is functional -- a single admin-passthru read
# above 128 KiB (data-len up to 4 MiB) that previously failed the ioctl with
# EINVAL now succeeds:
nvme admin-passthru /dev/nvme0 --opcode=0xC6 --data-len=3355443 --read \
  --namespace-id=0 -b > dump.bin   # replace opcode/cdw* with the real VUC

# 6. the reset loop is gone -- one line, not one per 5s
dmesg -w | grep -E 'nvme-noreset|persistent internal error'

# 7. the other drives are untouched
nvme list                               # all seven Intel NS still present
ceph -s                                 # all OSDs up
```

Expected on success: exactly one
`nvme-noreset: persistent internal error, reset suppressed` line, then silence,
and `/sys/class/nvme/nvmeN/state` stays `live` indefinitely.

---

## Rollback

**Fast, no reboot** (only safe if the target NVMe is not carrying live I/O):

```sh
sudo rm -f /etc/modprobe.d/nvme-noreset.conf
sudo rmmod nvme nvme_core && sudo modprobe nvme
```

**Full removal** — puts the stock `nvme-core.ko` back:

```sh
sudo rm -f /etc/modprobe.d/nvme-noreset.conf
sudo dkms uninstall -m nvme-noreset -v 1.0 --all
sudo dkms remove    -m nvme-noreset -v 1.0 --all
sudo rm -rf /usr/src/nvme-noreset-1.0
sudo update-initramfs -u -k all
sudo reboot
```

**If the host will not boot** because of this module (should be impossible —
`check-crc.sh` gates on it — but plan for it anyway):

- At the GRUB prompt append `module_blacklist=nvme_core` … which also means no
  NVMe. Better: pick the *previous kernel* from GRUB's "Advanced options". DKMS
  installed this for one kernel version only, so any other kernel boots stock.
- Or boot any rescue ISO, mount the root fs, and
  `rm /lib/modules/<kver>/updates/dkms/nvme-core.ko*` then
  `chroot ... update-initramfs -u -k <kver>`. Removing the DKMS copy is enough;
  `depmod` falls back to the in-tree module still present under
  `/lib/modules/<kver>/kernel/drivers/nvme/host/`.

Keep a known-good kernel installed. Do not `apt autoremove` the previous
`proxmox-kernel-*` while this is in place.

---

## Files

```
Makefile                  out-of-tree kbuild wrapper
dkms.conf                 DKMS packaging (nvme-core only, AUTOINSTALL=no)
install.sh                staged install with the ABI gate
check-crc.sh              symbol-CRC comparison against the stock kernel
vendored-version.sh       refuses a build against the wrong kernel series
fetch-sources.sh          re-vendor src/ for another kernel + reapply the patch
build-test.sh             containerised compile + ABI check (works from macOS)
patches/nvme-noreset.patch  the core.c delta, for re-application
src/                      vendored drivers/nvme/host + noreset.c/h + Kbuild
src/VENDORED-FROM         provenance of the vendored sources
```
