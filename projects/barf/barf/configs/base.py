"""Typed render context and the per-feature ConfigBlock contract.

The config-blocks refactor replaces the vpn Jinja templates with
per-feature classes: one class per feature, one method per vendor,
emitting the exact strings the templates emit today (tests/golden/
holds the byte-for-byte parity contract). The diff/deploy machinery
keeps parsing the same rendered text and does not change.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import TYPE_CHECKING, Dict, List, Optional

if TYPE_CHECKING:
    from barf.model.wireguard import WGNetworkLink
    from barf.util.sites import ImportRule, Site
    from barf.vendors import BaseHost


@dataclass
class RenderContext:
    """Everything a block needs to render one host.

    The typed form of the role_meta dict barf/util/render.py assembles
    for the templates; barf.configs.build_context is the single place
    that computes it.
    """

    host: "BaseHost"
    global_meta: dict
    # The Vault secret fetcher (util.secrets.VaultSecrets or a test
    # fake); supports both attribute and item access.
    secrets: object
    # The host's own fabric links, already filtered from the full set.
    vpn_links: List["WGNetworkLink"] = field(default_factory=list)
    # Geographic weighting inputs (empty/None for unweighted hosts);
    # barf/util/sites.py owns the semantics, blocks only format them.
    site_import_rules: Dict[str, List["ImportRule"]] = field(default_factory=dict)
    origin_site: Optional["Site"] = None
    community_asn: Optional[int] = None


def secret_value(secrets, key: str) -> str:
    """A secret by attribute or item access, whichever the fetcher has.

    Mirrors how the templates resolved ``secrets.x`` / ``secrets[x]``:
    VaultSecrets serves everything via __getattr__, test fakes are
    plain dicts.
    """
    try:
        return getattr(secrets, key)
    except AttributeError:
        return secrets[key]


class UnsupportedFeature(NotImplementedError):
    """A host activates a feature its vendor cannot express.

    The structural form of the fail-loud precedent (mikrotik ipsec
    links, RouterOS-only firewall matchers on VyOS): inexpressible
    intent aborts the render instead of silently skipping.
    """

    def __init__(self, host: "BaseHost", block: type, vendor: str) -> None:
        self.host = host
        self.block = block
        self.vendor = vendor
        super().__init__(
            f"{host.hostname}: {block.__name__} cannot render on {vendor!r} devices"
        )


class ConfigBlock:
    """One feature's rendering across all vendors.

    Subclasses define one method per devicetype (``vyos()``,
    ``mikrotik()``, ... -> list of config lines). The base class
    deliberately defines none of them, giving each feature three
    states: ``applies()`` False -> skipped silently; applies and the
    vendor method exists -> emitted; applies but no method -> the
    render fails loudly with UnsupportedFeature.
    """

    def __init__(self, ctx: RenderContext) -> None:
        self.ctx = ctx
        self.host = ctx.host

    def applies(self) -> bool:
        """Whether the host activates this feature."""
        return True

    def emit(self, vendor: str) -> List[str]:
        """The feature's config lines in ``vendor``'s dialect."""
        emitter = getattr(self, vendor, None)
        if emitter is None:
            raise UnsupportedFeature(self.host, type(self), vendor)
        return emitter()
