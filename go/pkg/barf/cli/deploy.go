package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/spf13/cobra"
)

// `barf deploy` writes to devices only after a per-device confirmation, and
// the writer is not constructed before then. A devicetype with no write
// implementation is refused before anything runs rather than half-deploying
// the fleet, and output is redacted unless --show-secrets. See confirm.go for
// the TTY/--yes matrix, which `barf device update` shares.

// DeployOptions are `barf deploy`'s own flags.
type DeployOptions struct {
	// Yes skips the per-device prompt; with no terminal it is what enables
	// writing at all.
	Yes         bool
	ShowSecrets bool
	// SkipSave leaves the config applied but not surviving a reboot.
	SkipSave bool

	// In is where confirmations are read from; nil means os.Stdin.
	In io.Reader
}

func newDeployCmd(o *Options) *cobra.Command {
	var opts DeployOptions

	cmd := &cobra.Command{
		Use:   "deploy [hosts...]",
		Short: "Apply rendered configs to devices (asks first; dry run when not on a terminal)",
		Long: "deploy renders each selected host, diffs it against the config the\n" +
			"device is actually running, and applies the difference.\n" +
			"\n" +
			"On a terminal it prints each device's change and asks [y/N] before\n" +
			"applying it; answering y applies it right there, with no second\n" +
			"invocation. --yes skips the prompt.\n" +
			"\n" +
			"With --plain, or no terminal, it never prompts: --yes is required to\n" +
			"change anything, and without it the run is a DRY RUN that prints the\n" +
			"exact operations it would send and exits.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDeploy(cmd.Context(), o, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.Yes, "yes", false,
		"skip the per-device confirmation (required to write anything when there is no terminal)")
	cmd.Flags().BoolVar(&opts.ShowSecrets, "show-secrets", false, "do not redact secret values in the output")
	cmd.Flags().BoolVar(&opts.SkipSave, "skip-save", false, "do not persist the config to boot after committing")
	return cmd
}

// deployPlan is one host's computed change set.
type deployPlan struct {
	host    *model.Host
	address string
	diff    ConfigDiff
	ops     []ConfigOp
	err     error
}

func runDeploy(ctx context.Context, o *Options, targets []string, opts DeployOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	net, hosts, err := o.loadTargets(targets)
	if err != nil {
		return err
	}
	hosts = filterHosts(hosts, func(h *model.Host) bool { return isTemplatable(h.DeviceType) })
	if len(hosts) == 0 {
		return fmt.Errorf("no templatable devices selected")
	}

	// Refuse the whole run rather than deploying part of the fleet.
	if err := checkDeployable(hosts); err != nil {
		return err
	}

	secrets, err := newSecrets()
	if err != nil {
		return fmt.Errorf("secret backend unavailable: %w", err)
	}

	// Render serially: templating can mint secrets, and that must not race.
	rendered := make(map[string]string, len(hosts))
	renderErrs := make(map[string]error, len(hosts))
	for _, h := range hosts {
		text, err := renderHost(h, net, secrets)
		if err != nil {
			renderErrs[h.Hostname] = err
			continue
		}
		rendered[h.Hostname] = text
	}

	plans := computeDeployPlans(ctx, o, net, hosts, rendered, renderErrs, secrets,
		DiffOptions{ShowSecrets: opts.ShowSecrets})

	return applyDeployPlans(ctx, o, plans, opts)
}

func checkDeployable(hosts []*model.Host) error {
	missing := map[string]bool{}
	for _, h := range hosts {
		if !hasWriter(h.DeviceType) {
			missing[h.DeviceType] = true
		}
	}
	if len(missing) == 0 {
		return nil
	}
	types := make([]string, 0, len(missing))
	for name := range missing {
		types = append(types, name)
	}
	return fmt.Errorf("cannot deploy devicetype(s) %v: no write implementation (deployable: %v); "+
		"select only deployable hosts, or use `barf diff` to inspect them",
		types, deployableDeviceTypes())
}

// computeDeployPlans reads every device and computes its change set. This
// phase is entirely read-only, and runs concurrently.
func computeDeployPlans(ctx context.Context, o *Options, net *model.Network, hosts []*model.Host,
	rendered map[string]string, renderErrs map[string]error,
	secrets SecretSource, diffOpts DiffOptions) []deployPlan {

	plans := make([]deployPlan, len(hosts))
	sem := make(chan struct{}, maxProbes)

	runBounded(len(hosts), func(i int) {
		h := hosts[i]
		if !acquire(ctx, sem) {
			plans[i] = deployPlan{host: h, err: context.Cause(ctx)}
			return
		}
		defer func() { <-sem }()
		plans[i] = planDeploy(ctx, o, net, h, rendered[h.Hostname], renderErrs[h.Hostname], secrets, diffOpts)
	})
	return plans
}

func planDeploy(ctx context.Context, o *Options, net *model.Network, h *model.Host,
	rendered string, renderErr error, secrets SecretSource, diffOpts DiffOptions) deployPlan {

	plan := deployPlan{host: h}
	if err := ctx.Err(); err != nil {
		// Cancelled between admission and here: no fresh device read.
		plan.err = err
		return plan
	}
	if renderErr != nil {
		plan.err = fmt.Errorf("render: %w", renderErr)
		return plan
	}

	plan.address = probeEndpoint(ctx, h, net.Global.SearchDomain)
	if plan.address == "" {
		plan.err = errors.New("no reachable address")
		return plan
	}

	reader, err := newReader(h, plan.address, secrets)
	if err != nil {
		plan.err = err
		return plan
	}
	running, err := reader.RunningConfig(ctx)
	if err != nil {
		o.Logger().Debug("running config fetch failed", "host", h.Hostname, "err", err)
		plan.err = err
		return plan
	}

	switch h.DeviceType {
	case "vyos":
		vyos, err := planVyOS(rendered, running)
		if err != nil {
			plan.err = err
			return plan
		}
		plan.diff = vyos.configDiff(diffOpts)
		plan.ops = vyos.ops()
	default:
		// checkDeployable rejected writerless devicetypes, so reaching here
		// means a writer exists but no planner does.
		plan.err = fmt.Errorf("no deploy planner for devicetype %q", h.DeviceType)
	}
	return plan
}

// applyDeployPlans prints every plan and, when permitted, applies it.
func applyDeployPlans(ctx context.Context, o *Options, plans []deployPlan, opts DeployOptions) error {
	gate := newWriteGate(o, opts.Yes, opts.In)

	rows := make([][]string, 0, len(plans))
	var failed, changed bool

	for _, plan := range plans {
		name := plan.host.Hostname

		// An interrupted run stops here; the rest are unwritten.
		if err := ctx.Err(); err != nil {
			failed = true
			rows = append(rows, []string{name, "cancelled"})
			continue
		}

		if plan.err != nil {
			failed = true
			o.printf("--- %s ---\nfailed: %v\n\n", name, plan.err)
			rows = append(rows, []string{name, "failed: " + plan.err.Error()})
			continue
		}
		if !plan.diff.HasChanges {
			rows = append(rows, []string{name, "no changes"})
			continue
		}
		changed = true

		o.printf("--- %s (%s) ---\n%s\n\n", name, plan.diff.Summary, plan.diff.Text)
		o.printf("%d operation(s) would be sent to %s:\n", len(plan.ops), name)
		for _, line := range describeOps(plan.ops, DiffOptions{ShowSecrets: opts.ShowSecrets}) {
			o.printf("  %s\n", line)
		}
		o.printf("\n")

		ok, err := gate.allows(fmt.Sprintf("apply %s to %s?", plan.diff.Summary, name))
		if err != nil {
			return confirmError(err)
		}
		if !ok {
			if gate.dryRun {
				rows = append(rows, []string{name, plan.diff.Summary + " (dry run)"})
				continue
			}
			o.printf("skipped %s\n\n", name)
			rows = append(rows, []string{name, plan.diff.Summary + " (skipped)"})
			continue
		}

		if err := pushPlan(ctx, plan, opts); err != nil {
			failed = true
			o.printf("%s: deploy failed: %v\n\n", name, err)
			rows = append(rows, []string{name, "failed: " + err.Error()})
			continue
		}
		rows = append(rows, []string{name, plan.diff.Summary + " applied"})
	}

	printTable(o.Out, []string{"DEVICE", "DEPLOY"}, rows)

	if gate.dryRun && changed {
		o.printf("\nDRY RUN: nothing was written. Re-run with --yes to apply.\n")
	}
	if failed {
		return errors.New("one or more devices could not be deployed")
	}
	return nil
}

// pushPlan is the one function in this package that writes to a device. The
// writer is built at the last possible moment, after the plan is confirmed.
func pushPlan(ctx context.Context, plan deployPlan, opts DeployOptions) error {
	if len(plan.ops) == 0 {
		return nil
	}
	secrets, err := newSecrets()
	if err != nil {
		return fmt.Errorf("secret backend unavailable: %w", err)
	}
	writer, err := newWriterFor(plan.host, plan.address, secrets)
	if err != nil {
		return err
	}
	if err := writer.Configure(ctx, plan.ops); err != nil {
		return err
	}
	if opts.SkipSave {
		return nil
	}
	return writer.SaveConfig(ctx)
}
