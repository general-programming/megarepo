from barf.util.vyos_config import (
    ConfigDiff,
    diff_paths,
    format_diff,
    parse_set_commands,
    paths_from_api_json,
    summarize_diff,
)


class TestParseSetCommands:
    def test_basic_paths(self):
        text = "set system host-name router1\nset interfaces ethernet eth0 mtu 9000"
        assert parse_set_commands(text) == {
            ("system", "host-name", "router1"),
            ("interfaces", "ethernet", "eth0", "mtu", "9000"),
        }

    def test_quotes_normalize(self):
        # The templates mix quoted and unquoted values; both must parse
        # to the same path.
        quoted = parse_set_commands(
            "set vpn ipsec esp-group E proposal 1 encryption 'aes256'"
        )
        bare = parse_set_commands(
            "set vpn ipsec esp-group E proposal 1 encryption aes256"
        )
        assert quoted == bare

    def test_quoted_value_with_spaces(self):
        paths = parse_set_commands(
            "set interfaces ethernet eth0 description 'external uplink (a -> b)'"
        )
        assert paths == {
            (
                "interfaces",
                "ethernet",
                "eth0",
                "description",
                "external uplink (a -> b)",
            )
        }

    def test_ignores_blanks_comments_and_non_set_lines(self):
        text = "\n# comment\ndelete interfaces ethernet eth9\nconfigure\nset system host-name r1\n"
        assert parse_set_commands(text) == {("system", "host-name", "r1")}

    def test_unbalanced_quote_falls_back_to_plain_split(self):
        paths = parse_set_commands("set system host-name it's-broken")
        assert ("system", "host-name", "it's-broken") in paths


class TestPathsFromApiJson:
    def test_single_value_leaf(self):
        data = {"interfaces": {"ethernet": {"eth0": {"address": "dhcp"}}}}
        assert paths_from_api_json(data) == {
            ("interfaces", "ethernet", "eth0", "address", "dhcp")
        }

    def test_multi_value_leaf_becomes_one_path_per_value(self):
        data = {"system": {"name-server": ["1.1.1.1", "10.255.1.8"]}}
        assert paths_from_api_json(data) == {
            ("system", "name-server", "1.1.1.1"),
            ("system", "name-server", "10.255.1.8"),
        }

    def test_valueless_node_is_its_own_path(self):
        data = {"protocols": {"bgp": {"parameters": {"shutdown": {}}}}}
        assert paths_from_api_json(data) == {
            ("protocols", "bgp", "parameters", "shutdown")
        }

    def test_matches_equivalent_set_commands(self):
        # The two representations of the same config must flatten to
        # the same path set, or every diff would be full of noise.
        data = {
            "interfaces": {
                "wireguard": {
                    "wg1": {
                        "peer": {"other": {"allowed-ips": ["0.0.0.0/0", "::/0"]}},
                        "port": "1",
                    }
                }
            }
        }
        rendered = "\n".join(
            [
                "set interfaces wireguard wg1 peer other allowed-ips 0.0.0.0/0",
                "set interfaces wireguard wg1 peer other allowed-ips ::/0",
                "set interfaces wireguard wg1 port 1",
            ]
        )
        assert paths_from_api_json(data) == parse_set_commands(rendered)

    def test_non_string_scalars_coerced(self):
        data = {"interfaces": {"ethernet": {"eth0": {"mtu": 9000}}}}
        assert paths_from_api_json(data) == {
            ("interfaces", "ethernet", "eth0", "mtu", "9000")
        }


class TestDiffPaths:
    def test_no_changes(self):
        paths = {("system", "host-name", "r1")}
        diff = diff_paths(paths, paths)
        assert not diff.has_changes
        assert summarize_diff(diff) == "no changes"

    def test_addition(self):
        diff = diff_paths(set(), {("system", "host-name", "r1")})
        assert diff.added == [("system", "host-name", "r1")]
        assert diff.has_changes

    def test_changed_value_pairs_as_replaced(self):
        diff = diff_paths(
            {("system", "host-name", "old")},
            {("system", "host-name", "new")},
        )
        assert diff.added == [("system", "host-name", "new")]
        assert diff.replaced == [("system", "host-name", "old")]
        assert diff.device_only == []

    def test_device_only_is_not_a_change(self):
        # Merge semantics: device-only config is reported but does not
        # make the diff "dirty".
        diff = diff_paths(
            {("service", "ssh", "port", "22")},
            set(),
        )
        assert not diff.has_changes
        assert diff.device_only == [("service", "ssh", "port", "22")]


class TestFormatDiff:
    def test_plus_minus_lines(self):
        diff = ConfigDiff(
            added=[("system", "host-name", "new")],
            replaced=[("system", "host-name", "old")],
        )
        text = format_diff(diff)
        assert "- set system host-name old" in text
        assert "+ set system host-name new" in text

    def test_secrets_redacted_by_default(self):
        diff = ConfigDiff(
            added=[("interfaces", "wireguard", "wg1", "private-key", "hunter2")]
        )
        text = format_diff(diff)
        assert "hunter2" not in text
        assert "<redacted>" in text

    def test_secrets_shown_when_asked(self):
        diff = ConfigDiff(
            added=[("vpn", "ipsec", "authentication", "psk", "p", "secret", "hunter2")]
        )
        assert "hunter2" in format_diff(diff, redact=False)

    def test_public_key_not_redacted(self):
        diff = ConfigDiff(
            added=[
                ("interfaces", "wireguard", "wg1", "peer", "x", "public-key", "PUBKEY")
            ]
        )
        assert "PUBKEY" in format_diff(diff)

    def test_device_only_hidden_behind_flag(self):
        diff = ConfigDiff(device_only=[("service", "ssh", "port", "22")])
        assert "ssh" not in format_diff(diff)
        assert "1 device-only paths hidden" in format_diff(diff)
        assert "set service ssh port 22" in format_diff(diff, show_device_only=True)

    def test_values_with_spaces_are_quoted(self):
        diff = ConfigDiff(
            added=[("interfaces", "ethernet", "eth0", "description", "a b")]
        )
        assert "description 'a b'" in format_diff(diff)


class TestSummarizeDiff:
    def test_counts(self):
        diff = ConfigDiff(
            added=[("a", "1"), ("b", "2")],
            replaced=[("a", "0")],
            device_only=[("c", "3")],
        )
        assert summarize_diff(diff) == "+2 ~1 (1 device-only)"
