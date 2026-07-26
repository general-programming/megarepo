package pytext

import (
	"slices"
	"testing"
)

func TestShellSplit(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "bare words", in: "set system host-name r1", want: []string{"set", "system", "host-name", "r1"}},
		{name: "empty", in: "", want: nil},
		{name: "only whitespace", in: "  \t ", want: nil},
		{name: "single quotes strip", in: "a 'b'", want: []string{"a", "b"}},
		{name: "double quotes strip", in: `a "b"`, want: []string{"a", "b"}},
		{name: "quoted spaces stay one token", in: "a 'b c'", want: []string{"a", "b c"}},
		{name: "empty quoted token", in: "a ''", want: []string{"a", ""}},
		{name: "adjacent quotes join", in: `a'b'"c"d`, want: []string{"abcd"}},
		{name: "no escapes inside single quotes", in: `a '\n'`, want: []string{"a", `\n`}},
		{name: "escaped quote inside double quotes", in: `a "b\"c"`, want: []string{"a", `b"c`}},
		{name: "escaped backslash inside double quotes", in: `a "b\\c"`, want: []string{"a", `b\c`}},
		{name: "other escapes keep the backslash in double quotes", in: `a "b\nc"`, want: []string{"a", `b\nc`}},
		{name: "bare backslash escapes the next char", in: `a b\ c`, want: []string{"a", "b c"}},
		{name: "tabs and newlines split", in: "a\tb\nc", want: []string{"a", "b", "c"}},
		{name: "ipv6 value survives", in: "set system name-server 2606:4700::1111", want: []string{"set", "system", "name-server", "2606:4700::1111"}},
		{name: "unbalanced single quote", in: "a 'b", wantErr: true},
		{name: "unbalanced double quote", in: `a "b`, wantErr: true},
		{name: "apostrophe opens a quote", in: "host-name it's-broken", wantErr: true},
		{name: "trailing escape", in: `a b\`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellSplit(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ShellSplit(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ShellSplit(%q): %v", tc.in, err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("ShellSplit(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "''"},
		{"plain", "plain"},
		{"eth0", "eth0"},
		{"10.0.0.1/24", "10.0.0.1/24"},
		{"2606:4700::1111", "2606:4700::1111"},
		{"a-b_c.d@e%f+g=h:i,j/k", "a-b_c.d@e%f+g=h:i,j/k"},
		{"a b", "'a b'"},
		{"a(b)", "'a(b)'"},
		{"it's", `'it'"'"'s'`},
		{"$6$salt$hash", "'$6$salt$hash'"},
		// sshx interpolates this into a remote shell command line.
		{"$(reboot)", "'$(reboot)'"},
		{"/tmp/barf-install.sh", "/tmp/barf-install.sh"},
		{"näive", "'näive'"}, // re.ASCII: non-ASCII word chars are unsafe
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			if got := ShellQuote(tc.in); got != tc.want {
				t.Fatalf("ShellQuote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Quoting must round-trip, which keeps FormatDiff output re-parseable by
// ParseSetCommands.
func TestShellRoundTrip(t *testing.T) {
	for _, s := range []string{"plain", "a b", "it's", "$6$salt$hash", `a"b`, `a\b`, ""} {
		got, err := ShellSplit(ShellQuote(s))
		if err != nil {
			t.Fatalf("round trip %q: %v", s, err)
		}
		if len(got) != 1 || got[0] != s {
			t.Fatalf("round trip %q = %#v", s, got)
		}
	}
}
