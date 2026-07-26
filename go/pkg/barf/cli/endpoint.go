package cli

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// apiPort is device.DefaultPort, probed to decide which of a host's addresses
// is reachable. One constant: the probe and the request that follows it must
// agree.
const apiPort = device.DefaultPort

// endpointProbeTimeout is the per-candidate TCP connect timeout.
var endpointProbeTimeout = 2 * time.Second

// dialContext is the dialer used for endpoint probing; tests replace it.
var dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: endpointProbeTimeout}
	return d.DialContext(ctx, network, address)
}

// endpointCandidates are the addresses to try, most specific first: FQDN,
// management interface, the host's own addresses, then the rest, global
// ones first. Delegates to device.EndpointCandidates rather than
// hand-rolling a second ranking: a hand-rolled netip.Addr.IsPrivate /
// IsLoopback / IsLinkLocalUnicast check (the previous implementation here)
// misranks CGNAT (100.64.0.0/10), 6to4, 198.18.0.0/15 and multicast as
// globally routable — device/probe.go's own comment already flags this
// ("must be model.IsGlobal") next to the constructor that gets it right but
// had no caller. It also falls back to device.DefaultDomain for the FQDN
// candidate when searchDomain is empty, matching BaseHost._endpoint_candidates
// (Python never drops the FQDN candidate just because global_meta has no
// search_domain).
func endpointCandidates(h *model.Host, searchDomain string) []string {
	return device.EndpointCandidates(h, searchDomain)
}

// probeEndpoint returns the first candidate answering on the management API
// port, or "". Connect-and-close is its only device contact.
func probeEndpoint(ctx context.Context, h *model.Host, searchDomain string) string {
	for _, candidate := range endpointCandidates(h, searchDomain) {
		if ctx.Err() != nil {
			return ""
		}
		conn, err := dialContext(ctx, "tcp", net.JoinHostPort(candidate, strconv.Itoa(apiPort)))
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate
	}
	return ""
}
