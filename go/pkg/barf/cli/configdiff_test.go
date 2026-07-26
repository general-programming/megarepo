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

// TestRedactLineIsKeyAwareNotPositional pins the fix for a redactLine
// that blanked the LAST whitespace token of any line mentioning a secret
// keyword. That is exactly backwards for the two spellings real vendors
// use, and every case below leaked the secret before.
func TestRedactLineIsKeyAwareNotPositional(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			// Was: "snmp-server community genprogllc <redacted>".
			"ios snmp community is not the last token",
			"snmp-server community genprogllc ro",
			"snmp-server community " + redacted + " ro",
		},
		{
			// Was: `... private-key=KEY comment=<redacted>`.
			"mikrotik key=value one-liner",
			`/interface/wireguard add name=wg0 private-key=PRIVKEYVALUE comment="fabric"`,
			`/interface/wireguard add name=wg0 private-key=` + redacted + ` comment="fabric"`,
		},
		{
			"ios tacacs key",
			"tacacs-server key TACACSSECRET",
			"tacacs-server key " + redacted,
		},
		{
			"ios enable password",
			"enable password hunter2",
			"enable password " + redacted,
		},
		{
			"username line keeps its trailing privilege level",
			"username admin password hunter2 privilege 15",
			"username admin password " + redacted + " privilege 15",
		},
		{
			// Substring matching used to see "password" in here and
			// blank the (harmless) last token.
			"a keyword that merely contains a secret word is untouched",
			"set service ssh disable-password-authentication",
			"set service ssh disable-password-authentication",
		},
		{
			// The SSH *public* key must stay readable.
			"public keys stay visible",
			"set system login user supertech authentication public-keys erin key AAAAC3NzaC1",
			"set system login user supertech authentication public-keys erin key AAAAC3NzaC1",
		},
		{
			"vyos https api key is hidden",
			"set service https api keys id vaultadmin key 'FLEETAPIKEY'",
			"set service https api keys id vaultadmin key " + redacted,
		},
		{
			"wireguard private key",
			"set interfaces wireguard wg0 private-key 'PRIVKEY'",
			"set interfaces wireguard wg0 private-key " + redacted,
		},
		{
			"ipsec psk secret",
			"set vpn ipsec authentication psk peer0 secret 'PSKVALUE'",
			"set vpn ipsec authentication psk " + redacted + " secret " + redacted,
		},
		{
			"whitespace is preserved",
			"  enable  password   hunter2",
			"  enable  password   " + redacted,
		},
		{"no secret at all", "set system host-name sea1-vpn-0", "set system host-name sea1-vpn-0"},
		{"keyword with nothing after it", "password", "password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := redactLine(tc.in); got != tc.want {
				t.Errorf("redactLine(%q)\n got %q\nwant %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestVyOSRedactionIsPathShapeAware is the security regression for the
// fleet-wide VyOS API key. Before this, `barf diff` and the `deploy`
// dry-run op list both printed
//
//	set service https api keys id vaultadmin key 'FLEETAPIKEY'
//
// in cleartext, because vyosconfig.RedactPath only looks at the parent
// node name ("key") and `key` is not in its SecretNodes set. Adding it
// there is not a fix: the SSH public key has the same parent.
func TestVyOSRedactionIsPathShapeAware(t *testing.T) {
	rendered := strings.Join([]string{
		"set service https api keys id vaultadmin key 'FLEETAPIKEY'",
		"set service snmp community 'SNMPCOMMUNITY' authorization 'ro'",
		"set system login user supertech authentication public-keys erin key 'AAAAPUBKEY'",
		"set system login user supertech authentication plaintext-password 'ADMINPW'",
		"set interfaces wireguard wg0 private-key 'WGPRIVKEY'",
	}, "\n") + "\n"
	// A running config that shares nothing, so every rendered path is an
	// addition and every device path is a removal.
	running := "set system host-name stale\n"

	plan, err := planVyOS(rendered, running)
	if err != nil {
		t.Fatal(err)
	}

	secrets := []string{"FLEETAPIKEY", "SNMPCOMMUNITY", "ADMINPW", "WGPRIVKEY"}

	t.Run("diff text", func(t *testing.T) {
		text := plan.configDiff(DiffOptions{}).Text
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Errorf("%s leaked into `barf diff` output:\n%s", secret, text)
			}
		}
		if !strings.Contains(text, "AAAAPUBKEY") {
			t.Errorf("the SSH public key must stay visible:\n%s", text)
		}
		// The community is not the last component of its path; the
		// authorization level either side of it stays readable.
		if !strings.Contains(text, "authorization ro") {
			t.Errorf("redaction ate the wrong component:\n%s", text)
		}
	})

	t.Run("deploy dry-run ops", func(t *testing.T) {
		lines := strings.Join(describeOps(plan.ops(), DiffOptions{}), "\n")
		for _, secret := range secrets {
			if strings.Contains(lines, secret) {
				t.Errorf("%s leaked into the deploy op list:\n%s", secret, lines)
			}
		}
		if !strings.Contains(lines, "AAAAPUBKEY") {
			t.Errorf("the SSH public key must stay visible:\n%s", lines)
		}
	})

	t.Run("--show-secrets still shows them", func(t *testing.T) {
		text := plan.configDiff(DiffOptions{ShowSecrets: true}).Text
		for _, secret := range secrets {
			if !strings.Contains(text, secret) {
				t.Errorf("--show-secrets hid %s:\n%s", secret, text)
			}
		}
	})
}

func TestRedactVyOSPath(t *testing.T) {
	cases := []struct {
		name string
		path []string
		want string
	}{
		{
			"https api key",
			[]string{"service", "https", "api", "keys", "id", "vaultadmin", "key", "SECRET"},
			"service https api keys id vaultadmin key " + redacted,
		},
		{
			"snmp community with authorization",
			[]string{"service", "snmp", "community", "SECRET", "authorization", "ro"},
			"service snmp community " + redacted + " authorization ro",
		},
		{
			"snmp community alone",
			[]string{"service", "snmp", "community", "SECRET"},
			"service snmp community " + redacted,
		},
		{
			"ssh public key stays",
			[]string{"system", "login", "user", "supertech", "authentication",
				"public-keys", "erin", "key", "PUBKEY"},
			"system login user supertech authentication public-keys erin key PUBKEY",
		},
		{
			"node-name fallback still works",
			[]string{"interfaces", "wireguard", "wg0", "private-key", "SECRET"},
			"interfaces wireguard wg0 private-key " + redacted,
		},
		{
			"a bare `key` node elsewhere is left alone",
			[]string{"service", "https", "api", "keys", "id", "vaultadmin"},
			"service https api keys id vaultadmin",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Path.String() shlex-quotes, and "<redacted>" quotes; compare
			// on the unquoted components instead.
			got := strings.Join(redactVyOSPath(tc.path), " ")
			if got != tc.want {
				t.Errorf("redactVyOSPath(%v)\n got %q\nwant %q", tc.path, got, tc.want)
			}
		})
	}
}
