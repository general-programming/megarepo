package render

import (
	"math"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Port of barf/util/sites.py: geographic distance-based BGP path
// weighting. This module owns the distance math; the vendor blocks
// render the resulting integers verbatim (routers receive literal
// numbers, never do the math themselves).

const earthRadiusKM = 6371.0

// baseLocalPref is the baseline local-preference for fabric-learned
// routes. Distance penalties (in km) are subtracted from it, so it must
// stay comfortably above FRR/bird's default local-pref of 100.
const baseLocalPref = 1_000_000

// siteOriginFunc is the large-community "function" field identifying a
// site-origin tag: <community_asn>:siteOriginFunc:<site_id>.
const siteOriginFunc = 1

// haversineKM is the great-circle distance between two (lat, lon)
// pairs, in whole km. Rounding is half-to-even to match Python's round().
func haversineKM(a, b [2]float64) int {
	lat1, lon1 := a[0]*math.Pi/180, a[1]*math.Pi/180
	lat2, lon2 := b[0]*math.Pi/180, b[1]*math.Pi/180
	dlat, dlon := lat2-lat1, lon2-lon1
	h := math.Pow(math.Sin(dlat/2), 2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dlon/2), 2)
	return int(math.RoundToEven(2 * earthRadiusKM * math.Asin(math.Sqrt(h))))
}

// siteDistanceKM is the distance between two sites; 0 for the same site
// or missing data.
func siteDistanceKM(a, b *model.Site) int {
	if a == nil || b == nil || a.Name == b.Name {
		return 0
	}
	return haversineKM(a.Coords, b.Coords)
}

// ImportRule says routes tagged with SiteID get LocalPref.
type ImportRule struct {
	SiteID    int
	LocalPref int
}

// SiteRules is one neighbor site's import rules. A slice rather than a
// map: the render order is the order the host's links were declared,
// and that ordering is part of the byte-parity contract.
type SiteRules struct {
	Site  string
	Rules []ImportRule
}

// neighborImportRules computes the per-origin-site import rules for
// routes heard from neighborSite:
//
//	local_pref = baseLocalPref - (dist(device, neighbor) + dist(neighbor, origin))
//
// for every known site, ordered by site id for a stable render.
func neighborImportRules(deviceSite, neighborSite *model.Site, sites map[string]model.Site) []ImportRule {
	base := siteDistanceKM(deviceSite, neighborSite)
	rules := make([]ImportRule, 0, len(sites))
	for _, origin := range sitesByID(sites) {
		rules = append(rules, ImportRule{
			SiteID:    origin.ID,
			LocalPref: baseLocalPref - (base + siteDistanceKM(neighborSite, &origin)),
		})
	}
	return rules
}

// sitesByID returns the sites ordered by id, the fleet-wide stable order.
func sitesByID(sites map[string]model.Site) []model.Site {
	ordered := make([]model.Site, 0, len(sites))
	for _, site := range sites {
		ordered = append(ordered, site)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	return ordered
}

// siteImportRules returns the neighbor-site import rules for a device's
// fabric links, keyed by the NEIGHBOR's site rather than by individual
// neighbor host: the rules depend only on (device site, neighbor site),
// so peers in the same site always get identical treatment.
//
// External peers are skipped even when they carry a `site` (recorded for
// their physical location only): they send untagged routes.
func siteImportRules(host *model.Host, links []model.Link, n *model.Network) []SiteRules {
	sites := n.Global.Sites
	deviceSite, ok := sites[host.Site]
	if host.Site == "" || !ok {
		return nil
	}

	var out []SiteRules
	seen := map[string]bool{}
	for _, link := range links {
		peer, found := n.Host(link.Other(host.Hostname))
		if !found || peer.DeviceType == "external" {
			continue
		}
		if peer.Site == "" || seen[peer.Site] {
			continue
		}
		peerSite, known := sites[peer.Site]
		if !known {
			continue
		}
		seen[peer.Site] = true
		out = append(out, SiteRules{
			Site:  peer.Site,
			Rules: neighborImportRules(&deviceSite, &peerSite, sites),
		})
	}
	return out
}

// rulesFor returns the import rules for a neighbor site, if any.
func rulesFor(all []SiteRules, site string) []ImportRule {
	if site == "" {
		return nil
	}
	for _, entry := range all {
		if entry.Site == site {
			return entry.Rules
		}
	}
	return nil
}
