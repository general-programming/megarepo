package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The testdata/ corpus is a snapshot of what this package renders, captured
// originally from the Python implementation barf was ported from and
// authoritative from Go since. It is not a correctness oracle: it makes any
// change to a rendered config visible as a diff before it reaches a device.
//
// Intentional render changes: re-run with -update and review the diff.
var update = flag.Bool("update", false, "rewrite the testdata/ render snapshots from the current renderer")

func testdataDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Join(filepath.Dir(file), "testdata")
}

func readFixture(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{testdataDir(t)}, parts...)...))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return string(data)
}

// checkSnapshot diffs got against testdata/<parts...>, or rewrites it under
// -update. A missing snapshot is a failure rather than a silent pass: that is
// how a newly rendered host gets noticed.
func checkSnapshot(t *testing.T, got string, parts ...string) {
	t.Helper()
	rel := filepath.Join(parts...)
	path := filepath.Join(testdataDir(t), rel)

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create snapshot dir: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		return
	}

	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot %s: %v\nre-run with -update to create it", rel, err)
	}
	if want := string(wantBytes); got != want {
		t.Errorf("render drifted from %s:\n%s\nre-run with -update if this change is intended", rel, firstDiff(want, got))
	}
}

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
