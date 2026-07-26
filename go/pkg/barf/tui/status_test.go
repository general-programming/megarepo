package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func probes(names ...string) []StatusProbe {
	out := make([]StatusProbe, len(names))
	for i, n := range names {
		name := n
		out[i] = StatusProbe{Device: name, Run: func(context.Context) StatusRow {
			return StatusRow{Device: name, Status: "ok", State: StateOK}
		}}
	}
	return out
}

func TestStatusModelStartsEveryRowPending(t *testing.T) {
	m := NewStatusModel(context.Background(), probes("a", "b", "c"))

	if got := len(m.Rows()); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
	for _, r := range m.Rows() {
		if r.State != StatePending {
			t.Errorf("%s: state = %v, want pending", r.Device, r.State)
		}
		if r.Status != "probing" {
			t.Errorf("%s: status = %q, want probing", r.Device, r.Status)
		}
	}
	// Every host must be on screen before anything answers: that is the
	// whole point of the live table.
	view := m.View()
	for _, name := range []string{"a", "b", "c"} {
		if !strings.Contains(view, name) {
			t.Errorf("initial view is missing %q:\n%s", name, view)
		}
	}
	if m.Done() {
		t.Error("model reports done before any probe answered")
	}
}

func TestStatusModelFillsRowsAsProbesAnswer(t *testing.T) {
	m := NewStatusModel(context.Background(), probes("a", "b"))

	row := StatusRow{Device: "b", Endpoint: "10.0.0.2", Model: "vyos", Uptime: "5d",
		Version: "1.5", Consistent: "yes", Status: "ok", State: StateOK}
	updated, cmd := m.Update(statusDoneMsg{index: 1, row: row})
	m = updated.(*StatusModel)

	if cmd != nil {
		t.Error("model quit before every probe answered")
	}
	if m.Rows()[1] != row {
		t.Errorf("row 1 = %+v, want %+v", m.Rows()[1], row)
	}
	if m.Rows()[0].State != StatePending {
		t.Error("row 0 stopped being pending without answering")
	}
	if !strings.Contains(m.View(), "1/2 devices reported") {
		t.Errorf("progress header missing:\n%s", m.View())
	}
}

func TestStatusModelQuitsWhenAllProbesAnswer(t *testing.T) {
	m := NewStatusModel(context.Background(), probes("a"))

	updated, cmd := m.Update(statusDoneMsg{index: 0, row: StatusRow{Device: "a", State: StateOK}})
	m = updated.(*StatusModel)

	if cmd == nil {
		t.Fatal("model did not quit after the last probe answered")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("final command = %T, want tea.Quit", msg)
	}
	if !m.Done() {
		t.Error("model is not done after the last probe")
	}
}

func TestStatusModelQuitKey(t *testing.T) {
	m := NewStatusModel(context.Background(), probes("a", "b"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("q produced %T, want tea.Quit", msg)
	}
}

func TestStatusRowCellsMatchColumns(t *testing.T) {
	r := StatusRow{"dev", "ep", "mod", "up", "ver", "yes", "ok", StateOK}
	if got, want := len(r.Cells()), len(StatusColumns); got != want {
		t.Fatalf("cells = %d, columns = %d", got, want)
	}
	if r.Cells()[0] != "dev" || r.Cells()[6] != "ok" {
		t.Errorf("cells out of order: %v", r.Cells())
	}
}

func TestStatusViewColoursByState(t *testing.T) {
	m := NewStatusModel(context.Background(), probes("a"))
	m.rows[0] = StatusRow{Device: "a", Status: "error: boom", State: StateError}
	m.pending = 0
	if !strings.Contains(m.View(), "error: boom") {
		t.Errorf("error row missing from view:\n%s", m.View())
	}
}
