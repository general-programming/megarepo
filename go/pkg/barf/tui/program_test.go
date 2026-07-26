package tui

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// The unit tests drive Update directly, which never exercises the parts v2
// actually changed: the Model interface shape (View now returns tea.View) and
// the renderer reading the screen modes off that view. These run the models
// under a real Program, headless, with fake probes -- no device is contacted.

// runHeadless drives m to completion with no TTY and no renderer, so it works
// in CI. It fails rather than hangs if the model never quits.
func runHeadless(t *testing.T, m tea.Model) {
	t.Helper()
	p := tea.NewProgram(m,
		tea.WithInput(nil), // no input reader: nothing to block on
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("the program never quit on its own")
	}
}

// Status auto-quits when the last probe lands, leaving the table on screen.
func TestStatusRunsUnderARealProgramAndSelfQuits(t *testing.T) {
	m := NewStatusModel(t.Context(), probes("a", "b", "c"))
	t.Cleanup(m.Cancel)

	runHeadless(t, m)

	if !m.Done() {
		t.Fatal("the program exited but the model is not done")
	}
	for _, r := range m.Rows() {
		if r.State == StatePending {
			t.Fatalf("%s never answered, yet the program exited", r.Device)
		}
	}
}

// The diff viewer stays open until the user quits, so it must be told to.
func TestDiffRunsUnderARealProgramAndQuitsOnKey(t *testing.T) {
	m := NewDiffModel(t.Context(), diffJobs("a", "b"))
	t.Cleanup(m.Cancel)

	p := tea.NewProgram(m,
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
		tea.WithoutSignals(),
	)

	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()

	// A quit key, delivered the way the input reader would deliver it.
	go func() {
		time.Sleep(50 * time.Millisecond)
		p.Send(keyPress("q"))
	}()

	select {
	case err := <-done:
		if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
			t.Fatalf("program returned %v", err)
		}
	case <-time.After(10 * time.Second):
		p.Kill()
		t.Fatal("q did not quit the diff viewer under a real program")
	}

	// The same property the unit tests assert, now end to end.
	if m.ctx.Err() == nil {
		t.Fatal("the viewer exited without cancelling the jobs' context")
	}
}

// The diff view carries the screen modes that used to be Program options.
func TestDiffViewDeclaresAltScreenAndMouse(t *testing.T) {
	m := NewDiffModel(t.Context(), diffJobs("a"))
	t.Cleanup(m.Cancel)

	for _, ready := range []bool{false, true} {
		m.ready = ready
		v := m.View()
		if !v.AltScreen {
			t.Errorf("ready=%v: the diff view left the altscreen; it must stay full-window", ready)
		}
		if v.MouseMode != tea.MouseModeCellMotion {
			t.Errorf("ready=%v: mouse mode = %v, want cell motion for viewport scrolling", ready, v.MouseMode)
		}
	}
}

// Status renders inline: turning the altscreen on would wipe the finished
// table off the screen on exit, which is the whole output of the command.
func TestStatusViewStaysInline(t *testing.T) {
	m := NewStatusModel(t.Context(), probes("a"))
	t.Cleanup(m.Cancel)

	v := m.View()
	if v.AltScreen {
		t.Fatal("status used the altscreen; the finished table would vanish on exit")
	}
	if !strings.Contains(v.Content, "a") {
		t.Fatalf("status view is missing its device:\n%s", v.Content)
	}
}
