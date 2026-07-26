// Package cli is the barf command surface: a cobra tree over the model,
// render and device packages. Every command is read-only except `deploy` and
// `device update`/`device cleanup`, which change nothing until the operator
// confirms; see confirm.go for the matrix. All commands are dual-mode: Bubble
// Tea on a TTY, deterministic ANSI-free text otherwise or under --plain/--json.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	charmlog "charm.land/log/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// defaultNetworkRelPath is where network.yml lives inside the megarepo.
const defaultNetworkRelPath = "projects/barf/network.yml"

// Options is the global CLI state shared by every subcommand.
type Options struct {
	// NetworkPath is --network; empty means "discover it".
	NetworkPath string
	Plain       bool
	Verbose     bool

	Out    io.Writer
	ErrOut io.Writer

	// forceInteractive overrides TTY detection in tests; nil means detect.
	forceInteractive *bool

	log *charmlog.Logger
}

// NewOptions returns options wired to the process's stdout/stderr.
func NewOptions() *Options {
	return &Options{Out: os.Stdout, ErrOut: os.Stderr}
}

// interactive is true only when stdout is a real terminal and the user did
// not ask for machine-readable output.
func (o *Options) interactive() bool {
	if o.forceInteractive != nil {
		return *o.forceInteractive
	}
	if o.Plain {
		return false
	}
	f, ok := o.Out.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// SetInteractive pins interactivity, so tests exercise both modes without a pty.
func (o *Options) SetInteractive(v bool) { o.forceInteractive = &v }

// Logger writes to stderr so it never pollutes a piped stdout.
func (o *Options) Logger() *charmlog.Logger {
	if o.log == nil {
		o.log = charmlog.NewWithOptions(o.ErrOut, charmlog.Options{
			ReportTimestamp: true,
			Prefix:          "barf",
		})
	}
	level := charmlog.WarnLevel
	if o.Verbose {
		level = charmlog.DebugLevel
	}
	o.log.SetLevel(level)
	return o.log
}

func (o *Options) printf(format string, args ...any) {
	fmt.Fprintf(o.Out, format, args...)
}

// networkPath resolves --network, else projects/barf/network.yml found by
// walking up from the working directory, else ./network.yml.
func (o *Options) networkPath() (string, error) {
	if o.NetworkPath != "" {
		return o.NetworkPath, nil
	}
	if p, err := discoverNetworkPath(); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("could not find %s; pass --network", defaultNetworkRelPath)
}

func discoverNetworkPath() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, defaultNetworkRelPath)
		if fileExists(candidate) {
			return candidate, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if fileExists("network.yml") {
		return "network.yml", nil
	}
	return "", os.ErrNotExist
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// NewRootCmd builds the barf command tree.
func NewRootCmd(o *Options) *cobra.Command {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.ErrOut == nil {
		o.ErrOut = os.Stderr
	}

	root := &cobra.Command{
		Use:   "barf",
		Short: "Network config tooling",
		Long: "barf renders device configs from network.yml and compares them\n" +
			"against what the fleet is actually running. Every command is\n" +
			"read-only except `deploy` and `device update`/`device cleanup`, and\n" +
			"those confirm each device before changing it (or, with no terminal\n" +
			"to ask on, require --yes and are a dry run without it).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(o.Out)
	root.SetErr(o.ErrOut)

	pf := root.PersistentFlags()
	pf.StringVar(&o.NetworkPath, "network", "", "path to network.yml (default: "+defaultNetworkRelPath+" found from the repo root)")
	pf.BoolVar(&o.Plain, "plain", false, "force plain, ANSI-free output even on a terminal")
	pf.BoolVarP(&o.Verbose, "verbose", "v", false, "enable debug logging on stderr")

	root.AddCommand(
		newStatusCmd(o),
		newGenerateCmd(o),
		newDiffCmd(o),
		newListCmd(o),
		newValidateCmd(o),
		newDeployCmd(o),
		newDeviceCmd(o),
	)
	return root
}

// notifyContext is signal.NotifyContext; a var so tests can observe stop
// without sending real signals at the test binary.
var notifyContext = signal.NotifyContext

// signalContext returns a context cancelled by the first SIGINT/SIGTERM, then
// immediately restores the default disposition: NotifyContext otherwise keeps
// swallowing signals, leaving a second Ctrl-C unable to kill a slow cleanup.
func signalContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := notifyContext(parent, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ctx.Done()
		stop()
	}()
	return ctx, stop
}

// Execute runs the barf CLI and returns the process exit status. The
// signal-aware context it builds is what every cancel check below it hangs off.
func Execute(args []string) int {
	ctx, stop := signalContext(context.Background())
	defer stop()
	return ExecuteContext(ctx, args)
}

// ExecuteContext runs the barf CLI under a caller-supplied context.
func ExecuteContext(ctx context.Context, args []string) int {
	o := NewOptions()
	root := NewRootCmd(o)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(o.ErrOut, "barf: %v\n", err)
		return 1
	}
	return 0
}
