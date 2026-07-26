package device

import (
	"context"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

func addr(s string) model.Address {
	prefix := netip.MustParsePrefix(s)
	return model.Address{IP: prefix.Addr(), Prefix: prefix.Bits()}
}

func addrPtr(s string) *model.Address {
	a := addr(s)
	return &a
}

func TestEndpointCandidates(t *testing.T) {
	host := &model.Host{
		Hostname:   "fmt2-vpn-spine-1",
		Address:    addrPtr("10.255.2.1/32"),
		IP6Address: addrPtr("2602:fd37::1/128"),
		Interfaces: []model.Interface{
			{
				Name:       "dum0",
				Management: true,
				// v6 wins for the management address.
				Addresses: []model.Address{addr("10.65.0.9/32"), addr("fd00:65::9/128")},
			},
			{
				Name:      "eth0",
				Addresses: []model.Address{addr("192.168.1.5/24")},
			},
			{
				Name: "eth1",
				// Global addresses sort ahead of the private eth0 one.
				Addresses: []model.Address{addr("2001:470:1::5/64"), addr("23.150.40.5/24")},
			},
		},
	}

	got := EndpointCandidates(host, "")
	want := []string{
		"fmt2-vpn-spine-1.generalprogramming.org",
		"fd00:65::9",
		"10.255.2.1",
		"2602:fd37::1",
		"2001:470:1::5",
		"23.150.40.5",
		// The management interface's v4 is still an interface candidate
		// (Python takes both families per interface); it just sorts
		// behind the global ones.
		"10.65.0.9",
		"192.168.1.5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates =\n %q\nwant\n %q", got, want)
	}
}

func TestEndpointCandidatesDeduplicates(t *testing.T) {
	host := &model.Host{
		Hostname: "h",
		Address:  addrPtr("10.0.0.1/32"),
		Interfaces: []model.Interface{
			{Name: "eth0", Management: true, Addresses: []model.Address{addr("10.0.0.1/32")}},
			{Name: "eth1", Addresses: []model.Address{addr("10.0.0.1/32")}},
		},
	}
	got := EndpointCandidates(host, "example.net")
	want := []string{"h.example.net", "10.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %q, want %q", got, want)
	}
}

func TestManagementAddress(t *testing.T) {
	none := &model.Host{Hostname: "h", Interfaces: []model.Interface{{Name: "eth0", Addresses: []model.Address{addr("10.0.0.1/32")}}}}
	if got := ManagementAddress(none); got.IsValid() {
		t.Errorf("no management interface -> %v", got)
	}

	v4only := &model.Host{Hostname: "h", Interfaces: []model.Interface{
		{Name: "dum0", Management: true, Addresses: []model.Address{addr("10.9.9.9/32")}},
	}}
	if got := ManagementAddress(v4only); got.String() != "10.9.9.9" {
		t.Errorf("got %v", got)
	}
}

func TestProbeEndpointPicksFirstAnswering(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	_, portText, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portText)

	host := &model.Host{
		Hostname: "unresolvable-host-for-tests",
		// The FQDN candidate resolves nowhere (.invalid); 127.0.0.1 answers.
		Interfaces: []model.Interface{
			{Name: "eth0", Addresses: []model.Address{addr("127.0.0.1/32")}},
		},
	}
	got, err := ProbeEndpoint(context.Background(), host, port, "invalid", 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got != "127.0.0.1" {
		t.Errorf("endpoint = %q", got)
	}
}

func TestProbeEndpointNoneAnswer(t *testing.T) {
	host := &model.Host{
		Hostname:   "nothing",
		Interfaces: []model.Interface{{Name: "eth0", Addresses: []model.Address{addr("192.0.2.1/32")}}},
	}
	if _, err := ProbeEndpoint(context.Background(), host, 1, "invalid", 200*time.Millisecond); err == nil {
		t.Error("want error when nothing answers")
	}
}
