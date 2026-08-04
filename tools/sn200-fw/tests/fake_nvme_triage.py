#!/usr/bin/env python3
"""A stand-in for `nvme` covering what sn200-triage.sh issues.

Unlike fake_nvme.py this must accept 0xFF, because the startup-type query is
0xFF/CDW12=0x0004 -- but ONLY that selector. Every destructive encoding aborts
the run, so a clean exit is itself the safety assertion.

Environment:
  FAKE_TRIAGE_MODE     startup type byte to report (default 6 = latched)
  FAKE_TRIAGE_VERSION  crash header version dword, e.g. 0x00020200
  FAKE_TRIAGE_TAG      8-byte tag at +0x40, e.g. UNEXSTRT
  FAKE_TRIAGE_UNVMCAP  unvmcap value for id-ctrl (default 0 = allocated)
  FAKE_TRIAGE_ARMED    comma list of armed sections: clog,pfail
  FAKE_TRIAGE_NOHDR    if set, the crash header read fails
"""

import os
import sys

# 0xFF selectors that must never leave the triage script.
FORBIDDEN_OAM = {0x0503, 0x0603, 0x0403, 0x0303, 0x0720, 0x0820}


def die(msg: str) -> int:
    sys.stderr.write("fake_nvme_triage: %s\n" % msg)
    sys.exit(1)


def main() -> int:
    argv = sys.argv[1:]
    if not argv:
        die("no args")

    if argv[0] == "id-ctrl":
        un = os.environ.get("FAKE_TRIAGE_UNVMCAP", "0")
        sys.stdout.write("mn        : HUSMR7676BDP3Y1\n")
        sys.stdout.write("tnvmcap   : 7681501126656\n")
        sys.stdout.write("unvmcap   : %s\n" % un)
        return 0

    if argv[0] != "admin-passthru":
        return 0

    opt = {}
    for a in argv:
        if a.startswith("--") and "=" in a:
            k, v = a[2:].split("=", 1)
            try:
                opt[k] = int(v, 0)
            except ValueError:
                pass

    op = opt.get("opcode")
    c12 = opt.get("cdw12", 0)

    if op in (0xCA, 0xDD):
        die("opcode 0x%02x is destructive and must never be issued" % op)
    if op == 0xFF:
        if c12 in FORBIDDEN_OAM:
            die("0xFF CDW12=0x%04x is destructive and must never be issued" % c12)
        if c12 != 0x0004:
            die("0xFF CDW12=0x%04x is not the read-only startup query" % c12)
        mode = int(os.environ.get("FAKE_TRIAGE_MODE", "6"))
        sys.stderr.write("NVMe command result:%08x\n" % (mode << 8))
        return 0
    if op != 0xC6:
        die("unexpected opcode 0x%s" % (op if op is None else "%02x" % op))

    armed = os.environ.get("FAKE_TRIAGE_ARMED", "clog,pfail").split(",")
    if c12 == 0x0320 and "clog" not in armed:
        sys.stderr.write("NVMe status: VENDOR SPECIFIC(0x7c3)\n")
        return 1
    if c12 == 0x0520 and "pfail" not in armed:
        sys.stderr.write("NVMe status: VENDOR SPECIFIC(0x7c3)\n")
        return 1
    if c12 in (0x0320, 0x0520):
        sys.stdout.buffer.write((0x320000).to_bytes(4, "little") + b"\0\0\0\0")
        return 0

    if c12 == 0x0420:
        if os.environ.get("FAKE_TRIAGE_NOHDR"):
            sys.stderr.write("NVMe status: INTERNAL(0x7c5)\n")
            return 1
        n = opt.get("data-len", 128)
        ver = int(os.environ.get("FAKE_TRIAGE_VERSION", "0x00020200"), 0)
        tag = os.environ.get("FAKE_TRIAGE_TAG", "").encode()
        buf = bytearray(n)
        buf[0:4] = ver.to_bytes(4, "little")
        buf[64 : 64 + len(tag)] = tag
        sys.stdout.buffer.write(bytes(buf))
        return 0

    die("unknown 0xC6 cdw12 0x%04x" % c12)


if __name__ == "__main__":
    sys.exit(main())
