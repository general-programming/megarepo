"""Tests for check-latch-state.sh, the read-only armed/empty triage.

The decision this script drives is whether a non-destructive recovery is even
possible, so the branch logic matters more than the happy path. It must never
emit anything but 0xC6 reads.
"""

import os
import shutil
import subprocess
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)
SCRIPT = os.path.join(TOOLS, "check-latch-state.sh")

pytestmark = pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")


def run(tmp_path, empty="", sizes=None):
    drive = tmp_path / "drive"
    drive.mkdir(exist_ok=True)
    for name, n in (
        sizes or {"crash": 0x320000, "pfail": 4096, "strtbl": 4096, "drvlog": 4096}
    ).items():
        (drive / (name + ".img")).write_bytes(b"\0" * n)

    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    shim = bindir / "nvme"
    shim.write_text(
        '#!/bin/sh\nexec %s %s "$@"\n'
        % (sys.executable, os.path.join(HERE, "fake_nvme.py"))
    )
    shim.chmod(0o755)

    dev = tmp_path / "fakedev"
    dev.write_bytes(b"")

    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    env["FAKE_NVME_DIR"] = str(drive)
    env["FAKE_NVME_EMPTY"] = empty
    return subprocess.run(
        ["bash", SCRIPT, str(dev)], env=env, capture_output=True, text=True
    )


def test_only_pfail_armed_is_the_hopeful_case(tmp_path):
    r = run(tmp_path, empty="crash")
    out = r.stdout
    assert "CRASH section (CDW12 0x0320) : EMPTY" in out
    assert "PFAIL section (CDW12 0x0520) : ARMED" in out
    assert "Only the PFAIL section is armed" in out
    assert "0x0603" in out
    # PFAIL-only is surprising after a power event (UNEXSTRT arms CRASH), and
    # the script should say so rather than encouraging a clear
    assert "SURPRISING" in out
    assert "UNVERIFIED" in out


def test_crash_armed_says_there_is_no_safe_path(tmp_path):
    r = run(tmp_path, empty="")
    out = r.stdout
    assert "The CRASH section is armed" in out
    assert "ZEROES THE NAMESPACE" in out
    assert "NO known non-destructive path" in out
    # it must also say the data is still recoverable-in-principle, so nobody
    # reads "armed" as "already lost" and fires the clear reflexively
    assert "INTACT" in out
    assert "irreversible" in out


def test_crash_armed_even_when_pfail_is_empty(tmp_path):
    """Either section alone arms the latch, so an empty pfail does not make a
    crash-armed drive safe."""
    r = run(tmp_path, empty="pfail")
    assert "The CRASH section is armed" in r.stdout


def test_neither_armed_refuses_to_guess(tmp_path):
    r = run(tmp_path, empty="crash,pfail")
    assert "Neither section reports as armed" in r.stdout


def test_armed_is_read_from_status_not_from_the_size_value(tmp_path):
    """A not-armed section makes the probe FAIL with SC 0xC3; it does not
    return zero. And 0x00320000 is a fixed reservation, so the VALUE must never
    be used to decide armed-ness."""
    r = run(
        tmp_path,
        empty="crash",
        sizes={"crash": 0x320000, "pfail": 0x320000, "strtbl": 4096, "drvlog": 4096},
    )
    # identical sizes, opposite verdicts -> the value played no part
    assert "CRASH section (CDW12 0x0320) : EMPTY" in r.stdout
    assert "PFAIL section (CDW12 0x0520) : ARMED 3276800" in r.stdout


def test_script_only_ever_issues_read_commands(tmp_path):
    """fake_nvme.py hard-fails on any opcode other than 0xC6, so a clean run is
    itself the assertion. Belt and braces: check every line that invokes nvme.

    (The destructive encodings DO appear in the script's explanatory output --
    that is deliberate and is why this greps invocations, not the whole file.)"""
    r = run(tmp_path, empty="crash")
    assert r.returncode == 0, r.stdout + r.stderr
    invocations = [
        line
        for line in open(SCRIPT).read().splitlines()
        if "nvme " in line
        and not line.lstrip().startswith("#")
        and "admin-passthru" in line
        or "id-ctrl" in line
    ]
    assert invocations, "no nvme invocations found -- test is not checking anything"
    for line in invocations:
        for forbidden in (
            "0xff",
            "0xFF",
            "0xdd",
            "0xDD",
            "0x0503",
            "0x0603",
            "0x0303",
            "0x0403",
        ):
            assert forbidden not in line, (
                "check-latch-state.sh must never issue %s: %s" % (forbidden, line)
            )
