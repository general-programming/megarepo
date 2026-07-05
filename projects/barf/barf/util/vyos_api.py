import json
import logging
import ssl
import urllib.parse
import urllib.request

log = logging.getLogger(__name__)


def vyos_api_show(host: str, key: str, path: list, timeout: int = 10) -> str:
    """Run a VyOS operational ``show`` command via the HTTPS API.

    Args:
        host (str): Hostname or address to reach the device on.
        key (str): The API key (the ``vaultadmin`` key from Vault).
        path (list): The ``show`` command path, e.g. ``["host", "name"]``.
        timeout (int): Socket timeout in seconds.

    Returns:
        str: The command's raw text output.

    Raises:
        RuntimeError: If the API reports a failure.
    """
    # Wrap bare IPv6 literals (e.g. management loopbacks) for the URL.
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"

    url = f"https://{host}/show"
    body = urllib.parse.urlencode(
        {
            "data": json.dumps({"op": "show", "path": path}),
            "key": key,
        }
    ).encode()

    # VyOS ships a self-signed cert by default, so skip verification.
    ctx = ssl.create_default_context()
    ctx.check_hostname = False
    ctx.verify_mode = ssl.CERT_NONE

    log.debug("POST %s op=show path=%s", url, path)
    req = urllib.request.Request(url, data=body, method="POST")
    with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
        result = json.load(resp)

    if not result.get("success"):
        raise RuntimeError(result.get("error") or "unknown API error")

    return result.get("data") or ""
