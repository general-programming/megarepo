package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/tui"
	"github.com/spf13/cobra"
)

func newDiffCmd(o *Options) *cobra.Command {
	var opts DiffOptions

	cmd := &cobra.Command{
		Use:   "diff [hosts...]",
		Short: "Compare rendered configs against what devices are running",
		Long: "diff renders each selected host locally, fetches the config the\n" +
			"device is actually running, and shows the difference. It is strictly\n" +
			"read-only: nothing is ever written to a device.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd.Context(), o, args, opts)
		},
	}
	cmd.Flags().BoolVar(&opts.ShowDeviceOnly, "show-device-only", false, "also list config that only exists on the device")
	cmd.Flags().BoolVar(&opts.ShowSecrets, "show-secrets", false, "do not redact secret values in the diff output")
	return cmd
}

func runDiff(ctx context.Context, o *Options, targets []string, opts DiffOptions) error {
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

	secrets, err := newSecrets()
	if err != nil {
		return fmt.Errorf("secret backend unavailable: %w", err)
	}

	// Render serially for the same reason status does: templating can
	// mint secrets, and that must not race.
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

	sem := make(chan struct{}, maxProbes)
	jobs := make([]tui.DiffJob, len(hosts))
	for i, h := range hosts {
		host := h
		run := diffJob(o, net, host, rendered[host.Hostname], renderErrs[host.Hostname], secrets, opts)
		jobs[i] = tui.DiffJob{
			Device: host.Hostname,
			Run: func(ctx context.Context) tui.DiffOutcome {
				if !acquire(ctx, sem) {
					return tui.DiffOutcome{
						Device:  host.Hostname,
						Err:     ctx.Err(),
						Summary: "cancelled",
					}
				}
				defer func() { <-sem }()
				return run(ctx)
			},
		}
	}

	var outcomes []tui.DiffOutcome
	if o.interactive() {
		outcomes, err = tui.RunDiff(ctx, jobs)
		if err != nil {
			return err
		}
	} else {
		outcomes = runDiffJobsPlain(ctx, jobs)
		for _, out := range outcomes {
			if out.Err != nil || strings.TrimSpace(out.Text) == "" {
				continue
			}
			o.printf("--- %s ---\n%s\n\n", out.Device, out.Text)
		}
	}

	rows := make([][]string, 0, len(outcomes))
	failed := false
	for _, out := range outcomes {
		switch {
		case out.Err != nil:
			failed = true
			rows = append(rows, []string{out.Device, "failed: " + out.Err.Error()})
		default:
			rows = append(rows, []string{out.Device, out.Summary})
		}
	}
	printTable(o.Out, []string{"DEVICE", "DIFF"}, rows)

	if failed {
		return errors.New("one or more devices could not be diffed")
	}
	return nil
}

func runDiffJobsPlain(ctx context.Context, jobs []tui.DiffJob) []tui.DiffOutcome {
	outcomes := make([]tui.DiffOutcome, len(jobs))
	runBounded(len(jobs), func(i int) {
		out := jobs[i].Run(ctx)
		if out.Device == "" {
			out.Device = jobs[i].Device
		}
		outcomes[i] = out
	})
	return outcomes
}

// diffJob renders one host against its running config. Errors come back
// in the outcome so one unreachable device never hides the rest.
func diffJob(o *Options, net *model.Network, h *model.Host, rendered string, renderErr error, secrets SecretSource, opts DiffOptions) func(context.Context) tui.DiffOutcome {
	log := o.Logger()

	return func(ctx context.Context) tui.DiffOutcome {
		out := tui.DiffOutcome{Device: h.Hostname}
		if renderErr != nil {
			out.Err = fmt.Errorf("render: %w", renderErr)
			out.Summary = "failed: " + out.Err.Error()
			return out
		}

		address := probeEndpoint(ctx, h, net.Global.SearchDomain)
		if address == "" {
			out.Err = errors.New("no reachable address")
			out.Summary = "failed: no reachable address"
			return out
		}

		reader, err := newReader(h, address, secrets)
		if err != nil {
			out.Err = err
			out.Summary = "failed: " + err.Error()
			return out
		}

		// Vendor-appropriate comparison, shared with `status` and
		// `deploy` so the three can never disagree. See compare.go.
		d, err := compareConfig(ctx, reader, h, net, rendered, secrets, opts)
		if err != nil {
			log.Debug("config comparison failed", "host", h.Hostname, "err", err)
			out.Err = err
			out.Summary = "failed: " + err.Error()
			return out
		}
		out.Text = d.Text
		out.Summary = d.Summary
		return out
	}
}
