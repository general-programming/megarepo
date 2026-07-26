package tui

import (
	"context"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Regression tests for finding 6 (fan-out sized by input length) and the
// TUI half of finding 2 (q/ctrl+c only quit, they never cancelled).
//
// These push messages through Update directly rather than running a
// program: Update is the only thing allowed to mutate a model, and these
// tests must not introduce a second writer.

// countingProbes returns probes that record how many have been started.
func countingProbes(n int, started *atomic.Int64, block <-chan struct{}) []StatusProbe {
	out := make([]StatusProbe, n)
	for i := range n {
		out[i] = StatusProbe{
			Device: "device-" + itoa(i),
			Run: func(ctx context.Context) StatusRow {
				started.Add(1)
				if block != nil {
					select {
					case <-block:
					case <-ctx.Done():
						return StatusRow{Device: "device-" + itoa(i), Status: "cancelled", State: StateError}
					}
				}
				return StatusRow{Device: "device-" + itoa(i), Status: "ok", State: StateOK}
			},
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}

// batchLen counts the commands in a tea.Cmd, which is how many goroutines
// Bubble Tea will start for it.
func batchLen(t *testing.T, cmd tea.Cmd) int {
	t.Helper()
	if cmd == nil {
		return 0
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return 1
	}
	return len(batch)
}

// TestStatusInitDoesNotFanOutByFleetSize is the reproduction: Init used
// to append one command per probe, and tea.Batch runs one goroutine per
// command, so 300 devices meant 301 goroutines — none of which could be
// reclaimed while parked on the caller's semaphore.
func TestStatusInitDoesNotFanOutByFleetSize(t *testing.T) {
	const devices = 300
	var started atomic.Int64
	m := NewStatusModel(t.Context(), countingProbes(devices, &started, nil))
	t.Cleanup(m.Cancel)

	// spinner tick + at most DefaultConcurrency probes.
	if got, want := batchLen(t, m.Init()), DefaultConcurrency+1; got > want {
		t.Fatalf("Init batched %d commands for %d devices, want at most %d", got, devices, want)
	}
	if m.next != DefaultConcurrency {
		t.Fatalf("dispatched %d probes up front, want %d", m.next, DefaultConcurrency)
	}
}

// TestStatusDispatchesTheRestAsProbesAnswer: bounding the fan-out must
// not drop devices. Every probe still runs, one released per completion.
func TestStatusDispatchesTheRestAsProbesAnswer(t *testing.T) {
	const devices = 20
	var started atomic.Int64
	m := NewStatusModel(t.Context(), countingProbes(devices, &started, nil))
	t.Cleanup(m.Cancel)
	m.SetConcurrency(4)
	m.Init()

	for i := range devices {
		if m.next > min(i+4+1, devices) {
			t.Fatalf("after %d completions %d probes had been dispatched; the cap is not holding", i, m.next)
		}
		_, cmd := m.Update(statusDoneMsg{index: i, row: StatusRow{Device: "device-" + itoa(i), State: StateOK}})
		if cmd != nil {
			cmd()
		}
	}

	if m.next != devices {
		t.Fatalf("only %d of %d probes were ever dispatched", m.next, devices)
	}
	if !m.Done() {
		t.Fatal("the model did not finish")
	}
	for i, row := range m.Rows() {
		if row.State == StatePending {
			t.Fatalf("row %d is still pending", i)
		}
	}
}

// TestStatusQuitCancelsTheProbes reproduces the other half: `q` used to
// return tea.Quit and nothing else, so probes that were queued or in
// flight kept contacting devices while the command printed its summary.
func TestStatusQuitCancelsTheProbes(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		t.Run(key, func(t *testing.T) {
			var started atomic.Int64
			m := NewStatusModel(t.Context(), countingProbes(10, &started, nil))
			t.Cleanup(m.Cancel)

			if m.ctx.Err() != nil {
				t.Fatal("the probe context is cancelled before the model even starts")
			}
			m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			if key == "esc" {
				m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			}
			if key == "ctrl+c" {
				m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			}
			if m.ctx.Err() == nil {
				t.Fatalf("%q quit the UI without cancelling the probes", key)
			}
		})
	}
}

// TestStatusCancelIsIndependentOfTheParent: cancelling the model's
// context must not look like the parent being cancelled, or the Bubble
// Tea program would report ErrProgramKilled instead of a clean quit.
func TestStatusCancelDoesNotTouchTheParent(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewStatusModel(parent, probes("a", "b"))
	m.Cancel()

	if parent.Err() != nil {
		t.Fatal("cancelling the model cancelled its parent context")
	}
	if m.ctx.Err() == nil {
		t.Fatal("Cancel did not cancel the model's own context")
	}
	// Idempotent.
	m.Cancel()
}

// -- the diff model, same two problems --------------------------------

func countingJobs(n int) []DiffJob {
	out := make([]DiffJob, n)
	for i := range n {
		out[i] = DiffJob{
			Device: "device-" + itoa(i),
			Run: func(context.Context) DiffOutcome {
				return DiffOutcome{Device: "device-" + itoa(i)}
			},
		}
	}
	return out
}

func TestDiffInitDoesNotFanOutByFleetSize(t *testing.T) {
	const devices = 300
	m := NewDiffModel(t.Context(), countingJobs(devices))
	t.Cleanup(m.Cancel)

	if got, want := batchLen(t, m.Init()), DefaultConcurrency+1; got > want {
		t.Fatalf("Init batched %d commands for %d devices, want at most %d", got, devices, want)
	}
	if m.next != DefaultConcurrency {
		t.Fatalf("dispatched %d jobs up front, want %d", m.next, DefaultConcurrency)
	}
}

func TestDiffDispatchesTheRestAsJobsAnswer(t *testing.T) {
	const devices = 20
	m := NewDiffModel(t.Context(), countingJobs(devices))
	t.Cleanup(m.Cancel)
	m.SetConcurrency(4)
	m.Init()

	for i := range devices {
		m.Update(diffDoneMsg{index: i, outcome: DiffOutcome{Device: "device-" + itoa(i)}})
	}
	if m.next != devices {
		t.Fatalf("only %d of %d jobs were ever dispatched", m.next, devices)
	}
	for i, filled := range m.filled {
		if !filled {
			t.Fatalf("outcome %d was never filled in", i)
		}
	}
}

func TestDiffQuitCancelsTheJobs(t *testing.T) {
	m := NewDiffModel(t.Context(), countingJobs(10))
	t.Cleanup(m.Cancel)

	if m.ctx.Err() != nil {
		t.Fatal("the job context is cancelled before the model even starts")
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if m.ctx.Err() == nil {
		t.Fatal("ctrl+c quit the viewer without cancelling the queued diffs; " +
			"runDiff would print its summary while devices were still being contacted")
	}
}
