package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/firmware"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/tui"
)

// The LATEST FIRMWARE column of `barf status`, between VERSION and CONFIG
// CONSISTENT as Python does: "yes", "no (<latest>)", or "?" for a vendor with
// no image provider or an unreachable feed. Spliced in by the plain and
// --json paths, so the live TUI table still omits the column.
// TODO(firmware-tui): give tui.StatusRow a Firmware field and drop these.

const firmwareColumnName = "LATEST FIRMWARE"

// newFirmwareChecker is the seam keeping unit tests off the network.
var newFirmwareChecker = firmware.NewChecker

// statusColumnsWithFirmware splices LATEST FIRMWARE back in after VERSION.
func statusColumnsWithFirmware() []string {
	return spliceAfterVersion(tui.StatusColumns, firmwareColumnName)
}

// spliceAfterVersion appends when there is no VERSION column, so it can never
// drop a cell if the column set changes underneath it.
func spliceAfterVersion(row []string, value string) []string {
	at := len(row)
	for i, c := range row {
		if c == "VERSION" {
			at = i + 1
			break
		}
	}
	out := make([]string, 0, len(row)+1)
	out = append(out, row[:at]...)
	out = append(out, value)
	out = append(out, row[at:]...)
	return out
}

// versionColumnIndex is where VERSION sits in tui.StatusColumns.
func versionColumnIndex() int {
	for i, c := range tui.StatusColumns {
		if c == "VERSION" {
			return i
		}
	}
	return len(tui.StatusColumns) - 1
}

func spliceFirmwareCell(cells []string, cell string) []string {
	at := versionColumnIndex() + 1
	if at > len(cells) {
		at = len(cells)
	}
	out := make([]string, 0, len(cells)+1)
	out = append(out, cells[:at]...)
	out = append(out, cell)
	out = append(out, cells[at:]...)
	return out
}

// firmwareCells keys the LATEST FIRMWARE cell by hostname. The feed is
// fetched once per table and, as in Python, an unreachable feed is logged
// rather than fatal: every cell reads "?".
func firmwareCells(ctx context.Context, log warnLogger, hosts []*model.Host, rows []tui.StatusRow) map[string]string {
	deviceTypes := make([]string, 0, len(hosts))
	byHost := make(map[string]string, len(hosts))
	for _, h := range hosts {
		if h == nil {
			continue
		}
		deviceTypes = append(deviceTypes, h.DeviceType)
		byHost[h.Hostname] = h.DeviceType
	}

	var warner firmware.WarnLogger
	if log != nil {
		warner = charmWarnAdapter{log}
	}
	checker := newFirmwareChecker(ctx, warner, deviceTypes...)

	cells := make(map[string]string, len(rows))
	for _, r := range rows {
		cells[r.Device] = checker.Cell(byHost[r.Device], r.Version)
	}
	return cells
}

// warnLogger adapts charmbracelet/log (message typed as any) onto the
// firmware package's slog-shaped interface.
type warnLogger interface {
	Warn(msg any, keyvals ...any)
}

type charmWarnAdapter struct{ log warnLogger }

func (a charmWarnAdapter) Warn(msg string, args ...any) { a.log.Warn(msg, args...) }
