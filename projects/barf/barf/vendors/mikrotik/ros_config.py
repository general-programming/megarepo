"""RouterOS config model: parse rendered CLI, scope ownership, diff.

RouterOS config is flat collections of items, not a path tree, so the
VyOS "own the whole tree minus kept prefixes" model becomes: barf owns
a fixed set of COLLECTIONS, and within each every non-dynamic device
item is barf's *unless* an ``excluded`` predicate protects it. This is
exclusion-based like VyOS: ownership is the default and the excluded
set is the shrinking knob. The exclusions seed what barf does not
control today — sea420's linuxgemini transit link (wg port 63666, AS
4280806675) — and each one dropped (as network.yml models more of the
box) widens ownership toward 100%; its bridges/LAN addresses, the
``export`` filter chain, and the firewall address/interface groups have
already been folded in. ``excluded_items`` surfaces exactly what is
still kept, powering ``config diff --show-device-only``.

Item identity is a natural key per collection (listen-port, public
key, address, ...), never RouterOS ``.id``s (unstable) and never names
(the hand config predates barf's naming). Property comparison covers
only the properties barf renders: an owned item's extra device-side
properties (e.g. ``multihop=true``) are left alone, VyOS-kept style.
"""

import shlex
from dataclasses import dataclass, field, replace
from typing import Callable, Dict, List, Optional, Tuple

# The fabric's derived WireGuard port band (51000 + low_id * 64 +
# high_id, see barf/util/network.py). WireGuard interfaces listening
# in this band are barf's; anything else (e.g. linuxgemini1 on 63666)
# is kept.
FABRIC_WG_PORTS = range(51000, 53000)

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


def _never(item: dict, owned_names: frozenset) -> bool:
    return False


@dataclass(frozen=True)
class _Collection:
    """How one RouterOS collection is parsed, scoped, and keyed."""

    path: str
    # Natural identity of an item. None marks a positionally-keyed
    # collection (routing/filter/rule), indexed by _index_rules instead.
    identity: Optional[Callable[[dict], Optional[str]]]
    # Whether barf keeps this device item out of its ownership: True
    # means hand-managed, leave it entirely alone. Everything else (and
    # not ``dynamic``) is owned. ``owned_names`` carries the
    # side-appropriate barf-owned interface names for this collection.
    excluded: Callable[[dict, frozenset], bool]
    # Whether this item is a recorded RouterOS default, not intent:
    # dropped from the diff *and* the device-only listing entirely
    # (never owned, never shown, never a shrink target) -- the VyOS
    # IGNORED_PATHS equivalent. e.g. the disabled ``ipv6/nd
    # interface=all`` default present on every box.
    ignored: Callable[[dict, frozenset], bool] = _never
    # Rendered properties the device never reads back (e.g. ssh key
    # material): sent on add, skipped in change comparison -- rotating
    # such a property alone is invisible to the diff, so pair it with
    # an identity-bearing property (the key-owner name) when it must
    # change.
    write_only: frozenset = frozenset()


def _wg_fabric_port(item: dict) -> bool:
    """Whether a WireGuard interface listens in the fabric port band."""
    try:
        return int(item.get("listen-port", -1)) in FABRIC_WG_PORTS
    except ValueError:
        return False


def _foreign_wg(item: dict, owned_names: frozenset) -> bool:
    """Hand-managed WireGuard: neither a fabric-band link nor a port
    barf explicitly renders (``owned_names`` here are the rendered wg
    ports). A fabric-band port stays owned even when unrendered so stale
    links are still removed; a modeled static tunnel (e.g. linuxgemini1
    on 63666) is owned by being rendered."""
    return not _wg_fabric_port(item) and item.get("listen-port") not in owned_names


def _off_fabric_interface(item: dict, owned_names: frozenset) -> bool:
    """Rides an interface barf does not render (LAN bridges, ethers,
    the transit tunnel), so its address/peer/ND is hand-managed. Drop
    this once those interfaces are modeled in network.yml."""
    return item.get("interface") not in owned_names


def _connection_identity(item: dict) -> Optional[str]:
    """Numbered sessions key on the peer /31 address; unnumbered ones
    (no remote.address -- the peer link-local is ND-discovered) key on
    the local interface, which RouterOS allows one connection per."""
    return item.get("remote.address") or item.get("local.address")


def _foreign_connection(item: dict, owned_names: frozenset) -> bool:
    """A BGP session barf does not render (``owned_names`` here are the
    rendered connection identities): owned exactly when barf renders a
    connection with this identity, so adopting a hand session (e.g. the
    linuxgemini transit) is just a matter of modeling it."""
    return _connection_identity(item) not in owned_names


def _not_genprog(key: str) -> Callable[[dict, frozenset], bool]:
    """Items whose ``key`` lacks the genprog prefix are hand-written
    (e.g. the default BGP template ``name``)."""
    return lambda item, owned_names: not item.get(key, "").startswith(OWNED_NAME_PREFIX)


def _foreign_bridge(item: dict, owned_names: frozenset) -> bool:
    """A bridge barf does not render (``owned_names`` here are the
    rendered bridge names): hand-created L2, left alone. Barf owns only
    the bridges it models in network.yml."""
    return item.get("name") not in owned_names


def _foreign_bridge_port(item: dict, owned_names: frozenset) -> bool:
    """A port on a bridge barf does not render (``owned_names`` here are
    the rendered bridge names): its membership is hand-managed."""
    return item.get("bridge") not in owned_names


def _ros_default(item: dict, owned_names: frozenset) -> bool:
    """A RouterOS built-in default entry (``default=true``): a recorded
    fact, not intent, and not removable -- ignored like VyOS IGNORED
    (e.g. the disabled ``ipv6/nd interface=all`` and the built-in
    ``routing/bgp/template default``)."""
    return item.get("default") == "true"


def _ros_builtin(item: dict, owned_names: frozenset) -> bool:
    """A RouterOS built-in entry (``builtin=true``): the stock
    ``all``/``none``/``dynamic``/``static`` interface lists, not
    removable and not intent -- ignored like ``_ros_default``."""
    return item.get("builtin") == "true"


def _foreign_address_list(item: dict, owned_names: frozenset) -> bool:
    """An address-list entry whose ``list`` barf does not render
    (``owned_names`` here are the rendered list names: genprog-networks
    plus every declared firewall address group). A hand list stays kept
    until modelled as a ``firewall.groups.address`` entry."""
    return item.get("list") not in owned_names


def _foreign_interface_list(item: dict, owned_names: frozenset) -> bool:
    """An interface list barf does not render (``owned_names`` here are
    the rendered interface-group names)."""
    return item.get("name") not in owned_names


def _ssh_key_identity(item: dict) -> Optional[str]:
    """SSH keys key on (user, key-owner): RouterOS never reads the key
    material back, and the owner name (the public key's trailing
    comment) is the stable half of the identity."""
    if item.get("user") is None and item.get("key-owner") is None:
        return None
    return f"{item.get('user')}:{item.get('key-owner')}"


def _foreign_ssh_key(item: dict, owned_names: frozenset) -> bool:
    """An SSH key on a user barf does not manage (``owned_names`` here
    are the rendered ssh-key users). Barf owns the WHOLE key set of a
    user it renders keys for -- authorized keys are declarative, so a
    hand-loaded (or rotated-away) key on that user reads as a removal
    instead of lingering as stale access. Keys on other users stay
    hand-managed."""
    return item.get("user") not in owned_names


def _foreign_interface_list_member(item: dict, owned_names: frozenset) -> bool:
    """A member of an interface list barf does not render (``owned_names``
    here are the rendered interface-group names), so barf must model
    every member of a group it owns."""
    return item.get("list") not in owned_names


def rendered_bridge_names(parsed: Dict[str, List[dict]]) -> frozenset:
    """The bridge names in a parsed/rendered config (barf's bridges).

    Bridge ownership is by name and identical on both sides, so this
    one set scopes bridge and bridge-address ownership for the diff and
    the device-only listing alike.
    """
    return frozenset(
        item["name"] for item in parsed.get("interface/bridge", []) if item.get("name")
    )


def rendered_connection_ids(parsed: Dict[str, List[dict]]) -> frozenset:
    """The BGP connection identities in a parsed/rendered config.

    Barf owns exactly the connections it renders; this set (identical
    on both sides) scopes connection ownership for the diff and the
    device-only listing.
    """
    return frozenset(
        _connection_identity(item)
        for item in parsed.get("routing/bgp/connection", [])
        if _connection_identity(item)
    )


def rendered_wg_ports(parsed: Dict[str, List[dict]]) -> frozenset:
    """The WireGuard listen-ports in a parsed/rendered config.

    Scopes wg-interface ownership for a modeled static tunnel (fabric
    links are owned by their port band regardless); identical on both
    sides, like the bridge/connection sets.
    """
    return frozenset(
        item["listen-port"]
        for item in parsed.get("interface/wireguard", [])
        if item.get("listen-port")
    )


def rendered_address_list_names(parsed: Dict[str, List[dict]]) -> frozenset:
    """The address-list names in a parsed/rendered config: the derived
    ``genprog-networks`` plus every declared firewall address group,
    across both the v4 and v6 address-list trees. Scopes address-list
    ownership on both sides.
    """
    return frozenset(
        item["list"]
        for path in ("ip/firewall/address-list", "ipv6/firewall/address-list")
        for item in parsed.get(path, [])
        if item.get("list")
    )


def rendered_interface_list_names(parsed: Dict[str, List[dict]]) -> frozenset:
    """The interface-list names in a parsed/rendered config (barf's
    firewall interface groups). Scopes interface-list and member
    ownership on both sides.
    """
    return frozenset(
        item["name"] for item in parsed.get("interface/list", []) if item.get("name")
    )


def rendered_ssh_key_users(parsed: Dict[str, List[dict]]) -> frozenset:
    """The users a rendered config loads SSH keys onto.

    Scopes ssh-key ownership on both sides: barf owns the whole key
    set of every user it renders keys for; other users' keys stay
    hand-managed.
    """
    return frozenset(
        item["user"] for item in parsed.get("user/ssh-keys", []) if item.get("user")
    )


@dataclass(frozen=True)
class OwnedScope:
    """The rendered-name sets scoping barf's ownership per collection.

    One value object instead of threading each set through every
    signature; ``rendered_scope`` builds it from a parsed config, and
    the device-side variants swap in the device's owned wg interface
    names (the only side-dependent set).
    """

    wg_names: frozenset = frozenset()
    bridge_names: frozenset = frozenset()
    conn_ids: frozenset = frozenset()
    wg_ports: frozenset = frozenset()
    addrlist_names: frozenset = frozenset()
    iflist_names: frozenset = frozenset()
    ssh_users: frozenset = frozenset()


def rendered_scope(parsed: Dict[str, List[dict]]) -> OwnedScope:
    """The ownership scope a parsed/rendered config defines."""
    wg_ports = rendered_wg_ports(parsed)
    return OwnedScope(
        wg_names=_owned_wg_names(parsed.get("interface/wireguard", []), wg_ports),
        bridge_names=rendered_bridge_names(parsed),
        conn_ids=rendered_connection_ids(parsed),
        wg_ports=wg_ports,
        addrlist_names=rendered_address_list_names(parsed),
        iflist_names=rendered_interface_list_names(parsed),
        ssh_users=rendered_ssh_key_users(parsed),
    )


def _device_scope(scope: OwnedScope, device: Dict[str, List[dict]]) -> OwnedScope:
    """``scope`` with wg interface names resolved from the device side."""
    return replace(
        scope,
        wg_names=_owned_wg_names(device.get("interface/wireguard", []), scope.wg_ports),
    )


def _owned_names(path: str, scope: OwnedScope) -> frozenset:
    """The barf-owned identities an item in ``path`` is scoped by.

    Addresses may sit on a fabric WireGuard link *or* a barf-rendered
    bridge; the bridge collection is scoped by bridge name; BGP
    connections by rendered identity; the wg interface by rendered port;
    firewall address-lists by rendered list name and interface lists by
    rendered group name; everything else (peers, ND) is owned-wg-interface
    only, so LAN peers stay hand-managed until modeled.
    """
    if path == "interface/wireguard":
        return scope.wg_ports
    if path == "ip/address":
        return scope.wg_names | scope.bridge_names
    if path in ("interface/bridge", "interface/bridge/port"):
        return scope.bridge_names
    if path == "ipv6/nd":
        # Fabric links' unnumbered RA plus a bridge's LAN RA. (nd/prefix
        # stays fabric-only: the bridge's prefix RA is dynamic state.)
        return scope.wg_names | scope.bridge_names
    if path == "routing/bgp/connection":
        return scope.conn_ids
    if path in ("ip/firewall/address-list", "ipv6/firewall/address-list"):
        return scope.addrlist_names
    if path in ("interface/list", "interface/list/member"):
        return scope.iflist_names
    if path == "user/ssh-keys":
        return scope.ssh_users
    return scope.wg_names


COLLECTIONS: Dict[str, _Collection] = {
    c.path: c
    for c in (
        _Collection(
            # LAN/L2 bridges barf models; ports and addresses hang off
            # the name. Only barf-rendered bridges are owned.
            "interface/bridge",
            identity=lambda i: i.get("name"),
            excluded=_foreign_bridge,
        ),
        _Collection(
            # Bridge member ports, keyed by the member interface (a port
            # rides one bridge). Owned when the bridge is barf-rendered,
            # so barf must model every member of a bridge it owns.
            "interface/bridge/port",
            identity=lambda i: i.get("interface"),
            excluded=_foreign_bridge_port,
        ),
        _Collection(
            "interface/wireguard",
            identity=lambda i: i.get("listen-port"),
            excluded=_foreign_wg,
        ),
        _Collection(
            "interface/wireguard/peers",
            identity=lambda i: i.get("public-key"),
            excluded=_off_fabric_interface,
        ),
        _Collection(
            "ip/address",
            identity=lambda i: i.get("address"),
            excluded=_off_fabric_interface,
        ),
        _Collection(
            "routing/bgp/template",
            identity=lambda i: i.get("name"),
            excluded=_not_genprog("name"),
            # The built-in ``default`` template cannot be removed; ignore
            # it so barf neither lists nor fights it.
            ignored=_ros_default,
        ),
        _Collection(
            "routing/bgp/connection",
            identity=_connection_identity,
            excluded=_foreign_connection,
        ),
        _Collection(
            # Per-interface ND config backing unnumbered BGP sessions.
            "ipv6/nd",
            identity=lambda i: i.get("interface"),
            excluded=_off_fabric_interface,
            # The disabled ``interface=all`` entry is a RouterOS default
            # (default=true) present on every box -- not intent.
            ignored=_ros_default,
        ),
        _Collection(
            "ipv6/nd/prefix",
            identity=lambda i: i.get("interface"),
            excluded=_off_fabric_interface,
        ),
        _Collection(
            # Rules are ordered within their chain; identity by
            # (chain, position) so reordering reads as changes. Fully
            # owned: barf renders every routing filter chain it needs
            # (genprog-*), so any chain it does not render (the retired
            # hand ``export`` chain) is deleted, not kept.
            "routing/filter/rule",
            identity=None,  # assigned positionally in _index_rules
            excluded=_never,
        ),
        _Collection(
            "ip/firewall/address-list",
            identity=lambda i: f"{i.get('list')}:{i.get('address')}",
            excluded=_foreign_address_list,
        ),
        _Collection(
            "ipv6/firewall/address-list",
            identity=lambda i: f"{i.get('list')}:{i.get('address')}",
            excluded=_foreign_address_list,
        ),
        _Collection(
            # Firewall interface groups. Barf owns the groups it renders;
            # the built-in all/none/dynamic/static lists are ignored.
            "interface/list",
            identity=lambda i: i.get("name"),
            excluded=_foreign_interface_list,
            ignored=_ros_builtin,
        ),
        _Collection(
            # Interface-group memberships, keyed by (list, interface).
            # Owned when the group is barf-rendered, so barf must model
            # every member of a group it owns.
            "interface/list/member",
            identity=lambda i: f"{i.get('list')}:{i.get('interface')}",
            excluded=_foreign_interface_list_member,
        ),
        _Collection(
            # SSH public keys, keyed by (user, key-owner). The key
            # material is write-only (RouterOS never returns it), so
            # rotating a key must also change its owner-comment name to
            # be visible to the diff.
            "user/ssh-keys",
            identity=_ssh_key_identity,
            excluded=_foreign_ssh_key,
            write_only=frozenset({"key"}),
        ),
    )
}

# Singleton settings paths barf may own (``/path set k=v``, no items,
# no identity). Only the rendered properties are compared -- barf
# rendering nothing for a path leaves the device's singleton entirely
# alone, and a singleton is never added or removed.
SETTINGS: frozenset = frozenset({"system/ntp/client"})


def parse_ros_commands(rendered: str) -> Dict[str, List[dict]]:
    """Parse rendered RouterOS CLI into ``collection -> [props]``.

    ``/collection/path add k=v ...`` lines are modeled for the known
    COLLECTIONS, ``/path set k=v ...`` for the known SETTINGS
    singletons; comments, blank lines, and anything else (e.g.
    free-form ``extra_config``) are ignored — unmodeled commands are
    deploy passthrough, not diff input.
    """
    items: Dict[str, List[dict]] = {}
    for line in rendered.splitlines():
        line = line.strip()
        if not line.startswith("/"):
            continue
        tokens = shlex.split(line)
        if len(tokens) < 2:
            continue
        collection = tokens[0].lstrip("/")
        verb = tokens[1]
        if not (
            (verb == "add" and collection in COLLECTIONS)
            or (verb == "set" and collection in SETTINGS)
        ):
            continue
        props = {}
        for token in tokens[2:]:
            key, sep, value = token.partition("=")
            if sep:
                props[key] = value
        items.setdefault(collection, []).append(props)
    return items


def _owned_wg_names(items: List[dict], wg_ports: frozenset = frozenset()) -> frozenset:
    """Names of owned wg interfaces: fabric-band links plus any rendered
    static tunnel (``wg_ports``)."""
    return frozenset(
        item["name"]
        for item in items
        if "name" in item
        and (_wg_fabric_port(item) or item.get("listen-port") in wg_ports)
    )


def _index(
    collection: _Collection, items: List[dict], owned_names: frozenset
) -> Dict[str, dict]:
    """Owned items keyed by identity; dynamic device items are state,
    not config, and are always skipped, as are excluded (hand-managed)
    items."""
    if collection.identity is None:
        return _index_rules(items, owned_names)

    indexed = {}
    for item in items:
        if item.get("dynamic") == "true":
            continue
        if collection.ignored(item, owned_names):
            continue
        if collection.excluded(item, owned_names):
            continue
        key = collection.identity(item)
        if key is not None:
            indexed[key] = item
    return indexed


def _index_rules(items: List[dict], owned_names: frozenset) -> Dict[str, dict]:
    """All routing filter rules keyed by (chain, position).

    Fully owned: every chain is indexed (not just genprog-*), so a chain
    barf no longer renders is diffed as removed. Barf renders every
    chain it needs, so nothing it wants survives only on the device.
    """
    indexed: Dict[str, dict] = {}
    position: Dict[str, int] = {}
    for item in items:
        chain = item.get("chain", "")
        position[chain] = position.get(chain, 0) + 1
        indexed[f"{chain}#{position[chain]}"] = item
    return indexed


def owned_index(
    device: Dict[str, List[dict]],
    scope: OwnedScope = OwnedScope(),
) -> Dict[str, Dict[str, dict]]:
    """Barf-owned device items keyed by identity, per collection.

    The raw device item (``.id`` and all) for each owned key, so the
    deploy path can resolve a natural key back to the live item it must
    change, remove, or restore. Positional collections
    (routing/filter/rule) are indexed the same way barf diffs them.
    ``scope`` (usually ``rendered_scope`` of the desired config) scopes
    ownership; omitted, none is claimed.
    """
    scope = _device_scope(scope, device)
    return {
        path: _index(collection, device.get(path, []), _owned_names(path, scope))
        for path, collection in COLLECTIONS.items()
    }


def diff_items(
    desired: Dict[str, List[dict]], device: Dict[str, List[dict]]
) -> RosDiff:
    """Diff parsed rendered items against device REST items.

    Comparison per owned item covers exactly the properties barf
    renders; device-side extras on an owned item are left alone.
    """
    want_scope = rendered_scope(desired)
    have_scope = _device_scope(want_scope, device)

    diff = RosDiff()
    for path, collection in COLLECTIONS.items():
        want = _index(collection, desired.get(path, []), _owned_names(path, want_scope))
        have = _index(collection, device.get(path, []), _owned_names(path, have_scope))

        for key, props in want.items():
            if key not in have:
                diff.added.append((path, props))
                continue
            current = have[key]
            deltas = [
                (prop, current.get(prop), value)
                for prop, value in props.items()
                if prop not in collection.write_only and current.get(prop) != value
            ]
            if deltas:
                diff.changed.append((path, key, deltas))

        for key in have:
            if key not in want:
                diff.removed.append((path, key))

    for path in SETTINGS:
        if path not in desired:
            # Barf renders nothing here: the singleton stays entirely
            # hand-managed (a singleton is never removed).
            continue
        want_props = desired[path][0]
        have_list = device.get(path) or [{}]
        current = have_list[0]
        deltas = [
            (prop, current.get(prop), value)
            for prop, value in want_props.items()
            if current.get(prop) != value
        ]
        if deltas:
            diff.changed.append((path, "settings", deltas))

    return diff


def excluded_items(
    device: Dict[str, List[dict]],
    scope: OwnedScope = OwnedScope(),
) -> List[Tuple[str, str, dict]]:
    """The hand-managed device items barf deliberately keeps.

    Non-dynamic items in the modeled collections that an ``excluded``
    predicate protects -- everything barf is *not* yet managing within
    the collections it looks at. ``ignored`` items (recorded RouterOS
    defaults) are omitted entirely. Backs ``config diff
    --show-device-only`` so the excluded set is visible and can be
    shrunk deliberately. Pass the rendered scope so items barf now owns
    drop off the kept list.

    Returns:
        ``(collection, identity label, raw item)`` triples.
    """
    scope = _device_scope(scope, device)
    kept: List[Tuple[str, str, dict]] = []
    for path, collection in COLLECTIONS.items():
        names = _owned_names(path, scope)
        for item in device.get(path, []):
            if item.get("dynamic") == "true":
                continue
            if collection.ignored(item, names):
                continue
            if not collection.excluded(item, names):
                continue
            if collection.identity:
                label = collection.identity(item) or format_props(item)
            else:
                label = f"{item.get('chain', '')}: {item.get('rule', '')}".strip()
            kept.append((path, label, item))
    return kept


def format_excluded(
    device: Dict[str, List[dict]],
    scope: OwnedScope = OwnedScope(),
) -> str:
    """A human-readable listing of the kept (hand-managed) items."""
    return "\n".join(
        f"= /{path} [{label}] (device-only: hand-managed, kept)"
        for path, label, _item in excluded_items(device, scope)
    )


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
