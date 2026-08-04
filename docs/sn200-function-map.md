# SN200 KNGND122 function-boundary map

A verified function-boundary map for all 18 processor images of the
HGST/WDC Ultrastar SN200 firmware (Tensilica Xtensa, `KNGND122`). Machine-
readable output: `tools/sn200-fw/function-map.json`. Lookup tool:
`tools/sn200-fw/whichfunc.py`. Builder: `tools/sn200-fw/funcmap.py`.

## Why this exists

Two separate reverse-engineering findings in this project were wrong
because someone disassembled starting at an address nobody had verified
was an instruction boundary:

- a `bnall` at `0x3003354d` that **never existed** — the bytes were the
  tail of a preceding FLIX bundle, and the desynced stream also produced a
  bogus floating-point `add.s` in integer control code.
- marker 8 was declared "dead code" after disassembling from `0x7ffa7d6d`,
  which is actually a `retw.n` *inside* the real function — the real entry
  is `0x7ffa7a68`.

`whichfunc.py 0x7ffa7d6d` now reports exactly that: in `PROC12_7ff80000`
(the image that writes marker 8), `0x7ffa7d6d` is offset `0x305` into the
function starting at `0x7ffa7a68`, not a function start itself. Checking an
address there before disassembling from it makes this error class
impossible.

## Method

1. **Byte-scan for `entry`.** `entry` is the only Xtensa opcode whose byte
   0 is `0x36` — op0=6 (from the low nibble), n=3 and m=0 (from the high
   two bits of that same byte) are all fixed by that one byte. Every
   `0x36` byte in a real (`.SEG`-header-derived) segment is a candidate
   function entry. Segments come from parsing the original
   `fw/KNGND122/PROC*.bin`/`FCC.bin` containers with `segparse.py`, **not**
   from `flat/*.bin`: the flat merge zero-pads large address-space gaps
   between segments (e.g. ~136KB between PROC8's two `0x30000000`-bank
   segments) that are not part of any real segment and would otherwise be
   miscounted as "scanned but empty."

2. **Plausibility pre-filter.** Measured over every `0x36` byte in all 18
   images: a real `entry` instruction only ever legitimately targets `a1`
   (the stack pointer) with a modest frame size — median 0x20 bytes, 99th
   percentile 0x90, with a long tail of >0x400 outliers that are all
   coincidental `0x36` bytes landing inside unrelated code, never real
   prologues. `entry a1, <=0x400` passes; anything using a different
   register or a bigger frame is rejected before a walk is ever attempted.
   This single check is what fixed the worst false-positive class found
   during development: a candidate `entry a8,0x5178` a few bytes inside a
   real function's FLIX-encoded body, whose absurd 20856-byte frame was the
   tell.

3. **Validate by forward walk.** Each surviving candidate is disassembled
   forward, instruction by instruction, until it reaches a terminator
   (`ret`, `retw`, `ret.n`, `retw.n`, or `jx`) — a CONFIRMED entry — or
   fails: a hard decode exception, a genuinely reserved/invalid `op0`
   class (`op0=3` or `op0=4`, essentially never emitted by a real
   compiler), a second plausible `entry` before any terminator (not valid
   straight-line windowed-ABI code), or exceeding an 8KB walk bound.
   Plain `?`-bearing decode results from `xdis.py`'s still-incomplete FLIX
   slot-B/C sub-forms (`?B`, `?C`, `?Balu`) are **not** treated as a
   desync signal: every Xtensa `op0` class fixes its instruction's length
   by construction (narrow=2, FLIX=8, everything else=3) regardless of
   whether the finer sub-opcode is named, so a `?`-bearing instruction only
   means "real code hit a decoder gap," not "we lost byte alignment."
   Treating it as fatal was tried during development and it rejected
   genuinely well-formed, correctly-terminating functions in PROC8's
   overlay bank that legitimately run 20-50% `?B`/`?C` over long clean
   stretches.

4. **Stop at the first terminator, not the last.** A function can have
   several early-exit `retw.n`s (compiled `switch`-like dispatch through
   `jx` is common here), so it is tempting to keep walking past the first
   terminator looking for a "real" final one. That was also tried, and it
   silently swallowed a neighboring real function (`0x7ffa7a68`) into a
   3137-byte "extent" by wandering through non-code bytes reachable only
   via the dispatcher's jump table, not by fall-through. Stopping at the
   first terminator undercounts the extent of multi-exit functions a
   little (their later case-handler blocks show up as gap bytes, not
   folded into the dispatcher); it never claims bytes that are not really
   this function's, which is the property that matters for the tool's
   purpose.

5. **Overlap resolution.** Real Xtensa function bodies never overlap.
   Candidates are processed in ascending address order; a candidate whose
   validated range starts before the previous accepted function's end is
   dropped as a misaligned re-sync artifact rather than accepted as a
   second, overlapping function.

6. **Calls.** `call8`/`call4`/`call12` targets (always statically
   resolvable) are collected from every confirmed function's walk and
   cross-checked against the confirmed-entry set: a target that is also an
   entry is marked `confirmed_call`; the callers list is recorded. `callx*`
   preceded by an `l32r` into the same, not-yet-clobbered register is
   resolved the same way. Targets that do **not** land on an entry are
   recorded per image as `call_targets_without_entry` — see Anomalies.

## Coverage

| Image | Functions | Rejected candidates | Calls found | Calls without entry | Coverage |
|---|---:|---:|---:|---:|---:|
| FCC_00100000 | 0 | 242 | 0 | 0 | 0.0% |
| PROC0_7ff80000 | 473 | 97 | 1915 | 19 | 53.3% |
| PROC1_7ff80000 | 111 | 60 | 400 | 76 | 76.2% |
| PROC2_7ff80000 | 145 | 155 | 649 | 118 | 77.7% |
| PROC3_7ff80000 | 144 | 160 | 647 | 115 | 77.9% |
| PROC4_7ff80000 | 144 | 160 | 645 | 114 | 77.9% |
| PROC5_7ff80000 | 145 | 161 | 647 | 116 | 77.9% |
| PROC6_7ff80000 | 364 | 170 | 1547 | 530 | 80.2% |
| PROC7_7ff80000 | 113 | 57 | 397 | 74 | 75.9% |
| PROC8_30000000 | 184 | 90 | 2307 | 2281 | 28.2% |
| PROC8_7ff80000 | 430 | 124 | 1734 | 71 | 59.0% |
| PROC9_7ff80000 | 379 | 166 | 2150 | 757 | 76.9% |
| PROC10_7ff80000 | 145 | 184 | 642 | 109 | 78.0% |
| PROC11_7ff80000 | 72 | 37 | 235 | 65 | 73.9% |
| PROC12_7ff80000 | 72 | 55 | 283 | 34 | 73.6% |
| PROC13_7ff80000 | 298 | 158 | 1349 | 442 | 75.7% |
| PROC14_7ff80000 | 211 | 139 | 732 | 156 | 75.9% |
| PROC15_7ff80000 | 116 | 59 | 410 | 81 | 76.1% |
| **Total** | **3546** | **2274** | **16689** | **5158** | **69.1%** (1,166,034 / 1,688,380 bytes) |

"Coverage" is bytes claimed by a confirmed function's `[entry, end)` range
divided by total real segment bytes (from `.SEG` headers, not the padded
flat merge). Uncovered bytes are literal pools, jump tables, later
case-handler blocks of multi-exit dispatchers (see method step 4), the
small fixed-size interrupt-vector segments most images carry near
`0x7ffa0000` (typically not `entry`-prefixed handler stubs), and — for
`PROC8_30000000` and `FCC_00100000` specifically — the two structural
anomalies below. The map does not guess at any of this; every byte is
either inside a confirmed function or explicitly not.

## Known-good anchor validation

| Anchor | Description | Result |
|---|---|---|
| `0x3003353c` | OAM erase handler (PROC8 overlay bank) | **IS an entry** |
| `0x7ffaae35` | latch test (crash-section check) | inside function `0x7ffaa078` (PROC10), offset `0xdbd` |
| `0x7ffaae3d` | latch test (pfail-section check) | inside function `0x7ffaa078` (PROC10), offset `0xdc5` |
| `0x7ffaaf08` | forced marker 9 target | inside function `0x7ffaaed0` (PROC10), offset `0x38` |
| `0x7ffa6b30` | Post-Crash gate | inside function `0x7ffa6930` (PROC10), offset `0x200` |
| `0x7ffabbf0` | marker-3 writer | **IS an entry** (PROC0) |
| `0x7ffa7a68` | marker-8 writer | **IS an entry** (PROC12) |
| `0x7ffba9dc` | SAM read-only flag | inside function `0x7ffba778` (PROC13), offset `0x264` |
| `0x7ffbba61` | System Area save | inside function `0x7ffbb848` (PROC13), offset `0x219` |

Three of the nine are themselves function entries; the other six are
mid-function addresses per the prior docs' own framing (branch tests, a
jump target, a gate condition, a flag check, a save-point instruction —
none of these are described as function starts in the source docs). What
matters for correctness is that **all nine fall inside a function this map
identifies**, never inside an unaccounted gap: every one of them checks out
under `whichfunc.py --image <name> <addr>`.

## Anomalies

### PROC8_30000000 (the overlay bank): ~99% of calls don't resolve, 28% coverage

Not a scan defect. Docs tag PROC8's `0x30000000`/`0x40078` region as
`OVB` — "overlay bank." Its `.SEG` headers show three segments with a real,
**unfilled** ~136KB gap between `0x30000af8` and `0x30022238` (confirmed
by inspecting the original `PROC8.bin` container, not the zero-padded flat
merge). Correctly-decoded, well-formed code in this bank
(`call8 0x30020da0`, checked directly at `0x30028b77`/`0x30028baf` after
disassembling from the *verified* entry `0x30028aa4`, not an arbitrary
address) calls into that gap. The only explanation consistent with the
segment data is memory-mapped bank switching: this dump captured whichever
overlay page was resident at capture time, and calls into the gap target
code that lives in a *different, not-currently-dumped* overlay bank at the
same address window. This map only claims what it can see; it does not
guess at the contents of banks it doesn't have.

### FCC_00100000: 0 functions found

Also not a defect. Direct disassembly of `FCC.bin`'s code segment
(`0x120180` onward) shows `call0`, `addi a1,a1,-80` / `addi a1,a1,80`
frame setup, and `rfei` (return-from-exception) — the **call0 ABI**, not
the windowed `entry`/`retw` convention this scan looks for. All 242 `0x36`
bytes found there are coincidental, and every one was correctly rejected
by the plausibility filter (none is `entry a1, <=0x400`). Mapping FCC's
functions would need a call0-ABI-aware scanner (look for `addi a1,a1,-N`
prologues terminated by `ret`, not `entry`/`retw`), which is out of scope
here; the honest result for this scan is zero.

### `call_targets_without_entry`: 5158 total, concentrated in a few images

Excluding PROC8_30000000's overlay-bank artifacts above, the remaining
~2800 fall into two expected buckets, sampled during development:

- **Genuine call0-ABI leaf helpers.** E.g. `0x3002c1a0`, called from 53
  sites: byte at that address is `0xb0`, not `0x36`, and disassembling
  from exactly that address (not a guessed nearby one) gives clean,
  plausible code (`addx2`, `l8ui`, `l32r`, ...) with no `entry`/`retw` at
  all — a small subroutine compiled without its own register window,
  reached via `call8` from windowed-ABI callers. This map only tracks
  `entry`-based functions; call0-ABI callees are a known gap, not a
  desync.
- **Decoder-desynced call instructions inside otherwise-valid functions.**
  A `call8`/`call4`/`call12` mnemonic can appear as a false read inside an
  `xdis.py` decode gap (a `?B`/`?C` slot) without the surrounding
  function's overall walk being rejected, since that walk still correctly
  reaches a terminator. These are lower-confidence and are reported as-is
  in `function-map.json` rather than silently dropped, so they remain
  auditable.

Per-image counts are in the coverage table above; the full list (caller
function, call site, and target) is in each image's
`call_targets_without_entry` array in `function-map.json`.

### Rejected candidates: 2274 total

Byte-scan hits that were either an implausible `entry` (wrong register,
frame >0x400 bytes) or a plausible one that failed the forward-walk
validation (hard decode exception, reserved `op0` class, or no terminator
within 8KB). These are recorded per image
(`function-map.json` → `rejected_candidates`) for auditability but are
never promoted to functions — consistent with not padding the map with
low-confidence guesses.

## Using the tools

```
cd tools/sn200-fw
python3 funcmap.py                              # rebuild function-map.json
python3 funcmap.py --self-test                  # check the anchors above
python3 whichfunc.py 0x7ffa7d6d                  # search all images
python3 whichfunc.py --image PROC12_7ff80000 0x7ffa7d6d   # scope to one image
```

`whichfunc.py` without `--image` searches every image and prints **every**
match, not just the first: the same address routinely falls inside
unrelated functions in several of the 15 independent PROC0-PROC15 address
spaces at once (they all load around `0x7ffa0000`+), and silently picking
one would reintroduce exactly the kind of wrong-context mistake this map
exists to prevent.
