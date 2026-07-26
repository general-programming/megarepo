package device

import (
	"errors"
	"fmt"
)

// The refusal errors, in one place because they answer one
// safety-critical question: did barf refuse to change the device, or did
// a change fail partway?
//
// These used to be three unrelated shapes — a struct here, a struct in
// eos_writer.go, and a bare sentinel in package lifecycle — so
// `errors.Is(err, lifecycle.ErrWritesNotAllowed)` was silently false for
// a refusal raised in this package. A caller asking "was anything
// touched?" got the wrong answer depending on which package happened to
// refuse. They now follow the shape client/vault already used: an
// exported sentinel to compare against, plus a typed error carrying the
// detail and unwrapping to that sentinel.
//
// Sentinels are `ErrX`; the types carrying detail are `XError`. The old
// names had that backwards, which is how the two kinds got confused.
var (
	// ErrWritesNotAllowed means writes were never enabled (a dry run):
	// barf declined before building a request. NOTHING reached the
	// device. lifecycle.ErrWritesNotAllowed is this same sentinel, so
	// errors.Is works across both packages.
	ErrWritesNotAllowed = errors.New("refusing to modify the device: writes are not enabled (dry run)")

	// ErrWriteAttempt means a closed allowlist refused an operation that
	// could change the device — a read transport asked to write, or a
	// writer asked for an endpoint outside its own allowlist. NOTHING
	// reached the device. Unlike ErrWritesNotAllowed this signals a
	// wiring bug, not an operator choice: no option enables it.
	ErrWriteAttempt = errors.New("device: refusing an operation that could change the device")

	// ErrUnsupported is returned for devicetypes with no read transport.
	ErrUnsupported = errors.New("unsupported devicetype")
)

// WritesNotAllowedError is returned when a write transport is
// constructed without Options.AllowWrites. It is the counterpart of
// WriteAttemptError: that one guards the read transports, this one
// guards accidental construction of a write transport.
//
// Compare against ErrWritesNotAllowed with errors.Is.
type WritesNotAllowedError struct {
	What string
}

func (e *WritesNotAllowedError) Error() string {
	return fmt.Sprintf("device: refusing to build %s: writes require Options.AllowWrites", e.What)
}

func (e *WritesNotAllowedError) Unwrap() error { return ErrWritesNotAllowed }

// WriteAttemptError is returned when a command or request that could
// change the device is passed to a transport whose allowlist forbids it.
// It is the package's structural guarantee: callers cannot talk a Reader
// into writing.
//
// Compare against ErrWriteAttempt with errors.Is.
type WriteAttemptError struct {
	What string
}

func (e *WriteAttemptError) Error() string {
	return fmt.Sprintf("device: refusing %s: this package is read-only", e.What)
}

func (e *WriteAttemptError) Unwrap() error { return ErrWriteAttempt }

// UnmanagedCommandError is returned when a command outside the managed
// scope is handed to a writer. Deploying the EOS slice must not be able
// to touch config barf does not own.
//
// It is a refusal like the two above, and unwraps to ErrWriteAttempt for
// the same reason: a command outside the managed scope reaching a writer
// is a wiring bug, and nothing was sent.
type UnmanagedCommandError struct {
	Command string
}

func (e *UnmanagedCommandError) Error() string {
	return fmt.Sprintf("device: refusing EOS command %q: outside the managed scope"+
		" (admin user, ssh-keys, enable password, eAPI block)", e.Command)
}

func (e *UnmanagedCommandError) Unwrap() error { return ErrWriteAttempt }

// IsRefusal reports whether err is any refusal to change a device,
// whichever guard raised it and in whichever package.
//
// This is the check that matters after a failed deploy: a refusal means
// the device was never contacted for a write, so the operator can retry
// safely. Any other error may have left a half-applied commit.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrWritesNotAllowed) || errors.Is(err, ErrWriteAttempt)
}
