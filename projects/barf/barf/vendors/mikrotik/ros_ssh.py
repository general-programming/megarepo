"""RouterOS interactive SSH with Safe Mode as the transaction.

RouterOS has no candidate config or commit for scripts; Safe Mode
(console Ctrl-X) is the one primitive that does: changes made while it
is held are reverted if the session drops, and kept when it is
released with a second Ctrl-X. Driving it over an interactive SSH
channel turns a deploy into commit-confirm:

    with RosSafeModeSession(client) as session:   # Ctrl-X on enter
        session.run("/interface/wireguard set ...")
        ...verify (REST probes, spine-side checks)...
        session.release()                          # Ctrl-X: keep

Any exception (or calling ``abort()``) closes the transport without
releasing -- RouterOS reverts everything from this session. Losing
management reachability mid-deploy kills the TCP session and reverts
the same way: the deadman's switch is intrinsic when the management
path rides the config being changed.

Safe Mode tracks a bounded action history (~100 changes); past it the
router drops the rollback guarantee, so keep transactions small.
"""

import re
import time
from typing import TYPE_CHECKING, Optional, Tuple

if TYPE_CHECKING:
    import paramiko

CTRL_X = b"\x18"

# RouterOS prompt, colors disabled via the ``+ct`` login suffix:
# ``[barf@sea420-acc-v-hv2] >`` or ``[barf@sea420-acc-v-hv2] <SAFE>``.
PROMPT_RE = re.compile(r"\[[^\[\]]+@[^\[\]]+\]\s*(<SAFE>\s*>?|>)\s*$")

# RouterOS emits cursor movements and terminal queries even on a
# ``+ct`` dumb terminal (observed on 7.22.3: ``\x1b[9999B``,
# ``\x1b[c``); strip them before any matching.
_ANSI_RE = re.compile(r"\x1b\[[0-9;?]*[a-zA-Z]")


def clean_terminal(text: str) -> str:
    """Terminal output with ANSI escapes and bare CRs removed."""
    return _ANSI_RE.sub("", text).replace("\r", "")


_ERROR_MARKERS = (
    "failure:",
    "syntax error",
    "bad command name",
    "input does not match",
    "expected end of command",
    "no such item",
    "invalid value",
)


def looks_like_error(output: str) -> Optional[str]:
    """The first RouterOS error line in command output, if any."""
    for line in output.splitlines():
        lowered = line.strip().lower()
        if any(marker in lowered for marker in _ERROR_MARKERS):
            return line.strip()
    return None


class RosSafeModeSession:
    """One Safe-Mode-guarded interactive RouterOS session."""

    def __init__(
        self,
        client: "paramiko.SSHClient",
        timeout: float = 15.0,
        takeover: Optional[str] = None,
    ):
        """``client`` is a connected paramiko SSHClient whose user was
        logged in with the ``+ct`` console suffix (no colors, dumb
        terminal). ``takeover`` is forwarded to :meth:`enter` when the
        session is used as a context manager."""
        self.timeout = timeout
        self.takeover = takeover
        self.channel = client.invoke_shell(width=500)
        self.channel.settimeout(timeout)
        self._read_until_prompt()
        self._released = False

    def __enter__(self) -> "RosSafeModeSession":
        try:
            self.enter(takeover=self.takeover)
        except BaseException:
            self.abort()
            raise
        return self

    def __exit__(self, exc_type, exc, tb) -> None:
        # Released sessions are already committed; anything else is
        # torn down hard so RouterOS reverts it.
        if not self._released:
            self.abort()

    def _read_until_prompt(self) -> str:
        buffer = ""
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            if self.channel.recv_ready():
                buffer += self.channel.recv(65536).decode(errors="replace")
                cleaned = clean_terminal(buffer)
                if PROMPT_RE.search(cleaned.rsplit("\n", 1)[-1]):
                    return cleaned
            else:
                time.sleep(0.05)
        raise TimeoutError(f"no RouterOS prompt within {self.timeout}s: {buffer!r}")

    def _prompt_in_safe_mode(self, output: str) -> bool:
        return "<SAFE>" in output.rsplit("\n", 1)[-1]

    def enter(self, takeover: Optional[str] = None) -> None:
        """Take Safe Mode (Ctrl-X).

        Success is judged by the prompt gaining ``<SAFE>`` -- the
        human-facing message wording varies across RouterOS versions
        ("Safe Mode taken", 7.22's "Taking Safe Mode session...
        Success!"), the prompt state does not.

        When a dead session still holds Safe Mode (observed on 7.22.3
        after a transport-level abort: the box keeps its changes
        PENDING and asks "Unroll, release or abort [u/r]?"),
        ``takeover`` decides: ``"r"`` releases (keeps) the zombie's
        pending changes, ``"u"`` unrolls (reverts) them; None refuses
        and raises so a human adjudicates.
        """
        self.channel.send(CTRL_X)
        output = self._read_until(("<SAFE>", "[u/r]?"))
        if "[u/r]?" in output:
            if takeover not in ("u", "r"):
                raise RuntimeError(
                    f"Safe Mode held by another session; pass takeover='u'"
                    f" (revert its pending changes) or 'r' (keep them): {output!r}"
                )
            self.channel.send(takeover.encode())
            output = self._read_until_prompt()
        if not self._prompt_in_safe_mode(output):
            raise RuntimeError(f"could not take Safe Mode: {output!r}")

    def _read_until(self, needles: Tuple[str, ...]) -> str:
        buffer = ""
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            if self.channel.recv_ready():
                buffer += self.channel.recv(65536).decode(errors="replace")
                cleaned = clean_terminal(buffer)
                if any(needle in cleaned for needle in needles):
                    return cleaned
            else:
                time.sleep(0.05)
        raise TimeoutError(f"expected one of {needles!r}: {buffer!r}")

    def run(self, command: str) -> str:
        """Run one command, raising on RouterOS error output."""
        self.channel.send((command + "\r").encode())
        output = self._read_until_prompt()
        # Drop the echoed command line.
        body = "\n".join(output.split("\n")[1:])
        error = looks_like_error(body)
        if error:
            raise RuntimeError(f"RouterOS rejected {command!r}: {error}")
        return body

    def release(self) -> None:
        """Release Safe Mode (second Ctrl-X): keep the changes."""
        self.channel.send(CTRL_X)
        output = self._read_until_prompt()
        if self._prompt_in_safe_mode(output):
            raise RuntimeError(f"could not release Safe Mode: {output!r}")
        self._released = True

    def abort(self) -> None:
        """Drop the session without releasing: RouterOS reverts.

        A bare transport close is NOT always enough: dropped mid
        interactive prompt, 7.22.3 kept the server-side session (and
        Safe Mode) alive with the changes pending instead of reverting
        them. Cancel any pending command (Ctrl-C) and quit the console
        (Ctrl-D, which reverts while Safe Mode is held) before closing.
        """
        try:
            self.channel.send(b"\x03")
            time.sleep(0.2)
            self.channel.send(b"\x04")
            time.sleep(0.5)
        except Exception:  # noqa: BLE001 - teardown is best effort
            pass
        try:
            self.channel.transport.close()
        except Exception:  # noqa: BLE001 - teardown is best effort
            pass
