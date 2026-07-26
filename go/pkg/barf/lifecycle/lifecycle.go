// Package lifecycle implements the device-lifecycle operations behind
// `barf device update` and `barf device cleanup`: image install, BGP
// drain, reboot, post-reboot routing verification, and old-image
// cleanup.
//
// This is the only barf package that can change a device, and it is
// built so that changing one is never the default and never implicit:
//
//   - Everything is split into a Plan (pure reads, always safe to run)
//     and an Execute (the writes). The CLI's default is to build the
//     plan, print it, and stop.
//   - Execute refuses unless Options.AllowWrites is set. That flag is
//     the single structural opt-in; the zero value of every type here
//     writes nothing.
//   - Execute refuses when SafeToReboot found no redundancy, and
//     AllowWrites alone cannot override that. Overriding it needs
//     Options.ForceUnsafe, a separate and deliberately loud flag.
//   - An Updater reboots at most one device, ever. A second Execute on
//     the same Updater is refused even with every flag set.
//
// Ports projects/barf/barf/cli/device.py (device_update, device_cleanup,
// wait_for_device_alive) and the helpers in
// projects/barf/barf/vendors/{__init__.py,vyos.py} (safe_to_reboot,
// system_images, verify_routing, update_host, cleanup_host).
package lifecycle

import (
	"errors"
	"fmt"
	"io"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
)

// Options is the write opt-in for every operation in this package.
//
// NOTE: device.Options now has its own `AllowWrites` with the same
// meaning, and the two are deliberately NOT merged into one type. They
// gate different things — this one gates reboot/drain/image-delete, that
// one gates constructing a config Writer — and a caller that wants one
// must not silently get the other. What did get unified is the error:
// both refusals now satisfy errors.Is(err, ErrWritesNotAllowed).
//
// Whatever else changes here, these must stay *separate booleans* from
// any read-side option, so that enabling a device read can never enable
// a device write.
type Options struct {
	// AllowWrites permits operations that modify a device: image
	// install, image delete, BGP drain, reboot. False (the zero value)
	// means every such call returns ErrWritesNotAllowed having contacted
	// the device only for reads.
	AllowWrites bool

	// ForceUnsafe overrides a SafeToReboot refusal. It is deliberately
	// distinct from AllowWrites: confirming "yes, do the update" must not
	// also mean "yes, take the last live spine down". Callers that set
	// this must say loudly what is being overridden.
	ForceUnsafe bool

	// RequireRouting turns the post-reboot routing check from a warning
	// into a hard failure.
	//
	// A single-device run only warns: the operator is watching, the
	// device is already back on the new image, and there is nothing left
	// to protect. A run that still has devices to reboot must stop
	// instead, because "the last device I rebooted is not carrying
	// traffic" is exactly the state in which rebooting the next one
	// black-holes the fabric. `barf device update` sets this whenever it
	// selected more than one device.
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
// Options.AllowWrites is false. Nothing on the device was touched.
//
// It IS device.ErrWritesNotAllowed, not a second sentinel that means the
// same thing. It used to be its own errors.New, so a refusal raised in
// package device — NewVyOSWriter without AllowWrites, say — did not
// satisfy errors.Is(err, lifecycle.ErrWritesNotAllowed) even though it
// was the identical condition. Callers deciding "was the device touched?"
// got a different answer depending on which package refused first, which
// is the one question this error exists to answer. Use
// device.IsRefusal for the broader "any guard refused" check.
var ErrWritesNotAllowed = device.ErrWritesNotAllowed

// ErrAlreadyExecuted is returned when an Updater is asked to perform a
// second device-changing run. One invocation reboots at most one device.
var ErrAlreadyExecuted = errors.New("this updater already rebooted a device; one reboot per invocation")

// RedundancyError is a SafeToReboot refusal: rebooting this host would
// take down the last live spine or the last live leaf.
//
// It is a hard error. Confirming the update (--yes) does not override it;
// only Options.ForceUnsafe does.
type RedundancyError struct {
	Hostname string
	Reason   string
	// AliveSpines and AliveLeaves are the other fleet members that
	// answered, for the report.
	AliveSpines []string
	AliveLeaves []string
}

func (e *RedundancyError) Error() string {
	return fmt.Sprintf("refusing to reboot %s: %s", e.Hostname, e.Reason)
}

// RoutingNotRecoveredError means the device rebooted, came back on the
// expected image, but its routing did not recover: BGP is still
// administratively down, or the check itself could not be run.
//
// It is only returned when Options.RequireRouting is set. The device is
// updated; what failed is the health verification afterwards. A caller
// updating several devices must treat it as fatal to the whole run.
type RoutingNotRecoveredError struct {
	Hostname string
	Detail   string
}

func (e *RoutingNotRecoveredError) Error() string {
	return fmt.Sprintf("%s: routing did not recover after the reboot: %s", e.Hostname, e.Detail)
}

// DeviceUnreachableError means the device never answered pre-flight, so
// nothing on it was changed and the fleet lost no redundancy.
type DeviceUnreachableError struct {
	Hostname string
	Detail   string
}

func (e *DeviceUnreachableError) Error() string {
	return fmt.Sprintf("%s: %s", e.Hostname, e.Detail)
}
