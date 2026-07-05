import math
import time
from pathlib import Path
from typing import TYPE_CHECKING, List, Optional

import click

from barf.vendors import BaseHost, DeployDiff

if TYPE_CHECKING:
    from barf.util.vyos_api import SystemImage


def _parse_version(output: str) -> str:
    """Pull the version out of ``show version`` output.

    The "VyOS " prefix is stripped so the value lines up with upstream
    release tags (e.g. ``2026.06.30-0048-rolling``).
    """
    for line in output.splitlines():
        if line.lower().startswith("version:"):
            return line.split(":", 1)[1].strip().removeprefix("VyOS ")
    if not output.strip():
        return "-"
    return output.strip().splitlines()[0].removeprefix("VyOS ")


def _parse_uptime(output: str) -> str:
    """Pull a human uptime out of ``show system uptime`` output."""
    for line in output.splitlines():
        line = line.strip()
        if not line:
            continue
        if line.lower().startswith("uptime:"):
            return line.split(":", 1)[1].strip()
        return line
    return "-"


def _script_ok(rc: int, output: str, marker: str) -> bool:
    """Whether a oneshot script really passed.

    Requires a clean exit AND the OK marker AND no FAIL marker:
    script-template hijacks ``exit``, so a failed script can still
    finish with rc 0 and its OK line printed further down.
    """
    return rc == 0 and f"{marker}-OK" in output and f"{marker}-FAIL" not in output


class VyOSHost(BaseHost):
    DEVICETYPE = "vyos"

    # Ownership is exclusion-based: barf owns the whole config —
    # deploys delete device config we did not render — EXCEPT under
    # these kept prefixes. Shrunk prefix by prefix as network.yml
    # coverage grows; seeded from a 2026-07-05 fleet survey of every
    # device-only path. Never drop a prefix while devices still carry
    # intentional out-of-band config under it.
    KEPT_PATHS = (
        # VyOS platform-managed config and hardware facts.
        ("interfaces", "loopback"),
        ("system", "config-management"),
        ("system", "conntrack"),
        ("system", "console"),
        ("system", "login"),
        # Interface details not yet modeled in network.yml (hw-id,
        # vifs, dhcp/static addresses, ipv6 autoconf).
        ("interfaces", "ethernet"),
        # Hand-built links and their routing/NAT, not yet in
        # network.yml (e.g. sea1-vpn-leaf-1's wg51820 "/dev/hack"
        # tunnel and port-forwards; sea69's OSPF).
        ("interfaces", "wireguard"),
        ("protocols", "bgp"),
        ("protocols", "ospf"),
        ("protocols", "static"),
        ("vpn", "ipsec", "site-to-site"),
    )

    @property
    def can_bfd(self) -> bool:
        return True

    @property
    def device_username(self):
        return "vyos"

    def interface_prefix(self, interface):
        if interface.management:
            interface_type = "dummy"
        else:
            interface_type = "ethernet"

        set_string = f"set interfaces {interface_type} {interface.name}"

        if interface.untagged_vlan:
            set_string += f" vif {interface.untagged_vlan.vid}"

        return set_string

    @staticmethod
    def _api_key() -> str:
        # Imported lazily: barf.util.secrets reaches barf.actions, which
        # imports this package back.
        from barf.util.secrets import VaultSecrets

        return VaultSecrets().vyos_api_password

    def _api_show(self, path: List[str], timeout: int = 10) -> str:
        """Run an operational ``show`` command via the HTTPS API."""
        from barf.util.vyos_api import vyos_api_show

        return vyos_api_show(
            self.require_management_ip(), self._api_key(), path, timeout=timeout
        )

    def _api_image_delete(self, name: str) -> None:
        """Delete an installed system image via the HTTPS API."""
        from barf.util.vyos_api import vyos_api_image_delete

        vyos_api_image_delete(self.require_management_ip(), self._api_key(), name)

    def running_config_paths(self):
        """The device's running config as a set of ``set``-path tuples."""
        from barf.util.vyos_api import vyos_api_retrieve_config
        from barf.util.vyos_config import paths_from_api_json

        data = vyos_api_retrieve_config(self.require_management_ip(), self._api_key())
        return paths_from_api_json(data)

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff ``rendered`` against the running config, locally.

        The running config comes off the HTTPS API as a JSON tree; both
        sides are flattened to path sets and diffed on this machine. No
        config session is opened on the device (unlike the NAPALM
        compare the other vendors use).
        """
        from barf.util.vyos_config import format_diff, summarize_diff

        diff, _running = self._config_diff(rendered)
        return DeployDiff(
            text=format_diff(diff, redact=redact, show_device_only=show_device_only),
            has_changes=diff.has_changes,
            summary=summarize_diff(diff),
        )

    def _config_diff(self, rendered: str):
        """The raw path diff plus the (reconciled) running path set."""
        from barf.util.vyos_config import (
            diff_paths,
            parse_set_commands,
            reconcile_hashed_passwords,
        )

        # The device stores passwords hashed; verify our plaintext
        # against its hash so unchanged passwords do not show as diffs.
        running, candidate = reconcile_hashed_passwords(
            self.running_config_paths(), parse_set_commands(rendered)
        )
        return diff_paths(running, candidate, kept=self.KEPT_PATHS), running

    def push_rendered_config(self, rendered: str) -> None:
        """Apply the rendered config via the HTTPS API and save it.

        One atomic ``/configure`` commit: deletions of stale owned
        config first (collapsed to whole-node deletes where possible),
        then the additions as ``set`` ops. Config under KEPT_PATHS is
        merged, never deleted. No SSH or NAPALM involved.
        """
        from barf.util.vyos_api import vyos_api_config_save, vyos_api_configure
        from barf.util.vyos_config import minimal_delete_paths

        diff, running = self._config_diff(rendered)
        if not diff.has_changes:
            return

        ops = [
            {"op": "delete", "path": list(path)}
            for path in minimal_delete_paths(diff.removed, running)
        ]
        ops += [{"op": "set", "path": list(path)} for path in diff.added]

        address = self.require_management_ip()
        vyos_api_configure(address, self._api_key(), ops)
        vyos_api_config_save(address, self._api_key())

    def system_images(self) -> List["SystemImage"]:
        """The installed system images, per ``show system image``."""
        from barf.util.vyos_api import parse_system_images

        return parse_system_images(self._api_show(["system", "image"]))

    def human_version(self) -> str:
        return _parse_version(self._api_show(["version"]))

    def uptime(self) -> str:
        return _parse_uptime(self._api_show(["system", "uptime"]))

    def verify_routing(self) -> Optional[str]:
        """Warn when BGP is still administratively down after a reboot."""
        if not self.asn:
            return None

        try:
            summary = self._api_show(["bgp", "summary"])
        except Exception as e:  # noqa: BLE001 - verification is best-effort
            return f"could not check BGP summary: {e}"

        if "Idle (Admin)" in summary:
            return (
                "BGP still administratively down after reboot;"
                " the shutdown may have been saved"
            )
        return None

    def update_host(
        self,
        filename: str,
        stage: bool,
        drain_wait: int = 5,
        version: Optional[str] = None,
    ) -> str:
        """Stage a new system image onto the device, optionally rebooting.

        Staging only copies the image to /tmp on the device; the actual
        ``add system image`` install happens on the reboot path, right
        before the BGP drain. An image that ``show system image`` already
        lists as default boot skips straight to the reboot; one that is
        listed but not default (an interrupted install) is deleted and
        reinstalled cleanly.

        Args:
            filename: Local path of the image to push.
            stage: Only copy the image; do not install or reboot.
            drain_wait: Seconds to wait after the BGP shutdown.
            version: Image version; derived from the filename when not
                given.

        Returns:
            A short human-readable result.
        """
        # Imported lazily: barf.util.ssh reaches barf.actions, which
        # imports this package back.
        from barf.util import vyos_scripts
        from barf.util.ssh import DeviceSSH

        image = Path(filename)
        if version is None:
            version = image.name.removeprefix("vyos-").removesuffix(
                "-generic-amd64.iso"
            )

        prefix = f"[{self.hostname}]"
        ssh_address = self.ssh_ip
        if not ssh_address:
            raise RuntimeError(f"{self.hostname}: no reachable SSH address")

        # Short-term staging area; cleared by the install or a reboot.
        remote_path = f"/tmp/{image.name}"
        # The installer unpacks next to the copied image, so demand room
        # for at least 1.5x the image.
        required_mb = max(1, math.ceil(image.stat().st_size * 1.5 / 2**20))

        install_needed = True
        if not stage:
            existing = next(
                (i for i in self.system_images() if i.name == version), None
            )
            if existing and existing.default_boot:
                click.echo(f"{prefix} image already installed and default boot")
                install_needed = False
            elif existing:
                # A directory in /boot alone (interrupted install, or an
                # image that lost default boot) must not skip the installer,
                # or the reboot would land on the old default image.
                click.echo(f"{prefix} deleting stale image {version}")
                self._api_image_delete(version)

        with DeviceSSH(self, ssh_address) as ssh:
            if install_needed:
                click.echo(f"{prefix} running pre-checks")
                rc, out = ssh.run_script(
                    "barf-precheck.sh",
                    vyos_scripts.precheck(remote_path, required_mb=required_mb),
                    echo_prefix=f"{prefix}   ",
                )
                if not _script_ok(rc, out, "PRECHECK"):
                    raise RuntimeError(f"{self.hostname}: pre-checks failed")

                click.echo(f"{prefix} copying image")
                ssh.put(image, remote_path, label=f"{prefix} {image.name}")

                if stage:
                    return f"staged {version} at {remote_path}"

                click.echo(f"{prefix} installing image")
                rc, out = ssh.run_script(
                    "barf-install.sh",
                    vyos_scripts.install(remote_path, version),
                    echo_prefix=f"{prefix}   ",
                )
                if not _script_ok(rc, out, "INSTALL"):
                    raise RuntimeError(f"{self.hostname}: image install failed")

            drain_bgp = bool(self.asn)
            click.echo(f"{prefix} launching detached drain+reboot")
            log_path = ssh.run_detached(
                "barf-reboot.sh",
                vyos_scripts.drain_and_reboot(version, drain_wait, drain_bgp),
            )

        # We reach the device over the same BGP paths the drain tears
        # down, so the script runs detached and we verify by absence:
        # wait out the drain, and only if the device is still reachable
        # afterwards look for a failure marker in its log.
        time.sleep(drain_wait + 5)
        failure = self._detached_reboot_failure(ssh_address, log_path)
        if failure:
            raise RuntimeError(f"{self.hostname}: drain/reboot failed: {failure}")

        return f"rebooting into {version}"

    def _detached_reboot_failure(
        self, ssh_address: str, log_path: str
    ) -> Optional[str]:
        """A failure line from the detached drain+reboot log, if any.

        Being unable to connect is the expected success case (the drain
        severed our path, or the device is already rebooting); a device
        that still answers with a FAIL marker in its log means the script
        bailed and no reboot is coming.
        """
        from barf.util.ssh import DeviceSSH

        try:
            with DeviceSSH(self, ssh_address, connect_timeout=5) as ssh:
                _rc, log_output = ssh.run(f"cat {log_path}", timeout=10)
        except Exception:  # noqa: BLE001 - unreachable means the drain worked
            return None

        for line in log_output.splitlines():
            if "-FAIL" in line:
                return line.strip()
        return None

    def cleanup_host(self) -> List[str]:
        """Run housekeeping tasks on the device via the HTTPS API."""
        actions = []
        actions += self._cleanup_old_images()
        # Future housekeeping steps (staging leftovers, logs, ...) go here.
        return actions

    def _cleanup_old_images(self) -> List[str]:
        """Delete system images that are neither running nor default boot."""
        images = self.system_images()
        if not images:
            raise RuntimeError(f"{self.hostname}: could not list system images")

        actions = []
        for image in images:
            if image.default_boot or image.running:
                continue
            self._api_image_delete(image.name)
            actions.append(f"deleted image {image.name}")

        return actions
