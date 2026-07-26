package cli

import (
	"strings"
	"testing"
)

func TestPlanVyOSAcceptsBothRunningShapes(t *testing.T) {
	rendered := "set system host-name r1\n"

	tests := []struct {
		name    string
		running string
		wantErr bool
	}{
		{"retrieve json tree", `{"system": {"host-name": "r1"}}`, false},
		{"pretty-printed json", "{\n  \"system\": {\n    \"host-name\": \"r1\"\n  }\n}\n", false},
		{"flat set listing", "set system host-name r1\n", false},
		{"garbage is an error, not an empty config", "!!! nope !!!", true},
		{"empty is an error", "", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := planVyOS(rendered, tc.running)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got plan %+v", plan)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if plan.diff.HasChanges() {
				t.Fatalf("identical configs differ: %+v", plan.diff)
			}
		})
	}
}

// A value change is one removal plus one addition on the *path*, and
// quoting differences are not drift.
func TestPlanVyOSIsAPathSetDiff(t *testing.T) {
	// The rendered side quotes the description; the device does not.
	rendered := strings.Join([]string{
		"set system host-name r1",
		"set interfaces ethernet eth0 description 'uplink a'",
		"set system name-server 1.1.1.1",
	}, "\n")
	running := `{
	  "system": {"host-name": "r0", "name-server": ["1.1.1.1"]},
	  "interfaces": {"ethernet": {"eth0": {
	    "description": "uplink a", "hw-id": "aa:bb:cc:dd:ee:ff"}}}
	}`

	plan, err := planVyOS(rendered, running)
	if err != nil {
		t.Fatal(err)
	}
	d := plan.configDiff(DiffOptions{})

	if d.Summary != "+1 -1" {
		t.Fatalf("summary = %q, want +1 -1 (quoting and hw-id must not count)", d.Summary)
	}
	if !strings.Contains(d.Text, "- set system host-name r0") {
		t.Errorf("stale hostname not shown as a removal:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "+ set system host-name r1") {
		t.Errorf("new hostname not shown as an addition:\n%s", d.Text)
	}
	if strings.Contains(d.Text, "hw-id") {
		t.Errorf("ignored hw-id leaked into the diff:\n%s", d.Text)
	}
	if strings.Contains(d.Text, "description") {
		t.Errorf("quoting difference reported as drift:\n%s", d.Text)
	}
}

// Pins what a deploy would send.
func TestVyOSPlanOpsOrderAndCollapse(t *testing.T) {
	rendered := "set system host-name r1\n"
	running := `{
	  "system": {"host-name": "r1"},
	  "service": {"lldp": {"interface": {"eth0": {}, "eth1": {}}}},
	  "protocols": {"static": {"route": {"0.0.0.0/0": {"next-hop": {"10.0.0.1": {}}}}}}
	}`

	plan, err := planVyOS(rendered, running)
	if err != nil {
		t.Fatal(err)
	}
	ops := plan.ops()

	// Two whole stale subtrees, each collapsed to one delete; no sets.
	if len(ops) != 2 {
		t.Fatalf("ops = %+v, want 2 collapsed deletes", ops)
	}
	for _, op := range ops {
		if op.Verb != opDelete {
			t.Fatalf("unexpected op %+v", op)
		}
	}
	if !hasOp(ops, opDelete, "protocols") || !hasOp(ops, opDelete, "service") {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestVyOSPlanOpsEmptyWhenClean(t *testing.T) {
	plan, err := planVyOS("set system host-name r1\n", `{"system": {"host-name": "r1"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if ops := plan.ops(); len(ops) != 0 {
		t.Fatalf("clean plan produced ops: %+v", ops)
	}
}

// `barf diff` and `barf deploy` must agree, so both use the same planner.
func TestDiffUsesTheVyOSPathDiff(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	// A JSON running config: only the path-set diff can read this at all.
	h.readers["sea1-vpn-0"] = fakeReader{running: `{"system": {"host-name": "wrong"}}`}

	if err := h.run(t, "diff", "sea1-vpn-0"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "+ set system host-name sea1-vpn-0") {
		t.Errorf("addition missing:\n%s", out)
	}
	// Ownership is total: the stale value is a real deletion, without
	// needing --show-device-only.
	if !strings.Contains(out, "- set system host-name wrong") {
		t.Errorf("removal missing:\n%s", out)
	}
	if !strings.Contains(out, "+1 -1") {
		t.Errorf("summary missing:\n%s", out)
	}
}

func TestDescribeOpsRedacts(t *testing.T) {
	ops := []ConfigOp{
		{Verb: opSet, Path: []string{"interfaces", "wireguard", "wg1", "private-key", "SECRET"}},
		{Verb: opDelete, Path: []string{"system", "name-server", "8.8.8.8"}},
	}

	redacted := strings.Join(describeOps(ops, DiffOptions{}), "\n")
	if strings.Contains(redacted, "SECRET") {
		t.Errorf("secret leaked: %s", redacted)
	}
	if !strings.Contains(redacted, "delete system name-server 8.8.8.8") {
		t.Errorf("delete line wrong: %s", redacted)
	}

	shown := strings.Join(describeOps(ops, DiffOptions{ShowSecrets: true}), "\n")
	if !strings.Contains(shown, "SECRET") {
		t.Errorf("--show-secrets did not reveal: %s", shown)
	}
}
