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
)

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
// commit.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrWritesNotAllowed) || errors.Is(err, ErrWriteAttempt)
}
