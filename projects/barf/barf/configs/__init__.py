"""Per-feature config blocks replacing the vpn Jinja templates.

Vendors cut over whole: a devicetype listed in BLOCK_VENDORS renders
via the ordered block registry, everything else keeps the legacy
template path. Output is byte-identical to the templates it replaces
(tests/golden/).
"""

from typing import TYPE_CHECKING, Dict, FrozenSet, List, Type

from barf.configs.base import ConfigBlock, RenderContext, UnsupportedFeature
from barf.util.sites import site_import_rules

if TYPE_CHECKING:
    from barf.model.wireguard import WGNetworkLink
    from barf.vendors import BaseHost

__all__ = [
    "BLOCK_VENDORS",
    "BLOCKS_BY_ROLE",
    "ConfigBlock",
    "RenderContext",
    "UnsupportedFeature",
    "build_context",
    "render_blocks",
]

# Ordered per-role block registries: registry order IS output order,
# part of the byte-parity contract. Filled in as features are ported.
BLOCKS_BY_ROLE: Dict[str, List[Type[ConfigBlock]]] = {
    "vpn": [],
}

# Devicetypes rendered via blocks instead of the legacy templates.
# Vendors flip here one at a time as their port lands (mikrotik first).
BLOCK_VENDORS: FrozenSet[str] = frozenset()


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
    for block_cls in BLOCKS_BY_ROLE.get(ctx.host.role, []):
        block = block_cls(ctx)
        if not block.applies():
            continue
        lines.extend(block.emit(ctx.host.devicetype))
    return "\n".join(lines) + "\n"
