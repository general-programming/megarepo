package cli

import (
	"context"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// spyPrefetchSecrets is a SecretSource that also satisfies
// prefetch.Prefetcher, recording every call so prefetchLinkKeys' wiring can
// be checked without a real Vault.
type spyPrefetchSecrets struct {
	warmed [][]string
}

func (spyPrefetchSecrets) HostSecret(hostname, key string) (string, error) { return "", nil }

func (s *spyPrefetchSecrets) PrefetchHostSecrets(_ context.Context, paths ...string) {
	s.warmed = append(s.warmed, append([]string(nil), paths...))
}

// Regression: prefetch.LinkKeyPaths/LinkKeysFor and vault.Client.Prefetch
// had zero production callers, so a fleet render paid two serial cold Vault
// reads per WireGuard link instead of warming them concurrently.
func TestPrefetchLinkKeysWarmsWhenSecretsSupportIt(t *testing.T) {
	hosts := testHosts()
	links := []model.Link{{A: "sea1-vpn-0", B: "fmt2-core", Network: "172.31.255.0/31", Pinned: true, Port: 51000}}
	secrets := &spyPrefetchSecrets{}

	prefetchLinkKeys(context.Background(), secrets, allHosts(hosts), links)

	if len(secrets.warmed) != 1 || len(secrets.warmed[0]) == 0 {
		t.Fatalf("PrefetchHostSecrets not called with the link's key paths: %v", secrets.warmed)
	}
}

// A SecretSource that does not support prefetch.Prefetcher (test fakes; a
// backend with nothing to batch) must be silently skipped, not panic.
func TestPrefetchLinkKeysSkipsUnsupportedSecretSource(t *testing.T) {
	hosts := testHosts()
	links := []model.Link{{A: "sea1-vpn-0", B: "fmt2-core", Network: "172.31.255.0/31", Pinned: true, Port: 51000}}

	prefetchLinkKeys(context.Background(), fakeSecrets{}, allHosts(hosts), links)
	// No panic, nothing to assert: fakeSecrets has no PrefetchHostSecrets.
}

func testHosts() []model.Host {
	return []model.Host{
		{Hostname: "sea1-vpn-0", DeviceType: "vyos", Role: "vpn", Site: "sea1"},
		{Hostname: "fmt2-core", DeviceType: "eos", Role: "core", Site: "fmt2"},
		{Hostname: "ext-peer", DeviceType: "external", Role: "external", Site: "sea1"},
	}
}

func names(hosts []*model.Host) []string {
	out := make([]string, len(hosts))
	for i, h := range hosts {
		out[i] = h.Hostname
	}
	return out
}

func TestResolveTargets(t *testing.T) {
	hosts := testHosts()
	cases := []struct {
		name    string
		targets []string
		want    []string
		wantErr string
	}{
		{"no targets is an error", nil, nil, `no hosts given; pass one or more hostnames, or "all"`},
		{"no targets is an error (empty slice)", []string{}, nil, `no hosts given; pass one or more hostnames, or "all"`},
		{"all", []string{"all"}, []string{"sea1-vpn-0", "fmt2-core", "ext-peer"}, ""},
		{"named", []string{"fmt2-core"}, []string{"fmt2-core"}, ""},
		{"order preserved", []string{"ext-peer", "sea1-vpn-0"}, []string{"ext-peer", "sea1-vpn-0"}, ""},
		{"deduplicated", []string{"fmt2-core", "fmt2-core"}, []string{"fmt2-core"}, ""},
		{"all is exclusive", []string{"all", "fmt2-core"}, nil, `"all" cannot be combined with other targets`},
		{"unknown", []string{"nope"}, nil, `unknown device "nope"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveTargets(hosts, tc.targets)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotNames := names(got)
			if len(gotNames) != len(tc.want) {
				t.Fatalf("got %v, want %v", gotNames, tc.want)
			}
			for i := range gotNames {
				if gotNames[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", gotNames, tc.want)
				}
			}
		})
	}
}

func TestResolveTargetsReturnsLivePointers(t *testing.T) {
	hosts := testHosts()
	got, err := resolveTargets(hosts, []string{"fmt2-core"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != &hosts[1] {
		t.Error("resolveTargets copied the host instead of pointing at it")
	}
}

func TestFilterHosts(t *testing.T) {
	hosts := allHosts(testHosts())
	got := filterHosts(hosts, func(h *model.Host) bool { return h.DeviceType != "external" })
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 hosts", names(got))
	}
}
