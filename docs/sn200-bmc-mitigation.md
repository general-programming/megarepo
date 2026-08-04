# The BMC side of the PROC9 fault — who talks NVMe-MI, and can we stop it

Follow-on to `docs/sn200-proc9-fault.md`, which proved the decoded trap was a
null-pointer store inside **`MI_ControlPrimitiveHandler` on PROC9**, the
NVMe-MI / MCTP out-of-band management processor. That document ended with an
open question it deliberately refused to over-answer:

> *"the BMC is the only thing that makes this code run at all, so BMC polling is
> a necessary condition and a legitimate mitigation lever. It is not shown to be
> a sufficient one."*

This document answers the operational half: **who generates that traffic on a
Dell R640 / iDRAC9, whether it can be reduced, and what it would cost.**

Labels: **PROVEN** = read off a live machine or a vendor statement;
**INFERRED** = short chain over proven facts; **SPECULATIVE**.

Everything below was gathered **read-only**. No iDRAC setting was changed, no
host rebooted, no drive touched.

---

## 0. Bottom line

| Question | Answer | Grade |
|---|---|---|
| Does iDRAC9 speak NVMe-MI to these drives? | **Yes.** iDRAC reports `DeviceSidebandProtocol = NVMe-MI1.0` for every NVMe drive in all three R640s, SN200s included | **PROVEN** (live Redfish) |
| Does it reach the SN200 specifically? | **Yes.** iDRAC has read the SN200's model, serial, firmware revision (`KNGND122` / `KNGND112`), capacity and `PredictedMediaLifeLeftPercent` out of band | **PROVEN** |
| Is there a knob to stop it? | **Yes — and a per-device one.** `PCIeVDM.1.FQDDDenyList` / `DenyFQDD` / `PCIIDDenyList`, plus a global `PCIeVDM.1.Enable`. All present, writable, currently at defaults on all three hosts | **PROVEN** (live attribute registry) |
| Does that knob cover the whole MI path? | **Only the PCIe VDM binding.** If any MI traffic also runs over the U.2 I²C sideband, the deny-list does not touch it | **open** |
| Does the fleet show MI-channel failures? | **Yes.** `CTL137` "iDRAC is unable to successfully communicate with the device PCIe SSD in Slot 3 in Bay 1" — **9 occurrences across 3 hosts, every one of them against the SN200, never against any of the 21 Intel P4510s** | **PROVEN** |
| Does that prove MI traffic causes the latch? | **No.** CTL137 also fires on the two drives that have *never* latched. Necessary-condition evidence, not sufficient-condition evidence | **PROVEN (negative)** |
| Should we turn it off? | **Not yet, and not blind.** There is a cheap, decisive experiment first — §6 | — |

**The single most useful new fact:** we now hold a *live, per-device, reversible
switch on the exact subsystem that faulted*, and a *free retrospective log* of
that subsystem failing. Neither existed when `sn200-proc9-fault.md` was written.

---

## 1. Who talks NVMe-MI to these drives — PROVEN

### 1.1 The live evidence

Three PowerEdge R640s, iDRAC9 firmware **7.00.00.171**, BIOS **2.21.2**, each
with **7 × Intel SSDPE2KX020T8 + 1 × HGST HUSMR7676BDP3Y1** on the NVMe
backplane (bays 2 and 4–9 Intel, **bay 3 the SN200**), plus 2 SAS disks on a
PERC H330 Mini.

| iDRAC | Service tag | SN200 firmware | SN200 state today |
|---|---|---|---|
| `172.20.2.188` | `D871SZ2` | `KNGND122` | **latched**; PCIe link disabled, absent from Redfish entirely |
| `172.20.2.186` | `G4VWG13` | `KNGND122` | healthy, `Ready`, 85 % life left |
| `172.20.2.187` | `G4VZG13` | `KNGND112` | healthy, `Ready`, 100 % life left |

From `/redfish/v1/Systems/System.Embedded.1/Storage/CPU.1/Drives/Disk.Bay.3:Enclosure.Internal.0-1`,
`Oem.Dell.DellPCIeSSD` on the two live SN200s:

```
DeviceProtocol         = NVMe 1.2
DeviceSidebandProtocol = NVMe-MI1.0        <-- iDRAC states the OOB protocol
BusProtocol            = PCIE
DriveState             = Ready
AvailableSparePercent  = 255
```

**`DeviceSidebandProtocol = NVMe-MI1.0` is the proof.** iDRAC is not reading a
backplane CPLD for these values — it names NVMe-MI as the protocol and returns
drive-internal data (firmware revision, serial, media life) that only the drive
can supply. The same field reads `NVMe-MI1.0` on the Intel drives too, so this
is the platform-wide mechanism, not something special about the SN200.

> **Aside worth flagging:** `AvailableSparePercent = 255` (`0xFF`) on **both**
> SN200s, versus `99`/`100` on every Intel drive. `0xFF` is the
> not-present/invalid value. The SN200's MI Health Status Poll response is
> **partially malformed or incomplete** where the Intel drives' is not. That is
> a direct, live observation that this drive's MI implementation is weaker than
> its bay-mates'. **PROVEN**; its significance is **SPECULATIVE**.

### 1.2 What Dell says

Dell KB 000190091 states it plainly:

> *"The iDRAC discovers installed NVMe devices over the server i2c bus through
> NVME-MI protocol. While all Dell-branded NVMe storage support NVME-MI, not all
> NVMe drive vendors implement this side-band protocol within device firmware."*

— <https://www.dell.com/support/kbdoc/en-us/000190091/non-dell-nvme-drives-reporting-ctl139-after-updating-to-idrac9-5-00-00-00>

Dell also documents the drive-side management endpoint wedging as a known
failure class, in the Express Flash NVMe PCIe SSD User's Guide, topic *"NVMe
drive properties intermittently not available in iDRAC"*: cause is *"the
sideband controller on the NVMe drive"* failing to initialise, and **the only
documented remedy is a full AC power cycle**. That is Dell's own position that
an NVMe drive's management processor can latch independently of the data path
and that nothing short of removing power clears it — which is exactly the shape
of what `sn200-proc9-fault.md` decoded.

### 1.3 Which binding — VDM or I²C? — INFERRED, not settled

MCTP has two relevant bindings: over SMBus/I²C (DSP0237) and over PCIe VDM
(DSP0238). iDRAC9 has a whole live attribute group for the second one
(§2). Evidence on this platform is mixed:

**For PCIe VDM:**

- `PCIeVDM.1.Enable = Enabled` on all three hosts, with
  `BroadcastEnable = Disabled` — i.e. iDRAC discovers VDM endpoints by unicast
  peer-to-peer, and `NVMeHotplugEnable = Enabled` explicitly extends that to
  NVMe hot-plug discovery.
- **When the PCIe link goes down, iDRAC loses management visibility of the drive
  completely.** On `D871SZ2` right now the SN200 is physically installed and
  powered, and iDRAC does not enumerate `Disk.Bay.3` at all — the Redfish
  resource 404s. If a live I²C sideband existed to that bay, iDRAC should still
  see the drive. **PROVEN observation, INFERRED conclusion.**
- Twice, in two different chassis, `CTL137` (management comms lost) follows
  `UEFI0067` (PCIe link disabled) within ~90 s:
  `D871SZ2` 2026-08-03 17:07:46 → 17:08:59, and `G4VWG13` 2025-08-07 11:19:01 →
  11:19:24.

**For I²C:**

- Dell's own KB text says "over the server i2c bus".
- `Backplane.1.BackplaneBusMode = I2C` is live on this system (though that
  attribute is documented as the *enclosure serial interface* — SGPIO vs I²C —
  which is the backplane SEP, not necessarily the drive MI path).
- Several `CTL137` events occur with **no** nearby `UEFI0067`, i.e. the MI
  channel failed while the PCIe link was up: `G4VZG13` 2025-07-10,
  `G4VWG13` 2025-11-25, `D871SZ2` 2025-08-06 and 2025-08-29.

**Verdict: not settled.** The most likely reading is that both exist and iDRAC
prefers VDM where the device supports it, which is why the VDM attribute group
has a `PCIID`/`FQDD` allow-and-deny machinery at all. **This matters
operationally**, because the deny-list in §2 is explicitly a *VDM* control — if
the SN200's MI traffic is actually arriving over I²C, setting it will change
nothing. §6 is designed to detect exactly that.

### 1.4 Polling cadence — NOT DETERMINED

**Dell publishes no cadence for NVMe sideband polling, and I could not measure
it read-only.** I will not invent a number. What can be said:

- **Discovery bursts** are documented at iDRAC boot, host boot and hot-insert.
  `LCAttributes.CollectSystemInventoryOnRestart = Enabled` on all three hosts,
  so every host restart triggers a full inventory pass.
- **Steady-state health polling** exists (iDRAC keeps `DriveState`,
  `PredictedMediaLifeLeftPercent` and `FailurePredicted` current) but its rate
  is undocumented.
- **Telemetry is not a factor here.** `TelemetryNVMeSMARTData` defaults to
  `EnableTelemetry = 0` with `DevicePollFrequency = 3600` s, and on these hosts
  the Telemetry groups are not even instantiated (`LCAttributes.Licensed = No`;
  only `Telemetry.1.EnableMetricInjection = Disabled` is present). **Nothing to
  turn off.** **PROVEN.**
- **No per-drive temperature sensor is exposed.** `/Chassis/System.Embedded.1/Thermal`
  lists only CPU1, CPU2, inlet and exhaust. So drive temperature is *not*
  visibly feeding fan control on 14G, which narrows the cost of disabling
  per-drive MI. **PROVEN for the exposed sensors; whether iDRAC uses drive temp
  internally is SPECULATIVE.**
- For an order-of-magnitude anchor only: OpenBMC's `nvmemi-over-smbus` design
  polls drives **once per second**, serialised through a mux. That is a
  different BMC stack and says nothing about iDRAC. **Not evidence.**

§6 proposes the measurement that would settle this.

---

## 2. The knobs that exist — PROVEN live

All of the following were read from the **live** iDRAC attribute registry
(`/redfish/v1/Registries/ManagerAttributeRegistry/...`, 3 264 attributes) and
the live attribute values on `D871SZ2`, and confirmed identical on `G4VWG13`
and `G4VZG13`. **Every one is at its factory default today. None is read-only.**

> This contradicts the published-documentation answer, which is that Dell exposes
> no way to suppress or blacklist NVMe out-of-band monitoring. The knob is real,
> it is per-device, and it is undocumented in the public manuals.

### 2.1 The PCIe VDM group — the primary lever

| Attribute | Live value | Registry help text | What setting it would do |
|---|---|---|---|
| `PCIeVDM.1.Enable` | `Enabled` | *"Enable/Disable the PCIe VDM capability."* | **Global off switch** for MCTP-over-VDM. Kills OOB management of *all* NVMe drives |
| `PCIeVDM.1.FQDDDenyList` | *(empty)* | *"iDRAC will not issue P2P PCIe VDM commands to the device in FQDDDenyList even if the device supports PCIe VDM."* | **Per-device suppression.** Max length 4096, comma-separated FQDDs |
| `PCIeVDM.1.DenyFQDD` | *(empty)* | *"FQDD of the device to disable PCIe VDM communication when the device supports PCIe VDM."* | Single-FQDD form of the same |
| `PCIeVDM.1.PCIIDDenyList` | *(empty)* | *"PCIID Deny List for PCIe VDM"* | Deny by PCI vendor/device ID — would cover **every SN200 in the fleet at once** |
| `PCIeVDM.1.PCIIDAllowOnlyList` | *(empty)* | *"PCIIDs in this List are only allowed... rest of all other PCIIDs are denied."* | Whitelist form. Riskier: silently denies anything you forget |
| `PCIeVDM.1.NVMeHotplugEnable` | `Enabled` | *"enforces iDRAC to discover NVMe Hotplug devices over PCIe VDM"* | Turning off stops hot-plug discovery polling. Loses hot-insert detection |
| `PCIeVDM.1.BroadcastEnable` | `Disabled` | broadcast vs unicast (P2P) discovery | Already at the quieter setting. Leave it |

The FQDD to use for the SN200 is **`Disk.Bay.3:Enclosure.Internal.0-1`** — it is
the same on all three chassis, and is the literal `Id` field of the
`DellPCIeSSD` object.

**Reboot required?** The registry entries carry **no** `ResetRequired` /
`RebootRequired` flag and no `@Redfish.SettingsApplyTime` requirement — these
are iDRAC-side attributes, so they should apply live. **INFERRED, not verified.**
Do not rely on it; assume it may need an `iDRAC` reset (not a host reboot) to
take effect, and plan to re-read the value afterwards.

**What you lose per denied drive:** iDRAC health/state for that drive, its
firmware revision and serial in inventory, `PredictedMediaLifeLeftPercent`,
failure prediction, and — importantly — **the `CTL137` events that are currently
our only free observable of the MI channel.** You would be trading the
instrument for the mitigation. See §6 before doing that.

### 2.2 Secondary knobs, live values

| Attribute | Live value | Effect if changed | Cost |
|---|---|---|---|
| `LCAttributes.CollectSystemInventoryOnRestart` | `Enabled` | `Disabled` removes the **full inventory pass on every host restart** — a discovery burst, not steady state | LC inventory goes stale; firmware-update and part-replacement workflows degrade |
| `LCAttributes.PartConfigurationUpdate` | `Apply Always` | `Disabled` removes an OOB **write** path to replaced parts | No auto-restore of settings on part swap |
| `LCAttributes.PartFirmwareUpdate` | `Match firmware of replaced part` | `Disabled` stops automatic firmware push to a replaced drive | Replaced drives keep whatever firmware they arrive with. **Arguably a feature here** given §4 of the runbook |
| `LCAttributes.AutoUpdate` | `Disabled` | already off | — |
| `Telemetry.*` | not instantiated | nothing to do | — |
| `ThermalSettings.ThermalProfile` | `Default Thermal Profile Settings` | fan curve only — **does not reduce polling** | not a lever |
| `iDRAC.OS-BMC.AdminState` | — | **irrelevant.** OS-to-iDRAC pass-through is a USB NIC between host OS and iDRAC. Nothing to do with NVMe-MI | — |
| `Backplane.1.BackplaneBusMode` | `I2C` | enum `0/1/2`, "enclosure serial interface, SGPIO or I2C" | **Do not touch.** Chassis-wide, affects all bays and enclosure management. Not a targeted lever |

### 2.3 Knobs that do NOT exist

- No documented or discoverable **rate limit** on iDRAC's NVMe health polling.
  `DevicePollFrequency` exists only inside the Telemetry report groups, which are
  unlicensed here and disabled by default. There is no "poll drives every N
  seconds" control for the base monitoring loop.
- No way to keep inventory but suppress health polling; it is all-or-nothing per
  device.
- No I²C-side equivalent of the VDM deny-list. If MI is arriving over the U.2
  sideband pins, there is **no software control at all** — the electrical path is
  independent of the host, and disabling the PCIe slot or powering the host off
  will not silence it.

---

## 3. The free observable we did not know we had — PROVEN

Every iDRAC keeps `CTL137` in the Lifecycle Log:

> *"iDRAC is unable to successfully communicate with the device PCIe SSD in Slot
> 3 in Bay 1, because of one or more of the following reasons: device is
> incorrectly seated, iDRAC firmware error, device is in a shutdown state or
> device firmware error."* — Severity `Warning`

**This is an NVMe-MI channel failure event, timestamped, retained for years, and
costing nothing to read.** Full occurrence list across the three chassis, from
the Lifecycle Logs as of 2026-08-04:

| Host | When | Adjacent events |
|---|---|---|
| `G4VZG13` (KNGND112, never latched) | 2025-07-10 02:17:23 | none |
| `G4VWG13` (KNGND122) | 2025-08-07 11:19:24 | **immediately after** a 141-event `PCI1319`/`PCI1318` storm (`"fatal error ... bus 174 device 3 function 0"`) and `UEFI0067` |
| `G4VWG13` | 2025-11-25 02:40:40 | none |
| `D871SZ2` (KNGND122, currently latched) | 2025-08-06 02:46:19 | (drive reflashed `KNGND112`→`KNGND122` the next day) |
| `D871SZ2` | 2025-08-29 19:23:27 | none |
| `D871SZ2` | 2026-08-03 15:52:16 | during this investigation |
| `D871SZ2` | 2026-08-03 17:08:59 | after `UEFI0067` 17:07:46 |
| `D871SZ2` | 2026-08-03 18:21:09 | during this investigation |
| `D871SZ2` | 2026-08-04 02:23:11 | before `UEFI0067` 02:29:50 |

**Two findings, opposite in sign:**

1. **PROVEN, and striking:** all nine `CTL137` events, across three independent
   chassis, are against the **SN200**. Twenty-one Intel P4510s in the same bays,
   same backplanes, same iDRACs, same firmware, have produced **zero**. The
   SN200's management endpoint fails in a way its bay-mates' does not. This is
   independent corroboration — from Dell's telemetry, not from our
   disassembly — that this drive's MI stack is the weak component.

2. **PROVEN, and it cuts the other way:** `CTL137` fires on drives that have
   **never latched**. `G4VZG13`'s SN200 has one `CTL137` and is at 100 % media
   life on the *older* `KNGND112`. So an MI-channel failure is **not sufficient**
   to latch a drive. The strong story — "MI traffic wedges the drive" — is not
   what this data shows.

The honest synthesis: **the MI endpoint on this drive misbehaves routinely and
recovers routinely.** `sn200-proc9-fault.md` decoded one instance where the
misbehaviour was fatal instead. Whether the fatal cases differ in kind, or are
the same defect losing a race, is **unknown**.

---

## 4. The causal chain, stated honestly

### (a) PROVEN

- The decoded trap was in `MI_ControlPrimitiveHandler` on PROC9, an NVMe-MI
  handler. (`sn200-proc9-fault.md`, unchanged.)
- PROC9's MI subsystem only runs in response to inbound NVMe-MI traffic. Its
  entire registered task array is MI handlers.
- iDRAC9 on these R640s **does** speak NVMe-MI 1.0 to these drives, including
  the SN200, and reads drive-internal data out of band.
- iDRAC has repeatedly failed to communicate over that channel with the SN200
  and only the SN200 — nine logged events, three chassis.
- A per-device, reversible control over the PCIe VDM binding exists and is at
  default.

### (b) INFERRED, reasonable

- The BMC is the **only** plausible producer of the MI messages that drove the
  faulting task. Nothing else on this platform speaks NVMe-MI to a drive.
- Therefore reducing or stopping BMC MI traffic to a drive reduces the number of
  times the defective code path executes, and a code path that never executes
  cannot fault.
- Because iDRAC loses all visibility of the drive when the PCIe link drops, the
  active binding is most likely **PCIe VDM**, which means the deny-list should
  actually bite.

### (c) SPECULATIVE / NOT ESTABLISHED

- That MI traffic *volume* correlates with latch incidence. **No evidence
  either way.** §3 shows MI failures on never-latched drives, which is weak
  evidence *against* a simple dose-response relationship.
- That a malformed or unusual MI message is the trigger. `sn200-proc9-fault.md`
  §5 already refuted the attractive version of this: MI input is validated and
  every error path returns through the same enqueue as success.
- That the fault is BMC-provoked at all rather than an internal race in PROC9's
  queue handling between the enqueue side (PROC9) and the off-core consumer.
  `sn200-proc9-fault.md` §4 could not find the consumer; it is not on PROC9. If
  the bug is a cross-core race, MI traffic is just the clock that drives it —
  which still means less traffic is fewer chances, but also means the defect
  cannot be *removed* this way.

### (d) What actually cuts against this mitigation

Two things, and they should be weighed properly:

1. **Rows 8–10 of `sn200-field-evidence.md`.** The drive latched across a
   `ForceOff` → power-on → power-cycle sequence with no DISCARD involved. If MI
   over VDM is the transport, MI traffic requires the host PCIe root complex to
   be powered, so *no MI traffic occurred while the host was off*. That does not
   refute the mechanism — the crash could have been armed earlier while powered —
   but it does mean the latch became visible during a window when this mitigation
   would have been inert.
2. **Row 2 vs row 6 is still the only controlled experiment we own,** and its
   variable was DISCARD, not BMC traffic. Suppressing DISCARD demonstrably
   prevented a latch. Nothing has been demonstrated for the BMC lever.

**So: this is a hypothesis with a newly-available switch, not a fix.**

---

## 5. One more corroborating fact, previously buried

`docs/sn200-firmware-availability.md` records, from two independent sources
(ServeTheHome and Dell's own forum), that **`KNGND122` fixed a hang that appears
when the host runs iDRAC 5.00.yy.z or later**:

> *"those drives work fine on iDRAC 5.y+ if you have firmware v122."*

`sn200-proc9-fault.md` §6 concluded "no errata entry names MI, MCTP, SMBus, VDM
or the management path". That remains true of the *published errata*, but this
field report is a **BMC-version-dependent hang fixed by a drive firmware
revision** — which is, by construction, a management-path interop defect. It is
the closest thing to prior art that the SN200's MI stack has a history of
misbehaving against iDRAC.

It also raises a concrete question worth asking: **these hosts run iDRAC
7.00.00.171**, far past the 5.x that provoked the original hang. Whether the
`KNGND122` fix covers 7.x behaviour is unknown. **SPECULATIVE**, but it is the
single cheapest thing to ask WD/Dell support if that channel is ever opened.

---

## 6. The experiment that would settle it — cheap, non-destructive, reversible

This is the most valuable output of this document. It measures the thing nobody
has measured: **how much NVMe-MI traffic actually reaches an SN200, and whether
the iDRAC knob stops it.**

### The instrument

`sn200-proc9-fault.md` §7 established that PROC9 keeps its own log at **dump
offset `0x36500`**, reachable now that `tools/nvme-noreset/` gained
`max_admin_xfer_ids` to lift the 128 KiB host cap. The relevant strings are
already identified:

| StrId | text | meaning |
|---|---|---|
| **175** | `MI: NVMe-MI: MI_ControlPrimitiveHandler signaled` | **one MI dispatch.** Counting these *is* counting MI traffic |
| **179** | `MI: Control primitive handled: ... returned status %d` | one completed MI command |
| **174** | `...signaled, but cmd list empty` | **the precursor condition.** Dispatch and queue state disagree |

### The procedure

Run against a **healthy** SN200 — `G4VWG13` or `G4VZG13`, **not** the latched
drive on `D871SZ2` — and preferably from a diagnostics boot, per the runbook's
"pace admin traffic" rule.

1. **Baseline.** Read PROC9's log region once. Count StrId 175 occurrences.
2. **Wait a fixed interval** — 30 minutes is plenty if the rate is anywhere near
   1 Hz, and long enough to see something if it is once a minute.
3. **Read again. Diff.** The delta ÷ elapsed time is **iDRAC's real NVMe-MI
   polling rate against this drive** — a number no vendor publishes.
4. **Set `PCIeVDM.1.FQDDDenyList = Disk.Bay.3:Enclosure.Internal.0-1`** on that
   one host. Re-read the value to confirm it stuck.
5. **Repeat steps 1–3.**

### What each outcome means

| Result | Conclusion |
|---|---|
| Rate drops to **zero** | The transport **is** PCIe VDM, the knob **works**, and we hold a real per-drive off switch for the faulting subsystem. Proceed to a long-run A/B |
| Rate drops **partially** | Both bindings are live. VDM deny helps; an I²C residue remains that we cannot control |
| Rate **unchanged** | The traffic is arriving over I²C. **The knob is worthless for this purpose** and the whole BMC-mitigation line is dead. That is a valuable negative — it saves a maintenance window |
| **StrId 174 appears at all**, at any rate | **Near-decisive for `sn200-proc9-fault.md` §4.** The guard at `0x7ffb2a2f` is firing on a *healthy* drive, meaning the queue-state disagreement is a routine occurrence and the latch is that disagreement losing a race. This would be the single most informative observation available |

### Why it is safe

- Read-only admin passthru only, on a **healthy** drive, not a reset-looping one.
  The wedge described in `.claude/skills/nvme-recovery/SKILL.md` was caused by
  50 small commands against a reset-looping controller; this is two large reads
  against a stable one.
- `PCIeVDM.1.FQDDDenyList` is a string attribute with an empty default. Reverting
  is writing `""`.
- Worst case of the deny-list itself is losing iDRAC visibility of one drive.
- **Still set `ceph osd set noout` first** if the target drive carries OSDs, per
  the runbook. And do steps 1–3 before step 4, so that a botched write does not
  cost the baseline.

### The longer, weaker experiment

If §6 shows the knob works, the fleet A/B is: deny VDM to the SN200 on **one**
healthy host, leave the other as control, and wait. The problem is time to
signal — latches here are months apart (2025-07, 2025-08, 2025-11, 2026-08), so
a single-pair A/B over a handful of events will never reach significance. Treat
it as a safety measure with a monitoring cost, not as an experiment.

---

## 7. Recommendation

**Do not change any iDRAC setting yet.** Specifically:

- **Do not** set `PCIeVDM.1.Enable = Disabled`. Global, blunt, and it would
  blind iDRAC to all 8 NVMe drives per host including the 21 healthy Intel
  drives that are the actual capacity.
- **Do not** set the deny-list before running §6 steps 1–3. Doing so destroys
  the baseline *and* removes `CTL137`, our only free observable.
- **Do** consider `LCAttributes.PartFirmwareUpdate = Disabled` independently of
  all this — it is a low-cost way to stop iDRAC pushing firmware at a replaced
  SN200, which the runbook already warns about for other reasons. **Unrelated to
  the PROC9 fault; just good hygiene.**

**Do run §6.** It is a few hours of read-only work, it produces a number nobody
in this investigation has, and every one of its four outcomes is actionable —
including the negative one.

---

## 8. Access notes

Reached read-only via the existing SOCKS5 tunnel to the sea1 management network:

```
ssh -D 8080 root@2605:bb00:c510::210          # already running this session
curl -sk --socks5-hostname 127.0.0.1:8080 -u "$IDRAC_USER:$IDRAC_PASS" \
     https://172.20.2.188/redfish/v1/...
```

Credentials in the session scratchpad `idrac.env` (1Password item
`sea1-ipmi-shared`, mode 600, never committed). iDRACs found on the management
network: `.182`, `.185` (different credentials, not probed), **`.186` `G4VWG13`**,
**`.187` `G4VZG13`**, **`.188` `D871SZ2` (= sea1-hv-2 / sea1-k8s-2)**, `.195`
(HPE DL20, unrelated).

The most useful read-only endpoints, for whoever picks this up:

```
/redfish/v1/Systems/System.Embedded.1/Storage/CPU.1                       # NVMe drive list
/redfish/v1/Systems/System.Embedded.1/Storage/CPU.1/Drives/Disk.Bay.3:Enclosure.Internal.0-1
/redfish/v1/Managers/iDRAC.Embedded.1/Attributes                          # PCIeVDM.* live values
/redfish/v1/Managers/iDRAC.Embedded.1/Oem/Dell/DellAttributes/LifecycleController.Embedded.1
/redfish/v1/Registries/ManagerAttributeRegistry/ManagerAttributeRegistry.v1_0_0.json
/redfish/v1/Managers/iDRAC.Embedded.1/LogServices/Lclog/Entries?$top=50&$skip=N
```

Note the Lifecycle Log ignores `$top` above 50 — page it.
