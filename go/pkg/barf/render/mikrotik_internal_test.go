package render

import (
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Regression: truthy() treated any non-empty, non-"false" string as true, so
// `advertise_dns: no` in the `ra:` passthrough map (decoded by yaml.v3 as the
// plain string "no", not bool false — yaml.v3 is YAML 1.2 and does not
// resolve `no`/`off` to bool the way PyYAML's YAML 1.1 loader does) rendered
// as advertise-dns=yes: the opposite of what network.yml said.
func TestTruthyMatchesPyYAMLBoolSemantics(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  bool
	}{
		{"nil", nil, false},
		{"bool false", false, false},
		{"bool true", true, true},
		{"string no", "no", false},
		{"string No", "No", false},
		{"string NO", "NO", false},
		{"string off", "off", false},
		{"string false", "false", false},
		{"string n", "n", false},
		{"string 0", "0", false},
		{"empty string", "", false},
		{"string yes", "yes", true},
		{"string Yes", "Yes", true},
		{"string on", "on", true},
		{"string true", "true", true},
		{"string y", "y", true},
		{"string 1", "1", true},
		{"unrecognised non-empty string is truthy (Python truthiness)", "maybe", true},
		{"nonzero int", 1, true},
		{"zero int", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truthy(tc.value); got != tc.want {
				t.Errorf("truthy(%#v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

// The end-to-end path a bug here actually breaks: a bridge interface's `ra:`
// block rendering the ND advertise-dns line.
func TestMikrotikBridgeRAAdvertiseDNSFalsyStringRendersNo(t *testing.T) {
	host := &model.Host{
		Hostname: "sea1-mikrotik-0",
		Role:     "vpn",
		Interfaces: []model.Interface{
			{
				Name: "bridge-internal",
				Type: "bridge",
				RA:   map[string]any{"advertise_dns": "no"},
			},
		},
	}
	network := &model.Network{Hosts: []model.Host{*host}}
	// mikrotikBridges never touches secrets; a nil SecretSource keeps this
	// test self-contained instead of borrowing render_test's external fake.
	ctx, err := newRenderCtx(&network.Hosts[0], network, nil)
	if err != nil {
		t.Fatal(err)
	}

	lines, err := mikrotikBridges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "advertise-dns=no") {
		t.Errorf("advertise_dns: no did not render advertise-dns=no:\n%s", joined)
	}
	if strings.Contains(joined, "advertise-dns=yes") {
		t.Errorf("advertise_dns: no rendered advertise-dns=yes:\n%s", joined)
	}
}
