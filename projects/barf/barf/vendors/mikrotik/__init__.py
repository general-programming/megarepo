import logging
from concurrent.futures import ThreadPoolExecutor
from typing import Any, List, Optional

from barf.util.secrets import VaultSecrets
from barf.vendors import BaseHost, DeployDiff
from barf.vendors.mikrotik import ros_api, ros_config, ros_deploy

log = logging.getLogger(__name__)

# Name shared by the pre-change backup, its restore scheduler job, and
# the on-device forward-apply script.
_ROLLBACK_JOB = "barf-rollback"
_APPLY_SCRIPT = "barf-apply"


class MikroTikHost(BaseHost):
    DEVICETYPE = "mikrotik"

    # REST rides www-ssl; 443 is taken by RouterOS's reverse-proxy
    # service on sea420, so barf's www-ssl (barf-ssl cert) sits here.
    API_PORT = 8443
    API_USERNAME = "barf"

    # Seconds the armed rollback waits before restoring the backup; the
    # deploy must apply and confirm within this window or the timer
    # fires. Minutes, not seconds: the revert is a backup-load reboot,
    # and the backup save + apply + confirm all run inside the window.
    ROLLBACK_TIMEOUT = 120

    @property
    def management_ip(self) -> Optional[str]:
        """First endpoint answering on the REST port (not 443)."""
        return self._probe_endpoint(self.API_PORT)

    def _api_password(self) -> str:
        return VaultSecrets().mikrotik_api_password

    def _api_get(self, path: str) -> Any:
        return ros_api.ros_api_get(
            self.require_management_ip(),
            self.API_USERNAME,
            self._api_password(),
            path,
            port=self.API_PORT,
        )

    def _api_add(self, collection: str, props: dict) -> Any:
        return ros_api.ros_api_add(
            self.require_management_ip(),
            self.API_USERNAME,
            self._api_password(),
            collection,
            props,
            port=self.API_PORT,
        )

    def _api_delete(self, collection: str, item_id: str) -> Any:
        return ros_api.ros_api_delete(
            self.require_management_ip(),
            self.API_USERNAME,
            self._api_password(),
            collection,
            item_id,
            port=self.API_PORT,
        )

    def _api_run_script(self, name: str) -> Any:
        """Run an on-device ``/system/script`` by name (REST POST)."""
        return ros_api.ros_api_request(
            "POST",
            self.require_management_ip(),
            self.API_USERNAME,
            self._api_password(),
            "system/script/run",
            body={"number": name},
            port=self.API_PORT,
        )

    def _api_remove_named(self, collection: str, name: str) -> None:
        """Delete every item in ``collection`` whose ``name`` matches.

        Resolves the unstable ``.id`` at call time; a no-op when the
        named item is already gone (a fired rollback cleaned up).
        """
        for item in self._api_get(collection) or []:
            if item.get("name") == name and item.get(".id"):
                self._api_delete(collection, item[".id"])

    def device_items(self) -> dict:
        """Every owned collection's items, straight off the REST API.

        SETTINGS singletons come back as a bare object; they are
        wrapped in a one-item list so every path holds a list.

        One REST round trip per path, each opening its own HTTPS
        connection (``ros_api`` doesn't keep-alive) -- fetched
        concurrently so ~20 paths cost one round trip, not twenty.
        """
        paths = list(ros_config.COLLECTIONS) + list(ros_config.SETTINGS)
        with ThreadPoolExecutor(max_workers=min(16, len(paths))) as pool:
            fetched = dict(zip(paths, pool.map(self._api_get, paths)))

        items = {path: fetched[path] for path in ros_config.COLLECTIONS}
        for path in ros_config.SETTINGS:
            value = fetched[path]
            items[path] = [value] if isinstance(value, dict) else value
        return items

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff rendered RouterOS CLI against the device, locally.

        ``show_device_only`` appends the hand-managed items barf keeps
        (the excluded set) so the gap to full ownership is visible;
        they are never counted as changes.
        """
        desired = ros_config.parse_ros_commands(rendered)
        device = self.device_items()
        diff = ros_config.diff_items(desired, device)
        text = ros_config.format_diff(diff, redact=redact)
        if show_device_only:
            kept = ros_config.format_excluded(
                device, ros_config.rendered_scope(desired)
            )
            if kept:
                text = f"{text}\n{kept}" if text else kept
        return DeployDiff(
            text=text,
            has_changes=diff.has_changes,
            summary=ros_config.summarize_diff(diff),
        )

    def push_rendered_config(self, rendered: str) -> None:
        """Apply the rendered config under a self-cancelling rollback.

        Commit-confirm without a candidate config: save a full backup,
        arm a one-shot scheduler to restore it in ``ROLLBACK_TIMEOUT``
        seconds, apply the forward changes, then re-probe. Reachable and
        converged -> cancel the rollback (remove the scheduler, delete
        the backup and apply script). Otherwise the scheduler is left
        armed and the timer runs ``/system backup load`` -- a full
        restore + reboot -- so a change that severs our management path,
        or wedges routing, self-heals instead of stranding the box.

        A partly-applied forward script is safe: the backup predates
        every change, so restoring it returns the box to the exact
        pre-deploy state regardless of how far the apply got.
        """
        desired = ros_config.parse_ros_commands(rendered)
        device = self.device_items()
        diff = ros_config.diff_items(desired, device)
        if not diff.has_changes:
            return

        apply_cmds = ros_deploy.build_apply_commands(diff, desired, device)

        self._arm_rollback()
        try:
            self._apply_forward(apply_cmds)
            self._confirm_converged(desired)
        except Exception:
            # Leave the rollback armed: the timer restores the backup.
            # Surface the failure so the operator knows a revert (and
            # reboot) is pending.
            log.warning(
                "%s: deploy failed; armed rollback will restore the backup "
                "and reboot within %ss",
                self.hostname,
                self.ROLLBACK_TIMEOUT,
            )
            raise
        self._cancel_rollback()

    def _arm_rollback(self) -> None:
        """Snapshot the box and arm the one-shot restore scheduler.

        A true one-shot: ``interval=0`` fired at an absolute start-time
        ``ROLLBACK_TIMEOUT`` seconds in the router's future (a plain
        ``interval=Ns`` scheduler repeats forever -- verified live). The
        start-time is computed from the device clock, not ours, so clock
        skew cannot shorten or stretch the window.
        """
        self._api_remove_named("system/scheduler", _ROLLBACK_JOB)
        self._delete_backup(_ROLLBACK_JOB)
        self._save_backup(_ROLLBACK_JOB)

        clock = self._api_get("system/clock")
        start_time, start_date = ros_deploy.schedule_start(
            clock["time"], clock["date"], self.ROLLBACK_TIMEOUT
        )
        self._api_add(
            "system/scheduler",
            {
                "name": _ROLLBACK_JOB,
                "interval": "0",
                "start-time": start_time,
                "start-date": start_date,
                "on-event": ros_deploy.build_rollback_script(_ROLLBACK_JOB),
            },
        )

    def _apply_forward(self, apply_cmds: List[str]) -> None:
        """Run the forward changes as one on-device script."""
        self._api_remove_named("system/script", _APPLY_SCRIPT)
        self._api_add(
            "system/script",
            {"name": _APPLY_SCRIPT, "source": "\n".join(apply_cmds)},
        )
        self._api_run_script(_APPLY_SCRIPT)

    def _confirm_converged(self, desired: dict) -> None:
        """Re-probe the box; raise unless reachable and diff-empty.

        A raised RuntimeError (or an unreachable device, which makes the
        re-fetch raise) leaves the rollback armed.
        """
        diff = ros_config.diff_items(desired, self.device_items())
        if diff.has_changes:
            raise RuntimeError(
                f"{self.hostname}: config did not converge after apply "
                f"({ros_config.summarize_diff(diff)})"
            )

    def _cancel_rollback(self) -> None:
        """Healthy deploy: disarm the timer, drop the backup and script."""
        self._api_remove_named("system/scheduler", _ROLLBACK_JOB)
        self._delete_backup(_ROLLBACK_JOB)
        self._api_remove_named("system/script", _APPLY_SCRIPT)

    def _save_backup(self, name: str) -> None:
        """Save a full unencrypted binary backup named ``<name>.backup``."""
        ros_api.ros_api_request(
            "POST",
            self.require_management_ip(),
            self.API_USERNAME,
            self._api_password(),
            "system/backup/save",
            body={"name": name, "dont-encrypt": "yes"},
            port=self.API_PORT,
        )

    def _delete_backup(self, name: str) -> None:
        """Delete the ``<name>.backup`` file if it exists (a no-op if not)."""
        filename = f"{name}.backup"
        for item in self._api_get("file") or []:
            if item.get("name") == filename and item.get(".id"):
                self._api_delete("file", item[".id"])
