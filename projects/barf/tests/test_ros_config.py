"""RouterOS parse/ownership/diff model (barf/vendors/mikrotik/ros_config.py).

Device fixtures mirror sea420-acc-v-hv2's real inventory (2026-07-05,
keys anonymized): three hand-named spine links sharing barf's derived
ports and Vault keys, a foreign transit link (linuxgemini1, port 63666,
AS 4280806675) that barf must never touch, and plenty of unrelated
config (bridges, dynamic addresses, bogons lists) proving the scoping.
"""

from barf.vendors.mikrotik.ros_config import (
    diff_items,
    excluded_items,
    format_diff,
    format_excluded,
    parse_ros_commands,
    rendered_bridge_names,
    rendered_connection_ids,
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
        # This RENDERED models no transit, so the linuxgemini session
        # (172.22.255.2) is not a rendered connection -> kept.
        assert "172.22.255.2" not in removed  # linuxgemini BGP session
        assert "10.3.0.1/23" not in removed  # LAN bridge address
        assert "default" not in removed  # hand BGP template (non-genprog)
        assert all("bogons" not in key for key in removed)  # address-list kept

    def test_unrendered_filter_chains_are_removed(self):
        # routing/filter/rule is fully owned: a chain barf does not
        # render (the hand `export` chain) is removed, not kept.
        d = diff()
        removed = [key for _path, key in d.removed]
        assert any(key.startswith("export#") for key in removed)

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


class TestUnnumbered:
    RENDERED = """
/interface/wireguard add name=wg51911 listen-port=51911 mtu=1420 private-key="PRIV-C"
/ipv6/nd add interface=wg51911 ra-interval=10s-15s comment="barf: unnumbered"
/ipv6/nd/prefix add prefix=none interface=wg51911 comment="barf: unnumbered"
/routing/bgp/connection add name=sea1-vpn-spine-1 templates=genprog-fabric local.address=wg51911 local.role=ebgp remote.as=4280805529 afi=ip,ipv6
"""

    DEVICE = {
        "interface/wireguard": [
            {
                ".id": "*F",
                "name": "wg51911",
                "listen-port": "51911",
                "mtu": "1420",
                "private-key": "PRIV-C",
            },
        ],
        "ipv6/nd": [
            # RouterOS default entry and a hand LAN entry: never barf's.
            {".id": "*1", "interface": "all", "default": "true", "disabled": "true"},
            {".id": "*2", "interface": "bridge-internal"},
        ],
        "ipv6/nd/prefix": [
            {
                ".id": "*1",
                "prefix": "fd94::/64",
                "interface": "bridge-internal",
                "dynamic": "true",
            },
        ],
        "routing/bgp/connection": [
            # Adopted unnumbered connection: identity by local interface.
            {
                ".id": "*5",
                "name": "sea1-vpn-spine-1",
                "templates": "genprog-fabric",
                "local.address": "wg51911",
                "local.role": "ebgp",
                "remote.as": "4280805529",
                "afi": "ip,ipv6",
            },
            # Foreign numbered session stays kept.
            {
                ".id": "*3",
                "name": "bgp-linuxgemini1",
                "remote.address": "172.22.255.2",
                "remote.as": "4280806675",
            },
        ],
    }

    def test_unnumbered_connection_adopted_by_interface(self):
        d = diff_items(parse_ros_commands(self.RENDERED), self.DEVICE)
        # The connection matches by local.address -> no add, no change.
        assert not [p for p, _props in d.added if p == "routing/bgp/connection"]
        removed = [key for _p, key in d.removed]
        assert "172.22.255.2" not in removed  # foreign session kept

    def test_nd_entries_owned_only_on_fabric_interfaces(self):
        d = diff_items(parse_ros_commands(self.RENDERED), self.DEVICE)
        added = [(p, props.get("interface")) for p, props in d.added]
        assert ("ipv6/nd", "wg51911") in added
        assert ("ipv6/nd/prefix", "wg51911") in added
        removed = [key for _p, key in d.removed]
        # The disabled default (interface=all) and LAN entries are kept.
        assert "all" not in removed
        assert "bridge-internal" not in removed


class TestExcluded:
    """The exclusion (kept) set surfaced for --show-device-only."""

    def _kept(self):
        parsed = parse_ros_commands(RENDERED)
        return {
            (path, label)
            for path, label, _item in excluded_items(
                DEVICE,
                rendered_bridge_names(parsed),
                rendered_connection_ids(parsed),
            )
        }

    def test_hand_managed_items_are_reported_kept(self):
        kept = self._kept()
        # Foreign transit tunnel, its peer/address and (unrendered here)
        # BGP session.
        assert ("interface/wireguard", "63666") in kept
        assert ("interface/wireguard/peers", "PUB-GEMINI") in kept
        assert ("ip/address", "172.22.255.3/31") in kept
        assert ("routing/bgp/connection", "172.22.255.2") in kept
        # Hand LAN address, default BGP template, bogons. (The `export`
        # chain is now fully owned -> removed, not kept.)
        assert ("ip/address", "10.3.0.1/23") in kept
        assert ("routing/bgp/template", "default") in kept
        assert ("ip/firewall/address-list", "bogons:10.0.0.0/8") in kept
        assert not any(path == "routing/filter/rule" for path, _label in kept)

    def test_barf_owned_items_are_not_reported_kept(self):
        kept = self._kept()
        # Fabric-band WG, its fabric address, and the in-pool session
        # are owned, never listed as device-only.
        assert ("interface/wireguard", "51078") not in kept
        assert ("ip/address", "172.31.255.45/31") not in kept
        assert ("routing/bgp/connection", "172.31.255.44") not in kept

    def test_dynamic_items_are_never_reported_kept(self):
        # Dynamic device state is neither owned nor kept -- invisible.
        labels = {label for _path, label, _item in excluded_items(DEVICE)}
        assert "10.255.255.116/24" not in labels

    def test_format_excluded_marks_device_only(self):
        text = format_excluded(DEVICE)
        assert "device-only: hand-managed, kept" in text
        assert "/interface/wireguard [63666]" in text


class TestBridges:
    """A modeled bridge and its address become owned; others stay kept."""

    RENDERED = """
/interface/bridge add name=bridge-internal comment="barf: internal LAN"
/ip/address add address=10.3.0.1/23 interface=bridge-internal comment="barf: bridge-internal address"
"""
    DEVICE = {
        "interface/bridge": [
            # Adopted post-deploy state: barf's comment already present.
            {".id": "*b1", "name": "bridge-internal", "comment": "barf: internal LAN"},
            # Hand-created bridge barf does not model -> kept.
            {".id": "*b2", "name": "bridge-guest"},
        ],
        "ip/address": [
            {
                ".id": "*2",
                "address": "10.3.0.1/23",
                "interface": "bridge-internal",
                "comment": "barf: bridge-internal address",
                "dynamic": "false",
            },
            # Address on a bridge barf does not model -> kept.
            {
                ".id": "*3",
                "address": "10.9.0.1/24",
                "interface": "bridge-guest",
                "dynamic": "false",
            },
        ],
    }

    def _diff(self):
        return diff_items(parse_ros_commands(self.RENDERED), self.DEVICE)

    def test_modeled_bridge_adopted_not_recreated(self):
        d = self._diff()
        assert not [p for p, _ in d.added if p == "interface/bridge"]
        assert "bridge-internal" not in [k for _p, k in d.removed]

    def test_bridge_address_becomes_owned_and_converges(self):
        d = self._diff()
        # 10.3.0.1/23 on the modeled bridge matches -> no add, no remove.
        assert not d.has_changes

    def test_foreign_bridge_and_its_address_kept(self):
        d = self._diff()
        removed = [k for _p, k in d.removed]
        assert "bridge-guest" not in removed
        assert "10.9.0.1/24" not in removed
        bn = rendered_bridge_names(parse_ros_commands(self.RENDERED))
        kept = {(p, label) for p, label, _i in excluded_items(self.DEVICE, bn)}
        assert ("interface/bridge", "bridge-guest") in kept
        assert ("ip/address", "10.9.0.1/24") in kept
        # The owned bridge + its address are NOT listed as device-only.
        assert ("ip/address", "10.3.0.1/23") not in kept

    def test_without_bridge_names_nothing_bridge_is_owned(self):
        # Default (no rendered bridges) keeps every bridge address --
        # the behavior-preserving baseline.
        kept = {(p, label) for p, label, _i in excluded_items(self.DEVICE)}
        assert ("ip/address", "10.3.0.1/23") in kept
        assert ("interface/bridge", "bridge-internal") in kept


class TestBridgePorts:
    """Ports on a modeled bridge are owned; ports on others stay kept."""

    RENDERED = """
/interface/bridge add name=bridge-internal
/interface/bridge/port add bridge=bridge-internal interface=ether-pcie2
"""
    DEVICE = {
        "interface/bridge": [
            {".id": "*b1", "name": "bridge-internal"},
            {".id": "*b2", "name": "bridge-guest"},
        ],
        "interface/bridge/port": [
            {".id": "*p1", "bridge": "bridge-internal", "interface": "ether-pcie2"},
            # A member barf did not model on an owned bridge -> removed.
            {".id": "*p2", "bridge": "bridge-internal", "interface": "ether-stray"},
            # A port on a bridge barf does not model -> kept.
            {".id": "*p3", "bridge": "bridge-guest", "interface": "ether-pcie9"},
        ],
    }

    def _diff(self):
        return diff_items(parse_ros_commands(self.RENDERED), self.DEVICE)

    def test_modeled_port_adopted(self):
        d = self._diff()
        added = [
            props.get("interface")
            for p, props in d.added
            if p == "interface/bridge/port"
        ]
        assert "ether-pcie2" not in added  # already present -> adopted

    def test_unmodeled_port_on_owned_bridge_removed(self):
        d = self._diff()
        assert ("interface/bridge/port", "ether-stray") in d.removed

    def test_port_on_foreign_bridge_kept(self):
        d = self._diff()
        assert ("interface/bridge/port", "ether-pcie9") not in d.removed
        bn = rendered_bridge_names(parse_ros_commands(self.RENDERED))
        kept = {(p, label) for p, label, _i in excluded_items(self.DEVICE, bn)}
        assert ("interface/bridge/port", "ether-pcie9") in kept


class TestRosDefaultsIgnored:
    """RouterOS built-in default=true entries are ignored, not kept."""

    DEVICE = {
        "routing/bgp/template": [
            {
                ".id": "*1",
                "name": "default",
                "default": "true",
                "output.filter-chain": "export",
            },
            {".id": "*2", "name": "genprog-fabric"},
        ],
    }

    def test_default_template_not_removed_and_not_listed(self):
        # Not owned (so a rendered config that omits it never deletes
        # it) and not surfaced as a shrink target.
        d = diff_items(
            {"routing/bgp/template": [{"name": "genprog-fabric"}]}, self.DEVICE
        )
        assert "default" not in [k for _p, k in d.removed]
        kept = {label for _p, label, _i in excluded_items(self.DEVICE)}
        assert "default" not in kept


class TestIgnored:
    """RouterOS defaults are dropped entirely, VyOS-IGNORED style."""

    DEVICE = {
        "ipv6/nd": [
            {".id": "*1", "interface": "all", "default": "true", "disabled": "true"},
            {".id": "*2", "interface": "bridge-internal"},
        ],
    }

    def test_nd_all_default_is_neither_owned_nor_listed(self):
        # Not removed (not owned) ...
        d = diff_items({}, self.DEVICE)
        assert "all" not in [k for _p, k in d.removed]
        # ... and not shown as a device-only shrink target.
        kept = {label for _p, label, _i in excluded_items(self.DEVICE)}
        assert "all" not in kept
        # A real hand ND entry is still surfaced as kept.
        assert "bridge-internal" in kept
