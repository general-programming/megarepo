import json
import logging
import re
import ssl
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Optional

log = logging.getLogger(__name__)


def _vyos_api_request(
    host: str, key: str, endpoint: str, payload: dict, timeout: int = 10
):
    """POST a request to the VyOS HTTPS API.

    Args:
        host: Hostname or address to reach the device on.
        key: The API key (the ``vaultadmin`` key from Vault).
        endpoint: API endpoint, e.g. ``show`` or ``image``.
        payload: The request body, e.g. ``{"op": "show", ...}``.
        timeout: Socket timeout in seconds.

    Returns:
        The command's output: raw text for op-mode endpoints, a JSON
        tree (dict) for ``/retrieve``.

    Raises:
        RuntimeError: If the API reports a failure.
    """
    # Wrap bare IPv6 literals (e.g. management loopbacks) for the URL.
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"

    url = f"https://{host}/{endpoint}"
    body = urllib.parse.urlencode(
        {
            "data": json.dumps(payload),
            "key": key,
        }
    ).encode()

    # VyOS ships a self-signed cert by default, so skip verification.
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    log.debug("POST %s payload=%s", url, payload)
    req = urllib.request.Request(url, data=body, method="POST")
    with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
        result = json.load(resp)

    if not result.get("success"):
        raise RuntimeError(result.get("error") or "unknown API error")

    return result.get("data") or ""


def vyos_api_show(host: str, key: str, path: list, timeout: int = 10) -> str:
    """Run a VyOS operational ``show`` command via the HTTPS API.

    Args:
        path: The ``show`` command path, e.g. ``["host", "name"]``.
    """
    return _vyos_api_request(
        host, key, "show", {"op": "show", "path": path}, timeout=timeout
    )


def vyos_api_retrieve_config(
    host: str, key: str, path: Optional[list] = None, timeout: int = 30
) -> dict:
    """Fetch (part of) the running config as a JSON tree via ``/retrieve``.

    Args:
        path: Config path to fetch, None/empty for the whole config.
    """
    data = _vyos_api_request(
        host, key, "retrieve", {"op": "showConfig", "path": path or []}, timeout=timeout
    )
    if not isinstance(data, dict):
        raise RuntimeError(f"unexpected /retrieve payload: {data!r}")
    return data


def vyos_api_image_delete(host: str, key: str, name: str, timeout: int = 60) -> str:
    """Delete an installed system image via the HTTPS API."""
    return _vyos_api_request(
        host, key, "image", {"op": "delete", "name": name}, timeout=timeout
    )


@dataclass
class SystemImage:
    name: str
    default_boot: bool = False
    running: bool = False


def parse_system_images(output: str) -> list[SystemImage]:
    """Parse ``show system image`` output into image entries.

    Handles both the modern table format (Name / Default boot / Running
    columns) and the legacy numbered list format
    (``1: name (default boot) (running image)``).
    """
    lines = output.splitlines()

    for i, line in enumerate(lines):
        if line.strip().startswith("Name") and "Running" in line:
            default_col = line.index("Default")
            running_col = line.index("Running")

            images = []
            for row in lines[i + 1 :]:
                if not row.strip() or set(row.strip()) <= {"-", " "}:
                    continue
                images.append(
                    SystemImage(
                        name=row.split()[0],
                        default_boot=row[default_col:running_col]
                        .strip()
                        .lower()
                        .startswith("yes"),
                        running=row[running_col:].strip().lower().startswith("yes"),
                    )
                )
            return images

    images = []
    for line in lines:
        numbered = re.match(r"^\s*\d+:\s+(\S+)(.*)$", line)
        if numbered:
            name, rest = numbered.groups()
            images.append(
                SystemImage(
                    name=name,
                    default_boot="default boot" in rest,
                    running="running" in rest,
                )
            )
    return images
