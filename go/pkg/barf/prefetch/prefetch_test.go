package prefetch

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// fakePrefetcher records what it was asked to warm.
type fakePrefetcher struct {
	mu    sync.Mutex
	calls [][]string
}

func (f *fakePrefetcher) PrefetchHostSecrets(_ context.Context, paths ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, append([]string(nil), paths...))
}

func fabric() []model.Link {
	return []model.Link{
		// Derived link: pair-based key paths.
		{A: "fmt2-vpn-leaf-1", B: "fmt2-vpn-spine-1"},
		// Pinned link: legacy port-based key paths.
		{A: "fmt2-vpn-leaf-1", B: "sea1-vpn-spine-1", Port: 51831, Pinned: true},
		// IPsec: no WireGuard keypairs in Vault at all.
		{A: "fmt2-vpn-leaf-1", B: "ipsec-peer", IPsec: true},
		// Untouched by the targets below.
		{A: "sea1-vpn-leaf-9", B: "sea1-vpn-spine-9"},
	}
}

func TestLinkKeyPathsCoversBothSidesAndSkipsIPsec(t *testing.T) {
	got := LinkKeyPaths([]string{"fmt2-vpn-leaf-1"}, fabric())
	want := []string{
		"wg-51831-fmt2-vpn-leaf-1",
		"wg-51831-sea1-vpn-spine-1",
		"wglink-fmt2-vpn-leaf-1--fmt2-vpn-spine-1-fmt2-vpn-leaf-1",
		"wglink-fmt2-vpn-leaf-1--fmt2-vpn-spine-1-fmt2-vpn-spine-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LinkKeyPaths =\n%v\nwant\n%v", got, want)
	}
	for _, p := range got {
		if p == "" || p == "ipsec-peer" {
			t.Fatalf("an IPsec link leaked into %v", got)
		}
	}
}

func TestLinkKeyPathsDeduplicates(t *testing.T) {
	links := fabric()
	// Both endpoints selected: the shared link must still be warmed once.
	got := LinkKeyPaths([]string{"fmt2-vpn-leaf-1", "fmt2-vpn-spine-1"}, links)
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Fatalf("duplicate path %q in %v", p, got)
		}
		seen[p] = true
	}
}

func TestLinkKeyPathsEmptyInputs(t *testing.T) {
	if got := LinkKeyPaths(nil, fabric()); got != nil {
		t.Fatalf("no targets should warm nothing, got %v", got)
	}
	if got := LinkKeyPaths([]string{"x"}, nil); got != nil {
		t.Fatalf("no links should warm nothing, got %v", got)
	}
	if got := LinkKeyPaths([]string{"nobody"}, fabric()); len(got) != 0 {
		t.Fatalf("an unrelated target should warm nothing, got %v", got)
	}
}

func TestLinkKeysWarmsOnce(t *testing.T) {
	f := &fakePrefetcher{}
	LinkKeys(context.Background(), f, []string{"fmt2-vpn-leaf-1"}, fabric())

	if len(f.calls) != 1 {
		t.Fatalf("expected a single warm call, got %d", len(f.calls))
	}
	if len(f.calls[0]) != 4 {
		t.Fatalf("warmed %v", f.calls[0])
	}

	f2 := &fakePrefetcher{}
	LinkKeys(context.Background(), f2, []string{"nobody"}, fabric())
	if len(f2.calls) != 0 {
		t.Fatalf("expected no warm call, got %v", f2.calls)
	}

	// A nil prefetcher (a secret source with no cache) is a no-op.
	LinkKeys(context.Background(), nil, []string{"fmt2-vpn-leaf-1"}, fabric())
}

func TestLinkKeysForHosts(t *testing.T) {
	f := &fakePrefetcher{}
	hosts := []*model.Host{{Hostname: "fmt2-vpn-leaf-1"}, nil}
	LinkKeysFor(context.Background(), f, hosts, fabric())

	if len(f.calls) != 1 || len(f.calls[0]) != 4 {
		t.Fatalf("calls = %v", f.calls)
	}
	if got := Hostnames(hosts); !reflect.DeepEqual(got, []string{"fmt2-vpn-leaf-1"}) {
		t.Fatalf("Hostnames = %v", got)
	}
}
