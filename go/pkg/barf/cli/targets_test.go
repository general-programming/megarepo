package cli

import (
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

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
		{"no targets means all", nil, []string{"sea1-vpn-0", "fmt2-core", "ext-peer"}, ""},
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
