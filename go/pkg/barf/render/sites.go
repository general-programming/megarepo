package render

import (
	"math"
	"sort"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Port of barf/util/sites.py: geographic distance-based BGP path weighting.
// This file owns the distance math; vendor blocks render the resulting
// integers verbatim, routers never compute them.

const earthRadiusKM = 6371.0

// baseLocalPref is the baseline local-preference for fabric-learned routes;
// distance penalties (km) are subtracted, so it must stay above bird's 100.
const baseLocalPref = 1_000_000

// siteOriginFunc is the function field of <community_asn>:func:<site_id>.
const siteOriginFunc = 1

// haversineKM is the great-circle distance in whole km; rounding is
// half-to-even to match Python's round().
func haversineKM(a, b [2]float64) int {
	lat1, lon1 := a[0]*math.Pi/180, a[1]*math.Pi/180
	lat2, lon2 := b[0]*math.Pi/180, b[1]*math.Pi/180
	dlat, dlon := lat2-lat1, lon2-lon1
	h := math.Pow(math.Sin(dlat/2), 2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin(dlon/2), 2)
	return int(math.RoundToEven(2 * earthRadiusKM * math.Asin(math.Sqrt(h))))
}

// siteDistanceKM is the distance between two sites; 0 if same or unknown.
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

// SiteRules is one neighbor site's import rules. A slice, not a map: render
// order follows link declaration order, which is byte-parity contract.
type SiteRules struct {
	Site  string
	Rules []ImportRule
}

// neighborImportRules computes, for every known site ordered by id,
//
//	local_pref = baseLocalPref - (dist(device, neighbor) + dist(neighbor, origin))
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

// siteImportRules returns the import rules for a device's fabric links, keyed
// by the NEIGHBOR's site: rules depend only on (device site, neighbor site).
// External peers are skipped even when they carry a `site`, since they send
// untagged routes.
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
