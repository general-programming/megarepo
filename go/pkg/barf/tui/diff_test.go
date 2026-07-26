package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func diffJobs(names ...string) []DiffJob {
	out := make([]DiffJob, len(names))
	for i, n := range names {
		name := n
		out[i] = DiffJob{Device: name, Run: func(context.Context) DiffOutcome {
			return DiffOutcome{Device: name}
		}}
	}
	return out
}

func TestDiffModelShowsEveryDeviceImmediately(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a", "b"))
	body := m.body()
	for _, want := range []string{"--- a ---", "--- b ---", "diffing"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
}

func TestDiffModelRendersOutcomes(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a", "b"))

	updated, _ := m.Update(diffDoneMsg{index: 0, outcome: DiffOutcome{
		Device: "a", Text: "+ set system host-name a", Summary: "+1"}})
	m = updated.(*DiffModel)
	updated, _ = m.Update(diffDoneMsg{index: 1, outcome: DiffOutcome{
		Device: "b", Err: errors.New("unreachable"), Summary: "failed: unreachable"}})
	m = updated.(*DiffModel)

	body := m.body()
	if !strings.Contains(body, "set system host-name a") {
		t.Errorf("diff text missing:\n%s", body)
	}
	if !strings.Contains(body, "error: unreachable") {
		t.Errorf("error missing:\n%s", body)
	}
	if !strings.Contains(m.View().Content, "2/2 devices diffed") {
		t.Errorf("progress header missing:\n%s", m.View().Content)
	}
	if m.Outcomes()[0].Summary != "+1" {
		t.Errorf("outcome not recorded: %+v", m.Outcomes()[0])
	}
}

func TestDiffModelNoChanges(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a"))
	updated, _ := m.Update(diffDoneMsg{index: 0, outcome: DiffOutcome{Device: "a", Summary: "no changes"}})
	m = updated.(*DiffModel)
	if !strings.Contains(m.body(), "no changes") {
		t.Errorf("clean device not reported:\n%s", m.body())
	}
}

func TestDiffModelStaysOpenUntilQuit(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a"))
	m.Init() // dispatch the queue, so a completion has nothing left to start
	// A finished job must not quit: the user still has to scroll.
	_, cmd := m.Update(diffDoneMsg{index: 0, outcome: DiffOutcome{Device: "a"}})
	if cmd != nil {
		t.Error("diff viewer quit on its own")
	}
	_, cmd = m.Update(keyPress("q"))
	if cmd == nil {
		t.Fatal("q did not quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("q produced %T, want tea.Quit", msg)
	}
}

func TestDiffOutcomeState(t *testing.T) {
	cases := []struct {
		name string
		out  DiffOutcome
		want RowState
	}{
		// A bare zero-value DiffOutcome — exactly what a "compared, no
		// drift" job produces — must classify as a clean success. This is
		// the regression guard for NotRun: it must never be inferred from a
		// zero value, only set explicitly by Outcomes() backfilling a job
		// that never completed.
		{"clean", DiffOutcome{}, StateOK},
		{"drift", DiffOutcome{Text: "+ set foo"}, StateDrift},
		{"error", DiffOutcome{Err: errors.New("x")}, StateError},
		{"not run", DiffOutcome{NotRun: true}, StateNotRun},
		// NotRun wins even if Err/Text are somehow also set: the device was
		// never actually compared, whatever else is sitting in the struct.
		{"not run beats error", DiffOutcome{NotRun: true, Err: errors.New("x")}, StateNotRun},
	}
	for _, tc := range cases {
		if got := tc.out.State(); got != tc.want {
			t.Errorf("%s: state = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// Quitting early must not leave the un-started devices looking like a clean
// fleet: Outcomes() has to backfill them as NotRun, and the run has to be
// able to tell they were never compared.
func TestDiffModelQuitBackfillsUnrunDevicesAsNotRun(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a", "b", "c"))
	// "a" finishes before the quit; "b" and "c" never do.
	updated, _ := m.Update(diffDoneMsg{index: 0, outcome: DiffOutcome{Device: "a", Summary: "no changes"}})
	m = updated.(*DiffModel)
	updated, _ = m.Update(keyPress("q"))
	m = updated.(*DiffModel)

	outcomes := m.Outcomes()
	if len(outcomes) != 3 {
		t.Fatalf("outcomes = %d, want 3", len(outcomes))
	}
	if outcomes[0].State() != StateOK {
		t.Errorf("a: state = %v, want StateOK (it did finish)", outcomes[0].State())
	}
	for _, i := range []int{1, 2} {
		if !outcomes[i].NotRun {
			t.Errorf("outcome %d (%s) not marked NotRun: %+v", i, outcomes[i].Device, outcomes[i])
		}
		if outcomes[i].State() != StateNotRun {
			t.Errorf("outcome %d state = %v, want StateNotRun", i, outcomes[i].State())
		}
		if outcomes[i].Device == "" {
			t.Errorf("outcome %d lost its device name", i)
		}
	}
}

func TestDiffModelResizes(t *testing.T) {
	m := NewDiffModel(context.Background(), diffJobs("a"))
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(*DiffModel)
	if m.vp.Width() != 100 || m.vp.Height() != 27 {
		t.Errorf("viewport = %dx%d, want 100x27", m.vp.Width(), m.vp.Height())
	}
	if !strings.Contains(m.View().Content, "scroll") {
		t.Errorf("help line missing:\n%s", m.View().Content)
	}
}
