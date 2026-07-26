package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/lifecycle"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/sshx"
)

// deviceHarness adds the lifecycle seams to the shared command harness so
// no test here can reach a real device. One httptest server stands in for
// the whole fleet: the API base URL override routes each device to
// /<hostname>/<endpoint>, so the redundancy probe sees per-host answers.
type deviceHarness struct {
	*harness

	mu          sync.Mutex
	devices     map[string]*fakeDevice
	requests    []string // "<hostname>/<endpoint>"
	sshArgs     []string
	sshSteps    []string // "<hostname>:<script>", the ordering evidence
	scriptFails map[string]bool
	server      *httptest.Server
}

// fakeDevice is one device's canned API answers.
//
// The version deliberately does NOT advance on each read: the redundancy
// probe reads other devices' versions constantly, so a consumed sequence
// would make a probed device look updated. It changes exactly once, when
// the fake SSH session launches the detached reboot.
type fakeDevice struct {
	catchAll string // answers any show path with no specific field set
	version  string
	// afterReboot is what `show version` reports post-reboot.
	afterReboot string
	images      string
	bgp         string

	rebooted bool
	writes   []string
}

func (f *fakeDevice) answer(key string) (string, bool) {
	switch key {
	case "version":
		if f.rebooted && f.afterReboot != "" {
			return f.afterReboot, true
		}
		if f.version != "" {
			return f.version, true
		}
	case "system image":
		if f.images != "" {
			return f.images, true
		}
	case "bgp summary":
		if f.bgp != "" {
			return f.bgp, true
		}
	}
	if f.catchAll != "" {
		return f.catchAll, true
	}
	return "", false
}

func newDeviceHarness(t *testing.T) *deviceHarness {
	t.Helper()
	d := &deviceHarness{
		harness:     newHarness(t),
		devices:     map[string]*fakeDevice{},
		scriptFails: map[string]bool{},
	}

	d.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		// /<hostname>/<endpoint>
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		w.Header().Set("Content-Type", "application/json")
		if len(parts) != 2 {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "bad path"})
			return
		}
		hostname, endpoint := parts[0], parts[1]

		var payload struct {
			Op   string   `json:"op"`
			Path []string `json:"path"`
			Name string   `json:"name"`
		}
		_ = json.Unmarshal([]byte(r.PostFormValue("data")), &payload)

		d.mu.Lock()
		defer d.mu.Unlock()
		d.requests = append(d.requests, hostname+"/"+endpoint)

		device := d.devices[hostname]
		if device == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "no such device"})
			return
		}
		if endpoint != "show" {
			device.writes = append(device.writes, endpoint+" "+payload.Op+" "+payload.Name)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": "ok"})
			return
		}
		reply, ok := device.answer(strings.Join(payload.Path, " "))
		if !ok {
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "unsupported"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": reply})
	}))
	t.Cleanup(d.server.Close)

	// Every candidate address "answers" so probing succeeds; the real
	// requests go to the httptest server via the base URL override.
	for _, address := range []string{"10.0.0.1", "10.0.0.2", "sea1-vpn-0.example.invalid"} {
		d.reachable[address] = true
	}

	oldCreds, oldKey, oldExec := newSupertechCredentials, newVyOSAPIKey, execSSH
	oldBase, oldDial, oldTiming := apiBaseURLOverride, newSSHDialer, updateTiming
	t.Cleanup(func() {
		newSupertechCredentials, newVyOSAPIKey, execSSH = oldCreds, oldKey, oldExec
		apiBaseURLOverride, newSSHDialer, updateTiming = oldBase, oldDial, oldTiming
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
	apiBaseURLOverride = func(hostname string) string { return d.server.URL + "/" + hostname }

	// No test may open an SSH connection or really wait out a reboot.
	newSSHDialer = func(context.Context, *model.Host, *model.Network, sshx.CredentialSource) lifecycle.SSHDialer {
		return func(context.Context) (lifecycle.SSHSession, error) {
			return nil, errors.New("this test must not reach the SSH path")
		}
	}
	updateTiming.sleep = func(context.Context, time.Duration) error { return nil }
	updateTiming.wait = lifecycle.WaitOptions{InitialWait: time.Nanosecond, Interval: time.Nanosecond}

	return d
}

func (d *deviceHarness) device(hostname string) *fakeDevice {
	d.mu.Lock()
	defer d.mu.Unlock()
	f, ok := d.devices[hostname]
	if !ok {
		f = &fakeDevice{}
		d.devices[hostname] = f
	}
	return f
}

func (d *deviceHarness) recorded() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.requests...)
}

func (d *deviceHarness) writesTo(hostname string) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if f := d.devices[hostname]; f != nil {
		return append([]string(nil), f.writes...)
	}
	return nil
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

const (
	oldVersion = "2026.05.01-0100-rolling"
	newVersion = "2026.06.30-0048-rolling"
	imageURL   = "https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso"
	healthyBGP = "Neighbor  V  AS  Up/Down  State\n10.0.0.2  4  65000  01:00:00  3\n"
	adminDown  = "Neighbor  V  AS  Up/Down  State\n10.0.0.2  4  65000  never  Idle (Admin)\n"
)

// setShow answers every `show` path on sea1-vpn-0 with the same output.
func (d *deviceHarness) setShow(output string) {
	f := d.device("sea1-vpn-0")
	d.mu.Lock()
	defer d.mu.Unlock()
	f.catchAll = output
}

func (d *deviceHarness) endpointsHit() []string {
	var out []string
	for _, r := range d.recorded() {
		_, endpoint, _ := strings.Cut(r, "/")
		out = append(out, endpoint)
	}
	return out
}

func (d *deviceHarness) assertReadOnly(t *testing.T, what string) {
	t.Helper()
	for _, endpoint := range d.endpointsHit() {
		if endpoint != "show" {
			t.Fatalf("%s sent a %q request to a device", what, endpoint)
		}
	}
	if len(d.argv()) != 0 {
		t.Fatalf("%s opened an SSH session: %v", what, d.argv())
	}
}

func versionOutput(v string) string { return "Version:          VyOS " + v + "\n" }

// -- the multi-device fleet -------------------------------------------

// fleetNetwork is two leaves and two spines, all VyOS with an ASN, so the
// redundancy gate and the BGP drain both have something to decide on.
// Deliberately declared spine-first so the leaves-before-spines ordering is
// a real assertion and not an accident of input order.
func fleetNetwork() *model.Network {
	addr := func(s string) *model.Address {
		return &model.Address{IP: netip.MustParseAddr(s), Prefix: 32}
	}
	return &model.Network{
		Global: model.GlobalMeta{SearchDomain: "example.invalid"},
		Hosts: []model.Host{
			{Hostname: "sea1-spine-0", DeviceType: "vyos", Role: "spine", Site: "sea1", ASN: 65000, Address: addr("10.1.0.3")},
			{Hostname: "sea1-leaf-0", DeviceType: "vyos", Role: "leaf", Site: "sea1", ASN: 65000, Address: addr("10.1.0.1")},
			{Hostname: "sea1-spine-1", DeviceType: "vyos", Role: "spine", Site: "sea1", ASN: 65000, Address: addr("10.1.0.4")},
			{Hostname: "sea1-leaf-1", DeviceType: "vyos", Role: "leaf", Site: "sea1", ASN: 65000, Address: addr("10.1.0.2")},
		},
	}
}

// fleetHarness swaps in fleetNetwork and makes every member reachable and
// stale, so a whole update can be driven.
func fleetHarness(t *testing.T) *deviceHarness {
	t.Helper()
	d := newDeviceHarness(t)
	d.net = fleetNetwork()

	for _, h := range d.net.Hosts {
		d.reachable[h.Hostname+".example.invalid"] = true
		d.reachable[h.Address.IP.String()] = true

		f := d.device(h.Hostname)
		f.version = versionOutput(oldVersion)
		f.afterReboot = versionOutput(newVersion)
		f.images = "Name  Default boot  Running\n"
		f.bgp = healthyBGP
	}
	d.installFakeSSH()
	return d
}

// installFakeSSH swaps the dialer for sessions that only record scripts.
func (d *deviceHarness) installFakeSSH() {
	newSSHDialer = func(_ context.Context, host *model.Host, _ *model.Network, _ sshx.CredentialSource) lifecycle.SSHDialer {
		return func(context.Context) (lifecycle.SSHSession, error) {
			return &fakeSession{d: d, hostname: host.Hostname}, nil
		}
	}
}

type fakeSession struct {
	d        *deviceHarness
	hostname string
}

func (s *fakeSession) record(step string) {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	s.d.sshSteps = append(s.d.sshSteps, s.hostname+":"+step)
}

func (s *fakeSession) failing(step string) bool {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	return s.d.scriptFails[s.hostname+":"+step]
}

func (s *fakeSession) RunScript(_ context.Context, req sshx.ScriptRequest) (sshx.Result, bool, error) {
	s.record(req.Name)
	return sshx.Result{}, !s.failing(req.Name), nil
}

func (s *fakeSession) RunDetached(_ context.Context, name, _ string) (string, error) {
	s.record("detached:" + name)
	// The reboot is what moves the device onto the new image.
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if f := s.d.devices[s.hostname]; f != nil {
		f.rebooted = true
	}
	return "/tmp/" + name + ".log", nil
}

func (s *fakeSession) Run(context.Context, string, time.Duration) (sshx.Result, error) {
	return sshx.Result{}, nil
}

func (s *fakeSession) Close() error { return nil }

// rebootOrder is the hostnames whose reboot script was launched, in order.
func (d *deviceHarness) rebootOrder() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for _, step := range d.sshSteps {
		host, step, _ := strings.Cut(step, ":")
		if strings.HasPrefix(step, "detached:") {
			out = append(out, host)
		}
	}
	return out
}

// -- single-device behaviour ------------------------------------------

func TestDeviceUpdateDryRunChangesNothing(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(versionOutput(oldVersion))

	if err := d.run(t, "device", "update", "sea1-vpn-0", "--image-url", imageURL); err != nil {
		t.Fatalf("dry run: %v", err)
	}

	out := d.out.String()
	for _, want := range []string{
		"running version: " + oldVersion,
		"target version:  " + newVersion,
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
	d.assertReadOnly(t, "a dry run")
}

func TestDeviceUpdateDryRunReportsARedundancyRefusal(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(versionOutput(oldVersion))
	// sea1-vpn-0 is the only VyOS host here, so nothing else is alive and
	// the gate must refuse.
	if err := d.run(t, "device", "update", "sea1-vpn-0", "--image-url", imageURL); err != nil {
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
	d.setShow(versionOutput(oldVersion))

	err := d.run(t, "device", "update", "sea1-vpn-0", "--yes", "--image-url", imageURL)
	if err == nil {
		t.Fatal("--yes must not be able to reboot the last live device")
	}
	if !strings.Contains(err.Error(), "--yes does not override it") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("the refusal must name --force explicitly: %v", err)
	}
	d.assertReadOnly(t, "a refused update")
}

// A [y/N] prompt cannot authorise overriding a hard safety refusal.
func TestDeviceUpdateForceRequiresYes(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(versionOutput(oldVersion))

	o := d.options()
	o.SetInteractive(true)
	err := runDeviceUpdate(context.Background(), o, []string{"sea1-vpn-0"},
		updateFlags{force: true, imageURL: imageURL, in: strings.NewReader("y\n")})
	if err == nil || !strings.Contains(err.Error(), "--force requires --yes") {
		t.Fatalf("unexpected error: %v", err)
	}
	d.assertReadOnly(t, "--force without --yes")
}

func TestDeviceUpdateSkipsWhenAlreadyCurrent(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(versionOutput(newVersion))

	if err := d.run(t, "device", "update", "sea1-vpn-0", "--image-url", imageURL); err != nil {
		t.Fatalf("update: %v", err)
	}
	out := d.out.String()
	if !strings.Contains(out, "already running the latest image") {
		t.Fatalf("expected an already-current report:\n%s", out)
	}
	// An already-current device is not a dry-run candidate.
	if strings.Contains(out, "DRY RUN: nothing was changed") {
		t.Fatalf("an already-current device triggered the dry-run trailer:\n%s", out)
	}
}

func TestDeviceUpdateRejectsNonVyOS(t *testing.T) {
	d := newDeviceHarness(t)
	err := d.run(t, "device", "update", "fmt2-core")
	if err == nil || !strings.Contains(err.Error(), "only supports vyos") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// `all` must not fail on the EOS boxes; it must skip them and say so.
func TestDeviceUpdateSkipsNonVyOSInAWiderSelection(t *testing.T) {
	d := newDeviceHarness(t)
	d.setShow(versionOutput(newVersion))

	if err := d.run(t, "device", "update", "all", "--image-url", imageURL); err != nil {
		t.Fatalf("update all: %v", err)
	}
	out := d.out.String()
	if !strings.Contains(out, "skip fmt2-core") {
		t.Fatalf("the non-vyos host was not reported as skipped:\n%s", out)
	}
}

func TestDeviceUpdateFailsClosedWithoutAnImageSource(t *testing.T) {
	// No image source: fail before reading the device, never fall back to
	// "reboot it anyway".
	d := newDeviceHarness(t)
	d.setShow(versionOutput(oldVersion))

	if err := d.run(t, "device", "update", "sea1-vpn-0"); err == nil {
		t.Fatal("expected the update to fail without an image source")
	}
	d.assertReadOnly(t, "an update with no image source")
}

// -- the prompt matrix ------------------------------------------------

// On a terminal the prompt IS the confirmation; with no terminal --yes is
// required and nothing is ever asked.
func TestDeviceUpdatePromptMatrix(t *testing.T) {
	tests := []struct {
		name        string
		interactive bool
		yes         bool
		answer      string
		wantReboot  bool
		wantPrompt  bool
		wantDryRun  bool
	}{
		{name: "tty, no --yes, y", interactive: true, answer: "y\n", wantReboot: true, wantPrompt: true},
		{name: "tty, no --yes, yes", interactive: true, answer: "yes\n", wantReboot: true, wantPrompt: true},
		{name: "tty, no --yes, n", interactive: true, answer: "n\n", wantPrompt: true},
		{name: "tty, no --yes, empty", interactive: true, answer: "\n", wantPrompt: true},
		{name: "tty, no --yes, EOF", interactive: true, answer: "", wantPrompt: true},
		{name: "tty, --yes", interactive: true, yes: true, wantReboot: true},
		{name: "no tty, no --yes", wantDryRun: true},
		{name: "no tty, --yes", yes: true, wantReboot: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := fleetHarness(t)
			o := d.options()
			o.SetInteractive(tc.interactive)

			// One named leaf: the rest keep the fleet redundant, so the
			// gate passes and only the prompt decides.
			in := io.Reader(errReader{})
			if tc.interactive && !tc.yes {
				in = strings.NewReader(tc.answer)
			}
			err := runDeviceUpdate(context.Background(), o, []string{"sea1-leaf-0"},
				updateFlags{yes: tc.yes, imageURL: imageURL, in: in})
			if err != nil {
				t.Fatalf("update: %v", err)
			}

			out := d.out.String()
			if got := strings.Contains(out, "[y/N]"); got != tc.wantPrompt {
				t.Fatalf("prompted = %v, want %v:\n%s", got, tc.wantPrompt, out)
			}
			rebooted := len(d.rebootOrder()) > 0
			if rebooted != tc.wantReboot {
				t.Fatalf("rebooted = %v, want %v:\n%s", rebooted, tc.wantReboot, out)
			}
			if got := strings.Contains(out, "DRY RUN: nothing was changed"); got != tc.wantDryRun {
				t.Fatalf("dry-run trailer = %v, want %v:\n%s", got, tc.wantDryRun, out)
			}
			// A declined prompt is a skip, not a dry run: the operator
			// answered and must not be told to re-run with --yes.
			if tc.wantPrompt && !tc.wantReboot && !strings.Contains(out, "skipped sea1-leaf-0") {
				t.Fatalf("a declined device was not reported as skipped:\n%s", out)
			}
		})
	}
}

// -- the multi-device run ---------------------------------------------

// "all" updates one device at a time, leaves before spines.
func TestDeviceUpdateAllIsSequentialLeavesFirst(t *testing.T) {
	d := fleetHarness(t)
	o := d.options()
	o.SetInteractive(true)

	err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\ny\n")})
	if err != nil {
		t.Fatalf("update all: %v", err)
	}

	got := d.rebootOrder()
	want := []string{"sea1-leaf-0", "sea1-leaf-1", "sea1-spine-0", "sea1-spine-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("reboot order = %v, want %v\n%s", got, want, d.out.String())
	}

	// Each device's install+reboot completes before the next one starts.
	d.mu.Lock()
	steps := append([]string(nil), d.sshSteps...)
	d.mu.Unlock()
	seen := map[string]bool{}
	var current string
	for _, step := range steps {
		host, _, _ := strings.Cut(step, ":")
		if host != current {
			if seen[host] {
				t.Fatalf("device %s was interleaved with another: %v", host, steps)
			}
			seen[current] = true
			current = host
		}
	}

	out := d.out.String()
	for _, h := range want {
		if !strings.Contains(out, "=== "+h+" (") {
			t.Fatalf("no per-device header for %s:\n%s", h, out)
		}
	}
}

// The safety check cannot be hoisted: rebooting leaf-1 changes what is
// safe for the spine, so a check made once at the start is stale.
func TestDeviceUpdateRechecksRedundancyPerDevice(t *testing.T) {
	d := fleetHarness(t)

	var checks int32
	old := newFleetProbe
	t.Cleanup(func() { newFleetProbe = old })
	newFleetProbe = func(n *model.Network, key string) lifecycle.AliveProbe {
		atomic.AddInt32(&checks, 1)
		return old(n, key)
	}

	o := d.options()
	o.SetInteractive(true)
	if err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\ny\n")}); err != nil {
		t.Fatalf("update all: %v", err)
	}

	// One evaluation per device planned, not one for the whole run.
	if got := atomic.LoadInt32(&checks); got != 4 {
		t.Fatalf("the redundancy gate was evaluated %d times, want 4 (once per device)", got)
	}
	if got := strings.Count(d.out.String(), "redundancy check (other devices probed in parallel)"); got != 4 {
		t.Fatalf("the redundancy check was reported %d times, want 4", got)
	}
}

// A fleet whose last member did not come back is not one to keep rebooting.
func TestDeviceUpdateStopsOnTheFirstFailure(t *testing.T) {
	d := fleetHarness(t)
	d.scriptFails = map[string]bool{"sea1-leaf-1:barf-install.sh": true}

	o := d.options()
	o.SetInteractive(true)
	err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\ny\n")})
	if err == nil {
		t.Fatal("a failed install must fail the run")
	}
	if !strings.Contains(err.Error(), "sea1-leaf-1") {
		t.Fatalf("the error does not name the device that failed: %v", err)
	}

	if got := d.rebootOrder(); strings.Join(got, ",") != "sea1-leaf-0" {
		t.Fatalf("reboots = %v, want only sea1-leaf-0", got)
	}
	out := d.out.String()
	if !strings.Contains(out, "stopping: a failed update reduces redundancy") {
		t.Fatalf("the abort was not explained:\n%s", out)
	}
	if !strings.Contains(out, "not attempted") {
		t.Fatalf("the untouched devices were not reported:\n%s", out)
	}
	if strings.Contains(out, "=== sea1-spine-0") {
		t.Fatalf("the run continued past the failure:\n%s", out)
	}
}

// Back on the new image but BGP still admin-down: stop rather than reboot
// the next member of an already-degraded fleet.
func TestDeviceUpdateHaltsWhenRoutingDoesNotRecover(t *testing.T) {
	d := fleetHarness(t)
	d.device("sea1-leaf-0").bgp = adminDown

	o := d.options()
	o.SetInteractive(true)
	err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\ny\n")})
	if err == nil {
		t.Fatal("a device whose routing did not recover must stop the run")
	}
	if !strings.Contains(err.Error(), "routing did not recover") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := d.rebootOrder(); strings.Join(got, ",") != "sea1-leaf-0" {
		t.Fatalf("reboots = %v, want only sea1-leaf-0", got)
	}
}

func TestDeviceUpdateSkipsCurrentDevicesWithoutRebooting(t *testing.T) {
	d := fleetHarness(t)
	d.device("sea1-leaf-1").version = versionOutput(newVersion)

	o := d.options()
	o.SetInteractive(true)
	if err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\n")}); err != nil {
		t.Fatalf("update all: %v", err)
	}

	got := d.rebootOrder()
	want := []string{"sea1-leaf-0", "sea1-spine-0", "sea1-spine-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("reboot order = %v, want %v", got, want)
	}
	if !strings.Contains(d.out.String(), "already current") {
		t.Fatalf("the current device was not reported:\n%s", d.out.String())
	}
}

// "all" with --yes is allowed: the protection is the mechanism (one device
// at a time, redundancy re-checked before each, stop on first failure), not
// the spelling of the selection.
func TestDeviceUpdateAllRunsTheWholeFleet(t *testing.T) {
	d := fleetHarness(t)

	if err := d.run(t, "device", "update", "all", "--yes", "--image-url", imageURL); err != nil {
		t.Fatalf("all --yes must be allowed: %v", err)
	}
	if got := len(d.rebootOrder()); got == 0 {
		t.Fatal("all --yes rebooted nothing")
	}
	// It still names what it is about to reboot, so an unattended run is
	// legible in the log afterwards.
	out := d.out.String()
	if !strings.Contains(out, "unattended run over the whole fleet") {
		t.Fatalf("the fleet selection was not announced:\n%s", out)
	}
	if !strings.Contains(out, "sea1-leaf-0") {
		t.Fatalf("the announcement did not name the hosts:\n%s", out)
	}
}

// The announcement prints only for an unattended (--yes) selection of the
// whole fleet; naming hosts or confirming interactively already says so.
func TestFleetAnnouncementShape(t *testing.T) {
	fleet := fleetNetwork()
	many := []*model.Host{&fleet.Hosts[0], &fleet.Hosts[1]}
	one := many[:1]

	cases := []struct {
		name     string
		targets  []string
		hosts    []*model.Host
		yes      bool
		announce bool
	}{
		{"all with --yes", []string{"all"}, many, true, true},
		{"no targets with --yes", nil, many, true, true},
		{"all without --yes", []string{"all"}, many, false, false},
		{"named hosts with --yes", []string{"a", "b"}, many, true, false},
		{"all with --yes but one device", []string{"all"}, one, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			o := &Options{Out: &buf}
			announceFleetSelection(o, tc.targets, tc.hosts, tc.yes)
			got := strings.Contains(buf.String(), "unattended run over the whole fleet")
			if got != tc.announce {
				t.Fatalf("announced = %v, want %v (%q)", got, tc.announce, buf.String())
			}
		})
	}
}

// A non-TTY run with no --yes plans every device and changes none of them.
func TestDeviceUpdateMultiDeviceDryRun(t *testing.T) {
	d := fleetHarness(t)

	if err := d.run(t, "device", "update", "all", "--image-url", imageURL); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := d.rebootOrder(); len(got) != 0 {
		t.Fatalf("a dry run rebooted %v", got)
	}
	out := d.out.String()
	if got := strings.Count(out, "DRY RUN (nothing will be changed)"); got != 4 {
		t.Fatalf("planned %d devices, want 4:\n%s", got, out)
	}
	if !strings.Contains(out, "DRY RUN: nothing was changed") {
		t.Fatalf("dry-run trailer missing:\n%s", out)
	}
	if strings.Contains(out, "[y/N]") {
		t.Fatalf("a non-TTY run prompted:\n%s", out)
	}
	d.assertReadOnly(t, "a multi-device dry run")
}

// --force overrides the refusal loudly, per device; it does not disable the
// gate for the rest of the run.
func TestDeviceUpdateForcePerDevice(t *testing.T) {
	d := fleetHarness(t)
	// Nothing but leaf-0 answers, so every device is refused.
	d.reachable = map[string]bool{
		"sea1-leaf-0.example.invalid": true,
		"10.1.0.1":                    true,
	}

	if err := d.run(t, "device", "update", "sea1-leaf-0", "--yes", "--force",
		"--image-url", imageURL); err != nil {
		t.Fatalf("--force --yes must proceed: %v", err)
	}
	out := d.out.String()
	if !strings.Contains(out, "OVERRIDING") {
		t.Fatalf("the override was not announced loudly:\n%s", out)
	}
	if !strings.Contains(out, "redundancy gate FORCED") {
		t.Fatalf("the summary did not record the override:\n%s", out)
	}
	if got := d.rebootOrder(); strings.Join(got, ",") != "sea1-leaf-0" {
		t.Fatalf("reboots = %v", got)
	}
}

// ErrAlreadyExecuted holds per Updater, so a multi-device run must not
// share one.
func TestDeviceUpdateBuildsAFreshUpdaterPerDevice(t *testing.T) {
	d := fleetHarness(t)
	o := d.options()
	o.SetInteractive(true)

	if err := runDeviceUpdate(context.Background(), o, []string{"all"},
		updateFlags{imageURL: imageURL, in: strings.NewReader("y\ny\ny\ny\n")}); err != nil {
		t.Fatalf("a shared updater would have refused the second device: %v", err)
	}
	if got := len(d.rebootOrder()); got != 4 {
		t.Fatalf("rebooted %d devices, want 4", got)
	}
}

// -- device cleanup ---------------------------------------------------

const imageTable = "Name                     Default boot    Running\n" +
	"-----------------------  --------------  ---------\n" +
	"2026.06.30               yes             yes\n" +
	"2026.01.01               no              no\n"

func TestDeviceCleanupDryRunDeletesNothing(t *testing.T) {
	d := newDeviceHarness(t)
	d.device("sea1-vpn-0").images = imageTable

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
	for _, endpoint := range d.endpointsHit() {
		if endpoint != "show" {
			t.Fatalf("a dry run sent a %q request", endpoint)
		}
	}
}

func TestDeviceCleanupWithYesDeletes(t *testing.T) {
	d := newDeviceHarness(t)
	d.device("sea1-vpn-0").images = imageTable

	if err := d.run(t, "device", "cleanup", "sea1-vpn-0", "--yes"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !strings.Contains(d.out.String(), "deleting image 2026.01.01") {
		t.Fatalf("unexpected output:\n%s", d.out.String())
	}
	if got := d.writesTo("sea1-vpn-0"); len(got) != 1 || !strings.Contains(got[0], "image delete 2026.01.01") {
		t.Fatalf("--yes did not reach the image endpoint: %v", got)
	}
}

func TestDeviceCleanupSkipsNonVyOS(t *testing.T) {
	d := newDeviceHarness(t)
	if err := d.run(t, "device", "cleanup", "fmt2-core"); err == nil ||
		!strings.Contains(err.Error(), "cleanup support") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Regression: strings.Contains(v, "") is true, so an image source with no
// known target version once reported every device as already current and
// skipped upgrades silently.
func TestStaticImageUnknownVersionIsNotCurrent(t *testing.T) {
	unknown := staticImage{url: "https://example.invalid/i.iso"}
	for _, version := range []string{"2026.07.21-1151-rolling", "1.4.2", ""} {
		if unknown.IsCurrent(version) {
			t.Errorf("staticImage{version:\"\"}.IsCurrent(%q) = true", version)
		}
	}
}

func TestStaticImageIsCurrentUsesContainment(t *testing.T) {
	image := staticImage{version: "2026.07.21-1151-rolling"}
	if !image.IsCurrent("VyOS 2026.07.21-1151-rolling (build x)") {
		t.Error("containment match should report current")
	}
	if image.IsCurrent("2026.07.11-0033-rolling") {
		t.Error("older version should not report current")
	}
	if image.IsCurrent("") {
		t.Error("empty device version should not report current")
	}
}
