"""Look up which (if any) known function an address falls inside.

This is the tool that makes the "disassembled from a mid-stream address"
mistake impossible: check an address here BEFORE disassembling from it.

    python3 whichfunc.py 0x7ffa7d6d
    python3 whichfunc.py 0x7ffa7d6d 0x3003353c ...   # multiple addresses
    python3 whichfunc.py --image PROC8_30000000 0x3003353c

Reads tools/sn200-fw/function-map.json (build/refresh it with funcmap.py).
"""

import bisect
import json
import os
import sys

MAP_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "function-map.json")


def load_map(path: str = MAP_PATH) -> dict:
    with open(path) as f:
        return json.load(f)


def build_index(data: dict) -> dict[str, tuple[list[int], list[dict]]]:
    """image name -> (sorted entry addrs, function dicts) for bisect lookup."""
    idx = {}
    for img in data["images"]:
        entries = []
        funcs = []
        for f in sorted(img["functions"], key=lambda f: int(f["entry"], 16)):
            entries.append(int(f["entry"], 16))
            funcs.append(f)
        idx[img["name"]] = (entries, funcs)
    return idx


def lookup(
    data: dict, addr: int, image: str | None = None
) -> list[tuple[str, dict, int]]:
    """Return every (image_name, function_dict, offset) whose function
    contains addr. Every one of the up to 18 images is its own independent
    processor and address space, so the SAME address routinely falls inside
    unrelated functions in several images at once -- callers must not assume
    a single answer. Pass `image` to scope the search to one image."""
    idx = build_index(data)
    names = [image] if image else list(idx.keys())
    hits = []
    for name in names:
        if name not in idx:
            continue
        entries, funcs = idx[name]
        i = bisect.bisect_right(entries, addr) - 1
        if i < 0:
            continue
        f = funcs[i]
        end = int(f["end"], 16)
        if entries[i] <= addr < end:
            hits.append((name, f, addr - entries[i]))
    return hits


def _describe_hit(addr: int, name: str, f: dict, off: int) -> str:
    entry = int(f["entry"], 16)
    if off == 0:
        return (
            "0x%08x: IS the entry point of a function in %s (size %d, confirmed_entry=%s confirmed_call=%s)"
            % (
                addr,
                name,
                f["size"],
                f["confirmed_entry"],
                f["confirmed_call"],
            )
        )
    return (
        "0x%08x: inside function 0x%08x+0x%x in %s (function size %d, ends 0x%08x)"
        % (
            addr,
            entry,
            off,
            name,
            f["size"],
            int(f["end"], 16),
        )
    )


def describe(data: dict, addr: int, image: str | None = None) -> str:
    hits = lookup(data, addr, image)
    if not hits:
        return (
            "0x%08x: NOT inside any known function%s -- do not disassemble from here without checking segment/gap context"
            % (
                addr,
                (" in " + image) if image else "",
            )
        )
    if len(hits) == 1 or image:
        name, f, off = hits[0]
        return _describe_hit(addr, name, f, off)
    lines = [
        "0x%08x: found in %d images (pass --image to disambiguate):" % (addr, len(hits))
    ]
    for name, f, off in hits:
        lines.append("  " + _describe_hit(addr, name, f, off))
    return "\n".join(lines)


def main() -> None:
    args = sys.argv[1:]
    image = None
    if args and args[0] == "--image":
        image = args[1]
        args = args[2:]
    if not args:
        sys.exit(__doc__)
    data = load_map()
    for a in args:
        addr = int(a, 16)
        print(describe(data, addr, image))


if __name__ == "__main__":
    main()
