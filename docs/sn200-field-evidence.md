# SN200 (nvme7 / sea1-hv-2) — field evidence log

Observed behaviour of one drive, `HUSMR7676BDP3Y1` s/n `SDM00003094B`, firmware
`KNGND122`, at `0000:b2:00.0` behind downstream port `0000:ae:03.0` in a Dell
R640. U.2 cable for this bay is known-flaky (owner).

This file is **observation only** — no firmware analysis, no inference beyond
what the events force. Any root-cause model must explain every row here.
Analysis lives in `sn200-firmware-re.md` and `sn200-independent-re.md`.

## Event log — 2026-08-03

| # | Event | Discard involved? | Clean shutdown? | Outcome |
|---|---|---|---|---|
| 0 | Found latched (pre-existing) | unknown | unknown | Post Crash Startup |
| 1 | Clears + cold cycle | — | — | **Recovered**, namespace ZEROED |
| 2 | Talos creates `u-data`: partition + `mkfs.xfs` | **YES** (mkfs default) | n/a | **LATCHED** |
| 3 | FLR, SBR/PERST#, NSSR, 60s link-disable, remove/rescan | — | no | no effect |
| 4 | Clears + graceful warm reboot ×2 | — | yes | got WORSE: bus-present → absent |
| 5 | Cold power cycle, 126 s off | — | — | **Recovered**: live, full 7.68 TB, `nuse==nsze`, 0 media errors, empty ns |
| 6 | `discard_max_bytes=0`, then partition + `mkfs.xfs` + 512 MiB write + remount + md5 | **NO** (suppressed) | n/a | **SURVIVED — zero resets**, mkfs 1 s |
| 7 | Boot Talos, `data` volume enabled | probably none (adopted existing fs) | n/a | node Ready, udevd OK, kubelet OK |
| 8 | `ForceOff` of the RUNNING host | **NO** | **NO** | — |
| 9 | Power on | — | — | `UEFI0067` PCIe link training failure, link **disabled**, POST halt at F1 |
| 10 | Power cycle | — | — | **LATCHED**: `resetting`, no namespace, GPT+XFS gone |

## What the log forces

**Row 2 vs row 6 is a controlled experiment.** Same drive, same host, same
partition + `mkfs.xfs` + I/O sequence. The only variable changed was
`queue/discard_max_bytes` (2199023255040 → 0). Row 2 latched the drive; row 6
completed with **zero controller resets** and mkfs finishing in 1 s instead of
wedging. So:

- A large DISCARD is **sufficient** to latch this drive.
- Suppressing DISCARD **prevented** that specific trigger.

**Rows 8–10 refute "DISCARD is the only trigger".** No mkfs, no fstrim, no
deallocate of any kind occurred between the healthy state at row 7 and the
latched state at row 10. Therefore at least one further, independent trigger
exists. Candidates, not yet distinguished:

- unclean start (no recorded clean shutdown) re-arming the crash section
- abrupt power removal arming a pfail section by a separate path
- the PCIe link training failure at row 9 being itself the arming event

**Row 9 is a distinct fault class.** `UEFI0067: A PCIe link training failure is
observed in Bus:174 Dev:3 F:0 and the link is disabled` (bus 174 decimal =
0xAE). BIOS disables the port, so the drive cannot enumerate regardless of its
internal state. iDRAC SEL logs a matching "fatal error ... bus 174 device 3
function 0". **A drive absent from `lspci` is this, not the firmware lockup** —
and it means "the drive came back after a power cycle" is ambiguous evidence,
because the cable may simply have trained that time.

**Row 4 is a caution.** Two graceful warm reboots after firing the clears did
not recover the drive and coincided with it leaving the PCIe bus entirely.
Whether the reboots caused that or the cable did is **not established**.

## THE CABLE IS NOT THE ROOT CAUSE — fleet-wide incidence

**Owner reports this lockup has occurred on EVERY host they own that uses an
SN200.** Multiple hosts, multiple cables, multiple chassis, multiple bays.

That refutes the marginal-U.2-cable explanation as root cause. A per-host
physical fault cannot produce a fleet-wide pattern; the only common factor is
the drive model. The cable on sea1-hv-2 is real (UEFI0067 link-training
failures are logged) but is at most an **aggravator on that one host**, and it
made the evidence there ambiguous.

The traced mechanism is unaffected — unfinished shutdown → markers 5/6/7 →
`UNEXSTRT` stub → `Detected a CRASH or PFCRASH section` forces post-crash on
every boot. What changes is *what makes the shutdown fail to finish*: it must be
intrinsic to the SN200.

### Leading hypothesis: VCAP / hold-up capacitor degradation

`VCAP has failed, drive is in write protect mode` (StrId 662) exists as a
distinct firmware posture. A batch of same-age SN200s whose power-loss-
protection capacitors have aged out would mean every power event starts a PFAIL
save that cannot complete inside a shrinking hold-up budget → marker 6/7 →
latch. This explains:

- fleet-wide incidence across unrelated hosts
- correlation with power events
- why peak-current workloads (whole-device deallocate) make it worse — a weak
  cap sags fastest under load
- why it needs no cable fault and no per-host cause

**Decisive measurement, not yet taken:** read VCAP/hold-up capacitor health on
several SN200s. If degraded fleet-wide, these drives are at end of life and no
firmware remedy exists — `KNGND122` (2020) is the newest image that exists.

Competing explanation still open: a genuine firmware defect where the shutdown
save does not complete under some condition common to all these hosts.

## Marker 0x06 — my reframing was WRONG, "diagnostic mode" was right

**Retracted.** I proposed that byte[1]==6 meant "PFAIL Shutdown STARTED" rather
than diagnostic mode. Independent RE refuted it with four proofs:

- `7ffaac95: extui a10,a5,0,3` masks the startup type to **3 bits** (7 reachable
  values). The marker enum has **11**. They are different enums.
- The marker table at PROC0 `0x7ff81180` is a u16 StrId array indexed
  separately — its printed order *is* correct, but it is not what this probe
  returns.
- WD's `gf_nvme_sys_init_done_real` @ `0x8b0b0` is exactly this probe, and
  `gf_is_diagnostic_mode` @ `0x42c90` tests `== 6` → `HDMS_DEV_DIAGNOSTIC_MODE`.
- The `== 7` variant is gated on **Firmware Revision**, not model:
  `FR[0]=='H' && FR[3]>'E'`. `KNGND122` starts with 'K', so `== 6` applies.

Kept as a worked example of why string-table adjacency must not be trusted:
the enum order was right, the *identification* of which enum was wrong.

**The hardware half of the hypothesis survived and was strengthened** — see
below.

### The original (refuted) reasoning, retained for context

`StringTable.csv` (KNGND122) holds these eleven strings **contiguously**, the
shape of a `const char *markerNames[]`:

```
0  No previous marker found        6  PFAIL Shutdown STARTED
1  CLEAN shutdown                  7  PFAIL Shutdown TIMEOUT
2  PFAIL shutdown                  8  READONLY Startup requested
3  Drive REINIT requested          9  POST CRASH Startup
4  FACTORY drive REINIT requested  10 Invalid marker
5  Normal Shutdown STARTED
```

Zero-indexed, **6 = "PFAIL Shutdown STARTED"** — a power-fail shutdown that
began and never completed. Corroborating: WD's `libdmi_core.so` gates its
crash-dump "retrieved" flag on `startup_type == expected` with `expected == 7`
for `HUSMR…`, and 7 would be `PFAIL Shutdown TIMEOUT` — the adjacent PFAIL
state. That is an economical explanation for the silent-gate bug.

**INFERRED, not proven** — derived from string-table order, which in this
firmware is known to follow source-file appearance rather than case value. Both
RE agents are locating the enum in code to confirm or refute.

### Why this would reframe everything

Supporting strings, all verbatim in KNGND122:

- `A de-allocate command is broken during PFail from LBA %x to %x` — deallocate
  and PFail interacting directly, matching OM-6588's "large deallocate **and a
  pfail**"
- `PCIe_SendResetRequest LINKDOWN Reset Detected port %d`, `PerstLinkDown set`,
  `BottomHalfAttentionHandler: Link down detected`, counters `LinkDownCnt`/`PerstCnt`
- `SYS: PFAIL is detected`, `PFAIL interrupt enabled`, `Enable PFAIL monitoring`
- VMON rail monitoring including `PC12V` and `ATX12V`, plus power-excursion warnings
- `SPI Crash Section is in an invalid state` / `SPI PFail Crash Sections …`

**A U.2 cable carries 12 V as well as the PCIe lanes**, and this bay's cable is
known-flaky. Marginal power delivery would produce real rail droop, which VMON
reports as a genuine PFAIL; the drive starts an emergency shutdown, the glitch
clears before it finishes, and the marker is left at "PFAIL Shutdown STARTED".

This explains what the deallocate-only theory cannot:

| Observation | Under the power model |
|---|---|
| row 2 — mkfs latched it | whole-device deallocate = massive parallel NAND erase = peak current = droop. Trigger is POWER, not TRIM semantics |
| row 6 — no-discard run survived | far lower peak draw |
| rows 8-10 — ForceOff latched it, no deallocate at all | a real power event |
| recovery only via cold cycle | full rail reset clears the marker |
| `percentage_used` 16%, 0 media errors | NAND healthy — this is power/interconnect, not wear |

If confirmed, **the firmware is behaving correctly and reporting a real power
fault**, and this is a cable/hardware problem rather than a firmware bug. That
flips the remedy from "mitigate in software" to "replace the cable".

## Open questions for the firmware analysis

1. Does an unclean start alone latch the drive, or only when combined with a
   dirty L2P / in-flight journal?
2. Is a PCIe link drop during operation handled as a power-fail event? If so
   the flaky cable can cook this drive with zero host involvement, and no
   host-side mitigation can ever make it stable.
3. Row 10: was the namespace merely suppressed, or was the media re-wiped
   again? The GPT and XFS from row 6 are gone — but "gone" could be either.
4. Is the row-6 mitigation (`discard_max_bytes=0`) worth keeping given it
   demonstrably does not cover the row-8/10 path?

## Recovery cost, for the keep-or-bin decision

Every recovery so far has required a **cold power cycle** and has returned the
drive with a **zeroed namespace** — `0x0503` schedules a re-init that rebuilds
the L2P. There is currently no known non-destructive recovery. `percentage_used`
is 16%, media errors 0, so the NAND itself is healthy; the problem is entirely
firmware state plus, apparently, a marginal physical link.

## Recovery via firmware activation — proven 2026-08-04

Latched drive recovered with **standard NVMe commands only**, no VUC:

| Step | Result |
|---|---|
| latch #2 (abrupt ForceOff of running host) | `state=resetting`, no namespace |
| **bare cold power cycle** | **still latched** — this is the control |
| `nvme fw-commit --slot=5 --action=2` (activate existing image) | `Success committing firmware action:2 slot:5` |
| drive during activation | `controller capabilities changed` → `CSTS=0x0` → `state=dead` |
| cold power cycle | **RECOVERED**: `state=live`, namespace present, **0 resets**, `afi 0x44 → 0x55` |

`afi` moving 0x44 → 0x55 confirms slot 5 actually activated. Health after:
`fr KNGND122`, `tnvmcap` full, `unvmcap 0`, 0 media errors, 0 critical warnings.

**The bare cold cycle failing first is what makes this a finding** rather than a
coincidence — the activation is doing the work, not the power cycle.

**Still destructive.** Media zeroed at every offset sampled 1 MiB → 1 TiB, and
the GPT+XFS written during the earlier no-discard test were gone. So firmware
activation appears to trigger the same re-init that `0x0503` schedules.

**Why prefer it over `0x0503` anyway:** identical data outcome, but it uses only
spec-defined commands — no vendor opcodes, no exposure to `Erase to SBL EEPROM`
(permanent brick) or `Drive Uninit` sitting one digit away, and no dependence on
the Post-Crash allow-list. Slot 1 being read-only guarantees a bootable image
survives regardless.

### Slot layout found on this drive

```
afi 0x44 (slot 4 active)   frs1 KNGND112 [READ-ONLY]   frs4 KNGND122
                           frs2 KNGND112               frs5 KNGND122
                           frs3 KNGND112
```

Three of five slots held `KNGND112` — an **undocumented** revision with no
release notes and no binary in the firmware zip. Worth checking across the fleet
and upgrading slots 2/3, since any future activation of those lands on it.

## Reaching the drive from Kubernetes (no Proxmox needed)

Talos has no shell, but a privileged pod on the node gives **full NVMe admin
access**, including vendor passthru. Verified 2026-08-04 on `sea1-k8s-2`:
`id-ctrl`, `fw-log` and `admin-passthru --opcode=0xff` all succeed.

```sh
kubectl apply -f tools/sn200-fw/nvme-debug-pod.yaml
kubectl -n kube-system exec nvme-debug -- nvme fw-log /dev/nvme6
kubectl -n kube-system delete pod nvme-debug     # it is disposable
```

**The device number is not stable across OSes.** The same SN200 is `nvme7`
under Proxmox and `nvme6` under Talos. Always resolve by model:

```sh
for n in /sys/class/nvme/nvme*; do
  grep -q HUSMR "$n/model" 2>/dev/null && echo "${n##*/}"
done
```

**Do not use the shared `workpod` DaemonSet for this.** It lacks
`CAP_SYS_ADMIN` (`CapEff 0xa80425fb` — the stock container set), so passthru
fails; and its `/dev` hostPath is **live drift** — `base/daemonset.yaml`
declares no volumes at all, so an ArgoCD resync would remove it. It also runs
on every node including both control planes, which is the wrong place to add
privilege for a one-off job.

**A latched drive does not harm the node** as long as no volume targets it.
Observed here: 21 controller resets logged against `nvme6` while `udevd` and
`kubelet` both stayed `OK`. It only wedged udevd previously because Talos was
trying to partition and format it.
