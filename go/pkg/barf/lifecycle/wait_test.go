package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeClock advances by step on every read, so the timeout logic runs
// without the test waiting for anything.
type fakeClock struct {
	now  time.Time
	step time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.now = c.now.Add(c.step)
	return c.now
}

func noSleep(t *testing.T) {
	t.Helper()
	old := sleep
	sleep = func(context.Context, time.Duration) error { return nil }
	t.Cleanup(func() { sleep = old })
}

func TestWaitForAliveIgnoresTheOldVersion(t *testing.T) {
	noSleep(t)

	// A rebooting device can keep answering on the old image for a
	// while; those answers must not count as "it came back".
	answers := []string{"old", "old", "old", "new"}
	i := 0
	version := func(context.Context) (string, error) {
		v := answers[i]
		if i < len(answers)-1 {
			i++
		}
		return v, nil
	}

	var out bytes.Buffer
	got, err := WaitForAlive(context.Background(), "sea1-leaf-0", version, "old", WaitOptions{Out: &out})
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got != "new" {
		t.Fatalf("version = %q", got)
	}
	if i != 3 {
		t.Fatalf("expected to poll past the stale answers, stopped at %d", i)
	}
	if !strings.Contains(out.String(), "device came back after") {
		t.Fatalf("no completion line:\n%s", out.String())
	}
}

func TestWaitForAliveTreatsPlaceholdersAsDown(t *testing.T) {
	noSleep(t)

	answers := []string{"", "-", "new"}
	i := 0
	version := func(context.Context) (string, error) {
		v := answers[i]
		if i < len(answers)-1 {
			i++
		}
		return v, nil
	}

	got, err := WaitForAlive(context.Background(), "sea1-leaf-0", version, "old", WaitOptions{})
	if err != nil || got != "new" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestWaitForAliveTimesOut(t *testing.T) {
	noSleep(t)

	version := func(context.Context) (string, error) { return "", errors.New("down") }
	clock := &fakeClock{now: time.Unix(0, 0), step: time.Minute}

	_, err := WaitForAlive(context.Background(), "sea1-leaf-0", version, "old",
		WaitOptions{Timeout: 3 * time.Minute, now: clock.Now})
	if err == nil || !strings.Contains(err.Error(), "did not come back") {
		t.Fatalf("expected a timeout, got %v", err)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:  "45s",
		95 * time.Second:  "1m35s",
		120 * time.Second: "2m00s",
		0:                 "0s",
	}
	for in, want := range cases {
		if got := HumanDuration(in); got != want {
			t.Fatalf("HumanDuration(%s) = %q, want %q", in, got, want)
		}
	}
}
