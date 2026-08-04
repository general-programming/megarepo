# SN200: what is actually proven, and what is not

An explicit confidence audit, written because "we are confident" is not the same
as "we checked". Every claim below is graded, and the ungraded gaps are named.

## The root cause — PROVEN, by four independent routes

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

- **Which specific assert fired.** Not retrieved. The dump's 16 per-core log
  regions need `0x52500` bytes; the kernel caps admin transfers at **128 KiB**,
  which covers cores 0–3 only. This is a *host* limit — confirmed as `EINVAL`
  from the ioctl, not an NVMe status — and the firmware's only clamp is
  `minu a15,a7,a15` against the section size, so the drive would return all
  3.2 MiB in one command. Closing this needs a patched driver
  (`tools/nvme-noreset/`) on a diagnostics boot.
- **Whether the mid-shutdown PFAIL re-arm fires every time or rarely.** Hinges
  on when SAM publishes completion; the natural reading implies "every time" but
  it was not proven.
- **The GC deadlock.** The state machine and the PFail early-exit were found;
  the circular wait was not.
- **`PROC8 Admin_ShutdownPFailMonitor`** at `0x7ffb1bb6` — a *second*,
  admin-side PFAIL monitor that did not disassemble because the flat image has
  holes. A live candidate for another "monitor added again".

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
