package render_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vendor"
)

// barfRoot locates projects/barf — now just the network.yml the fleet is
// described in — relative to this source file.
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

// Every renderable host in network.yml carries a snapshot, so adding a host
// fails here until its render has been eyeballed once.
func TestGoldenSnapshots(t *testing.T) {
	network := loadFleet(t)

	rendered := map[string]bool{}
	for i := range network.Hosts {
		host := &network.Hosts[i]
		if _, ok := vendor.Renderer(host.DeviceType); !ok {
			continue
		}
		rendered[host.Hostname+".conf"] = true

		t.Run(host.Hostname, func(t *testing.T) {
			got, err := vendor.Render(host, network, fakeSecrets{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			checkSnapshot(t, got, "golden", host.Hostname+".conf")
		})
	}
	if len(rendered) == 0 {
		t.Fatal("no renderable hosts in network.yml")
	}

	// A snapshot for a host that no longer exists would otherwise sit there
	// passing forever.
	entries, err := os.ReadDir(filepath.Join(testdataDir(t), "golden"))
	if err != nil {
		t.Fatalf("read golden dir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".conf") || rendered[name] {
			continue
		}
		if *update {
			if err := os.Remove(filepath.Join(testdataDir(t), "golden", name)); err != nil {
				t.Fatalf("remove orphaned snapshot: %v", err)
			}
			continue
		}
		t.Errorf("golden/%s has no renderable host in network.yml; re-run with -update to drop it", name)
	}
}

// Nondeterminism would make the snapshots meaningless.
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
