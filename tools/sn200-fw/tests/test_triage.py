"""Tests for sn200-triage.sh.

The script's whole job is to be trustworthy on a drive that is one wrong
command away from unrecoverable, so the tests care about two things: that the
verdict follows the evidence, and that no destructive opcode can escape.
"""

import os
import shutil
import subprocess
import sys

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
TOOLS = os.path.dirname(HERE)
SCRIPT = os.path.join(TOOLS, "sn200-triage.sh")

pytestmark = pytest.mark.skipif(shutil.which("bash") is None, reason="needs bash")


def sysfs(tmp_path, model="HUSMR7676BDP3Y1", state=None):
    """Build a fake /sys/class/nvme. The model file is what gates the vendor
    opcodes, so most tests need one."""
    d = tmp_path / "sysfs" / "nvme7"
    d.mkdir(parents=True, exist_ok=True)
    (d / "model").write_text(model + "\n")
    if state:
        (d / "state").write_text(state + "\n")
    return ["--sysfs", str(tmp_path / "sysfs")]


def run(tmp_path, args=None, **env_over):
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    shim = bindir / "nvme"
    shim.write_text(
        '#!/bin/sh\nexec %s %s "$@"\n'
        % (sys.executable, os.path.join(HERE, "fake_nvme_triage.py"))
    )
    shim.chmod(0o755)

    dev = tmp_path / "nvme7"
    dev.write_bytes(b"")

    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    for k, v in env_over.items():
        env["FAKE_TRIAGE_" + k.upper()] = v
    args = list(args or [])
    if "--sysfs" not in args:
        args = sysfs(tmp_path) + args
    return subprocess.run(
        ["bash", SCRIPT] + args + [str(dev)],
        env=env,
        capture_output=True,
        text=True,
    )


def test_latched_with_data_refuses_to_recommend_the_destructive_clear(tmp_path):
    r = run(tmp_path, mode="6", unvmcap="0")
    out = r.stdout
    assert "THE DATA IS THERE" in out
    assert "LATCHED and THE DATA IS STILL THERE" in out
    # the one command that has ever destroyed data here must be named as a
    # prohibition, never as a suggestion
    assert "DO NOT send 0xFF cdw12=0x0503" in out
    # and the third-party tools that fire it behind your back
    assert "get-crash-dump" in out
    assert "dm-cli" in out


def test_healthy_drive_says_so_and_does_not_mention_recovery(tmp_path):
    (tmp_path / "nvme7n1").write_bytes(b"")
    r = run(tmp_path, mode="1")
    assert "Drive is HEALTHY" in r.stdout
    assert "0x0503" not in r.stdout


def test_trap_and_shutdown_headers_are_distinguished(tmp_path):
    """The whole point of reading the header: 0x00020100+UNEXSTRT is a shutdown
    stub, 0x00020200 is a genuine firmware trap. They imply different
    mitigations, so conflating them would be worse than not looking."""
    r = run(tmp_path, version="0x00020100", tag="UNEXSTRT")
    assert "unfinished shutdown" in r.stdout.lower()
    assert "UNEXSTRT" in r.stdout

    r = run(tmp_path, version="0x00020200", tag="")
    assert "GENUINE FIRMWARE TRAP" in r.stdout
    assert "PROC9" in r.stdout


def test_unrecognised_header_refuses_to_pick_a_side(tmp_path):
    r = run(tmp_path, version="0xdeadbeef")
    assert "unrecognised" in r.stdout
    assert "do not assume" in r.stdout


def test_unreadable_header_is_not_silently_treated_as_zero(tmp_path):
    r = run(tmp_path, nohdr="1")
    assert "could not read the crash section header" in r.stdout
    assert "unfinished shutdown" not in r.stdout.lower()


def test_unarmed_sections_are_reported_not_crashed_on(tmp_path):
    r = run(tmp_path, armed="pfail")
    assert "section not armed" in r.stdout
    assert r.returncode == 0


def test_dump_writes_the_evidence_before_anything_destroys_it(tmp_path):
    out = tmp_path / "eviD"
    r = run(tmp_path, ["--dump", str(out)], version="0x00020200")
    blob = out / "crash-128k.bin"
    assert blob.exists(), r.stdout + r.stderr
    assert len(blob.read_bytes()) == 131072
    assert blob.read_bytes()[0:4] == (0x00020200).to_bytes(4, "little")
    # the 128 KiB ceiling is a host limitation, not the drive's -- if that is
    # not said, a partial dump reads as a complete one
    assert "HOST limit" in r.stdout


def test_no_destructive_opcode_can_be_issued(tmp_path):
    """fake_nvme_triage.py aborts on 0xCA, 0xDD and every 0xFF selector other
    than the read-only 0x0004, so a clean run across every branch is the
    assertion. Belt and braces: grep the invocations too."""
    for kw in ({}, {"mode": "1"}, {"armed": ""}, {"nohdr": "1"}):
        r = run(tmp_path, **kw)
        assert r.returncode == 0, r.stdout + r.stderr
        assert "must never be issued" not in r.stderr

    # join backslash continuations first -- an invocation split over two lines
    # would otherwise hide its CDW12 from this check
    src = open(SCRIPT).read().replace("\\\n", " ")
    # The script PRINTS a 0x0603 recommendation for the data-preserving case, so
    # echo/printf lines are advice, not invocations. Excluding them is a loophole
    # unless we also assert that no echoed command is ever executed -- which the
    # fake_nvme_triage.py run above already guarantees, since it aborts on any
    # destructive opcode actually reaching it across every branch.
    invocations = [
        ln
        for ln in src.splitlines()
        if "nvme admin-passthru" in ln
        and not ln.lstrip().startswith("#")
        and not ln.lstrip().startswith("echo")
        and not ln.lstrip().startswith("printf")
    ]
    assert invocations
    for ln in invocations:
        low = ln.lower()
        for bad in ("0xca", "0xdd", "0x0503", "0x0603", "0x0403", "0x0303"):
            assert bad not in low, "%s must never appear in an invocation: %s" % (
                bad,
                ln,
            )
        if "0xff" in low:
            assert "cdw12=0x0004" in low, "the only allowed 0xFF selector: %s" % ln


def test_unreadable_identify_is_never_reported_as_data_loss(tmp_path):
    """The failure seen on real hardware: a reset-looping controller answers
    nothing, so Identify fails. Reading that as "capacity unallocated" would
    tell an operator their data is gone on no evidence at all -- the single
    worst thing this script could do."""
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    # every command fails with EAGAIN, exactly like a controller in `resetting`
    (bindir / "nvme").write_text(
        "#!/bin/sh\necho '/dev/nvme7: Resource temporarily unavailable' >&2\n"
        "echo 'Usage: nvme admin-passthru <device> [OPTIONS]' >&2\nexit 1\n"
    )
    (bindir / "nvme").chmod(0o755)
    dev = tmp_path / "nvme7"
    dev.write_bytes(b"")
    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    r = subprocess.run(
        ["bash", SCRIPT] + sysfs(tmp_path) + [str(dev)],
        env=env,
        capture_output=True,
        text=True,
    )
    out = r.stdout
    assert "COULD NOT READ" in out
    assert "says NOTHING about your data" in out
    assert "unallocated. The namespace is gone" not in out
    assert "Data may be gone" not in out
    # and it must not dump nvme-cli's usage text over the report
    assert "Usage: nvme" not in out
    assert "Resource temporarily unavailable" in out, "the real error must survive"


def test_ansi_bolded_usage_text_is_still_stripped(tmp_path):
    """nvme-cli 2.13 emits "\\e[1mUsage:" -- a plain ^Usage: anchor never
    matches it, which is exactly how the noise reached the report on real
    hardware."""
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    (bindir / "nvme").write_text(
        "#!/bin/sh\n"
        "echo '/dev/nvme7: Resource temporarily unavailable' >&2\n"
        "printf '\\033[1mUsage: nvme admin-passthru <device>\\033[0m\\n' >&2\n"
        "echo 'passthrough, return results.' >&2\nexit 1\n"
    )
    (bindir / "nvme").chmod(0o755)
    dev = tmp_path / "nvme7"
    dev.write_bytes(b"")
    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    r = subprocess.run(
        ["bash", SCRIPT] + sysfs(tmp_path) + [str(dev)],
        env=env,
        capture_output=True,
        text=True,
    )
    assert "Usage:" not in r.stdout
    assert "return results" not in r.stdout


def test_unaskable_section_probe_is_not_reported_as_not_armed(tmp_path):
    """ "probe failed" means "not armed" only if the command actually reached the
    drive. Mid-reset it means nothing was asked."""
    r = run(tmp_path, sysfs(tmp_path, state="resetting"), armed="")
    assert "could not ask; controller is resetting" in r.stdout
    assert "section not armed" not in r.stdout


def test_reset_loop_is_named_and_says_do_not_retry(tmp_path):
    """`state: resetting` means no admin command can land. Reporting the
    resulting failures without that context reads as a broken drive; worse,
    the obvious reaction is to retry, which has wedged a node here before."""
    r = run(tmp_path, sysfs(tmp_path, state="resetting"))
    out = r.stdout
    assert "reset-looping" in out
    assert "EAGAIN" in out
    assert "Do NOT retry in a loop" in out
    assert "nvme-noreset" in out


def test_no_device_lists_and_explains_the_lookalike_failure(tmp_path):
    """A drive absent from lspci is UEFI0067 link training, not this bug. If the
    script does not say so, the operator burns hours on vendor commands against
    a cabling fault."""
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    (bindir / "nvme").write_text("#!/bin/sh\nexit 0\n")
    (bindir / "nvme").chmod(0o755)
    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    r = subprocess.run(["bash", SCRIPT], env=env, capture_output=True, text=True)
    assert r.returncode == 0
    assert "UEFI0067" in r.stdout or "none found" in r.stdout


def test_namespace_suffix_is_stripped_from_the_basename_only(tmp_path):
    """/dev/nvme7n1 -> /dev/nvme7, but a directory like .../run_n0/ in the path
    must not be eaten -- ${DEV%n[0-9]*} silently did exactly that."""
    d = tmp_path / "run_n0"
    d.mkdir()
    (d / "nvme7").write_bytes(b"")
    r = run(tmp_path, sysfs(tmp_path))
    assert "REFUSING" not in r.stdout
    # the controller path must keep the whole directory
    proc = subprocess.run(
        ["bash", SCRIPT, "--sysfs", str(tmp_path / "sysfs"), str(d / "nvme7n1")],
        env={**os.environ, "PATH": "%s:%s" % (tmp_path / "bin", os.environ["PATH"])},
        capture_output=True,
        text=True,
    )
    assert str(d / "nvme7") in proc.stdout
    assert "run_n0" in proc.stdout


def test_pfcl_only_latch_is_flagged_as_the_data_preserving_case(tmp_path):
    """CLOG not armed + PFCL armed is the one case 0x0603 can lift without
    touching the L2P (0x0603's resume handler has no path to the re-init verb).
    Missing it means an operator either loses data unnecessarily or leaves a
    recoverable drive powered off forever."""
    r = run(tmp_path, armed="pfail")
    out = r.stdout
    assert "RARE" in out
    assert "cdw12=0x0603" in out
    assert "NEVER 0x0503" in out
    # it must not be oversold -- nobody has run this
    assert "nobody has run it yet" in out


def test_both_sections_armed_still_says_stop(tmp_path):
    """UNEXSTRT stamps CLOG, so the ordinary power-event latch does NOT qualify
    for the 0x0603 branch. Offering it there would destroy data."""
    r = run(tmp_path, armed="clog,pfail")
    assert "RARE" not in r.stdout
    assert "DO NOT send 0xFF cdw12=0x0503" in r.stdout


def test_the_0603_branch_never_suggests_0503(tmp_path):
    """0x0503 is one nibble away and is the entire data cost."""
    r = run(tmp_path, armed="pfail")
    body = r.stdout.split("VERDICT")[1]
    assert "--cdw12=0x0503" not in body
