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
