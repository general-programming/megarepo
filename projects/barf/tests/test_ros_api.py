"""RouterOS REST client (barf/vendors/mikrotik/ros_api.py)."""

import io
import json
import urllib.request

import pytest

from barf.vendors.mikrotik import ros_api


@pytest.fixture
def captured(monkeypatch):
    calls = {}

    def fake_urlopen(req, timeout=None, context=None):
        calls["url"] = req.full_url
        calls["auth"] = req.get_header("Authorization")
        calls["timeout"] = timeout
        calls["context"] = context
        return io.StringIO(json.dumps([{"name": "wg51078"}]))

    monkeypatch.setattr(urllib.request, "urlopen", fake_urlopen)
    return calls


def test_get_builds_rest_url_with_auth(captured):
    items = ros_api.ros_api_get("10.3.0.1", "barf", "pw", "interface/wireguard")
    assert items == [{"name": "wg51078"}]
    assert captured["url"] == "https://10.3.0.1:8443/rest/interface/wireguard"
    # basic auth for barf:pw
    assert captured["auth"] == "Basic YmFyZjpwdw=="
    # the shared unverified-TLS context (self-signed barf-ssl cert)
    assert captured["context"] is ros_api._SSL_CTX


def test_ipv6_hosts_are_bracketed(captured):
    ros_api.ros_api_get("2602:fa6d:f::1", "barf", "pw", "ip/address", port=443)
    assert captured["url"] == "https://[2602:fa6d:f::1]:443/rest/ip/address"
