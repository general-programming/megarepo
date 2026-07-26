package cli

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

func TestEndpointCandidatesOrder(t *testing.T) {
	h := &model.Host{
		Hostname: "sea1-vpn-0",
		Address:  &model.Address{IP: netip.MustParseAddr("10.0.0.1")},
		Interfaces: []model.Interface{
			{Name: "eth0", Addresses: []model.Address{{IP: netip.MustParseAddr("192.0.2.5")}}},
			{Name: "eth1", Management: true, Addresses: []model.Address{{IP: netip.MustParseAddr("10.9.9.9")}}},
		},
	}
	got := endpointCandidates(h, "example.invalid")
	want := []string{"sea1-vpn-0.example.invalid", "10.9.9.9", "10.0.0.1", "192.0.2.5"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
}

func TestEndpointCandidatesDeduplicates(t *testing.T) {
	h := &model.Host{
		Hostname: "x",
		Address:  &model.Address{IP: netip.MustParseAddr("10.0.0.1")},
		Interfaces: []model.Interface{
			{Management: true, Addresses: []model.Address{{IP: netip.MustParseAddr("10.0.0.1")}}},
		},
	}
	// The management address and Host.Address are the same IP, so they must
	// collapse to one entry; the FQDN (device.DefaultDomain, since no search
	// domain was given) is a distinct, additional candidate.
	got := endpointCandidates(h, "")
	want := []string{"x." + device.DefaultDomain, "10.0.0.1"}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %v, want %v", got, want)
		}
	}
}

// Regression: an empty global_meta.search_domain used to drop the FQDN
// candidate entirely; Python's BaseHost._endpoint_candidates always emits
// one, falling back to a fleet-wide default domain.
func TestEndpointCandidatesFQDNPresentWithoutSearchDomain(t *testing.T) {
	h := &model.Host{Hostname: "sea1-vpn-0"}
	got := endpointCandidates(h, "")
	if len(got) == 0 || got[0] != "sea1-vpn-0."+device.DefaultDomain {
		t.Fatalf("candidates = %v, want the default-domain FQDN first", got)
	}
}

// Regression: the hand-rolled ranking used netip.Addr.IsPrivate /
// IsLoopback / IsLinkLocalUnicast, which misses CGNAT (100.64.0.0/10) and
// multicast — both would have sorted as "global" (probed first) even though
// neither is actually globally routable.
func TestEndpointCandidatesDoesNotRankCGNATOrMulticastAsGlobal(t *testing.T) {
	h := &model.Host{
		Hostname: "x",
		Interfaces: []model.Interface{
			{Name: "eth0", Addresses: []model.Address{{IP: netip.MustParseAddr("100.64.0.5")}}},
			{Name: "eth1", Addresses: []model.Address{{IP: netip.MustParseAddr("224.0.0.5")}}},
			{Name: "eth2", Addresses: []model.Address{{IP: netip.MustParseAddr("8.8.8.8")}}},
		},
	}
	got := endpointCandidates(h, "example.invalid")
	// The one genuinely global address (8.8.8.8) must sort ahead of the
	// CGNAT and multicast addresses, not just wherever
	// IsPrivate/IsLoopback/IsLinkLocalUnicast happened to leave it.
	globalIdx, cgnatIdx, mcastIdx := -1, -1, -1
	for i, c := range got {
		switch c {
		case "8.8.8.8":
			globalIdx = i
		case "100.64.0.5":
			cgnatIdx = i
		case "224.0.0.5":
			mcastIdx = i
		}
	}
	if globalIdx == -1 || cgnatIdx == -1 || mcastIdx == -1 {
		t.Fatalf("candidates missing an address: %v", got)
	}
	if globalIdx > cgnatIdx || globalIdx > mcastIdx {
		t.Errorf("candidates = %v; the genuinely global address must rank before CGNAT/multicast", got)
	}
}

func TestProbeEndpointPicksFirstAnswer(t *testing.T) {
	old := dialContext
	t.Cleanup(func() { dialContext = old })

	var tried []string
	dialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(address)
		tried = append(tried, host)
		if host == "10.0.0.1" {
			return fakeConn{}, nil
		}
		return nil, errors.New("refused")
	}

	h := &model.Host{Hostname: "x", Address: &model.Address{IP: netip.MustParseAddr("10.0.0.1")}}
	if got := probeEndpoint(context.Background(), h, "example.invalid"); got != "10.0.0.1" {
		t.Errorf("endpoint = %q, want 10.0.0.1", got)
	}
	if len(tried) != 2 || tried[0] != "x.example.invalid" {
		t.Errorf("probe order = %v", tried)
	}
}

func TestProbeEndpointNothingAnswers(t *testing.T) {
	old := dialContext
	t.Cleanup(func() { dialContext = old })
	dialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("refused")
	}

	h := &model.Host{Hostname: "x", Address: &model.Address{IP: netip.MustParseAddr("10.0.0.1")}}
	if got := probeEndpoint(context.Background(), h, ""); got != "" {
		t.Errorf("endpoint = %q, want empty", got)
	}
}

func TestProbeEndpointHonoursContext(t *testing.T) {
	old := dialContext
	t.Cleanup(func() { dialContext = old })
	dialContext = func(context.Context, string, string) (net.Conn, error) {
		t.Error("dialled with a cancelled context")
		return nil, errors.New("refused")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h := &model.Host{Hostname: "x", Address: &model.Address{IP: netip.MustParseAddr("10.0.0.1")}}
	if got := probeEndpoint(ctx, h, ""); got != "" {
		t.Errorf("endpoint = %q, want empty", got)
	}
}
