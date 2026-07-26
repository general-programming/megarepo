// Package lifecycle implements `barf device update` and `barf device
// cleanup`: image install, BGP drain, reboot, post-reboot routing
// verification, and old-image cleanup.
//
// The only barf package that can change a device, so writing is never
// implicit:
//
//   - Every operation splits into Plan (pure reads) and Execute (writes);
//     the CLI default is plan, print, stop.
//   - Execute refuses unless Options.AllowWrites.
//   - A SafeToReboot refusal is overridable only by Options.ForceUnsafe,
//     never by AllowWrites.
//   - An Updater reboots at most one device, ever.
//
// Ports barf/cli/device.py and barf/vendors/{__init__,vyos}.py.
package lifecycle

import (
	"errors"
	"fmt"
	"io"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
)

// Options is the write opt-in for this package. Deliberately not merged with
// device.Options.AllowWrites: this gates reboot/drain/image-delete, that gates
// constructing a config Writer, and wanting one must not grant the other.
type Options struct {
	// AllowWrites permits image install/delete, BGP drain and reboot; false
	// returns ErrWritesNotAllowed having only read from the device.
	AllowWrites bool

	// ForceUnsafe overrides a SafeToReboot refusal, kept distinct from
	// AllowWrites: "do the update" must not mean "drop the last live spine".
	ForceUnsafe bool

	// RequireRouting makes the post-reboot routing check fatal. Set whenever
	// `barf device update` selected more than one device: rebooting the next
	// while the last carries no traffic black-holes the fabric.
	RequireRouting bool

	// Out receives human-readable progress. nil means io.Discard.
	Out io.Writer
}

func (o Options) out() io.Writer {
	if o.Out == nil {
		return io.Discard
	}
	return o.Out
}

// ErrWritesNotAllowed is returned by every write path when
// Options.AllowWrites is false; nothing was touched. It IS
// device.ErrWritesNotAllowed, aliased so "was the device touched?" gets the
// same answer whichever package refused first.
var ErrWritesNotAllowed = device.ErrWritesNotAllowed

// ErrAlreadyExecuted means one invocation reboots at most one device.
var ErrAlreadyExecuted = errors.New("this updater already rebooted a device; one reboot per invocation")

// RedundancyError is a SafeToReboot refusal: rebooting this host would take
// down the last live spine or leaf. Only Options.ForceUnsafe overrides it.
type RedundancyError struct {
	Hostname    string
	Reason      string
	AliveSpines []string
	AliveLeaves []string
}

func (e *RedundancyError) Error() string {
	return fmt.Sprintf("refusing to reboot %s: %s", e.Hostname, e.Reason)
}

// RoutingNotRecoveredError means the device rebooted onto the expected image
// but BGP is still down. Only returned under Options.RequireRouting.
type RoutingNotRecoveredError struct {
	Hostname string
	Detail   string
}

func (e *RoutingNotRecoveredError) Error() string {
	return fmt.Sprintf("%s: routing did not recover after the reboot: %s", e.Hostname, e.Detail)
}

// DeviceUnreachableError means nothing was changed: no pre-flight answer.
type DeviceUnreachableError struct {
	Hostname string
	Detail   string
}

func (e *DeviceUnreachableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Hostname, e.Detail)
}
