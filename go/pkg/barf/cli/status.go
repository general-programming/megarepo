package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/tui"
	"github.com/spf13/cobra"
)

// maxProbes is how many devices are contacted at once, matching the
// Python ThreadPoolExecutor(max_workers=8).
const maxProbes = 8

// runBounded calls fn(0..n-1) on at most maxProbes goroutines.
//
// Sizing the fan-out by the number of selected devices (one goroutine per
// host, all parked on a semaphore) means a 300-device run spawns 300
// goroutines that cannot be reclaimed until they are admitted. A fixed
// pool bounds the goroutine count, not just the in-flight network calls.
// Cancellation is handled by fn itself, which returns a filled-in
// "cancelled" result rather than leaving a hole in the output.
func runBounded(n int, fn func(i int)) {
	workers := min(n, maxProbes)
	if workers <= 0 {
		return
	}
	indices := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range indices {
				fn(i)
			}
		}()
	}
	for i := range n {
		indices <- i
	}
	close(indices)
	wg.Wait()
}

// acquire takes a semaphore slot, giving up if ctx is cancelled first.
// A plain `sem <- struct{}{}` cannot be interrupted, so on Ctrl-C the
// queued probes stay parked and keep contacting devices as slots free up
// while the command is already printing its summary.
func acquire(ctx context.Context, sem chan struct{}) bool {
	select {
	case sem <- struct{}{}:
		if ctx.Err() != nil {
			// Cancelled while we were being admitted: hand the slot back
			// rather than holding it for a probe that will not run.
			<-sem
			return false
		}
		return true
	case <-ctx.Done():
		return false
	}
}

func newStatusCmd(o *Options) *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "status [hosts...]",
		Short: "Endpoint, uptime, version and config drift for managed devices",
		Long: "status probes every selected device and reports what it is running\n" +
			"and whether its running config still matches what network.yml renders.\n" +
			"On a terminal the table appears immediately and fills in as devices\n" +
			"answer; with --plain or no TTY it prints once, sorted as given.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(cmd.Context(), o, args, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a table (implies --plain)")
	return cmd
}

// statusJSON is one device in `barf status --json` output.
type statusJSON struct {
	Device           string `json:"device"`
	Endpoint         string `json:"endpoint"`
	Model            string `json:"model"`
	Uptime           string `json:"uptime"`
	Version          string `json:"version"`
	LatestFirmware   string `json:"latest_firmware"`
	ConfigConsistent string `json:"config_consistent"`
	Status           string `json:"status"`
	State            string `json:"state"`
}

func runStatus(ctx context.Context, o *Options, targets []string, jsonOut bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	log := o.Logger()

	net, hosts, err := o.loadTargets(targets)
	if err != nil {
		return err
	}
	hosts = filterHosts(hosts, func(h *model.Host) bool { return reportsStatus(h.DeviceType) })
	if len(hosts) == 0 {
		return fmt.Errorf("no status-reporting devices selected")
	}

	// Fail fast on secret-backend problems in the main goroutine, the
	// way the Python implementation primes VaultSecrets before probing.
	secrets, err := newSecrets()
	if err != nil {
		return fmt.Errorf("secret backend unavailable: %w", err)
	}

	// Render up front and serially: templating may create missing
	// secrets, which must not race across probes.
	rendered := make(map[string]string, len(hosts))
	renderErrs := make(map[string]error, len(hosts))
	for _, h := range hosts {
		text, err := renderHost(h, net, secrets)
		if err != nil {
			log.Debug("render failed", "host", h.Hostname, "err", err)
			renderErrs[h.Hostname] = err
			continue
		}
		rendered[h.Hostname] = text
	}

	sem := make(chan struct{}, maxProbes)
	probes := make([]tui.StatusProbe, len(hosts))
	for i, h := range hosts {
		host := h
		probe := statusProbe(o, net, host, rendered[host.Hostname], renderErrs[host.Hostname], secrets)
		probes[i] = tui.StatusProbe{
			Device: host.Hostname,
			Run: func(ctx context.Context) tui.StatusRow {
				if !acquire(ctx, sem) {
					return errorRow(host.Hostname, "-", "cancelled")
				}
				defer func() { <-sem }()
				return probe(ctx)
			},
		}
	}

	if o.interactive() && !jsonOut {
		// The live table is the output; it stays on screen on exit.
		_, err := tui.RunStatus(ctx, probes)
		return err
	}

	rows := runProbesPlain(ctx, probes)
	// LATEST FIRMWARE: one release lookup for the whole table, "?" when
	// the vendor has no image provider or the feed is unreachable.
	firmware := firmwareCells(ctx, log, hosts, rows)

	if jsonOut {
		out := make([]statusJSON, len(rows))
		for i, r := range rows {
			out[i] = statusJSON{
				Device: r.Device, Endpoint: r.Endpoint, Model: r.Model,
				Uptime: r.Uptime, Version: r.Version,
				LatestFirmware:   firmware[r.Device],
				ConfigConsistent: r.Consistent, Status: r.Status,
				State: r.State.String(),
			}
		}
		enc := json.NewEncoder(o.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = spliceFirmwareCell(r.Cells(), firmware[r.Device])
	}
	printTable(o.Out, statusColumnsWithFirmware(), cells)
	return nil
}

// runProbesPlain runs every probe concurrently and returns the rows in
// the order the devices were selected, so output is deterministic.
func runProbesPlain(ctx context.Context, probes []tui.StatusProbe) []tui.StatusRow {
	rows := make([]tui.StatusRow, len(probes))
	runBounded(len(probes), func(i int) {
		rows[i] = probes[i].Run(ctx)
	})
	return rows
}

// statusProbe builds the probe for one host: reach it, read its status,
// and compare its running config against the rendered one. Failures land
// in the row rather than aborting the command.
func statusProbe(o *Options, net *model.Network, h *model.Host, rendered string, renderErr error, secrets SecretSource) func(context.Context) tui.StatusRow {
	log := o.Logger()

	return func(ctx context.Context) tui.StatusRow {
		row := tui.StatusRow{Device: h.Hostname, State: tui.StateOK}

		address := probeEndpoint(ctx, h, net.Global.SearchDomain)
		if address == "" {
			return errorRow(h.Hostname, "-", "no reachable address")
		}
		row.Endpoint = address

		reader, err := newReader(h, address, secrets)
		if err != nil {
			return errorRow(h.Hostname, address, err.Error())
		}

		st, err := reader.Status(ctx)
		if err != nil {
			log.Debug("status probe failed", "host", h.Hostname, "endpoint", address, "err", err)
			return errorRow(h.Hostname, address, err.Error())
		}
		row.Version = orUnknown(st.Version)
		row.Uptime = orUnknown(st.Uptime)
		row.Model = orUnknown(st.Model)
		row.Status = "ok"

		row.Consistent, row.State = consistencyCell(ctx, reader, h, net, rendered, renderErr, secrets)
		return row
	}
}

// consistencyCell is the CONFIG CONSISTENT cell plus the row's state.
func consistencyCell(
	ctx context.Context,
	reader DeviceReader,
	h *model.Host,
	net *model.Network,
	rendered string,
	renderErr error,
	secrets SecretSource,
) (string, tui.RowState) {
	if renderErr != nil {
		return "render error: " + renderErr.Error(), tui.StateError
	}
	// Same comparison `diff` and `deploy` use — see compare.go. Using a
	// plain line diff here made VyOS rows report hundreds of phantom
	// changes on devices the other two commands called clean.
	diff, err := compareConfig(ctx, reader, h, net, rendered, secrets, DiffOptions{})
	if err != nil {
		return "error: " + err.Error(), tui.StateError
	}
	if !diff.HasChanges {
		return "yes", tui.StateOK
	}
	return diff.Summary, tui.StateDrift
}

// errorRow is a device that could not be probed: dashes across, with the
// reason in STATUS, exactly like the Python table.
func errorRow(device, endpoint, reason string) tui.StatusRow {
	return tui.StatusRow{
		Device:     device,
		Endpoint:   endpoint,
		Model:      "-",
		Uptime:     "-",
		Version:    "-",
		Consistent: "-",
		Status:     "error: " + reason,
		State:      tui.StateError,
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
