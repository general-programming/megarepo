package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// The confirmation semantics shared by the only two commands that can
// change a device: `barf deploy` and `barf device update`.
//
// Both commands answer one question the same way — "may this run change
// THIS device?" — and the answer depends on exactly two inputs, whether
// stdout is a terminal and whether --yes was given:
//
//	terminal, no --yes   show the change, then ask [y/N] per device. The
//	                     prompt IS the confirmation: answering y performs
//	                     the change in this same invocation. Empty input,
//	                     anything that is not y/yes, and EOF all skip.
//	terminal, --yes      no prompt, for someone scripting on a terminal.
//	no terminal/--plain  never prompt: an unattended run must not hang on
//	                     a question nobody will answer. --yes is
//	                     therefore REQUIRED to change anything, and
//	                     without it the run is a dry run.
//
// The rule that must never be weakened is the last one: no TTY and no
// --yes changes nothing, ever.

// writeGate decides, per device, whether a run may proceed.
type writeGate struct {
	// dryRun means nothing may be changed at all: no TTY to ask on and
	// no --yes. allows always answers false.
	dryRun bool
	// prompt means each device is confirmed interactively.
	prompt bool

	confirm func(question string) (bool, error)
}

// newWriteGate builds the gate for a run. in is where confirmations are
// read from; nil means os.Stdin.
func newWriteGate(o *Options, yes bool, in io.Reader) writeGate {
	switch {
	case yes:
		// Permission was given on the command line, terminal or not.
		return writeGate{}
	case o.interactive():
		return writeGate{prompt: true, confirm: newConfirmer(o, in)}
	default:
		return writeGate{dryRun: true}
	}
}

// allows reports whether the device this question is about may be
// changed. A dry run answers false without asking anything.
func (g writeGate) allows(question string) (bool, error) {
	if g.dryRun {
		return false, nil
	}
	if !g.prompt {
		return true, nil
	}
	return g.confirm(question)
}

// newConfirmer returns a yes/no prompt reading from in (os.Stdin when
// nil). Only a gate in prompt mode ever calls it; a plain or piped run
// never constructs one.
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

// confirmError wraps a failure to read the operator's answer.
func confirmError(err error) error {
	return fmt.Errorf("reading confirmation: %w", err)
}
