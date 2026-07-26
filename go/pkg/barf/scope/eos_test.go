package scope

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/GehirnInc/crypt/sha512_crypt"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

const (
	testAdminPassword  = "fixture-admin-not-a-real-password"
	testEnablePassword = "fixture-enable-not-a-real-password"
	testSSHKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPrimaryKey erin@devbox"
	testSSHKey2        = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAISecondKey erin@laptop"
)

type fakeSecrets struct {
	admin  string
	enable string
	err    error
}

func (f fakeSecrets) HostSecret(hostname, key string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	switch key {
	case "admin-password":
		return f.admin, nil
	case "enable-password":
		return f.enable, nil
	}
	return "", errors.New("no such key: " + key)
}

func defaultSecrets() fakeSecrets {
	return fakeSecrets{admin: testAdminPassword, enable: testEnablePassword}
}

// fakeSections stands in for the read-only eAPI section fetch.
type fakeSections struct {
	text string
	err  error
	// Requested sections; tests assert the full config is never fetched.
	asked []string
}

func (f *fakeSections) RunningConfigSections(ctx context.Context, names ...string) (string, error) {
	f.asked = append(f.asked, names...)
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

// deviceHash uses a salt that is NOT the one barf derives — the
// adopted-device case: right password, different hash text.
func deviceHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := sha512_crypt.New().Generate([]byte(password), []byte("$6$handsetsalt00"))
	if err != nil {
		t.Fatalf("hashing: %v", err)
	}
	return hash
}

func testHost() *model.Host {
	return &model.Host{Hostname: "fmt2-cor-r-140752-1", DeviceType: "eos", EAPIVRF: "internal"}
}

func testNetwork(keys ...string) *model.Network {
	return &model.Network{Global: model.GlobalMeta{SSHKeys: keys}}
}

// inSyncConfig mimics real `show running-config all section ...` output:
// three-space indent, explicit defaults, unmanaged users, and unrelated
// `... enable ...` lines the section filter drags in.
func inSyncConfig(t *testing.T) string {
	t.Helper()
	return strings.Join([]string{
		"no username root ssh principal",
		"username admin privilege 15 role network-admin secret sha512 " + deviceHash(t, testAdminPassword),
		"username admin ssh-key " + testSSHKey,
		"username erin privilege 15 secret sha512 $6$other$hash",
		"username erin ssh-key ssh-rsa AAAAsomethingelse erin@elsewhere",
		"tacacs-server username max-length 255",
		"enable password sha512 " + deviceHash(t, testEnablePassword),
		"default snmp-server enable traps bgp",
		"aaa authentication enable default local",
		"interface Ethernet1",
		"   no ipv6 enable",
		"   default sflow enable",
		"management api http-commands",
		"   protocol https port 443",
		"   no protocol http port 80",
		"   no protocol unix-socket",
		"   protocol https ssl profile eapi",
		"   no shutdown",
		"   !",
		"   vrf internal",
		"      no shutdown",
		"no mpls oam downstream validation enabled",
	}, "\n")
}

func TestParseEOSManagedStateFromRealisticOutput(t *testing.T) {
	state := ParseEOSManagedState(inSyncConfig(t), "internal")

	if !strings.HasPrefix(state.AdminLine, "username admin privilege 15 role network-admin secret sha512 ") {
		t.Errorf("AdminLine = %q", redact(state.AdminLine))
	}
	if !strings.HasPrefix(state.AdminHash, "$6$") {
		t.Errorf("AdminHash not a crypt hash")
	}
	if state.SSHKey != testSSHKey {
		t.Errorf("SSHKey = %q, want the admin key", state.SSHKey)
	}
	if state.SSHKeySecondary != "" {
		t.Errorf("SSHKeySecondary = %q, want empty", state.SSHKeySecondary)
	}
	if !strings.HasPrefix(state.EnableHash, "$6$") {
		t.Errorf("EnableHash = %q", redact(state.EnableHash))
	}
	if !isTrue(state.EAPIEnabled) || !isTrue(state.EAPIHTTPS) || !isTrue(state.EAPIVRFEnabled) {
		t.Errorf("eAPI state = %v/%v/%v, want all true",
			state.EAPIEnabled, state.EAPIHTTPS, state.EAPIVRFEnabled)
	}
}

// Unmanaged accounts in the `username` section must not be mistaken for
// the managed one.
func TestParseEOSManagedStateIgnoresOtherUsers(t *testing.T) {
	text := strings.Join([]string{
		"username erin privilege 15 secret sha512 $6$erin$hash",
		"username erin ssh-key ssh-rsa AAAAerin erin@elsewhere",
	}, "\n")
	state := ParseEOSManagedState(text, "")
	if state.AdminLine != "" || state.AdminHash != "" || state.SSHKey != "" {
		t.Errorf("unmanaged user leaked into state: %+v", state)
	}
}

// `show running-config all` prints defaults literally, so a disabled eAPI
// block says so rather than being absent.
func TestParseEOSAPISectionShutdownAndVRF(t *testing.T) {
	text := strings.Join([]string{
		"management api http-commands",
		"   no protocol http port 80",
		"   shutdown",
		"   vrf mgmt",
		"      no shutdown",
		"   vrf internal",
		"      shutdown",
		"interface Ethernet1",
		"   no shutdown",
	}, "\n")
	state := ParseEOSManagedState(text, "internal")

	if state.EAPIEnabled == nil || *state.EAPIEnabled {
		t.Errorf("EAPIEnabled = %v, want false", state.EAPIEnabled)
	}
	if state.EAPIHTTPS != nil {
		t.Errorf("EAPIHTTPS = %v, want nil (no https line)", state.EAPIHTTPS)
	}
	if state.EAPIVRFEnabled == nil || *state.EAPIVRFEnabled {
		t.Errorf("EAPIVRFEnabled = %v, want false", state.EAPIVRFEnabled)
	}
}

// An un-indented line ends the block: a later interface's `no shutdown`
// is not eAPI state.
func TestParseEOSAPISectionEndsAtUnindentedLine(t *testing.T) {
	text := strings.Join([]string{
		"management api http-commands",
		"   shutdown",
		"interface Ethernet1",
		"   no shutdown",
	}, "\n")
	state := ParseEOSManagedState(text, "")
	if state.EAPIEnabled == nil || *state.EAPIEnabled {
		t.Errorf("EAPIEnabled = %v, want false", state.EAPIEnabled)
	}
}

// Regression: block termination tested three ASCII bytes where Python's
// `line[0].isspace()` is a Unicode question, so any other whitespace
// indent closed the block early and reported eAPI absent on a device
// that has it.
func TestParseEOSAPISectionAcceptsUnicodeIndents(t *testing.T) {
	indents := map[string]string{
		"space":              "   ",
		"tab":                "\t",
		"form feed":          "\f  ",
		"vertical tab":       "\v  ",
		"non-breaking space": "\u00a0  ",
		"en quad":            "\u2000  ",
		"ideographic space":  "\u3000  ",
		"file separator":     "\x1c  ",
	}
	for name, indent := range indents {
		t.Run(name, func(t *testing.T) {
			text := strings.Join([]string{
				"management api http-commands",
				indent + "protocol https port 443",
				indent + "no shutdown",
				"interface Ethernet1",
				"   no shutdown",
			}, "\n")
			state := ParseEOSManagedState(text, "")
			if !isTrue(state.EAPIEnabled) {
				t.Errorf("EAPIEnabled = %v, want true: the %s indent closed the block early",
					state.EAPIEnabled, name)
			}
			if !isTrue(state.EAPIHTTPS) {
				t.Errorf("EAPIHTTPS = %v, want true", state.EAPIHTTPS)
			}
		})
	}
}

// Regression: strings.Split(_, "\n") is not Python's splitlines() — CRLF
// left a stray "\r" per line and form feeds never split, both reporting
// drift that is not there.
func TestParseEOSManagedStateHandlesPythonLineBoundaries(t *testing.T) {
	crlf := strings.Join([]string{
		"management api http-commands",
		"   protocol https port 443",
		"   no shutdown",
		"username admin ssh-key ssh-ed25519 AAAAC3 someone@host",
	}, "\r\n")
	state := ParseEOSManagedState(crlf, "")
	if !isTrue(state.EAPIEnabled) {
		t.Errorf("EAPIEnabled = %v, want true across CRLF", state.EAPIEnabled)
	}
	if !isTrue(state.EAPIHTTPS) {
		t.Errorf("EAPIHTTPS = %v, want true across CRLF", state.EAPIHTTPS)
	}
	if state.SSHKey != "ssh-ed25519 AAAAC3 someone@host" {
		t.Errorf("SSHKey = %q; a stray \\r leaked into the parsed value", state.SSHKey)
	}

	// A form feed is a line boundary in Python but not in Go.
	ff := "management api http-commands\f   no shutdown\funtouched"
	if got := ParseEOSManagedState(ff, ""); !isTrue(got.EAPIEnabled) {
		t.Errorf("EAPIEnabled = %v, want true across a form feed", got.EAPIEnabled)
	}
}

// An adopted device whose hash carries a different salt but the same
// password is IN SYNC, so a deploy never rewrites its credentials.
func TestEOSCompareInSyncDespiteDifferentSalt(t *testing.T) {
	sections := &fakeSections{text: inSyncConfig(t)}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:        testHost(),
		Network:     testNetwork(testSSHKey),
		Secrets:     defaultSecrets(),
		Reader:      sections,
		ShowSecrets: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.HasChanges {
		t.Errorf("device reported drift:\n%s", result.Text)
	}
	if result.Summary != "no changes" {
		t.Errorf("Summary = %q", result.Summary)
	}
	if result.Text != "" {
		t.Errorf("Text = %q, want empty", result.Text)
	}
}

// Only the managed sections are read; the whole running-config dump that
// produced the `+4 -32859` nonsense is never fetched.
func TestEOSCompareReadsOnlyManagedSections(t *testing.T) {
	sections := &fakeSections{text: inSyncConfig(t)}
	if _, err := (EOS{}).Compare(context.Background(), Input{
		Host:    testHost(),
		Network: testNetwork(testSSHKey),
		Secrets: defaultSecrets(),
		Reader:  sections,
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"username", "enable", "management api http-commands"}
	if strings.Join(sections.asked, "|") != strings.Join(want, "|") {
		t.Errorf("sections read = %v, want %v", sections.asked, want)
	}
}

func TestEOSCompareWrongPasswordDrifts(t *testing.T) {
	sections := &fakeSections{text: inSyncConfig(t)}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:        testHost(),
		Network:     testNetwork(testSSHKey),
		Secrets:     fakeSecrets{admin: "a-different-password", enable: "another-one"},
		Reader:      sections,
		ShowSecrets: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drift) != 2 {
		t.Fatalf("Drift = %d items, want admin + enable:\n%s", len(result.Drift), result.Text)
	}
	if result.Summary != "2 managed item(s) drifted" {
		t.Errorf("Summary = %q", result.Summary)
	}
	if strings.Count(result.Text, "- ") != 2 || strings.Count(result.Text, "+ ") != 2 {
		t.Errorf("body is not `- device / + desired` shaped:\n%s", result.Text)
	}
	if strings.Contains(result.Text, "$6$") {
		t.Errorf("hash leaked into redacted output:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, RedactedHash) {
		t.Errorf("no redaction marker:\n%s", result.Text)
	}
}

func TestEOSCompareShowSecrets(t *testing.T) {
	sections := &fakeSections{text: inSyncConfig(t)}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:        testHost(),
		Network:     testNetwork(testSSHKey),
		Secrets:     fakeSecrets{admin: "wrong", enable: "wrong"},
		Reader:      sections,
		ShowSecrets: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Text, "$6$") {
		t.Errorf("--show-secrets still redacted:\n%s", result.Text)
	}
}

// Never-adopted device: every item drifts and no `- ` line is emitted,
// because there is nothing on the device to show.
func TestEOSCompareUnadoptedDevice(t *testing.T) {
	sections := &fakeSections{text: "username erin privilege 15 secret sha512 $6$e$h\n"}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:        testHost(),
		Network:     testNetwork(testSSHKey, testSSHKey2),
		Secrets:     defaultSecrets(),
		Reader:      sections,
		ShowSecrets: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	// admin, primary key, secondary key, enable, eAPI block, eAPI vrf.
	if len(result.Drift) != 6 {
		t.Fatalf("Drift = %d items, want 6:\n%s", len(result.Drift), result.Text)
	}
	if strings.Contains(result.Text, "\n- ") || strings.HasPrefix(result.Text, "- ") {
		t.Errorf("absent items must not print a device line:\n%s", result.Text)
	}
	if !strings.Contains(result.Text, "+ management api http-commands / vrf internal / no shutdown") {
		t.Errorf("eAPI VRF drift missing:\n%s", result.Text)
	}
}

func TestEOSCompareSSHKeyRotation(t *testing.T) {
	text := inSyncConfig(t)
	sections := &fakeSections{text: text}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:    testHost(),
		Network: testNetwork(testSSHKey2),
		Secrets: defaultSecrets(),
		Reader:  sections,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drift) != 1 {
		t.Fatalf("Drift = %d, want just the ssh-key:\n%s", len(result.Drift), result.Text)
	}
	if result.Drift[0].Device != "username admin ssh-key "+testSSHKey {
		t.Errorf("device line = %q", result.Drift[0].Device)
	}
	if result.Drift[0].Desired != "username admin ssh-key "+testSSHKey2 {
		t.Errorf("desired line = %q", result.Drift[0].Desired)
	}
}

// Right password but dropped privilege is drift: the hash check alone is
// not enough.
func TestEOSCompareRequiresPrivilege15(t *testing.T) {
	text := strings.Replace(inSyncConfig(t),
		"username admin privilege 15 role network-admin secret",
		"username admin privilege 1 role network-admin secret", 1)
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:    testHost(),
		Network: testNetwork(testSSHKey),
		Secrets: defaultSecrets(),
		Reader:  &fakeSections{text: text},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Drift) != 1 || !strings.Contains(result.Drift[0].Desired, "privilege 15") {
		t.Errorf("privilege drop not reported:\n%s", result.Text)
	}
}

func TestEOSHashMatches(t *testing.T) {
	hash := deviceHash(t, testAdminPassword)
	if !EOSHashMatches(testAdminPassword, hash) {
		t.Error("correct password did not verify")
	}
	if EOSHashMatches("nope", hash) {
		t.Error("wrong password verified")
	}
	// Non-crypt values (a plaintext or md5 secret) are never a match.
	for _, bad := range []string{"", "plaintextpassword", "$5$md5ish$hash", "$1$x$y"} {
		if EOSHashMatches(testAdminPassword, bad) {
			t.Errorf("hash %q counted as a match", bad)
		}
	}
}

// Regression: EOSHashMatches passed device-reported hashes straight to
// sha512_crypt.Verify with no bound, so a hostile or corrupted
// `$6$rounds=999999999$...` reported by a compromised/misbehaving device
// would burn minutes of CPU per probe. It must be rejected fast, as an
// unverifiable (never a match) hash, matching Python passlib's
// ValueError -> False.
func TestEOSHashMatchesRejectsAbsurdRoundsQuickly(t *testing.T) {
	// A well-shaped but out-of-bounds hash: valid `$6$rounds=N$salt$checksum`
	// shape so it clears the cheap prefix/arity checks, forcing the rounds
	// bound itself to be what stops it. One rounds count past passlib's own
	// max (999999999, sha512CryptMaxRounds) — not the max itself, which is a
	// legitimate, if slow, value the shape check must still accept (see
	// vyosconfig's TestVerifyCryptHashAcceptsPasslibRange, which asserts that
	// case without hashing for exactly this reason).
	checksum86 := strings.Repeat("a", 86)
	absurd := "$6$rounds=1000000000$saltstring$" + checksum86

	start := time.Now()
	if EOSHashMatches(testAdminPassword, absurd) {
		t.Error("an absurd-rounds hash reported a match")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("EOSHashMatches took %s; it must reject out-of-bounds rounds before hashing", elapsed)
	}
}

func TestEOSCompareReaderErrorPropagates(t *testing.T) {
	_, err := EOS{}.Compare(context.Background(), Input{
		Host:    testHost(),
		Network: testNetwork(testSSHKey),
		Secrets: defaultSecrets(),
		Reader:  &fakeSections{err: errors.New("unreachable")},
	})
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("err = %v", err)
	}
}

// The zero value of Input must redact: the field is ShowSecrets rather
// than Redact so that forgetting it fails safe.
func TestEOSCompareZeroValueInputRedacts(t *testing.T) {
	sections := &fakeSections{text: inSyncConfig(t)}
	result, err := EOS{}.Compare(context.Background(), Input{
		Host:    testHost(),
		Network: testNetwork(testSSHKey),
		Secrets: fakeSecrets{admin: "wrong", enable: "wrong"},
		Reader:  sections,
		// ShowSecrets deliberately not set.
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasChanges {
		t.Fatal("expected drift with the wrong secrets")
	}
	if strings.Contains(result.Text, "$6$") {
		t.Errorf("zero-value Input leaked a hash:\n%s", result.Text)
	}
}

// diff must refuse the same over-long key list generate refuses.
func TestEOSDriftRejectsOverLongSSHKeyList(t *testing.T) {
	network := testNetwork(testSSHKey, testSSHKey2, "ssh-rsa AAAAthird third@host")
	_, err := EOS{}.Compare(context.Background(), Input{
		Host:    testHost(),
		Network: network,
		Secrets: defaultSecrets(),
		Reader:  &fakeSections{text: inSyncConfig(t)},
	})
	if err == nil {
		t.Fatal("expected a refusal for three ssh keys")
	}
	if !strings.Contains(err.Error(), "one primary and one secondary") {
		t.Errorf("err = %v, want the EOS ssh-key limit error", err)
	}
}
