package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/common/pytext"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/sshx"
)

// The collaborators below are declared as local interfaces on purpose:
// the firmware mirror and image providers live in a package another
// worker owns, and Go interfaces are structural, so this package compiles
// and tests without importing it.

// ImageProvider is the upstream image source for a device type. Mirrors
// barf.util.images.ImageProvider.
type ImageProvider interface {
	// LatestVersion is the newest upstream release tag.
	LatestVersion(ctx context.Context) (string, error)
	// IsCurrent reports whether a running version is already the latest.
	IsCurrent(version string) bool
	// Download fetches the image locally, returning its path and size.
	Download(ctx context.Context) (path string, size int64, err error)
	// DownloadSignature fetches the detached .minisig, or returns "" when
	// upstream ships none.
	DownloadSignature(ctx context.Context) (string, error)
}

// Mirror publishes an image (and its signature) somewhere the devices can
// download it from. Mirrors barf.util.firmware.FirmwareMirror.
type Mirror interface {
	Publish(ctx context.Context, image, signature string) (string, error)
}

// DeviceAPI is the target device's HTTPS API surface. *APIClient
// implements it.
type DeviceAPI interface {
	Version(ctx context.Context) (string, error)
	SystemImages(ctx context.Context) ([]SystemImage, error)
	// DeleteImage is a WRITE and must itself refuse when writes are off.
	DeleteImage(ctx context.Context, name string) error
	VerifyRouting(ctx context.Context, hasASN bool) string
}

// SSHSession is the subset of *sshx.Client the updater uses.
type SSHSession interface {
	RunScript(ctx context.Context, req sshx.ScriptRequest) (sshx.Result, bool, error)
	RunDetached(ctx context.Context, name, content string) (string, error)
	Run(ctx context.Context, command string, timeout time.Duration) (sshx.Result, error)
	Close() error
}

// SSHDialer opens a session to the target. The updater dials twice: once
// for the install + drain launch, and once afterwards to look for a
// failure marker in the detached log.
type SSHDialer func(ctx context.Context) (SSHSession, error)

// sleep is the delay primitive; tests replace it so the suite never
// actually waits.
var sleep = func(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Updater performs at most ONE device update.
type Updater struct {
	// Host is the device to update.
	Host *model.Host
	// Fleet is every device the redundancy check may consider (the
	// Python side passes all VyOS hosts).
	Fleet []*model.Host
	// API talks to Host's HTTPS API.
	API DeviceAPI
	// Provider supplies the image; Mirror publishes it.
	Provider ImageProvider
	Mirror   Mirror
	// Probe answers "is this other fleet member alive?".
	Probe AliveProbe
	// SSH opens sessions to Host.
	SSH SSHDialer
	// DrainWait is how long to wait after the BGP shutdown before
	// rebooting. 0 means DefaultDrainWait.
	DrainWait time.Duration
	// Wait tunes the post-reboot liveness poll.
	Wait WaitOptions
	// Sleep is the delay primitive used for the drain wait. nil means a
	// real, context-interruptible sleep. It is injected by tests (in this
	// package and in cli) so a suite never actually waits out a drain.
	Sleep func(ctx context.Context, d time.Duration) error
	// Opts holds the write opt-in and the unsafe override.
	Opts Options

	mu       sync.Mutex
	executed bool
}

// DefaultDrainWait matches the Python --drain-wait default.
const DefaultDrainWait = 5 * time.Second

func (u *Updater) drainWait() time.Duration {
	if u.DrainWait > 0 {
		return u.DrainWait
	}
	return DefaultDrainWait
}

// Plan is everything BuildPlan learned, and everything Execute would do.
// Building it touches the fleet with reads only.
type Plan struct {
	Hostname       string
	CurrentVersion string
	TargetVersion  string
	// AlreadyCurrent means there is no newer image: nothing to do.
	AlreadyCurrent bool
	// InstallNeeded is false when the target image is already installed
	// AND already the default boot image, so only the reboot is left.
	InstallNeeded bool
	// StaleImage means the target version is installed but is NOT the
	// default boot image (an interrupted install): it gets deleted and
	// reinstalled, or the reboot would land on the old image.
	StaleImage bool
	// DrainBGP mirrors `bool(self.asn)`.
	DrainBGP  bool
	DrainWait time.Duration

	// Redundancy is what the fleet probe saw; RedundancyErr is non-nil
	// when the gate refuses.
	Redundancy    Redundancy
	RedundancyErr error

	// Steps is the human-readable plan, in order.
	Steps []string
}

// Safe reports whether the redundancy gate passed.
func (p *Plan) Safe() bool { return p.RedundancyErr == nil }

// BuildPlan gathers everything needed to decide, and to print, an update.
//
// It performs READS ONLY: the running version, the upstream release, the
// installed image list, and a parallel liveness probe of the rest of the
// fleet. It never downloads the image, never opens an SSH session and
// never changes anything. `barf device update` without --yes stops here.
func (u *Updater) BuildPlan(ctx context.Context) (*Plan, error) {
	if u.Host == nil {
		return nil, errors.New("lifecycle: no host to update")
	}
	plan := &Plan{
		Hostname:  u.Host.Hostname,
		DrainBGP:  u.Host.ASN != 0,
		DrainWait: u.drainWait(),
	}

	current, err := u.API.Version(ctx)
	if err != nil || current == "" || current == "-" {
		detail := "API unreachable"
		if err != nil {
			detail = fmt.Sprintf("API unreachable: %v", err)
		}
		return nil, &DeviceUnreachableError{Hostname: u.Host.Hostname, Detail: detail}
	}
	plan.CurrentVersion = current

	if u.Provider == nil {
		return nil, fmt.Errorf("%s: no image provider for devicetype %q",
			u.Host.Hostname, u.Host.DeviceType)
	}
	latest, err := u.Provider.LatestVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: could not determine the latest image: %w", u.Host.Hostname, err)
	}
	plan.TargetVersion = latest

	if u.Provider.IsCurrent(current) {
		plan.AlreadyCurrent = true
		plan.Steps = []string{"nothing to do: already running the latest image"}
		return plan, nil
	}

	// Which install work is left, from `show system image`.
	plan.InstallNeeded = true
	images, err := u.API.SystemImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: could not list system images: %w", u.Host.Hostname, err)
	}
	for _, image := range images {
		if image.Name != latest {
			continue
		}
		if image.DefaultBoot {
			plan.InstallNeeded = false
		} else {
			plan.StaleImage = true
		}
		break
	}

	// The redundancy gate. Failing it is recorded, not raised: the dry
	// run must be able to print the refusal.
	plan.Redundancy, plan.RedundancyErr = SafeToReboot(ctx, u.Host, u.Fleet, u.Probe)

	plan.Steps = planSteps(plan)
	return plan, nil
}

func planSteps(p *Plan) []string {
	var steps []string
	if p.InstallNeeded {
		steps = append(steps,
			fmt.Sprintf("mirror image %s and publish it for the device to download", p.TargetVersion),
			"run pre-checks on the device (free space on /, no unsaved config)",
		)
		if p.StaleImage {
			steps = append(steps, fmt.Sprintf(
				"DELETE the stale (non-default-boot) image %s already on the device", p.TargetVersion))
		}
		steps = append(steps, fmt.Sprintf(
			"INSTALL image %s and make it the default boot image", p.TargetVersion))
	} else {
		steps = append(steps, fmt.Sprintf(
			"skip install: %s is already installed and default boot", p.TargetVersion))
	}
	if p.DrainBGP {
		steps = append(steps,
			"SHUT DOWN BGP (committed, deliberately not saved, so a reboot restores it)",
			fmt.Sprintf("wait %s for traffic to drain", p.DrainWait))
	} else {
		steps = append(steps, "no BGP drain: this host has no ASN")
	}
	steps = append(steps,
		"REBOOT the device",
		"poll until it answers again, then check it came back on the new image",
		"verify BGP is not still Idle (Admin)",
	)
	return steps
}

// Execute performs the update. THIS CHANGES A PRODUCTION DEVICE.
//
// Every gate is checked here, before anything is downloaded or dialed:
//
//	Opts.AllowWrites  — without it nothing happens at all (dry run).
//	Opts.ForceUnsafe  — required, separately, when the redundancy gate
//	                    refused. Confirming the update does not imply it.
//	one reboot only   — a second Execute on this Updater is refused.
//
// Opts.RequireRouting additionally makes the post-reboot routing check
// fatal rather than advisory, so a caller with more devices to go stops
// instead of rebooting into a fleet that has not recovered.
func (u *Updater) Execute(ctx context.Context, plan *Plan) (string, error) {
	if plan == nil {
		return "", errors.New("lifecycle: Execute needs a Plan from BuildPlan")
	}
	if plan.AlreadyCurrent {
		return "already current", nil
	}
	if !u.Opts.AllowWrites {
		return "", ErrWritesNotAllowed
	}
	if plan.RedundancyErr != nil && !u.Opts.ForceUnsafe {
		return "", plan.RedundancyErr
	}

	u.mu.Lock()
	if u.executed {
		u.mu.Unlock()
		return "", ErrAlreadyExecuted
	}
	u.executed = true
	u.mu.Unlock()

	out := u.Opts.out()
	prefix := "[" + plan.Hostname + "] "

	if plan.RedundancyErr != nil {
		fmt.Fprintf(out, "%sOVERRIDING THE REDUNDANCY GATE (--force): %v\n", prefix, plan.RedundancyErr)
		fmt.Fprintf(out, "%salive spines: %s; alive leaves: %s\n", prefix,
			joinOrNone(plan.Redundancy.AliveSpines), joinOrNone(plan.Redundancy.AliveLeaves))
	}

	// Mirror the image. Local/bucket work, not a device write, but it is
	// deliberately after the gates so a refused update never spends the
	// bandwidth.
	imagePath, size, err := u.Provider.Download(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: downloading image: %w", plan.Hostname, err)
	}
	signature, err := u.Provider.DownloadSignature(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: downloading image signature: %w", plan.Hostname, err)
	}
	url, err := u.Mirror.Publish(ctx, imagePath, signature)
	if err != nil {
		return "", fmt.Errorf("%s: mirroring image: %w", plan.Hostname, err)
	}
	fmt.Fprintf(out, "%simage mirrored at %s\n", prefix, url)

	version := plan.TargetVersion
	if version == "" {
		version = VersionFromURL(url)
	}

	if err := u.installAndReboot(ctx, plan, url, size, version); err != nil {
		return "", err
	}

	fmt.Fprintf(out, "%sdevice rebooting\n", prefix)
	newVersion, err := WaitForAlive(ctx, plan.Hostname, u.API.Version, plan.CurrentVersion, u.waitOptions())
	if err != nil {
		return "", err
	}
	if !u.Provider.IsCurrent(newVersion) {
		return "", fmt.Errorf("%s came back on %q, expected %s", plan.Hostname, newVersion, version)
	}

	if warning := u.API.VerifyRouting(ctx, u.Host.ASN != 0); warning != "" {
		fmt.Fprintf(out, "%swarning: %s\n", prefix, warning)
		if u.Opts.RequireRouting {
			// The device IS updated -- that is reported alongside the
			// error so a summary can say so -- but the fleet is not
			// healthy, so the caller must not move on to the next device.
			return "updated to " + version + ", but routing did not recover",
				&RoutingNotRecoveredError{Hostname: plan.Hostname, Detail: warning}
		}
	}

	return "updated to " + version, nil
}

// pause waits d, through the injected Sleep when there is one.
func (u *Updater) pause(ctx context.Context, d time.Duration) error {
	if u.Sleep != nil {
		return u.Sleep(ctx, d)
	}
	return sleep(ctx, d)
}

func (u *Updater) waitOptions() WaitOptions {
	opts := u.Wait
	if opts.Out == nil {
		opts.Out = u.Opts.out()
	}
	return opts
}

// installAndReboot is the device-changing core: precheck, install, then
// the detached drain+reboot. Ports VyOSHost.update_host.
func (u *Updater) installAndReboot(ctx context.Context, plan *Plan, url string, size int64, version string) error {
	out := u.Opts.out()
	prefix := "[" + plan.Hostname + "] "

	// The installer downloads the ISO into the invoking user's home
	// directory and copies the squashfs to /boot -- both on the root
	// filesystem. It refuses to run below 2GiB free on / itself, so demand
	// that floor (or 2x the image for oversized ones) so we fail cleanly
	// at precheck instead of mid-install.
	requiredMB := 2048
	if doubled := int(math.Ceil(float64(size) * 2 / (1 << 20))); doubled > requiredMB {
		requiredMB = doubled
	}

	conn, err := u.SSH(ctx)
	if err != nil {
		return &DeviceUnreachableError{Hostname: plan.Hostname, Detail: "ssh: " + err.Error()}
	}
	// Closed explicitly before the post-drain probe below; the defer is
	// the failure path.
	defer func() { _ = conn.Close() }()

	if plan.InstallNeeded {
		fmt.Fprintf(out, "%srunning pre-checks\n", prefix)
		_, ok, err := conn.RunScript(ctx, sshx.ScriptRequest{
			Name:       "barf-precheck.sh",
			Content:    sshx.PrecheckScript(requiredMB, "/"),
			Marker:     sshx.MarkerPrecheck,
			Echo:       out,
			EchoPrefix: prefix + "  ",
		})
		if err != nil {
			return fmt.Errorf("%s: pre-checks: %w", plan.Hostname, err)
		}
		if !ok {
			return fmt.Errorf("%s: pre-checks failed", plan.Hostname)
		}

		if plan.StaleImage {
			// A directory in /boot alone (interrupted install, or an image
			// that lost default boot) must not skip the installer, or the
			// reboot would land on the old default image. Deleted only
			// after the precheck so a failed precheck leaves the device
			// untouched.
			fmt.Fprintf(out, "%sdeleting stale image %s\n", prefix, version)
			if err := u.API.DeleteImage(ctx, version); err != nil {
				return fmt.Errorf("%s: deleting stale image %s: %w", plan.Hostname, version, err)
			}
		}

		fmt.Fprintf(out, "%sinstalling image from %s\n", prefix, url)
		_, ok, err = conn.RunScript(ctx, sshx.ScriptRequest{
			Name:       "barf-install.sh",
			Content:    sshx.InstallScript(url, version),
			Marker:     sshx.MarkerInstall,
			Echo:       out,
			EchoPrefix: prefix + "  ",
		})
		if err != nil {
			return fmt.Errorf("%s: image install: %w", plan.Hostname, err)
		}
		if !ok {
			return fmt.Errorf("%s: image install failed", plan.Hostname)
		}
	}

	drainWait := plan.DrainWait
	fmt.Fprintf(out, "%slaunching detached drain+reboot\n", prefix)
	logPath, err := conn.RunDetached(ctx, "barf-reboot.sh",
		sshx.DrainAndRebootScript(version, int(drainWait.Seconds()), plan.DrainBGP))
	if err != nil {
		return fmt.Errorf("%s: launching drain+reboot: %w", plan.Hostname, err)
	}
	_ = conn.Close()

	// We reach the device over the same BGP paths the drain tears down,
	// so the script runs detached and we verify by absence: wait out the
	// drain, and only if the device is still reachable afterwards look
	// for a failure marker in its log.
	if err := u.pause(ctx, drainWait+5*time.Second); err != nil {
		return err
	}
	if failure := u.detachedRebootFailure(ctx, logPath); failure != "" {
		return fmt.Errorf("%s: drain/reboot failed: %s", plan.Hostname, failure)
	}
	return nil
}

// detachedRebootFailure returns a failure line from the detached
// drain+reboot log, if any.
//
// Being unable to connect is the expected success case (the drain severed
// our path, or the device is already rebooting); a device that still
// answers with a FAIL marker in its log means the script bailed and no
// reboot is coming.
func (u *Updater) detachedRebootFailure(ctx context.Context, logPath string) string {
	conn, err := u.SSH(ctx)
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()

	result, err := conn.Run(ctx, "cat "+pytext.ShellQuote(logPath), 10*time.Second)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(result.Output, "\n") {
		if strings.Contains(line, "-FAIL") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// VersionFromURL derives an image version from its mirror URL, as the
// Python update_host does when no version is passed.
func VersionFromURL(rawURL string) string {
	name := rawURL
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	name = path.Base(name)
	return strings.TrimSuffix(strings.TrimPrefix(name, "vyos-"), "-generic-amd64.iso")
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
