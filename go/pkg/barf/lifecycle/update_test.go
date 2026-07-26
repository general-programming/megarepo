package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/sshx"
)

// -- fakes ------------------------------------------------------------

// fakeAPI records every call; writes holds anything that would change a device.
type fakeAPI struct {
	mu       sync.Mutex
	versions []string // consumed in order; the last one repeats
	images   []SystemImage
	warning  string
	writes   []string
	versErr  error
}

func (f *fakeAPI) Version(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.versErr != nil {
		return "", f.versErr
	}
	v := f.versions[0]
	if len(f.versions) > 1 {
		f.versions = f.versions[1:]
	}
	return v, nil
}

func (f *fakeAPI) SystemImages(context.Context) ([]SystemImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.images, nil
}

func (f *fakeAPI) DeleteImage(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, "delete-image "+name)
	return nil
}

func (f *fakeAPI) VerifyRouting(context.Context, bool) string { return f.warning }

func (f *fakeAPI) recordedWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

// fakeSSH records the scripts it was asked to run.
type fakeSSH struct {
	mu sync.Mutex
	// scriptOK decides whether each marker's script passes.
	scriptOK map[string]bool
	ran      []string
	closed   int
	logLine  string
}

func (f *fakeSSH) RunScript(_ context.Context, req sshx.ScriptRequest) (sshx.Result, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, req.Name)
	ok, set := f.scriptOK[req.Marker]
	if !set {
		ok = true
	}
	// VyOS returns rc 0 even when a stage failed, so the updater must trust
	// the marker verdict rather than the exit status.
	return sshx.Result{ExitStatus: 0}, ok, nil
}

func (f *fakeSSH) RunDetached(_ context.Context, name, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ran = append(f.ran, "detached:"+name)
	return "/tmp/" + name + ".log", nil
}

func (f *fakeSSH) Run(context.Context, string, time.Duration) (sshx.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sshx.Result{Output: f.logLine}, nil
}

func (f *fakeSSH) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeSSH) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

type fakeProvider struct {
	latest    string
	published string
	mu        sync.Mutex
	downloads int
}

func (p *fakeProvider) LatestVersion(context.Context) (string, error) { return p.latest, nil }
func (p *fakeProvider) IsCurrent(version string) bool {
	return version != "" && strings.Contains(version, p.latest)
}

func (p *fakeProvider) Download(context.Context) (string, int64, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads++
	return "/tmp/image.iso", 900 << 20, nil
}

func (p *fakeProvider) DownloadSignature(context.Context) (string, error) { return "", nil }

func (p *fakeProvider) Publish(context.Context, string, string) (string, error) {
	return "https://mirror.invalid/vyos-" + p.latest + "-generic-amd64.iso", nil
}

func (p *fakeProvider) downloadCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.downloads
}

// newUpdater: target one release behind, redundant fleet, everything succeeds.
func newUpdater(t *testing.T, opts Options) (*Updater, *fakeAPI, *fakeSSH, *fakeProvider) {
	t.Helper()

	// Nothing in this suite ever really waits.
	old := sleep
	sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { sleep = old })

	api := &fakeAPI{
		versions: []string{"2026.05.01-0100-rolling", "2026.06.30-0048-rolling"},
		images:   []SystemImage{{Name: "2026.05.01-0100-rolling", DefaultBoot: true, Running: true}},
	}
	ssh := &fakeSSH{scriptOK: map[string]bool{}}
	provider := &fakeProvider{latest: "2026.06.30-0048-rolling"}

	target := host("sea1-leaf-0")
	fleet := []*model.Host{target, host("sea1-leaf-1"), host("sea1-spine-0")}

	updater := &Updater{
		Host:     target,
		Fleet:    fleet,
		API:      api,
		Provider: provider,
		Mirror:   provider,
		Probe:    aliveSet("sea1-leaf-1", "sea1-spine-0"),
		SSH:      func(context.Context) (SSHSession, error) { return ssh, nil },
		Wait:     WaitOptions{InitialWait: time.Nanosecond, Interval: time.Nanosecond},
		Opts:     opts,
	}
	return updater, api, ssh, provider
}

// -- plan -------------------------------------------------------------

func TestBuildPlanReadsOnly(t *testing.T) {
	updater, api, ssh, provider := newUpdater(t, Options{})

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.CurrentVersion != "2026.05.01-0100-rolling" || plan.TargetVersion != "2026.06.30-0048-rolling" {
		t.Fatalf("unexpected plan versions: %+v", plan)
	}
	if !plan.InstallNeeded || plan.StaleImage {
		t.Fatalf("unexpected install state: %+v", plan)
	}
	if !plan.Safe() {
		t.Fatalf("expected the redundancy gate to pass: %v", plan.RedundancyErr)
	}
	if !plan.DrainBGP {
		t.Fatal("a host with an ASN must drain BGP")
	}

	// Nothing may have touched the device or the network.
	if got := api.recordedWrites(); len(got) != 0 {
		t.Fatalf("planning wrote to the device: %v", got)
	}
	if got := ssh.recorded(); len(got) != 0 {
		t.Fatalf("planning opened an SSH session: %v", got)
	}
	if provider.downloadCount() != 0 {
		t.Fatal("planning downloaded the image")
	}

	joined := strings.Join(plan.Steps, "\n")
	for _, want := range []string{"INSTALL image", "SHUT DOWN BGP", "REBOOT the device"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("plan is missing %q:\n%s", want, joined)
		}
	}
}

func TestBuildPlanDetectsAlreadyCurrent(t *testing.T) {
	updater, _, _, _ := newUpdater(t, Options{})
	updater.API.(*fakeAPI).versions = []string{"2026.06.30-0048-rolling"}

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if !plan.AlreadyCurrent {
		t.Fatal("expected AlreadyCurrent")
	}
}

func TestBuildPlanDetectsAStaleImage(t *testing.T) {
	// On disk but not default boot = interrupted install; it must be deleted
	// and reinstalled or the reboot lands on the old image.
	updater, _, _, _ := newUpdater(t, Options{})
	updater.API.(*fakeAPI).images = []SystemImage{
		{Name: "2026.05.01-0100-rolling", DefaultBoot: true, Running: true},
		{Name: "2026.06.30-0048-rolling"},
	}

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if !plan.InstallNeeded || !plan.StaleImage {
		t.Fatalf("expected a stale-image reinstall: %+v", plan)
	}
	if !strings.Contains(strings.Join(plan.Steps, "\n"), "DELETE the stale") {
		t.Fatalf("plan does not mention the delete: %v", plan.Steps)
	}
}

func TestBuildPlanSkipsInstallWhenAlreadyStaged(t *testing.T) {
	updater, _, _, _ := newUpdater(t, Options{})
	updater.API.(*fakeAPI).images = []SystemImage{
		{Name: "2026.05.01-0100-rolling", Running: true},
		{Name: "2026.06.30-0048-rolling", DefaultBoot: true},
	}

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.InstallNeeded {
		t.Fatal("an already-default-boot image needs no install")
	}
}

func TestBuildPlanRecordsARedundancyRefusalInsteadOfRaising(t *testing.T) {
	// A dry run must be able to print the refusal, so BuildPlan records it.
	updater, _, _, _ := newUpdater(t, Options{})
	updater.Probe = aliveSet() // nothing else is alive

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Safe() {
		t.Fatal("expected the gate to refuse")
	}
}

func TestBuildPlanFailsWhenTheDeviceIsUnreachable(t *testing.T) {
	updater, api, _, _ := newUpdater(t, Options{})
	api.versErr = errors.New("connection refused")

	_, err := updater.BuildPlan(context.Background())
	var unreachable *DeviceUnreachableError
	if !errors.As(err, &unreachable) {
		t.Fatalf("expected DeviceUnreachableError, got %v", err)
	}
}

// -- the gates --------------------------------------------------------

func TestExecuteRefusesWithoutAllowWrites(t *testing.T) {
	updater, api, ssh, provider := newUpdater(t, Options{})

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := updater.Execute(context.Background(), plan); !errors.Is(err, ErrWritesNotAllowed) {
		t.Fatalf("expected ErrWritesNotAllowed, got %v", err)
	}
	if len(api.recordedWrites()) != 0 || len(ssh.recorded()) != 0 || provider.downloadCount() != 0 {
		t.Fatal("a dry run touched something")
	}
}

func TestAllowWritesAloneCannotOverrideARedundancyRefusal(t *testing.T) {
	// --yes is not enough to reboot the last live spine.
	updater, api, ssh, provider := newUpdater(t, Options{AllowWrites: true})
	updater.Probe = aliveSet()

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	_, err = updater.Execute(context.Background(), plan)
	var redundancy *RedundancyError
	if !errors.As(err, &redundancy) {
		t.Fatalf("expected a RedundancyError, got %v", err)
	}
	if len(api.recordedWrites()) != 0 || len(ssh.recorded()) != 0 || provider.downloadCount() != 0 {
		t.Fatal("a refused update still touched the device")
	}
}

func TestForceUnsafeOverridesTheRefusalLoudly(t *testing.T) {
	var out bytes.Buffer
	updater, _, ssh, _ := newUpdater(t, Options{AllowWrites: true, ForceUnsafe: true, Out: &out})
	updater.Probe = aliveSet()

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := updater.Execute(context.Background(), plan); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "OVERRIDING THE REDUNDANCY GATE") {
		t.Fatalf("the override was not announced:\n%s", out.String())
	}
	if !strings.Contains(strings.Join(ssh.recorded(), " "), "detached:barf-reboot.sh") {
		t.Fatalf("the forced update did not reboot: %v", ssh.recorded())
	}
}

func TestOnlyOneRebootPerUpdater(t *testing.T) {
	updater, _, _, _ := newUpdater(t, Options{AllowWrites: true})

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := updater.Execute(context.Background(), plan); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if _, err := updater.Execute(context.Background(), plan); !errors.Is(err, ErrAlreadyExecuted) {
		t.Fatalf("expected ErrAlreadyExecuted, got %v", err)
	}
}

// -- the happy path ---------------------------------------------------

func TestExecuteRunsTheStagesInOrder(t *testing.T) {
	var out bytes.Buffer
	updater, api, ssh, provider := newUpdater(t, Options{AllowWrites: true, Out: &out})

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	result, err := updater.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result != "updated to 2026.06.30-0048-rolling" {
		t.Fatalf("result = %q", result)
	}

	want := []string{"barf-precheck.sh", "barf-install.sh", "detached:barf-reboot.sh"}
	got := ssh.recorded()
	if len(got) != len(want) {
		t.Fatalf("stages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("stages = %v, want %v", got, want)
		}
	}
	if len(api.recordedWrites()) != 0 {
		t.Fatalf("no image should have been deleted: %v", api.recordedWrites())
	}
	if provider.downloadCount() != 1 {
		t.Fatalf("image downloaded %d times", provider.downloadCount())
	}
}

func TestExecuteDeletesTheStaleImageOnlyAfterThePrecheck(t *testing.T) {
	updater, api, ssh, _ := newUpdater(t, Options{AllowWrites: true})
	api.images = []SystemImage{
		{Name: "2026.05.01-0100-rolling", DefaultBoot: true, Running: true},
		{Name: "2026.06.30-0048-rolling"},
	}
	// A failed precheck must leave the device exactly as it was.
	ssh.scriptOK[sshx.MarkerPrecheck] = false

	plan, err := updater.BuildPlan(context.Background())
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := updater.Execute(context.Background(), plan); err == nil {
		t.Fatal("expected the update to abort on a failed precheck")
	}
	if got := api.recordedWrites(); len(got) != 0 {
		t.Fatalf("a failed precheck still deleted an image: %v", got)
	}
	if strings.Contains(strings.Join(ssh.recorded(), " "), "detached") {
		t.Fatal("a failed precheck still rebooted the device")
	}
}

func TestFailedPrecheckNeverReboots(t *testing.T) {
	updater, _, ssh, _ := newUpdater(t, Options{AllowWrites: true})
	ssh.scriptOK[sshx.MarkerPrecheck] = false

	plan, _ := updater.BuildPlan(context.Background())
	_, err := updater.Execute(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "pre-checks failed") {
		t.Fatalf("expected a precheck failure, got %v", err)
	}
}

func TestFailedInstallNeverReboots(t *testing.T) {
	updater, _, ssh, _ := newUpdater(t, Options{AllowWrites: true})
	ssh.scriptOK[sshx.MarkerInstall] = false

	plan, _ := updater.BuildPlan(context.Background())
	_, err := updater.Execute(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "image install failed") {
		t.Fatalf("expected an install failure, got %v", err)
	}
	if strings.Contains(strings.Join(ssh.recorded(), " "), "detached") {
		t.Fatal("a failed install still rebooted the device")
	}
}

func TestDetachedFailureMarkerAborts(t *testing.T) {
	updater, _, ssh, _ := newUpdater(t, Options{AllowWrites: true})
	// Still reachable after the drain wait plus a FAIL line: no reboot coming.
	ssh.logLine = "DRAIN-FAIL: commit failed (another config session open?)\n"

	plan, _ := updater.BuildPlan(context.Background())
	_, err := updater.Execute(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "drain/reboot failed") {
		t.Fatalf("expected a drain failure, got %v", err)
	}
}

func TestExecuteWarnsWhenBGPStaysAdminDown(t *testing.T) {
	var out bytes.Buffer
	updater, api, _, _ := newUpdater(t, Options{AllowWrites: true, Out: &out})
	api.warning = "BGP still administratively down after reboot; the shutdown may have been saved"

	plan, _ := updater.BuildPlan(context.Background())
	if _, err := updater.Execute(context.Background(), plan); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "administratively down") {
		t.Fatalf("the routing warning was swallowed:\n%s", out.String())
	}
}

// With more devices to reboot, "BGP is still admin-down" must not be advisory.
func TestRequireRoutingMakesTheWarningFatal(t *testing.T) {
	var out bytes.Buffer
	updater, api, _, _ := newUpdater(t, Options{AllowWrites: true, RequireRouting: true, Out: &out})
	api.warning = "BGP still administratively down after reboot; the shutdown may have been saved"

	plan, _ := updater.BuildPlan(context.Background())
	result, err := updater.Execute(context.Background(), plan)

	var notRecovered *RoutingNotRecoveredError
	if !errors.As(err, &notRecovered) {
		t.Fatalf("expected a RoutingNotRecoveredError, got %v", err)
	}
	if notRecovered.Hostname != "sea1-leaf-0" {
		t.Fatalf("the error does not name the device: %+v", notRecovered)
	}
	// The device did update; the report must still say so.
	if !strings.Contains(result, "updated to 2026.06.30-0048-rolling") {
		t.Fatalf("result = %q, want it to still say the device updated", result)
	}
	if !strings.Contains(out.String(), "administratively down") {
		t.Fatalf("the warning was not printed:\n%s", out.String())
	}
}

// Single-device behaviour: still just a warning, as in the Python original.
func TestRequireRoutingIsOffByDefault(t *testing.T) {
	updater, api, _, _ := newUpdater(t, Options{AllowWrites: true})
	api.warning = "BGP still administratively down after reboot"

	plan, _ := updater.BuildPlan(context.Background())
	if _, err := updater.Execute(context.Background(), plan); err != nil {
		t.Fatalf("without RequireRouting the warning must not fail the update: %v", err)
	}
}

// The drain wait goes through Updater.Sleep when one is set.
func TestInjectedSleepReplacesTheDrainWait(t *testing.T) {
	updater, _, _, _ := newUpdater(t, Options{AllowWrites: true})
	var waited []time.Duration
	updater.DrainWait = 90 * time.Second
	updater.Sleep = func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)
		return nil
	}

	plan, _ := updater.BuildPlan(context.Background())
	if _, err := updater.Execute(context.Background(), plan); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(waited) != 1 || waited[0] != 95*time.Second {
		t.Fatalf("drain waits = %v, want one of 95s (drain + grace)", waited)
	}
}

// Regression: int(900*time.Millisecond.Seconds()) truncates to 0, which the
// reboot script reads as `sleep 0` — no BGP drain at all before rebooting.
func TestDrainWaitSecondsRoundsUp(t *testing.T) {
	cases := []struct {
		name string
		d    time.Duration
		want int
	}{
		{"sub-second rounds up to one second", 900 * time.Millisecond, 1},
		{"exact seconds unchanged", 5 * time.Second, 5},
		{"fractional seconds round up", 1500 * time.Millisecond, 2},
		{"zero means default, not sleep 0", 0, 0},
		{"negative clamps to zero", -1 * time.Second, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := drainWaitSeconds(tc.d); got != tc.want {
				t.Errorf("drainWaitSeconds(%v) = %d, want %d", tc.d, got, tc.want)
			}
		})
	}
}

func TestExecuteFailsWhenTheDeviceComesBackOnTheOldImage(t *testing.T) {
	updater, api, _, _ := newUpdater(t, Options{AllowWrites: true})
	// It reboots but comes back on something unexpected.
	api.versions = []string{"2026.05.01-0100-rolling", "2026.01.01-0000-rolling"}

	plan, _ := updater.BuildPlan(context.Background())
	_, err := updater.Execute(context.Background(), plan)
	if err == nil || !strings.Contains(err.Error(), "came back on") {
		t.Fatalf("expected a version mismatch, got %v", err)
	}
}

func TestExecuteOnAnAlreadyCurrentPlanChangesNothing(t *testing.T) {
	updater, api, ssh, provider := newUpdater(t, Options{AllowWrites: true})
	api.versions = []string{"2026.06.30-0048-rolling"}

	plan, _ := updater.BuildPlan(context.Background())
	result, err := updater.Execute(context.Background(), plan)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result != "already current" {
		t.Fatalf("result = %q", result)
	}
	if len(ssh.recorded()) != 0 || provider.downloadCount() != 0 {
		t.Fatal("an already-current device was touched")
	}
}

func TestVersionFromURL(t *testing.T) {
	cases := map[string]string{
		"https://mirror.invalid/vyos-2026.06.30-0048-rolling-generic-amd64.iso": "2026.06.30-0048-rolling",
		"https://mirror.invalid/vyos-1.5.iso?x=1":                               "1.5.iso",
	}
	for in, want := range cases {
		if got := VersionFromURL(in); got != want {
			t.Fatalf("VersionFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}
