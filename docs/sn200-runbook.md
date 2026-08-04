# SN200 operator runbook

What to **do**, in order. The analysis lives elsewhere
(`sn200-firmware-flow.md` is the index); this is the operational sheet.

**The single most important fact:** a latched drive's **media and data are
intact**. Only the boot path refuses. Everything that has destroyed data so far
was *our own recovery command*, not the fault. So the default action on a
latched drive holding anything you care about is **stop and do nothing
destructive**.

---

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

Prevention that is **not** established: anything BMC-related. The one decoded
fault was on **PROC9, the NVMe-MI/SMBus management processor**, which the host
does not control — so host-side mitigations may not touch that path at all.

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
├─ NO  → 0xFF cdw12=0x0603, then 0x0503, then a COLD power cycle.
│         Drive returns healthy, namespace ZEROED. Fast and proven.
└─ YES → STOP. Do not send 0x0503. Do not run `nvme wdc get-crash-dump`
          (it fires 0x0503 itself on a successful read).
          A latched drive left powered DOWN preserves every option.
          Your only non-destructive route is the UART/SBL procedure —
          see docs/sn200-data-recovery.md. It has never been run.
```

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
| `0xDD` | Start Secure Purge |
| `0xFF` `CDW12=0x0720`/`0x0820` | unidentified producers; not confirmed read-only |

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
