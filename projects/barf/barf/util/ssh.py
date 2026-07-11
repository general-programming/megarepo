import hashlib
import logging
import os
import select
import shlex
import shutil
import signal
import socket
import sys
import termios
import tty
from pathlib import Path
from typing import TYPE_CHECKING, Optional

import click
import paramiko

from barf.actions import get_supertech_creditentials
from barf.util.progress import PvProgress

if TYPE_CHECKING:
    from barf.vendors import BaseHost

log = logging.getLogger(__name__)


def file_md5(path: Path) -> str:
    """MD5 of a local file, read in chunks."""
    digest = hashlib.md5()
    with open(path, "rb") as f:
        while chunk := f.read(1024 * 1024):
            digest.update(chunk)
    return digest.hexdigest()


class DeviceSSH:
    """Thin paramiko wrapper for pushing files and oneshot scripts to devices.

    Authenticates as the shared supertech user from Vault, preferring
    agent/local keys (the fleet is publickey-only) with the password as
    a fallback.
    """

    def __init__(
        self,
        host: "BaseHost",
        address: str,
        connect_timeout: int = 10,
        username: Optional[str] = None,
    ):
        """Connect to ``address``.

        Args:
            host: The device this connection belongs to.
            address: The address to connect to.
            connect_timeout: Socket timeout for each auth attempt.
            username: Log in as this user with keys only, instead of
                the shared supertech user (whose Vault password is the
                fallback). Linux hosts are managed as root.
        """
        self.host = host
        self.address = address

        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())

        # Keys and password go in separate connection attempts: sshd counts
        # every offered agent key against MaxAuthTries, so one combined
        # attempt can get the transport dropped ("saw EOF") before password
        # auth is ever tried -- and key-only fleets never need the password.
        attempts = [{"allow_agent": True, "look_for_keys": True}]
        if username:
            self.username = username
        else:
            self.username, password = get_supertech_creditentials(host)
            attempts.append(
                {
                    "password": password,
                    "allow_agent": False,
                    "look_for_keys": False,
                }
            )
        errors = []
        for extra_args in attempts:
            try:
                self.client.connect(
                    address,
                    username=self.username,
                    timeout=connect_timeout,
                    # Wire compression is mostly moot for squashfs, but free.
                    compress=True,
                    **extra_args,
                )
                break
            except (paramiko.SSHException, OSError) as e:
                log.debug("SSH auth attempt failed for %s: %s", address, e)
                errors.append(str(e) or e.__class__.__name__)
                self.client.close()
        else:
            raise RuntimeError(
                f"all SSH auth methods failed for {self.username}@{address}: "
                + "; ".join(errors)
            )

    def __enter__(self) -> "DeviceSSH":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def close(self) -> None:
        self.client.close()

    def interactive_shell(self) -> int:
        """Attach the local terminal to a shell on the device.

        Puts the local tty into raw mode and pumps bytes both ways
        until the remote shell exits, forwarding window resizes as
        SIGWINCH arrives. Returns the remote exit status (0 when the
        device does not report one).
        """
        if not sys.stdin.isatty():
            raise RuntimeError("an interactive terminal is required")

        size = shutil.get_terminal_size()
        channel = self.client.invoke_shell(
            term=os.environ.get("TERM", "xterm"),
            width=size.columns,
            height=size.lines,
        )

        def forward_resize(signum, frame):
            new_size = shutil.get_terminal_size()
            channel.resize_pty(width=new_size.columns, height=new_size.lines)

        stdin_fd = sys.stdin.fileno()
        saved_attrs = termios.tcgetattr(stdin_fd)
        saved_winch = signal.signal(signal.SIGWINCH, forward_resize)
        try:
            tty.setraw(stdin_fd)
            channel.settimeout(0.0)
            while True:
                readable, _, _ = select.select([channel, stdin_fd], [], [])
                if channel in readable:
                    try:
                        data = channel.recv(65536)
                    except socket.timeout:
                        continue
                    if not data:
                        break
                    os.write(sys.stdout.fileno(), data)
                if stdin_fd in readable:
                    data = os.read(stdin_fd, 65536)
                    if not data:
                        break
                    channel.sendall(data)
        finally:
            termios.tcsetattr(stdin_fd, termios.TCSADRAIN, saved_attrs)
            signal.signal(signal.SIGWINCH, saved_winch)

        status = channel.recv_exit_status() if channel.exit_status_ready() else 0
        channel.close()
        return status

    def run(self, command: str, timeout: int = 60) -> tuple[int, str]:
        """Run a single command, returning (exit_status, output)."""
        _stdin, stdout, _stderr = self.client.exec_command(command, timeout=timeout)
        output = stdout.read().decode(errors="replace")
        return stdout.channel.recv_exit_status(), output

    def _remote_md5(self, remote: str) -> Optional[str]:
        """MD5 of a remote file, or None if it cannot be hashed."""
        rc, out = self.run(f"md5sum {shlex.quote(remote)}", timeout=120)
        if rc != 0 or not out.strip():
            return None
        return out.split()[0]

    def put(
        self,
        local: Path,
        remote: str,
        label: Optional[str] = None,
        attempts: int = 3,
    ) -> None:
        """SFTP a file, verifying its MD5 and restarting the copy on mismatch.

        A pre-existing remote file with a matching checksum is left alone.
        """
        local_md5 = file_md5(local)
        size = local.stat().st_size

        sftp = self.client.open_sftp()
        try:
            for attempt in range(1, attempts + 1):
                if attempt == 1:
                    try:
                        exists = sftp.stat(remote).st_size == size
                    except FileNotFoundError:
                        exists = False
                    if exists and self._remote_md5(remote) == local_md5:
                        log.debug(
                            "%s already present with matching md5 on %s",
                            remote,
                            self.host.hostname,
                        )
                        return
                else:
                    click.echo(
                        f"checksum mismatch for {remote} on"
                        f" {self.host.hostname}, restarting copy"
                        f" ({attempt}/{attempts})"
                    )

                progress = PvProgress(label or f"copy {local.name}", size)

                def update(sent: int, _total: int) -> None:
                    progress.update(sent)

                try:
                    sftp.put(str(local), remote, callback=update)
                finally:
                    progress.finish()

                if self._remote_md5(remote) == local_md5:
                    return
        finally:
            sftp.close()

        raise RuntimeError(
            f"md5 mismatch for {remote} on {self.host.hostname} after {attempts} copies"
        )

    def write_file(self, remote: str, content: str, mode: int = 0o644) -> None:
        """Write ``content`` to ``remote`` over SFTP, setting ``mode``."""
        sftp = self.client.open_sftp()
        try:
            with sftp.open(remote, "w") as f:
                f.write(content)
            sftp.chmod(remote, mode)
        finally:
            sftp.close()

    def _upload_script(self, name: str, content: str) -> str:
        """Upload a script to /tmp, returning its remote path."""
        remote = f"/tmp/{name}"
        sftp = self.client.open_sftp()
        try:
            with sftp.open(remote, "w") as f:
                f.write(content)
            sftp.chmod(remote, 0o755)
        finally:
            sftp.close()
        return remote

    def run_detached(self, name: str, content: str) -> str:
        """Upload a script and launch it detached from this session.

        For scripts that sever our own connectivity (BGP drain + reboot):
        the exec returns immediately and the script keeps running on the
        device even after the session drops, logging to ``<script>.log``.
        Returns the remote log path.
        """
        remote = self._upload_script(name, content)
        log_path = f"{remote}.log"

        rc, out = self.run(
            f"nohup vbash {shlex.quote(remote)} > {shlex.quote(log_path)} 2>&1 &"
            " echo DETACHED",
            timeout=30,
        )
        if rc != 0 or "DETACHED" not in out:
            raise RuntimeError(
                f"failed to launch {name} detached on {self.host.hostname}"
            )
        return log_path

    def run_script(
        self,
        name: str,
        content: str,
        timeout: int = 900,
        echo_prefix: Optional[str] = None,
    ) -> tuple[int, str]:
        """Upload a script and execute it in a single exec channel.

        Everything a stage needs must be inside the script so that we are
        not sending commands one after another over separate channels.
        Returns ``(exit_status, combined_output)``.
        """
        remote = self._upload_script(name, content)

        # A pty merges stderr into stdout and keeps sudo happy.
        _stdin, stdout, _stderr = self.client.exec_command(
            f"vbash {shlex.quote(remote)}", timeout=timeout, get_pty=True
        )
        lines = []
        for line in stdout:
            line = line.rstrip("\r\n")
            lines.append(line)
            if echo_prefix is not None and line:
                click.echo(f"{echo_prefix}{line}")

        return stdout.channel.recv_exit_status(), "\n".join(lines)
