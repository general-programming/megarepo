package vendor_test

import (
	"errors"
	"sort"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/scope"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

type fakeSecrets struct{}

func (fakeSecrets) HostSecret(hostname, key string) (string, error)   { return "s", nil }
func (fakeSecrets) GlobalSecret(name string) (string, error)          { return "g", nil }
func (fakeSecrets) VaultSecret(key string) (string, error)            { return "v", nil }
func (fakeSecrets) TacacsKey(hostname string) (string, error)         { return "t", nil }
func (fakeSecrets) WireguardKeypair(p string) (render.Keypair, error) { return render.Keypair{}, nil }

// -- the table itself --------------------------------------------------

// A deliberately literal list: adding a vendor must require saying so
// here too.
func TestTableIsTheWholeTruth(t *testing.T) {
	want := []string{"cisco", "dnos6", "dnos9", "edgeos", "eos", "external", "linux", "mikrotik", "vyos"}
	got := vendor.Types()
	if len(got) != len(want) {
		t.Fatalf("types = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("types = %v, want %v", got, want)
		}
	}
}

// Every capability of every vendor, checkable as data, in one assertion.
func TestCapabilityMatrix(t *testing.T) {
	for _, tc := range []struct {
		deviceType                     string
		renders, reads, writes, scoped bool
	}{
		{"eos", true, true, false, true},
		{"vyos", true, true, true, false},
		{"linux", true, false, false, false},
		{"mikrotik", true, false, false, false},
		{"edgeos", true, false, false, false},
		{"cisco", true, false, false, false},
		{"dnos6", true, false, false, false},
		{"dnos9", true, false, false, false},
		{"external", false, false, false, false},
	} {
		t.Run(tc.deviceType, func(t *testing.T) {
			v, ok := vendor.Get(tc.deviceType)
			if !ok {
				t.Fatalf("%s not in the table", tc.deviceType)
			}
			if v.Templatable() != tc.renders {
				t.Errorf("Templatable = %v, want %v", v.Templatable(), tc.renders)
			}
			if v.ReportsStatus() != tc.reads {
				t.Errorf("ReportsStatus = %v, want %v", v.ReportsStatus(), tc.reads)
			}
			if v.Deployable() != tc.writes {
				t.Errorf("Deployable = %v, want %v", v.Deployable(), tc.writes)
			}
			if v.Scoped() != tc.scoped {
				t.Errorf("Scoped = %v, want %v", v.Scoped(), tc.scoped)
			}
			// The CLI calls the package-level predicates; they must agree
			// with the row's methods.
			if vendor.Templatable(tc.deviceType) != tc.renders ||
				vendor.ReportsStatus(tc.deviceType) != tc.reads ||
				vendor.Deployable(tc.deviceType) != tc.writes {
				t.Error("package predicate disagrees with the row")
			}
		})
	}
}

// A devicetype with no row can do nothing, rather than defaulting to
// "probably renderable" the way render.Templatable's `!= "external"` did.
func TestUnknownDeviceTypeHasNoCapabilities(t *testing.T) {
	if _, ok := vendor.Get("junos"); ok {
		t.Fatal("junos should not be in the table")
	}
	for name, got := range map[string]bool{
		"Templatable":   vendor.Templatable("junos"),
		"ReportsStatus": vendor.ReportsStatus("junos"),
		"Deployable":    vendor.Deployable("junos"),
	} {
		if got {
			t.Errorf("%s(junos) = true", name)
		}
	}
}

// Pins the NetBox platform-slug spellings from Python's VENDOR_MAP.
func TestAliasesResolveToTheSameRow(t *testing.T) {
	for alias, canonical := range map[string]string{
		"cisco-ios": "cisco", "dnos-6": "dnos6", "dnos-9": "dnos9",
	} {
		aliased, ok := vendor.Get(alias)
		if !ok {
			t.Errorf("%s: no row", alias)
			continue
		}
		if aliased.Type != canonical {
			t.Errorf("%s resolved to %s, want %s", alias, aliased.Type, canonical)
		}
		direct, _ := vendor.Get(canonical)
		if aliased.Renderer != direct.Renderer {
			t.Errorf("%s should resolve to the %s renderer", alias, canonical)
		}
	}
}

func TestLookupIsCaseInsensitive(t *testing.T) {
	for _, spelling := range []string{"VyOS", "EOS", " eos "} {
		if _, ok := vendor.Get(spelling); !ok {
			t.Errorf("Get(%q) missed", spelling)
		}
	}
}

func TestTypesWhere(t *testing.T) {
	got := vendor.TypesWhere(vendor.Vendor.Deployable)
	if len(got) != 1 || got[0] != "vyos" {
		t.Errorf("deployable = %v, want [vyos]", got)
	}
	got = vendor.TypesWhere(vendor.Vendor.ReportsStatus)
	sort.Strings(got)
	if len(got) != 2 || got[0] != "eos" || got[1] != "vyos" {
		t.Errorf("readable = %v, want [eos vyos]", got)
	}
}

// -- dispatch -----------------------------------------------------------

// The table must hand back the right concrete transport, and refuse for a
// vendor barf cannot talk to.
func TestNewReaderDispatch(t *testing.T) {
	opts := device.Options{Secrets: fakeSecrets{}, GlobalSecrets: fakeSecrets{}}

	eos, err := vendor.NewReader(&model.Host{Hostname: "a", DeviceType: "eos"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := eos.(*device.EOSReader); !ok {
		t.Errorf("eos -> %T", eos)
	}

	vyos, err := vendor.NewReader(&model.Host{Hostname: "b", DeviceType: "VyOS"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vyos.(*device.VyOSReader); !ok {
		t.Errorf("vyos -> %T", vyos)
	}

	if _, err := vendor.NewReader(&model.Host{Hostname: "c", DeviceType: "linux"}, opts); !errors.Is(err, device.ErrUnsupported) {
		t.Errorf("linux err = %v, want ErrUnsupported", err)
	}
}

func TestComparerDispatch(t *testing.T) {
	c, ok := vendor.Comparer("eos")
	if !ok {
		t.Fatal("eos has no scoped comparer")
	}
	if _, isEOS := c.(scope.EOS); !isEOS {
		t.Errorf("eos comparer is %T", c)
	}
	if _, ok := vendor.Comparer("EOS"); !ok {
		t.Error("devicetype lookup must be case-insensitive")
	}
	// Vendors owned whole must fall through to the generic diff.
	for _, dt := range []string{"vyos", "linux", "mikrotik", "external", ""} {
		if _, ok := vendor.Comparer(dt); ok {
			t.Errorf("%q unexpectedly has a scoped comparer", dt)
		}
	}
}

// The two refusals stay distinguishable: `external` is unmanaged on
// purpose, `junos` is simply not ported.
func TestRenderRejectsExternalAndUnknown(t *testing.T) {
	_, err := vendor.Render(&model.Host{Hostname: "x", DeviceType: "external"}, &model.Network{}, fakeSecrets{})
	if err == nil || !contains(err.Error(), "unmanaged") {
		t.Errorf("external err = %v", err)
	}
	_, err = vendor.Render(&model.Host{Hostname: "x", DeviceType: "junos"}, &model.Network{}, fakeSecrets{})
	if err == nil || !contains(err.Error(), "no renderer") {
		t.Errorf("junos err = %v", err)
	}
}

// -- the write guard is unchanged --------------------------------------

// Routing writer construction through the table must not become a way
// around device's opt-in, and the refusal must stay device's own.
func TestNewWriterStillRequiresAllowWrites(t *testing.T) {
	h := &model.Host{Hostname: "r", DeviceType: "vyos"}
	if _, err := vendor.NewWriter(h, device.Options{GlobalSecrets: fakeSecrets{}}); err == nil {
		t.Fatal("a writer was built without Options.AllowWrites")
	} else {
		var refusal *device.WritesNotAllowedError
		if !errors.As(err, &refusal) {
			t.Errorf("err = %v (%T), want device.WritesNotAllowedError", err, err)
		}
	}
	if _, err := vendor.NewWriter(h, device.Options{AllowWrites: true, GlobalSecrets: fakeSecrets{}}); err != nil {
		t.Errorf("with AllowWrites: %v", err)
	}
}

// A nil NewWriter must not fall back to another vendor's writer or panic.
// EOS is the live case: device.NewEOSWriter exists but no row names it.
func TestNoWriterForNonDeployableVendors(t *testing.T) {
	for _, dt := range []string{"eos", "linux", "cisco", "external", "junos"} {
		_, err := vendor.NewWriter(&model.Host{Hostname: "h", DeviceType: dt}, device.Options{AllowWrites: true})
		if err == nil {
			t.Errorf("%s: got a writer", dt)
		}
	}
}

// -- the DNOS-class bug ------------------------------------------------

// The DNOS outage happened in the gap between a complete fake and an
// incomplete real adapter, so fakes are held to the production composite.
var _ render.CompleteSecretSource = fakeSecrets{}

func TestCheckSecretSourceNamesWhatIsMissing(t *testing.T) {
	if err := render.CheckSecretSource(fakeSecrets{}); err != nil {
		t.Errorf("complete source rejected: %v", err)
	}
	// hostOnly is roughly the adapter shape from the outage: an incomplete
	// source must be reported here, not discovered on a device.
	if err := render.CheckSecretSource(hostOnly{}); err == nil {
		t.Fatal("an incomplete secret source passed the check")
	} else {
		for _, want := range []string{"VaultSource", "TacacsSource", "WireguardSource"} {
			if !contains(err.Error(), want) {
				t.Errorf("err %q does not name %s", err, want)
			}
		}
	}
}

type hostOnly struct{}

func (hostOnly) HostSecret(hostname, key string) (string, error) { return "", nil }

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
