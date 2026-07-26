package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStatusPlainTable(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{
		status:  DeviceStatus{Version: "1.5-rolling", Uptime: "5d4h", Model: "vyos-vm"},
		running: "set system host-name sea1-vpn-0\n",
	}
	h.readers["fmt2-core"] = fakeReader{
		status:  DeviceStatus{Version: "4.31.2F", Uptime: "12d", Model: "DCS-7050"},
		running: "set system host-name fmt2-core\nset extra thing\n",
	}

	if err := h.run(t, "status"); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	want := strings.Join([]string{
		"DEVICE      ENDPOINT  MODEL     UPTIME  VERSION      CONFIG CONSISTENT  STATUS",
		"----------  --------  --------  ------  -----------  -----------------  ------",
		"sea1-vpn-0  10.0.0.1  vyos-vm   5d4h    1.5-rolling  yes                ok",
		"fmt2-core   10.0.0.2  DCS-7050  12d     4.31.2F      +0 -1              ok",
		"",
	}, "\n")
	if got := h.out.String(); got != want {
		t.Errorf("status table mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStatusSkipsNonReportingDevices(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: "set system host-name sea1-vpn-0\n"}
	h.readers["fmt2-core"] = fakeReader{running: "set system host-name fmt2-core\n"}

	if err := h.run(t, "status"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "ext-peer") {
		t.Errorf("external device appeared in status:\n%s", h.out.String())
	}
}

func TestStatusUnreachableDevice(t *testing.T) {
	h := newHarness(t)
	h.readers["sea1-vpn-0"] = fakeReader{}
	h.readers["fmt2-core"] = fakeReader{}

	if err := h.run(t, "status"); err != nil {
		t.Fatalf("an unreachable device must not fail the command: %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "error: no reachable address") {
		t.Errorf("unreachable device not reported:\n%s", out)
	}
	if strings.Count(out, "error: no reachable address") != 2 {
		t.Errorf("expected both devices unreachable:\n%s", out)
	}
}

func TestStatusDeviceErrorStaysInTheTable(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{statusErr: errors.New("api timeout")}
	h.readers["fmt2-core"] = fakeReader{configErr: errors.New("show run failed")}

	if err := h.run(t, "status"); err != nil {
		t.Fatalf("per-device failures must not fail the command: %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "error: api timeout") {
		t.Errorf("status error missing:\n%s", out)
	}
	if !strings.Contains(out, "error: show run failed") {
		t.Errorf("running-config error missing:\n%s", out)
	}
}

func TestStatusRenderErrorIsPerDevice(t *testing.T) {
	h := newHarness(t)
	h.renderer = fakeRenderer{err: errors.New("missing template")}
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{}
	h.readers["fmt2-core"] = fakeReader{}

	if err := h.run(t, "status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "render error: missing template") {
		t.Errorf("render error not surfaced:\n%s", h.out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{
		status:  DeviceStatus{Version: "1.5", Uptime: "5d", Model: "vm"},
		running: "set system host-name sea1-vpn-0\n",
	}

	if err := h.run(t, "status", "sea1-vpn-0", "--json"); err != nil {
		t.Fatal(err)
	}
	var rows []statusJSON
	if err := json.Unmarshal(h.out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, h.out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Device != "sea1-vpn-0" || rows[0].ConfigConsistent != "yes" || rows[0].State != "ok" {
		t.Errorf("row = %+v", rows[0])
	}
}

func TestStatusSecretBackendFailsFast(t *testing.T) {
	h := newHarness(t)
	h.secretErr = errors.New("vault sealed")
	err := h.run(t, "status")
	if err == nil || !strings.Contains(err.Error(), "vault sealed") {
		t.Fatalf("err = %v, want a vault failure", err)
	}
}

func TestStatusUnknownTarget(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "status", "nope"); err == nil {
		t.Fatal("expected an error for an unknown device")
	}
}

func TestStatusPlainOutputHasNoANSI(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: "set system host-name sea1-vpn-0\n"}
	if err := h.run(t, "status", "sea1-vpn-0"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "\x1b") {
		t.Errorf("plain output contains ANSI escapes: %q", h.out.String())
	}
}
