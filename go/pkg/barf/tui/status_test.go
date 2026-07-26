package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keyPress builds the tea.KeyPressMsg a real terminal produces for name, as
// ultraviolet's key table decodes it: a printable key carries Text, while esc
// and ctrl combos carry only a Code (plus Mod) and get their spelling from
// Keystroke. Hand-rolling these wrong is the easy way to make the quit-path
// tests pass against a model that would never quit in practice.
func keyPress(name string) tea.KeyPressMsg {
	switch name {
	case "q":
		return tea.KeyPressMsg{Code: 'q', Text: "q"}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	panic("unknown key " + name)
}

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
	// Every host must be on screen before anything answers.
	view := m.View().Content
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
	// Init dispatches the first batch; otherwise probes stay queued.
	m.Init()

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
	if !strings.Contains(m.View().Content, "1/2 devices reported") {
		t.Errorf("progress header missing:\n%s", m.View().Content)
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
	_, cmd := m.Update(keyPress("q"))
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("q produced %T, want tea.Quit", msg)
	}
}

// The quit tests are only as good as the messages they feed in: if keyPress
// built something the model's `switch msg.String()` would never match, they
// would pass against a model that never quits. Pin the spellings.
func TestKeyPressHelperMatchesTheRealSpellings(t *testing.T) {
	for _, name := range []string{"q", "esc", "ctrl+c"} {
		if got := keyPress(name).String(); got != name {
			t.Errorf("keyPress(%q).String() = %q; the quit tests are not exercising %q",
				name, got, name)
		}
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
	if !strings.Contains(m.View().Content, "error: boom") {
		t.Errorf("error row missing from view:\n%s", m.View().Content)
	}
}
