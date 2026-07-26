package cli

import (
	"context"

	"github.com/general-programming/megarepo/go/pkg/barf/firmware"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/tui"
)

// The LATEST FIRMWARE column of `barf status`.
//
// Python prints it between VERSION and CONFIG CONSISTENT, filled from
// the vendor's image provider: "yes" when the device runs the newest
// release, "no (<latest>)" when it is behind, and "?" for a vendor with
// no provider (everything except VyOS today) or when the release feed
// could not be reached.
//
// It lives here rather than in tui.StatusRow so the column is additive:
// the plain and --json paths splice it in, and the live TUI table is
// unchanged.
//
// TODO(firmware-tui): the live TUI table still omits this column,
// because tui.StatusRow has no field for it. The exact change, once the
// tui package is free to edit:
//
//  1. tui/status.go StatusColumns: insert "LATEST FIRMWARE" after "VERSION"
//  2. tui/status.go StatusRow: add `Firmware string` after Version
//  3. tui/status.go Cells(): insert r.Firmware after r.Version
//  4. tui/status.go PendingRow(): add Firmware: "…"
//  5. cli/status.go statusProbe(): row.Firmware = fw.Cell(h.DeviceType, st.Version)
//     with the Checker primed before the probe pool starts, and errorRow
//     gaining one more "-".
//
// At that point firmwareCells and the splice helpers below go away.

// firmwareColumnName is the header Python prints.
const firmwareColumnName = "LATEST FIRMWARE"

// newFirmwareChecker is the seam the release lookup goes through, in the
// same spirit as deps.go: tests replace it so no unit test ever reaches
// the network for a firmware release.
var newFirmwareChecker = firmware.NewChecker

// statusColumnsWithFirmware is tui.StatusColumns with LATEST FIRMWARE
// spliced back in after VERSION.
func statusColumnsWithFirmware() []string {
	return spliceAfterVersion(tui.StatusColumns, firmwareColumnName)
}

// spliceAfterVersion inserts value immediately after the VERSION column.
// A table without a VERSION column gets the value appended, so this can
// never drop a cell if the column set changes underneath it.
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

// versionColumnIndex is where VERSION sits in tui.StatusColumns, so a
// row's cells can be spliced at the same offset as the headers.
func versionColumnIndex() int {
	for i, c := range tui.StatusColumns {
		if c == "VERSION" {
			return i
		}
	}
	return len(tui.StatusColumns) - 1
}

// spliceFirmwareCell inserts a row's firmware cell after its version cell.
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

// firmwareCells is the LATEST FIRMWARE cell for every probed device,
// keyed by hostname.
//
// The release feed is fetched once for the whole table, and an
// unreachable feed is not fatal: it is logged and every cell reads "?",
// exactly as the Python implementation does.
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

// warnLogger is the slice of the command logger firmwareCells needs.
// charmbracelet/log types its message as any, so it is adapted onto the
// firmware package's slog-shaped interface below.
type warnLogger interface {
	Warn(msg any, keyvals ...any)
}

type charmWarnAdapter struct{ log warnLogger }

func (a charmWarnAdapter) Warn(msg string, args ...any) { a.log.Warn(msg, args...) }
