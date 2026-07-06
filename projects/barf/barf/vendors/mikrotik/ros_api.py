"""Minimal RouterOS v7 REST API client.

GET for config diffing plus the write verbs (PUT add, PATCH update,
DELETE remove, POST command) that back the rollback-guarded deploy.
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
from typing import Any, Optional

log = logging.getLogger(__name__)

# The fleet's RouterOS boxes serve self-signed certs (barf-ssl), so
# verification is skipped -- one shared context, not one per request.
_SSL_CTX = ssl.create_default_context()
_SSL_CTX.check_hostname = False
_SSL_CTX.verify_mode = ssl.CERT_NONE


def ros_api_request(
    method: str,
    host: str,
    username: str,
    password: str,
    path: str,
    body: Optional[dict] = None,
    port: int = 8443,
    timeout: int = 10,
) -> Any:
    """Issue one RouterOS REST request and decode the JSON reply.

    Args:
        method: HTTP verb -- GET (read), PUT (add), PATCH (update),
            DELETE (remove), or POST (run a command).
        path: REST path after ``/rest/`` (e.g. ``interface/wireguard``
            for a collection, ``system/scheduler/*ID`` for one item).
        body: JSON payload for PUT/PATCH/POST; omitted for GET/DELETE.

    Returns:
        The decoded JSON reply, or None when the device answers with an
        empty body (RouterOS does this for some DELETEs).
    """
    if ":" in host and not host.startswith("["):
        host = f"[{host}]"

    url = f"https://{host}:{port}/rest/{path}"

    data = None
    headers = {"Authorization": f"Basic {_basic(username, password)}"}
    if body is not None:
        data = json.dumps(body).encode()
        headers["Content-Type"] = "application/json"

    req = urllib.request.Request(url, data=data, headers=headers, method=method)

    log.debug("%s %s", method, url)
    with urllib.request.urlopen(req, timeout=timeout, context=_SSL_CTX) as resp:
        raw = resp.read()
    return json.loads(raw) if raw else None


def _basic(username: str, password: str) -> str:
    return base64.b64encode(f"{username}:{password}".encode()).decode()


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
    return ros_api_request(
        "GET", host, username, password, path, port=port, timeout=timeout
    )


def ros_api_add(
    host: str,
    username: str,
    password: str,
    collection: str,
    props: dict,
    port: int = 8443,
    timeout: int = 10,
) -> Any:
    """Create an item in ``collection`` (REST PUT); returns the new item."""
    return ros_api_request(
        "PUT",
        host,
        username,
        password,
        collection,
        body=props,
        port=port,
        timeout=timeout,
    )


def ros_api_set(
    host: str,
    username: str,
    password: str,
    collection: str,
    item_id: str,
    props: dict,
    port: int = 8443,
    timeout: int = 10,
) -> Any:
    """Update the item ``item_id`` in ``collection`` (REST PATCH)."""
    return ros_api_request(
        "PATCH",
        host,
        username,
        password,
        f"{collection}/{item_id}",
        body=props,
        port=port,
        timeout=timeout,
    )


def ros_api_delete(
    host: str,
    username: str,
    password: str,
    collection: str,
    item_id: str,
    port: int = 8443,
    timeout: int = 10,
) -> Any:
    """Remove the item ``item_id`` from ``collection`` (REST DELETE)."""
    return ros_api_request(
        "DELETE",
        host,
        username,
        password,
        f"{collection}/{item_id}",
        port=port,
        timeout=timeout,
    )
