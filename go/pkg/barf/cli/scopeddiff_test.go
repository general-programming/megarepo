package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/scope"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// fakeScopedReader answers a scoped comparison. RunningConfig fails
// loudly: a vendor with a managed slice must never have its whole config
// pulled for a diff.
type fakeScopedReader struct {
	diff   ConfigDiff
	err    error
	called bool
	opts   DiffOptions
}

func (f *fakeScopedReader) Status(context.Context) (DeviceStatus, error) {
	return DeviceStatus{}, nil
}

func (f *fakeScopedReader) RunningConfig(context.Context) (string, error) {
	return "", errors.New("the whole running config must not be fetched for a scoped vendor")
}

func (f *fakeScopedReader) ScopedDiff(_ context.Context, _ *model.Host, _ *model.Network, _ SecretSource, opts DiffOptions) (ConfigDiff, error) {
	f.called = true
	f.opts = opts
	return f.diff, f.err
}

func installScoped(h *harness, hostname string, scoped *fakeScopedReader) {
	plain := newReader
	newReader = func(host *model.Host, address string, s SecretSource) (DeviceReader, error) {
		if host.Hostname == hostname {
			return scoped, nil
		}
		return plain(host, address, s)
	}
}

func TestDiffUsesScopedComparisonWhenTheReaderOffersOne(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.2"] = true

	scoped := &fakeScopedReader{diff: ConfigDiff{
		Text:       "- enable password sha512 <hash-redacted>\n+ enable password sha512 <hash-redacted>",
		HasChanges: true,
		Summary:    "1 managed item(s) drifted",
	}}
	installScoped(h, "fmt2-core", scoped)

	if err := h.run(t, "diff", "fmt2-core"); err != nil {
		t.Fatal(err)
	}
	if !scoped.called {
		t.Fatal("the scoped comparison was not used")
	}
	out := h.out.String()
	if !strings.Contains(out, "1 managed item(s) drifted") {
		t.Errorf("scoped summary missing:\n%s", out)
	}
	if !strings.Contains(out, "- enable password sha512 <hash-redacted>") {
		t.Errorf("scoped body missing:\n%s", out)
	}
	// The scoped path must not report the unmanaged lines the generic
	// whole-config diff used to.
	if strings.Contains(out, "-32859") || strings.Contains(out, "+4 ") {
		t.Errorf("whole-config diff leaked into a scoped vendor:\n%s", out)
	}
}

func TestScopedDiffRedactsByDefault(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.2"] = true
	scoped := &fakeScopedReader{diff: ConfigDiff{Summary: "no changes"}}
	installScoped(h, "fmt2-core", scoped)

	if err := h.run(t, "diff", "fmt2-core"); err != nil {
		t.Fatal(err)
	}
	if scoped.opts.ShowSecrets {
		t.Error("ShowSecrets defaulted to true")
	}

	h2 := newHarness(t)
	h2.reachable["10.0.0.2"] = true
	scoped2 := &fakeScopedReader{diff: ConfigDiff{Summary: "no changes"}}
	installScoped(h2, "fmt2-core", scoped2)
	if err := h2.run(t, "diff", "fmt2-core", "--show-secrets"); err != nil {
		t.Fatal(err)
	}
	if !scoped2.opts.ShowSecrets {
		t.Error("--show-secrets did not reach the scoped comparison")
	}
}

func TestScopedDiffErrorFailsTheDevice(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.2"] = true
	installScoped(h, "fmt2-core", &fakeScopedReader{err: errors.New("eAPI unreachable")})

	err := h.run(t, "diff", "fmt2-core")
	if err == nil {
		t.Fatal("a failed scoped diff must exit non-zero")
	}
	if !strings.Contains(h.out.String(), "failed: eAPI unreachable") {
		t.Errorf("failure not in the table:\n%s", h.out.String())
	}
}

// Dispatch is a reader capability: EOS gets one, VyOS (owned whole) does not.
func TestWrapScopedOnlyUpgradesScopedVendors(t *testing.T) {
	for _, tc := range []struct {
		deviceType string
		want       bool
	}{
		{"eos", true},
		{"vyos", false},
	} {
		host := &model.Host{Hostname: "h", DeviceType: tc.deviceType}
		r, err := vendor.NewReader(host, device.Options{
			Endpoint:      "127.0.0.1",
			Secrets:       fakeSecrets{},
			GlobalSecrets: fakeGlobalSecrets{},
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.deviceType, err)
		}
		wrapped := wrapScoped(host, readerAdapter{r}, r)
		if _, ok := wrapped.(ScopedDiffer); ok != tc.want {
			t.Errorf("%s: ScopedDiffer = %v, want %v", tc.deviceType, ok, tc.want)
		}
	}
}

type fakeGlobalSecrets struct{}

func (fakeGlobalSecrets) GlobalSecret(name string) (string, error) { return "SECRET-" + name, nil }

func TestScopedConfigDiffMapping(t *testing.T) {
	result := scope.Result{
		Drift: []scope.Change{
			{Device: "enable password sha512 <hash-redacted>", Desired: "enable password sha512 <hash-redacted>"},
			// An item absent from the device has no `- ` line.
			{Desired: "management api http-commands / vrf internal / no shutdown"},
		},
		Text:       "body",
		HasChanges: true,
		Summary:    "2 managed item(s) drifted",
	}
	d := scopedConfigDiff(result)
	if len(d.Added) != 2 {
		t.Errorf("Added = %v, want both desired lines", d.Added)
	}
	if len(d.Removed) != 1 {
		t.Errorf("Removed = %v, want only the line the device actually has", d.Removed)
	}
	if d.Text != "body" || d.Summary != "2 managed item(s) drifted" || !d.HasChanges {
		t.Errorf("ConfigDiff = %+v", d)
	}
}
