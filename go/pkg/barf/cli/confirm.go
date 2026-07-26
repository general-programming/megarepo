package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Confirmation semantics shared by `barf deploy` and `barf device update`.
// "May this run change THIS device?" depends on whether stdout is a terminal
// and whether --yes was given:
//
//	terminal, no --yes   show the change, then ask [y/N] per device; y
//	                     performs it in this same invocation. Anything
//	                     else, including EOF, skips.
//	terminal, --yes      no prompt.
//	no terminal/--plain  never prompt, so an unattended run cannot hang.
//	                     --yes is REQUIRED to change anything; without it
//	                     the run is a dry run.
//
// The last rule must never be weakened: no TTY and no --yes changes nothing.

// writeGate decides, per device, whether a run may proceed.
type writeGate struct {
	// dryRun (no TTY, no --yes) makes allows always answer false.
	dryRun bool
	// prompt means each device is confirmed interactively.
	prompt bool

	confirm func(question string) (bool, error)
}

// newWriteGate builds the gate for a run. in is where confirmations are read
// from; nil means os.Stdin.
func newWriteGate(o *Options, yes bool, in io.Reader) writeGate {
	switch {
	case yes:
		return writeGate{}
	case o.interactive():
		return writeGate{prompt: true, confirm: newConfirmer(o, in)}
	default:
		return writeGate{dryRun: true}
	}
}

// allows reports whether the device in question may be changed; a dry run
// answers false without asking anything.
func (g writeGate) allows(question string) (bool, error) {
	if g.dryRun {
		return false, nil
	}
	if !g.prompt {
		return true, nil
	}
	return g.confirm(question)
}

// newConfirmer returns a yes/no prompt reading from in (os.Stdin when nil).
// Only a gate in prompt mode ever constructs one.
func newConfirmer(o *Options, in io.Reader) func(question string) (bool, error) {
	if in == nil {
		in = os.Stdin
	}
	reader := bufio.NewReader(in)
	return func(question string) (bool, error) {
		o.printf("%s [y/N] ", question)
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			if errors.Is(err, io.EOF) {
				// No answer is not consent.
				o.printf("\n")
				return false, nil
			}
			return false, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes", nil
	}
}

func confirmError(err error) error {
	return fmt.Errorf("reading confirmation: %w", err)
}
