package pytext

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// Replays a corpus captured from CPython's `shlex` (regenerate by running
// shlex.split / shlex.quote over the same inputs). The Go port must agree on
// every entry, including the inputs where Python raises: a slightly wrong
// quoter is a command-injection bug.

type shlexCorpus struct {
	// Split entries are [input, expected tokens]; a non-list second element
	// means Python raised.
	Split [][]json.RawMessage `json:"shlex"`
	// Quote entries are [input, expected output].
	Quote [][]string `json:"quote"`
}

func loadShlexCorpus(t *testing.T) shlexCorpus {
	t.Helper()
	raw, err := os.ReadFile("testdata/python_shlex.json")
	if err != nil {
		t.Fatalf("reading reference corpus: %v", err)
	}
	var corpus shlexCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatalf("decoding reference corpus: %v", err)
	}
	return corpus
}

func TestReferenceShellSplit(t *testing.T) {
	corpus := loadShlexCorpus(t)
	if len(corpus.Split) == 0 {
		t.Fatal("empty shlex corpus")
	}
	for _, entry := range corpus.Split {
		var in string
		if err := json.Unmarshal(entry[0], &in); err != nil {
			t.Fatal(err)
		}
		var want []string
		wantErr := json.Unmarshal(entry[1], &want) != nil

		got, err := ShellSplit(in)
		if wantErr {
			if err == nil {
				t.Errorf("ShellSplit(%q) = %#v, python raised", in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ShellSplit(%q): %v, python got %#v", in, err, want)
			continue
		}
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("ShellSplit(%q) = %#v, python got %#v", in, got, want)
		}
	}
}

func TestReferenceShellQuote(t *testing.T) {
	corpus := loadShlexCorpus(t)
	if len(corpus.Quote) == 0 {
		t.Fatal("empty quote corpus")
	}
	for _, entry := range corpus.Quote {
		if got := ShellQuote(entry[0]); got != entry[1] {
			t.Errorf("ShellQuote(%q) = %q, python got %q", entry[0], got, entry[1])
		}
	}
}
