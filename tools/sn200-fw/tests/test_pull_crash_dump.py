"""End-to-end tests for pull-crash-dump.sh against a fake SN200.

No hardware. `tests/fake_nvme.py` stands in for the `nvme` binary and serves
synthetic section images, so the real script's chunking, offset probe, resume
and reassembly logic all run for real.
"""

import os
import shutil
import subprocess
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)
SCRIPT = os.path.join(TOOLS, "pull-crash-dump.sh")

pytestmark = pytest.mark.skipif(
    shutil.which("sha256sum") is None and shutil.which("shasum") is None,
    reason="needs sha256sum or shasum",
)


def make_drive(tmp_path, crash_size=64 * 1024 * 3 + 4096, sizes=None):
    """Synthetic section images with position-dependent content, so a chunk
    served from the wrong offset is detectable byte for byte."""
    d = tmp_path / "drive"
    d.mkdir()
    sizes = sizes or {"crash": crash_size, "pfail": 0, "strtbl": 4096, "drvlog": 8192}
    for name, n in sizes.items():
        buf = bytearray()
        i = 0
        while len(buf) < n:
            buf += ("%s:%08d;" % (name, i)).encode()
            i += 1
        (d / (name + ".img")).write_bytes(bytes(buf[:n]))
    return d


def run(tmp_path, drive, *args, offset="cdw13", fail_at=None, fail_once=False):
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    shim = bindir / "nvme"
    shim.write_text(
        '#!/bin/sh\nexec %s %s "$@"\n'
        % (sys.executable, os.path.join(HERE, "fake_nvme.py"))
    )
    shim.chmod(0o755)

    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    env["FAKE_NVME_DIR"] = str(drive)
    env["FAKE_NVME_OFFSET"] = offset
    if fail_at is not None:
        env["FAKE_NVME_FAIL_AT"] = str(fail_at)
    if fail_once:
        env["FAKE_NVME_FAIL_ONCE"] = "1"
    # The script tees everything (including stderr) into pull.log, so callers
    # should assert against stdout + stderr combined.
    return subprocess.run(
        ["bash", SCRIPT, *args],
        env=env,
        capture_output=True,
        text=True,
        cwd=str(tmp_path),
    )


@pytest.fixture
def fakedev(tmp_path):
    dev = tmp_path / "nvmefake"
    dev.write_bytes(b"")
    return dev


def test_dry_run_emits_only_read_commands(tmp_path):
    r = run(
        tmp_path,
        tmp_path,
        "--dry-run",
        "--section",
        "all",
        "--outdir",
        str(tmp_path / "out"),
        "/dev/nvmeX",
    )
    assert r.returncode == 0, r.stderr
    cmds = [line for line in r.stdout.splitlines() if line.startswith("nvme ")]
    assert cmds, r.stdout
    for c in cmds:
        assert "--opcode=0xC6" in c, c
        assert "--namespace-id=0" in c, c
        assert c.rstrip().endswith("-r") or " -r " in c, c
        for bad in ("0xff", "0xFF", "0xdd", "0xDD", "0x0503", "0x0603"):
            assert bad not in c, "script must never emit %s: %s" % (bad, c)


def test_dry_run_uses_documented_cdw12_values(tmp_path):
    r = run(
        tmp_path,
        tmp_path,
        "--dry-run",
        "--section",
        "all",
        "--outdir",
        str(tmp_path / "out"),
        "/dev/nvmeX",
    )
    out = r.stdout
    for expect in ("--cdw12=0x0320", "--cdw12=0x0520", "--cdw12=0x0120"):
        assert expect in out, expect


def test_chunked_pull_reassembles_exactly(tmp_path, fakedev):
    drive = make_drive(tmp_path)
    out = tmp_path / "out"
    r = run(
        tmp_path,
        drive,
        "--section",
        "crash",
        "--chunk-size",
        "65536",
        "--outdir",
        str(out),
        str(fakedev),
    )
    assert r.returncode == 0, r.stdout + r.stderr
    got = (out / "crash.bin").read_bytes()
    want = (drive / "crash.img").read_bytes()
    assert got == want
    assert "chunk diversity" in r.stdout


def test_single_shot_matches_chunked(tmp_path, fakedev):
    drive = make_drive(tmp_path)
    a, b = tmp_path / "a", tmp_path / "b"
    assert (
        run(
            tmp_path,
            drive,
            "--section",
            "crash",
            "--chunk-size",
            "16384",
            "--outdir",
            str(a),
            str(fakedev),
        ).returncode
        == 0
    )
    assert (
        run(
            tmp_path,
            drive,
            "--section",
            "crash",
            "--single-shot",
            "--outdir",
            str(b),
            str(fakedev),
        ).returncode
        == 0
    )
    assert (a / "crash.bin").read_bytes() == (b / "crash.bin").read_bytes()


def test_offset_probe_rejects_a_drive_that_ignores_the_offset(tmp_path, fakedev):
    """The failure this guard exists for: without it you get a file that is
    chunk 0 repeated N times and looks superficially fine."""
    drive = make_drive(tmp_path)
    out = tmp_path / "out"
    r = run(
        tmp_path,
        drive,
        "--section",
        "crash",
        "--chunk-size",
        "16384",
        "--outdir",
        str(out),
        str(fakedev),
        offset="ignored",
    )
    assert r.returncode == 2, r.stdout + r.stderr
    assert "IS BEING IGNORED" in r.stdout + r.stderr
    assert not (out / "crash.bin").exists()


def test_offset_cdw11_mode(tmp_path, fakedev):
    drive = make_drive(tmp_path)
    out = tmp_path / "out"
    r = run(
        tmp_path,
        drive,
        "--section",
        "crash",
        "--chunk-size",
        "16384",
        "--offset-cdw",
        "11",
        "--outdir",
        str(out),
        str(fakedev),
        offset="cdw11",
    )
    assert r.returncode == 0, r.stdout + r.stderr
    assert (out / "crash.bin").read_bytes() == (drive / "crash.img").read_bytes()


def test_resume_after_a_mid_transfer_failure(tmp_path, fakedev):
    drive = make_drive(tmp_path)
    out = tmp_path / "out"
    args = (
        "--section",
        "crash",
        "--chunk-size",
        "16384",
        "--outdir",
        str(out),
        str(fakedev),
    )
    r1 = run(tmp_path, drive, *args, fail_at=16384 * 4, fail_once=True)
    assert r1.returncode == 3
    assert not (out / "crash.bin").exists()
    r2 = run(tmp_path, drive, *args, fail_at=16384 * 4, fail_once=True)
    assert r2.returncode == 0, r2.stdout + r2.stderr
    assert "resumed" in r2.stdout
    assert (out / "crash.bin").read_bytes() == (drive / "crash.img").read_bytes()


def test_empty_section_is_not_an_error(tmp_path, fakedev):
    drive = make_drive(tmp_path)
    out = tmp_path / "out"
    r = run(tmp_path, drive, "--section", "pfail", "--outdir", str(out), str(fakedev))
    assert r.returncode == 0, r.stdout + r.stderr
    assert "empty" in r.stdout


def test_strtbl_size_comes_from_the_second_dword(tmp_path, fakedev):
    """0x0120 returns drvlog size in dword[0] and string-table size in
    dword[1]; getting this backwards truncates or overruns the table."""
    drive = make_drive(
        tmp_path, sizes={"crash": 4096, "pfail": 0, "strtbl": 12288, "drvlog": 4096}
    )
    out = tmp_path / "out"
    r = run(
        tmp_path,
        drive,
        "--section",
        "strtbl",
        "--chunk-size",
        "4096",
        "--outdir",
        str(out),
        str(fakedev),
    )
    assert r.returncode == 0, r.stdout + r.stderr
    assert len((out / "strtbl.bin").read_bytes()) == 12288
