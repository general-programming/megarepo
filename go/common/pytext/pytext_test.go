package pytext

import "testing"

// Pins SplitLines against Python's str.splitlines().
func TestSplitLinesMatchesPythonSplitlines(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\r\nb", []string{"a", "b"}},
		{"a\rb", []string{"a", "b"}},
		{"a\r\n", []string{"a"}},
		{"a\n\nb", []string{"a", "", "b"}},
		{"a\vb", []string{"a", "b"}},
		{"a\fb", []string{"a", "b"}},
		{"a\x1cb", []string{"a", "b"}},
		{"a\x1db", []string{"a", "b"}},
		{"a\x1eb", []string{"a", "b"}},
		{"a\u0085b", []string{"a", "b"}},
		{"a\u2028b", []string{"a", "b"}},
		{"a\u2029b", []string{"a", "b"}},
		// Not boundaries in Python: tab, space, NBSP.
		{"a\tb", []string{"a\tb"}},
		{"a b", []string{"a b"}},
		{"a\u00a0b", []string{"a\u00a0b"}},
	}
	for _, tc := range tests {
		got := SplitLines(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("SplitLines(%q) = %q, want %q", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("SplitLines(%q) = %q, want %q", tc.in, got, tc.want)
				break
			}
		}
	}
}
