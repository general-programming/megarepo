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
