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

// ManagementAddress returns the address of h's management interface,
// preferring IPv6, or the zero Addr when h has no management interface.
// Mirrors Python BaseHost.management_address.
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

// EndpointCandidates lists the addresses to try for reaching h, most
// specific first: FQDN, management address, the host's own addresses,
// then interface addresses with global ones first. Deduplicated, order
// preserved. Mirrors Python BaseHost._endpoint_candidates.
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
		// Python takes (ip6_address, address) per interface: the first
		// address of each family, v6 first.
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
	// External (global) addresses first; stable, like Python's sort.
	sort.SliceStable(ifaceIPs, func(i, j int) bool {
		return isGlobal(ifaceIPs[i]) && !isGlobal(ifaceIPs[j])
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

// isGlobal approximates Python's ipaddress is_global: publicly routable.
func isGlobal(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	if ip.Is4() || ip.Is4In6() {
		v4 := ip.As4()
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64/10 CGNAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0/24
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 2: // TEST-NET-1
			return false
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18/15 benchmarking
			return false
		case v4[0] == 198 && v4[1] == 51 && v4[2] == 100: // TEST-NET-2
			return false
		case v4[0] == 203 && v4[1] == 0 && v4[2] == 113: // TEST-NET-3
			return false
		case v4[0] >= 240: // reserved / broadcast
			return false
		}
		return true
	}
	// IPv6: ULA (fc00::/7) is not global; netip.IsPrivate covers it, but
	// be explicit about the documentation range too.
	if ip.Is6() {
		b := ip.As16()
		if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 { // 2001:db8::/32
			return false
		}
	}
	return true
}

// ProbeEndpoint returns the first candidate answering a TCP connect on
// port. Mirrors Python BaseHost._probe_endpoint (which backs
// management_ip). It opens and immediately closes a connection; nothing
// is sent.
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

// endpointResolver caches the probed endpoint for one device, so a Reader
// probes at most once regardless of how many reads it serves (Python
// memoizes _probe_endpoint with lru_cache).
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

// hostForURL wraps a bare IPv6 literal in brackets for use in a URL.
func hostForURL(address string) string {
	if ip, err := netip.ParseAddr(address); err == nil && ip.Is6() && !ip.Is4In6() {
		return "[" + address + "]"
	}
	return address
}
