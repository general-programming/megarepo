package lifecycle

import (
	"errors"
	"fmt"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Regression: device and lifecycle refusals were separate types with no
// Unwrap, so errors.Is failed in both directions.
func TestRefusalsMatchAcrossPackages(t *testing.T) {
	host := &model.Host{Hostname: "rtr-1", DeviceType: "vyos"}

	// device-side refusal: a writer built without AllowWrites.
	_, deviceErr := device.NewVyOSWriter(host, device.Options{})
	if deviceErr == nil {
		t.Fatal("NewVyOSWriter without AllowWrites must refuse")
	}

	if !errors.Is(deviceErr, ErrWritesNotAllowed) {
		t.Errorf("errors.Is(deviceErr, lifecycle.ErrWritesNotAllowed) = false: %v", deviceErr)
	}
	if !errors.Is(deviceErr, device.ErrWritesNotAllowed) {
		t.Errorf("errors.Is(deviceErr, device.ErrWritesNotAllowed) = false: %v", deviceErr)
	}
	if !device.IsRefusal(deviceErr) {
		t.Errorf("device.IsRefusal(deviceErr) = false: %v", deviceErr)
	}

	// The typed error is still reachable, so callers keep the detail.
	var notAllowed *device.WritesNotAllowedError
	if !errors.As(deviceErr, &notAllowed) {
		t.Errorf("errors.As to *device.WritesNotAllowedError failed: %v", deviceErr)
	}

	// lifecycle-side refusal, wrapped with %w as the API client does.
	lifecycleErr := fmt.Errorf("rtr-1: /image: %w", ErrWritesNotAllowed)
	if !errors.Is(lifecycleErr, device.ErrWritesNotAllowed) {
		t.Errorf("errors.Is(lifecycleErr, device.ErrWritesNotAllowed) = false: %v", lifecycleErr)
	}
	if !device.IsRefusal(lifecycleErr) {
		t.Errorf("device.IsRefusal(lifecycleErr) = false: %v", lifecycleErr)
	}
}

// A guard trip (read transport asked to write) is a refusal too, but a
// distinct sentinel: it means a wiring bug, not a dry run.
func TestWriteAttemptIsARefusalButNotADryRun(t *testing.T) {
	attempt := &device.WriteAttemptError{What: "VyOS configure request"}

	if !device.IsRefusal(attempt) {
		t.Error("IsRefusal(WriteAttemptError) = false")
	}
	if !errors.Is(attempt, device.ErrWriteAttempt) {
		t.Error("errors.Is(attempt, ErrWriteAttempt) = false")
	}
	if errors.Is(attempt, ErrWritesNotAllowed) {
		t.Error("a guard trip must not masquerade as a dry-run refusal")
	}
}

// The refusal/failure distinction tells an operator whether a retry is safe.
func TestOrdinaryErrorIsNotARefusal(t *testing.T) {
	if device.IsRefusal(errors.New("connection reset by peer")) {
		t.Error("IsRefusal(ordinary error) = true")
	}
}
