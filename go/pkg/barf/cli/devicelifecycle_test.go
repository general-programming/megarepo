package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/sshx"
)

// deviceHarness extends the shared command harness with the lifecycle
// seams: a fake VyOS API, fake credentials, and an ssh(1) that is never
// actually spawned. No test here can reach a real device.
type deviceHarness struct {
	*harness

	mu sync.Mutex
	// endpoints answered by the fake API, keyed by endpoint name.
	replies map[string]any
	// requests records every API request that reached "the device".
	requests []string
	// sshArgs records the ssh(1) command line, when one was run.
	sshArgs []string
	server  *httptest.Server
}

func newDeviceHarness(t *testing.T) *deviceHarness {
	t.Helper()
	d := &deviceHarness{harness: newHarness(t), replies: map[string]any{}}

	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		endpoint := strings.TrimPrefix(r.URL.Path, "/")

		var payload map[string]any
		_ = json.Unmarshal([]byte(r.PostFormValue("data")), &payload)

		d.mu.Lock()
		d.requests = append(d.requests, endpoint)
		reply, ok := d.replies[endpoint]
		d.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "unsupported"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": reply})
	}))
	t.Cleanup(d.server.Close)

	// Every candidate address "answers", so probing succeeds; the real
	// requests go to the httptest server via the base URL override
	// installed below.
	for _, address := range []string{"10.0.0.1", "10.0.0.2", "sea1-vpn-0.example.invalid"} {
		d.reachable[address] = true
	}

	oldCreds, oldKey, oldExec := newSupertechCredentials, newVyOSAPIKey, execSSH
	oldBase := apiBaseURLOverride
	t.Cleanup(func() {
		newSupertechCredentials, newVyOSAPIKey, execSSH = oldCreds, oldKey, oldExec
		apiBaseURLOverride = oldBase
	})

	newSupertechCredentials = func() (sshx.CredentialSource, error) {
		return sshx.StaticCredentials{Username: "supertech", Password: "never-printed"}, nil
	}
	newVyOSAPIKey = func() (string, error) { return "SECRET-API-KEY", nil }
	execSSH = func(_ context.Context, argv []string) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.sshArgs = argv
		return nil
	}
	apiBaseURLOverride = d.server.URL

	return d
}

func (d *deviceHarness) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requests...)
}

func (d *deviceHarness) argv() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.sshArgs...)
}

// -- device ssh -------------------------------------------------------

func TestDeviceSSHExecsTheSystemClient(t *testing.T) {
	d := newDeviceHarness(t)

	if err := d.run(t, "device", "ssh", "sea1-vpn-0"); err != nil {
		t.Fatalf("device ssh: %v", err)
	}
	argv := d.argv()
	want := []string{"ssh", "-l", "supertech", "sea1-vpn-0.example.invalid"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	if !strings.Contains(d.out.String(), "connecting as supertech@") {
		t.Fatalf("no connection line:\n%s", d.out.String())
	}
	// The shared account's password must never reach a command line.
	if strings.Contains(strings.Join(argv, " "), "never-printed") ||
		strings.Contains(d.out.String(), "never-printed") {
		t.Fatal("the SSH password leaked")
	}
}

func TestDeviceSSHRejectsAll(t *testing.T) {
	d := newDeviceHarness(t)
	if err := d.run(t, "device", "ssh", "all"); err == nil {
		t.Fatal(`"all" must be rejected`)
	}
}

func TestDeviceSSHFailsWhenNothingAnswers(t *testing.T) {
	d := newDeviceHarness(t)
	d.reachable = map[string]bool{}
	if err := d.run(t, "device", "ssh", "sea1-vpn-0"); err == nil ||
		!strings.Contains(err.Error(), "no reachable SSH address") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSSHUsernamePerVendor(t *testing.T) {
	cases := map[string]string{"eos": "admin", "linux": "root", "vyos": "", "mikrotik": ""}
	for deviceType, want := range cases {
		if got := sshUsername(deviceType); got != want {
			t.Fatalf("sshUsername(%q) = %q, want %q", deviceType, got, want)
		}
	}
}

func TestSSHArgv(t *testing.T) {
	if got := strings.Join(sshArgv("admin", "10.0.0.2"), " "); got != "ssh -l admin 10.0.0.2" {
		t.Fatalf("argv = %q", got)
	}
	if got := strings.Join(sshArgv("", "10.0.0.2"), " "); got != "ssh 10.0.0.2" {
		t.Fatalf("argv = %q", got)
	}
}

// -- device update ----------------------------------------------------

// showReply is the fake device's `show` output. The API client's single
// show endpoint serves version, system image and bgp summary, so the
// tests set whichever one the command under test asks for first.
func (d *deviceHarness) setShow(output string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.replies["show"] = output
}

func TestDeviceUpdateRejectsAll(t *testing.T) {
	d := newDeviceHarness(t)
	err := d.run(t, "device", "update", "all")
	if err == nil || !strings.Contains(err.Error(), "single hostname") {
		t.Fatalf(`"all" must be rejected for update, got %v`, err)
	}
}

func TestDeviceUpdateDryRunChangesNothing(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow("Version:          VyOS 2026.05.01-0100-rolling\n")

	err := d.run(t, "device", "update", "sea1-vpn-0",
		"--image-url", "https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}

	out := d.out.String()
	for _, want := range []string{
		"running version: 2026.05.01-0100-rolling",
		"target version:  2026.06.30-0048-rolling",
		"DRY RUN (nothing will be changed)",
		"redundancy check",
		"INSTALL image",
		"REBOOT the device",
		"DRY RUN: nothing was changed. Re-run with --yes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out)
		}
	}

	// Only reads may have reached the device, and no ssh at all.
	for _, endpoint := range d.recorded() {
		if endpoint != "show" {
			t.Fatalf("a dry run sent a %q request to the device", endpoint)
		}
	}
	if len(d.argv()) != 0 {
		t.Fatalf("a dry run opened an SSH session: %v", d.argv())
	}
}

func TestDeviceUpdateDryRunReportsARedundancyRefusal(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow("Version:          VyOS 2026.05.01-0100-rolling\n")
	// sea1-vpn-0 is the only VyOS host in the fake network, so nothing
	// else is alive and the gate must refuse.
	err := d.run(t, "device", "update", "sea1-vpn-0",
		"--image-url", "https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso")
	if err != nil {
		t.Fatalf("a dry run must not fail: %v", err)
	}
	out := d.out.String()
	if !strings.Contains(out, "RESULT:       REFUSED") {
		t.Fatalf("the refusal was not reported:\n%s", out)
	}
	if !strings.Contains(out, "--yes alone is NOT enough here") {
		t.Fatalf("the dry run did not explain that --yes is insufficient:\n%s", out)
	}
}

func TestDeviceUpdateYesAloneCannotOverrideTheRefusal(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow("Version:          VyOS 2026.05.01-0100-rolling\n")

	err := d.run(t, "device", "update", "sea1-vpn-0", "--yes",
		"--image-url", "https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso")
	if err == nil {
		t.Fatal("--yes must not be able to reboot the last live device")
	}
	if !strings.Contains(err.Error(), "--yes does not override it") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("the refusal must name --force explicitly: %v", err)
	}
	if len(d.argv()) != 0 {
		t.Fatal("a refused update still opened an SSH session")
	}
	for _, endpoint := range d.recorded() {
		if endpoint != "show" {
			t.Fatalf("a refused update sent a %q request", endpoint)
		}
	}
}

func TestDeviceUpdateSkipsWhenAlreadyCurrent(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow("Version:          VyOS 2026.06.30-0048-rolling\n")

	if err := d.run(t, "device", "update", "sea1-vpn-0",
		"--image-url", "https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !strings.Contains(d.out.String(), "already running the latest image") {
		t.Fatalf("expected an already-current report:\n%s", d.out.String())
	}
}

func TestDeviceUpdateRejectsNonVyOS(t *testing.T) {
	d := newDeviceHarness(t)
	err := d.run(t, "device", "update", "fmt2-core")
	if err == nil || !strings.Contains(err.Error(), "only supports vyos") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeviceUpdateFailsClosedWithoutAnImageSource(t *testing.T) {
	// No --image-url and no usable firmware mirror: the command must
	// fail before it has read anything from the device, not fall back to
	// "reboot it anyway".
	d := newDeviceHarness(t)
	d.setShow("Version:          VyOS 2026.05.01-0100-rolling\n")

	if err := d.run(t, "device", "update", "sea1-vpn-0"); err == nil {
		t.Fatal("expected the update to fail without an image source")
	}
	if len(d.argv()) != 0 {
		t.Fatal("an update with no image source still opened an SSH session")
	}
	for _, endpoint := range d.recorded() {
		if endpoint != "show" {
			t.Fatalf("an update with no image source sent a %q request", endpoint)
		}
	}
}

// -- device cleanup ---------------------------------------------------

const imageTable = "Name                     Default boot    Running\n" +
	"-----------------------  --------------  ---------\n" +
	"2026.06.30               yes             yes\n" +
	"2026.01.01               no              no\n"

func TestDeviceCleanupDryRunDeletesNothing(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(imageTable)

	if err := d.run(t, "device", "cleanup", "sea1-vpn-0"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	out := d.out.String()
	if !strings.Contains(out, "DRY RUN (nothing will be deleted") {
		t.Fatalf("no dry-run banner:\n%s", out)
	}
	if !strings.Contains(out, "would delete image 2026.01.01") {
		t.Fatalf("the candidate was not listed:\n%s", out)
	}
	if !strings.Contains(out, "keeping 2026.06.30 (running, default boot)") {
		t.Fatalf("the kept image was not explained:\n%s", out)
	}
	for _, endpoint := range d.recorded() {
		if endpoint != "show" {
			t.Fatalf("a dry run sent a %q request", endpoint)
		}
	}
}

func TestDeviceCleanupWithYesDeletes(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(imageTable)
	d.mu.Lock()
	d.replies["image"] = "deleted"
	d.mu.Unlock()

	if err := d.run(t, "device", "cleanup", "sea1-vpn-0", "--yes"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !strings.Contains(d.out.String(), "deleting image 2026.01.01") {
		t.Fatalf("unexpected output:\n%s", d.out.String())
	}
	found := false
	for _, endpoint := range d.recorded() {
		if endpoint == "image" {
			found = true
		}
	}
	if !found {
		t.Fatal("--yes did not reach the image endpoint")
	}
}

func TestDeviceCleanupSkipsNonVyOS(t *testing.T) {
	d := newDeviceHarness(t)
	if err := d.run(t, "device", "cleanup", "fmt2-core"); err == nil ||
		!strings.Contains(err.Error(), "cleanup support") {
		t.Fatalf("unexpected error: %v", err)
	}
}
