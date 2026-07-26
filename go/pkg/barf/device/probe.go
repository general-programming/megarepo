package device

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// ManagementAddress returns h's management interface address, preferring
// IPv6, or the zero Addr when there is none.
func ManagementAddress(h *model.Host) netip.Addr {
	for _, iface := range h.Interfaces {
		if !iface.Management {
			continue
		}
		var v4 netip.Addr
		for _, addr := range iface.Addresses {
			if addr.IP.Is6() && !addr.IP.Is4In6() {
				return addr.IP
			}
			if !v4.IsValid() {
				v4 = addr.IP
			}
		}
		return v4
	}
	return netip.Addr{}
}

// EndpointCandidates lists the addresses to try for h, most specific first:
// FQDN, management address, host addresses, then interface addresses with
// global ones first. Deduplicated, order preserved.
func EndpointCandidates(h *model.Host, domain string) []string {
	if domain == "" {
		domain = DefaultDomain
	}

	candidates := []string{fmt.Sprintf("%s.%s", h.Hostname, domain)}
	if mgmt := ManagementAddress(h); mgmt.IsValid() {
		candidates = append(candidates, mgmt.String())
	}
	for _, addr := range []*model.Address{h.Address, h.IP6Address} {
		if addr != nil && addr.IP.IsValid() {
			candidates = append(candidates, addr.IP.String())
		}
	}

	var ifaceIPs []netip.Addr
	for _, iface := range h.Interfaces {
		// Python takes the first address of each family per interface, v6 first.
		var v6, v4 netip.Addr
		for _, addr := range iface.Addresses {
			if addr.IP.Is6() && !addr.IP.Is4In6() {
				if !v6.IsValid() {
					v6 = addr.IP
				}
			} else if !v4.IsValid() {
				v4 = addr.IP
			}
		}
		for _, ip := range []netip.Addr{v6, v4} {
			if ip.IsValid() {
				ifaceIPs = append(ifaceIPs, ip)
			}
		}
	}
	// Global addresses first, stably. Must be model.IsGlobal: it matches
	// Python's ipaddress on 0.0.0.0/8, 64:ff9b:1::/48, 100::/64, 2001::/23 and
	// 2002::/16, which a hand-rolled check calls routable.
	sort.SliceStable(ifaceIPs, func(i, j int) bool {
		return model.IsGlobal(ifaceIPs[i]) && !model.IsGlobal(ifaceIPs[j])
	})
	for _, ip := range ifaceIPs {
		candidates = append(candidates, ip.String())
	}

	seen := make(map[string]bool, len(candidates))
	out := candidates[:0:0]
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	return out
}

// ProbeEndpoint returns the first candidate answering a TCP connect on port;
// it opens and immediately closes a connection, sending nothing.
func ProbeEndpoint(ctx context.Context, h *model.Host, port int, domain string, timeout time.Duration) (string, error) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	var lastErr error
	for _, address := range EndpointCandidates(h, domain) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, strconv.Itoa(port)))
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.Close()
		return address, nil
	}
	return "", fmt.Errorf("%s: no address answering on %d (last error: %v)", h.Hostname, port, lastErr)
}

// endpointResolver caches the probed endpoint, so a Reader probes at most
// once however many reads it serves.
type endpointResolver struct {
	host  *model.Host
	opts  Options
	once  sync.Once
	value string
	err   error
}

func (r *endpointResolver) resolve(ctx context.Context) (string, error) {
	if r.opts.Endpoint != "" {
		return r.opts.Endpoint, nil
	}
	r.once.Do(func() {
		r.value, r.err = ProbeEndpoint(ctx, r.host, r.opts.port(), r.opts.domain(), 2*time.Second)
	})
	return r.value, r.err
}

// HostForURL wraps a bare IPv6 literal in brackets for use in a URL.
func HostForURL(address string) string {
	if ip, err := netip.ParseAddr(address); err == nil && ip.Is6() && !ip.Is4In6() {
		return "[" + address + "]"
	}
	return address
}
