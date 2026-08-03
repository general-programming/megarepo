#!/usr/bin/env python3
"""A stand-in for `nvme` that emulates an SN200's 0xC6 vendor reads.

Lets the retrieval script be exercised end to end with no hardware. The
emulated drive is described by environment variables:

  FAKE_NVME_DIR       working dir holding the synthetic section images
  FAKE_NVME_OFFSET    "cdw13" (default) | "cdw11" | "ignored"
                      which register the fake firmware honours as the read
                      offset. "ignored" reproduces the failure mode the real
                      script's offset probe exists to catch.
  FAKE_NVME_FAIL_AT   byte offset at which the crash-dump body read starts
                      failing, to exercise resume. Empty = never fail.
  FAKE_NVME_FAIL_ONCE if set, FAKE_NVME_FAIL_AT only fires on the first attempt

Section images live at $FAKE_NVME_DIR/<name>.img and are created by the tests.
"""

import os
import sys

# CDW12 -> (section name, is_size_probe, which dword of the size reply)
BODY = {0x0420: "crash", 0x0620: "pfail", 0x0220: "strtbl", 0x0020: "drvlog"}
SIZE = {0x0320: ("crash", 0), 0x0520: ("pfail", 0), 0x0120: ("drvlog", 0)}
# 0x0120 is a shared probe: dword[0] = drvlog size, dword[1] = strtbl size.


def die(msg: str, code: int = 1):
    sys.stderr.write("fake_nvme: %s\n" % msg)
    sys.exit(code)


def main() -> int:
    argv = sys.argv[1:]
    if not argv:
        die("no args")
    if argv[0] == "id-ctrl":
        sys.stdout.buffer.write(b"\0" * 4096)
        return 0
    if argv[0] != "admin-passthru":
        return 0

    opt = {}
    for a in argv:
        if a.startswith("--") and "=" in a:
            k, v = a[2:].split("=", 1)
            opt[k] = int(v, 0)

    if opt.get("opcode") != 0xC6:
        die(
            "refusing non-0xC6 opcode 0x%x -- the tool must never emit this"
            % opt.get("opcode", 0)
        )
    if opt.get("namespace-id", 0) != 0:
        die("NSID must be 0")

    d = os.environ["FAKE_NVME_DIR"]
    c12 = opt["cdw12"]
    nbytes = opt["data-len"]
    ndw = opt["cdw10"]

    def img(name):
        with open(os.path.join(d, name + ".img"), "rb") as fh:
            return fh.read()

    if c12 in SIZE:
        if nbytes != 8 or ndw != 2:
            die("size probe must be cdw10=2 data-len=8")
        if c12 == 0x0120:
            reply = len(img("drvlog")).to_bytes(4, "little") + len(
                img("strtbl")
            ).to_bytes(4, "little")
        else:
            reply = len(img(SIZE[c12][0])).to_bytes(4, "little") + b"\0\0\0\0"
        sys.stdout.buffer.write(reply)
        return 0

    if c12 not in BODY:
        die("unknown cdw12 0x%04x" % c12)
    if ndw * 4 != nbytes:
        die("cdw10 (%d dwords) disagrees with data-len (%d bytes)" % (ndw, nbytes))

    mode = os.environ.get("FAKE_NVME_OFFSET", "cdw13")
    if mode == "ignored":
        off = 0
    else:
        off = opt.get(mode, 0) * 4

    name = BODY[c12]
    if name == "crash":
        fail_at = os.environ.get("FAKE_NVME_FAIL_AT", "")
        if fail_at and off == int(fail_at):
            stamp = os.path.join(d, ".failed_once")
            if not (os.environ.get("FAKE_NVME_FAIL_ONCE") and os.path.exists(stamp)):
                open(stamp, "w").close()
                sys.stderr.write("NVMe status: INTERNAL(0x7c5)\n")
                return 1

    data = img(name)
    sys.stdout.buffer.write(data[off : off + nbytes].ljust(nbytes, b"\0"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
