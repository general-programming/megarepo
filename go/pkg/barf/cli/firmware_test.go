package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/firmware"
	"github.com/general-programming/megarepo/go/pkg/barf/tui"
)

// Stubs the firmware release lookup package-wide so no test reaches the
// vendor's release feed.
func TestMain(m *testing.M) {
	newFirmwareChecker = offlineChecker(nil)
	os.Exit(m.Run())
}

// offlineChecker builds a Checker seeded with fixed versions and no
// network. A nil map reports everything unknown, i.e. an unreachable feed.
func offlineChecker(latest map[string]string) func(context.Context, firmware.WarnLogger, ...string) *firmware.Checker {
	return func(ctx context.Context, _ firmware.WarnLogger, deviceTypes ...string) *firmware.Checker {
		c := &firmware.Checker{}
		for dt, version := range latest {
			c.Set(dt, version)
		}
		return c
	}
}

func withFirmware(t *testing.T, latest map[string]string) {
	t.Helper()
	previous := newFirmwareChecker
	newFirmwareChecker = offlineChecker(latest)
	t.Cleanup(func() { newFirmwareChecker = previous })
}

func TestStatusLatestFirmwareColumn(t *testing.T) {
	// Real fleet values: the VyOS box is a release behind; the EOS box has
	// no image provider, so it shows "?" like Python.
	withFirmware(t, map[string]string{"vyos": "2026.07.21-1151-rolling"})

	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{
		status:  DeviceStatus{Version: "2026.07.11-0033-rolling", Uptime: "5d4h", Model: "vyos-vm"},
		running: "set system host-name sea1-vpn-0\n",
	}
	h.readers["fmt2-core"] = fakeReader{
		status:  DeviceStatus{Version: "4.31.2F", Uptime: "12d", Model: "DCS-7050"},
		running: "set system host-name fmt2-core\n",
	}

	if err := h.run(t, "status"); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	out := h.out.String()

	if !strings.Contains(out, firmwareColumnName) {
		t.Fatalf("no LATEST FIRMWARE column:\n%s", out)
	}
	if !strings.Contains(out, "no (2026.07.21-1151-rolling)") {
		t.Errorf("the behind VyOS box should report the latest release:\n%s", out)
	}
	eos := rowFields(t, out, "fmt2-core")
	if eos[4] != "4.31.2F" || eos[5] != "?" || eos[6] != "yes" {
		t.Errorf("a vendor without an image provider should show ?: %v", eos)
	}
}

// rowFields splits a device's row on whitespace runs, so assertions do
// not depend on column padding.
func rowFields(t *testing.T, table, device string) []string {
	t.Helper()
	for _, line := range strings.Split(table, "\n") {
		if strings.HasPrefix(line, device+" ") {
			return strings.Fields(line)
		}
	}
	t.Fatalf("no row for %s in:\n%s", device, table)
	return nil
}

func TestStatusFirmwareCurrentAndUnknown(t *testing.T) {
	withFirmware(t, map[string]string{"vyos": "2026.07.21-1151-rolling"})

	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{
		status:  DeviceStatus{Version: "2026.07.21-1151-rolling", Uptime: "1h", Model: "vyos-vm"},
		running: "set system host-name sea1-vpn-0\n",
	}
	if err := h.run(t, "status", "sea1-vpn-0"); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(h.out.String(), "yes") {
		t.Errorf("a current device should report yes:\n%s", h.out.String())
	}

	// An unreachable release feed degrades to "?" rather than failing.
	withFirmware(t, nil)
	h2 := newHarness(t)
	h2.reachable["10.0.0.1"] = true
	h2.readers["sea1-vpn-0"] = fakeReader{
		status:  DeviceStatus{Version: "2026.07.11-0033-rolling", Uptime: "1h", Model: "vyos-vm"},
		running: "set system host-name sea1-vpn-0\n",
	}
	if err := h2.run(t, "status", "sea1-vpn-0"); err != nil {
		t.Fatalf("status with no release feed failed: %v", err)
	}
	if !strings.Contains(h2.out.String(), "?") {
		t.Errorf("an unreachable feed should degrade to ?:\n%s", h2.out.String())
	}
}

func TestFirmwareColumnSplicedAfterVersion(t *testing.T) {
	cols := statusColumnsWithFirmware()
	if len(cols) != len(tui.StatusColumns)+1 {
		t.Fatalf("columns = %v", cols)
	}
	var at int
	for i, c := range cols {
		if c == firmwareColumnName {
			at = i
		}
	}
	if cols[at-1] != "VERSION" || cols[at+1] != "CONFIG CONSISTENT" {
		t.Fatalf("LATEST FIRMWARE is in the wrong place: %v", cols)
	}

	// A row's cells must land at the same offset as the header.
	row := tui.StatusRow{
		Device: "d", Endpoint: "e", Model: "m", Uptime: "u",
		Version: "v", Consistent: "c", Status: "s",
	}
	cells := spliceFirmwareCell(row.Cells(), "yes")
	if len(cells) != len(cols) {
		t.Fatalf("cells %v do not line up with columns %v", cells, cols)
	}
	if cells[at] != "yes" || cells[at-1] != "v" {
		t.Fatalf("firmware cell landed at the wrong offset: %v", cells)
	}
}

func TestSpliceFallsBackToAppend(t *testing.T) {
	// A column set without VERSION must not drop a cell.
	got := spliceAfterVersion([]string{"DEVICE", "STATUS"}, firmwareColumnName)
	if len(got) != 3 || got[2] != firmwareColumnName {
		t.Fatalf("spliceAfterVersion = %v", got)
	}
}
