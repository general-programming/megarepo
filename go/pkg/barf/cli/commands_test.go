package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

func TestListPlain(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "list"); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"DEVICE      TYPE      ROLE      SITE",
		"----------  --------  --------  ----",
		"sea1-vpn-0  vyos      vpn       sea1",
		"fmt2-core   eos       core      fmt2",
		"ext-peer    external  external  sea1",
		"",
	}, "\n")
	if got := h.out.String(); got != want {
		t.Errorf("list mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestListJSON(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "list", "--json"); err != nil {
		t.Fatal(err)
	}
	var rows []listJSON
	if err := json.Unmarshal(h.out.Bytes(), &rows); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, h.out.String())
	}
	if len(rows) != 3 || rows[1].Device != "fmt2-core" || rows[1].Type != "eos" {
		t.Errorf("rows = %+v", rows)
	}
}

func TestListFilters(t *testing.T) {
	h := newHarness(t)
	if err := h.run(t, "list", "ext-peer"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.out.String(), "fmt2-core") {
		t.Errorf("unselected host listed:\n%s", h.out.String())
	}
}

func TestGenerateWritesConfigs(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()

	if err := h.run(t, "generate", "sea1-vpn-0", "-o", dir); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(dir, "vpn", "sea1-vpn-0")
	body, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("main config not written: %v", err)
	}
	if !strings.Contains(string(body), "set system host-name sea1-vpn-0") {
		t.Errorf("config body = %q", body)
	}

	cloud, err := os.ReadFile(filepath.Join(dir, "vpn", "cloud_init", "sea1-vpn-0"))
	if err != nil {
		t.Fatalf("cloud-init twin not written: %v", err)
	}
	if !strings.HasPrefix(string(cloud), "#cloud-config\n") {
		t.Errorf("cloud-init missing its header: %q", cloud)
	}
	if !strings.Contains(string(cloud), "vyos_config_commands:") {
		t.Errorf("cloud-init missing the command list: %q", cloud)
	}

	if !strings.Contains(h.out.String(), "[sea1-vpn-0] wrote "+configPath) {
		t.Errorf("generate did not report the path:\n%s", h.out.String())
	}
}

func TestGenerateSkipsNonTemplatable(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	if err := h.run(t, "generate", "all", "-o", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "external")); !os.IsNotExist(err) {
		t.Error("a non-templatable host was rendered")
	}
}

// Regression: an empty target list used to mean the whole fleet, so a bare
// `barf generate`/`diff` with no arguments silently rendered/probed every
// managed device. It must now be a usage error; "all" is the explicit spelling.
func TestGenerateWithNoTargetsIsAnError(t *testing.T) {
	h := newHarness(t)
	err := h.run(t, "generate", "-o", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no hosts given") {
		t.Fatalf("err = %v, want a no-hosts-given usage error", err)
	}
}

func TestDiffWithNoTargetsIsAnError(t *testing.T) {
	h := newHarness(t)
	err := h.run(t, "diff")
	if err == nil || !strings.Contains(err.Error(), "no hosts given") {
		t.Fatalf("err = %v, want a no-hosts-given usage error", err)
	}
}

func TestGenerateOnlyExternalIsAnError(t *testing.T) {
	h := newHarness(t)
	err := h.run(t, "generate", "ext-peer", "-o", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "no templatable devices") {
		t.Fatalf("err = %v, want a no-templatable-devices error", err)
	}
}

func TestDiffPlain(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: "set system host-name sea1-vpn-0\n"}
	h.readers["fmt2-core"] = fakeReader{running: "set system host-name wrong-name\n"}

	if err := h.run(t, "diff", "all"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if !strings.Contains(out, "--- fmt2-core ---") {
		t.Errorf("drifted device body missing:\n%s", out)
	}
	if !strings.Contains(out, "+ set system host-name fmt2-core") {
		t.Errorf("added line missing:\n%s", out)
	}
	if strings.Contains(out, "--- sea1-vpn-0 ---") {
		t.Errorf("clean device printed a body:\n%s", out)
	}
	if !strings.Contains(out, "sea1-vpn-0  no changes") {
		t.Errorf("summary table missing the clean device:\n%s", out)
	}
	if strings.Contains(out, "\x1b") {
		t.Errorf("plain diff contains ANSI escapes:\n%q", out)
	}
}

// Regression: diff used to select hosts by isTemplatable alone, but several
// templatable vendors (linux, mikrotik, cisco, ...) have no reader at all —
// bare `barf diff` on a fleet with any of them always emitted a failed row
// and exited 1. A no-reader vendor must be an explicit skip, matching
// Python's diff_config raising NotImplementedError -> "skipped: no diff
// support", and must not fail the run or the exit code.
func TestDiffSkipsTemplatableVendorsWithNoReader(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: "set system host-name sea1-vpn-0\n"}
	h.readers["fmt2-core"] = fakeReader{running: "set system host-name fmt2-core\n"}
	// mikrotik: isTemplatable true (the fake mirrors the real vendor table),
	// reportsStatus false (real barf has no MikroTik reader either).
	h.net.Hosts = append(h.net.Hosts, model.Host{Hostname: "sea1-mikrotik-0", DeviceType: "mikrotik", Role: "vpn", Site: "sea1"})

	if err := h.run(t, "diff", "all"); err != nil {
		t.Fatalf("a skip must not fail the run: %v\n%s", err, h.out.String())
	}
	out := h.out.String()
	if !strings.Contains(out, "sea1-mikrotik-0  skipped: no diff support") {
		t.Errorf("skip row missing or misformatted:\n%s", out)
	}
}

func TestDiffFailureExitsNonZero(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{configErr: errors.New("boom")}
	h.readers["fmt2-core"] = fakeReader{}

	err := h.run(t, "diff", "all")
	if err == nil {
		t.Fatal("a failed diff must be an error exit")
	}
	// Every device still appears in the summary table.
	if !strings.Contains(h.out.String(), "failed: boom") {
		t.Errorf("failure not in the table:\n%s", h.out.String())
	}
	if !strings.Contains(h.out.String(), "fmt2-core") {
		t.Errorf("other devices were skipped:\n%s", h.out.String())
	}
}

func TestDiffNeverWritesToTheDevice(t *testing.T) {
	// Pins DeviceReader's method set so a write verb cannot be added
	// without failing here.
	var r DeviceReader = fakeReader{}
	switch any(r).(type) {
	case interface{ Push(string) error }:
		t.Fatal("DeviceReader gained a write method")
	}
}

func TestInteractiveDetection(t *testing.T) {
	o := &Options{Out: os.Stdout, ErrOut: os.Stderr}
	o.Plain = true
	if o.interactive() {
		t.Error("--plain must force plain output")
	}

	// A pipe is never a terminal.
	o = &Options{Out: new(strings.Builder), ErrOut: os.Stderr}
	if o.interactive() {
		t.Error("a non-file writer must not be interactive")
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Skip("no pipes available")
	}
	defer r.Close()
	defer w.Close()
	o = &Options{Out: w, ErrOut: os.Stderr}
	if o.interactive() {
		t.Error("an os.Pipe must not be detected as a terminal")
	}
}
