"""RouterOS config model: parse rendered CLI, scope ownership, diff.

RouterOS config is flat collections of items, not a path tree, so the
VyOS "own the whole tree minus kept prefixes" model becomes: barf owns
a fixed set of COLLECTIONS, and within each an ``owned`` predicate
decides which device items barf claims. Everything failing the
predicate is kept — sea420's linuxgemini transit link (wg port 63666,
AS 4280806675), its bridges and LAN addresses, the hand-written
``export`` filter chain, the bogons/rfc1918 address-lists. The kept
set shrinks as network.yml models more of the box.

Item identity is a natural key per collection (listen-port, public
key, address, ...), never RouterOS ``.id``s (unstable) and never names
(the hand config predates barf's naming). Property comparison covers
only the properties barf renders: an owned item's extra device-side
properties (e.g. ``multihop=true``) are left alone, VyOS-kept style.
"""

from dataclasses import dataclass, field
from ipaddress import ip_address, ip_network
import shlex
from typing import Callable, Dict, List, Optional, Tuple

# The fabric's derived WireGuard port band (51000 + low_id * 64 +
# high_id, see barf/util/network.py). WireGuard interfaces listening
# in this band are barf's; anything else (e.g. linuxgemini1 on 63666)
# is kept.
FABRIC_WG_PORTS = range(51000, 53000)

# The fabric's numbered-link /31 pool (network.yml ``links``
# ``network:`` values). BGP connections whose remote sits in it are
# barf's; other ASes' sessions are kept.
FABRIC_LINK_NET = ip_network("172.31.255.0/24")

# Chain / list name prefix marking barf-generated routing policy.
OWNED_NAME_PREFIX = "genprog"


@dataclass
class RosDiff:
    """An item-level diff between rendered and device config."""

    # (collection, props) for items to create.
    added: List[Tuple[str, dict]] = field(default_factory=list)
    # (collection, identity label, [(prop, device value, rendered value)]).
    changed: List[Tuple[str, str, List[Tuple[str, Optional[str], str]]]] = field(
        default_factory=list
    )
    # (collection, identity label) for stale owned device items.
    removed: List[Tuple[str, str]] = field(default_factory=list)

    @property
    def has_changes(self) -> bool:
        return bool(self.added or self.changed or self.removed)


@dataclass(frozen=True)
class _Collection:
    """How one RouterOS collection is parsed, scoped, and keyed."""

    path: str
    # Natural identity of an item. None marks a positionally-keyed
    # collection (routing/filter/rule), indexed by _index_rules instead.
    identity: Optional[Callable[[dict], Optional[str]]]
    # Whether barf claims this device item. ``owned_names`` carries the
    # side-appropriate owned WireGuard interface names.
    owned: Callable[[dict, frozenset], bool]


def _wg_owned(item: dict) -> bool:
    try:
        return int(item.get("listen-port", -1)) in FABRIC_WG_PORTS
    except ValueError:
        return False


def _on_owned_interface(item: dict, owned_names: frozenset) -> bool:
    return item.get("interface") in owned_names


def _fabric_remote(item: dict, owned_names: frozenset) -> bool:
    try:
        return ip_address(item.get("remote.address", "")) in FABRIC_LINK_NET
    except ValueError:
        return False


def _named_genprog(key: str) -> Callable[[dict, frozenset], bool]:
    return lambda item, owned_names: item.get(key, "").startswith(OWNED_NAME_PREFIX)


COLLECTIONS: Dict[str, _Collection] = {
    c.path: c
    for c in (
        _Collection(
            "interface/wireguard",
            identity=lambda i: i.get("listen-port"),
            owned=lambda item, owned_names: _wg_owned(item),
        ),
        _Collection(
            "interface/wireguard/peers",
            identity=lambda i: i.get("public-key"),
            owned=_on_owned_interface,
        ),
        _Collection(
            "ip/address",
            identity=lambda i: i.get("address"),
            owned=_on_owned_interface,
        ),
        _Collection(
            "routing/bgp/template",
            identity=lambda i: i.get("name"),
            owned=_named_genprog("name"),
        ),
        _Collection(
            "routing/bgp/connection",
            identity=lambda i: i.get("remote.address"),
            owned=_fabric_remote,
        ),
        _Collection(
            # Rules are ordered within their chain; identity by
            # (chain, position) so reordering reads as changes.
            "routing/filter/rule",
            identity=None,  # assigned positionally in _index_rules
            owned=_named_genprog("chain"),
        ),
        _Collection(
            "ip/firewall/address-list",
            identity=lambda i: f"{i.get('list')}:{i.get('address')}",
            owned=_named_genprog("list"),
        ),
    )
}


def parse_ros_commands(rendered: str) -> Dict[str, List[dict]]:
    """Parse rendered RouterOS CLI into ``collection -> [props]``.

    Only ``/collection/path add k=v ...`` lines are modeled; comments,
    blank lines, and anything else (e.g. free-form ``extra_config``)
    are ignored — unmodeled commands are deploy passthrough, not diff
    input.
    """
    items: Dict[str, List[dict]] = {}
    for line in rendered.splitlines():
        line = line.strip()
        if not line.startswith("/"):
            continue
        tokens = shlex.split(line)
        if len(tokens) < 2 or tokens[1] != "add":
            continue
        collection = tokens[0].lstrip("/")
        if collection not in COLLECTIONS:
            continue
        props = {}
        for token in tokens[2:]:
            key, sep, value = token.partition("=")
            if sep:
                props[key] = value
        items.setdefault(collection, []).append(props)
    return items


def _owned_wg_names(items: List[dict]) -> frozenset:
    return frozenset(
        item["name"] for item in items if "name" in item and _wg_owned(item)
    )


def _index(
    collection: _Collection, items: List[dict], owned_names: frozenset
) -> Dict[str, dict]:
    """Owned items keyed by identity; dynamic device items are state,
    not config, and are always skipped."""
    if collection.identity is None:
        return _index_rules(items, owned_names)

    indexed = {}
    for item in items:
        if item.get("dynamic") == "true":
            continue
        if not collection.owned(item, owned_names):
            continue
        key = collection.identity(item)
        if key is not None:
            indexed[key] = item
    return indexed


def _index_rules(items: List[dict], owned_names: frozenset) -> Dict[str, dict]:
    indexed: Dict[str, dict] = {}
    position: Dict[str, int] = {}
    for item in items:
        chain = item.get("chain", "")
        if not chain.startswith(OWNED_NAME_PREFIX):
            continue
        position[chain] = position.get(chain, 0) + 1
        indexed[f"{chain}#{position[chain]}"] = item
    return indexed


def diff_items(
    desired: Dict[str, List[dict]], device: Dict[str, List[dict]]
) -> RosDiff:
    """Diff parsed rendered items against device REST items.

    Comparison per owned item covers exactly the properties barf
    renders; device-side extras on an owned item are left alone.
    """
    desired_names = _owned_wg_names(desired.get("interface/wireguard", []))
    device_names = _owned_wg_names(device.get("interface/wireguard", []))

    diff = RosDiff()
    for path, collection in COLLECTIONS.items():
        want = _index(collection, desired.get(path, []), desired_names)
        have = _index(collection, device.get(path, []), device_names)

        for key, props in want.items():
            if key not in have:
                diff.added.append((path, props))
                continue
            current = have[key]
            deltas = [
                (prop, current.get(prop), value)
                for prop, value in props.items()
                if current.get(prop) != value
            ]
            if deltas:
                diff.changed.append((path, key, deltas))

        for key in have:
            if key not in want:
                diff.removed.append((path, key))

    return diff


def format_props(props: dict) -> str:
    return " ".join(f"{k}={v}" for k, v in props.items())


def format_diff(diff: RosDiff, redact: bool = True) -> str:
    lines = []
    for path, props in diff.added:
        if redact and "private-key" in props:
            props = {**props, "private-key": "<redacted>"}
        lines.append(f"+ /{path} add {format_props(props)}")
    for path, key, deltas in diff.changed:
        for prop, old, new in deltas:
            if redact and prop == "private-key":
                old, new = old and "<redacted>", "<redacted>"
            lines.append(f"~ /{path} [{key}] {prop}: {old or '(unset)'} -> {new}")
    for path, key in diff.removed:
        lines.append(f"- /{path} [{key}] (stale: no longer rendered)")
    return "\n".join(lines)


def summarize_diff(diff: RosDiff) -> str:
    parts = []
    if diff.added:
        parts.append(f"+{len(diff.added)}")
    if diff.changed:
        parts.append(f"~{len(diff.changed)}")
    if diff.removed:
        parts.append(f"-{len(diff.removed)}")
    return " ".join(parts) if parts else "no changes"
