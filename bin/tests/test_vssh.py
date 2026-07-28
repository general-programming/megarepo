"""Tests for bin/vssh.

`bao` and `ssh` are stubbed out on PATH so the whole flow runs offline: the
stub signer emits a real certificate (generated once per session with a
throwaway CA) and the stub ssh records the argv it was handed.
"""

from __future__ import annotations

import json
import os
import subprocess
import textwrap
from pathlib import Path

import pytest

VSSH = Path(__file__).resolve().parent.parent / "vssh"


@pytest.fixture(scope="session")
def ca_key(tmp_path_factory: pytest.TempPathFactory) -> Path:
    """A throwaway SSH CA used to sign certs in the stub signer."""
    d = tmp_path_factory.mktemp("ca")
    key = d / "ca"
    subprocess.run(
        ["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", str(key)],
        check=True,
    )
    return key


@pytest.fixture
def env(tmp_path: Path, ca_key: Path) -> dict[str, str]:
    """PATH-stubbed environment with a fresh cert cache per test."""
    stub_dir = tmp_path / "stubs"
    stub_dir.mkdir()
    log = tmp_path / "ssh-argv.json"
    ttl_file = tmp_path / "ttl"
    ttl_file.write_text("+30m")

    bao = stub_dir / "bao"
    bao.write_text(
        textwrap.dedent(f"""\
        #!/usr/bin/env bash
        # Mimic: bao write -field=signed_key <mount>/sign/<role> public_key=@F valid_principals=P
        set -euo pipefail
        for arg in "$@"; do
          case "$arg" in
            public_key=@*) pub="${{arg#public_key=@}}" ;;
            valid_principals=*) principals="${{arg#valid_principals=}}" ;;
          esac
        done
        [ -n "${{VSSH_TEST_BAO_FAIL:-}}" ] && {{ echo "permission denied" >&2; exit 1; }}
        [ -n "${{VSSH_TEST_BAO_GARBAGE:-}}" ] && {{ echo "not-a-certificate"; exit 0; }}
        work="$(mktemp -d)"
        cp "$pub" "$work/k.pub"
        ssh-keygen -q -s {ca_key} -I vssh-test -n "$principals" \\
            -V "$(cat {ttl_file})" "$work/k.pub"
        cat "$work/k-cert.pub"
        rm -rf "$work"
        """)
    )
    bao.chmod(0o755)

    ssh = stub_dir / "ssh"
    ssh.write_text(
        textwrap.dedent(f"""\
        #!/usr/bin/env python3
        import json, sys
        json.dump(sys.argv[1:], open({str(log)!r}, "w"))
        """)
    )
    ssh.chmod(0o755)

    e = dict(os.environ)
    e["PATH"] = f"{stub_dir}:{e['PATH']}"
    e["XDG_RUNTIME_DIR"] = str(tmp_path / "run")
    (tmp_path / "run").mkdir()
    e["BAO_ADDR"] = "http://vault.invalid:8200"
    e["BAO_TOKEN"] = "test-token"
    e["_LOG"] = str(log)
    e["_TTL"] = str(ttl_file)
    return e


def run(env: dict[str, str], *args: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run([str(VSSH), *args], env=env, capture_output=True, text=True)


def ssh_argv(env: dict[str, str]) -> list[str]:
    return json.loads(Path(env["_LOG"]).read_text())


def cache_dir(env: dict[str, str]) -> Path:
    return Path(env["XDG_RUNTIME_DIR"]) / f"vssh-{os.getuid()}"


def test_no_args_is_a_usage_error(env: dict[str, str]) -> None:
    r = run(env)
    assert r.returncode != 0
    assert "usage:" in r.stderr


def test_help_does_not_need_vault(env: dict[str, str]) -> None:
    e = dict(env)
    del e["BAO_ADDR"]
    del e["BAO_TOKEN"]
    r = run(e, "--help")
    assert r.returncode == 0
    assert "vssh" in r.stdout


def test_status_without_a_cert_fails(env: dict[str, str]) -> None:
    r = run(env, "--status")
    assert r.returncode == 1
    assert "no valid cached certificate" in r.stdout


def cert_principals(env: dict[str, str]) -> str:
    cert = (cache_dir(env) / "id_ed25519-cert.pub").read_text()
    return subprocess.run(
        ["ssh-keygen", "-L", "-f", "/dev/stdin"],
        input=cert,
        capture_output=True,
        text=True,
        check=True,
    ).stdout


def test_bare_host_logs_in_as_root(env: dict[str, str]) -> None:
    # NixOS hosts have no admin account; you log in as root and root's
    # AuthorizedPrincipalsFile accepts the admin principal.
    r = run(env, "somehost")
    assert r.returncode == 0, r.stderr
    assert "root@somehost" in ssh_argv(env)


def test_login_user_does_not_become_the_principal(env: dict[str, str]) -> None:
    # The regression that made vssh unusable: asking the CA for a `root`
    # principal (which it refuses) just because the login user is root.
    r = run(env, "root@somehost")
    assert r.returncode == 0, r.stderr
    assert "root@somehost" in ssh_argv(env)
    listed = cert_principals(env)
    assert "admin" in listed
    assert "root" not in listed.split("Principals:")[1]


def test_explicit_user_is_login_only(env: dict[str, str]) -> None:
    r = run(env, "localadmin@somehost")
    assert r.returncode == 0, r.stderr
    assert "localadmin@somehost" in ssh_argv(env)
    # Salt hosts log in as localadmin but the cert still says admin.
    assert "admin" in cert_principals(env)


def test_vssh_login_env_sets_the_default_user(env: dict[str, str]) -> None:
    env["VSSH_LOGIN"] = "localadmin"
    r = run(env, "somehost")
    assert r.returncode == 0, r.stderr
    assert "localadmin@somehost" in ssh_argv(env)


def test_ssh_is_pinned_to_the_minted_identity(env: dict[str, str]) -> None:
    run(env, "somehost")
    argv = ssh_argv(env)
    # Must not fall back to the forwarded agent — that is the whole point.
    assert "IdentitiesOnly=yes" in argv
    assert "IdentityAgent=none" in argv
    assert any(a.startswith("CertificateFile=") for a in argv)


def test_extra_args_are_passed_through(env: dict[str, str]) -> None:
    run(env, "somehost", "-o", "ConnectTimeout=3", "uptime")
    argv = ssh_argv(env)
    assert argv[-3:] == ["-o", "ConnectTimeout=3", "uptime"]


def test_valid_cert_is_reused(env: dict[str, str]) -> None:
    run(env, "somehost")
    first = (cache_dir(env) / "id_ed25519-cert.pub").read_text()
    run(env, "somehost")
    assert (cache_dir(env) / "id_ed25519-cert.pub").read_text() == first


def test_cert_near_expiry_is_reminted(env: dict[str, str]) -> None:
    # Inside the 5 minute renew margin, so the next call must re-sign.
    Path(env["_TTL"]).write_text("+2m")
    run(env, "somehost")
    first = (cache_dir(env) / "id_ed25519-cert.pub").read_text()
    run(env, "somehost")
    assert (cache_dir(env) / "id_ed25519-cert.pub").read_text() != first


def test_principal_change_forces_a_new_cert(env: dict[str, str]) -> None:
    run(env, "somehost")
    first = (cache_dir(env) / "id_ed25519-cert.pub").read_text()
    env["VSSH_PRINCIPAL"] = "someone-else"
    run(env, "somehost")
    assert (cache_dir(env) / "id_ed25519-cert.pub").read_text() != first


def test_different_login_users_share_one_cert(env: dict[str, str]) -> None:
    # The principal is the same either way, so re-signing would be wasteful.
    run(env, "root@somehost")
    first = (cache_dir(env) / "id_ed25519-cert.pub").read_text()
    run(env, "localadmin@somehost")
    assert (cache_dir(env) / "id_ed25519-cert.pub").read_text() == first


def test_signing_failure_is_reported(env: dict[str, str]) -> None:
    env["VSSH_TEST_BAO_FAIL"] = "1"
    r = run(env, "somehost")
    assert r.returncode != 0
    assert "signing failed" in r.stderr


def test_non_certificate_response_is_rejected(env: dict[str, str]) -> None:
    env["VSSH_TEST_BAO_GARBAGE"] = "1"
    r = run(env, "somehost")
    assert r.returncode != 0
    assert "unexpected response" in r.stderr


def test_missing_token_is_reported(env: dict[str, str]) -> None:
    e = dict(env)
    del e["BAO_TOKEN"]
    e["HOME"] = str(Path(e["XDG_RUNTIME_DIR"]).parent / "emptyhome")
    Path(e["HOME"]).mkdir(exist_ok=True)
    r = run(e, "somehost")
    assert r.returncode != 0
    assert "no token" in r.stderr


def test_cache_dir_is_private(env: dict[str, str]) -> None:
    run(env, "somehost")
    assert (cache_dir(env).stat().st_mode & 0o777) == 0o700


def test_logout_clears_the_cache(env: dict[str, str]) -> None:
    run(env, "somehost")
    assert cache_dir(env).exists()
    r = run(env, "--logout")
    assert r.returncode == 0
    assert not cache_dir(env).exists()
