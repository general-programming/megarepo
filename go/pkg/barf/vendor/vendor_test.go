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

// TestTableIsTheWholeTruth pins the set of devicetypes barf knows about.
// It is deliberately a literal list: adding a vendor should require
// saying so here, in a test, on purpose.
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

// TestCapabilityMatrix is the whole point of the descriptor: every
// capability of every vendor, checkable as data, in one assertion.
//
// Under the old layout this table did not exist anywhere; answering it
// meant reading four registries in four packages and a hand-written
// switch in a fifth place, and any of them could disagree.
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
			// The package-level predicates must answer identically to the
			// row's methods; they are what the CLI calls.
			if vendor.Templatable(tc.deviceType) != tc.renders ||
				vendor.ReportsStatus(tc.deviceType) != tc.reads ||
				vendor.Deployable(tc.deviceType) != tc.writes {
				t.Error("package predicate disagrees with the row")
			}
		})
	}
}

// TestUnknownDeviceTypeHasNoCapabilities: a devicetype with no row can
// do nothing, rather than defaulting to "probably renderable" the way
// render.Templatable's `!= "external"` used to.
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

// TestAliasesResolveToTheSameRow pins the NetBox platform-slug spellings
// from Python's VENDOR_MAP.
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

// -- dispatch (moved here with the registries) --------------------------

// TestNewReaderDispatch is the old device.TestNewDispatch: the vendor
// table must hand back the right concrete transport, and refuse for a
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

// TestComparerDispatch is the old scope.TestRegistryDispatch.
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

// TestRenderRejectsExternalAndUnknown keeps the two different refusals
// render.Host used to make distinguishable: `external` is unmanaged on
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

// TestNewWriterStillRequiresAllowWrites proves the descriptor did not
// become a way around device's opt-in. Routing writer construction
// through a table must not make a writer easier to obtain: without
// AllowWrites the constructor still refuses, and the refusal is the
// device package's, not a new one invented here.
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

// TestNoWriterForNonDeployableVendors: a nil NewWriter is not a fallback
// to some other vendor's writer, and not a panic. EOS is the live case —
// device.NewEOSWriter exists and is tested, but no row names it, so
// there is no path from here to changing an Arista switch.
func TestNoWriterForNonDeployableVendors(t *testing.T) {
	for _, dt := range []string{"eos", "linux", "cisco", "external", "junos"} {
		_, err := vendor.NewWriter(&model.Host{Hostname: "h", DeviceType: dt}, device.Options{AllowWrites: true})
		if err == nil {
			t.Errorf("%s: got a writer", dt)
		}
	}
}

// -- the DNOS-class bug ------------------------------------------------

// TestFakeSecretsIsComplete: a fake used to render must satisfy the same
// composite the production adapter is asserted against. The DNOS outage
// happened in the gap between a complete fake and an incomplete real
// adapter, so the fakes are held to the real bar.
var _ render.CompleteSecretSource = fakeSecrets{}

func TestCheckSecretSourceNamesWhatIsMissing(t *testing.T) {
	if err := render.CheckSecretSource(fakeSecrets{}); err != nil {
		t.Errorf("complete source rejected: %v", err)
	}
	// hostOnly is the shape the production adapter had during the outage,
	// minus more: it must be reported, not discovered on a device.
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
