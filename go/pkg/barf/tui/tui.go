// Package tui holds the Bubble Tea interfaces for the read-only barf commands.
// Every model is a pure state machine over messages and the work arrives as a
// caller-supplied func, so cli can drive the same probes in non-TTY mode. It
// imports none of barf/cli, barf/device or barf/render.
package tui

import "charm.land/lipgloss/v2"

// DefaultConcurrency caps in-flight probes, and so goroutines: Bubble Tea runs
// one per batched command. Matches the cli limit (Python max_workers=8).
const DefaultConcurrency = 8

// RowState is how a probed device is doing, and picks the row colour.
type RowState int

const (
	StatePending RowState = iota // probe has not answered yet
	StateOK                      // answered, config matches
	StateDrift                   // answered, config differs
	StateError                   // could not be probed
	StateNotRun                  // the viewer quit before this one ran at all
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
	case StateNotRun:
		return "not run"
	}
	return "unknown"
}

// Package-level so a caller can restyle the whole surface at once.
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

// styleFor is the style a row in the given state renders with.
func styleFor(s RowState) lipgloss.Style {
	switch s {
	case StateOK:
		return styleOK
	case StateDrift:
		return styleDrift
	case StateError, StateNotRun:
		return styleError
	default:
		return stylePending
	}
}
