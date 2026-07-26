package lifecycle

import (
	"context"
	"sync"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

const maxProbes = device.MaxProbes

// AliveProbe reports whether a fleet member is answering: any failure, and any
// placeholder version from a half-booted device, counts as not alive. Must be
// concurrency-safe.
type AliveProbe func(ctx context.Context, h *model.Host) bool

// Redundancy is what SafeToReboot saw, reported in the dry-run plan.
type Redundancy struct {
	Target        string
	TargetIsSpine bool
	// AliveSpines and AliveLeaves are OTHER hosts (never the target) that
	// answered, in fleet order.
	AliveSpines []string
	AliveLeaves []string
	Unreachable []string
}

// SafeToReboot reports whether rebooting target leaves the fleet redundant.
// It probes every OTHER fleet member in parallel (never the target, whose own
// liveness says nothing about redundancy) and refuses when:
//
//   - the target is a spine and no other spine is online; or
//   - no other leaf is alive at all — unconditional in the Python original,
//     spine or leaf: no live leaf means nothing carries traffic.
//
// A refusal is a hard *RedundancyError: only Options.ForceUnsafe overrides it.
func SafeToReboot(ctx context.Context, target *model.Host, fleet []*model.Host, probe AliveProbe) (Redundancy, error) {
	others := make([]*model.Host, 0, len(fleet))
	for _, h := range fleet {
		if h == nil || h.Hostname == target.Hostname {
			continue
		}
		others = append(others, h)
	}

	alive := make([]bool, len(others))
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxProbes)
	for i, h := range others {
		wg.Add(1)
		go func(i int, h *model.Host) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			// Each goroutine writes only its own slot; the WaitGroup is the
			// happens-before edge.
			alive[i] = probe(ctx, h)
		}(i, h)
	}
	wg.Wait()

	result := Redundancy{Target: target.Hostname, TargetIsSpine: target.IsSpine()}
	for i, h := range others {
		switch {
		case !alive[i]:
			result.Unreachable = append(result.Unreachable, h.Hostname)
		case h.IsSpine():
			result.AliveSpines = append(result.AliveSpines, h.Hostname)
		default:
			result.AliveLeaves = append(result.AliveLeaves, h.Hostname)
		}
	}

	if result.TargetIsSpine && len(result.AliveSpines) == 0 {
		return result, &RedundancyError{
			Hostname:    target.Hostname,
			Reason:      "no other spine is online",
			AliveSpines: result.AliveSpines,
			AliveLeaves: result.AliveLeaves,
		}
	}
	if len(result.AliveLeaves) == 0 {
		return result, &RedundancyError{
			Hostname:    target.Hostname,
			Reason:      "no other leaf is alive",
			AliveSpines: result.AliveSpines,
			AliveLeaves: result.AliveLeaves,
		}
	}
	return result, nil
}
