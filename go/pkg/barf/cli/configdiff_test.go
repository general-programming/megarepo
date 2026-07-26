package cli

import (
	"strings"
	"testing"
)

func TestDiffConfigsNoChanges(t *testing.T) {
	config := "set system host-name foo\nset system domain-name bar\n"
	d := DiffConfigs(config, config, DiffOptions{})
	if d.HasChanges {
		t.Error("identical configs reported as changed")
	}
	if d.Summary != "no changes" {
		t.Errorf("summary = %q, want %q", d.Summary, "no changes")
	}
	if d.Text != "" {
		t.Errorf("text = %q, want empty", d.Text)
	}
}

func TestDiffConfigsIgnoresOrderBlanksAndComments(t *testing.T) {
	rendered := "set b\n\nset a\n"
	running := "# a comment\nset a\n! another\nset b   \n"
	if d := DiffConfigs(rendered, running, DiffOptions{}); d.HasChanges {
		t.Errorf("spurious changes: %+v", d)
	}
}

func TestDiffConfigsAddedAndRemoved(t *testing.T) {
	d := DiffConfigs("set a\nset b\n", "set a\nset c\nset d\n", DiffOptions{})
	if !d.HasChanges {
		t.Fatal("expected changes")
	}
	if len(d.Added) != 1 || d.Added[0] != "set b" {
		t.Errorf("added = %v, want [set b]", d.Added)
	}
	if len(d.Removed) != 2 {
		t.Errorf("removed = %v, want 2 entries", d.Removed)
	}
	if d.Summary != "+1 -2" {
		t.Errorf("summary = %q, want %q", d.Summary, "+1 -2")
	}
}

func TestDiffSummaryOmitsRemovedWhenNone(t *testing.T) {
	d := DiffConfigs("set a\nset b\n", "set a\n", DiffOptions{})
	if d.Summary != "+1" {
		t.Errorf("summary = %q, want %q", d.Summary, "+1")
	}
}

func TestDiffTextHidesDeviceOnlyByDefault(t *testing.T) {
	d := DiffConfigs("set a\n", "set gone\n", DiffOptions{})
	if strings.Contains(d.Text, "set gone") {
		t.Errorf("device-only line leaked into default output:\n%s", d.Text)
	}

	d = DiffConfigs("set a\n", "set gone\n", DiffOptions{ShowDeviceOnly: true})
	if !strings.Contains(d.Text, "- set gone") {
		t.Errorf("device-only line missing with --show-device-only:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "+ set a") {
		t.Errorf("added line missing:\n%s", d.Text)
	}
	// Removals print before additions, matching format_diff.
	if strings.Index(d.Text, "- set gone") > strings.Index(d.Text, "+ set a") {
		t.Errorf("removals must print first:\n%s", d.Text)
	}
}

func TestDiffRedactsSecretsByDefault(t *testing.T) {
	rendered := "set interfaces wireguard wg0 private-key SUPERSECRETKEY\n"
	d := DiffConfigs(rendered, "", DiffOptions{})
	if strings.Contains(d.Text, "SUPERSECRETKEY") {
		t.Errorf("secret leaked into diff output:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, redacted) {
		t.Errorf("redaction placeholder missing:\n%s", d.Text)
	}
	// The Added set keeps the real line: only the printed body is redacted.
	if d.Added[0] != strings.TrimRight(rendered, "\n") {
		t.Errorf("added line was mutated: %q", d.Added[0])
	}

	d = DiffConfigs(rendered, "", DiffOptions{ShowSecrets: true})
	if !strings.Contains(d.Text, "SUPERSECRETKEY") {
		t.Errorf("--show-secrets did not show the value:\n%s", d.Text)
	}
}

func TestRedactLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{"set system login user admin password hunter2", "set system login user admin password " + redacted},
		{"set service snmp community genprogllc", "set service snmp community " + redacted},
		{"set system host-name sea1-vpn-0", "set system host-name sea1-vpn-0"},
		{"password", "password"},
	}
	for _, tc := range cases {
		if got := redactLine(tc.in); got != tc.want {
			t.Errorf("redactLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
