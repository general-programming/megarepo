// Package cli is the barf command surface: a cobra tree over the model,
// render and device packages. Every command is read-only; deploy and
// push are deliberately not implemented here.
//
// All commands are dual-mode. On a TTY they run a Bubble Tea interface
// (see barf/tui); with no TTY, or with --plain or --json, they emit
// deterministic, ANSI-free, pipe-safe text so CI and a future GitOps
// reconcile loop can consume them.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	charmlog "github.com/charmbracelet/log"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// defaultNetworkRelPath is where network.yml lives inside the megarepo.
const defaultNetworkRelPath = "projects/barf/network.yml"

// Options is the global CLI state shared by every subcommand.
type Options struct {
	// NetworkPath is --network; empty means "discover it".
	NetworkPath string
	// Plain forces plain output even on a TTY.
	Plain bool
	// Verbose turns on debug logging.
	Verbose bool

	Out    io.Writer
	ErrOut io.Writer

	// forceInteractive overrides TTY detection in tests. nil means
	// "detect".
	forceInteractive *bool

	log *charmlog.Logger
}

// NewOptions returns options wired to the process's stdout/stderr.
func NewOptions() *Options {
	return &Options{Out: os.Stdout, ErrOut: os.Stderr}
}

// interactive reports whether a Bubble Tea interface should be used:
// only when stdout is a real terminal and the user did not ask for
// machine-readable output.
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

// SetInteractive pins interactivity, bypassing TTY detection. Tests use
// it to exercise both modes without a pty.
func (o *Options) SetInteractive(v bool) { o.forceInteractive = &v }

// Logger is the command logger. It writes to stderr so it never
// pollutes a piped stdout.
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

// printf writes to the command's stdout.
func (o *Options) printf(format string, args ...any) {
	fmt.Fprintf(o.Out, format, args...)
}

// networkPath is the network.yml to load: --network when given,
// otherwise projects/barf/network.yml found by walking up from the
// working directory (so barf works from anywhere in the megarepo),
// falling back to ./network.yml.
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
		Short: "Read-only network config tooling",
		Long: "barf renders device configs from network.yml and compares them\n" +
			"against what the fleet is actually running. Every command here is\n" +
			"read-only: nothing is ever pushed to a device.",
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
	)
	return root
}

// Execute runs the barf CLI and returns the process exit status.
func Execute(args []string) int {
	o := NewOptions()
	root := NewRootCmd(o)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		fmt.Fprintf(o.ErrOut, "barf: %v\n", err)
		return 1
	}
	return 0
}
