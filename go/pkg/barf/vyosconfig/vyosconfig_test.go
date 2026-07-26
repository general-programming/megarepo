package vyosconfig

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// The cases below are ported one-for-one from
// projects/barf/tests/test_vyos_config.py, which stays authoritative.

// specHash is a real sha512-crypt vector from Ulrich Drepper's SHA-crypt
// spec: crypt("Hello world!", "$6$saltstring").
const specHash = "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFN" +
	"jnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"

// loginPrefix is the Python tests' PREFIX.
var loginPrefix = Path{"system", "login", "user", "supertech", "authentication"}

func path(components ...string) Path { return Path(components) }

// assertSet compares a Set against the exact paths it must contain.
func assertSet(t *testing.T, got Set, want ...Path) {
	t.Helper()
	gotSorted := got.Sorted()
	wantSorted := slices.Clone(want)
	sortPaths(wantSorted)
	if len(gotSorted) != len(wantSorted) {
		t.Fatalf("set size = %d %v, want %d %v", len(gotSorted), gotSorted, len(wantSorted), wantSorted)
	}
	for i := range gotSorted {
		if !slices.Equal(gotSorted[i], wantSorted[i]) {
			t.Fatalf("set[%d] = %v, want %v", i, gotSorted[i], wantSorted[i])
		}
	}
}

func assertPaths(t *testing.T, got []Path, want ...Path) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d paths %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range got {
		if !slices.Equal(got[i], want[i]) {
			t.Fatalf("path[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestParseSetCommands(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []Path
	}{
		{
			name: "basic paths",
			text: "set system host-name router1\nset interfaces ethernet eth0 mtu 9000",
			want: []Path{
				path("system", "host-name", "router1"),
				path("interfaces", "ethernet", "eth0", "mtu", "9000"),
			},
		},
		{
			name: "quoted value normalizes to bare",
			text: "set vpn ipsec esp-group E proposal 1 encryption 'aes256'",
			want: []Path{path("vpn", "ipsec", "esp-group", "E", "proposal", "1", "encryption", "aes256")},
		},
		{
			name: "double-quoted value normalizes too",
			text: `set vpn ipsec esp-group E proposal 1 encryption "aes256"`,
			want: []Path{path("vpn", "ipsec", "esp-group", "E", "proposal", "1", "encryption", "aes256")},
		},
		{
			name: "quoted value with spaces stays one component",
			text: "set interfaces ethernet eth0 description 'external uplink (a -> b)'",
			want: []Path{path("interfaces", "ethernet", "eth0", "description", "external uplink (a -> b)")},
		},
		{
			name: "blanks, comments and non-set lines ignored",
			text: "\n# comment\ndelete interfaces ethernet eth9\nconfigure\nset system host-name r1\n",
			want: []Path{path("system", "host-name", "r1")},
		},
		{
			name: "leading whitespace tolerated",
			text: "   set system host-name r1  ",
			want: []Path{path("system", "host-name", "r1")},
		},
		{
			name: "duplicate set lines collapse to one path",
			text: "set system host-name r1\nset system host-name r1",
			want: []Path{path("system", "host-name", "r1")},
		},
		{
			name: "setfoo is not a set line",
			text: "setfoo bar\nsettings x",
			want: nil,
		},
		{
			name: "empty quoted value is a real empty component",
			text: "set system foo ''",
			want: []Path{path("system", "foo", "")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertSet(t, ParseSetCommands(tc.text), tc.want...)
		})
	}
}

func TestParseSetCommandsUnbalancedQuoteFallsBackToPlainSplit(t *testing.T) {
	// shlex would raise; Python keeps the raw whitespace split instead of
	// dying, and so must we.
	got := ParseSetCommands("set system host-name it's-broken")
	if !got.Has(path("system", "host-name", "it's-broken")) {
		t.Fatalf("fallback split missing: %v", got.Sorted())
	}
}

func TestPathsFromAPIJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []Path
	}{
		{
			name: "single value leaf",
			raw:  `{"interfaces": {"ethernet": {"eth0": {"address": "dhcp"}}}}`,
			want: []Path{path("interfaces", "ethernet", "eth0", "address", "dhcp")},
		},
		{
			name: "multi-value leaf becomes one path per value",
			raw:  `{"system": {"name-server": ["1.1.1.1", "10.255.1.8"]}}`,
			want: []Path{
				path("system", "name-server", "1.1.1.1"),
				path("system", "name-server", "10.255.1.8"),
			},
		},
		{
			name: "valueless node is its own path",
			raw:  `{"protocols": {"bgp": {"parameters": {"shutdown": {}}}}}`,
			want: []Path{path("protocols", "bgp", "parameters", "shutdown")},
		},
		{
			name: "non-string scalars coerced",
			raw:  `{"interfaces": {"ethernet": {"eth0": {"mtu": 9000}}}}`,
			want: []Path{path("interfaces", "ethernet", "eth0", "mtu", "9000")},
		},
		{
			name: "empty tree yields nothing",
			raw:  `{}`,
			want: nil,
		},
		{
			name: "sibling branches do not share a prefix buffer",
			raw:  `{"a": {"b": {"c": "1"}, "d": {"e": "2"}}}`,
			want: []Path{path("a", "b", "c", "1"), path("a", "d", "e", "2")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var data any
			if err := json.Unmarshal([]byte(tc.raw), &data); err != nil {
				t.Fatal(err)
			}
			assertSet(t, PathsFromAPIJSON(data), tc.want...)
		})
	}
}

// TestAPIJSONMatchesEquivalentSetCommands is the load-bearing property:
// the two representations of the same config must flatten to the same
// path set, or every diff would be full of noise.
func TestAPIJSONMatchesEquivalentSetCommands(t *testing.T) {
	var data any
	raw := `{"interfaces": {"wireguard": {"wg1": {
		"peer": {"other": {"allowed-ips": ["0.0.0.0/0", "::/0"]}},
		"port": "1"}}}}`
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	rendered := strings.Join([]string{
		"set interfaces wireguard wg1 peer other allowed-ips 0.0.0.0/0",
		"set interfaces wireguard wg1 peer other allowed-ips ::/0",
		"set interfaces wireguard wg1 port 1",
	}, "\n")

	fromAPI := PathsFromAPIJSON(data)
	fromText := ParseSetCommands(rendered)
	assertSet(t, fromAPI, fromText.Sorted()...)

	diff := DiffPaths(fromAPI, fromText, nil)
	if diff.HasChanges() {
		t.Fatalf("representations disagree: %s", FormatDiff(diff, true))
	}
}

func TestDiffPaths(t *testing.T) {
	tests := []struct {
		name        string
		running     Set
		candidate   Set
		ignored     []Path
		wantAdded   []Path
		wantRemoved []Path
		wantSummary string
	}{
		{
			name:        "no changes",
			running:     NewSet(path("system", "host-name", "r1")),
			candidate:   NewSet(path("system", "host-name", "r1")),
			wantSummary: "no changes",
		},
		{
			name:        "addition",
			running:     NewSet(),
			candidate:   NewSet(path("system", "host-name", "r1")),
			wantAdded:   []Path{path("system", "host-name", "r1")},
			wantSummary: "+1",
		},
		{
			// Ownership is total: the stale value is a real removal.
			name:        "changed value is add plus remove",
			running:     NewSet(path("system", "host-name", "old")),
			candidate:   NewSet(path("system", "host-name", "new")),
			wantAdded:   []Path{path("system", "host-name", "new")},
			wantRemoved: []Path{path("system", "host-name", "old")},
			wantSummary: "+1 -1",
		},
		{
			// Nothing is exempt from ownership: a path only on the device
			// (not ignored) is deleted, not kept.
			name:        "unrendered device path is removed",
			running:     NewSet(path("service", "ssh", "port", "22")),
			candidate:   NewSet(),
			wantRemoved: []Path{path("service", "ssh", "port", "22")},
			wantSummary: "+0 -1",
		},
		{
			name:        "unrendered section is fully removed",
			running:     NewSet(path("system", "conntrack", "modules", "ftp")),
			candidate:   NewSet(),
			wantRemoved: []Path{path("system", "conntrack", "modules", "ftp")},
			wantSummary: "+0 -1",
		},
		{
			name:        "changed leaf is add and remove",
			running:     NewSet(path("system", "name-server", "2606:4700::1111")),
			candidate:   NewSet(path("system", "name-server", "2606:4700:4700::1111")),
			wantAdded:   []Path{path("system", "name-server", "2606:4700:4700::1111")},
			wantRemoved: []Path{path("system", "name-server", "2606:4700::1111")},
			wantSummary: "+1 -1",
		},
		{
			name:        "wildcard ignores hw-id on eth0",
			running:     NewSet(path("interfaces", "ethernet", "eth0", "hw-id", "aa:bb")),
			candidate:   NewSet(),
			ignored:     IgnoredPaths,
			wantSummary: "no changes",
		},
		{
			name:        "wildcard ignores hw-id on eth1 too",
			running:     NewSet(path("interfaces", "ethernet", "eth1", "hw-id", "aa:bb")),
			candidate:   NewSet(),
			ignored:     IgnoredPaths,
			wantSummary: "no changes",
		},
		{
			name:        "wildcard does not ignore other leaves",
			running:     NewSet(path("interfaces", "ethernet", "eth0", "mtu", "9000")),
			candidate:   NewSet(),
			ignored:     IgnoredPaths,
			wantRemoved: []Path{path("interfaces", "ethernet", "eth0", "mtu", "9000")},
			wantSummary: "+0 -1",
		},
		{
			name:        "path shorter than the prefix is not ignored",
			running:     NewSet(path("interfaces", "ethernet")),
			candidate:   NewSet(),
			ignored:     IgnoredPaths,
			wantRemoved: []Path{path("interfaces", "ethernet")},
			wantSummary: "+0 -1",
		},
		{
			name:      "ignored paths are not deleted when the candidate omits them",
			running:   NewSet(path("interfaces", "ethernet", "eth0", "hw-id", "aa:bb"), path("interfaces", "ethernet", "eth0", "mtu", "9000")),
			candidate: NewSet(path("interfaces", "ethernet", "eth0", "mtu", "9000")),

			ignored:     IgnoredPaths,
			wantSummary: "no changes",
		},
		{
			name:        "results sort in tuple order",
			running:     NewSet(),
			candidate:   NewSet(path("b"), path("a", "z"), path("a")),
			wantAdded:   []Path{path("a"), path("a", "z"), path("b")},
			wantSummary: "+3",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := DiffPaths(tc.running, tc.candidate, tc.ignored)
			assertPaths(t, diff.Added, tc.wantAdded...)
			assertPaths(t, diff.Removed, tc.wantRemoved...)
			if got := SummarizeDiff(diff); got != tc.wantSummary {
				t.Fatalf("summary = %q, want %q", got, tc.wantSummary)
			}
			if diff.HasChanges() != (tc.wantSummary != "no changes") {
				t.Fatalf("HasChanges = %v for summary %q", diff.HasChanges(), tc.wantSummary)
			}
		})
	}
}

// TestDiffPathsDoesNotMutateInputs guards the deploy path: the caller
// keeps using `running` to compute minimal deletes after diffing.
func TestDiffPathsDoesNotMutateInputs(t *testing.T) {
	running := NewSet(path("a", "b"), path("c", "d"))
	candidate := NewSet(path("a", "b"))
	DiffPaths(running, candidate, nil)
	assertSet(t, running, path("a", "b"), path("c", "d"))
	assertSet(t, candidate, path("a", "b"))
}

func TestFormatDiff(t *testing.T) {
	tests := []struct {
		name       string
		diff       ConfigDiff
		redact     bool
		wantHas    []string
		wantHasNot []string
	}{
		{
			name: "plus minus lines",
			diff: ConfigDiff{
				Added:   []Path{path("system", "host-name", "new")},
				Removed: []Path{path("system", "host-name", "old")},
			},
			redact:  true,
			wantHas: []string{"- set system host-name old", "+ set system host-name new"},
		},
		{
			name:       "secrets redacted by default",
			diff:       ConfigDiff{Added: []Path{path("interfaces", "wireguard", "wg1", "private-key", "hunter2")}},
			redact:     true,
			wantHas:    []string{Redacted},
			wantHasNot: []string{"hunter2"},
		},
		{
			name:    "secrets shown when asked",
			diff:    ConfigDiff{Added: []Path{path("vpn", "ipsec", "authentication", "psk", "p", "secret", "hunter2")}},
			redact:  false,
			wantHas: []string{"hunter2"},
		},
		{
			name:       "psk secret redacted when asked to redact",
			diff:       ConfigDiff{Added: []Path{path("vpn", "ipsec", "authentication", "psk", "p", "secret", "hunter2")}},
			redact:     true,
			wantHas:    []string{Redacted},
			wantHasNot: []string{"hunter2"},
		},
		{
			name:    "public key is not a secret node",
			diff:    ConfigDiff{Added: []Path{path("interfaces", "wireguard", "wg1", "peer", "x", "public-key", "PUBKEY")}},
			redact:  true,
			wantHas: []string{"PUBKEY"},
		},
		{
			name:    "values with spaces are quoted",
			diff:    ConfigDiff{Added: []Path{path("interfaces", "ethernet", "eth0", "description", "a b")}},
			redact:  true,
			wantHas: []string{"description 'a b'"},
		},
		{
			name:       "removed device hash under plaintext-password is redacted",
			diff:       ConfigDiff{Removed: []Path{append(loginPrefix.Clone(), "plaintext-password", specHash)}},
			redact:     true,
			wantHas:    []string{Redacted},
			wantHasNot: []string{specHash},
		},
		{
			name:       "encrypted-password value is redacted",
			diff:       ConfigDiff{Removed: []Path{append(loginPrefix.Clone(), "encrypted-password", specHash)}},
			redact:     true,
			wantHasNot: []string{specHash},
		},
		{
			name:    "empty diff renders empty",
			diff:    ConfigDiff{},
			redact:  true,
			wantHas: []string{""},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text := FormatDiff(tc.diff, tc.redact)
			for _, want := range tc.wantHas {
				if !strings.Contains(text, want) {
					t.Fatalf("output %q missing %q", text, want)
				}
			}
			for _, bad := range tc.wantHasNot {
				if strings.Contains(text, bad) {
					t.Fatalf("output %q leaked %q", text, bad)
				}
			}
		})
	}
}

// TestFormatDiffOrdersRemovalsFirst pins the printed order: deletes are
// applied before sets on the device, and the diff must read the same way.
func TestFormatDiffOrdersRemovalsFirst(t *testing.T) {
	diff := ConfigDiff{
		Added:   []Path{path("a", "1")},
		Removed: []Path{path("b", "2")},
	}
	want := "- set b 2\n+ set a 1"
	if got := FormatDiff(diff, true); got != want {
		t.Fatalf("FormatDiff = %q, want %q", got, want)
	}
}

func TestRedactPath(t *testing.T) {
	tests := []struct {
		name string
		in   Path
		want Path
	}{
		{"private-key", path("i", "private-key", "K"), path("i", "private-key", Redacted)},
		{"secret", path("a", "secret", "S"), path("a", "secret", Redacted)},
		{"password", path("a", "password", "P"), path("a", "password", Redacted)},
		{"passphrase", path("a", "passphrase", "P"), path("a", "passphrase", Redacted)},
		{"pre-shared-secret", path("a", "pre-shared-secret", "P"), path("a", "pre-shared-secret", Redacted)},
		{"plaintext-password", path("a", "plaintext-password", "P"), path("a", "plaintext-password", Redacted)},
		{"encrypted-password", path("a", "encrypted-password", "P"), path("a", "encrypted-password", Redacted)},
		{"public-key untouched", path("a", "public-key", "K"), path("a", "public-key", "K")},
		{"secret as the value, not the node", path("a", "b", "secret"), path("a", "b", "secret")},
		{"one-component path untouched", path("secret"), path("secret")},
		{"empty path untouched", path(), path()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactPath(tc.in); !slices.Equal(got, tc.want) {
				t.Fatalf("RedactPath(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactPathDoesNotMutateInput(t *testing.T) {
	in := path("a", "secret", "hunter2")
	RedactPath(in)
	if in[2] != "hunter2" {
		t.Fatalf("input mutated: %v", in)
	}
}

func TestSummarizeDiff(t *testing.T) {
	tests := []struct {
		name string
		diff ConfigDiff
		want string
	}{
		{"empty", ConfigDiff{}, "no changes"},
		{"counts", ConfigDiff{Added: []Path{path("a", "1"), path("b", "2")}, Removed: []Path{path("c", "3")}}, "+2 -1"},
		{"adds only", ConfigDiff{Added: []Path{path("a", "1")}}, "+1"},
		{"removes only", ConfigDiff{Removed: []Path{path("a", "1")}}, "+0 -1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummarizeDiff(tc.diff); got != tc.want {
				t.Fatalf("SummarizeDiff = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestMinimalDeletePaths is the safety-critical one: these results become
// `delete` ops against a live router.
func TestMinimalDeletePaths(t *testing.T) {
	tests := []struct {
		name    string
		running Set
		removed []Path
		want    []Path
	}{
		{
			name: "leaf delete when a sibling survives",
			running: NewSet(
				path("system", "name-server", "1.1.1.1"),
				path("system", "name-server", "8.8.8.8"),
			),
			removed: []Path{path("system", "name-server", "1.1.1.1")},
			want:    []Path{path("system", "name-server", "1.1.1.1")},
		},
		{
			name: "collapses to the node when the subtree is fully removed",
			running: NewSet(
				path("system", "name-server", "1.1.1.1"),
				path("system", "name-server", "8.8.8.8"),
				path("system", "host-name", "r1"),
			),
			removed: []Path{
				path("system", "name-server", "1.1.1.1"),
				path("system", "name-server", "8.8.8.8"),
			},
			want: []Path{path("system", "name-server")},
		},
		{
			// host-name survives, so the collapse rises to the
			// name-server node but never to bare "system".
			name: "collapse stops where surviving config lives",
			running: NewSet(
				path("system", "name-server", "1.1.1.1"),
				path("system", "host-name", "r1"),
			),
			removed: []Path{path("system", "name-server", "1.1.1.1")},
			want:    []Path{path("system", "name-server")},
		},
		{
			name: "collapses a whole group",
			running: NewSet(
				path("vpn", "ipsec", "esp-group", "OLD", "mode", "tunnel"),
				path("vpn", "ipsec", "esp-group", "OLD", "lifetime", "3600"),
				path("vpn", "ipsec", "esp-group", "E", "mode", "tunnel"),
			),
			removed: []Path{
				path("vpn", "ipsec", "esp-group", "OLD", "mode", "tunnel"),
				path("vpn", "ipsec", "esp-group", "OLD", "lifetime", "3600"),
			},
			want: []Path{path("vpn", "ipsec", "esp-group", "OLD")},
		},
		{
			// Everything on the device goes: the collapse reaches the
			// topmost component, never the empty path.
			name:    "whole tree removed collapses to the top component",
			running: NewSet(path("system", "name-server", "1.1.1.1"), path("system", "host-name", "r1")),
			removed: []Path{path("system", "name-server", "1.1.1.1"), path("system", "host-name", "r1")},
			want:    []Path{path("system")},
		},
		{
			name:    "nothing removed yields no deletes",
			running: NewSet(path("system", "host-name", "r1")),
			removed: nil,
			want:    nil,
		},
		{
			name:    "single-component path cannot collapse",
			running: NewSet(path("shutdown")),
			removed: []Path{path("shutdown")},
			want:    []Path{path("shutdown")},
		},
		{
			name: "sibling subtrees collapse independently",
			running: NewSet(
				path("a", "x", "1"), path("a", "x", "2"),
				path("a", "y", "1"),
			),
			removed: []Path{path("a", "x", "1"), path("a", "x", "2")},
			want:    []Path{path("a", "x")},
		},
		{
			name: "duplicate removals dedupe into one delete",
			running: NewSet(
				path("system", "name-server", "1.1.1.1"),
				path("system", "host-name", "r1"),
			),
			removed: []Path{
				path("system", "name-server", "1.1.1.1"),
				path("system", "name-server", "1.1.1.1"),
			},
			want: []Path{path("system", "name-server")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MinimalDeletePaths(tc.removed, tc.running)
			assertPaths(t, got, tc.want...)
		})
	}
}

// TestMinimalDeletePathsNeverDeletesSurvivingConfig is the invariant the
// per-case table exists to protect: no chosen delete path may be a prefix
// of a running path that is staying.
func TestMinimalDeletePathsNeverDeletesSurvivingConfig(t *testing.T) {
	running := NewSet(
		path("interfaces", "ethernet", "eth0", "hw-id", "aa:bb"),
		path("interfaces", "ethernet", "eth0", "mtu", "9000"),
		path("interfaces", "ethernet", "eth1", "mtu", "1500"),
		path("system", "host-name", "r1"),
		path("system", "name-server", "1.1.1.1"),
		path("vpn", "ipsec", "esp-group", "OLD", "mode", "tunnel"),
	)
	candidate := NewSet(
		path("interfaces", "ethernet", "eth1", "mtu", "1500"),
		path("system", "host-name", "r1"),
	)

	diff := DiffPaths(running, candidate, IgnoredPaths)
	deletes := MinimalDeletePaths(diff.Removed, running)

	// Every path that survives (candidate-rendered or ignored) must not
	// sit under any delete.
	survivors := []Path{
		path("interfaces", "ethernet", "eth0", "hw-id", "aa:bb"), // ignored
		path("interfaces", "ethernet", "eth1", "mtu", "1500"),
		path("system", "host-name", "r1"),
	}
	for _, survivor := range survivors {
		for _, del := range deletes {
			if survivor.HasPrefix(del) {
				t.Fatalf("delete %v would remove surviving path %v", del, survivor)
			}
		}
	}

	// And every removed path must actually be covered by some delete.
	for _, removed := range diff.Removed {
		covered := false
		for _, del := range deletes {
			if removed.HasPrefix(del) {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("removed path %v is not covered by any delete %v", removed, deletes)
		}
	}
}

// TestIgnoredPathsNeverCollapseIntoDeletes: hw-id stays in running, so a
// whole-interface (or wider) delete cannot swallow it — the collapse
// anchors at the mtu node, below the ignored sibling.
func TestIgnoredPathsNeverCollapseIntoDeletes(t *testing.T) {
	running := NewSet(
		path("interfaces", "ethernet", "eth0", "hw-id", "aa:bb"),
		path("interfaces", "ethernet", "eth0", "mtu", "9000"),
	)
	diff := DiffPaths(running, NewSet(), IgnoredPaths)
	assertPaths(t, diff.Removed, path("interfaces", "ethernet", "eth0", "mtu", "9000"))
	assertPaths(t, MinimalDeletePaths(diff.Removed, running),
		path("interfaces", "ethernet", "eth0", "mtu"))
}

func TestVerifyCryptHash(t *testing.T) {
	tests := []struct {
		name     string
		password string
		hashed   string
		want     CryptResult
	}{
		{"match", "Hello world!", specHash, CryptMatch},
		{"mismatch", "wrong password", specHash, CryptMismatch},
		{"yescrypt is unknown", "x", "$y$j9T$salt$hash", CryptUnknown},
		{"plain text is unknown", "x", "not-a-hash", CryptUnknown},
		{"md5 crypt is unknown", "x", "$1$salt$hash", CryptUnknown},
		{"empty hash is unknown", "x", "", CryptUnknown},
		{"malformed sha512 is unknown", "x", "$6$", CryptUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := VerifyCryptHash(tc.password, tc.hashed); got != tc.want {
				t.Fatalf("VerifyCryptHash(%q, %q) = %v, want %v", tc.password, tc.hashed, got, tc.want)
			}
		})
	}
}

func TestReconcileHashedPasswords(t *testing.T) {
	t.Run("matching password produces no diff", func(t *testing.T) {
		running := NewSet(append(loginPrefix.Clone(), "encrypted-password", specHash))
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "Hello world!"))

		r, c := ReconcileHashedPasswords(running, candidate)
		diff := DiffPaths(r, c, IgnoredPaths)
		if diff.HasChanges() {
			t.Fatalf("unchanged password reported as drift: %s", FormatDiff(diff, false))
		}
		// It must also not be rewritten on deploy.
		if len(MinimalDeletePaths(diff.Removed, r)) != 0 {
			t.Fatalf("unchanged password produced deletes")
		}
	})

	t.Run("changed password shows as add and remove", func(t *testing.T) {
		running := NewSet(append(loginPrefix.Clone(), "encrypted-password", specHash))
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "new password"))

		r, c := ReconcileHashedPasswords(running, candidate)
		diff := DiffPaths(r, c, IgnoredPaths)
		assertPaths(t, diff.Added, append(loginPrefix.Clone(), "plaintext-password", "new password"))
		// reconcile rewrites the device hash under plaintext-password so
		// both sides share the node; a mismatch is a plain -old +new.
		assertPaths(t, diff.Removed, append(loginPrefix.Clone(), "plaintext-password", specHash))

		// Both sides redact: neither the new password nor the old hash
		// may appear in the printed diff.
		text := FormatDiff(diff, true)
		if strings.Contains(text, "new password") || strings.Contains(text, specHash) {
			t.Fatalf("secret leaked into diff: %q", text)
		}
	})

	t.Run("unknown scheme leaves both sides alone", func(t *testing.T) {
		running := NewSet(append(loginPrefix.Clone(), "encrypted-password", "$y$j9T$salt$hash"))
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "whatever"))

		r, c := ReconcileHashedPasswords(running, candidate)
		assertSet(t, r, running.Sorted()...)
		assertSet(t, c, candidate.Sorted()...)
	})

	t.Run("password without a device counterpart is an addition", func(t *testing.T) {
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "hunter2"))
		r, c := ReconcileHashedPasswords(NewSet(), candidate)
		assertPaths(t, DiffPaths(r, c, nil).Added,
			append(loginPrefix.Clone(), "plaintext-password", "hunter2"))
	})

	t.Run("other paths untouched", func(t *testing.T) {
		running := NewSet(path("system", "host-name", "r1"))
		candidate := NewSet(path("system", "host-name", "r2"))
		r, c := ReconcileHashedPasswords(running, candidate)
		assertSet(t, r, path("system", "host-name", "r1"))
		assertSet(t, c, path("system", "host-name", "r2"))
	})

	t.Run("inputs are not mutated", func(t *testing.T) {
		running := NewSet(append(loginPrefix.Clone(), "encrypted-password", specHash))
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "Hello world!"))
		ReconcileHashedPasswords(running, candidate)
		assertSet(t, running, append(loginPrefix.Clone(), "encrypted-password", specHash))
		assertSet(t, candidate, append(loginPrefix.Clone(), "plaintext-password", "Hello world!"))
	})

	t.Run("hash on a different user is not matched", func(t *testing.T) {
		other := Path{"system", "login", "user", "someone", "authentication"}
		running := NewSet(append(other.Clone(), "encrypted-password", specHash))
		candidate := NewSet(append(loginPrefix.Clone(), "plaintext-password", "Hello world!"))

		r, c := ReconcileHashedPasswords(running, candidate)
		diff := DiffPaths(r, c, nil)
		assertPaths(t, diff.Added, append(loginPrefix.Clone(), "plaintext-password", "Hello world!"))
		assertPaths(t, diff.Removed, append(other.Clone(), "encrypted-password", specHash))
	})

	t.Run("two users reconcile independently", func(t *testing.T) {
		a := Path{"system", "login", "user", "a", "authentication"}
		b := Path{"system", "login", "user", "b", "authentication"}
		running := NewSet(
			append(a.Clone(), "encrypted-password", specHash),
			append(b.Clone(), "encrypted-password", specHash),
		)
		candidate := NewSet(
			append(a.Clone(), "plaintext-password", "Hello world!"), // matches
			append(b.Clone(), "plaintext-password", "nope"),         // does not
		)

		r, c := ReconcileHashedPasswords(running, candidate)
		diff := DiffPaths(r, c, nil)
		assertPaths(t, diff.Added, append(b.Clone(), "plaintext-password", "nope"))
		assertPaths(t, diff.Removed, append(b.Clone(), "plaintext-password", specHash))
	})
}

// TestEndToEndVyOSDiff walks a rendered config against a device JSON tree
// the way the deploy path does, exercising every stage together.
func TestEndToEndVyOSDiff(t *testing.T) {
	rendered := strings.Join([]string{
		"# barf-generated",
		"set system host-name r1",
		"set system name-server 1.1.1.1",
		"set interfaces ethernet eth0 mtu 9000",
		"set interfaces ethernet eth0 description 'uplink a'",
		"set system login user supertech authentication plaintext-password 'Hello world!'",
	}, "\n")

	deviceJSON := `{
		"system": {
			"host-name": "r1",
			"name-server": ["1.1.1.1", "8.8.8.8"],
			"login": {"user": {"supertech": {"authentication": {
				"encrypted-password": "` + specHash + `"}}}}
		},
		"interfaces": {"ethernet": {"eth0": {
			"mtu": "9000",
			"description": "uplink a",
			"hw-id": "aa:bb:cc:dd:ee:ff"
		}}},
		"service": {"lldp": {"interface": {"all": {}}}}
	}`

	var tree any
	if err := json.Unmarshal([]byte(deviceJSON), &tree); err != nil {
		t.Fatal(err)
	}

	running, candidate := ReconcileHashedPasswords(
		PathsFromAPIJSON(tree), ParseSetCommands(rendered))
	diff := DiffPaths(running, candidate, IgnoredPaths)

	// The password matched, hw-id is ignored, everything else is equal;
	// only the stray name-server and the unrendered lldp service go.
	assertPaths(t, diff.Added)
	assertPaths(t, diff.Removed,
		path("service", "lldp", "interface", "all"),
		path("system", "name-server", "8.8.8.8"),
	)
	if got := SummarizeDiff(diff); got != "+0 -2" {
		t.Fatalf("summary = %q", got)
	}

	// name-server 1.1.1.1 survives, so its delete stays at the leaf; the
	// lldp subtree is wholly stale and collapses to the service node.
	assertPaths(t, MinimalDeletePaths(diff.Removed, running),
		path("service"),
		path("system", "name-server", "8.8.8.8"),
	)
}
