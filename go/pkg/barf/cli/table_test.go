package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintTableExactLayout(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []string{"DEVICE", "DIFF"}, [][]string{
		{"sea1-vpn-0", "+12 -2"},
		{"fmt2-core", "no changes"},
	})

	want := strings.Join([]string{
		"DEVICE      DIFF",
		"----------  ----------",
		"sea1-vpn-0  +12 -2",
		"fmt2-core   no changes",
		"",
	}, "\n")
	if got := buf.String(); got != want {
		t.Errorf("table mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestPrintTableIsANSIFree(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []string{"A"}, [][]string{{"x"}})
	if strings.Contains(buf.String(), "\x1b") {
		t.Errorf("plain table contains ANSI escapes: %q", buf.String())
	}
}

func TestPrintTableNoTrailingWhitespace(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []string{"DEVICE", "STATUS"}, [][]string{
		{"short", "ok"},
		{"a-much-longer-name", "error: something went wrong"},
	})
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != strings.TrimRight(line, " ") {
			t.Errorf("line has trailing whitespace: %q", line)
		}
	}
}

func TestPrintTableShortRowsPad(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []string{"A", "B", "C"}, [][]string{{"1", "2"}})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got, want := lines[2], "1  2"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

func TestPrintTableEmptyRows(t *testing.T) {
	var buf bytes.Buffer
	printTable(&buf, []string{"DEVICE"}, nil)
	if got, want := buf.String(), "DEVICE\n------\n"; got != want {
		t.Errorf("table = %q, want %q", got, want)
	}
}
