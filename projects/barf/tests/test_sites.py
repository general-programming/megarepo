import pytest
from conftest import FMT2, SEA, make_link

from barf.util.sites import (
    BASE_LOCAL_PREF,
    Site,
    haversine_km,
    neighbor_import_rules,
    parse_sites,
    site_distance_km,
    site_import_rules,
)
from barf.vendors import BaseHost
from barf.vendors.external import ExternalHost

ORD = Site(name="ord", id=3, coords=(41.97, -87.91))


class TestHaversine:
    def test_sea_to_fmt2_is_about_1120km(self):
        # Seattle <-> Fremont, the fabric's real cross-coast hop.
        assert haversine_km(SEA.coords, FMT2.coords) == pytest.approx(1120, abs=30)

    def test_same_point_is_zero(self):
        assert haversine_km(SEA.coords, SEA.coords) == 0

    def test_symmetric(self):
        assert haversine_km(SEA.coords, FMT2.coords) == haversine_km(
            FMT2.coords, SEA.coords
        )


class TestSiteDistanceKm:
    def test_same_site_is_zero_even_with_different_coords_objects(self):
        other_sea = Site(name="sea", id=1, coords=(0.0, 0.0))
        assert site_distance_km(SEA, other_sea) == 0

    def test_missing_site_is_zero(self):
        assert site_distance_km(None, FMT2) == 0
        assert site_distance_km(SEA, None) == 0

    def test_different_sites_use_haversine(self):
        assert site_distance_km(SEA, FMT2) == haversine_km(SEA.coords, FMT2.coords)


class TestParseSites:
    def test_none_is_empty(self):
        assert parse_sites(None) == {}

    def test_parses_id_and_coords(self):
        sites = parse_sites(
            {
                "sea": {"id": 1, "coords": [47.61, -122.33]},
                "fmt2": {"id": 2, "coords": [37.55, -121.99]},
            }
        )
        assert sites["sea"] == Site(name="sea", id=1, coords=(47.61, -122.33))
        assert sites["fmt2"].id == 2

    def test_duplicate_id_is_an_error(self):
        with pytest.raises(ValueError, match="id 1 used by both"):
            parse_sites(
                {
                    "sea": {"id": 1, "coords": [0, 0]},
                    "sea2": {"id": 1, "coords": [1, 1]},
                }
            )


class TestNeighborImportRules:
    def test_same_site_neighbor_and_origin_is_base_pref(self):
        sites = {"sea": SEA, "fmt2": FMT2}
        rules = neighbor_import_rules(SEA, SEA, sites)
        origin_sea = next(r for r in rules if r.site_id == SEA.id)
        assert origin_sea.local_pref == BASE_LOCAL_PREF

    def test_cross_site_penalizes_by_distance(self):
        sites = {"sea": SEA, "fmt2": FMT2}
        rules = neighbor_import_rules(SEA, FMT2, sites)
        # Heard from fmt2, tagged as originating in sea: penalized twice
        # (device<->neighbor plus neighbor<->origin), both dist(sea,fmt2).
        origin_sea = next(r for r in rules if r.site_id == SEA.id)
        d = haversine_km(SEA.coords, FMT2.coords)
        assert origin_sea.local_pref == BASE_LOCAL_PREF - 2 * d

    def test_ordered_by_site_id(self):
        sites = {"sea": SEA, "fmt2": FMT2, "ord": ORD}
        rules = neighbor_import_rules(SEA, FMT2, sites)
        assert [r.site_id for r in rules] == [1, 2, 3]


class TestSiteImportRules:
    def test_empty_when_device_has_no_site(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1)
        neighbor = BaseHost(hostname="spine", role="vpn", asn=2, site="fmt2")
        links = [make_link(1, neighbor, device)]
        assert site_import_rules(device, links, {"fmt2": FMT2}) == {}

    def test_empty_when_device_site_unknown(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1, site="nowhere")
        assert site_import_rules(device, [], {"fmt2": FMT2}) == {}

    def test_keyed_by_neighbor_site_not_hostname(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1, site="sea")
        spine1 = BaseHost(hostname="spine-1", role="vpn", asn=2, site="fmt2")
        spine2 = BaseHost(hostname="spine-2", role="vpn", asn=3, site="fmt2")
        links = [make_link(1, spine1, device), make_link(2, spine2, device)]
        rules = site_import_rules(device, links, {"sea": SEA, "fmt2": FMT2})
        assert list(rules.keys()) == ["fmt2"]

    def test_neighbor_without_site_is_skipped(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1, site="sea")
        neighbor = BaseHost(hostname="spine", role="vpn", asn=2)
        links = [make_link(1, neighbor, device)]
        assert site_import_rules(device, links, {"sea": SEA}) == {}

    def test_external_peer_is_skipped_even_with_a_site(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1, site="sea")
        oracle = ExternalHost(hostname="oracle-1", role="vpn", asn=31898, site="iad")
        links = [make_link(1, oracle, device)]
        assert site_import_rules(device, links, {"sea": SEA, "iad": FMT2}) == {}

    def test_exact_local_pref_values(self):
        device = BaseHost(hostname="leaf", role="vpn", asn=1, site="sea")
        spine = BaseHost(hostname="spine", role="vpn", asn=2, site="fmt2")
        links = [make_link(1, spine, device)]
        sites = {"sea": SEA, "fmt2": FMT2}
        rules = site_import_rules(device, links, sites)["fmt2"]
        d = haversine_km(SEA.coords, FMT2.coords)
        by_id = {r.site_id: r.local_pref for r in rules}
        # base = dist(device=sea, neighbor=fmt2) = d.
        # origin sea:  BASE - (d + dist(neighbor=fmt2, origin=sea))  = BASE - 2d.
        # origin fmt2: BASE - (d + dist(neighbor=fmt2, origin=fmt2)) = BASE - d.
        assert by_id[SEA.id] == BASE_LOCAL_PREF - 2 * d
        assert by_id[FMT2.id] == BASE_LOCAL_PREF - d
