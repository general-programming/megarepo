package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// fakeWriter records ops instead of sending them; no test contacts a device.
type fakeWriter struct {
	mu         sync.Mutex
	configured [][]ConfigOp
	saved      int
	configErr  error
	saveErr    error
}

func (f *fakeWriter) Configure(_ context.Context, ops []ConfigOp) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.configErr != nil {
		return f.configErr
	}
	f.configured = append(f.configured, ops)
	return nil
}

func (f *fakeWriter) SaveConfig(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.saved++
	return nil
}

func installWriters(t *testing.T, writers map[string]*fakeWriter) {
	t.Helper()
	old := writerFactories
	t.Cleanup(func() { writerFactories = old })

	writerFactories = map[string]writerFactory{
		"vyos": func(h *model.Host, _ string, _ SecretSource) (DeviceWriter, error) {
			w, ok := writers[h.Hostname]
			if !ok {
				return nil, errors.New("no writer configured for " + h.Hostname)
			}
			return w, nil
		},
	}
}

// A `/retrieve` tree: host-name already correct, plus a stray name-server
// and an lldp service the render does not carry.
const vyosDeviceJSON = `{
  "system": {"host-name": "sea1-vpn-0", "name-server": ["8.8.8.8"]},
  "service": {"lldp": {"interface": {"all": {}}}},
  "interfaces": {"ethernet": {"eth0": {"hw-id": "aa:bb:cc:dd:ee:ff"}}}
}`

func deployHarness(t *testing.T, writers map[string]*fakeWriter) *harness {
	t.Helper()
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: vyosDeviceJSON}
	installWriters(t, writers)
	return h
}

// Safety invariant: no --yes, no writes, not even a constructed writer.
func TestDeployDryRunIsTheDefault(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

	if err := h.run(t, "deploy", "sea1-vpn-0"); err != nil {
		t.Fatal(err)
	}

	if len(w.configured) != 0 || w.saved != 0 {
		t.Fatalf("dry run wrote to the device: %v saves=%d", w.configured, w.saved)
	}
	out := h.out.String()
	if !strings.Contains(out, "DRY RUN") {
		t.Errorf("dry run not announced:\n%s", out)
	}
	if !strings.Contains(out, "(dry run)") {
		t.Errorf("summary row missing the dry-run marker:\n%s", out)
	}
	if !strings.Contains(out, "delete service") {
		t.Errorf("planned delete not shown:\n%s", out)
	}
	// host-name survives under "system", so the collapse rises to the
	// name-server node and no further.
	if !strings.Contains(out, "delete system name-server\n") {
		t.Errorf("planned collapsed delete not shown:\n%s", out)
	}
	if strings.Contains(out, "delete system\n") {
		t.Errorf("collapse swallowed surviving config:\n%s", out)
	}
}

// --yes on a non-TTY applies without prompting.
func TestDeployWritesWithYes(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

	if err := h.run(t, "deploy", "sea1-vpn-0", "--yes"); err != nil {
		t.Fatal(err)
	}

	if len(w.configured) != 1 {
		t.Fatalf("configure calls = %d, want 1 atomic call", len(w.configured))
	}
	if w.saved != 1 {
		t.Fatalf("saves = %d, want 1", w.saved)
	}

	ops := w.configured[0]
	// Deletes before sets in one commit, per VyOSHost.push_rendered_config.
	var seenSet bool
	for _, op := range ops {
		switch op.Verb {
		case opDelete:
			if seenSet {
				t.Fatalf("a delete followed a set: %v", ops)
			}
		case opSet:
			seenSet = true
		default:
			t.Fatalf("unexpected verb %q", op.Verb)
		}
	}
	if !hasOp(ops, opDelete, "service") {
		t.Errorf("collapsed delete missing: %v", ops)
	}
	for _, op := range ops {
		if strings.Contains(strings.Join(op.Path, " "), "hw-id") {
			t.Fatalf("ignored hw-id path reached the device: %v", op)
		}
	}
}

func hasOp(ops []ConfigOp, verb string, path ...string) bool {
	for _, op := range ops {
		if op.Verb == verb && strings.Join(op.Path, " ") == strings.Join(path, " ") {
			return true
		}
	}
	return false
}

// A device already in the rendered state is not touched, even with --yes.
func TestDeployNoChangesSkips(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})
	// Exactly what fakeRenderer produces for this host.
	h.readers["sea1-vpn-0"] = fakeReader{running: `{"system": {"host-name": "sea1-vpn-0"}}`}

	if err := h.run(t, "deploy", "sea1-vpn-0", "--yes"); err != nil {
		t.Fatal(err)
	}
	if len(w.configured) != 0 || w.saved != 0 {
		t.Fatalf("a clean device was written to: %v", w.configured)
	}
	if !strings.Contains(h.out.String(), "no changes") {
		t.Errorf("no-changes not reported:\n%s", h.out.String())
	}
}

// EOS has no writer here: the run is refused, not half-applied.
func TestDeployRefusesUnsupportedDeviceType(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

	err := h.run(t, "deploy", "--yes")
	if err == nil {
		t.Fatal("want an error for the eos host")
	}
	if !strings.Contains(err.Error(), "no write implementation") {
		t.Fatalf("err = %v", err)
	}
	if len(w.configured) != 0 {
		t.Fatalf("a refused run still wrote to the deployable host: %v", w.configured)
	}
}

func TestDeployRedaction(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})
	h.renderer = fakeRenderer{text: "set interfaces wireguard wg1 private-key SUPERSECRET\n"}

	if err := h.run(t, "deploy", "sea1-vpn-0"); err != nil {
		t.Fatal(err)
	}
	out := h.out.String()
	if strings.Contains(out, "SUPERSECRET") {
		t.Fatalf("secret leaked into deploy output:\n%s", out)
	}
	if !strings.Contains(out, "<redacted>") {
		t.Fatalf("secret not redacted:\n%s", out)
	}

	h.out.Reset()
	if err := h.run(t, "deploy", "sea1-vpn-0", "--show-secrets"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.out.String(), "SUPERSECRET") {
		t.Fatalf("--show-secrets did not reveal the value:\n%s", h.out.String())
	}
	// Redaction is display-only: the real op still carries the value.
	if len(w.configured) != 0 {
		t.Fatal("a dry run wrote")
	}
}

// On a TTY without --yes the prompt is the confirmation: y applies in the
// same invocation, anything else means no.
func TestDeployInteractiveConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		answer    string
		wantWrite bool
	}{
		{"y applies", "y\n", true},
		{"yes applies", "yes\n", true},
		{"n skips", "n\n", false},
		{"empty skips", "\n", false},
		{"garbage skips", "maybe\n", false},
		{"EOF skips", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

			o := h.options()
			o.SetInteractive(true)
			opts := DeployOptions{In: strings.NewReader(tc.answer)}
			if err := runDeploy(context.Background(), o, []string{"sea1-vpn-0"}, opts); err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(h.out.String(), "[y/N]") {
				t.Fatalf("no prompt was shown:\n%s", h.out.String())
			}
			wrote := len(w.configured) > 0
			if wrote != tc.wantWrite {
				t.Fatalf("wrote = %v, want %v (output: %s)", wrote, tc.wantWrite, h.out.String())
			}
			if !tc.wantWrite && !strings.Contains(h.out.String(), "skipped") {
				t.Errorf("skip not reported:\n%s", h.out.String())
			}
			// A declined device is not a dry run: the operator answered.
			if !tc.wantWrite && strings.Contains(h.out.String(), "DRY RUN") {
				t.Errorf("a declined prompt was reported as a dry run:\n%s", h.out.String())
			}
		})
	}
}

// --yes on a TTY is the scripting escape hatch: stdin must not be read.
func TestDeployInteractiveYesSkipsThePrompt(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

	o := h.options()
	o.SetInteractive(true)
	opts := DeployOptions{Yes: true, In: errReader{}}
	if err := runDeploy(context.Background(), o, []string{"sea1-vpn-0"}, opts); err != nil {
		t.Fatal(err)
	}
	if len(w.configured) != 1 {
		t.Fatalf("--yes on a TTY did not apply: %v", w.configured)
	}
	if strings.Contains(h.out.String(), "[y/N]") {
		t.Fatalf("--yes still prompted:\n%s", h.out.String())
	}
}

// With no TTY the reader is never consulted, so an unattended run cannot
// hang; without --yes nothing is written.
func TestDeployPlainNeverPrompts(t *testing.T) {
	t.Run("with --yes it applies", func(t *testing.T) {
		w := &fakeWriter{}
		h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

		o := h.options() // forced non-interactive
		opts := DeployOptions{Yes: true, In: errReader{}}
		if err := runDeploy(context.Background(), o, []string{"sea1-vpn-0"}, opts); err != nil {
			t.Fatal(err)
		}
		if len(w.configured) != 1 {
			t.Fatalf("plain --yes did not apply: %v", w.configured)
		}
		if strings.Contains(h.out.String(), "[y/N]") {
			t.Fatalf("plain mode prompted:\n%s", h.out.String())
		}
	})

	// Never weaken: no terminal and no --yes changes nothing, and never
	// looks for an answer on stdin.
	t.Run("without --yes it is a dry run", func(t *testing.T) {
		w := &fakeWriter{}
		h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

		o := h.options()
		opts := DeployOptions{In: errReader{}}
		if err := runDeploy(context.Background(), o, []string{"sea1-vpn-0"}, opts); err != nil {
			t.Fatal(err)
		}
		if len(w.configured) != 0 || w.saved != 0 {
			t.Fatalf("a non-TTY run without --yes wrote: %v", w.configured)
		}
		out := h.out.String()
		if strings.Contains(out, "[y/N]") {
			t.Fatalf("plain mode prompted:\n%s", out)
		}
		if !strings.Contains(out, "DRY RUN: nothing was written") {
			t.Fatalf("dry-run line missing:\n%s", out)
		}
	})
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("stdin must not be read in plain mode")
}

func TestDeploySkipSave(t *testing.T) {
	w := &fakeWriter{}
	h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

	if err := h.run(t, "deploy", "sea1-vpn-0", "--yes", "--skip-save"); err != nil {
		t.Fatal(err)
	}
	if len(w.configured) != 1 {
		t.Fatalf("configure calls = %d", len(w.configured))
	}
	if w.saved != 0 {
		t.Fatalf("--skip-save still saved")
	}
}

func TestDeployReportsDeviceErrors(t *testing.T) {
	t.Run("configure fails", func(t *testing.T) {
		w := &fakeWriter{configErr: errors.New("commit failed")}
		h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})

		err := h.run(t, "deploy", "sea1-vpn-0", "--yes")
		if err == nil {
			t.Fatal("want an error")
		}
		if w.saved != 0 {
			t.Fatal("saved after a failed commit")
		}
	})

	t.Run("unreachable device", func(t *testing.T) {
		w := &fakeWriter{}
		h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})
		h.reachable = map[string]bool{}

		if err := h.run(t, "deploy", "sea1-vpn-0", "--yes"); err == nil {
			t.Fatal("want an error")
		}
		if len(w.configured) != 0 {
			t.Fatal("wrote to an unreachable device")
		}
	})

	t.Run("unparseable running config", func(t *testing.T) {
		w := &fakeWriter{}
		h := deployHarness(t, map[string]*fakeWriter{"sea1-vpn-0": w})
		h.readers["sea1-vpn-0"] = fakeReader{running: "!!! not a config !!!"}

		if err := h.run(t, "deploy", "sea1-vpn-0", "--yes"); err == nil {
			t.Fatal("want an error rather than an empty running config")
		}
		if len(w.configured) != 0 {
			t.Fatal("wrote from an unparseable running config")
		}
	})
}

// A build that never imported the writer wiring cannot deploy.
func TestDeployWithoutWiringCannotWrite(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: vyosDeviceJSON}

	old := writerFactories
	t.Cleanup(func() { writerFactories = old })
	writerFactories = map[string]writerFactory{}

	if err := h.run(t, "deploy", "sea1-vpn-0", "--yes"); err == nil {
		t.Fatal("want an error with no writers registered")
	}
}
