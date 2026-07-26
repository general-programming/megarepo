package device

import (
	"errors"
	"fmt"
)

// The refusal errors, together because they answer one safety-critical
// question: did barf refuse to change the device, or did a change fail
// partway? Each is a sentinel `ErrX` plus a typed `XError` unwrapping to it.
var (
	// ErrWritesNotAllowed means writes were never enabled (a dry run), so
	// NOTHING reached the device. lifecycle.ErrWritesNotAllowed is this same
	// sentinel, so errors.Is works across both packages.
	ErrWritesNotAllowed = errors.New("refusing to modify the device: writes are not enabled (dry run)")

	// ErrWriteAttempt means a closed allowlist refused an operation that
	// could change the device, so NOTHING reached it. Unlike
	// ErrWritesNotAllowed this is a wiring bug: no option enables it.
	ErrWriteAttempt = errors.New("device: refusing an operation that could change the device")

	// ErrUnsupported is returned for devicetypes with no read transport.
	ErrUnsupported = errors.New("unsupported devicetype")

	// ErrPreSend means the failure happened before any request reached the
	// device: resolving a secret (the VyOS API key, EOS credentials),
	// resolving an endpoint, or an op-shape guard (an empty delete path, an
	// EOS-shaped Op sent to the VyOS writer). Nothing could have been
	// applied, so a caller distinguishing "maybe applied" from "definitely
	// not" (cli's writerAdapter.Configure) must not treat this like an
	// in-flight failure.
	ErrPreSend = errors.New("device: refused before any request reached the device")
)

// PreSendError wraps a failure that happened before a request was sent.
type PreSendError struct {
	What string
	Err  error
}

func (e *PreSendError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("device: %s: %v", e.What, e.Err)
	}
	return fmt.Sprintf("device: %s", e.What)
}

// Unwrap exposes both ErrPreSend (so errors.Is(err, ErrPreSend) works) and
// the underlying cause, if any (so errors.Is/As still reach it).
func (e *PreSendError) Unwrap() []error {
	if e.Err != nil {
		return []error{ErrPreSend, e.Err}
	}
	return []error{ErrPreSend}
}

// WritesNotAllowedError guards accidental construction of a writer, where
// WriteAttemptError guards the read transports.
type WritesNotAllowedError struct {
	What string
}

func (e *WritesNotAllowedError) Error() string {
	return fmt.Sprintf("device: refusing to build %s: writes require Options.AllowWrites", e.What)
}

func (e *WritesNotAllowedError) Unwrap() error { return ErrWritesNotAllowed }

// WriteAttemptError is the structural guarantee that callers cannot talk a
// Reader into writing: an allowlist forbade the command or request.
type WriteAttemptError struct {
	What string
}

func (e *WriteAttemptError) Error() string {
	return fmt.Sprintf("device: refusing %s: this package is read-only", e.What)
}

func (e *WriteAttemptError) Unwrap() error { return ErrWriteAttempt }

// UnmanagedCommandError means a writer was handed a command outside the
// managed scope; deploying the EOS slice must not touch config barf does not
// own. Nothing was sent.
type UnmanagedCommandError struct {
	Command string
}

func (e *UnmanagedCommandError) Error() string {
	return fmt.Sprintf("device: refusing EOS command %q: outside the managed scope"+
		" (admin user, ssh-keys, enable password, eAPI block)", e.Command)
}

func (e *UnmanagedCommandError) Unwrap() error { return ErrWriteAttempt }

// IsRefusal reports whether err is any guard's refusal to change a device.
// The check that matters after a failed deploy: a refusal means no write
// reached the device, while any other error may have left a half-applied
// commit. ErrPreSend is the broader case: not a closed-allowlist refusal
// specifically, but still provably pre-send (a secret/endpoint lookup or an
// op-shape guard failed before any bytes went to the device).
func IsRefusal(err error) bool {
	return errors.Is(err, ErrWritesNotAllowed) || errors.Is(err, ErrWriteAttempt) || errors.Is(err, ErrPreSend)
}
