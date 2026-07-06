"""Minimal RouterOS v7 REST API client.

Read-only for now (config diffing); write verbs arrive with deploy.
The REST API rides the ``www-ssl`` service — on sea420 that is port
8443 with the self-signed ``barf-ssl`` certificate (443 is occupied by
RouterOS's reverse-proxy service), so verification is skipped like the
VyOS client does.
"""

import base64
import json
import logging
import ssl
import urllib.request
from typing import Any

log = logging.getLogger(__name__)

# The fleet's RouterOS boxes serve self-signed certs (barf-ssl), so
# verification is skipped -- one shared context, not one per request.
_SSL_CTX = ssl.create_default_context()
_SSL_CTX.check_hostname = False
_SSL_CTX.verify_mode = ssl.CERT_NONE


def ros_api_get(
    host: str,
    username: str,
    password: str,
    path: str,
    port: int = 8443,
    timeout: int = 10,
) -> Any:
    """GET a RouterOS REST resource, e.g. ``interface/wireguard``.

    Returns:
        The decoded JSON: a list of item dicts for collections, a dict
        for single resources. All values are strings, RouterOS-style.
    """
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"

    url = f"https://{host}:{port}/rest/{path}"

    token = base64.b64encode(f"{username}:{password}".encode()).decode()
    req = urllib.request.Request(url, headers={"Authorization": f"Basic {token}"})

    log.debug("GET %s", url)
    with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as resp:
        return json.load(resp)
