package progress

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// clock is a manual clock so every rendering test is deterministic.
type clock struct{ t time.Time }

func newClock() *clock {
	return &clock{t: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

const iso = "vyos-2026.07.21-1151-rolling-generic-amd64.iso"

func transfer(total int64) Transfer {
	return Transfer{Op: OpDownload, Name: iso, Total: total, URL: "https://example.invalid/" + iso}
}

func TestBytesAndDuration(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{700 * 1024 * 1024, "700.0 MiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := Bytes(c.n); got != c.want {
			t.Errorf("Bytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{11 * time.Second, "0:11"},
		{95 * time.Second, "1:35"},
		{2*time.Hour + 3*time.Minute + 4*time.Second, "2:03:04"},
		{-time.Second, "0:00"},
	} {
		if got := Duration(c.d); got != c.want {
			t.Errorf("Duration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

// TestTerminalRedrawsInPlace pins the terminal contract: one header line, then
// a single line rewritten with \r, cleared before the summary.
func TestTerminalRedrawsInPlace(t *testing.T) {
	var out bytes.Buffer
	c := newClock()
	r := NewReporter(&out, Options{Prefix: "[sea1-leaf-0] ", Terminal: true, Now: c.now})

	total := int64(700 * 1024 * 1024)
	r.Start(transfer(total))
	c.add(2 * time.Second)
	r.Update(total / 4)
	c.add(2 * time.Second)
	r.Update(total / 2)
	c.add(4 * time.Second)
	r.Finish(total, nil)

	got := out.String()
	if strings.Count(got, "\r") != 3 { // two redraws plus the pre-summary clear
		t.Fatalf("expected in-place redraws, got:\n%q", got)
	}
	if !strings.Contains(got, "\x1b[K") {
		t.Fatalf("terminal mode must erase the line it rewrites:\n%q", got)
	}
	for _, want := range []string{
		"[sea1-leaf-0] downloading " + iso + " (700.0 MiB)",
		" 25%",
		"175.0 MiB / 700.0 MiB",
		"ETA ",
		"[sea1-leaf-0] downloaded " + iso + ": 700.0 MiB in 0:08 (87.5 MiB/s)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%q", want, got)
		}
	}
}

// TestPlainModeIsLineOrientedAndAnsiFree is the piping guarantee: no ANSI, no
// carriage returns, one line per update.
func TestPlainModeIsLineOrientedAndAnsiFree(t *testing.T) {
	var out bytes.Buffer
	c := newClock()
	r := NewReporter(&out, Options{Prefix: "[sea1-leaf-0] ", Now: c.now})

	total := int64(1000)
	r.Start(transfer(total))
	for n := int64(100); n <= total; n += 100 {
		c.add(time.Second)
		r.Update(n)
	}
	r.Finish(total, nil)

	got := out.String()
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("plain output must be ANSI/CR free:\n%q", got)
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	// Header, ten 10% steps, summary: the interval (5s) alone would give fewer.
	if len(lines) != 12 {
		t.Fatalf("expected a line per 10%%, got %d:\n%s", len(lines), got)
	}
	if !strings.HasPrefix(lines[0], "[sea1-leaf-0] downloading ") {
		t.Fatalf("first line = %q", lines[0])
	}
	if strings.Contains(got, "[==") {
		t.Fatalf("no bar off a terminal, got:\n%s", got)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "[sea1-leaf-0] ") {
			t.Fatalf("every line keeps the hostname prefix, got %q", line)
		}
	}
}

// TestPlainModeThrottlesToTheInterval keeps a piped log from being one line
// per read when the total is unknown and percentage steps cannot fire.
func TestPlainModeThrottlesToTheInterval(t *testing.T) {
	var out bytes.Buffer
	c := newClock()
	r := NewReporter(&out, Options{Interval: 5 * time.Second, Now: c.now})

	r.Start(Transfer{Op: OpDownload, Name: iso})
	for i := 0; i < 100; i++ {
		c.add(time.Second)
		r.Update(int64(i+1) * 1024)
	}
	r.Finish(100*1024, nil)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	// 100 seconds at one line per 5s, plus header and summary.
	if len(lines) != 22 {
		t.Fatalf("throttling produced %d lines:\n%s", len(lines), out.String())
	}
	if strings.Contains(out.String(), "%") {
		t.Fatal("an unknown total must not claim a percentage")
	}
}

func TestFailureNamesTheURLAndHowFarItGot(t *testing.T) {
	var out bytes.Buffer
	c := newClock()
	r := NewReporter(&out, Options{Prefix: "[sea1-leaf-0] ", Terminal: true, Now: c.now})

	total := int64(700 * 1024 * 1024)
	r.Start(transfer(total))
	c.add(30 * time.Second)
	r.Update(total / 3)
	r.Finish(total/3, errors.New("context deadline exceeded"))

	got := out.String()
	for _, want := range []string{
		"download of " + iso + " FAILED after 233.3 MiB of 700.0 MiB (33%)",
		"from https://example.invalid/" + iso,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%q", want, got)
		}
	}
}

// TestSkipIsDistinctFromAnInstantDownload is the whole point of Skip: a cache
// hit must not read like a 0-second transfer.
func TestSkipIsDistinctFromAnInstantDownload(t *testing.T) {
	var out bytes.Buffer
	r := NewReporter(&out, Options{Prefix: "[sea1-leaf-0] ", Now: newClock().now})
	r.Skip(transfer(700*1024*1024), "already in the local cache at /home/n/.cache/barf/images/x.iso")

	got := out.String()
	want := "[sea1-leaf-0] no download needed for " + iso + " (700.0 MiB): already in the local cache"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("Skip line = %q, want prefix %q", got, want)
	}
}

func TestUpdateWithoutStartIsIgnored(t *testing.T) {
	var out bytes.Buffer
	r := NewReporter(&out, Options{Now: newClock().now})
	r.Update(10)
	r.Finish(10, nil)
	if out.Len() != 0 {
		t.Fatalf("no transfer was started, got %q", out.String())
	}
}

// TestWriterIsByteExact is the safety property: progress must not change what
// lands on disk.
func TestWriterIsByteExact(t *testing.T) {
	payload := bytes.Repeat([]byte("vyos"), 5000)
	var dst bytes.Buffer
	var out bytes.Buffer
	c := newClock()
	r := NewReporter(&out, Options{Now: c.now, Interval: time.Nanosecond})

	r.Start(transfer(int64(len(payload))))
	w := NewWriter(&dst, r)
	n, err := io.Copy(w, bytes.NewReader(payload))
	c.add(time.Second)
	r.Finish(w.N(), err)

	if err != nil || n != int64(len(payload)) {
		t.Fatalf("copy = %d, %v", n, err)
	}
	if !bytes.Equal(dst.Bytes(), payload) {
		t.Fatal("the counting writer changed the bytes")
	}
	if w.N() != int64(len(payload)) {
		t.Fatalf("counted %d bytes, wrote %d", w.N(), len(payload))
	}
	if !strings.Contains(out.String(), "100%") {
		t.Fatalf("progress never reached 100%%:\n%s", out.String())
	}
}

func TestReaderCountsAndPassesThrough(t *testing.T) {
	payload := []byte("sig-bytes")
	rec := &recordingSink{}
	rd := NewReader(bytes.NewReader(payload), rec)
	got, err := io.ReadAll(rd)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
	if rd.N() != int64(len(payload)) || rec.last != int64(len(payload)) {
		t.Fatalf("counted %d, sink saw %d", rd.N(), rec.last)
	}
}

// TestContextSinkRoundTrip covers the plumbing firmware relies on.
func TestContextSinkRoundTrip(t *testing.T) {
	if FromContext(context.Background()) != Discard {
		t.Fatal("an unset context must yield Discard, never nil")
	}
	//nolint:staticcheck // a nil context is exactly what this guards against.
	if FromContext(nil) != Discard {
		t.Fatal("a nil context must yield Discard")
	}
	rec := &recordingSink{}
	ctx := WithSink(context.Background(), rec)
	if FromContext(ctx) != Sink(rec) {
		t.Fatal("WithSink/FromContext did not round-trip")
	}
	if WithSink(ctx, nil) != ctx {
		t.Fatal("WithSink(nil) must leave the context alone")
	}
	// Discard must stay safe to call.
	Discard.Start(transfer(1))
	Discard.Update(1)
	Discard.Finish(1, nil)
	Discard.Skip(transfer(1), "why")
}

type recordingSink struct {
	started []Transfer
	last    int64
	done    int
	err     error
	skips   []string
}

func (s *recordingSink) Start(t Transfer) { s.started = append(s.started, t) }
func (s *recordingSink) Update(n int64)   { s.last = n }
func (s *recordingSink) Finish(n int64, err error) {
	s.last, s.done, s.err = n, s.done+1, err
}
func (s *recordingSink) Skip(_ Transfer, reason string) { s.skips = append(s.skips, reason) }
