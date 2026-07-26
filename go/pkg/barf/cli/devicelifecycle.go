package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/general-programming/megarepo/go/pkg/barf/firmware"
	"github.com/general-programming/megarepo/go/pkg/barf/lifecycle"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/sshx"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// `barf device ...`: the lifecycle commands.
//
// These are the only barf commands that can change a device, and the
// rules they follow are:
//
//   - Nothing is changed until the operator says so. On a terminal
//     `update` prints the full plan (what is running, what would be
//     installed, what the redundancy check found, every step) and then
//     asks [y/N] for THAT device; answering y performs it in the same
//     invocation. --yes skips the prompt.
//   - With no terminal there is no prompt, so --yes is required to
//     change anything and without it the run is a dry run that prints
//     the plan and exits. See confirm.go; `deploy` shares this matrix.
//   - --force, separately, is the only way past a redundancy refusal.
//     --yes never implies it, and --force applies per device.
//   - `update` accepts several hostnames, or "all", but it updates them
//     STRICTLY ONE AT A TIME, re-runs the redundancy check immediately
//     before each one, and waits for each device to be genuinely healthy
//     (answering, on the new image, routing recovered) before starting
//     the next. The first failure stops the run.
//   - "all" may not be combined with --yes: an unprompted, fleet-wide
//     reboot has to be spelled out host by host.

// sshPort is the port probed to find a device's SSH address. Python
// probes it separately from the API port: sshd and the API can be bound
// to different addresses.
const sshPort = 22

// The lifecycle backends this file needs. They are vars so tests can
// replace them with fakes: no test in this package may reach Vault, a
// mirror, or a device.
var (
	// newSupertechCredentials resolves the shared supertech account.
	newSupertechCredentials = func() (sshx.CredentialSource, error) {
		c, err := vault.New(vault.Options{})
		if err != nil {
			return nil, err
		}
		return vaultSupertech{c: c}, nil
	}

	// newVyOSAPIKey resolves the VyOS HTTPS API key.
	newVyOSAPIKey = func() (string, error) {
		c, err := vault.New(vault.Options{})
		if err != nil {
			return "", err
		}
		return c.Get(context.Background(), "", "vyos-api-password", "secret")
	}

	// newImageProvider returns the upstream image source for a device
	// type, adapted onto the shape the updater wants.
	newImageProvider = func(deviceType string) (lifecycle.ImageProvider, error) {
		p, ok := firmware.For(strings.ToLower(deviceType))
		if !ok {
			return nil, fmt.Errorf("no image provider for %q: pass --image-url (and --target-version) to update from an explicit image", deviceType)
		}
		return &firmwareProvider{provider: p}, nil
	}

	// newFirmwareMirror publishes an image where devices can fetch it.
	// The mirror settings live in network.yml's global_meta.firmware
	// block, which model.GlobalMeta does not (yet) carry, so the file is
	// re-read as a raw map here — the same thing firmware's own
	// MirrorConfigFromMeta expects.
	newFirmwareMirror = func(networkPath string) (lifecycle.Mirror, error) {
		raw, err := os.ReadFile(networkPath) // #nosec G304 -- the operator's own --network path
		if err != nil {
			return nil, err
		}
		var document map[string]any
		if err := yaml.Unmarshal(raw, &document); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", networkPath, err)
		}
		globalMeta, _ := document["global_meta"].(map[string]any)
		if globalMeta == nil {
			return nil, errors.New("global_meta: no firmware mirror configured; pass --image-url instead")
		}
		client, err := vault.New(vault.Options{})
		if err != nil {
			return nil, err
		}
		return firmware.NewMirrorFromVault(context.Background(), globalMeta, client)
	}
)

// firmwareProvider adapts firmware.Provider onto
// lifecycle.ImageProvider. The two differ in two places: firmware's
// IsCurrent takes a context and can fail, and its Download takes the
// asset rather than fetching it itself.
type firmwareProvider struct {
	provider firmware.Provider

	// once fetches the release tag exactly once; mu guards the result, so
	// the ctx-less IsCurrent can read it without racing the fetch. once
	// alone only orders callers that go through once.Do, and IsCurrent
	// deliberately does not (it must never start a network fetch of its
	// own — it has no context to bound one with).
	once   sync.Once
	mu     sync.RWMutex
	latest string
	err    error
}

func (f *firmwareProvider) LatestVersion(ctx context.Context) (string, error) {
	f.once.Do(func() {
		latest, err := f.provider.LatestVersion(ctx)
		f.mu.Lock()
		f.latest, f.err = latest, err
		f.mu.Unlock()
	})
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.latest, f.err
}

// IsCurrent compares against the release tag resolved by LatestVersion,
// which the updater always calls first. If it somehow has not been
// resolved, "not current" is the safe answer: it makes the updater do
// more work (and hit a real error later), never less.
func (f *firmwareProvider) IsCurrent(version string) bool {
	f.mu.RLock()
	latest := f.latest
	f.mu.RUnlock()
	if latest == "" {
		return false
	}
	return firmware.IsCurrent(latest, version)
}

func (f *firmwareProvider) Download(ctx context.Context) (string, int64, error) {
	asset, err := f.provider.Image(ctx)
	if err != nil {
		return "", 0, err
	}
	path, err := f.provider.Download(ctx, asset)
	if err != nil {
		return "", 0, err
	}
	return path, asset.Size, nil
}

func (f *firmwareProvider) DownloadSignature(ctx context.Context) (string, error) {
	asset, ok, err := f.provider.Signature(ctx)
	if err != nil || !ok {
		return "", err
	}
	return f.provider.Download(ctx, asset)
}

// apiBaseURLOverride redirects a device's API requests at a test server.
// It is nil in production: the real path always builds
// https://<probed address>:443 from the host being operated on. Tests
// set it to a per-hostname router, so one httptest server can stand in
// for a whole fleet and each fake device answers for itself.
var apiBaseURLOverride func(hostname string) string

func apiBaseURL(hostname string) string {
	if apiBaseURLOverride == nil {
		return ""
	}
	return apiBaseURLOverride(hostname)
}

// vaultSupertech reads the shared supertech account from Vault. The
// password is held in memory only: it is never logged, printed or
// written anywhere.
type vaultSupertech struct{ c *vault.Client }

func (v vaultSupertech) Supertech(ctx context.Context) (sshx.Credentials, error) {
	username, err := v.c.Get(ctx, "", "supertech-credentials", "username")
	if err != nil {
		return sshx.Credentials{}, err
	}
	password, err := v.c.Get(ctx, "", "supertech-credentials", "password")
	if err != nil {
		return sshx.Credentials{}, err
	}
	return sshx.Credentials{Username: username, Password: password}, nil
}

// sshUsername mirrors the per-vendor BaseHost.SSH_USERNAME. "" means the
// shared supertech account (with its Vault password as the fallback).
func sshUsername(deviceType string) string {
	switch strings.ToLower(deviceType) {
	case "eos":
		return "admin"
	case "linux":
		return "root"
	}
	return ""
}

// probeSSHEndpoint returns the first of a host's candidate addresses
// answering on port 22, or "". Ports BaseHost.ssh_ip. Connecting and
// immediately closing is the only device contact this makes.
func probeSSHEndpoint(ctx context.Context, h *model.Host, searchDomain string) string {
	for _, candidate := range endpointCandidates(h, searchDomain) {
		if ctx.Err() != nil {
			return ""
		}
		conn, err := dialContext(ctx, "tcp", net.JoinHostPort(candidate, strconv.Itoa(sshPort)))
		if err != nil {
			continue
		}
		_ = conn.Close()
		return candidate
	}
	return ""
}

// newDeviceCmd builds the `barf device` command group.
func newDeviceCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "device",
		Short: "Interact with live devices",
		Long: "device groups the commands that talk to a running device.\n\n" +
			"update and cleanup are the only barf commands that can change one.\n" +
			"Both print exactly what they would do first: update then asks per\n" +
			"device on a terminal, and is a dry run without --yes when there is\n" +
			"no terminal to ask on. --force is required, separately, to override\n" +
			"a redundancy refusal.",
	}
	cmd.AddCommand(
		newDeviceSSHCmd(o),
		newDeviceUpdateCmd(o),
		newDeviceCleanupCmd(o),
	)
	return cmd
}

// -- device ssh -------------------------------------------------------

func newDeviceSSHCmd(o *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "ssh <host>",
		Short: "Open an interactive shell on a device",
		Long: "ssh resolves the device's reachable SSH address the same way deploys\n" +
			"do and hands the terminal to ssh(1) as the vendor's SSH user (or the\n" +
			"shared supertech account). Exits with the remote shell's status.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceSSH(cmd.Context(), o, args[0])
		},
	}
}

// execSSH runs the system ssh client. A var so tests can intercept it
// without spawning anything.
var execSSH = func(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv is built below from a resolved host, not user text
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sshArgv builds the ssh(1) command line for a device.
func sshArgv(username, address string) []string {
	argv := []string{"ssh"}
	if username != "" {
		argv = append(argv, "-l", username)
	}
	return append(argv, address)
}

// runDeviceSSH hands the terminal to the system ssh binary.
//
// Why exec ssh(1) rather than proxy a PTY through x/crypto/ssh: an
// interactive shell is not just a byte pump. It needs the local tty in
// raw mode with a guaranteed restore on every exit path, SIGWINCH
// forwarded as window resizes, and correct signal and exit-status
// propagation — the Python version spends ~50 lines on exactly that and
// still ignores the user's ~/.ssh/config. ssh(1) already does all of it,
// plus ProxyJump/ProxyCommand, known_hosts, agent forwarding, Match
// blocks and Kerberos, so a device reachable in the operator's normal
// shell stays reachable here. Re-implementing that is a large amount of
// termios code whose failure mode is a wedged terminal on a router
// console.
//
// os/exec (rather than syscall.Exec) keeps this portable and testable;
// stdin/stdout/stderr are inherited, so the tty passes straight through
// and the remote exit status comes back via ExitError.
func runDeviceSSH(ctx context.Context, o *Options, target string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if target == "all" {
		return errors.New("device ssh takes a single hostname")
	}

	net, hosts, err := o.loadTargets([]string{target})
	if err != nil {
		return err
	}
	host := hosts[0]

	address := probeSSHEndpoint(ctx, host, net.Global.SearchDomain)
	if address == "" {
		return fmt.Errorf("%s: no reachable SSH address", host.Hostname)
	}

	username := sshUsername(host.DeviceType)
	if username == "" {
		// The shared account: ssh(1) needs the name, and only the name.
		// The password is never passed on a command line; the fleet is
		// publickey-only and the operator's agent/keys do the work.
		source, err := newSupertechCredentials()
		if err != nil {
			return fmt.Errorf("%s: resolving the shared SSH account: %w", host.Hostname, err)
		}
		creds, err := source.Supertech(ctx)
		if err != nil {
			return fmt.Errorf("%s: resolving the shared SSH account: %w", host.Hostname, err)
		}
		username = creds.Username
	}

	o.printf("[%s] connecting as %s@%s\n", host.Hostname, username, address)

	if err := execSSH(ctx, sshArgv(username, address)); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitStatusError(exitErr.ExitCode())
		}
		return fmt.Errorf("%s: %w", host.Hostname, err)
	}
	return nil
}

// exitStatusError carries a remote exit status out to the caller.
type exitStatusError int

func (e exitStatusError) Error() string {
	return fmt.Sprintf("remote shell exited with status %d", int(e))
}

// -- device update ----------------------------------------------------

type updateFlags struct {
	drainWait     time.Duration
	yes           bool
	force         bool
	imageURL      string
	targetVersion string

	// in is where per-device confirmations are read from; nil means
	// os.Stdin.
	in io.Reader
}

func newDeviceUpdateCmd(o *Options) *cobra.Command {
	var f updateFlags

	cmd := &cobra.Command{
		Use:   "update <host...|all>",
		Short: "Install the latest image on devices and reboot into it, one at a time",
		Long: "update installs a newer image on each selected device, drains its BGP\n" +
			"sessions, reboots it, waits for it to come back and verifies routing\n" +
			"recovered.\n\n" +
			"Several hostnames, or \"all\", may be given. Devices are updated\n" +
			"STRICTLY ONE AT A TIME. The fleet redundancy check is re-run\n" +
			"immediately before each device — rebooting leaf-2 changes what is\n" +
			"safe for leaf-1 — and the next device is not started until the\n" +
			"previous one is answering, running the new image and has recovered\n" +
			"its routing. The first failure stops the run; the rest are reported\n" +
			"as not attempted.\n\n" +
			"On a terminal each device's plan is printed and confirmed with a\n" +
			"[y/N] prompt; answering y performs it right there. --yes skips the\n" +
			"prompt. With --plain, or no terminal, nothing is ever prompted, so\n" +
			"--yes is required to change anything and without it the run is a DRY\n" +
			"RUN that prints every plan and exits.\n\n" +
			"The redundancy check refuses to reboot the last live spine or the last\n" +
			"live leaf. That refusal is a hard error: --yes does not override it,\n" +
			"only --force does, and it is re-decided per device.\n\n" +
			"\"all\" cannot be combined with --yes: an unprompted fleet-wide reboot\n" +
			"has to be spelled out host by host.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceUpdate(cmd.Context(), o, args, f)
		},
	}
	flags := cmd.Flags()
	flags.DurationVar(&f.drainWait, "drain-wait", lifecycle.DefaultDrainWait,
		"how long to wait after the BGP shutdown before rebooting")
	flags.BoolVar(&f.yes, "yes", false,
		"skip the per-device confirmation (required to change anything when there is no terminal)")
	flags.BoolVar(&f.force, "force", false,
		"override a redundancy refusal (rebooting the last live spine/leaf). Requires --yes")
	flags.StringVar(&f.imageURL, "image-url", "",
		"install this already-published image URL instead of the mirrored latest release")
	flags.StringVar(&f.targetVersion, "target-version", "",
		"the version --image-url contains (default: derived from the URL)")
	return cmd
}

// Summary results, so the table wording is decided in one place.
const (
	resultAlreadyOK     = "already current"
	resultSkipped       = "skipped (declined)"
	resultWouldUpdate   = "would update (dry run)"
	resultRefused       = "REFUSED: redundancy"
	resultNotAttempted  = "not attempted (run stopped earlier)"
	resultForcedUpdated = "updated (redundancy gate FORCED)"
)

// updateRun holds what a `device update` resolves ONCE for the whole
// run. Everything that depends on the state of the fleet — the endpoint
// probe, the running version, and above all the redundancy check — is
// deliberately NOT in here: it is redone per device, because rebooting
// one device changes the answer for the next.
type updateRun struct {
	o     *Options
	net   *model.Network
	flags updateFlags

	key         string
	provider    lifecycle.ImageProvider
	mirror      lifecycle.Mirror
	credentials sshx.CredentialSource

	// requireRouting makes a post-reboot routing failure fatal instead of
	// advisory. Set when this run has more than one device to get
	// through: continuing to reboot a fleet whose last member has not
	// recovered is how a maintenance window becomes an outage.
	requireRouting bool
}

func runDeviceUpdate(ctx context.Context, o *Options, targets []string, f updateFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// --force is an override of a hard safety refusal, never a thing you
	// arrive at by pressing y at a prompt.
	if f.force && !f.yes {
		return errors.New("--force requires --yes: overriding the redundancy gate is not something " +
			"a [y/N] prompt can authorise, it has to be stated on the command line")
	}

	net, selected, err := o.loadTargets(targets)
	if err != nil {
		return err
	}
	hosts, err := updatableHosts(o, selected)
	if err != nil {
		return err
	}

	gate := newWriteGate(o, f.yes, f.in)
	announceFleetSelection(o, targets, hosts, f.yes)

	// Reboot leaves before spines, as the Python original does: a spine's
	// safety gate only means anything while the leaves under it are up,
	// so the leaves go first and the gate stays honest.
	sort.SliceStable(hosts, func(i, j int) bool {
		return !hosts[i].IsSpine() && hosts[j].IsSpine()
	})

	key, err := newVyOSAPIKey()
	if err != nil {
		return fmt.Errorf("resolving the VyOS API key: %w", err)
	}
	networkPath, err := o.networkPath()
	if err != nil {
		return err
	}
	provider, mirror, err := imageSource(hosts[0].DeviceType, networkPath, f)
	if err != nil {
		return err
	}
	credentials, err := newSupertechCredentials()
	if err != nil {
		return fmt.Errorf("resolving the shared SSH account: %w", err)
	}

	run := &updateRun{
		o: o, net: net, flags: f,
		key: key, provider: provider, mirror: mirror, credentials: credentials,
		requireRouting: len(hosts) > 1,
	}
	return run.sequential(ctx, hosts, gate)
}

// updatableHosts narrows a selection to the devices update can handle.
//
// A single named device that is not supported is an error: the operator
// asked for that device by name and got nothing. In a wider selection it
// is a reported skip, so `all` does not fail on the EOS boxes.
func updatableHosts(o *Options, selected []*model.Host) ([]*model.Host, error) {
	isVyOS := func(h *model.Host) bool { return strings.EqualFold(h.DeviceType, "vyos") }

	if len(selected) == 1 && !isVyOS(selected[0]) {
		return nil, fmt.Errorf("%s: device update only supports vyos devices (this one is %q)",
			selected[0].Hostname, selected[0].DeviceType)
	}
	for _, h := range selected {
		if !isVyOS(h) {
			o.printf("skip %s: device update only supports vyos devices (this one is %q)\n",
				h.Hostname, h.DeviceType)
		}
	}
	hosts := filterHosts(selected, isVyOS)
	if len(hosts) == 0 {
		return nil, errors.New("no updatable devices selected (vyos only)")
	}
	return hosts, nil
}

// announceFleetSelection prints the resolved device list before an
// unattended multi-device run.
//
// "all" with --yes is deliberately NOT refused. The protection that
// matters is in the mechanism, not in the spelling of the selection:
// devices are updated strictly one at a time, the redundancy gate is
// re-evaluated immediately before each one, and the first failure stops
// the run. Pinning a hostname list instead would go stale — a device
// added next month would silently never be updated, and a device quietly
// missed for a year is the failure mode this fleet actually suffers
// from. What is worth keeping is visibility: say out loud what is about
// to be rebooted, so it is in the log even when nobody was watching.
func announceFleetSelection(o *Options, targets []string, hosts []*model.Host, yes bool) {
	if !yes || len(hosts) < 2 {
		return
	}
	isAll := len(targets) == 0
	for _, t := range targets {
		if t == "all" {
			isAll = true
		}
	}
	if !isAll {
		return
	}
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Hostname)
	}
	o.printf("unattended run over the whole fleet: %d device(s) will be rebooted one at a time\n", len(hosts))
	o.printf("  %s\n\n", strings.Join(names, " "))
}

// sequential updates every host, one at a time, stopping on the first
// failure.
//
// The ordering guarantee is the point of this function: nothing here is
// concurrent, each device's plan (and therefore its redundancy check) is
// built immediately before that device is touched, and the previous
// device has already been confirmed answering, on the new image and
// routing-healthy by the time the next one is planned.
func (r *updateRun) sequential(ctx context.Context, hosts []*model.Host, gate writeGate) error {
	o := r.o
	rows := make([][]string, 0, len(hosts))
	var anyChangeable bool
	var failedHost string
	var firstErr error

	for i, host := range hosts {
		if err := ctx.Err(); err != nil {
			firstErr, failedHost = err, host.Hostname
			rows = append(rows, []string{host.Hostname, "cancelled"})
			rows = appendNotAttempted(rows, hosts[i+1:])
			break
		}

		o.printf("\n=== %s (%d/%d) ===\n", host.Hostname, i+1, len(hosts))

		result, err := r.one(ctx, host, gate, &anyChangeable)
		if err != nil {
			firstErr, failedHost = err, host.Hostname
			o.printf("\n[%s] %v\n", host.Hostname, err)
			rows = append(rows, []string{host.Hostname, "failed: " + firstLine(err.Error())})
			if i+1 < len(hosts) {
				o.printf("\nstopping: a failed update reduces redundancy for the rest of the fleet.\n")
				rows = appendNotAttempted(rows, hosts[i+1:])
			}
			break
		}
		rows = append(rows, []string{host.Hostname, result})
	}

	o.printf("\n")
	printTable(o.Out, []string{"DEVICE", "RESULT"}, rows)

	if gate.dryRun && anyChangeable {
		o.printf("\nDRY RUN: nothing was changed. Re-run with --yes to perform this update.\n")
	}
	switch {
	case firstErr == nil:
		return nil
	case len(hosts) == 1:
		// One device: the device's own error IS the run's error.
		return firstErr
	default:
		return fmt.Errorf("stopped at %s: %w", failedHost, firstErr)
	}
}

// firstLine keeps a multi-line explanation out of a table cell.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func appendNotAttempted(rows [][]string, remaining []*model.Host) [][]string {
	for _, h := range remaining {
		rows = append(rows, []string{h.Hostname, resultNotAttempted})
	}
	return rows
}

// one plans and, if permitted, performs the update of a single device.
// It returns the summary line for the device, or an error that stops the
// whole run.
func (r *updateRun) one(ctx context.Context, host *model.Host, gate writeGate, anyChangeable *bool) (string, error) {
	o := r.o

	// The planning updater's API client is constructed read-only, so the
	// phase that decides whether to proceed is structurally incapable of
	// changing the device. The redundancy check happens here, NOW, for
	// this device: any check run before the previous reboot is stale.
	planner, err := r.newUpdater(ctx, host, false)
	if err != nil {
		return "", err
	}
	plan, err := planner.BuildPlan(ctx)
	if err != nil {
		return "", err
	}
	printUpdatePlan(o, plan, gate)

	if plan.AlreadyCurrent {
		return resultAlreadyOK, nil
	}
	*anyChangeable = true

	if gate.dryRun {
		if plan.RedundancyErr != nil {
			o.printf("\n         --yes alone is NOT enough here: the redundancy check refuses.\n")
			return resultRefused + " (dry run)", nil
		}
		return resultWouldUpdate, nil
	}

	// A refusal is decided per device and is hard: only --force passes
	// it, and --force already required --yes.
	if plan.RedundancyErr != nil && !r.flags.force {
		return "", fmt.Errorf("%w\n"+
			"       This is a hard safety refusal and --yes does not override it.\n"+
			"       Re-run with --force ONLY if you have confirmed out of band that\n"+
			"       taking %s down right now will not black-hole traffic.",
			plan.RedundancyErr, plan.Hostname)
	}
	forced := plan.RedundancyErr != nil

	if forced {
		o.printf("\n!! --force: OVERRIDING the redundancy refusal for %s: %v\n",
			plan.Hostname, plan.RedundancyErr)
		o.printf("!! alive spines: %s; alive leaves: %s\n",
			listOrNone(plan.Redundancy.AliveSpines), listOrNone(plan.Redundancy.AliveLeaves))
	}

	o.printf("\n")
	ok, err := gate.allows(fmt.Sprintf("[%s] install %s, drain BGP, and REBOOT now?",
		plan.Hostname, plan.TargetVersion))
	if err != nil {
		return "", confirmError(err)
	}
	if !ok {
		o.printf("skipped %s\n", plan.Hostname)
		return resultSkipped, nil
	}

	// Only now is a write-enabled client built, and a fresh Updater with
	// it: one Updater still reboots at most one device.
	executor, err := r.newUpdater(ctx, host, true)
	if err != nil {
		return "", err
	}
	result, err := executor.Execute(ctx, plan)
	if err != nil {
		return "", err
	}
	o.printf("\n[%s] %s\n", plan.Hostname, result)
	if forced {
		return resultForcedUpdated, nil
	}
	return result, nil
}

// newFleetProbe builds the liveness probe backing one redundancy check.
// It is a var, and called once per device rather than once per run, so
// tests can count that the gate really is re-evaluated for every device.
var newFleetProbe = fleetProbe

// newSSHDialer opens sessions to the device being updated. A var so the
// tests can drive a whole update without an SSH client existing.
var newSSHDialer = sshDialer

// updateTiming is how long an update waits: for the BGP drain to take
// effect, and for the device to come back. Both zero values mean the
// lifecycle defaults (a real 5s+ drain wait, a 15s/5s/15m reboot poll).
// The tests shrink them so the suite never sits out a reboot.
var updateTiming struct {
	wait  lifecycle.WaitOptions
	sleep func(ctx context.Context, d time.Duration) error
}

// newUpdater builds an Updater for one device.
//
// allowWrites drives BOTH the lifecycle write opt-in and the API
// client's own: a planning updater cannot reach a write endpoint even
// through a bug in a caller.
func (r *updateRun) newUpdater(ctx context.Context, host *model.Host, allowWrites bool) (*lifecycle.Updater, error) {
	address := probeEndpoint(ctx, host, r.net.Global.SearchDomain)
	if address == "" {
		return nil, fmt.Errorf("%s: no reachable address", host.Hostname)
	}
	api, err := lifecycle.NewAPIClient(host.Hostname, lifecycle.APIOptions{
		Address:            address,
		Key:                r.key,
		InsecureSkipVerify: true,
		BaseURL:            apiBaseURL(host.Hostname),
		AllowWrites:        allowWrites,
	})
	if err != nil {
		return nil, err
	}
	// Only the planning updater gets a probe: the redundancy gate is
	// evaluated exactly once per device, in BuildPlan, and Execute acts
	// on the verdict the plan recorded. Handing the write-enabled updater
	// a probe would only invite a second, differently-timed opinion.
	var probe lifecycle.AliveProbe
	if !allowWrites {
		probe = newFleetProbe(r.net, r.key)
	}
	return &lifecycle.Updater{
		Host:      host,
		Fleet:     fleetFor(r.net, host),
		API:       api,
		Provider:  r.provider,
		Mirror:    r.mirror,
		Probe:     probe,
		SSH:       newSSHDialer(ctx, host, r.net, r.credentials),
		DrainWait: r.flags.drainWait,
		Wait:      updateTiming.wait,
		Sleep:     updateTiming.sleep,
		Opts: lifecycle.Options{
			AllowWrites:    allowWrites,
			ForceUnsafe:    allowWrites && r.flags.force,
			RequireRouting: allowWrites && r.requireRouting,
			Out:            r.o.Out,
		},
	}, nil
}

// imageSource resolves where the image comes from: an explicit
// --image-url, or the wired provider + firmware mirror.
func imageSource(deviceType, networkPath string, f updateFlags) (lifecycle.ImageProvider, lifecycle.Mirror, error) {
	if f.imageURL != "" {
		static := staticImage{url: f.imageURL, version: f.targetVersion}
		if static.version == "" {
			static.version = lifecycle.VersionFromURL(f.imageURL)
		}
		if static.version == "" {
			return nil, nil, errors.New("--image-url: could not derive a version from the URL; pass --target-version")
		}
		return static, static, nil
	}

	provider, err := newImageProvider(deviceType)
	if err != nil {
		return nil, nil, err
	}
	mirror, err := newFirmwareMirror(networkPath)
	if err != nil {
		return nil, nil, err
	}
	return provider, mirror, nil
}

// staticImage is the --image-url escape hatch: an image that is already
// published somewhere the device can reach. It is both the "provider"
// (it knows the target version) and the "mirror" (publishing is a no-op,
// the URL is already public).
type staticImage struct {
	url     string
	version string
}

func (s staticImage) LatestVersion(context.Context) (string, error) { return s.version, nil }

// IsCurrent mirrors the Python `self.latest_version in version`
// substring test, which tolerates the device reporting extra build
// detail around the release tag.
func (s staticImage) IsCurrent(version string) bool {
	return version != "" && strings.Contains(version, s.version)
}

func (s staticImage) Download(context.Context) (string, int64, error)   { return "", 0, nil }
func (s staticImage) DownloadSignature(context.Context) (string, error) { return "", nil }
func (s staticImage) Publish(context.Context, string, string) (string, error) {
	return s.url, nil
}

// fleetFor is the set of devices the redundancy check considers: every
// VyOS host in the network, as the Python side does.
func fleetFor(n *model.Network, exclude *model.Host) []*model.Host {
	var fleet []*model.Host
	for i := range n.Hosts {
		h := &n.Hosts[i]
		if !strings.EqualFold(h.DeviceType, "vyos") {
			continue
		}
		fleet = append(fleet, h)
	}
	_ = exclude // SafeToReboot drops the target itself.
	return fleet
}

// fleetProbe answers "is this other device alive?" for the redundancy
// check: probe its API port, then ask for its version. Read-only, and
// safe to call from several goroutines at once (each call builds its own
// client).
func fleetProbe(n *model.Network, key string) lifecycle.AliveProbe {
	return func(ctx context.Context, h *model.Host) bool {
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		address := probeEndpoint(probeCtx, h, n.Global.SearchDomain)
		if address == "" {
			return false
		}
		client, err := lifecycle.NewAPIClient(h.Hostname, lifecycle.APIOptions{
			Address:            address,
			Key:                key,
			InsecureSkipVerify: true,
			BaseURL:            apiBaseURL(h.Hostname),
			Timeout:            10 * time.Second,
			// Explicitly read-only: the probe must never be able to write.
			AllowWrites: false,
		})
		if err != nil {
			return false
		}
		version, err := client.Version(probeCtx)
		return err == nil && version != "" && version != "-"
	}
}

// sshDialer opens an authenticated session to the target device.
func sshDialer(_ context.Context, host *model.Host, n *model.Network, creds sshx.CredentialSource) lifecycle.SSHDialer {
	return func(ctx context.Context) (lifecycle.SSHSession, error) {
		address := probeSSHEndpoint(ctx, host, n.Global.SearchDomain)
		if address == "" {
			return nil, fmt.Errorf("%s: no reachable SSH address", host.Hostname)
		}
		return sshx.Dial(ctx, host.Hostname, address, sshx.Options{
			Username:    sshUsername(host.DeviceType),
			Credentials: creds,
		})
	}
}

func printUpdatePlan(o *Options, plan *lifecycle.Plan, gate writeGate) {
	mode := "LIVE RUN (--yes given: this device will be changed and rebooted)"
	switch {
	case gate.dryRun:
		mode = "DRY RUN (nothing will be changed)"
	case gate.prompt:
		mode = "LIVE RUN (you will be asked to confirm before this device is changed)"
	}

	o.printf("device:          %s\n", plan.Hostname)
	o.printf("running version: %s\n", plan.CurrentVersion)
	o.printf("target version:  %s\n", plan.TargetVersion)
	o.printf("mode:            %s\n", mode)

	if plan.AlreadyCurrent {
		o.printf("\n%s is already running the latest image; nothing to do.\n", plan.Hostname)
		return
	}

	o.printf("\nredundancy check (other devices probed in parallel):\n")
	o.printf("  alive spines: %s\n", listOrNone(plan.Redundancy.AliveSpines))
	o.printf("  alive leaves: %s\n", listOrNone(plan.Redundancy.AliveLeaves))
	if len(plan.Redundancy.Unreachable) > 0 {
		o.printf("  unreachable:  %s\n", listOrNone(plan.Redundancy.Unreachable))
	}
	if plan.RedundancyErr != nil {
		o.printf("  RESULT:       REFUSED — %v\n", plan.RedundancyErr)
	} else {
		o.printf("  RESULT:       ok, the fleet stays redundant across this reboot\n")
	}

	o.printf("\nplan:\n")
	for i, step := range plan.Steps {
		o.printf("  %d. %s\n", i+1, step)
	}
}

func listOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}

// -- device cleanup ---------------------------------------------------

func newDeviceCleanupCmd(o *Options) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "cleanup <host...|all>",
		Short: "Remove system images that are neither running nor default boot (DRY RUN by default)",
		Long: "cleanup deletes the old system images left behind by past updates:\n" +
			"everything that is neither the running image nor the default boot one.\n\n" +
			"Without --yes it lists what it would delete and exits having changed\n" +
			"nothing.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceCleanup(cmd.Context(), o, args, yes)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "actually delete the images (without this, nothing is deleted)")
	return cmd
}

func runDeviceCleanup(ctx context.Context, o *Options, targets []string, yes bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	net, hosts, err := o.loadTargets(targets)
	if err != nil {
		return err
	}
	hosts = filterHosts(hosts, func(h *model.Host) bool {
		return strings.EqualFold(h.DeviceType, "vyos")
	})
	if len(hosts) == 0 {
		return errors.New("no devices with cleanup support selected (vyos only)")
	}

	key, err := newVyOSAPIKey()
	if err != nil {
		return fmt.Errorf("resolving the VyOS API key: %w", err)
	}

	if !yes {
		o.printf("DRY RUN (nothing will be deleted; re-run with --yes to apply)\n\n")
	}

	rows := make([][]string, 0, len(hosts))
	failed := false
	for _, host := range hosts {
		result, err := cleanupHost(ctx, o, host, net, key, yes)
		if err != nil {
			rows = append(rows, []string{host.Hostname, "failed: " + err.Error()})
			failed = true
			continue
		}
		rows = append(rows, []string{host.Hostname, result})
	}

	o.printf("\n")
	printTable(o.Out, []string{"DEVICE", "RESULT"}, rows)
	if failed {
		return errors.New("one or more devices failed")
	}
	return nil
}

func cleanupHost(ctx context.Context, o *Options, host *model.Host, n *model.Network, key string, yes bool) (string, error) {
	address := probeEndpoint(ctx, host, n.Global.SearchDomain)
	if address == "" {
		return "", errors.New("no reachable address")
	}
	api, err := lifecycle.NewAPIClient(host.Hostname, lifecycle.APIOptions{
		Address:            address,
		Key:                key,
		InsecureSkipVerify: true,
		BaseURL:            apiBaseURL(host.Hostname),
		AllowWrites:        yes,
	})
	if err != nil {
		return "", err
	}

	plan, err := lifecycle.BuildCleanupPlan(ctx, host.Hostname, api)
	if err != nil {
		return "", err
	}
	for _, image := range plan.Keep {
		o.printf("[%s] keeping %s (%s)\n", host.Hostname, image.Name, imageWhy(image))
	}
	if plan.Empty() {
		return "nothing to do", nil
	}
	for _, image := range plan.Delete {
		verb := "would delete"
		if yes {
			verb = "deleting"
		}
		o.printf("[%s] %s image %s\n", host.Hostname, verb, image.Name)
	}
	if !yes {
		return fmt.Sprintf("would delete %d image(s)", len(plan.Delete)), nil
	}

	actions, err := lifecycle.ExecuteCleanup(ctx, plan, api, lifecycle.Options{AllowWrites: true, Out: o.Out})
	if err != nil {
		return "", err
	}
	return strings.Join(actions, "; "), nil
}

func imageWhy(image lifecycle.SystemImage) string {
	switch {
	case image.Running && image.DefaultBoot:
		return "running, default boot"
	case image.Running:
		return "running"
	default:
		return "default boot"
	}
}
