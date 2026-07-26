package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

func host(name string) *model.Host {
	return &model.Host{Hostname: name, DeviceType: "vyos", ASN: 65000}
}

// aliveSet builds a probe that considers exactly the named hosts alive.
func aliveSet(names ...string) AliveProbe {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(_ context.Context, h *model.Host) bool { return set[h.Hostname] }
}

func TestSafeToRebootLeafWithRedundancy(t *testing.T) {
	target := host("sea1-leaf-0")
	fleet := []*model.Host{target, host("sea1-leaf-1"), host("sea1-spine-0")}

	got, err := SafeToReboot(context.Background(), target, fleet, aliveSet("sea1-leaf-1", "sea1-spine-0"))
	if err != nil {
		t.Fatalf("expected the reboot to be allowed: %v", err)
	}
	if len(got.AliveLeaves) != 1 || got.AliveLeaves[0] != "sea1-leaf-1" {
		t.Fatalf("alive leaves = %v", got.AliveLeaves)
	}
	if len(got.AliveSpines) != 1 {
		t.Fatalf("alive spines = %v", got.AliveSpines)
	}
}

func TestSafeToRebootRefusesTheLastSpine(t *testing.T) {
	target := host("sea1-spine-0")
	fleet := []*model.Host{target, host("sea1-spine-1"), host("sea1-leaf-0")}

	// Only the leaf answers: the other spine is down, so rebooting this
	// one leaves the fabric spineless.
	_, err := SafeToReboot(context.Background(), target, fleet, aliveSet("sea1-leaf-0"))
	var redundancy *RedundancyError
	if !errors.As(err, &redundancy) {
		t.Fatalf("expected a RedundancyError, got %v", err)
	}
	if redundancy.Reason != "no other spine is online" {
		t.Fatalf("reason = %q", redundancy.Reason)
	}
}

func TestSafeToRebootRefusesWhenNoLeafIsAlive(t *testing.T) {
	// Unconditional in the Python original, spine or leaf: a fleet with
	// no live leaf has nothing carrying traffic.
	target := host("sea1-spine-0")
	fleet := []*model.Host{target, host("sea1-spine-1"), host("sea1-leaf-0")}

	_, err := SafeToReboot(context.Background(), target, fleet, aliveSet("sea1-spine-1"))
	var redundancy *RedundancyError
	if !errors.As(err, &redundancy) {
		t.Fatalf("expected a RedundancyError, got %v", err)
	}
	if redundancy.Reason != "no other leaf is alive" {
		t.Fatalf("reason = %q", redundancy.Reason)
	}
}

func TestSafeToRebootNeverProbesTheTarget(t *testing.T) {
	target := host("sea1-leaf-0")
	fleet := []*model.Host{target, host("sea1-leaf-1"), host("sea1-spine-0")}

	var probedTarget atomic.Bool
	probe := func(_ context.Context, h *model.Host) bool {
		if h.Hostname == target.Hostname {
			probedTarget.Store(true)
		}
		return true
	}
	if _, err := SafeToReboot(context.Background(), target, fleet, probe); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if probedTarget.Load() {
		// The target's own liveness says nothing about redundancy, and
		// counting it would let a healthy device vouch for itself.
		t.Fatal("the target must not be counted as its own redundancy")
	}
}

func TestSafeToRebootRecordsUnreachable(t *testing.T) {
	target := host("sea1-leaf-0")
	fleet := []*model.Host{target, host("sea1-leaf-1"), host("sea1-leaf-2"), host("sea1-spine-0")}

	got, _ := SafeToReboot(context.Background(), target, fleet, aliveSet("sea1-leaf-1", "sea1-spine-0"))
	if len(got.Unreachable) != 1 || got.Unreachable[0] != "sea1-leaf-2" {
		t.Fatalf("unreachable = %v", got.Unreachable)
	}
}

// TestSafeToRebootProbesInParallel is the -race target: many probes at
// once, all writing into the same result slice.
func TestSafeToRebootProbesInParallel(t *testing.T) {
	target := host("sea1-leaf-0")
	fleet := []*model.Host{target}
	for i := 1; i <= 40; i++ {
		fleet = append(fleet, host("sea1-leaf-"+string(rune('a'+i%26))+string(rune('0'+i/26))))
	}
	fleet = append(fleet, host("sea1-spine-0"))

	var concurrent, peak atomic.Int64
	probe := func(_ context.Context, _ *model.Host) bool {
		n := concurrent.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(time.Millisecond)
		concurrent.Add(-1)
		return true
	}

	if _, err := SafeToReboot(context.Background(), target, fleet, probe); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	if peak.Load() < 2 {
		t.Fatalf("probes did not run in parallel (peak %d)", peak.Load())
	}
	if peak.Load() > maxProbes {
		t.Fatalf("probe concurrency %d exceeded the %d cap", peak.Load(), maxProbes)
	}
}
