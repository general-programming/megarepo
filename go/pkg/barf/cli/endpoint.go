package cli

import (
	"context"
	"net"
	"strconv"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// apiPort is the HTTPS management API port probed to decide which of a
// host's addresses is reachable, matching the Python implementation.
const apiPort = 443

// endpointProbeTimeout is the per-candidate TCP connect timeout.
var endpointProbeTimeout = 2 * time.Second

// dialContext is the dialer used for endpoint probing; tests replace it.
var dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
	d := net.Dialer{Timeout: endpointProbeTimeout}
	return d.DialContext(ctx, network, address)
}

// endpointCandidates are the addresses to try for reaching a host, most
// specific first: FQDN, the management interface address, the host's own
// addresses, then the remaining interface addresses. Ported from
// BaseHost._endpoint_candidates.
func endpointCandidates(h *model.Host, searchDomain string) []string {
	var candidates []string
	add := func(s string) {
		if s == "" {
			return
		}
		for _, existing := range candidates {
			if existing == s {
				return
			}
		}
		candidates = append(candidates, s)
	}

	if searchDomain != "" {
		add(h.Hostname + "." + searchDomain)
	}
	for _, iface := range h.Interfaces {
		if !iface.Management {
			continue
		}
		for _, a := range iface.Addresses {
			add(a.IP.String())
		}
	}
	for _, a := range []*model.Address{h.Address, h.IP6Address} {
		if a != nil {
			add(a.IP.String())
		}
	}
	// Globally routable interface addresses before private ones, as the
	// fleet's management path is frequently only reachable globally.
	var global, local []string
	for _, iface := range h.Interfaces {
		for _, a := range iface.Addresses {
			if a.IP.IsPrivate() || a.IP.IsLoopback() || a.IP.IsLinkLocalUnicast() {
				local = append(local, a.IP.String())
				continue
			}
			global = append(global, a.IP.String())
		}
	}
	for _, s := range append(global, local...) {
		add(s)
	}
	return candidates
}

// probeEndpoint returns the first candidate address answering on the
// management API port, or "" when nothing answers. Connecting and
// immediately closing is the only device contact this makes.
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
