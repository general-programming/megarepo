package firmware

import (
	"context"
	"fmt"
	"sync"
)

// WarnLogger is the slice of a logger this package needs; nil is fine.
type WarnLogger interface {
	Warn(msg string, args ...any)
}

// Checker answers the LATEST FIRMWARE column, priming the release fetch once
// up front as Python does, but safe to read from the probe goroutines.
type Checker struct {
	mu     sync.RWMutex
	latest map[string]string // deviceType -> latest version ("" = unknown)
}

// NewChecker primes the latest version per device type. Missing providers and
// failed lookups are left unknown: an unreachable feed must not fail `barf
// status`, so it is logged and the cells read "?".
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

// Set records a known-latest version without going to the network, for
// callers that already hold it and for tests, which must never reach the feed.
func (c *Checker) Set(deviceType, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.latest == nil {
		c.latest = make(map[string]string, 1)
	}
	c.latest[deviceType] = version
}

// Latest is the primed latest version for a device type, "" when unknown.
func (c *Checker) Latest(deviceType string) string {
	if c == nil {
		return ""
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest[deviceType]
}

// Cell is the LATEST FIRMWARE cell, byte-identical to Python's firmware_cell:
// "?" when unknown, "yes" when current, "no (<latest>)" otherwise.
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
