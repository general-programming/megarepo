package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
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
//   - Dry run is the DEFAULT. Without --yes, `update` and `cleanup`
//     print the full plan (what is running, what would be installed,
//     what the redundancy check found, every step) and exit having
//     changed nothing.
//   - --yes enables the writes for THAT plan.
//   - --force, separately, is the only way past a redundancy refusal.
//     --yes never implies it.
//   - `update` takes exactly one hostname. "all" is rejected, so one
//     invocation can never reboot two devices.

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

// apiBaseURLOverride redirects every device API request at a test server.
// It is empty in production: the real path always builds
// https://<probed address>:443 from the host being operated on.
var apiBaseURLOverride string

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
			"Both are dry-run by default: they print exactly what they would do\n" +
			"and exit. --yes performs it; --force is required, separately, to\n" +
			"override a redundancy refusal.",
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
}

func newDeviceUpdateCmd(o *Options) *cobra.Command {
	var f updateFlags

	cmd := &cobra.Command{
		Use:   "update <host>",
		Short: "Install the latest image on a device and reboot into it (DRY RUN by default)",
		Long: "update installs a newer image on ONE device, drains its BGP sessions,\n" +
			"reboots it, waits for it to come back and verifies routing recovered.\n\n" +
			"Without --yes this is a dry run: it prints the running version, the\n" +
			"target version, the fleet redundancy check and every step it would\n" +
			"take, then exits having changed nothing.\n\n" +
			"The redundancy check refuses to reboot the last live spine or the last\n" +
			"live leaf. That refusal is a hard error: --yes does not override it,\n" +
			"only --force does.\n\n" +
			"Exactly one hostname is accepted; \"all\" is rejected. One invocation\n" +
			"never reboots more than one device.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeviceUpdate(cmd.Context(), o, args[0], f)
		},
	}
	flags := cmd.Flags()
	flags.DurationVar(&f.drainWait, "drain-wait", lifecycle.DefaultDrainWait,
		"how long to wait after the BGP shutdown before rebooting")
	flags.BoolVar(&f.yes, "yes", false,
		"actually perform the update (without this, nothing is changed)")
	flags.BoolVar(&f.force, "force", false,
		"override a redundancy refusal (rebooting the last live spine/leaf). Requires --yes")
	flags.StringVar(&f.imageURL, "image-url", "",
		"install this already-published image URL instead of the mirrored latest release")
	flags.StringVar(&f.targetVersion, "target-version", "",
		"the version --image-url contains (default: derived from the URL)")
	return cmd
}

func runDeviceUpdate(ctx context.Context, o *Options, target string, f updateFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if target == "all" {
		return errors.New(`device update takes a single hostname: "all" is rejected so one run cannot reboot the whole fleet`)
	}

	net, hosts, err := o.loadTargets([]string{target})
	if err != nil {
		return err
	}
	host := hosts[0]
	if !strings.EqualFold(host.DeviceType, "vyos") {
		return fmt.Errorf("%s: device update only supports vyos devices (this one is %q)",
			host.Hostname, host.DeviceType)
	}

	key, err := newVyOSAPIKey()
	if err != nil {
		return fmt.Errorf("resolving the VyOS API key: %w", err)
	}

	address := probeEndpoint(ctx, host, net.Global.SearchDomain)
	if address == "" {
		return fmt.Errorf("%s: no reachable address", host.Hostname)
	}

	// The API client only gets its write opt-in when the run is confirmed.
	// A dry run therefore cannot delete an image even if a caller asked.
	api, err := lifecycle.NewAPIClient(host.Hostname, lifecycle.APIOptions{
		Address:            address,
		Key:                key,
		InsecureSkipVerify: true,
		BaseURL:            apiBaseURLOverride,
		AllowWrites:        f.yes,
	})
	if err != nil {
		return err
	}

	networkPath, err := o.networkPath()
	if err != nil {
		return err
	}
	provider, mirror, err := imageSource(host.DeviceType, networkPath, f)
	if err != nil {
		return err
	}

	credentials, err := newSupertechCredentials()
	if err != nil {
		return fmt.Errorf("resolving the shared SSH account: %w", err)
	}

	updater := &lifecycle.Updater{
		Host:      host,
		Fleet:     fleetFor(net, host),
		API:       api,
		Provider:  provider,
		Mirror:    mirror,
		Probe:     fleetProbe(net, key),
		SSH:       sshDialer(ctx, host, net, credentials),
		DrainWait: f.drainWait,
		Opts: lifecycle.Options{
			AllowWrites: f.yes,
			ForceUnsafe: f.force && f.yes,
			Out:         o.Out,
		},
	}

	plan, err := updater.BuildPlan(ctx)
	if err != nil {
		return err
	}
	printUpdatePlan(o, plan, f)

	if plan.AlreadyCurrent {
		return nil
	}
	if !f.yes {
		o.printf("\nDRY RUN: nothing was changed. Re-run with --yes to perform this update.\n")
		if plan.RedundancyErr != nil {
			o.printf("         --yes alone is NOT enough here: the redundancy check refuses.\n")
		}
		return nil
	}
	if plan.RedundancyErr != nil && !f.force {
		return fmt.Errorf("%w\n"+
			"       This is a hard safety refusal and --yes does not override it.\n"+
			"       Re-run with --force ONLY if you have confirmed out of band that\n"+
			"       taking %s down right now will not black-hole traffic.",
			plan.RedundancyErr, plan.Hostname)
	}

	result, err := updater.Execute(ctx, plan)
	if err != nil {
		return err
	}
	o.printf("\n[%s] %s\n", plan.Hostname, result)
	return nil
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
			BaseURL:            apiBaseURLOverride,
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

func printUpdatePlan(o *Options, plan *lifecycle.Plan, f updateFlags) {
	mode := "DRY RUN (nothing will be changed)"
	if f.yes {
		mode = "LIVE RUN (--yes given: this device will be changed and rebooted)"
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
		BaseURL:            apiBaseURLOverride,
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
