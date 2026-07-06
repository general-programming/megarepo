"""Vendor-neutral firewall model (barf/util/firewall.py)."""

from barf.util.firewall import parse_firewall


class TestAddressGroups:
    def test_bare_strings_and_dicts_mix(self):
        fw = parse_firewall(
            {
                "groups": {
                    "address": {
                        "rfc1918": ["10.0.0.0/8", "192.168.0.0/16"],
                        "trusted": [
                            {"address": "79.110.170.0/24", "comment": "hq"},
                            "10.3.6.0/27",
                        ],
                    }
                }
            }
        )
        assert fw.address_names == ["rfc1918", "trusted"]
        trusted = fw.address[1]
        assert trusted.members[0].address == "79.110.170.0/24"
        assert trusted.members[0].comment == "hq"
        assert trusted.members[1].comment is None

    def test_members_split_by_family(self):
        fw = parse_firewall(
            {
                "groups": {
                    "address": {
                        "trusted": ["10.3.6.0/27", "2602:fa6d:10::/48"],
                    }
                }
            }
        )
        group = fw.address[0]
        assert [m.address for m in group.members_v(4)] == ["10.3.6.0/27"]
        assert [m.address for m in group.members_v(6)] == ["2602:fa6d:10::/48"]

    def test_bare_host_address_has_a_family(self):
        fw = parse_firewall({"groups": {"address": {"g": ["1.2.3.4", "::1"]}}})
        group = fw.address[0]
        assert len(group.members_v(4)) == 1
        assert len(group.members_v(6)) == 1


class TestInterfaceGroups:
    def test_flat_list_and_nested_include(self):
        fw = parse_firewall(
            {
                "groups": {
                    "interface": {
                        "wg-tunnels": ["wg1", "wg2"],
                        "internal": {
                            "interfaces": ["bridge0"],
                            "include": ["wg-tunnels"],
                        },
                    }
                }
            }
        )
        assert fw.interface_names == ["wg-tunnels", "internal"]
        assert fw.interface[1].interfaces == ["bridge0"]
        assert fw.interface[1].include == ["wg-tunnels"]

    def test_resolved_interfaces_flattens_transitively(self):
        fw = parse_firewall(
            {
                "groups": {
                    "interface": {
                        "wg-tunnels": ["wg1", "wg2"],
                        "internal": {
                            "interfaces": ["bridge0"],
                            "include": ["wg-tunnels"],
                        },
                        "internal-links": {
                            "interfaces": ["eth9"],
                            "include": ["internal"],
                        },
                    }
                }
            }
        )
        # eth9 -> internal (bridge0) -> wg-tunnels (wg1, wg2), first-seen order.
        assert fw.resolved_interfaces("internal-links") == [
            "eth9",
            "bridge0",
            "wg1",
            "wg2",
        ]

    def test_resolved_interfaces_dedupes(self):
        fw = parse_firewall(
            {
                "groups": {
                    "interface": {
                        "a": ["wg1"],
                        "b": {"interfaces": ["wg1"], "include": ["a"]},
                    }
                }
            }
        )
        assert fw.resolved_interfaces("b") == ["wg1"]

    def test_resolved_interfaces_survives_a_cycle(self):
        fw = parse_firewall(
            {
                "groups": {
                    "interface": {
                        "a": {"interfaces": ["wg1"], "include": ["b"]},
                        "b": {"interfaces": ["wg2"], "include": ["a"]},
                    }
                }
            }
        )
        assert sorted(fw.resolved_interfaces("a")) == ["wg1", "wg2"]

    def test_unknown_include_contributes_nothing(self):
        fw = parse_firewall(
            {
                "groups": {
                    "interface": {"a": {"interfaces": ["wg1"], "include": ["ghost"]}}
                }
            }
        )
        assert fw.resolved_interfaces("a") == ["wg1"]


class TestEmpty:
    def test_no_firewall_is_falsy(self):
        assert not parse_firewall({})
        assert not parse_firewall(None)

    def test_groups_make_it_truthy(self):
        assert parse_firewall({"groups": {"address": {"g": ["1.2.3.4"]}}})
