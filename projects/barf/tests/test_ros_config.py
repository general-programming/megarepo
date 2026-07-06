"""RouterOS parse/ownership/diff model (barf/vendors/mikrotik/ros_config.py).

Device fixtures mirror sea420-acc-v-hv2's real inventory (2026-07-05,
keys anonymized): three hand-named spine links sharing barf's derived
ports and Vault keys, a foreign transit link (linuxgemini1, port 63666,
AS 4280806675) that barf must never touch, and plenty of unrelated
config (bridges, dynamic addresses, bogons lists) proving the scoping.
"""

from barf.vendors.mikrotik.ros_config import (
    diff_items,
    format_diff,
    parse_ros_commands,
    summarize_diff,
)

RENDERED = """
# comment line
/interface/wireguard add name=wg51078 listen-port=51078 mtu=1420 private-key="PRIV-A" comment="barf: fmt2-vpn-spine-1 -> sea420"
/interface/wireguard/peers add interface=wg51078 name=fmt2-vpn-spine-1 public-key="PUB-SPINE-A" allowed-address=0.0.0.0/0,::/0 endpoint-address=2a0d:1a43:8008:420::1 endpoint-port=51078 persistent-keepalive=10s
/ip/address add address=172.31.255.45/31 interface=wg51078 comment="barf: fmt2-vpn-spine-1 link"
/interface/wireguard add name=wg51913 listen-port=51913 mtu=1420 private-key="PRIV-D" comment="barf: sea1-vpn-spine-2 -> sea420"
/interface/wireguard/peers add interface=wg51913 name=sea1-vpn-spine-2 public-key="PUB-SPINE-D" allowed-address=0.0.0.0/0,::/0
/ip/address add address=172.31.255.17/31 interface=wg51913
/routing/filter/rule add chain=genprog-in-sea comment="barf: origin site 1" rule="if (bgp-large-communities includes 4280805525:1:1) { set bgp-local-pref 1000000; accept }"
/routing/filter/rule add chain=genprog-in-sea comment="barf: untagged fallthrough" rule="accept"
/routing/bgp/template add name=genprog-fabric as=4280805533 hold-time=30s keepalive-time=10s output.network=genprog-networks
/routing/bgp/connection add name=fmt2-vpn-spine-1 templates=genprog-fabric remote.address=172.31.255.44 remote.as=4280805525 local.address=172.31.255.45 local.role=ebgp input.filter=genprog-in-fmt2 output.filter-chain=genprog-out comment="barf: fmt2-vpn-spine-1"
/ip/firewall/address-list add list=genprog-networks address=10.3.0.0/23 comment="barf: announced to the fabric"
"""

DEVICE = {
    "interface/wireguard": [
        # Hand-named, same derived port and Vault key as barf renders.
        {
            ".id": "*A",
            "name": "fmt2-spine-1",
            "listen-port": "51078",
            "mtu": "1420",
            "private-key": "PRIV-A",
            "running": "true",
        },
        # Foreign transit link: out of the fabric port band -> kept.
        {
            ".id": "*D",
            "name": "linuxgemini1",
            "listen-port": "63666",
            "mtu": "1420",
            "private-key": "PRIV-GEMINI",
            "running": "true",
        },
    ],
    "interface/wireguard/peers": [
        {
            ".id": "*1",
            "interface": "fmt2-spine-1",
            "name": "peer1",
            "public-key": "PUB-SPINE-A",
            "allowed-address": "0.0.0.0/0",
            "endpoint-address": "79.110.170.6",
            "endpoint-port": "51078",
            "persistent-keepalive": "10s",
            "dynamic": "false",
        },
        {
            ".id": "*4",
            "interface": "linuxgemini1",
            "name": "peer4",
            "public-key": "PUB-GEMINI",
            "allowed-address": "0.0.0.0/0,::/0",
            "endpoint-address": "91.107.210.194",
            "dynamic": "false",
        },
    ],
    "ip/address": [
        {
            ".id": "*2",
            "address": "10.3.0.1/23",
            "interface": "bridge-internal",
            "dynamic": "false",
        },
        {
            ".id": "*5",
            "address": "172.31.255.45/31",
            "interface": "fmt2-spine-1",
            "dynamic": "false",
        },
        {
            ".id": "*9",
            "address": "172.22.255.3/31",
            "interface": "linuxgemini1",
            "dynamic": "false",
        },
        {
            ".id": "*1F",
            "address": "10.255.255.116/24",
            "interface": "MANAGEMENT",
            "dynamic": "true",
        },
    ],
    "routing/bgp/template": [
        {
            ".id": "*1",
            "name": "default",
            "as": "4280805533",
            "output.filter-chain": "export",
        },
    ],
    "routing/bgp/connection": [
        {
            ".id": "*1",
            "name": "bgp-spine1",
            "templates": "default",
            "remote.address": "172.31.255.44",
            "remote.as": "4280805525",
            "local.role": "ebgp",
            "multihop": "true",
            "output.filter-chain": "export",
        },
        # Foreign AS outside the fabric /31 pool -> kept.
        {
            ".id": "*3",
            "name": "bgp-linuxgemini1",
            "templates": "default",
            "remote.address": "172.22.255.2",
            "remote.as": "4280806675",
            "local.role": "ebgp",
        },
    ],
    "routing/filter/rule": [
        {".id": "*5", "chain": "export", "rule": "if (dst-len != 0) {accept}"},
    ],
    "ip/firewall/address-list": [
        {".id": "*1", "list": "bogons", "address": "10.0.0.0/8"},
    ],
}


def diff():
    return diff_items(parse_ros_commands(RENDERED), DEVICE)


class TestParse:
    def test_only_modeled_add_lines(self):
        parsed = parse_ros_commands(RENDERED)
        assert len(parsed["interface/wireguard"]) == 2
        assert len(parsed["routing/filter/rule"]) == 2
        # Comment lines and unmodeled commands are ignored.
        assert "system/identity" not in parsed

    def test_quoted_values_with_spaces(self):
        parsed = parse_ros_commands(RENDERED)
        rule = parsed["routing/filter/rule"][0]
        assert rule["chain"] == "genprog-in-sea"
        assert rule["rule"].startswith("if (bgp-large-communities includes")

    def test_ignores_free_form_lines(self):
        parsed = parse_ros_commands("/system/reboot\nnot-a-command\n")
        assert parsed == {}


class TestOwnership:
    def test_foreign_wireguard_link_is_kept(self):
        d = diff()
        # linuxgemini1 (port 63666) is out of band: not removed even
        # though it is not rendered, and its peer/address ride along.
        removed = [key for _path, key in d.removed]
        assert "63666" not in removed
        assert "PUB-GEMINI" not in removed
        assert "172.22.255.3/31" not in removed

    def test_foreign_bgp_and_lan_config_kept(self):
        d = diff()
        removed = [key for _path, key in d.removed]
        assert "172.22.255.2" not in removed  # linuxgemini BGP session
        assert "10.3.0.1/23" not in removed  # LAN bridge address
        assert "default" not in removed  # hand BGP template
        # Hand `export` chain rules and bogons lists are invisible.
        assert all("export" not in key for key in removed)
        assert all("bogons" not in key for key in removed)

    def test_dynamic_items_are_state_not_config(self):
        d = diff()
        removed = [key for _path, key in d.removed]
        assert "10.255.255.116/24" not in removed


class TestDiff:
    def test_adoption_by_natural_key_yields_changes_not_recreates(self):
        d = diff()
        # wg51078 exists on device (as fmt2-spine-1, same port): it is
        # adopted, so no add for it -- only property changes.
        wg_adds = [props for path, props in d.added if path == "interface/wireguard"]
        assert len(wg_adds) == 1
        assert wg_adds[0]["listen-port"] == "51913"

        changes = {
            (path, key): dict((p, (o, n)) for p, o, n in deltas)
            for path, key, deltas in d.changed
        }
        wg_change = changes[("interface/wireguard", "51078")]
        assert wg_change["name"] == ("fmt2-spine-1", "wg51078")

    def test_peer_matched_by_public_key(self):
        d = diff()
        changes = {
            (path, key): dict((p, (o, n)) for p, o, n in deltas)
            for path, key, deltas in d.changed
        }
        peer = changes[("interface/wireguard/peers", "PUB-SPINE-A")]
        assert peer["endpoint-address"] == ("79.110.170.6", "2a0d:1a43:8008:420::1")
        assert peer["allowed-address"] == ("0.0.0.0/0", "0.0.0.0/0,::/0")

    def test_unrendered_device_props_left_alone(self):
        d = diff()
        changes = {
            (path, key): dict((p, (o, n)) for p, o, n in deltas)
            for path, key, deltas in d.changed
        }
        conn = changes[("routing/bgp/connection", "172.31.255.44")]
        # multihop=true is device-side only: not rendered, not diffed.
        assert "multihop" not in conn
        assert conn["templates"] == ("default", "genprog-fabric")
        assert conn["input.filter"] == (None, "genprog-in-fmt2")

    def test_new_items_are_adds(self):
        d = diff()
        added = {
            (path, props.get("name") or props.get("list") or props.get("chain"))
            for path, props in d.added
        }
        assert ("routing/bgp/template", "genprog-fabric") in added
        assert ("routing/filter/rule", "genprog-in-sea") in added
        assert ("ip/firewall/address-list", "genprog-networks") in added

    def test_format_redacts_private_keys(self):
        text = format_diff(diff(), redact=True)
        assert "PRIV-D" not in text
        assert "<redacted>" in text
        assert format_diff(diff(), redact=False).count("PRIV-D") == 1

    def test_summary_counts(self):
        d = diff()
        summary = summarize_diff(d)
        assert summary.startswith("+")
        assert "~" in summary

    def test_converged_devices_diff_empty(self):
        parsed = parse_ros_commands(RENDERED)
        # Round-trip: pretend the device holds exactly what we render.
        device = {
            path: [dict(item) for item in items] for path, items in parsed.items()
        }
        d = diff_items(parsed, device)
        assert not d.has_changes
        assert summarize_diff(d) == "no changes"

    def test_redact_covers_changed_private_keys(self):
        # A rotated key on an adopted interface lands in `changed`;
        # redact=True must not leak either side of the delta.
        rotated = {
            "interface/wireguard": [
                {
                    ".id": "*A",
                    "name": "wg51078",
                    "listen-port": "51078",
                    "private-key": "OLD-SECRET",
                },
            ]
        }
        d = diff_items(parse_ros_commands(RENDERED), rotated)
        text = format_diff(d, redact=True)
        assert "OLD-SECRET" not in text and "PRIV-A" not in text
        assert "private-key: <redacted> -> <redacted>" in text
