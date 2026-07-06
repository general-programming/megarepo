"""Rollback-guarded RouterOS deploy (barf/vendors/mikrotik/ros_deploy.py).

The forward/inverse command builders are pure and fully covered here.
The live choreography (arm -> apply -> confirm -> cancel) is exercised
through MikroTikHost with the REST calls faked; the on-wire behaviour
itself is proven by a live spike against sea420 before first use, like
the Safe-Mode session.
"""

import pytest

from barf.vendors.mikrotik import MikroTikHost, ros_config, ros_deploy

# One diff exercising every category: an add (wg51913 + its address), a
# change (adopted wg51078 gains a name and a comment), two removals of
# stale owned items (wgOLD, its address), and a genprog filter chain
# whose single rule changed (whole-chain replace).
RENDERED = """
/interface/wireguard add name=wg51078 listen-port=51078 mtu=1420 private-key="PRIV-A" comment="barf: sea420"
/interface/wireguard add name=wg51913 listen-port=51913 mtu=1420 private-key="PRIV-D"
/ip/address add address=172.31.255.17/31 interface=wg51913
/routing/filter/rule add chain=genprog-in-sea rule="accept"
"""

DEVICE = {
    "interface/wireguard": [
        # Adopted by port: name and comment differ from rendered.
        {
            ".id": "*A",
            "name": "fmt2-spine-1",
            "listen-port": "51078",
            "mtu": "1420",
            "private-key": "PRIV-A",
            "running": "true",
        },
        # Fabric-band port, not rendered -> owned stale, removed.
        {
            ".id": "*Z",
            "name": "wgOLD",
            "listen-port": "51099",
            "mtu": "1420",
            "private-key": "PRIV-OLD",
        },
    ],
    "ip/address": [
        # On an owned fabric interface, not rendered -> removed.
        {
            ".id": "*5",
            "address": "172.31.255.45/31",
            "interface": "fmt2-spine-1",
            "dynamic": "false",
        },
    ],
    "routing/filter/rule": [
        {".id": "*7", "chain": "genprog-in-sea", "rule": "if (x) {accept}"},
    ],
}


@pytest.fixture
def apply_cmds():
    desired = ros_config.parse_ros_commands(RENDERED)
    diff = ros_config.diff_items(desired, DEVICE)
    return ros_deploy.build_apply_commands(diff, desired, DEVICE)


class TestApply:
    def test_adds_new_items(self, apply_cmds):
        assert (
            '/interface/wireguard add name="wg51913" listen-port="51913" mtu="1420" private-key="PRIV-D"'
            in apply_cmds
        )
        assert (
            '/ip/address add address="172.31.255.17/31" interface="wg51913"'
            in apply_cmds
        )

    def test_sets_changed_props_by_natural_key(self, apply_cmds):
        # Adopted interface: found by listen-port, name + comment set.
        setline = next(
            c for c in apply_cmds if c.startswith("/interface/wireguard set")
        )
        assert '[find listen-port="51078"]' in setline
        assert 'name="wg51078"' in setline
        assert 'comment="barf: sea420"' in setline

    def test_removes_stale_owned_items(self, apply_cmds):
        assert '/interface/wireguard remove [find listen-port="51099"]' in apply_cmds
        assert '/ip/address remove [find address="172.31.255.45/31"]' in apply_cmds

    def test_filter_chain_is_replaced_wholesale(self, apply_cmds):
        assert '/routing/filter/rule remove [find chain="genprog-in-sea"]' in apply_cmds
        assert (
            '/routing/filter/rule add chain="genprog-in-sea" rule="accept"'
            in apply_cmds
        )

    def test_meta_state_fields_never_emitted(self, apply_cmds):
        assert all("running=" not in c for c in apply_cmds)

    def test_bridge_changes_key_on_name(self):
        rendered = '/interface/bridge add name=bridge-internal comment="barf: LAN"\n'
        device = {
            "interface/bridge": [{".id": "*b1", "name": "bridge-internal"}],
        }
        desired = ros_config.parse_ros_commands(rendered)
        diff = ros_config.diff_items(desired, device)
        cmds = ros_deploy.build_apply_commands(diff, desired, device)
        # Comment add on the adopted bridge, found by name (no KeyError).
        assert (
            '/interface/bridge set [find name="bridge-internal"] comment="barf: LAN"'
            in cmds
        )


class TestRollbackScript:
    def test_restores_named_backup(self):
        src = ros_deploy.build_rollback_script("barf-rollback")
        assert src == '/system backup load name="barf-rollback" password=""'


class TestScheduleStart:
    def test_delay_within_the_day(self):
        # 120s after 22:19:10 on 2026-07-05.
        assert ros_deploy.schedule_start("22:19:10", "2026-07-05", 120) == (
            "22:21:10",
            "2026-07-05",
        )

    def test_delay_crossing_midnight_rolls_the_date(self):
        # 120s after 23:59:30 lands at 00:01:30 the next day -- RouterOS
        # would otherwise read a bare earlier time as tomorrow anyway,
        # but with a full day's delay, so the date must advance.
        assert ros_deploy.schedule_start("23:59:30", "2026-07-05", 120) == (
            "00:01:30",
            "2026-07-06",
        )

    def test_month_boundary(self):
        assert ros_deploy.schedule_start("23:59:00", "2026-07-31", 120) == (
            "00:01:00",
            "2026-08-01",
        )


class TestQuoting:
    def test_values_with_spaces_and_quotes_are_escaped(self):
        rendered = '/routing/filter/rule add chain=genprog-x rule="set comment \\"hi\\"; accept"'
        desired = ros_config.parse_ros_commands(rendered)
        diff = ros_config.diff_items(desired, {})
        apply = ros_deploy.build_apply_commands(diff, desired, {})
        line = next(c for c in apply if c.startswith("/routing/filter/rule add"))
        # Embedded quotes are backslash-escaped inside the double-quoted value.
        assert r"\"hi\"" in line


class TestOrchestration:
    """arm -> apply -> confirm -> cancel ordering on MikroTikHost."""

    def _host(self, monkeypatch):
        host = MikroTikHost(hostname="sea420-acc-v-hv2", role="vpn")
        calls = []
        monkeypatch.setattr(host, "_arm_rollback", lambda: calls.append("arm"))
        monkeypatch.setattr(host, "_apply_forward", lambda cmds: calls.append("apply"))
        monkeypatch.setattr(host, "_cancel_rollback", lambda: calls.append("cancel"))
        return host, calls

    def test_healthy_deploy_cancels_rollback(self, monkeypatch, fake_vault):
        host, calls = self._host(monkeypatch)
        monkeypatch.setattr(host, "device_items", lambda: DEVICE)
        # Converged: confirm sees no changes.
        monkeypatch.setattr(
            host, "_confirm_converged", lambda desired: calls.append("confirm")
        )
        host.push_rendered_config(RENDERED)
        assert calls == ["arm", "apply", "confirm", "cancel"]

    def test_failed_confirm_leaves_rollback_armed(self, monkeypatch, fake_vault):
        host, calls = self._host(monkeypatch)
        monkeypatch.setattr(host, "device_items", lambda: DEVICE)

        def boom(desired):
            calls.append("confirm")
            raise RuntimeError("did not converge")

        monkeypatch.setattr(host, "_confirm_converged", boom)
        with pytest.raises(RuntimeError, match="did not converge"):
            host.push_rendered_config(RENDERED)
        # Rollback was armed and never cancelled -> the timer will fire.
        assert calls == ["arm", "apply", "confirm"]
        assert "cancel" not in calls

    def test_no_changes_skips_everything(self, monkeypatch, fake_vault):
        host, calls = self._host(monkeypatch)
        # Device already holds exactly what we render.
        desired = ros_config.parse_ros_commands(RENDERED)
        converged = {p: [dict(i) for i in items] for p, items in desired.items()}
        monkeypatch.setattr(host, "device_items", lambda: converged)
        monkeypatch.setattr(
            host, "_confirm_converged", lambda desired: calls.append("confirm")
        )
        host.push_rendered_config(RENDERED)
        assert calls == []
