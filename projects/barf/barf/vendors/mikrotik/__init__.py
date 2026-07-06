from typing import Any, Optional

from barf.util.secrets import VaultSecrets
from barf.vendors import BaseHost, DeployDiff
from barf.vendors.mikrotik import ros_api, ros_config


class MikroTikHost(BaseHost):
    DEVICETYPE = "mikrotik"

    # REST rides www-ssl; 443 is taken by RouterOS's reverse-proxy
    # service on sea420, so barf's www-ssl (barf-ssl cert) sits here.
    API_PORT = 8443
    API_USERNAME = "barf"

    @property
    def management_ip(self) -> Optional[str]:
        """First endpoint answering on the REST port (not 443)."""
        return self._probe_endpoint(self.API_PORT)

    def _api_get(self, path: str) -> Any:
        return ros_api.ros_api_get(
            self.require_management_ip(),
            self.API_USERNAME,
            VaultSecrets().mikrotik_api_password,
            path,
            port=self.API_PORT,
        )

    def device_items(self) -> dict:
        """Every owned collection's items, straight off the REST API."""
        return {path: self._api_get(path) for path in ros_config.COLLECTIONS}

    def diff_config(
        self, rendered: str, *, redact: bool = True, show_device_only: bool = False
    ) -> DeployDiff:
        """Diff rendered RouterOS CLI against the device, locally.

        ``show_device_only`` has no meaning here: unowned items are
        invisible by design (see barf.vendors.mikrotik.ros_config ownership).
        """
        desired = ros_config.parse_ros_commands(rendered)
        diff = ros_config.diff_items(desired, self.device_items())
        return DeployDiff(
            text=ros_config.format_diff(diff, redact=redact),
            has_changes=diff.has_changes,
            summary=ros_config.summarize_diff(diff),
        )
