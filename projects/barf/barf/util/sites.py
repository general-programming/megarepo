"""Geographic distance-based BGP path weighting for the WireGuard fabric.

Every leaf peers with all four spines (2 in Fremont, 2 in Seattle);
eBGP with per-site ASNs and multipath/ECMP means every candidate path
for a same-site prefix has an equal AS-path length, so ECMP happily
sprays local traffic through the other coast. The fix: tag routes
with their origin site via a BGP large community on origination, and
set local-pref at import based on the geographic distance between the
importing device, the neighbor it heard the route from, and the
route's origin site.

This module owns the distance math and the site table; the vendor
templates render the resulting integers into route-maps/filters
verbatim (routers receive literal numbers, never do the math
themselves).
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from functools import lru_cache
from typing import TYPE_CHECKING, Dict, List, Optional, Tuple

if TYPE_CHECKING:
    from barf.model.wireguard import WGNetworkLink
    from barf.vendors import BaseHost

EARTH_RADIUS_KM = 6371.0

# Baseline local-preference for fabric-learned routes. Distance
# penalties (in km) are subtracted from this, so it must stay
# comfortably above FRR/bird's default local-pref (100) that untagged
# (non-fabric-originated, e.g. external peers) routes keep.
BASE_LOCAL_PREF = 1_000_000

# Large-community "function" field identifying a site-origin tag:
# <community_asn>:SITE_ORIGIN_FUNC:<site_id>. The first field is the
# fabric-wide Global Administrator value from network.yml's
# global_meta.community_asn (RFC 8195), NOT the originating host's
# ASN: a single well-known value lets importers match the exact
# triplet. bird's set grammar cannot wildcard the first field, so
# per-host ASNs there would be unmatchable.
SITE_ORIGIN_FUNC = 1


@dataclass(frozen=True)
class Site:
    """One entry of network.yml's ``global_meta.sites``."""

    name: str
    id: int
    coords: Tuple[float, float]


@lru_cache(maxsize=None)
def haversine_km(a: Tuple[float, float], b: Tuple[float, float]) -> int:
    """Great-circle distance between two ``(lat, lon)`` pairs, in whole km."""
    lat1, lon1 = math.radians(a[0]), math.radians(a[1])
    lat2, lon2 = math.radians(b[0]), math.radians(b[1])
    dlat = lat2 - lat1
    dlon = lon2 - lon1
    h = (
        math.sin(dlat / 2) ** 2
        + math.cos(lat1) * math.cos(lat2) * math.sin(dlon / 2) ** 2
    )
    return round(2 * EARTH_RADIUS_KM * math.asin(math.sqrt(h)))


def site_distance_km(a: Optional[Site], b: Optional[Site]) -> int:
    """Distance between two sites; 0 for the same site or missing data."""
    if a is None or b is None or a.name == b.name:
        return 0
    return haversine_km(a.coords, b.coords)


def parse_sites(sites_meta: Optional[dict]) -> Dict[str, Site]:
    """Parse network.yml's ``global_meta.sites`` into ``name -> Site``.

    Args:
        sites_meta: The raw ``{name: {id, coords}}`` mapping, or None
            when network.yml has no ``sites`` block at all.

    Raises:
        ValueError: two sites share an ``id``.
    """
    sites: Dict[str, Site] = {}
    seen_ids: Dict[int, str] = {}
    for name, meta in (sites_meta or {}).items():
        site_id = meta["id"]
        if site_id in seen_ids:
            raise ValueError(
                f"global_meta.sites: id {site_id} used by both "
                f"{seen_ids[site_id]!r} and {name!r}"
            )
        seen_ids[site_id] = name
        lat, lon = meta["coords"]
        sites[name] = Site(name=name, id=site_id, coords=(float(lat), float(lon)))
    return sites


@dataclass(frozen=True)
class ImportRule:
    """One import rule: routes tagged with ``site_id`` get ``local_pref``."""

    site_id: int
    local_pref: int


def neighbor_import_rules(
    device_site: Site, neighbor_site: Site, sites: Dict[str, Site]
) -> List[ImportRule]:
    """Per-origin-site import rules for routes heard from ``neighbor_site``.

    ``local_pref = BASE_LOCAL_PREF - (dist(device, neighbor) +
    dist(neighbor, origin))``, for every known site, ordered by site id
    for a stable, deterministic render.
    """
    base = site_distance_km(device_site, neighbor_site)
    ordered = sorted(sites.values(), key=lambda s: s.id)
    return [
        ImportRule(
            site_id=origin.id,
            local_pref=BASE_LOCAL_PREF
            - (base + site_distance_km(neighbor_site, origin)),
        )
        for origin in ordered
    ]


def site_import_rules(
    device: "BaseHost", links: List["WGNetworkLink"], sites: Dict[str, Site]
) -> Dict[str, List[ImportRule]]:
    """``neighbor site name -> import rules`` for a device's fabric links.

    ``links`` must already be the device's own links (the render layer
    passes exactly those). Empty when the device has no site (there is
    nothing to weight against) or its site is unknown. Keyed by the
    NEIGHBOR's site rather than by individual neighbor host: the
    computed rules only depend on (device site, neighbor site), so
    peers in the same site always get identical treatment. That keeps
    the number of generated route-maps/filters small (one per neighbor
    site, not one per neighbor) on both the VyOS/FRR and bird sides.

    External peers (e.g. OCI) are skipped even when they carry a
    ``site`` (recorded for their physical location only): they send
    untagged routes and get no import weighting, only the default
    local-pref.
    """
    device_site = sites.get(device.site) if device.site else None
    if device_site is None:
        return {}

    result: Dict[str, List[ImportRule]] = {}
    for link in links:
        peer = link.side_b if link.side_a is device else link.side_a
        if peer.devicetype == "external":
            continue
        if not peer.site or peer.site in result:
            continue
        peer_site = sites.get(peer.site)
        if peer_site is None:
            continue
        result[peer.site] = neighbor_import_rules(device_site, peer_site, sites)

    return result
