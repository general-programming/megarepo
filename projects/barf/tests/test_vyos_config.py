from barf.util.vyos_config import (
    minimal_delete_paths,
    reconcile_hashed_passwords,
    verify_crypt_hash,
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

    def test_changed_value_is_removed(self):
        # Ownership is total: the stale value is a real removal.
        diff = diff_paths(
            {("system", "host-name", "old")},
            {("system", "host-name", "new")},
        )
        assert diff.added == [("system", "host-name", "new")]
        assert diff.removed == [("system", "host-name", "old")]

    def test_unrendered_device_path_is_removed(self):
        # Nothing is exempt from ownership: a path only on the device
        # (not ignored) is deleted, not kept.
        diff = diff_paths({("service", "ssh", "port", "22")}, set())
        assert diff.has_changes
        assert diff.removed == [("service", "ssh", "port", "22")]


class TestFormatDiff:
    def test_plus_minus_lines(self):
        diff = ConfigDiff(
            added=[("system", "host-name", "new")],
            removed=[("system", "host-name", "old")],
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

    def test_values_with_spaces_are_quoted(self):
        diff = ConfigDiff(
            added=[("interfaces", "ethernet", "eth0", "description", "a b")]
        )
        assert "description 'a b'" in format_diff(diff)


class TestSummarizeDiff:
    def test_counts(self):
        diff = ConfigDiff(
            added=[("a", "1"), ("b", "2")],
            removed=[("c", "3")],
        )
        assert summarize_diff(diff) == "+2 -1"


# A real sha512-crypt vector from Ulrich Drepper's SHA-crypt spec:
# crypt("Hello world!", "$6$saltstring").
SPEC_HASH = (
    "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFN"
    "jnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"
)


class TestVerifyCryptHash:
    def test_match(self):
        assert verify_crypt_hash("Hello world!", SPEC_HASH) is True

    def test_mismatch(self):
        assert verify_crypt_hash("wrong password", SPEC_HASH) is False

    def test_unknown_scheme_is_none(self):
        assert verify_crypt_hash("x", "$y$j9T$salt$hash") is None
        assert verify_crypt_hash("x", "not-a-hash") is None


class TestReconcileHashedPasswords:
    PREFIX = ("system", "login", "user", "supertech", "authentication")

    def test_matching_password_produces_no_diff(self):
        running = {self.PREFIX + ("encrypted-password", SPEC_HASH)}
        candidate = {self.PREFIX + ("plaintext-password", "Hello world!")}
        r, c = reconcile_hashed_passwords(running, candidate)
        assert not diff_paths(r, c).has_changes

    def test_changed_password_shows_as_add_and_remove(self):
        running = {self.PREFIX + ("encrypted-password", SPEC_HASH)}
        candidate = {self.PREFIX + ("plaintext-password", "new password")}
        r, c = reconcile_hashed_passwords(running, candidate)
        diff = diff_paths(r, c)
        assert diff.added == [self.PREFIX + ("plaintext-password", "new password")]
        # reconcile rewrites the device hash under plaintext-password so
        # both sides share the node; a mismatch is a plain -old +new.
        assert diff.removed == [self.PREFIX + ("plaintext-password", SPEC_HASH)]
        # Both sides redact: neither the new password nor the old hash
        # may appear in the printed diff.
        text = format_diff(diff)
        assert "new password" not in text
        assert SPEC_HASH not in text

    def test_unknown_scheme_left_alone(self):
        running = {self.PREFIX + ("encrypted-password", "$y$j9T$salt$hash")}
        candidate = {self.PREFIX + ("plaintext-password", "whatever")}
        r, c = reconcile_hashed_passwords(running, candidate)
        assert r == running
        assert c == candidate

    def test_password_without_device_counterpart_is_an_addition(self):
        candidate = {self.PREFIX + ("plaintext-password", "hunter2")}
        r, c = reconcile_hashed_passwords(set(), candidate)
        assert diff_paths(r, c).added == [
            self.PREFIX + ("plaintext-password", "hunter2")
        ]

    def test_other_paths_untouched(self):
        running = {("system", "host-name", "r1")}
        candidate = {("system", "host-name", "r2")}
        assert reconcile_hashed_passwords(running, candidate) == (running, candidate)


class TestOwnership:
    def test_changed_leaf_is_add_and_remove(self):
        running = {("system", "name-server", "2606:4700::1111")}
        candidate = {("system", "name-server", "2606:4700:4700::1111")}
        diff = diff_paths(running, candidate)
        assert diff.removed == [("system", "name-server", "2606:4700::1111")]
        assert diff.added == [("system", "name-server", "2606:4700:4700::1111")]

    def test_removal_alone_is_a_change(self):
        running = {("system", "name-server", "1.1.1.1")}
        diff = diff_paths(running, set())
        assert diff.has_changes
        assert summarize_diff(diff) == "+0 -1"

    def test_unrendered_section_is_fully_removed(self):
        # Ownership is total: even config network.yml never mentions
        # (here a conntrack module) is deleted rather than kept.
        running = {("system", "conntrack", "modules", "ftp")}
        diff = diff_paths(running, set())
        assert diff.has_changes
        assert diff.removed == [("system", "conntrack", "modules", "ftp")]

    def test_removed_shown_as_minus_lines(self):
        diff = ConfigDiff(removed=[("system", "name-server", "1.1.1.1")])
        assert "- set system name-server 1.1.1.1" in format_diff(diff)


class TestMinimalDeletePaths:
    def test_leaf_delete_when_sibling_survives(self):
        running = {
            ("system", "name-server", "1.1.1.1"),
            ("system", "name-server", "8.8.8.8"),
        }
        removed = [("system", "name-server", "1.1.1.1")]
        assert minimal_delete_paths(removed, running) == [
            ("system", "name-server", "1.1.1.1")
        ]

    def test_collapses_to_node_when_subtree_fully_removed(self):
        running = {
            ("system", "name-server", "1.1.1.1"),
            ("system", "name-server", "8.8.8.8"),
            ("system", "host-name", "r1"),
        }
        removed = [
            ("system", "name-server", "1.1.1.1"),
            ("system", "name-server", "8.8.8.8"),
        ]
        assert minimal_delete_paths(removed, running) == [("system", "name-server")]

    def test_collapse_stops_where_surviving_config_lives(self):
        # host-name survives (kept or rendered), so the collapse rises
        # to the name-server node but never to bare "system".
        running = {
            ("system", "name-server", "1.1.1.1"),
            ("system", "host-name", "r1"),
        }
        removed = [("system", "name-server", "1.1.1.1")]
        assert minimal_delete_paths(removed, running) == [("system", "name-server")]

    def test_collapses_whole_group(self):
        running = {
            ("vpn", "ipsec", "esp-group", "OLD", "mode", "tunnel"),
            ("vpn", "ipsec", "esp-group", "OLD", "lifetime", "3600"),
            ("vpn", "ipsec", "esp-group", "E", "mode", "tunnel"),
        }
        removed = [
            ("vpn", "ipsec", "esp-group", "OLD", "mode", "tunnel"),
            ("vpn", "ipsec", "esp-group", "OLD", "lifetime", "3600"),
        ]
        assert minimal_delete_paths(removed, running) == [
            ("vpn", "ipsec", "esp-group", "OLD")
        ]


class TestWildcardIgnored:
    WILD = (("interfaces", "ethernet", "*", "hw-id"),)

    def test_wildcard_matches_any_component(self):
        for iface in ("eth0", "eth1"):
            diff = diff_paths(
                {("interfaces", "ethernet", iface, "hw-id", "aa:bb")},
                set(),
                ignored=self.WILD,
            )
            assert not diff.has_changes
            assert diff.removed == []

    def test_wildcard_does_not_ignore_other_leaves(self):
        diff = diff_paths(
            {("interfaces", "ethernet", "eth0", "mtu", "9000")},
            set(),
            ignored=self.WILD,
        )
        assert diff.removed == [("interfaces", "ethernet", "eth0", "mtu", "9000")]

    def test_path_shorter_than_prefix_is_not_ignored(self):
        diff = diff_paths({("interfaces", "ethernet")}, set(), ignored=self.WILD)
        assert diff.removed == [("interfaces", "ethernet")]


class TestIgnoredPaths:
    IGNORED = (("interfaces", "ethernet", "*", "hw-id"),)

    def test_ignored_paths_vanish_from_the_diff(self):
        diff = diff_paths(
            {("interfaces", "ethernet", "eth0", "hw-id", "aa:bb")},
            set(),
            ignored=self.IGNORED,
        )
        assert not diff.has_changes
        assert diff.removed == []
        assert "hw-id" not in format_diff(diff)
        assert summarize_diff(diff) == "no changes"

    def test_ignored_paths_never_collapse_into_deletes(self):
        # hw-id stays in running, so a whole-interface (or wider)
        # delete cannot swallow it: the collapse anchors at the mtu
        # node, below the ignored sibling.
        running = {
            ("interfaces", "ethernet", "eth0", "hw-id", "aa:bb"),
            ("interfaces", "ethernet", "eth0", "mtu", "9000"),
        }
        diff = diff_paths(running, set(), ignored=self.IGNORED)
        assert diff.removed == [("interfaces", "ethernet", "eth0", "mtu", "9000")]
        assert minimal_delete_paths(diff.removed, running) == [
            ("interfaces", "ethernet", "eth0", "mtu")
        ]
