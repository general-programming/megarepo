package scope

import (
	"context"
	"errors"
	"strings"
	"testing"

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
	// asked records the sections the comparer requested, so the test can
	// assert the full running config is never fetched.
	asked []string
}

func (f *fakeSections) RunningConfigSections(ctx context.Context, names ...string) (string, error) {
	f.asked = append(f.asked, names...)
	if f.err != nil {
		return "", f.err
	}
	return f.text, nil
}

// deviceHash is a sha512-crypt hash with a salt that is NOT the one barf
// derives — the adopted-device case, where the password is right but the
// hash text differs.
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

// inSyncConfig is shaped like real `show running-config all section ...`
// output: three-space indent, explicit default state, unmanaged users and
// several hundred lines of unrelated `... enable ...` config that the
// section filter drags in.
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

// The `username` section also lists accounts barf does not manage; none
// of them may be mistaken for the managed one.
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
// block says so out loud rather than by omission.
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

// An un-indented line ends the block; the `no shutdown` under a later
// interface must not be read as eAPI state.
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

// Regression: block termination asked `isSpace(raw[0])` against three
// ASCII bytes, where Python asks `line[0].isspace()` — a Unicode
// question. Any other whitespace indent (form feed, NBSP, a vertical tab
// from a pasted document) read as "un-indented", closed the
// `management api http-commands` block on its very first line, and made
// barf report the eAPI block absent on a device where it is present:
// spurious drift, and a deploy that rewrites correct config.
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

// Regression: strings.Split(_, "\n") is not Python's splitlines(). A
// running-config captured with CRLF endings left "\r" on every line, and
// a device answer containing a form feed was never split at all — either
// way the scoped comparison sees different lines than the Python
// implementation and reports drift that is not there.
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

	// A form feed is a line boundary in Python and was none in Go, so
	// everything after it used to vanish into one unmatched line.
	ff := "management api http-commands\f   no shutdown\funtouched"
	if got := ParseEOSManagedState(ff, ""); !isTrue(got.EAPIEnabled) {
		t.Errorf("EAPIEnabled = %v, want true across a form feed", got.EAPIEnabled)
	}
}

// The headline case: an adopted device whose hash carries a different
// salt but the same password is IN SYNC, so a deploy never needlessly
// rewrites its credentials.
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

// Only the managed sections are read; the megabyte running-config dump
// that produced the `+4 -32859` nonsense is never fetched.
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
	// Both the device line and the desired line appear, hashes redacted.
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

// A device that has never been adopted: no admin user, no enable
// password, eAPI block absent. Every item drifts and no `- ` line is
// emitted, because there is nothing on the device to show.
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

// A device with the right password but privilege dropped is drift: the
// hash check alone is not enough.
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

// The registry dispatch tests that used to live here (TestRegistryDispatch,
// TestCompareRejectsUnknownDeviceType) moved with the registry to
// ../vendor: TestComparerDispatch. They assert the same thing -- eos has
// a scoped comparer, the lookup is case-insensitive, and vyos/linux/
// mikrotik fall through to the generic whole-config diff.

// The zero value of Input must redact. The field used to be `Redact
// bool`, so an Input built without setting it printed crypt hashes in
// cleartext; the polarity was flipped to ShowSecrets so that forgetting
// the field fails safe.
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

// diff must refuse the same over-long key list generate refuses, rather
// than reporting drift against a list that could never be produced.
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
