# SN200 operator runbook

What to **do**, in order. The analysis lives elsewhere
(`sn200-firmware-flow.md` is the index); this is the operational sheet.

**The single most important fact:** a latched drive's **media and data are
intact**. Only the boot path refuses. Everything that has destroyed data so far
was *our own recovery command*, not the fault. So the default action on a
latched drive holding anything you care about is **stop and do nothing
destructive**.

---

## 0. The only recovery route that actually works today

There is **no proven way to get data off a latched SN200.** The NVMe surface is
exhausted (§2), and the UART/SBL route has never been run and needs a pinout
that does not exist publicly. Planning to recover a latched drive is planning on
something nobody has done.

So the recovery story cannot be "get it back". It has to be **"the drive was
never the last copy"** — and that is entirely within our control, needs no
firmware work, and is testable today:

```sh
tools/sn200-fw/sn200-blast-radius.py --node sea1-k8s-0 --node sea1-k8s-1
```

It reads live PVs, groups them by owning workload, and reports what would be
lost. Exit status is non-zero if anything is `CRITICAL`. **Run it before any
maintenance that could power-cycle a host, and after any change to database
topology.**

**As of 2026-08-04, after the migrations below: 0 CRITICAL, 11 HIGH, 1 OK.**
Nothing at sea1 is one drive fault from permanent loss any more. Getting there
took two moves, both to `ceph-rbd-xfs` (which lives on the Intel OSD drives,
entirely off the SN200s):

- `shared-db/shared-timescaledb` — was 1 running instance of a declared 2, on
  an SN200, `backup: null`. Its replica had been `Pending` for 14 h with
  `persistentvolumeclaim not found` after the hv-2 evacuation deleted the PVC.
  Now 2/2, streaming, second copy on Ceph.
- `shared-db/meilisearch` — one replica, no backup, 104 KB of actual index.

Two traps worth knowing before repeating this:

- **Do not raise `size` in the same change as `storageClass`.** CNPG tries to
  expand the *existing* PVC, `local-path` is `allowVolumeExpansion: false`, and
  the error aborts the whole reconcile before any instance is created — the
  cluster reports "waiting for instances" while doing nothing at all. Set
  `storage.resizeInUseVolumes: false` until no local-path PVC remains.
- **Size from measured usage, never from the PVC.** `local-path` is a hostPath
  directory and enforces no quota: timescaledb's `pgdata/base` was **477 G
  against a 256 Gi PVC**. Ceph RBD *does* enforce size, so the declared figure
  would have under-provisioned by ~1.9×.

| Verdict | Meaning |
|---|---|
| `CRITICAL` | one copy, on an SN200. One latch and it is gone permanently |
| `HIGH` | every copy is on an SN200 — a replica count of 2 that is **not** independent redundancy, because both copies share a model, a firmware defect, and a common trigger |
| `OK` | at least one copy lives somewhere that is not an SN200 |

The `HIGH` class is the point of the tool. Eleven CNPG clusters look protected
at two replicas each; all eleven have both replicas on the two SN200s, with no
backup anywhere in the cluster (`kubectl get backups.postgresql.cnpg.io -A` →
none, and the barman ObjectStore CRD is not installed). Replication is the only
thing protecting any of it, and it is replication across two units of the same
defective part.

The two `CRITICAL` items are not hypothetical:

- **`shared-db/shared-timescaledb`, 256 Gi, `sea1-k8s-0`.** Declares 2
  instances, runs **1**. `shared-timescaledb-9` has been `Pending` for 14 h with
  `persistentvolumeclaim "shared-timescaledb-9" not found` — its PVC was deleted
  during the hv-2 evacuation and CNPG has not recreated it. `backup: null`.
- **`shared-db/meilisearch`, 32 Gi, `sea1-k8s-1`.** One replica, no backup.

`authentik-db-5` is `Pending` in the same PVC-not-found way, though authentik
still has 2 live copies so it is `HIGH`, not `CRITICAL`.

**This is the highest-value work available on this whole problem.** Restoring
those two second copies converts the worst case from *permanent loss* to *an
inconvenience*, and unlike every firmware avenue it is ordinary, reversible,
well-understood work.

## 0b. The blast radius is the NODE, not the drive

Observed 2026-08-04 on `sea1-k8s-2`. A latched SN200 does not just cost you the
drive — it can take the whole node down, and the path is not obvious:

```
SN200 latches, stops presenting a block device
  └─ the `data` userVolume selector matches NO disk
     └─ Talos never completes startAllServices  → stage stays `booting`
        └─ container runtime degrades: sandbox creation fails with
           "failed to reserve sandbox name ... is reserved for <id>"
           and DeadlineExceeded
           └─ node NotReady → 7 OSDs down, 31% objects degraded
```

**The volume selector is the load-bearing part.** Talos will not finish booting
with a declared userVolume it cannot satisfy, and it reports `Ready` to
Kubernetes the whole time, so `kubectl` shows nothing wrong. Check
`talosctl get machinestatus` — `stage: booting` with `READY: true` on a node
that has been up for hours is the signature.

A second, independent trap on the same node: **`lldpd` is an extension
*service***, unlike the file-only extensions in the schematic. With no
`ExtensionServiceConfig` it waits forever and blocks `startAllServices` by
itself. Both must be fixed or the node never reaches `stage: running`, which
`talosctl upgrade` gates on.

### The reboot-recovery cascade (bit us hard, will recur)

Rebooting a node carrying ~74 pods produced a **CNI IPAM lock convoy** that no
amount of waiting cleared:

```
host-local blocked in flock() on /var/lib/cni/networks/cbr0/lock
  └─ bridge waits on its child (do_wait)
     └─ multus never returns → containerd kills multus-shim on timeout
        └─ "netplugin failed ... signal: killed" → CNI teardown fails
           └─ sandbox name stays reserved → replacement pod cannot start
```

Self-sustaining, because `cni0` only receives `10.244.1.1/24` when a bridge ADD
*succeeds* — so `cni0` sat `DOWN` with no IPv4 and every ADD queued forever.
`/var/lib/cni` lives on EPHEMERAL, so **198 stale IPAM leases survived the
reboot** and the herd of restarting pods all contended on one lock.

Diagnosis that actually works — `/proc/locks` names the holder outright:

```sh
# from a hostNetwork pod (these still start: they bypass CNI entirely)
ino=$(stat -c %i /var/lib/cni/networks/cbr0/lock); grep "$ino" /proc/locks
ip -4 addr show cni0          # no IPv4 => no ADD has ever succeeded
ps -eo etime,args | grep /opt/cni/bin/   # plugins older than ~1 min are stuck
```

Recovery, in this order:

1. `kubectl cordon` the node, then delete its reschedulable pods so the herd stops
2. clear stale leases — **only** once `kubectl` shows zero Running pod-network
   pods on that node: `rm /var/lib/cni/networks/cbr0/10.244.* .../fd40:* .../last_reserved_ip.*`
3. `ip link del cni0` to drop the half-configured bridge
4. **reboot the node** — killing the stuck plugin one at a time only hands the
   flock to the next victim, and a fresh `host-local` wedges the same way

Do not bother restarting multus or flannel: both are downstream symptoms.
`"Waiting for all goroutines to exit"` is flannel's **normal** steady-state log
line, not a shutdown — do not chase it.

## 0a. Where the drives are, and what is protecting them

Three SN200s, all at sea1. **They do not appear on the hypervisors** — hv-0 and
hv-1 pass theirs straight through to the k8s guests, so `/sys/class/nvme` on
the HV shows only the seven Intel OSD drives and looks like a clean negative.
Look inside the guest.

| Node | dev | firmware | `discard_max_bytes` | state |
|---|---|---|---|---|
| `sea1-k8s-0` | nvme0 | `KNGND122` | 2199023255040 | live, in service |
| `sea1-k8s-1` | nvme0 | **`KNGND112`** | 2199023255040 | live, in service |
| `sea1-k8s-2` | nvme7 | `KNGND122` | — | **latched**, `state=resetting` |

**The DISCARD suppression protects only the broken one.** The udev rule lives in
`sea1-k8s-2`'s *node* patch (`infrastructure/talos/sea1/talconfig.yaml`), not in
a shared one, so the two drives still holding data are the two with DISCARD
fully enabled at 2 TiB — and a whole-device TRIM is the one trigger demonstrated
in the field. `sea1-k8s-1` also still carries `KNGND112`, which has the
PFAIL-monitor defect `KNGND122` closed.

**This is a known, accepted exposure, not an oversight to fix on sight.** Both
drives are in active production use. The owner's decision (2026-08-03) is to
leave them untouched: a protective sysfs write is cheap, but any action against
a working SN200 is itself a risk, and firmware activation is worse — `--action=2`
writes marker 3 gated on **the target image's own flags bit**, so whether it
wipes is a property of the image in that slot, not of the action chosen.

Close the gap when the data on these two is replicated or evacuated — not before.

## 0. Prevention — ranked by how much they actually buy

| Do this | Why | Confidence |
|---|---|---|
| **Never put single-copy data on an SN200** | The drive can latch at any time and the only fast recovery wipes it. Ceph/RAID replication turns a latch into an inconvenience | certain |
| **Suppress DISCARD** (`machine.udev.rules` → `discard_max_bytes=0`, or `mkfs -K` / `nodiscard` / LVM `issue_discards=0`) | A whole-device TRIM demonstrably latched a drive in the field, and WD's failing test profiles are deallocate-heavy. Removes one provocation | high |
| **Orderly OS shutdown via UPS, not a delayed power cut** | Cuts the dominant exposure. **But not a guarantee** — a normal `CC.SHN` can hang in the GC wait (§3), and `CSTS.SHST` may not even wait for the System Area save | moderate |
| **Keep firmware at `KNGND122` and fill every writable slot** | It closed the PFAIL-monitor defect. `KNGND100`/`112` have more open. Filling slots stops an accidental activation landing on an older image | high |
| **Do not hammer a latched drive with admin commands** | A 50-chunk read wedged a Talos node: kernel alive, `apid`/`kubelet` dead, 7 OSDs down, ceph 33% degraded | learned the hard way |
| **Suppress iDRAC's NVMe-MI traffic to the SN200** — `PCIeVDM.1.FQDDDenyList = Disk.Bay.3:Enclosure.Internal.0-1` | The one decoded fault was in an NVMe-MI handler on PROC9, and iDRAC **is** the thing driving it (`DeviceSidebandProtocol = NVMe-MI1.0`, PROVEN live). The knob is real, per-device and reversible | **speculative — do not do this yet.** MI-channel failures (`CTL137`) also occur on drives that never latched, so the causal link is unproven. **Measure first**: `docs/sn200-bmc-mitigation.md` §6 |

On the BMC lever, in one paragraph: `docs/sn200-bmc-mitigation.md` establishes
that iDRAC9 speaks NVMe-MI 1.0 to these drives, that **all nine logged
`CTL137` management-comms failures across three R640s are against the SN200 and
none against 21 Intel P4510s in the same bays**, and that a per-device off
switch exists and is at default. It also establishes that `CTL137` fires on
never-latched drives, so the switch is **not** shown to prevent anything. Run
the §6 measurement before spending a maintenance window on it — one of its four
outcomes kills the idea outright, cheaply.

---

## 1. Triage — read-only, safe on a latched drive

```sh
# resolve by MODEL, never by nvmeN -- numbering shifts between OSes
for n in /sys/class/nvme/nvme*; do
  grep -q HUSMR "$n/model" 2>/dev/null && echo "${n##*/}"
done

D=/dev/nvmeN
nvme id-ctrl $D | grep -E '^(mn|sn|fr|frmw|tnvmcap|unvmcap)'
nvme fw-log  $D                                   # slot contents + active slot
nvme admin-passthru $D --opcode=0xff -n 0 --cdw10=0 --cdw12=0x0004 --data-len=0
```

> **☠ Check that `0x0004` before you press enter.** `0xFF`/`0x0003` — one
> nibble away — erases EEPROM System-Area section 6, the boot-marker record,
> and an empty System Area is itself a latch predicate. This is the only
> dangerous typo on a *healthy* drive, and this is the command you type on
> every drive.

Reading the result:

- **`tnvmcap` > 0 and `unvmcap` == 0** → capacity is still allocated to a
  namespace. **The data is there.** Do not format.
- Probe returns byte[1] == **6** → latched (`INVALID` startup, i.e. Post Crash).
- **Absent from `lspci` entirely** → that is *not* this bug. It is a PCIe
  link-training failure (`UEFI0067`) — a cabling/riser problem. No VUC work will
  fix it.

### Which section is armed, and what armed it

```sh
nvme admin-passthru $D --opcode=0xc6 -n 0 --cdw10=2 --cdw12=0x0320 --data-len=8 -r -b | od -A d -t x4  # CLOG
nvme admin-passthru $D --opcode=0xc6 -n 0 --cdw10=2 --cdw12=0x0520 --data-len=8 -r -b | od -A d -t x4  # PFCL
```

Then pull the first 128 KiB of the crash section and read its header — this
tells you **which failure mode you actually have**:

```sh
nvme admin-passthru $D --opcode=0xc6 -n 0 --cdw10=32768 --cdw12=0x0420 \
     --data-len=131072 -r -b > crash.bin
od -A d -t x1 -N 72 crash.bin
```

| header at `+0x00` / `+0x40` | meaning |
|---|---|
| version `0x00020100`, `"UNEXSTRT"` at `+0x40` | **unfinished shutdown** stamped the section |
| version `0x00020200`, `+0x40` zero | **a genuine firmware trap** — a different failure, and the one actually observed on `nvme7` |

Decode it with `tools/sn200-fw/decode-crash-dump.py`. **Copy `crash.bin` off the
machine before doing anything else** — every recovery below destroys it.

---

## 2. Recovery — the decision, stated honestly

```
Is there data on this drive you want?
├─ NO  → 0xFF cdw12=0x0503, then a COLD power cycle.
│         Drive returns healthy, namespace ZEROED. Fast and proven.
├─ YES, and the §1 probes say PFCL armed / CLOG NOT armed
│      → 0xFF cdw12=0x0603, then a COLD power cycle. NEVER 0x0503.
│         Mechanism proven, never yet tested on hardware. See below.
└─ YES, in every other case → STOP. Do not send 0x0503.
          Do not run `nvme wdc get-crash-dump` (it fires 0x0503 itself
          on a successful read).
          A latched drive left powered DOWN preserves every option.
          Your only non-destructive route is the UART/SBL procedure —
          see docs/sn200-data-recovery.md. It has never been run.
```

**`0x0603` was never part of the wipe, and `0x0503` never needed it.** The old
"send `0x0603` then `0x0503`" sequence has been traced to the instruction:
`0x0603` erases the PFail dump section and returns — no startup-type test, no
second request, no boot marker. `0x0503` erases the Crash Dump section and then
its *resume* handler tests `*(0x7ff87c64) == 6` and posts the re-init verb.
**`0x0503` alone is the entire data cost; `0x0603` alone can never cost data.**
PROVEN — `sn200-oam-dispatch.md` §4.2. Dropping `0x0603` from the destructive
path changes nothing; it is removed above only because it was never doing
anything.

That is also what makes the middle branch worth trying. A latch armed *only* by
PFCL is released by clearing PFCL, and `0x0603` does exactly that and nothing
else. It is rare — `UNEXSTRT` stamps **CLOG**, so every ordinary power-event
latch and every reset-loop iteration arms the section `0x0603` cannot touch —
so run the `0x0320`/`0x0520` probes in §1 first and only take this branch if
CLOG comes back **not armed** (SC `0xC3`) and PFCL comes back armed.

**There is no NVMe-surface way to read the data off a latched drive.** This was
chased to the end, not assumed. `Admin_VucFlashRead` (`0xCA`/`CDW12=0x0001`)
does exactly what is wanted — real user data through the L2P — and is **not in
the post-crash allow-list**, along with `Admin_VucFlashLogicalToPhysical`
(`0xCA`/`0x0000`). Those two are the only `0xCA` sub-values below `0x02` and the
only two that understand LBAs; everything the allow-list admits works in
*physical* addresses. What is left is `0xCA`/`0x03` raw page read at **640 bytes
per command** — ~1.2×10¹⁰ commands for 7.68 TB, on a controller resetting every
~5 s, with no L2P to reassemble any of it. See `sn200-vuc-flash-read.md`.

So "power it down and leave it" is not caution for its own sake — it is the
only option that keeps the UART route alive.

**Firmware activation is not a gentler alternative.** `--action=2` writes marker
3 gated on **bit 0 of the target image's own flags word**, so whether it wipes
is a property of the image in that slot, not of the action you chose. Same data
cost; its only real advantage is using no vendor opcodes.

### ☠ Never send these

| Command | Effect |
|---|---|
| `0xCA` `CDW12[7:0]=0x0F` | NAND block erase — `CDW12[15:8]` is *ignored*, no harmless sub-value |
| `0xCA` `CDW12=0x0010`/`0x0110` | raw page write |
| `0xFF` `CDW12=0x0403` | Drive Uninit — **no startup-type gate at all**, sets FACTORY re-init |
| `0xFF` `CDW12=0x0303` | Erase to SBL EEPROM — permanent brick |
| `0xFF` `CDW12=0x0003` | Erase System Area 0 — the boot-marker record. **One nibble from the `0x0004` probe.** |
| `0xDD` | Start Secure Purge |
| `0xC6` `CDW12=0x0720`/`0x0820` | unidentified producers; not confirmed read-only. **These are `0xC6`, not `0xFF`** — under `0xFF` they are simply invalid command ids and do nothing. |

A vendor command is only reliably inert with **CDW10, CDW11, CDW12 *and*
CDW13 all zero**. Note the spacing: `0x0F` erase is two values from `0x10`
write, and `0x0403` is one nibble from the `0x0503` used in recovery.

---

## 3. Why "shut down cleanly" is not a guarantee

Three things undercut the obvious mitigation, all proven in code:

1. **A normal `CC.SHN` can hang outright.** It sets internal mode 4, but PROC11's
   GC waits on three counters whose only release is mode **5** — no timeout, no
   bail-out — while its own producers, also gated on mode 5, keep incrementing
   them. Mode 5 is written only from outside GC. A normal shutdown with no PFail
   has **no escape**.
2. **`CSTS.SHST` may not mean what you think.** The marker-5 submit jumps
   straight into `"Returning shutdown completion"`, with the System Area save
   reached only afterwards (INFERRED). Waiting for the handshake may not wait for
   the save.
3. **The 25 ms PFAIL timer enforces nothing.** At expiry it writes a breadcrumb
   and exits; nothing reads the deadline. The real limit is **hold-up energy**,
   which is nowhere in the firmware and has never been measured on these drives.

Best-effort order when you must power something down:
**stop deallocates/TRIM → `sync` + unmount → `CC.SHN` → wait → then cut power.**

---

## 4. Tooling

| Tool | Use |
|---|---|
| `tools/sn200-fw/check-latch-state.sh` | read-only triage |
| `tools/sn200-fw/pull-crash-dump.sh` | dump retrieval; **cannot** emit `0xFF`, enforced by test |
| `tools/sn200-fw/decode-crash-dump.py` | decode records against the string table |
| `tools/sn200-fw/fill-fw-slots.sh` | fill writable slots with `KNGND122`; `CA=0` only, never slot 0/1 |
| `tools/sn200-fw/nvme-debug-pod.yaml` | privileged pod — full NVMe admin access from Talos, which has no shell |
| `tools/nvme-noreset/` | patched `nvme-core`: suppress the reset loop, force `discard_max_bytes=0`, and raise the admin transfer cap. **Diagnostics boot only** |

`nvme wdc get-crash-dump` and `dm-cli` **destroy the drive** — both fire
`0xFF/0x0503` automatically on a successful read. Use the scripts here instead.
