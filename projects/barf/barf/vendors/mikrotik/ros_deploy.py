"""Rollback-guarded RouterOS deploy: forward script + backup revert.

RouterOS has no candidate/commit for the REST path, so barf gets
commit-confirm the way an operator would by hand:

  1. save a full binary backup of the box,
  2. arm a one-shot ``/system/scheduler`` job to restore that backup
     in ``ROLLBACK_TIMEOUT`` seconds,
  3. apply the forward changes,
  4. re-probe the box and -- only if it is still reachable and
     converged -- cancel the scheduler and delete the backup.

Lose the box (a bad change severs the management path, or the apply
wedges routing) and nobody cancels: the timer fires,
``/system backup load`` restores the pre-change config and reboots into
it. Restoring a full backup is a guaranteed revert -- it undoes
everything since the snapshot, not just barf's diff, and needs no
hand-computed inverse that could itself be wrong. The cost is a reboot
on rollback, which is why the confirm window is minutes, not seconds.

The forward changes are ordinary RouterOS console commands keyed by
``[find <natural-key>]`` rather than ``.id`` (unstable), the same
identities the diff uses. This module is the deterministic builder;
the live choreography (backup, arm, apply, confirm, cancel) lives on
:class:`barf.vendors.mikrotik.MikroTikHost` and is proven by a live
spike against sea420 before first use, like the Safe-Mode session.
"""

from datetime import date, timedelta
from typing import Dict, List, Tuple

from barf.vendors.mikrotik import ros_config

# Device-side bookkeeping fields that are state, not intent: never
# emitted as config we set.
_META_FIELDS = frozenset({".id", "dynamic", "running", "invalid", "actual-interface"})

# Natural-key fields per collection, matching ros_config identities.
# routing/bgp/connection and routing/filter/rule are handled specially.
_FIND_FIELDS: Dict[str, List[str]] = {
    "interface/bridge": ["name"],
    "interface/bridge/port": ["interface"],
    "interface/wireguard": ["listen-port"],
    "interface/wireguard/peers": ["public-key"],
    "ip/address": ["address"],
    "routing/bgp/template": ["name"],
    "ipv6/nd": ["interface"],
    "ipv6/nd/prefix": ["interface"],
    "ip/firewall/address-list": ["list", "address"],
}

_RULES = "routing/filter/rule"


def _quote(value: str) -> str:
    """Double-quote a RouterOS value, escaping backslashes and quotes."""
    escaped = str(value).replace("\\", "\\\\").replace('"', '\\"')
    return f'"{escaped}"'


def _props_str(props: dict) -> str:
    """``k=v`` pairs for an ``add``/``set``, meta fields dropped."""
    return " ".join(
        f"{key}={_quote(value)}"
        for key, value in props.items()
        if key not in _META_FIELDS
    )


def _find_fields(path: str, item: dict) -> List[str]:
    if path == "routing/bgp/connection":
        # Numbered sessions key on the peer address; unnumbered ones on
        # the local interface (mirrors ros_config._connection_identity).
        return ["remote.address"] if item.get("remote.address") else ["local.address"]
    return _FIND_FIELDS[path]


def _find_expr(path: str, item: dict) -> str:
    parts = [f"{field}={_quote(item[field])}" for field in _find_fields(path, item)]
    return "[find " + " ".join(parts) + "]"


def _affected_rule_chains(diff: ros_config.RosDiff) -> List[str]:
    """genprog filter chains touched by the diff, in stable order.

    Filter rules are positional, so a single changed/added/removed rule
    means the whole chain is rebuilt rather than surgically edited.
    """
    chains: List[str] = []
    for path, props in diff.added:
        if path == _RULES:
            chains.append(props.get("chain", ""))
    for path, key, _deltas in diff.changed:
        if path == _RULES:
            chains.append(key.split("#", 1)[0])
    for path, key in diff.removed:
        if path == _RULES:
            chains.append(key.split("#", 1)[0])
    seen: Dict[str, None] = {}
    for chain in chains:
        seen.setdefault(chain, None)
    return list(seen)


def _rules_for_chain(items: List[dict], chain: str) -> List[dict]:
    return [item for item in items if item.get("chain") == chain]


def _replace_chain(chain: str, rules: List[dict]) -> List[str]:
    """Remove a filter chain and re-add ``rules`` in order."""
    cmds = [f"/{_RULES} remove [find chain={_quote(chain)}]"]
    cmds += [f"/{_RULES} add {_props_str(rule)}" for rule in rules]
    return cmds


def build_apply_commands(
    diff: ros_config.RosDiff,
    desired: Dict[str, List[dict]],
    device: Dict[str, List[dict]],
) -> List[str]:
    """Forward console commands that apply ``diff`` to the device."""
    owned = ros_config.owned_index(
        device,
        ros_config.rendered_bridge_names(desired),
        ros_config.rendered_connection_ids(desired),
    )
    cmds: List[str] = []

    for path, props in diff.added:
        if path == _RULES:
            continue
        cmds.append(f"/{path} add {_props_str(props)}")

    for path, key, deltas in diff.changed:
        if path == _RULES:
            continue
        item = owned[path][key]
        setprops = {prop: new for prop, _old, new in deltas}
        cmds.append(f"/{path} set {_find_expr(path, item)} {_props_str(setprops)}")

    for path, key in diff.removed:
        if path == _RULES:
            continue
        item = owned[path][key]
        cmds.append(f"/{path} remove {_find_expr(path, item)}")

    for chain in _affected_rule_chains(diff):
        cmds += _replace_chain(chain, _rules_for_chain(desired.get(_RULES, []), chain))

    return cmds


def schedule_start(now_time: str, now_date: str, seconds: int) -> Tuple[str, str]:
    """Absolute ``(start-time, start-date)`` ``seconds`` after the router now.

    A one-shot rollback uses ``interval=0`` with an absolute start time
    (a plain ``interval=Ns`` scheduler fires every N seconds, not once).
    RouterOS interprets a bare start-time earlier than now as *tomorrow*,
    so when the delay crosses midnight the date must roll forward too --
    verified live: 7.22.3 accepts ISO ``YYYY-MM-DD`` for start-date.

    Args:
        now_time: The router clock ``HH:MM:SS`` (``/system/clock`` time).
        now_date: The router clock ISO date (``/system/clock`` date).
        seconds: Delay from now until the scheduler should fire.

    Returns:
        ``(start_time, start_date)``, both RouterOS-formatted.
    """
    hours, minutes, secs = (int(part) for part in now_time.split(":"))
    total = hours * 3600 + minutes * 60 + secs + seconds
    days_ahead, remainder = divmod(total, 86400)
    start_time = (
        f"{remainder // 3600:02d}:{(remainder % 3600) // 60:02d}:{remainder % 60:02d}"
    )
    start_date = (date.fromisoformat(now_date) + timedelta(days=days_ahead)).isoformat()
    return start_time, start_date


def build_rollback_script(backup_name: str) -> str:
    """RouterOS script source that restores the pre-change backup.

    ``/system backup load`` reboots the router into the restored
    config, so this is a last-resort full revert -- it undoes
    everything since the backup, not just barf's diff. No self-cleanup
    is needed: the restored config predates the rollback scheduler, so
    the reboot removes it. The backup is saved unencrypted (a transient
    same-box file, deleted on a healthy deploy), so the load needs no
    password.
    """
    return f'/system backup load name={_quote(backup_name)} password=""'
