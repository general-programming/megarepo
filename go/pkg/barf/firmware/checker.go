package firmware

import (
	"context"
	"fmt"
	"sync"
)

// WarnLogger is the tiny slice of a logger this package needs. log/slog
// satisfies it directly and a nil logger is fine, so nothing here forces
// a logging dependency on callers.
type WarnLogger interface {
	Warn(msg string, args ...any)
}

// Checker answers the LATEST FIRMWARE column.
//
// Python primes the release fetch once in the main thread before the
// probe pool starts, because its cached_property is not thread-safe;
// Checker does the same priming for the same reason (one network round
// trip, not one per device) but is additionally safe to call from the
// probe goroutines.
type Checker struct {
	mu     sync.RWMutex
	latest map[string]string // deviceType -> latest version ("" = unknown)
}

// NewChecker primes the latest version for each device type that has a
// provider. Device types without one, and lookups that fail, are left
// unknown: an unreachable release feed must not fail `barf status`, so
// the failure is logged (when log is non-nil) and the cells read "?".
func NewChecker(ctx context.Context, log WarnLogger, deviceTypes ...string) *Checker {
	c := &Checker{latest: make(map[string]string, len(deviceTypes))}
	seen := make(map[string]bool, len(deviceTypes))
	for _, dt := range deviceTypes {
		if seen[dt] {
			continue
		}
		seen[dt] = true

		p, ok := For(dt)
		if !ok {
			continue
		}
		version, err := p.LatestVersion(ctx)
		if err != nil {
			if log != nil {
				log.Warn("could not fetch the latest release", "devicetype", dt, "err", err)
			}
			continue
		}
		c.latest[dt] = version
	}
	return c
}

// Set records a known-latest version for a device type without going to
// the network. It exists for callers that already have the version in
// hand (and for tests, which must never reach the release feed).
func (c *Checker) Set(deviceType, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		c.latest = make(map[string]string, 1)
	}
	c.latest[deviceType] = version
}

// Latest is the primed latest version for a device type, or "" when it
// is unknown.
func (c *Checker) Latest(deviceType string) string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest[deviceType]
}

// Cell is the LATEST FIRMWARE table cell for one device, byte-identical
// to Python's firmware_cell: "?" with no provider or no known latest
// version, "yes" when current, and "no (<latest>)" otherwise.
func (c *Checker) Cell(deviceType, version string) string {
	latest := c.Latest(deviceType)
	if latest == "" {
		return Unknown
	}
	if IsCurrent(latest, version) {
		return "yes"
	}
	return fmt.Sprintf("no (%s)", latest)
}
