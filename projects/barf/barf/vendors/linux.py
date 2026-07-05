"""Linux hosts running bird2 (Debian + ifupdown + WireGuard).

The rendered config is a shell script of quoted ``BARF_FILE`` heredocs
(see ``templates/vpn/linux.j2``); each block is one barf-owned file.
``diff_config``/``push_rendered_config`` parse those blocks back into a
path -> content map and manage the files over SSH as root, so the
script and the deploy always agree on the file contents.
"""

import fnmatch
import re
import shlex
from difflib import unified_diff
from typing import Dict, List, Optional, Tuple

from barf.util.ssh import DeviceSSH
from barf.vendors import BaseHost, DeployDiff

# One barf-owned file per block: `cat << 'BARF_FILE' > <path>` ... `BARF_FILE`.
_FILE_BLOCK = re.compile(
    r"^cat << 'BARF_FILE' > (?P<path>\S+)\n(?P<content>.*?)^BARF_FILE$",
    re.MULTILINE | re.DOTALL,
)

_PRIVKEY_LINE = re.compile(r"^(?P<indent>[-+ ]?PrivateKey = ).+$", re.MULTILINE)


def parse_rendered_files(rendered: str) -> Dict[str, str]:
    """The ``path -> content`` map encoded in a rendered linux config."""
    files = {
        match["path"]: match["content"] for match in _FILE_BLOCK.finditer(rendered)
    }
    if not files:
        raise ValueError("rendered config contains no BARF_FILE blocks")
    return files


def redact_private_keys(text: str) -> str:
    """Blank WireGuard private keys in config or diff ``text``."""
    return _PRIVKEY_LINE.sub(r"\g<indent><redacted>", text)


class LinuxBirdHost(BaseHost):
    DEVICETYPE = "linux"

    # Linux hosts are managed as root over SSH (agent keys only); the
    # fleet-wide supertech user does not exist on them.
    SSH_USERNAME = "root"

    # Ownership is exclusion-based like VyOS: any device file matching
    # these globs is barf's, and matching files we did not render are
    # deleted on deploy. Fabric interfaces are wg<5-digit port>
    # (51000 + min(id)*64 + max(id)), so human-managed tunnels with
    # short names (wg0, wg1) never match.
    OWNED_GLOBS = (
        "/etc/wireguard/wg[0-9][0-9][0-9][0-9][0-9].conf",
        "/etc/network/interfaces.d/wg[0-9][0-9][0-9][0-9][0-9].conf",
    )

    def _open_ssh(self) -> DeviceSSH:
        address = self.ssh_ip
        if not address:
            raise RuntimeError(f"{self.hostname}: no reachable SSH address")
        return DeviceSSH(self, address, username=self.SSH_USERNAME)

    def _device_state(
        self, ssh: DeviceSSH, files: Dict[str, str]
    ) -> Tuple[Dict[str, Optional[str]], List[str]]:
        """The device's view of our files: current contents and strays.

        Returns:
            A ``path -> content`` map (None for files that do not exist
            on the device) and the sorted stale paths: device files
            matching OWNED_GLOBS that we no longer render.
        """
        remote: Dict[str, Optional[str]] = {}
        for path in files:
            rc, out = ssh.run(f"cat {shlex.quote(path)} 2>/dev/null")
            remote[path] = out if rc == 0 else None

        rc, out = ssh.run(f"ls -1 {' '.join(self.OWNED_GLOBS)} 2>/dev/null")
        listed = [line.strip() for line in out.splitlines() if line.strip()]
        stale = sorted(
            path
            for path in listed
            if path not in files
            and any(fnmatch.fnmatch(path, glob) for glob in self.OWNED_GLOBS)
        )
        return remote, stale

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff the rendered file map against the device's files.

        ``show_device_only`` has no meaning here: ownership is by file,
        and non-owned files are invisible to barf.
        """
        files = parse_rendered_files(rendered)
        with self._open_ssh() as ssh:
            remote, stale = self._device_state(ssh, files)

        chunks = []
        changed = added = 0
        for path, content in files.items():
            current = remote[path]
            if current == content:
                continue
            if current is None:
                added += 1
            else:
                changed += 1
            diff_lines = unified_diff(
                (current or "").splitlines(),
                content.splitlines(),
                fromfile=f"{path} (device)",
                tofile=f"{path} (rendered)",
                lineterm="",
            )
            chunks.append("\n".join(diff_lines))

        for path in stale:
            chunks.append(f"--- {path} (device)\n+++ (deleted: no longer rendered)")

        text = "\n\n".join(chunks)
        if redact:
            text = redact_private_keys(text)

        parts = []
        if added:
            parts.append(f"+{added} new")
        if changed:
            parts.append(f"~{changed} changed")
        if stale:
            parts.append(f"-{len(stale)} stale")
        return DeployDiff(
            text=text,
            has_changes=bool(chunks),
            summary=", ".join(parts) if parts else "no changes",
        )

    def push_rendered_config(self, rendered: str) -> None:
        """Write the rendered files, remove stale ones, and apply.

        Apply is deliberately conservative:
          * changed WireGuard configs are applied live with
            ``wg syncconf`` (no interface bounce);
          * interfaces for stale links are downed and their files
            removed; brand-new links are brought up with ``ifup``;
          * bird is reloaded once at the end when its files changed.
        Changed ifupdown configs are NOT bounced -- their pre/post
        hooks only run on ifdown/ifup, which would drop BGP; they take
        effect on the next boot or manual bounce.
        """
        files = parse_rendered_files(rendered)
        with self._open_ssh() as ssh:
            remote, stale = self._device_state(ssh, files)

            rc, out = ssh.run(
                "mkdir -p /etc/wireguard /etc/network/interfaces.d /etc/bird/conf.d"
            )
            if rc != 0:
                raise RuntimeError(f"{self.hostname}: mkdir failed: {out}")

            bird_changed = False
            new_interfaces = []
            for path, content in files.items():
                if remote[path] == content:
                    continue
                mode = 0o600 if path.startswith("/etc/wireguard/") else 0o644
                ssh.write_file(path, content, mode=mode)

                if path.startswith("/etc/bird/"):
                    bird_changed = True
                elif path.startswith("/etc/wireguard/"):
                    interface = path.rsplit("/", 1)[1].removesuffix(".conf")
                    if remote[path] is None:
                        new_interfaces.append(interface)
                    else:
                        # Live-apply key/peer/port changes; never bounces.
                        rc, out = ssh.run(
                            f"wg syncconf {interface} {shlex.quote(path)}"
                        )
                        if rc != 0:
                            raise RuntimeError(
                                f"{self.hostname}: wg syncconf {interface}"
                                f" failed: {out}"
                            )

            # Stale links: down the interface (best effort -- it may
            # already be gone), then drop both files.
            for path in stale:
                interface = path.rsplit("/", 1)[1].removesuffix(".conf")
                if path.startswith("/etc/network/"):
                    ssh.run(f"ifdown --force {interface} 2>/dev/null")
                    ssh.run(f"ip link del {interface} 2>/dev/null")
                ssh.run(f"rm -f {shlex.quote(path)}")

            for interface in sorted(set(new_interfaces)):
                rc, out = ssh.run(f"ifup {interface}")
                if rc != 0:
                    raise RuntimeError(
                        f"{self.hostname}: ifup {interface} failed: {out}"
                    )

            if bird_changed:
                # Success prints "Reconfigured" (or "Reconfiguration in
                # progress" while sessions drain); anything else is a
                # parse failure and bird keeps the old config.
                rc, out = ssh.run("birdc configure")
                if rc != 0 or "Reconfigur" not in out:
                    raise RuntimeError(
                        f"{self.hostname}: birdc configure failed: {out}"
                    )
