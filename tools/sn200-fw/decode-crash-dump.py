#!/usr/bin/env python3
"""Decode an SN200 crash dump into human-readable firmware log/assert text.

    ./decode-crash-dump.py crash.bin --string-table ~/sn200fw/fw/KNGND122/StringTable.csv
    ./decode-crash-dump.py crash.bin --string-table strtbl.bin      # pulled off the drive
    ./decode-crash-dump.py crash.bin --fw-dir ~/sn200fw --rev KNGND122

The string table may be the firmware image's `StringTable.csv[.gz]`, or the raw
blob retrieved from the drive with VUC 0xC6 CDW12 0x0220 -- the loader sniffs
the container either way. Prefer the drive's own copy when you have it: it is
guaranteed to match the running firmware.

Purely offline. Reads files, talks to nothing.
"""

from __future__ import annotations

import argparse
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from sn200_dump import (
    ascii_runs,
    find_frame_layout,
    parse_chain,
    rank_asserts,
    scan_records,
    sniff,
)
from sn200_strtab import StringTable

import sn200_container


def decode_container(data: bytes, table: StringTable, args) -> int:
    """The blob carries the CDH container magic, so the framing is known and
    does not need deriving. See docs/sn200-crash-dump-retrieval.md §4.3."""
    print("-- container ---------------------------------------------------------")
    print(
        "  CDH crash dump, version 0x%08x, FWREV %s"
        % (
            sn200_container.container_version(data),
            sn200_container.container_fwrev(data),
        )
    )
    cov = sn200_container.coverage(data)
    blocks = sn200_container.parse_container(data, table)
    print(
        "  %d log blocks, cores present: %s"
        % (cov["blocks"], cov["cores_present"] or "none")
    )

    ok = sn200_container.hash_matches(blocks, table)
    if ok is False:
        print("  !! block HASHVAL does not match the string table's. The table is")
        print("     for a different firmware revision; every message below is WRONG.")
    elif ok:
        print("  HASHVAL 0x%08x matches the string table" % table.hashval)

    if len(data) < cov["bytes_for_16_cores"]:
        print(
            "  !! TRUNCATED. All 16 cores need 0x%x bytes; this blob has 0x%x."
            % (cov["bytes_for_16_cores"], len(data))
        )
        print(
            "     Cores above %d are absent, and an assert on one of them"
            % cov["highest_complete_core"]
        )
        print("     cannot be found here no matter how the data is parsed.")
    print()

    records = [r for b in blocks for r in b.records]
    asserts = [r for r in records if r.is_assert]
    print("-- records -----------------------------------------------------------")
    print(
        "  %d records across %d blocks; assert-level (0x20): %d"
        % (len(records), len(blocks), len(asserts))
    )
    print()
    if asserts:
        print("  *** THE ASSERT ***")
        for r in asserts:
            print("  core%d StrId %d: %s" % (r.core, r.str_id, r.text))
        print()
    elif records:
        print("  No assert-level record is present. Every record here is")
        print("  informational. If the drive is latched, the firing assert is")
        print("  somewhere this blob does not reach.")
        print()

    if not args.asserts_only:
        for b in blocks:
            print(
                "== block @%05x  core%d flags%x  index %d  serial %d  (%d records)"
                % (b.offset, b.core, b.flags, b.index, b.serial, len(b.records))
            )
            for r in b.records:
                print(
                    "  %05x #%-3d lvl%02x id%-4d ts=%08x %s%s"
                    % (
                        r.offset,
                        r.index,
                        r.level,
                        r.str_id,
                        r.ccount,
                        r.text,
                        "   *** ASSERT ***" if r.is_assert else "",
                    )
                )
    if args.json:
        with open(args.json, "w") as f:
            json.dump(
                {
                    "container": "CDH",
                    "fwrev": sn200_container.container_fwrev(data),
                    "coverage": cov,
                    "records": [vars(r) for r in records],
                },
                f,
                indent=2,
                default=str,
            )
    return 0


def hexdump(data: bytes, base: int = 0, limit: int = 256) -> str:
    out = []
    for i in range(0, min(len(data), limit), 16):
        row = data[i : i + 16]
        hexs = " ".join("%02x" % b for b in row)
        txt = "".join(chr(b) if 32 <= b < 127 else "." for b in row)
        out.append("  %08x  %-47s  |%s|" % (base + i, hexs, txt))
    return "\n".join(out)


def load_table(args) -> StringTable:
    if args.string_table:
        return StringTable.load(args.string_table)
    if args.fw_dir:
        p = os.path.join(args.fw_dir, "fw", args.rev, "StringTable.csv")
        if not os.path.exists(p):
            p += ".gz"
        return StringTable.load(p)
    raise SystemExit("need --string-table or --fw-dir")


def main() -> int:
    ap = argparse.ArgumentParser(
        description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter
    )
    ap.add_argument("dump", help="crash.bin / pfail.bin / drvlog.bin")
    ap.add_argument(
        "--string-table", help="StringTable.csv[.gz] or an on-drive STRTBL blob"
    )
    ap.add_argument(
        "--fw-dir",
        default=os.environ.get("SN200_FW"),
        help="unpacked firmware tree (default $SN200_FW)",
    )
    ap.add_argument("--rev", default=os.environ.get("SN200_REV", "KNGND122"))
    ap.add_argument(
        "--mode",
        choices=("auto", "framed", "scan"),
        default="auto",
        help="auto: derive the record framing, fall back to scan",
    )
    ap.add_argument("--max-records", type=int, default=5000)
    ap.add_argument("--asserts-only", action="store_true")
    ap.add_argument("--json", metavar="FILE", help="also write structured output")
    ap.add_argument("--strings", action="store_true", help="dump ASCII runs")
    args = ap.parse_args()

    data = open(args.dump, "rb").read()
    table = load_table(args)

    print("=" * 78)
    print("SN200 crash dump decode")
    print("=" * 78)
    print("dump         : %s (%d bytes, 0x%x)" % (args.dump, len(data), len(data)))
    print("string table : FWREV=%s, %d entries" % (table.fw_rev, len(table)))
    if table.fw_rev != args.rev and args.rev:
        print(
            "  !! string table is %s but --rev says %s. StrIds are NOT stable"
            % (table.fw_rev, args.rev)
        )
        print("     across firmware revisions -- decoding with the wrong table")
        print("     yields plausible-looking but WRONG messages.")
    print()

    print("-- container ---------------------------------------------------------")
    for n in sniff(data):
        print("  " + n)
    print()
    print("-- first 256 bytes ---------------------------------------------------")
    print(hexdump(data))
    print()

    if sn200_container.is_container(data) and args.mode != "scan":
        return decode_container(data, table, args)

    layout = None
    chain_len = 0
    if args.mode in ("auto", "framed"):
        layout, chain_len = find_frame_layout(data, table)
        print("-- record framing ----------------------------------------------------")
        if layout:
            print("  derived layout: %s" % layout.describe())
            print("  longest self-consistent record chain: %d records" % chain_len)
            print("  (a wrong layout cannot chain; treat a long chain as proof)")
        else:
            print(
                "  no self-consistent record framing found (best chain %d)." % chain_len
            )
            print("  Falling back to an unframed descriptor scan: message text is")
            print("  still reliable, ARGUMENT VALUES ARE NOT.")
        print()

    if layout and args.mode != "scan":
        starts = []
        # restart the chain wherever a previous one ended, to cover a ring
        # buffer that wraps or a dump with several log regions
        off = 0
        import struct as _s
        from sn200_dump import _is_descriptor

        while off <= len(data) - 4:
            if _is_descriptor(_s.unpack_from("<I", data, off)[0], table):
                starts.append(off)
                recs = parse_chain(data, off, layout, table, limit=args.max_records)
                off = (
                    recs[-1].offset + layout.record_words(recs[-1].desc.nargs) * 4
                    if recs
                    else off + 4
                )
            else:
                off += 4
        records = []
        for s in starts:
            records += parse_chain(data, s, layout, table, limit=args.max_records)
        # de-dup by offset, keep order
        seen = set()
        records = [r for r in records if not (r.offset in seen or seen.add(r.offset))]
        mode_used = "framed"
    else:
        records = scan_records(data, table)
        mode_used = "scan"

    print("-- candidate root cause ---------------------------------------------")
    print("  level 0x20 IS the assert level: the firmware's assert idiom is")
    print("  `log(descriptor); break.n`, and the StrId of that record is the")
    print("  entire assert identity. Records marked ASSERT below fired a trap.")
    print()
    ranked = rank_asserts(records, table)
    if not ranked:
        print("  no assert-level or failure-looking record found.")
    for r in ranked[:15]:
        mark = "ASSERT" if r.is_assert else "      "
        ts = ("ccount=%10u " % r.timestamp) if r.timestamp is not None else ""
        print(
            "  %s @0x%08x %sStrId %-5d lvl %02x | %s"
            % (mark, r.offset, ts, r.str_id, r.desc.level, r.text)
        )
    print()

    if not args.asserts_only:
        print(
            "-- records (%s mode, %d found) ----------------------------------"
            % (mode_used, len(records))
        )
        print("  (timestamps are raw Xtensa CCOUNT cycles, NOT wall time)")
        for r in records[: args.max_records]:
            mark = "!" if r.is_assert else " "
            ts = ("%10u " % r.timestamp) if r.timestamp is not None else ""
            print(
                "  %s@0x%08x %sStrId %-5d | %s" % (mark, r.offset, ts, r.str_id, r.text)
            )
        if len(records) > args.max_records:
            print(
                "  ... %d more (raise --max-records)"
                % (len(records) - args.max_records)
            )
        print()

    if args.strings:
        print("-- ASCII runs --------------------------------------------------------")
        for off, s in ascii_runs(data):
            print("  @0x%08x %s" % (off, s))
        print()

    if args.json:
        with open(args.json, "w") as fh:
            json.dump(
                {
                    "dump": args.dump,
                    "dump_size": len(data),
                    "fw_rev": table.fw_rev,
                    "mode": mode_used,
                    "layout": layout.describe() if layout else None,
                    "chain_len": chain_len,
                    "container": sniff(data),
                    "asserts": [
                        {
                            "offset": r.offset,
                            "str_id": r.str_id,
                            "level": r.desc.level,
                            "is_assert": r.is_assert,
                            "ccount": r.timestamp,
                            "args": r.args,
                            "text": r.text,
                        }
                        for r in ranked[:50]
                    ],
                    "records": [
                        {
                            "offset": r.offset,
                            "str_id": r.str_id,
                            "level": r.desc.level,
                            "nargs": r.desc.nargs,
                            "is_assert": r.is_assert,
                            "ccount": r.timestamp,
                            "args": r.args,
                            "pre": r.pre_words,
                            "text": r.text,
                        }
                        for r in records[: args.max_records]
                    ],
                },
                fh,
                indent=2,
            )
        print("wrote %s" % args.json)
    return 0


if __name__ == "__main__":
    sys.exit(main())
