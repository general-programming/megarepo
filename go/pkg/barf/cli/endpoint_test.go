package cli

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

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
	if got := endpointCandidates(h, ""); len(got) != 1 {
		t.Errorf("candidates = %v, want one entry", got)
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
