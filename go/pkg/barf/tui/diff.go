package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// DiffOutcome is one device's diff between its rendered config and the
// config it is actually running. Err is a per-device failure; it never
// aborts the other devices.
type DiffOutcome struct {
	Device  string
	Text    string
	Summary string
	Err     error
}

// State classifies an outcome for colouring and for the summary table.
func (o DiffOutcome) State() RowState {
	switch {
	case o.Err != nil:
		return StateError
	case strings.TrimSpace(o.Text) != "":
		return StateDrift
	default:
		return StateOK
	}
}

// DiffJob renders and diffs one device. Run must not write to the
// device; `barf diff` is a read-only command.
type DiffJob struct {
	Device string
	Run    func(ctx context.Context) DiffOutcome
}

type diffDoneMsg struct {
	index   int
	outcome DiffOutcome
}

// DiffModel streams per-device diffs into a scrollable viewport as they
// arrive.
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

	// concurrency caps in-flight jobs, and so the goroutine count:
	// tea.Batch runs one goroutine per command.
	concurrency int
	// next is the index of the first job not yet dispatched; Update-only.
	next int
}

// NewDiffModel builds the diff viewer for the given jobs.
//
// The model derives a cancellable context from ctx; call Cancel (RunDiff
// does) to release jobs that are still queued or in flight.
func NewDiffModel(ctx context.Context, jobs []DiffJob) *DiffModel {
	if ctx == nil {
		ctx = context.Background()
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = stylePending

	jobCtx, cancel := context.WithCancel(ctx)
	m := &DiffModel{
		ctx:         jobCtx,
		cancel:      cancel,
		jobs:        jobs,
		outcomes:    make([]DiffOutcome, len(jobs)),
		filled:      make([]bool, len(jobs)),
		pending:     len(jobs),
		vp:          viewport.New(80, 20),
		spin:        sp,
		concurrency: DefaultConcurrency,
	}
	for i, j := range jobs {
		m.outcomes[i] = DiffOutcome{Device: j.Device}
	}
	m.vp.SetContent(m.body())
	return m
}

// SetConcurrency caps how many jobs run at once. Values below 1 are
// ignored. Call it before Init.
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

// Init starts the spinner and the first batch of diff jobs. Only
// `concurrency` are launched; each completion dispatches the next, so a
// 300-device run does not become 300 goroutines.
func (m *DiffModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, m.concurrency+1)
	cmds = append(cmds, m.spin.Tick)
	for m.next < len(m.jobs) && m.next < m.concurrency {
		cmds = append(cmds, m.jobCmd(m.next))
		m.next++
	}
	return tea.Batch(cmds...)
}

// dispatchNext returns the command for the next undispatched job, or nil
// when they have all been started.
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
		m.vp.Width = msg.Width
		m.vp.Height = max(msg.Height-3, 3)
		m.ready = true
		m.vp.SetContent(m.body())

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			// Cancel as well as quit, or `runDiff` would print its summary
			// while queued jobs kept contacting devices.
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

// View renders the header, the viewport, and the key help.
func (m *DiffModel) View() string {
	answered := len(m.outcomes) - m.pending
	header := styleTitle.Render("barf diff") + styleDim.Render(
		fmt.Sprintf("  %d/%d devices diffed  (read-only)", answered, len(m.outcomes)))

	if !m.ready {
		// No WindowSizeMsg yet: print the document unpaged so a very
		// short-lived run still shows something useful.
		return header + "\n" + m.body()
	}
	help := styleDim.Render("↑/↓ pgup/pgdn scroll · q quit")
	return header + "\n" + m.vp.View() + "\n" + help
}

// Outcomes is the current per-device result set, valid after exit.
func (m *DiffModel) Outcomes() []DiffOutcome { return m.outcomes }

// RunDiff drives the diff viewer and returns the per-device outcomes.
func RunDiff(ctx context.Context, jobs []DiffJob) ([]DiffOutcome, error) {
	m := NewDiffModel(ctx, jobs)
	// The program watches the PARENT context; the model's own context is
	// what stops the jobs.
	defer m.Cancel()
	final, err := tea.NewProgram(m,
		tea.WithContext(ctx),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	).Run()
	if err != nil {
		return m.Outcomes(), err
	}
	if dm, ok := final.(*DiffModel); ok {
		return dm.Outcomes(), nil
	}
	return m.Outcomes(), nil
}
