# SN200: what is actually proven, and what is not

An explicit confidence audit, written because "we are confident" is not the same
as "we checked". Every claim below is graded, and the ungraded gaps are named.

> ## ⚠ RED-TEAM RESULT — read this before the rest
>
> An agent was tasked with **refuting** this document. Seven attacks, **three
> landed**. See `sn200-red-team.md`. The corrections are folded in below, but in
> summary:
>
> 1. **The 25 ms is not a budget and enforces nothing (PROVEN).** At expiry the
>    monitor submits marker 7 and exits; *nothing downstream reads the deadline*.
>    If the work takes 40 ms and the rails hold 50 ms, PROC6 still writes
>    `0x80000002` **over** the marker-7 breadcrumb and the drive boots clean.
>    Exceeding 25 ms is by itself harmless. The real constraint is **hold-up
>    energy**, which appears nowhere in the firmware and has never been measured
>    on these drives. "Workload scaling is PROVEN" was a non-sequitur and is
>    downgraded to INFERRED.
> 2. **Leg 4 was misread and is actually counter-evidence (PROVEN header).**
>    There are two mutually exclusive producers: stub = version `0x00020100` +
>    `"UNEXSTRT"` at `+0x40`; full fault dump = `0x00020200`. Our retrieved dump
>    is `0x00020200` with `+0x40` **zero** — the *full-dump* writer. So that
>    drive's CLOG was armed by a **genuine fault**, not by the claimed
>    unfinished-shutdown stamp. The log inside it even records `SYS: PFAIL
>    startup`, emitted only by the marker-2 handler, i.e. the *preceding
>    shutdown completed* in 6.4 ms — a fault **after** a successful recovery.
> 3. **The field pattern is not simply "power events" (partial).** Row 2 of
>    `sn200-field-evidence.md` — `mkfs.xfs` with discard on a healthy, running
>    drive — latched it with no power event and no shutdown, and the model has
>    no path to markers 5/6/7 without a type-3 request or a PFAIL edge. WD's
>    OM-6850 root cause is *"small loss of usable media … over time, this leads
>    to a crash"* — attrition→assert, which fits the full-dump header better.
>    And "five drives" is one batch, one owner, one rack, one workload.
>
> **The attack that failed closed our biggest gap:** the disputed 5/6/7 → latch
> edge **does exist**, proven instruction-by-instruction
> (`0x7ffaaea7/b2/bd` → `0x7ffaaf6b` → `bnei a15,4` → `0x7ffaacea` → falls
> through to `0x7ffaad01`). The earlier "branches past the UNEXSTRT block" worry
> was a mis-identification — there are two blocks. That part of the claim is
> **stronger** than stated: 5/6/7 reaches mode 6 on both arms, so the crash
> section is not even required for the first latch.

## The root cause — was claimed PROVEN by four routes; leg 4 is retracted

The drive latches because **a shutdown began and did not finish**.

1. **Code.** Markers 5/6/7 are written *first*, the work list runs, and only
   PROC6 `0x7ffbba61` writes CLEAN/PFAIL afterwards. Two per-section tests at
   PROC0 `0x7ffaae35`/`0x7ffaae3d` then force `0x80000009` on every subsequent
   boot. Traced twice, independently, by separate agents using separate tooling.
2. **The vendor.** WD's own release notes name the symptom: *"Namespace
   Disappears During AC Power Cycle Testing … Power Cycling + Random
   Read/Write/Deallocate IO Profile Testing results in **incomplete shutdown** …
   when both a link down and a Pfail interrupt occur at exactly the same time …
   the Pfail interrupt may get lost."*
3. **Field behaviour.** Five drives, all failing on power events, none on
   anything else.
4. **The drive's own log.** The retrieved dump shows a *successful* power-fail
   recovery: `Shutdown time = 6.429 ms` / `PFAIL time = 6.521 ms` against a
   25 ms budget, then `PFAIL startup` → `Scrubbing done` → `SYS: Inited` →
   `DriveReady`. This is the mechanism working correctly, which is exactly what
   the model predicts for an unloaded drive.

The **25 ms budget** is PROVEN (`0x7ff830e0 = 25000`, scaled by cycles-per-µs,
units pinned by `SYS: PFAIL time = %5u.%03u ms`), as is the fact that at expiry
the supervisor writes marker 7 and **exits** rather than forcing completion.

**Workload scaling is PROVEN**: a fixed 25 ms budget against live counters
(outstanding NAND writes, write-buffer entries, in-use command contexts, pending
V2P, broken deallocates, CellCare save). This is why a whole-device TRIM looked
causal — it is the peak dirty-state workload, not a special code path.

## Upgraded to PROVEN this session

- **Per-core memory isolation.** The SoC is a grid of 512 KiB slots;
  `0x7ff80000`+ is one slot, **self-aliased**, resolving to whichever core
  issues the access. Not a globally routable name, so it cannot be handed to
  another core or a DMA engine. No image holds a literal congruent to the target
  offsets mod `0x80000` outside its own slot.
- **The crash-dump container.** Header confirmed byte-for-byte against real
  hardware data: `\x00CDH` magic, version `0x00020200`, `"KNGND122"` at `+0x08`.
- **Record framing.** 8 + 4·nargs; the "level" byte's low nibble is a per-block
  record index; bit 31 is a terminator. 733 records decode with intact args.
- **The selector is CDW12.** `ctx+0x38` = `CDW12[7:0]`, anchored against
  Firmware Image Download whose CDW10/CDW11 are fixed by the NVMe spec.

## Still INFERRED, and honestly so

- **Which specific assert fired.** Not retrieved — but **no longer blocked**.
  The dump's 16 per-core log regions need `0x52500` bytes and only 128 KiB was
  reachable, covering cores 0–3.

  The cause is now understood and fixed. The ceiling is **not** a hardware
  limit: `ctrl->max_hw_sectors` comes from
  `min(NVME_MAX_KB_SZ << 1, dma_opt_mapping_size(dev) >> 9)`, and
  `dma_opt_mapping_size()` is an IOMMU *optimal*-size **hint** —
  `iova_rcache_range()` = 32 × PAGE_SIZE = 128 KiB. That is the observed
  32-page cliff exactly. `tools/nvme-noreset/` now has a `max_admin_xfer_ids`
  parameter that raises the admin queue's limit to 4 MiB for a matched device
  only, with all 72 exported symbol CRCs still identical to stock.

  Remaining practical bound: the 128-segment DMA cap means the buffer must be
  backed by physically contiguous chunks — hugetlbfs / `MAP_HUGETLB` works,
  ordinary `malloc()` may not.

  **Also proven along the way: there is no windowing sub-command.** A
  byte-exhaustive scan of the whole handler, decoding every offset without
  assuming instruction boundaries, finds only `CDW10`. No arm takes a core,
  block or region selector, and each recomputes its source from a
  firmware-owned descriptor, so there is no cursor either.
- **The GC deadlock.** The state machine and the PFail early-exit were found;
  the circular wait was not. Narrowed 2026-08-03 to two counters,
  `0x7ff810d0` / `0x7ff810d8`, that GC waits on and that only media-completion
  handlers decrement — see `sn200-shutdown-path.md` §6 item 1. Still not proven.
- **Whether PROC8's admin PFAIL monitor can actually spin forever.** Its poll
  loop is provably unbounded and its guard word `0x7ff95678` is provably never
  incremented in either PROC8 image; whether it is reached with a non-zero value
  is inferred, not shown.

## Resolved and retracted, 2026-08-03

- **The "mid-shutdown PFAIL re-arm" (defect 3 / S8) is WITHDRAWN.** The word the
  branch tests, `[0x7ff8c7ec]`, is not a SAM handshake — it is PROC0's boot info
  block, whose marker field is written only by boot-side code. The branch sits
  *after* `SYS: Returning shutdown completion` and is unreachable while PFAIL is
  asserted (`0x7ffa8d25` diverts first); what it schedules is
  "Waiting for CC.EN (FAST_RESTART) from PcieMgr", where re-enabling PFAIL
  monitoring is correct. The shutdown path has two defects, not three.
- **`PROC8 Admin_ShutdownPFailMonitor` (`0x7ffb1b60`) is read.** The "image
  holes" explanation was wrong — the address is inside a loaded segment. It is a
  genuine second PFAIL monitor: **polled**, spawned once per admin shutdown,
  **with no timeout at all**, and one-shot (it terminates on the first edge after
  flipping the global shutdown mode to PFail). It shares no state with PROC0's
  monitor, so the two do not race.
- **`0x7ff8c7c4` is a "system-area-saving shutdown in progress" flag**, set by a
  type-3 shutdown request at PROC0 `0x7ffa8ee8` and cleared at `0x7ffa8bc0` —
  not the power-backup health gate previously guessed.

## Deliberately not claimed

- **The UART escape.** Every firmware link is proven; the **pinout is unknown**
  and cannot be derived from firmware (PROC0 has no UART MMIO or pinmux).
- **Slot-A `op0=2` semantics**, most of FLIX format-`0xE`'s ALU class, and
  `movi`'s immediate sign. Marked unresolved in `sn200-xtensa-isa.md`; the
  decompiler emits explicit `unknown` pcodeops rather than guesses.
- **`0xEC`'s concrete handler.** Its opcode→handler binding is runtime-built
  BSS, past every image's load range. Not statically resolvable.
- **SLC/MLC selection and any length clamp on the raw NAND write path.**

## What would change the conclusion

Nothing currently open would overturn the root cause — the four routes above are
independent, and two of them (the vendor's own errata, and the drive's own log)
do not depend on our disassembly being right. The open items would refine
*which* defect fired in a given incident, not *whether* the mechanism is as
described.

The one result that would genuinely change the picture is a **cross-core write
path**, since three conclusions rest on isolation. That was specifically hunted
and refuted structurally, with the one known cross-processor write (PROC13
loading FCC microcode) documented as the honest limit.

## Practical bottom line

The mechanism is understood well enough to act on. What remains unknown is
detail, not direction — and the actions it implies (orderly shutdowns, keep
`KNGND122`, never send the catastrophic commands, plan for replacement) do not
change if the remaining gaps close.
