"""Per-feature config blocks replacing the vpn Jinja templates.

Vendors cut over whole: a (role, devicetype) pair present in
BLOCK_REGISTRY renders via its ordered block list, everything else
keeps the legacy template path. Output is byte-identical to the
templates it replaces (tests/golden/).
"""

from typing import TYPE_CHECKING, Dict, List, Tuple, Type

from barf.configs.base import ConfigBlock, RenderContext, UnsupportedFeature
from barf.util.sites import site_import_rules

if TYPE_CHECKING:
    from barf.model.wireguard import WGNetworkLink
    from barf.vendors import BaseHost

__all__ = [
    "BLOCK_REGISTRY",
    "ConfigBlock",
    "RenderContext",
    "UnsupportedFeature",
    "build_context",
    "render_blocks",
    "renders_with_blocks",
]

from barf.configs import fabric, firewall, interfaces, routing, system

# Ordered block lists keyed by (role, devicetype): list order IS output
# order, part of the byte-parity contract, and it is per-vendor because
# the templates each grew their own section order. The block CLASSES
# are shared across vendors (one class per feature, one method per
# vendor); only the ordering and vendor-specific boilerplate (e.g. the
# RouterOS header) differ per list. Pairs appear here as their port
# lands (mikrotik first).
BLOCK_REGISTRY: Dict[Tuple[str, str], List[Type[ConfigBlock]]] = {
    ("vpn", "vyos"): [
        system.VyosSystem,
        interfaces.VyosInterfaces,
        system.VyosSshAccess,
        system.VyosPlatform,
        firewall.FirewallGroups,
        system.VyosNtp,
        routing.Ospf,
        routing.StaticRoutes,
        firewall.Nat,
        fabric.IpsecDefaults,
        fabric.SiteWeighting,
        fabric.FabricWireGuard,
        fabric.FabricBGP,
        system.ExtraConfig,
    ],
    ("vpn", "mikrotik"): [
        system.MikrotikHeader,
        fabric.FabricWireGuard,
        interfaces.Bridges,
        interfaces.StaticWireGuard,
        fabric.AnnouncedNetworks,
        firewall.FirewallGroups,
        fabric.SiteWeighting,
        fabric.FabricBGP,
        fabric.TransitBGP,
        system.ExtraConfig,
    ],
}


def renders_with_blocks(host: "BaseHost") -> bool:
    """Whether this host's config renders via blocks (vs. a template)."""
    return (host.role, host.devicetype) in BLOCK_REGISTRY


def build_context(
    host: "BaseHost",
    links: List["WGNetworkLink"],
    global_meta: dict,
    secrets,
) -> RenderContext:
    """Assemble the render context for one network.yml host.

    Selects the host's own fabric links and precomputes the geographic
    weighting inputs (barf/util/sites.py owns the semantics; blocks and
    templates only format the ready-made values into vendor syntax).
    """
    ctx = RenderContext(host=host, global_meta=global_meta, secrets=secrets)
    if host.role != "vpn":
        return ctx

    ctx.vpn_links = [
        link for link in links if host == link.side_a or host == link.side_b
    ]
    sites = global_meta.get("sites") or {}
    ctx.site_import_rules = site_import_rules(host, ctx.vpn_links, sites)
    ctx.origin_site = sites.get(host.site) if host.site else None
    ctx.community_asn = global_meta.get("community_asn")

    # bird cannot compose a standalone import_filter with the
    # generated site-weighted filters (filters cannot call
    # filters), so the combination would silently drop the
    # host's own filter on every weighted peer. Fail fast.
    if ctx.site_import_rules and (getattr(host, "bird", None) or {}).get(
        "import_filter"
    ):
        raise ValueError(
            f"{host.hostname}: bird.import_filter cannot be combined"
            " with site weighting; expose the check as a function and"
            " set bird.import_check_function instead"
        )

    # The mikrotik template has no IPsec branch: such a link would
    # render as WireGuard (generating spurious keypairs into
    # Vault) and then vanish from the diff via ownership scoping.
    # Fail fast instead of emitting broken config.
    if host.devicetype == "mikrotik":
        for link in ctx.vpn_links:
            if link.ipsec:
                raise ValueError(
                    f"{host.hostname}: ipsec link to"
                    f" {link.side_a.hostname if link.side_a != host else link.side_b.hostname}"
                    " is not supported on mikrotik hosts"
                )

    return ctx


def render_blocks(ctx: RenderContext) -> str:
    """Render a host by concatenating its role's config blocks.

    Same flat-string contract as the templates: blocks emit their
    exact lines (including deliberate blank separators) and the result
    ends in one newline.
    """
    lines: List[str] = []
    for block_cls in BLOCK_REGISTRY[(ctx.host.role, ctx.host.devicetype)]:
        block = block_cls(ctx)
        if not block.applies():
            continue
        lines.extend(block.emit(ctx.host.devicetype))
    return "\n".join(lines) + "\n"
