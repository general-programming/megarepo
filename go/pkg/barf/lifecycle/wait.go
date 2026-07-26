package lifecycle

import (
	"context"
	"fmt"
	"io"
	"time"
)

// WaitOptions tunes the post-reboot liveness poll; zero values mean 15s
// initial wait, 5s interval, 900s timeout.
type WaitOptions struct {
	InitialWait time.Duration
	Interval    time.Duration
	Timeout     time.Duration
	Out         io.Writer
	// now overrides the clock in tests.
	now func() time.Time
}

func (w WaitOptions) initialWait() time.Duration {
	if w.InitialWait > 0 {
		return w.InitialWait
	}
	return 15 * time.Second
}

func (w WaitOptions) interval() time.Duration {
	if w.Interval > 0 {
		return w.Interval
	}
	return 5 * time.Second
}

func (w WaitOptions) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return 15 * time.Minute
}

func (w WaitOptions) out() io.Writer {
	if w.Out == nil {
		return io.Discard
	}
	return w.Out
}

func (w WaitOptions) clock() func() time.Time {
	if w.now != nil {
		return w.now
	}
	return time.Now
}

// VersionFunc reports a device's running version; an error or a placeholder
// ("" / "-") means "not alive yet".
type VersionFunc func(ctx context.Context) (string, error)

// WaitForAlive blocks until a device answers, returning its version. A
// rebooting device can keep answering on the old image, so a poll reporting
// changedFrom (its pre-reboot version) does not count as alive. Read-only.
func WaitForAlive(ctx context.Context, hostname string, version VersionFunc, changedFrom string, opts WaitOptions) (string, error) {
	out := opts.out()
	now := opts.clock()

	// ~2 minutes is what a VyOS leaf reboot measures in practice.
	fmt.Fprintf(out, "[%s] waiting for the device to come alive (a reboot typically takes ~2 minutes)", hostname)

	started := now()
	if err := sleep(ctx, opts.initialWait()); err != nil {
		fmt.Fprintln(out)
		return "", err
	}

	deadline := started.Add(opts.timeout())
	for now().Before(deadline) {
		reported, err := version(ctx)
		if err == nil && reported != "" && reported != "-" && reported != changedFrom {
			fmt.Fprintln(out)
			fmt.Fprintf(out, "[%s] device came back after %s\n",
				hostname, HumanDuration(now().Sub(started)))
			return reported, nil
		}
		fmt.Fprint(out, ".")
		if err := sleep(ctx, opts.interval()); err != nil {
			fmt.Fprintln(out)
			return "", err
		}
	}

	fmt.Fprintln(out)
	return "", fmt.Errorf("%s did not come back within %s", hostname, opts.timeout())
}

// HumanDuration renders 95s as "1m35s" and 45s as "45s".
func HumanDuration(d time.Duration) string {
	seconds := int(d.Round(time.Second).Seconds())
	if seconds < 0 {
		seconds = 0
	}
	minutes, secs := seconds/60, seconds%60
	if minutes > 0 {
		return fmt.Sprintf("%dm%02ds", minutes, secs)
	}
	return fmt.Sprintf("%ds", secs)
}
