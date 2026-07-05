"""LinuxBirdHost file ownership: parsing, diffing, and deploying."""

import pytest

from barf.vendors.linux import (
    LinuxBirdHost,
    parse_rendered_files,
    redact_private_keys,
)

WG_PATH = "/etc/wireguard/wg51070.conf"
IFUP_PATH = "/etc/network/interfaces.d/wg51070.conf"
BIRD_PATH = "/etc/bird/bird.conf"
STALE_WG = "/etc/wireguard/wg51825.conf"
STALE_IFUP = "/etc/network/interfaces.d/wg51825.conf"


def rendered_script(wg="PrivateKey = SECRET\n", bird="router id 1.2.3.4;\n") -> str:
    blocks = {
        WG_PATH: wg,
        IFUP_PATH: "iface wg51070 inet manual\n",
        BIRD_PATH: bird,
    }
    script = "#!/bin/sh\n"
    for path, content in blocks.items():
        script += f"cat << 'BARF_FILE' > {path}\n{content}BARF_FILE\n"
    return script


class FakeSSH:
    """Canned DeviceSSH: remote file contents in, executed commands out."""

    def __init__(self, remote_files: dict):
        self.remote_files = remote_files
        self.commands: list = []
        self.written: dict = {}
        self.bird_active = True

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        pass

    def run(self, command: str, timeout: int = 60):
        self.commands.append(command)
        if command.startswith("cat "):
            path = command.split()[1].strip("'")
            content = self.remote_files.get(path)
            return (0, content) if content is not None else (1, "")
        if command.startswith("ls -1 "):
            listed = [p for p in self.remote_files if "/wg" in p]
            return (0, "\n".join(sorted(listed)))
        if command.startswith("systemctl is-active"):
            return (0 if self.bird_active else 3), ""
        if command.startswith("systemctl restart bird"):
            self.bird_active = True
            return 0, ""
        if command.startswith("birdc configure"):
            return 0, "Reconfigured"
        return 0, ""

    def write_file(self, remote: str, content: str, mode: int = 0o644):
        self.written[remote] = (content, mode)


@pytest.fixture
def leaf(monkeypatch):
    host = LinuxBirdHost(hostname="leaf-x", role="vpn", asn=65010)
    ssh = FakeSSH({})
    monkeypatch.setattr(LinuxBirdHost, "_open_ssh", lambda self: ssh)
    return host, ssh


def test_parse_rendered_files_round_trip():
    files = parse_rendered_files(rendered_script())
    assert files == {
        WG_PATH: "PrivateKey = SECRET\n",
        IFUP_PATH: "iface wg51070 inet manual\n",
        BIRD_PATH: "router id 1.2.3.4;\n",
    }


def test_parse_rendered_files_rejects_non_linux_config():
    with pytest.raises(ValueError):
        parse_rendered_files("set protocols bgp system-as 65000")


def test_redact_private_keys():
    redacted = redact_private_keys("PrivateKey = SECRET\n+PrivateKey = SECRET2\n")
    assert "SECRET" not in redacted
    assert redacted.count("<redacted>") == 2


def test_diff_no_changes(leaf):
    host, ssh = leaf
    ssh.remote_files.update(parse_rendered_files(rendered_script()))
    diff = host.diff_config(rendered_script())
    assert not diff.has_changes
    assert diff.summary == "no changes"


def test_diff_counts_and_redacts(leaf):
    host, ssh = leaf
    # wg conf differs, ifupdown matches, bird.conf missing, stale file present.
    ssh.remote_files.update(
        {
            WG_PATH: "PrivateKey = OLD\n",
            IFUP_PATH: "iface wg51070 inet manual\n",
            STALE_WG: "PrivateKey = ROTATED-AWAY\n",
        }
    )
    diff = host.diff_config(rendered_script())
    assert diff.has_changes
    assert diff.summary == "+1 new, ~1 changed, -1 stale"
    assert STALE_WG in diff.text
    assert "SECRET" not in diff.text
    assert "OLD" not in diff.text
    assert "<redacted>" in diff.text


def test_diff_show_secrets(leaf):
    host, ssh = leaf
    diff = host.diff_config(rendered_script(), redact=False)
    assert "PrivateKey = SECRET" in diff.text


def test_push_writes_applies_and_cleans(leaf):
    host, ssh = leaf
    ssh.remote_files.update(
        {
            # Existing interface with an outdated key: syncconf, no bounce.
            WG_PATH: "PrivateKey = OLD\n",
            IFUP_PATH: "iface wg51070 inet manual\n",
            BIRD_PATH: "router id 1.2.3.4;\n",
            # Rotated-away link still on disk: downed and deleted.
            STALE_WG: "old",
            STALE_IFUP: "old",
        }
    )
    host.push_rendered_config(rendered_script())

    # wg conf written 0600; only the changed file is written.
    assert ssh.written[WG_PATH][1] == 0o600
    assert list(ssh.written) == [WG_PATH]
    joined = "\n".join(ssh.commands)
    assert f"wg syncconf wg51070 {WG_PATH}" in joined
    # Only the wg conf changed: applied live, never bounced.
    assert "ifup wg51070" not in joined
    assert "ifdown --force wg51070" not in joined
    assert "ifdown --force wg51825" in joined
    assert f"rm -f {STALE_WG}" in joined
    assert f"rm -f {STALE_IFUP}" in joined
    # bird files untouched: no reload.
    assert "birdc configure" not in joined


def test_push_bounces_changed_ifupdown(leaf):
    host, ssh = leaf
    ssh.remote_files.update(
        {
            WG_PATH: "PrivateKey = OLD\n",
            # Live link whose stanza changed (e.g. gained the
            # link-local post-up): hooks only run on a bounce.
            IFUP_PATH: "iface wg51070 inet static\n",
            BIRD_PATH: "router id 1.2.3.4;\n",
        }
    )
    host.push_rendered_config(rendered_script())
    joined = "\n".join(ssh.commands)
    assert "ifdown --force wg51070" in joined
    assert "ifup wg51070" in joined
    # The bounce re-runs wg setconf; no separate syncconf.
    assert "syncconf" not in joined


def test_push_brings_up_brand_new_links(leaf):
    host, ssh = leaf
    # Nothing on the device yet: every file is new.
    host.push_rendered_config(rendered_script())
    joined = "\n".join(ssh.commands)
    assert "ifup wg51070" in joined
    # New link: nothing to bounce or live-patch.
    assert "ifdown --force wg51070" not in joined
    assert "syncconf" not in joined
    assert "birdc configure" in joined


def test_push_restarts_dead_bird(leaf):
    host, ssh = leaf
    ssh.bird_active = False
    host.push_rendered_config(rendered_script())
    joined = "\n".join(ssh.commands)
    assert "systemctl restart bird" in joined
    assert "birdc configure" not in joined


def test_push_skips_apply_when_unchanged(leaf):
    host, ssh = leaf
    ssh.remote_files.update(parse_rendered_files(rendered_script()))
    host.push_rendered_config(rendered_script())
    joined = "\n".join(ssh.commands)
    assert not ssh.written
    assert "birdc configure" not in joined
    assert "syncconf" not in joined


def test_push_fails_on_bird_parse_error(leaf, monkeypatch):
    host, ssh = leaf

    def failing_run(command, timeout=60):
        if command.startswith("birdc configure"):
            return 0, "syntax error"
        return FakeSSH.run(ssh, command, timeout)

    monkeypatch.setattr(ssh, "run", failing_run)
    with pytest.raises(RuntimeError, match="birdc configure"):
        host.push_rendered_config(rendered_script())
