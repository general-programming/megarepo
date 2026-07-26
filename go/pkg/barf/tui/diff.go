package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// DiffOutcome is one device's rendered-vs-running diff; Err is a per-device
// failure and never aborts the other devices.
type DiffOutcome struct {
	Device  string
	Text    string
	Summary string
	Err     error

	// NotRun is set only by Outcomes() backfilling a job that never got a
	// diffDoneMsg (the viewer was quit before it finished). It must never be
	// set by a job's own Run: a zero-value DiffOutcome{} — e.g. "compared,
	// no drift" — is a legitimate success and must stay StateOK.
	NotRun bool
}

// State classifies an outcome for colouring and for the summary table.
func (o DiffOutcome) State() RowState {
	switch {
	case o.NotRun:
		return StateNotRun
	case o.Err != nil:
		return StateError
	case strings.TrimSpace(o.Text) != "":
		return StateDrift
	default:
		return StateOK
	}
}

// DiffJob renders and diffs one device; Run must not write to it.
type DiffJob struct {
	Device string
	Run    func(ctx context.Context) DiffOutcome
}

type diffDoneMsg struct {
	index   int
	outcome DiffOutcome
}

// DiffModel streams per-device diffs into a scrollable viewport as they arrive.
type DiffModel struct {
	ctx      context.Context
	cancel   context.CancelFunc
	jobs     []DiffJob
	outcomes []DiffOutcome
	filled   []bool
	pending  int
	vp       viewport.Model
	spin     spinner.Model
	ready    bool
	quitting bool

	concurrency int // caps in-flight jobs: tea.Batch is one goroutine per cmd
	next        int // first job not yet dispatched; Update-only
}

// NewDiffModel builds the diff viewer, deriving a cancellable context from
// ctx; call Cancel (RunDiff does) to release queued and in-flight jobs.
func NewDiffModel(ctx context.Context, jobs []DiffJob) *DiffModel {
	if ctx == nil {
		ctx = context.Background()
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(stylePending))

	jobCtx, cancel := context.WithCancel(ctx)
	m := &DiffModel{
		ctx:         jobCtx,
		cancel:      cancel,
		jobs:        jobs,
		outcomes:    make([]DiffOutcome, len(jobs)),
		filled:      make([]bool, len(jobs)),
		pending:     len(jobs),
		vp:          viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		spin:        sp,
		concurrency: DefaultConcurrency,
	}
	for i, j := range jobs {
		m.outcomes[i] = DiffOutcome{Device: j.Device}
	}
	m.vp.SetContent(m.body())
	return m
}

// SetConcurrency caps how many jobs run at once; call it before Init.
func (m *DiffModel) SetConcurrency(n int) {
	if n > 0 {
		m.concurrency = n
	}
}

// Cancel releases the jobs' context. Safe to call more than once.
func (m *DiffModel) Cancel() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Init starts the spinner and the first `concurrency` jobs; each completion
// dispatches the next, so a 300-device run is not 300 goroutines.
func (m *DiffModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, m.concurrency+1)
	cmds = append(cmds, m.spin.Tick)
	for m.next < len(m.jobs) && m.next < m.concurrency {
		cmds = append(cmds, m.jobCmd(m.next))
		m.next++
	}
	return tea.Batch(cmds...)
}

// dispatchNext returns the next undispatched job's command, nil when done.
func (m *DiffModel) dispatchNext() tea.Cmd {
	if m.next >= len(m.jobs) {
		return nil
	}
	cmd := m.jobCmd(m.next)
	m.next++
	return cmd
}

func (m *DiffModel) jobCmd(i int) tea.Cmd {
	job := m.jobs[i]
	return func() tea.Msg {
		out := job.Run(m.ctx)
		if out.Device == "" {
			out.Device = job.Device
		}
		return diffDoneMsg{index: i, outcome: out}
	}
}

// Update handles job completions, scrolling, and quit keys.
func (m *DiffModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Two lines of chrome: the header and the help line.
		m.vp.SetWidth(msg.Width)
		m.vp.SetHeight(max(msg.Height-3, 3))
		m.ready = true
		m.vp.SetContent(m.body())

	case tea.KeyPressMsg:
		// v2 made tea.KeyMsg an interface covering presses AND releases;
		// matching it would fire the quit path twice on terminals that report
		// releases. Only a press quits.
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			// Without Cancel, runDiff prints its summary while queued
			// jobs keep contacting devices.
			m.Cancel()
			return m, tea.Quit
		}

	case diffDoneMsg:
		if msg.index >= 0 && msg.index < len(m.outcomes) {
			if !m.filled[msg.index] {
				m.filled[msg.index] = true
				m.pending--
			}
			m.outcomes[msg.index] = msg.outcome
		}
		m.vp.SetContent(m.body())
		if m.pending <= 0 {
			m.Cancel()
		}
		return m, m.dispatchNext()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if m.pending > 0 {
			cmds = append(cmds, cmd)
		}
	}

	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

// body is the full scrollable document: one section per device.
func (m *DiffModel) body() string {
	var b strings.Builder
	for i, out := range m.outcomes {
		b.WriteString(styleTitle.Render("--- " + out.Device + " ---"))
		b.WriteString("\n")
		switch {
		case !m.filled[i]:
			b.WriteString(stylePending.Render(m.spin.View() + " diffing…"))
			b.WriteString("\n")
		case out.Err != nil:
			b.WriteString(styleError.Render("error: " + out.Err.Error()))
			b.WriteString("\n")
		case strings.TrimSpace(out.Text) == "":
			b.WriteString(styleOK.Render("no changes"))
			b.WriteString("\n")
		default:
			for _, line := range strings.Split(strings.TrimRight(out.Text, "\n"), "\n") {
				switch {
				case strings.HasPrefix(line, "+"):
					b.WriteString(styleAdded.Render(line))
				case strings.HasPrefix(line, "-"):
					b.WriteString(styleRemoved.Render(line))
				default:
					b.WriteString(line)
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if len(m.outcomes) == 0 {
		b.WriteString(styleDim.Render("no devices selected"))
		b.WriteString("\n")
	}
	return b.String()
}

// View renders the header, the viewport, and the key help. In v2 the screen
// modes are properties of the view rather than Program options, so the
// altscreen and mouse reporting the scrollable viewport needs are declared
// here instead of at tea.NewProgram.
func (m *DiffModel) View() tea.View {
	answered := len(m.outcomes) - m.pending
	header := styleTitle.Render("barf diff") + styleDim.Render(
		fmt.Sprintf("  %d/%d devices diffed  (read-only)", answered, len(m.outcomes)))

	content := header + "\n" + m.body()
	if m.ready {
		help := styleDim.Render("↑/↓ pgup/pgdn scroll · q quit")
		content = header + "\n" + m.vp.View() + "\n" + help
	}
	// Otherwise: no WindowSizeMsg yet, so print unpaged; a short run still
	// shows something.

	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// Outcomes is the current per-device result set, valid after exit. A device
// whose job never completed (the viewer was quit, or the run was cancelled,
// before its diffDoneMsg arrived) is backfilled as NotRun rather than left a
// zero-value DiffOutcome, which State() would otherwise read as a clean,
// successful compare.
func (m *DiffModel) Outcomes() []DiffOutcome {
	for i, filled := range m.filled {
		if filled {
			continue
		}
		device := m.outcomes[i].Device
		if device == "" && i < len(m.jobs) {
			device = m.jobs[i].Device
		}
		m.outcomes[i] = DiffOutcome{
			Device:  device,
			NotRun:  true,
			Summary: "not run (quit before this device was compared)",
		}
	}
	return m.outcomes
}

// RunDiff drives the diff viewer and returns the per-device outcomes.
func RunDiff(ctx context.Context, jobs []DiffJob) ([]DiffOutcome, error) {
	m := NewDiffModel(ctx, jobs)
	// The program watches the PARENT ctx; the model's own ctx stops the jobs.
	defer m.Cancel()
	// Altscreen and mouse motion are declared by DiffModel.View, not here.
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return m.Outcomes(), err
	}
	if dm, ok := final.(*DiffModel); ok {
		return dm.Outcomes(), nil
	}
	return m.Outcomes(), nil
}
