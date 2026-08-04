#!/usr/bin/env python3
"""A stand-in for `nvme` that emulates an SN200's firmware slot machinery.

Lets fill-fw-slots.sh be exercised end to end with no hardware. The emulated
drive lives in a JSON state file so slot writes persist across invocations.

  FAKE_FW_STATE   path to the JSON state file (created on first use)

State keys:
  fr, mn, sn      Identify Controller strings
  frmw            Identify Controller byte 260 (default 0x0b: 5 slots, slot 1 RO)
  afi             active(bits 2:0) / next-reset(bits 6:4) firmware slot
  slots           {"1": "KNGND100", ...}
  staged          sha256 of the last completed fw-download, or null

The device's refusals mirror the real firmware (docs/sn200-firmware-flashing.md):
  slot 1 + a replacing action  -> "Firmware Activate Invalid Slot"
  commit action 3             -> INVALID_FIELD (0xC0040000 -> Generic SC 0x02)
  commit action 0/1 with no staged image -> "Invalid Image"
"""

import hashlib
import json
import os
import sys

DEFAULT = {
    "fr": "KNGND122",
    "mn": "HUSMR7676BDP3Y1",
    "sn": "FAKE0000000000000001",
    "frmw": 0x0B,
    "afi": 2,
    "slots": {"1": "KNGND100", "2": "KNGND122", "3": "KNGND110", "4": "", "5": ""},
    "staged": None,
}


def load():
    p = os.environ["FAKE_FW_STATE"]
    if not os.path.exists(p):
        json.dump(DEFAULT, open(p, "w"))
    return json.load(open(p))


def save(st):
    json.dump(st, open(os.environ["FAKE_FW_STATE"], "w"))


def die(msg: str, code: int = 1):
    sys.stderr.write("NVMe status: %s\n" % msg)
    sys.exit(code)


def opts(argv):
    out = {}
    for a in argv:
        if a.startswith("--") and "=" in a:
            k, v = a[2:].split("=", 1)
            out[k] = v
    return out


def main() -> int:
    argv = sys.argv[1:]
    if not argv:
        die("no args")
    cmd = argv[0]
    st = load()
    o = opts(argv[1:])

    # Any command that could activate, reset or wipe must never be reachable
    # from this tool. Fail loudly rather than emulate it.
    for forbidden in ("reset", "subsystem-reset", "format", "sanitize", "delete-ns"):
        if cmd == forbidden:
            die("fake_nvme_fw: tool must never emit `nvme %s`" % cmd, 99)

    if cmd == "id-ctrl":
        print(
            json.dumps(
                {
                    "fr": st["fr"],
                    "mn": st["mn"],
                    "sn": st["sn"],
                    "frmw": st["frmw"],
                    "fwug": 0,
                    "cmic": 0,
                }
            )
        )
        return 0

    if cmd == "fw-log":
        nslots = (st["frmw"] >> 1) & 7
        d = {"afi": st["afi"]}
        for i in range(1, nslots + 1):
            d["frs%d" % i] = st["slots"].get(str(i), "")
        print(json.dumps(d))
        return 0

    if cmd == "fw-download":
        path = o.get("fw")
        if not path:
            die("fw-download needs --fw", 1)
        blob = open(path, "rb").read()
        if len(blob) % 4:
            die("Invalid size: not a multiple of 4", 1)
        xfer = int(o.get("xfer", "4096"), 0)
        if xfer % 4096:
            die("xfer must be a multiple of 4096", 1)
        # Record what the last chunk would have been, so a test can assert the
        # drive is expected to take a short final transfer.
        st["last_xfer"] = xfer
        st["last_tail"] = len(blob) % xfer or xfer
        st["staged"] = hashlib.sha256(blob).hexdigest()
        st["staged_rev"] = rev_of(blob)
        save(st)
        return 0

    if cmd == "fw-commit":
        slot = int(o.get("slot", "0"), 0)
        action = int(o.get("action", "0"), 0)
        nslots = (st["frmw"] >> 1) & 7
        if slot > nslots:
            die("INVALID_FIRMWARE_SLOT: Firmware Activate Invalid Slot", 1)
        if action == 3:
            die("INVALID_FIELD: Firmware Activate Invalid Activation Action", 1)
        if action > 3:
            die("INVALID_FIELD: commit action out of range", 1)
        if action in (0, 1):
            if slot == 1 and (st["frmw"] & 1):
                die("INVALID_FIRMWARE_SLOT: Firmware Activate Invalid Slot", 1)
            if not st.get("staged"):
                die("INVALID_FIRMWARE_IMAGE: Firmware Activate Invalid Image", 1)
            st["slots"][str(slot)] = st.get("staged_rev") or "UNKNOWN"
            st["staged"] = None
        if action in (1, 2):
            st["afi"] = (st["afi"] & 0x07) | ((slot & 7) << 4)
        save(st)
        return 0

    return 0


def rev_of(blob: bytes) -> str:
    """The revision string lives in FWHEADER.bin, the first tar member's data,
    which starts at byte 512 of the bundle."""
    return blob[512:520].decode("latin1").rstrip("\0") or "UNKNOWN"


if __name__ == "__main__":
    sys.exit(main())
