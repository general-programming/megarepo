"""End-to-end tests for fill-fw-slots.sh against a fake SN200.

No hardware. `tests/fake_nvme_fw.py` stands in for the `nvme` binary and keeps
per-slot state in a JSON file, so the real script's frmw decode, slot selection,
per-slot commit and between-step verification all run for real.

The load-bearing assertions are the safety ones, mirroring
test_pull_crash_dump.py's "must never emit 0xFF":

  * the script can only ever emit `--action=0`
  * it can never emit `--slot=0` or `--slot=1`
  * it refuses any image containing an SBLPATCH.bin member
  * it never emits a reset of any kind
"""

import json
import os
import shutil
import subprocess
import sys
import tarfile

import pytest

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
from fake_nvme_fw import DEFAULT  # noqa: E402

TOOLS = os.path.dirname(HERE)
SCRIPT = os.path.join(TOOLS, "fill-fw-slots.sh")

# The real KNGND122.bin, verified locally.
REAL_SHA = "b11298346020af0f3a859e5a0d849c464eed186c9a102cf8956b3f6c44db3e70"
REAL_SIZE = 1762048

pytestmark = pytest.mark.skipif(
    shutil.which("sha256sum") is None and shutil.which("shasum") is None,
    reason="needs sha256sum or shasum",
)


def make_bundle(path, rev="KNGND122", size=REAL_SIZE, sblpatch=False):
    """A structurally faithful stand-in for a firmware bundle: ustar members
    with FWHEADER.bin first (its data at byte 512), then the 512-byte
    end-of-archive block and a 256-byte trailer, padded to `size`."""
    members = [
        ("FWHEADER.bin", rev.encode().ljust(12, b"\0") + b"\x01\0\0\0" + b"\0" * 48)
    ]
    members += [("PROC%d.bin" % i, b"\xa5" * 1024) for i in range(4)]
    members += [("SECURITY.bin", b"\x01" + b"\0" * 1599)]
    if sblpatch:
        members.append(("SBLPATCH.bin", b"\x5a" * 2048))

    raw = path + ".tar"
    with tarfile.open(raw, "w") as t:
        for name, data in members:
            ti = tarfile.TarInfo(name)
            ti.size = len(data)
            import io

            t.addfile(ti, io.BytesIO(data))
    blob = bytearray(open(raw, "rb").read())
    os.unlink(raw)
    # tarfile writes two zero blocks plus padding; trim to one, then trailer.
    while len(blob) >= 1024 and blob[-1024:] == b"\0" * 1024:
        del blob[-512:]
    blob += b"\x7f" * 256
    if size is not None:
        if len(blob) > size:
            del blob[size:]
        else:
            blob += b"\0" * (size - len(blob))
    with open(path, "wb") as fh:
        fh.write(bytes(blob))
    return path


def run(tmp_path, *args, state=None, image=None, skip_sha=True):
    bindir = tmp_path / "bin"
    bindir.mkdir(exist_ok=True)
    shim = bindir / "nvme"
    shim.write_text(
        '#!/bin/sh\nexec %s %s "$@"\n'
        % (sys.executable, os.path.join(HERE, "fake_nvme_fw.py"))
    )
    shim.chmod(0o755)

    env = dict(os.environ)
    env["PATH"] = "%s:%s" % (bindir, env["PATH"])
    env["FAKE_FW_STATE"] = str(state or (tmp_path / "drive.json"))

    argv = ["bash", SCRIPT]
    if image:
        argv += ["--image", str(image)]
    if skip_sha:
        argv += ["--skip-sha"]
    argv += list(args)
    return subprocess.run(
        argv, env=env, capture_output=True, text=True, cwd=str(tmp_path)
    )


@pytest.fixture
def fakedev(tmp_path):
    dev = tmp_path / "nvmefake"
    dev.write_bytes(b"")
    return dev


@pytest.fixture
def image(tmp_path):
    return make_bundle(str(tmp_path / "KNGND122.bin"))


def state_of(tmp_path):
    return json.loads((tmp_path / "drive.json").read_text())


def drive_state(tmp_path, **over):
    """Pre-seed the fake drive with a non-default starting state."""
    st = json.loads(json.dumps(DEFAULT))
    st.update(over)
    (tmp_path / "drive.json").write_text(json.dumps(st))
    return st


# --------------------------------------------------------------------------
# safety invariants -- these are the reason this file exists
# --------------------------------------------------------------------------


def test_dry_run_emits_only_action_zero_commits(tmp_path, image, fakedev):
    r = run(tmp_path, "--dry-run", str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    commits = [c for c in r.stdout.splitlines() if "fw-commit" in c]
    assert commits, r.stdout
    for c in commits:
        assert "--action=0" in c, c
        for bad in ("--action=1", "--action=2", "--action=3", "--slot=0", "--slot=1"):
            assert bad not in c, "script must never emit %s: %s" % (bad, c)


def test_dry_run_never_emits_a_reset(tmp_path, image, fakedev):
    r = run(tmp_path, "--dry-run", str(fakedev), image=image)
    out = r.stdout + r.stderr
    for bad in ("nvme reset", "subsystem-reset", "nvme format", "nvme sanitize"):
        assert bad not in out, bad


def test_refuses_the_sblpatch_image(tmp_path, fakedev):
    bad = make_bundle(str(tmp_path / "KNGND110.bin"), rev="KNGND110", sblpatch=True)
    r = run(tmp_path, "--dry-run", str(fakedev), image=bad)
    assert r.returncode == 2, r.stdout + r.stderr
    assert "SBLPATCH" in r.stdout + r.stderr


def test_refuses_slot_one(tmp_path, image, fakedev):
    r = run(tmp_path, "--slots", "1", str(fakedev), image=image)
    assert r.returncode == 1, r.stdout + r.stderr
    assert "read-only" in r.stdout + r.stderr


def test_refuses_slot_zero(tmp_path, image, fakedev):
    """FS=0 means 'the controller chooses the slot' and the firmware's range
    check (FS <= slot_count) accepts it."""
    r = run(tmp_path, "--slots", "0", str(fakedev), image=image)
    assert r.returncode == 1, r.stdout + r.stderr
    assert "controller-chooses" in r.stdout + r.stderr


def test_refuses_a_wrong_sha_without_skip(tmp_path, fakedev):
    img = make_bundle(str(tmp_path / "KNGND122.bin"))
    r = run(tmp_path, "--dry-run", str(fakedev), image=img, skip_sha=False)
    assert r.returncode == 2, r.stdout + r.stderr
    assert REAL_SHA in r.stdout + r.stderr


def test_accepts_the_real_image_size(tmp_path, image):
    assert os.path.getsize(image) == REAL_SIZE
    assert REAL_SIZE % 4 == 0


# --------------------------------------------------------------------------
# behaviour
# --------------------------------------------------------------------------


def test_fills_every_writable_non_active_slot(tmp_path, image, fakedev):
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    st = state_of(tmp_path)
    # slot 1 read-only, slot 2 active -> both untouched; 3,4,5 written.
    assert st["slots"]["1"] == "KNGND100"
    assert st["slots"]["2"] == "KNGND122"
    for s in ("3", "4", "5"):
        assert st["slots"][s] == "KNGND122", st["slots"]


def test_active_slot_is_skipped_by_default(tmp_path, image, fakedev):
    r = run(tmp_path, str(fakedev), image=image)
    assert "skipping slot 2" in r.stdout
    assert "slots to fill: 3 4 5" in r.stdout


def test_rewrite_active_includes_the_active_slot(tmp_path, image, fakedev):
    r = run(tmp_path, "--rewrite-active", str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    assert "slots to fill: 2 3 4 5" in r.stdout


def test_afi_never_moves(tmp_path, image, fakedev):
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    assert state_of(tmp_path)["afi"] == DEFAULT["afi"]


def test_slot_count_comes_from_frmw(tmp_path, image, fakedev):
    """frmw 0x07 -> 3 slots, slot 1 writable. The script must still refuse
    slot 1 (policy) but must only offer 2..3."""
    drive_state(tmp_path, frmw=0x07)
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    assert "slots  : 3" in r.stdout
    assert "slots to fill: 3" in r.stdout


def test_refuses_a_slot_beyond_the_drives_count(tmp_path, image, fakedev):
    r = run(tmp_path, "--slots", "6", str(fakedev), image=image)
    assert r.returncode == 1, r.stdout + r.stderr
    assert "beyond the drive" in r.stdout + r.stderr


def test_refuses_a_pending_activation(tmp_path, image, fakedev):
    drive_state(tmp_path, afi=2 | (3 << 4))
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 3, r.stdout + r.stderr
    assert "pending" in r.stdout + r.stderr


def test_refuses_a_foreign_oem_branch(tmp_path, image, fakedev):
    drive_state(tmp_path, fr="KNCCD122")
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 3, r.stdout + r.stderr
    assert "KNGN" in r.stdout + r.stderr


def test_redownloads_before_every_commit(tmp_path, image, fakedev):
    """The download buffer is not guaranteed to survive a commit; the fake
    drive clears `staged` on every replacing commit, so a script that
    downloaded only once would fail the second slot with Invalid Image."""
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    assert state_of(tmp_path)["slots"]["5"] == "KNGND122"


def test_final_transfer_is_a_short_chunk(tmp_path, image, fakedev):
    """1762048 % 4096 == 768: the drive must accept a partial final page, which
    is exactly what WD's own by-page fallback emits."""
    r = run(tmp_path, str(fakedev), image=image)
    assert r.returncode == 0, r.stdout + r.stderr
    st = state_of(tmp_path)
    assert st["last_xfer"] == 4096
    assert st["last_tail"] == 768


def test_rejects_a_non_dword_multiple_image(tmp_path, fakedev):
    img = make_bundle(str(tmp_path / "KNGND122.bin"), size=REAL_SIZE + 1)
    r = run(tmp_path, "--dry-run", str(fakedev), image=img)
    assert r.returncode == 2, r.stdout + r.stderr
    assert "multiple of 4" in r.stdout + r.stderr


def test_bad_xfer_is_refused(tmp_path, image, fakedev):
    r = run(tmp_path, "--xfer", "1000", "--dry-run", str(fakedev), image=image)
    assert r.returncode == 1, r.stdout + r.stderr


# --------------------------------------------------------------------------
# against the genuine bundle, when it is available locally
# --------------------------------------------------------------------------

REAL_ZIP = os.path.expanduser(
    os.environ.get("SN200_FW_ZIP", "~/Downloads/HGST-UltraStar-SN200-HHHL.zip")
)


def extract_real(tmp_path, name):
    import zipfile

    with zipfile.ZipFile(REAL_ZIP) as z:
        member = "HGST-UltraStar-SN200-HHHL/firmwares/%s" % name
        out = tmp_path / name
        out.write_bytes(z.read(member))
    return out


needs_zip = pytest.mark.skipif(
    not os.path.exists(REAL_ZIP), reason="firmware zip not present locally"
)


@needs_zip
def test_real_kngnd122_passes_every_image_check(tmp_path, fakedev):
    """No --skip-sha: size, sha256, dword alignment, FWHEADER presence and the
    SBLPATCH refusal all run against the genuine bundle."""
    img = extract_real(tmp_path, "KNGND122.bin")
    r = run(tmp_path, "--dry-run", str(fakedev), image=img, skip_sha=False)
    assert r.returncode == 0, r.stdout + r.stderr
    assert REAL_SHA in r.stdout
    assert "20 members, no SBLPATCH" in r.stdout


@needs_zip
def test_real_kngnd110_is_the_sblpatch_image_and_is_refused(tmp_path, fakedev):
    """`firmwares/KNGND110.bin` is byte-identical to KNGND110+sblpatch+k.bin.
    The innocuous filename is the trap; the extra tar member is the tell."""
    img = extract_real(tmp_path, "KNGND110.bin")
    assert tarfile.open(img).getnames()[-1] == "SBLPATCH.bin"
    r = run(tmp_path, "--dry-run", str(fakedev), image=img, skip_sha=True)
    assert r.returncode == 2, r.stdout + r.stderr
    assert "SBLPATCH" in r.stdout + r.stderr


@needs_zip
def test_real_bundle_member_crcs_are_crc32_mpeg2(tmp_path):
    """Bytes 508-511 of every ustar header hold a little-endian CRC-32/MPEG-2 of
    that member's data. Standard tar leaves them zero; the drive parses this."""
    import struct

    img = extract_real(tmp_path, "KNGND122.bin")
    blob = img.read_bytes()

    def crc32_mpeg2(buf):
        c = 0xFFFFFFFF
        for b in buf:
            c ^= b << 24
            for _ in range(8):
                c = (
                    ((c << 1) ^ 0x04C11DB7) & 0xFFFFFFFF
                    if c & 0x80000000
                    else (c << 1) & 0xFFFFFFFF
                )
        return c

    with tarfile.open(img) as t:
        members = t.getmembers()
    assert len(members) == 20
    for m in members:
        stored = struct.unpack_from("<I", blob, m.offset + 508)[0]
        want = crc32_mpeg2(blob[m.offset_data : m.offset_data + m.size])
        assert stored == want, m.name


@needs_zip
def test_real_bundle_ends_with_one_zero_block_and_a_256_byte_trailer(tmp_path):
    """Layout the drive is fed: members, ONE end-of-archive block, then a
    256-byte trailer. Padding the file would move the trailer off EOF."""
    img = extract_real(tmp_path, "KNGND122.bin")
    blob = img.read_bytes()
    assert len(blob) == REAL_SIZE
    with tarfile.open(img) as t:
        last = t.getmembers()[-1]
    end = last.offset_data + ((last.size + 511) // 512) * 512
    assert len(blob) - end == 768
    assert blob[end : end + 512] == b"\0" * 512
    assert blob[end + 512 :] != b"\0" * 256
    # 1762048 % 4096 == 768: the final fw-download page is a short transfer.
    assert REAL_SIZE % 4096 == 768
    assert REAL_SIZE % 4 == 0
