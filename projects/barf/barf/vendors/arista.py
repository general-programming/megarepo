"""Scoped Arista EOS management: admin user, its SSH keys, enable password.

Unlike VyOS hosts (whole-config declarative ownership), EOS devices are
adopted one config slice at a time; this is the first slice. render, diff,
and deploy all operate ONLY on:

  - the ``admin`` username: privilege 15, network-admin role, sha512
    secret, primary/secondary ssh-key
  - the ``enable`` password
  - eAPI itself: ``management api http-commands`` enabled over HTTPS,
    including the host's ``eapi_vrf`` (network.yml) so the API answers on
    the internal-facing side, not just the default VRF

Nothing else on the device is read into diffs or written by deploys — a
deploy sends exactly the managed ``username admin ...`` / ``enable
password ...`` lines plus a startup-config save, never a ``no ...`` for
anything outside the scope.

Transport is eAPI (JSON-RPC over HTTPS) via pyeapi, authenticated as the
managed ``admin`` user itself; see docs/network/arista-adoption.md for the
one-time per-device bootstrap that breaks the chicken-and-egg.

Password hashes are deterministic (salt derived from hostname + purpose +
secret) so repeated renders are byte-identical, and diffs compare by
*verifying* the Vault secret against whatever hash the device holds — a
device hash with a different salt but the same password counts as in
sync, so adopting a hand-set device never rewrites matching credentials.
"""

import hashlib
import re
import ssl
from typing import Any, Dict, List, Optional, Tuple

# passlib's hash registry is populated at runtime, which ty cannot see.
from passlib.hash import sha512_crypt  # ty: ignore[unresolved-import]

from barf.vendors import BaseHost, DeployDiff, generate_tacacs_key

MANAGED_USERNAME = "admin"

# One primary + one secondary ssh-key is all EOS models per user.
MAX_SSH_KEYS = 2

_REDACTED = "<hash-redacted>"


def deterministic_sha512(password: str, salt_seed: str) -> str:
    """A sha512-crypt hash whose salt is a pure function of its inputs.

    Args:
        password: The secret to hash.
        salt_seed: Stable per-host/per-purpose seed, e.g.
            ``"<hostname>:admin"``.
    """
    # sha512-crypt salts take [./0-9A-Za-z]; hex digest chars are a
    # subset, and 16 chars is the format's maximum salt length.
    salt = hashlib.sha256(f"{salt_seed}:{password}".encode()).hexdigest()[:16]
    return sha512_crypt.using(salt=salt, rounds=5000).hash(password)


class EosHost(BaseHost):
    DEVICETYPE = "eos"
    TEMPLATABLE = True
    # Scoped management only: the generic NAPALM whole-config merge path
    # must never run against these devices.
    NAPALM_DRIVER = None
    SSH_USERNAME = MANAGED_USERNAME

    # global_meta is needed at diff/deploy time (ssh keys live there),
    # but the BaseHost diff/push interface doesn't carry it;
    # load_network() attaches it right after construction.
    global_meta: Optional[dict] = None

    # VRF the device's internal-facing addresses live in; eAPI must be
    # explicitly enabled per VRF or it only answers in default.
    eapi_vrf: Optional[str] = None

    @classmethod
    def from_meta(cls, hostname: str, meta: dict):
        host = super().from_meta(hostname=hostname, meta=meta)
        host.eapi_vrf = meta.get("eapi_vrf")
        return host

    # -- desired state ------------------------------------------------

    def _admin_password(self) -> str:
        return self.secret("admin-password", generate_tacacs_key)

    def _enable_password(self) -> str:
        return self.secret("enable-password", generate_tacacs_key)

    def _ssh_keys(self, global_meta: dict) -> List[str]:
        keys = [key.strip() for key in global_meta.get("ssh_keys", []) if key.strip()]
        if len(keys) > MAX_SSH_KEYS:
            raise ValueError(
                f"{self.hostname}: EOS supports one primary and one"
                f" secondary ssh-key per user; got {len(keys)}"
                " global_meta.ssh_keys"
            )
        return keys

    def managed_commands(self, global_meta: dict) -> List[str]:
        """The full managed-scope config, as EOS CLI commands."""
        admin_hash = deterministic_sha512(
            self._admin_password(), f"{self.hostname}:{MANAGED_USERNAME}"
        )
        enable_hash = deterministic_sha512(
            self._enable_password(), f"{self.hostname}:enable"
        )

        commands = [
            f"username {MANAGED_USERNAME} privilege 15 role network-admin"
            f" secret sha512 {admin_hash}"
        ]
        keys = self._ssh_keys(global_meta)
        if keys:
            commands.append(f"username {MANAGED_USERNAME} ssh-key {keys[0]}")
        if len(keys) == MAX_SSH_KEYS:
            commands.append(
                f"username {MANAGED_USERNAME} ssh-key secondary {keys[1]}"
            )
        commands.append(f"enable password sha512 {enable_hash}")
        commands.extend(self._eapi_commands())
        return commands

    def _eapi_commands(self) -> List[str]:
        """The eAPI slice: HTTPS on, plus the internal-facing VRF.

        Emitted as the flat command sequence pyeapi sends in configure
        mode; ``vrf <name>`` enters a sub-mode of the api block, so
        order is the hierarchy.
        """
        commands = [
            "management api http-commands",
            "protocol https port 443",
            "no shutdown",
        ]
        if self.eapi_vrf:
            commands += [f"vrf {self.eapi_vrf}", "no shutdown"]
        return commands

    def render_managed_config(self, global_meta: dict) -> str:
        """Managed-slice render; picked up by ``render_host_config``."""
        return "\n".join(self.managed_commands(global_meta)) + "\n"

    # -- device access ------------------------------------------------

    def _eapi_node(self):
        """A pyeapi node for this device, authenticated as ``admin``."""
        import pyeapi

        address = self.management_ip
        if not address:
            raise RuntimeError(
                f"{self.hostname}: no address answering on 443 (is eAPI"
                " enabled? see docs/network/arista-adoption.md)"
            )

        node = pyeapi.connect(
            transport="https",
            host=address,
            username=MANAGED_USERNAME,
            password=self._admin_password(),
            return_node=True,
            # Devices run the self-signed default SSL profile.
            context=ssl._create_unverified_context(),
        )
        node.enable_authentication(self._enable_password())
        return node

    def _device_managed_state(self, node=None) -> Dict[str, Any]:
        """The device's current managed-scope lines, parsed.

        Returns ``admin_line``, ``admin_hash``, ``ssh_key``,
        ``ssh_key_secondary``, ``enable_line``, ``enable_hash`` — each
        None when absent on the device.
        """
        node = node or self._eapi_node()
        responses = node.enable(
            [
                "show running-config all section username",
                "show running-config all section enable",
                "show running-config all section management api http-commands",
            ],
            encoding="text",
        )
        text = "\n".join(
            response["result"]["output"] for response in responses
        )

        state: Dict[str, Any] = {
            "admin_line": None,
            "admin_hash": None,
            "ssh_key": None,
            "ssh_key_secondary": None,
            "enable_line": None,
            "enable_hash": None,
        }
        state.update(self._parse_eapi_section(text))

        for line in text.splitlines():
            line = line.strip()
            match = re.match(
                rf"^username {MANAGED_USERNAME} ssh-key secondary (.+)$", line
            )
            if match:
                state["ssh_key_secondary"] = match.group(1).strip()
                continue
            match = re.match(rf"^username {MANAGED_USERNAME} ssh-key (.+)$", line)
            if match:
                state["ssh_key"] = match.group(1).strip()
                continue
            match = re.match(
                rf"^username {MANAGED_USERNAME} .*secret (?:sha512 )?(\S+)$", line
            )
            if match:
                state["admin_line"] = line
                state["admin_hash"] = match.group(1)
                continue
            match = re.match(r"^enable password (?:sha512 )?(\S+)$", line)
            if match:
                state["enable_line"] = line
                state["enable_hash"] = match.group(1)

        return state

    def _parse_eapi_section(self, text: str) -> Dict[str, Optional[bool]]:
        """eAPI state from ``management api http-commands`` output.

        ``show running-config all`` prints default state explicitly, so
        both ``shutdown`` and ``no shutdown`` appear literally. The api
        block's own shutdown line sits at one indent level; per-VRF
        shutdown lines sit under their ``vrf <name>`` sub-block.
        """
        state: Dict[str, Optional[bool]] = {
            "eapi_enabled": None,
            "eapi_https": None,
            "eapi_vrf_enabled": None,
        }

        in_block = False
        current_vrf = None
        for raw in text.splitlines():
            stripped = raw.strip()
            if stripped.startswith("management api http-commands"):
                in_block = True
                current_vrf = None
                continue
            if in_block and raw and not raw[0].isspace():
                # Un-indented line: the section ended.
                in_block = False
                current_vrf = None
            if not in_block:
                continue

            vrf_match = re.match(r"^vrf (\S+)$", stripped)
            if vrf_match:
                current_vrf = vrf_match.group(1)
                continue
            if stripped.startswith("protocol https"):
                state["eapi_https"] = True
                continue
            if stripped == "no shutdown":
                if current_vrf is None:
                    state["eapi_enabled"] = True
                elif current_vrf == self.eapi_vrf:
                    state["eapi_vrf_enabled"] = True
            elif stripped == "shutdown":
                if current_vrf is None:
                    state["eapi_enabled"] = False
                elif current_vrf == self.eapi_vrf:
                    state["eapi_vrf_enabled"] = False

        return state

    # -- diff / deploy ------------------------------------------------

    @staticmethod
    def _hash_matches(password: str, device_hash: Optional[str]) -> bool:
        if not device_hash or not device_hash.startswith("$6$"):
            return False
        try:
            return sha512_crypt.verify(password, device_hash)
        except ValueError:
            return False

    def _drift(
        self, global_meta: dict, state: Dict[str, Any]
    ) -> List[Tuple[Optional[str], str]]:
        """(device line, desired line) pairs for every out-of-sync item."""
        desired = self.managed_commands(global_meta)
        desired_admin = desired[0]
        desired_enable = next(
            line for line in desired if line.startswith("enable password")
        )
        keys = self._ssh_keys(global_meta)

        drift: List[Tuple[Optional[str], str]] = []

        admin_ok = (
            state["admin_line"] is not None
            and "privilege 15" in state["admin_line"]
            and self._hash_matches(self._admin_password(), state["admin_hash"])
        )
        if not admin_ok:
            drift.append((state["admin_line"], desired_admin))

        desired_primary = keys[0] if keys else None
        if desired_primary and state["ssh_key"] != desired_primary:
            device_line = (
                f"username {MANAGED_USERNAME} ssh-key {state['ssh_key']}"
                if state["ssh_key"]
                else None
            )
            drift.append(
                (device_line, f"username {MANAGED_USERNAME} ssh-key {desired_primary}")
            )

        desired_secondary = keys[1] if len(keys) == MAX_SSH_KEYS else None
        if desired_secondary and state["ssh_key_secondary"] != desired_secondary:
            device_line = (
                f"username {MANAGED_USERNAME} ssh-key secondary"
                f" {state['ssh_key_secondary']}"
                if state["ssh_key_secondary"]
                else None
            )
            drift.append(
                (
                    device_line,
                    f"username {MANAGED_USERNAME} ssh-key secondary"
                    f" {desired_secondary}",
                )
            )

        if not self._hash_matches(self._enable_password(), state["enable_hash"]):
            drift.append((state["enable_line"], desired_enable))

        if state.get("eapi_enabled") is not True or state.get("eapi_https") is not True:
            drift.append(
                (
                    "management api http-commands: shutdown or non-https"
                    if state.get("eapi_enabled") is not None
                    else None,
                    "management api http-commands / protocol https port 443"
                    " / no shutdown",
                )
            )
        if self.eapi_vrf and state.get("eapi_vrf_enabled") is not True:
            drift.append(
                (
                    f"management api http-commands vrf {self.eapi_vrf}: shutdown"
                    if state.get("eapi_vrf_enabled") is not None
                    else None,
                    f"management api http-commands / vrf {self.eapi_vrf}"
                    " / no shutdown",
                )
            )

        return drift

    @staticmethod
    def _redact(line: str) -> str:
        return re.sub(r"\$6\$\S+", _REDACTED, line)

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff the managed slice only; the rest of the device is invisible.

        ``rendered`` is accepted for interface parity but the desired
        state is recomputed from Vault + network.yml so passwords can be
        compared by hash verification instead of by salt-sensitive text.
        """
        state = self._device_managed_state()
        drift = self._drift(self._require_global_meta(), state)

        lines = []
        for device_line, desired_line in drift:
            if device_line:
                lines.append(
                    f"- {self._redact(device_line) if redact else device_line}"
                )
            lines.append(
                f"+ {self._redact(desired_line) if redact else desired_line}"
            )

        return DeployDiff(
            text="\n".join(lines),
            has_changes=bool(drift),
            summary=(
                f"{len(drift)} managed item(s) drifted" if drift else "no changes"
            ),
        )

    def push_rendered_config(self, rendered: str) -> None:
        """Apply the full managed slice and save the config.

        Idempotent: re-sends every managed line (identical lines are
        no-ops on EOS) and never touches anything outside the slice.
        """
        node = self._eapi_node()
        node.config(self.managed_commands(self._require_global_meta()))
        node.enable(["copy running-config startup-config"])

    def _require_global_meta(self) -> dict:
        if self.global_meta is None:
            raise RuntimeError(
                f"{self.hostname}: global_meta not attached; load hosts via"
                " load_network()"
            )
        return self.global_meta
