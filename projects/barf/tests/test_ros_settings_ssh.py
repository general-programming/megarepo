"""RouterOS singleton SETTINGS diffing and ssh-key ownership."""

from barf.vendors.mikrotik.ros_config import (
    OwnedScope,
    diff_items,
    excluded_items,
    parse_ros_commands,
    rendered_scope,
)
from barf.vendors.mikrotik.ros_deploy import build_apply_commands

ERIN_KEY = "ssh-ed25519 AAAAKEY erin"

NTP_RENDERED = """
/system/ntp/client set enabled=yes mode=unicast servers=time1.vyos.net,time2.vyos.net,time3.vyos.net
"""

SSH_RENDERED = f"""
/user/ssh-keys add user=admin key-owner="erin" key="{ERIN_KEY}"
"""

# Device readback: the singleton carries status noise barf never
# renders; the ssh key comes back WITHOUT its key material.
NTP_SYNCED = {
    "enabled": "yes",
    "mode": "unicast",
    "servers": "time1.vyos.net,time2.vyos.net,time3.vyos.net",
    "vrf": "main",
    "status": "synchronized",
}
ERIN_DEVICE_KEY = {
    ".id": "*1",
    "user": "admin",
    "key-owner": "erin",
    "bits": "256",
    "key-type": "ed25519",
}
HAND_KEY = {
    ".id": "*2",
    "user": "nep",
    "key-owner": "nep@laptop",
    "bits": "256",
    "key-type": "ed25519",
}


class TestNtpSettings:
    def test_set_line_parses_into_settings(self):
        parsed = parse_ros_commands(NTP_RENDERED)
        assert parsed["system/ntp/client"] == [
            {
                "enabled": "yes",
                "mode": "unicast",
                "servers": "time1.vyos.net,time2.vyos.net,time3.vyos.net",
            }
        ]

    def test_converged_singleton_diffs_clean(self):
        diff = diff_items(
            parse_ros_commands(NTP_RENDERED), {"system/ntp/client": [NTP_SYNCED]}
        )
        assert not [c for c in diff.changed if c[0] == "system/ntp/client"]

    def test_drifted_property_diffs_and_applies_without_find(self):
        device = {"system/ntp/client": [{**NTP_SYNCED, "servers": "192.0.2.1"}]}
        desired = parse_ros_commands(NTP_RENDERED)
        diff = diff_items(desired, device)
        (path, key, deltas) = next(
            c for c in diff.changed if c[0] == "system/ntp/client"
        )
        assert key == "settings"
        assert deltas == [
            (
                "servers",
                "192.0.2.1",
                "time1.vyos.net,time2.vyos.net,time3.vyos.net",
            )
        ]
        cmds = build_apply_commands(diff, desired, device)
        assert (
            "/system/ntp/client set"
            ' servers="time1.vyos.net,time2.vyos.net,time3.vyos.net"' in cmds
        )

    def test_unrendered_singleton_left_alone(self):
        # No settings rendered: whatever the device runs is kept, and
        # nothing is ever removed.
        diff = diff_items(
            {}, {"system/ntp/client": [{**NTP_SYNCED, "servers": "192.0.2.1"}]}
        )
        assert not diff.has_changes

    def test_status_noise_is_not_compared(self):
        # vrf/status exist only on the device; barf does not render
        # them, so they never show as drift.
        diff = diff_items(
            parse_ros_commands(NTP_RENDERED), {"system/ntp/client": [NTP_SYNCED]}
        )
        assert not diff.has_changes


class TestSshKeys:
    def test_rendered_key_missing_on_device_is_added(self):
        diff = diff_items(parse_ros_commands(SSH_RENDERED), {"user/ssh-keys": []})
        added = [props for path, props in diff.added if path == "user/ssh-keys"]
        assert added == [{"user": "admin", "key-owner": "erin", "key": ERIN_KEY}]

    def test_key_material_is_write_only(self):
        # Device readback has no `key` property; identity matches, so
        # the key adopts clean instead of forever-pending.
        diff = diff_items(
            parse_ros_commands(SSH_RENDERED), {"user/ssh-keys": [ERIN_DEVICE_KEY]}
        )
        assert not diff.has_changes

    def test_other_users_keys_are_kept_not_removed(self):
        desired = parse_ros_commands(SSH_RENDERED)
        device = {"user/ssh-keys": [ERIN_DEVICE_KEY, HAND_KEY]}
        diff = diff_items(desired, device)
        assert not diff.removed
        kept = {
            label for _p, label, _i in excluded_items(device, rendered_scope(desired))
        }
        assert "nep:nep@laptop" in kept
        assert "admin:erin" not in kept

    def test_unrendered_key_on_managed_user_is_removed(self):
        # Barf owns the WHOLE key set of a user it loads keys onto:
        # a hand-added (or rotated-away) key on that user is stale
        # access, surfaced as a removal.
        stale = {**HAND_KEY, "user": "admin", "key-owner": "old-laptop"}
        diff = diff_items(
            parse_ros_commands(SSH_RENDERED),
            {"user/ssh-keys": [ERIN_DEVICE_KEY, stale]},
        )
        assert ("user/ssh-keys", "admin:old-laptop") in diff.removed

    def test_renamed_owner_rotates_the_key(self):
        # Rotation recipe: a new key-owner name changes the identity,
        # so the old key is removed and the new one added.
        rendered = SSH_RENDERED.replace('key-owner="erin"', 'key-owner="erin-2026"')
        diff = diff_items(
            parse_ros_commands(rendered), {"user/ssh-keys": [ERIN_DEVICE_KEY]}
        )
        assert ("user/ssh-keys", "admin:erin") in diff.removed
        assert any(
            props.get("key-owner") == "erin-2026"
            for path, props in diff.added
            if path == "user/ssh-keys"
        )

    def test_unscoped_default_owns_no_keys(self):
        scope = OwnedScope()
        kept = {
            label
            for _p, label, _i in excluded_items(
                {"user/ssh-keys": [ERIN_DEVICE_KEY]}, scope
            )
        }
        assert "admin:erin" in kept
