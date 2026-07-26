package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeYAML drops a network.yml into a temp dir and returns its path.
func writeYAML(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "network.yml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const goodNetwork = `
global_meta:
  community_asn: 65000
  nameservers: [1.1.1.1]
  sites:
    sea1: {id: 1, coords: [47.6, -122.3]}
    fmt2: {id: 2, coords: [50.1, 8.6]}
hosts:
  sea1-vpn-0:
    type: vyos
    role: vpn
    site: sea1
    id: 1
    address: 10.0.0.1/32
    interfaces:
      - name: eth0
        vlan: 5
        address: 10.1.0.1/24
      - name: eth0
        vlan: 6
  fmt2-core:
    type: eos
    role: core
    site: fmt2
    id: 2
links:
  fmt2-core:
    sea1-vpn-0:
      network: 10.255.0.0/31
`

func TestValidateAcceptsAGoodFile(t *testing.T) {
	h := newHarness(t)
	path := writeYAML(t, goodNetwork)

	if err := h.run(t, "validate", path); err != nil {
		t.Fatalf("valid file rejected: %v\n%s", err, h.out.String())
	}
	if !strings.Contains(h.out.String(), "OK: ") ||
		!strings.Contains(h.out.String(), "(2 hosts, 1 links)") {
		t.Errorf("summary = %q", h.out.String())
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	path := writeYAML(t, `
global_meta:
  sites:
    sea1: {id: 1, coords: [47.6, -122.3]}
    dupe: {id: 1, coords: [1.0, 2.0]}
    noid: {coords: [1.0]}
hosts:
  good:
    type: vyos
    role: vpn
    site: sea1
    id: 1
  bad-type:
    type: junos
    role: vpn
  bad-site:
    type: vyos
    role: vpn
    site: nowhere
    id: 2
  no-role:
    type: vyos
    id: 3
  bad-address:
    type: vyos
    role: vpn
    address: not-an-ip
    id: 4
    interfaces:
      - name: eth0
        address: 10.0.0.300/24
links:
  good:
    ghost: 51100
  bad-site:
    good:
      port: 51100
`)

	report, err := validateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.OK {
		t.Fatal("a broken file validated clean")
	}

	joined := strings.Join(func() []string {
		var out []string
		for _, p := range report.Problems {
			out = append(out, p.String())
		}
		return out
	}(), "\n")

	for _, want := range []string{
		`id 1 is already used by site "sea1"`,
		"global_meta.sites.noid:6: no id",
		"needs exactly two coords",
		"community_asn is missing",
		`unsupported type "junos"`,
		`unknown site "nowhere"`,
		"missing required field 'role'",
		`address "not-an-ip" is not an IP address`,
		`interface eth0: "10.0.0.300/24" is not an IP address`,
		`"ghost" is not a host`,
		"port 51100 is already used by links.good.ghost",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing problem %q in:\n%s", want, joined)
		}
	}
}

func TestValidateMissingSections(t *testing.T) {
	report, err := validateFile(writeYAML(t, "{}\n"))
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, p := range report.Problems {
		joined += p.String() + "\n"
	}
	if !strings.Contains(joined, "missing 'global_meta' section") ||
		!strings.Contains(joined, "missing 'hosts' section") {
		t.Errorf("problems = %s", joined)
	}
}

func TestValidateDerivedPortCollision(t *testing.T) {
	// Two links between the same pair derive the same port; one of them
	// has to be pinned.
	report, err := validateFile(writeYAML(t, `
global_meta:
  community_asn: 65000
  sites:
    sea1: {id: 1, coords: [1.0, 2.0]}
hosts:
  a: {type: vyos, role: vpn, id: 1}
  b: {type: vyos, role: vpn, id: 2}
  c: {type: vyos, role: vpn}
links:
  a:
    b: {}
  b:
    a: {}
  c:
    a: {}
`))
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, p := range report.Problems {
		joined += p.String() + "\n"
	}
	// 51000 + 1*64 + 2
	if !strings.Contains(joined, "port 51066 is already used by links.a.b") {
		t.Errorf("derived-port collision not reported:\n%s", joined)
	}
	if !strings.Contains(joined, "c has no 'id'") {
		t.Errorf("missing-id link not reported:\n%s", joined)
	}
}

func TestValidateJSONAndExitStatus(t *testing.T) {
	h := newHarness(t)
	path := writeYAML(t, "hosts:\n  broken: {type: junos}\n")

	err := h.run(t, "validate", path, "--json")
	if err == nil {
		t.Fatal("a broken file must exit non-zero")
	}

	var report validateReport
	if decodeErr := json.Unmarshal(h.out.Bytes(), &report); decodeErr != nil {
		t.Fatalf("not JSON: %v\n%s", decodeErr, h.out.String())
	}
	if report.OK || len(report.Problems) == 0 || report.File != path {
		t.Errorf("report = %+v", report)
	}
}

func TestValidateUnreadableFile(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "validate", filepath.Join(t.TempDir(), "nope.yml")); err == nil {
		t.Fatal("a missing file must be an error")
	}
}
