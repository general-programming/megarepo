// Package tui holds the Bubble Tea interfaces for the read-only barf
// commands. Every model here is a pure state machine over messages: the
// work itself arrives as a func the caller supplies, so the cli package
// can drive the same probes in plain (non-TTY) mode without a terminal.
//
// The package deliberately does not import barf/cli, barf/device or
// barf/render. It only knows about rows of strings and funcs that
// produce them, which keeps it testable by pushing messages through
// Update directly.
package tui

import "github.com/charmbracelet/lipgloss"

// DefaultConcurrency is how many probes/jobs a model keeps in flight,
// and therefore how many goroutines it creates: Bubble Tea runs one
// goroutine per batched command, so a model that fired every device's
// work up front would size its goroutine count by the fleet. It matches
// the cli package's own probe limit (Python's max_workers=8).
const DefaultConcurrency = 8

// RowState is how a probed device is doing, and picks the row colour.
type RowState int

const (
	// StatePending means the probe has not answered yet.
	StatePending RowState = iota
	// StateOK means the device answered and its config matches.
	StateOK
	// StateDrift means the device answered but its config differs.
	StateDrift
	// StateError means the device could not be probed.
	StateError
)

// String is the plain-text spelling of a state, used in tests and logs.
func (s RowState) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateOK:
		return "ok"
	case StateDrift:
		return "drift"
	case StateError:
		return "error"
	}
	return "unknown"
}

// Styles are the colours shared by every model in this package. They are
// package-level so a caller can restyle the whole surface at once.
var (
	styleHeader  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleDrift   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	stylePending = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleAdded   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleRemoved = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
)

// styleFor is the lipgloss style a row in the given state renders with.
func styleFor(s RowState) lipgloss.Style {
	switch s {
	case StateOK:
		return styleOK
	case StateDrift:
		return styleDrift
	case StateError:
		return styleError
	default:
		return stylePending
	}
}
