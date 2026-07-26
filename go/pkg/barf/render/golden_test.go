package render_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// barfRoot locates projects/barf relative to this source file, so the
// test works from any working directory.
func barfRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "projects", "barf")
	if _, err := os.Stat(filepath.Join(root, "network.yml")); err != nil {
		t.Fatalf("network.yml not found under %s: %v", root, err)
	}
	return root
}

func loadFleet(t *testing.T) *model.Network {
	t.Helper()
	network, err := model.Load(filepath.Join(barfRoot(t), "network.yml"))
	if err != nil {
		t.Fatalf("model.Load: %v", err)
	}
	return network
}

// TestGoldenParity renders every host that has a golden and diffs the
// result byte-for-byte. Hosts whose vendor has no Go renderer yet are
// skipped loudly, so the parity table stays honest.
func TestGoldenParity(t *testing.T) {
	root := barfRoot(t)
	network := loadFleet(t)
	goldenDir := filepath.Join(root, "tests", "golden")

	entries, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatalf("read golden dir: %v", err)
	}

	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".conf")
		if name == entry.Name() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			host, ok := network.Host(name)
			if !ok {
				t.Fatalf("golden %s has no host in network.yml", name)
			}
			if _, ok := vendor.Renderer(host.DeviceType); !ok {
				t.Skipf("no Go renderer for device type %q yet", host.DeviceType)
			}

			got, err := vendor.Render(host, network, fakeSecrets{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			wantBytes, err := os.ReadFile(filepath.Join(goldenDir, entry.Name()))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			want := string(wantBytes)
			if got != want {
				t.Errorf("render drifted from golden:\n%s", firstDiff(want, got))
			}
		})
	}
}

// TestRenderIsDeterministic guards the golden contract itself: any
// nondeterminism would make byte-parity meaningless.
func TestRenderIsDeterministic(t *testing.T) {
	network := loadFleet(t)
	for i := range network.Hosts {
		host := &network.Hosts[i]
		if _, ok := vendor.Renderer(host.DeviceType); !ok {
			continue
		}
		first, err := vendor.Render(host, network, fakeSecrets{})
		if err != nil {
			continue
		}
		second, err := vendor.Render(host, network, fakeSecrets{})
		if err != nil {
			t.Fatalf("%s: second render failed: %v", host.Hostname, err)
		}
		if first != second {
			t.Errorf("%s: renders differ between runs", host.Hostname)
		}
	}
}

// firstDiff reports the first differing line plus a little context.
func firstDiff(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")

	var b strings.Builder
	shown := 0
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w == g {
			continue
		}
		b.WriteString("line " + strconv.Itoa(i+1) + ":\n  want: " + strconv.Quote(w) +
			"\n  got:  " + strconv.Quote(g) + "\n")
		if shown++; shown >= 10 {
			b.WriteString("... (further differences elided)\n")
			break
		}
	}
	if shown == 0 {
		b.WriteString("(line-by-line identical; trailing bytes differ)\n")
	}
	return b.String()
}
