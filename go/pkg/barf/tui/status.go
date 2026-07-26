package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	ltable "charm.land/lipgloss/v2/table"
)

// StatusColumns are the `barf status` headers in Python's order, less firmware.
var StatusColumns = []string{
	"DEVICE", "ENDPOINT", "MODEL", "UPTIME", "VERSION", "CONFIG CONSISTENT", "STATUS",
}

// StatusRow is one device's line in the status table.
type StatusRow struct {
	Device     string
	Endpoint   string
	Model      string
	Uptime     string
	Version    string
	Consistent string
	Status     string
	State      RowState
}

// PendingRow is the placeholder for an unanswered device; the spinner fills
// in the STATUS cell.
func PendingRow(device string) StatusRow {
	return StatusRow{
		Device:     device,
		Endpoint:   "…",
		Model:      "…",
		Uptime:     "…",
		Version:    "…",
		Consistent: "…",
		Status:     "probing",
		State:      StatePending,
	}
}

// Cells is the row as plain strings, in StatusColumns order.
func (r StatusRow) Cells() []string {
	return []string{r.Device, r.Endpoint, r.Model, r.Uptime, r.Version, r.Consistent, r.Status}
}

// StatusProbe is one device's probe. Run must return a populated row rather
// than an error: per-device failures belong in the table, not the exit path.
type StatusProbe struct {
	Device string
	Run    func(ctx context.Context) StatusRow
}

// statusDoneMsg reports one finished probe back to the model.
type statusDoneMsg struct {
	index int
	row   StatusRow
}

// StatusModel renders the status table live: every device is on screen from
// the first frame "probing", and rows are replaced as their probes answer.
type StatusModel struct {
	ctx     context.Context
	cancel  context.CancelFunc
	probes  []StatusProbe
	rows    []StatusRow
	spin    spinner.Model
	pending int
	width   int
	done    bool

	concurrency int // caps in-flight probes: tea.Batch is one goroutine per cmd
	next        int // first probe not yet dispatched; Update-only, as is all state
}

// NewStatusModel builds the live status model, deriving a cancellable context
// from ctx; call Cancel (RunStatus does) to release probes still parked.
func NewStatusModel(ctx context.Context, probes []StatusProbe) *StatusModel {
	if ctx == nil {
		ctx = context.Background()
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(stylePending))

	rows := make([]StatusRow, len(probes))
	for i, p := range probes {
		rows[i] = PendingRow(p.Device)
	}
	probeCtx, cancel := context.WithCancel(ctx)
	return &StatusModel{
		ctx:         probeCtx,
		cancel:      cancel,
		probes:      probes,
		rows:        rows,
		spin:        sp,
		pending:     len(probes),
		width:       0,
		done:        len(probes) == 0,
		concurrency: DefaultConcurrency,
	}
}

// SetConcurrency caps how many probes run at once; call it before Init.
func (m *StatusModel) SetConcurrency(n int) {
	if n > 0 {
		m.concurrency = n
	}
}

// Cancel releases the probes' context. Safe to call more than once.
func (m *StatusModel) Cancel() {
	if m.cancel != nil {
		m.cancel()
	}
}

// Init starts the spinner and dispatches the first `concurrency` probes; each
// completion dispatches the next, bounding goroutines regardless of fleet size.
func (m *StatusModel) Init() tea.Cmd {
	cmds := make([]tea.Cmd, 0, m.concurrency+1)
	cmds = append(cmds, m.spin.Tick)
	for m.next < len(m.probes) && m.next < m.concurrency {
		cmds = append(cmds, m.probeCmd(m.next))
		m.next++
	}
	return tea.Batch(cmds...)
}

// dispatchNext returns the next undispatched probe's command, nil when done.
func (m *StatusModel) dispatchNext() tea.Cmd {
	if m.next >= len(m.probes) {
		return nil
	}
	cmd := m.probeCmd(m.next)
	m.next++
	return cmd
}

func (m *StatusModel) probeCmd(i int) tea.Cmd {
	probe := m.probes[i]
	return func() tea.Msg {
		return statusDoneMsg{index: i, row: probe.Run(m.ctx)}
	}
}

// Update handles probe completions, the spinner tick, and quit keys.
func (m *StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case tea.KeyPressMsg:
		// v2 made tea.KeyMsg an interface covering presses AND releases;
		// matching it would fire the quit path twice on terminals that report
		// releases. Only a press quits.
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.done = true
			// tea.Quit only stops the UI; without Cancel, in-flight probes
			// keep contacting devices while the summary prints.
			m.Cancel()
			return m, tea.Quit
		}
		return m, nil

	case statusDoneMsg:
		if msg.index >= 0 && msg.index < len(m.rows) {
			if m.rows[msg.index].State == StatePending {
				m.pending--
			}
			m.rows[msg.index] = msg.row
		}
		if m.pending <= 0 {
			m.done = true
			m.Cancel()
			// The finished table is the output: leave it on screen and exit.
			return m, tea.Quit
		}
		return m, m.dispatchNext()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

// View renders the table plus a one-line progress header. Status runs inline
// (no altscreen), so the finished table is left on screen when the program
// exits; the zero tea.View fields are exactly that.
func (m *StatusModel) View() tea.View {
	answered := len(m.rows) - m.pending
	header := styleTitle.Render("barf status") + styleDim.Render(
		fmt.Sprintf("  %d/%d devices reported", answered, len(m.rows)))

	rows := make([][]string, 0, len(m.rows))
	states := make([]RowState, 0, len(m.rows))
	for _, r := range m.rows {
		cells := r.Cells()
		if r.State == StatePending {
			cells[len(cells)-1] = m.spin.View() + " " + r.Status
		}
		rows = append(rows, cells)
		states = append(states, r.State)
	}

	t := ltable.New().
		Border(lipgloss.NormalBorder()).
		BorderColumn(false).
		BorderLeft(false).
		BorderRight(false).
		BorderTop(false).
		BorderBottom(false).
		BorderStyle(styleDim).
		Headers(StatusColumns...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().PaddingRight(2)
			// lipgloss/table numbers the header row -1.
			if row < 0 || row >= len(states) {
				return base.Inherit(styleHeader)
			}
			return base.Inherit(styleFor(states[row]))
		})
	if m.width > 0 {
		t = t.Width(m.width)
	}

	var b strings.Builder
	b.WriteString(header)
	b.WriteString("\n")
	b.WriteString(t.Render())
	b.WriteString("\n")
	if !m.done {
		b.WriteString(styleDim.Render("q to quit"))
		b.WriteString("\n")
	}
	return tea.NewView(b.String())
}

// Rows is the current table contents, valid after the program exits.
func (m *StatusModel) Rows() []StatusRow { return m.rows }

// Done reports whether every probe has answered (or the user quit).
func (m *StatusModel) Done() bool { return m.done }

// RunStatus drives the live status table to completion and returns the final
// rows; probes the user quit before are left pending.
func RunStatus(ctx context.Context, probes []StatusProbe) ([]StatusRow, error) {
	m := NewStatusModel(ctx, probes)
	// The program watches the PARENT ctx; the model's own ctx stops the probes.
	defer m.Cancel()
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return m.Rows(), err
	}
	if sm, ok := final.(*StatusModel); ok {
		return sm.Rows(), nil
	}
	return m.Rows(), nil
}
