package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/firmware"
)

// -- nothing was cancellable ------------------------------------------

// Regression: Execute called root.Execute(), so everything ran under
// context.Background() and the ctx plumbing below was dead. The handler
// must also be released, or a second Ctrl-C is swallowed.
func TestSignalContextCancelsAndRestoresTheDefaultHandler(t *testing.T) {
	var stopped atomic.Int64
	fired := make(chan struct{})

	original := notifyContext
	t.Cleanup(func() { notifyContext = original })
	notifyContext = func(parent context.Context, _ ...os.Signal) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		go func() {
			<-fired
			cancel()
		}()
		return ctx, func() { stopped.Add(1) }
	}

	ctx, stop := signalContext(context.Background())
	defer stop()

	if ctx.Err() != nil {
		t.Fatal("the context was cancelled before any signal arrived")
	}
	if stopped.Load() != 0 {
		t.Fatal("the signal handler was released before the first signal")
	}

	close(fired) // "Ctrl-C"

	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the first signal did not cancel the command context")
	}

	// The default disposition must come back so a second Ctrl-C kills.
	deadline := time.Now().Add(2 * time.Second)
	for stopped.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the signal handler was never released: a second Ctrl-C would be swallowed " +
				"and the user could not abort a slow cleanup")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSignalContextUsesTheRealSignals(t *testing.T) {
	var got []os.Signal
	original := notifyContext
	t.Cleanup(func() { notifyContext = original })
	notifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		got = signals
		return context.WithCancel(parent)
	}

	_, stop := signalContext(context.Background())
	stop()

	if len(got) != 2 || got[0] != os.Interrupt {
		t.Fatalf("signals = %v, want interrupt and SIGTERM", got)
	}
	// Compare SIGTERM by string to stay portable.
	if !strings.Contains(strings.ToLower(got[1].String()), "term") {
		t.Fatalf("second signal = %v, want SIGTERM", got[1])
	}
}

// A cancelled context must stop devices being contacted, end to end.
func TestStatusStopsProbingWhenTheContextIsCancelled(t *testing.T) {
	h := newHarness(t)
	h.reachable["10.0.0.1"] = true
	h.reachable["10.0.0.2"] = true
	h.readers["sea1-vpn-0"] = fakeReader{running: "set system host-name sea1-vpn-0\n"}
	h.readers["fmt2-core"] = fakeReader{running: "set system host-name fmt2-core\n"}

	var dials atomic.Int64
	inner := dialContext
	dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		return inner(ctx, network, address)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := runStatus(ctx, h.options(), nil, false); err != nil {
		t.Fatalf("status: %v", err)
	}
	if dials.Load() != 0 {
		t.Fatalf("%d devices were contacted after the run was cancelled", dials.Load())
	}
	if !strings.Contains(h.out.String(), "cancelled") {
		t.Fatalf("cancelled devices are not reported as such:\n%s", h.out.String())
	}
}

// -- fan-out sized by input, semaphore not ctx-aware ------------------

// Regression: pools started one goroutine per device, each parked on a
// semaphore, so 300 devices meant 300 goroutines.
func TestRunBoundedCapsGoroutinesNotJustNetworkCalls(t *testing.T) {
	const items = 300

	var live, peak atomic.Int64
	var peakGoroutines atomic.Int64
	baseline := int64(runtime.NumGoroutine())

	var done atomic.Int64
	runBounded(items, func(int) {
		now := live.Add(1)
		for {
			was := peak.Load()
			if now <= was || peak.CompareAndSwap(was, now) {
				break
			}
		}
		if extra := int64(runtime.NumGoroutine()) - baseline; extra > peakGoroutines.Load() {
			peakGoroutines.Store(extra)
		}
		time.Sleep(time.Millisecond)
		live.Add(-1)
		done.Add(1)
	})

	if done.Load() != items {
		t.Fatalf("%d of %d items ran", done.Load(), items)
	}
	if peak.Load() > maxProbes {
		t.Fatalf("peak concurrency = %d, want at most %d", peak.Load(), maxProbes)
	}
	// Generous ceiling: the point is it does not scale with items.
	if peakGoroutines.Load() > maxProbes+8 {
		t.Fatalf("peak extra goroutines = %d for %d items; the fan-out is still sized by the input",
			peakGoroutines.Load(), items)
	}
}

// Regression: a plain `sem <- struct{}{}` cannot be interrupted, so on
// quit the queued probes kept contacting devices as slots freed.
func TestAcquireIsCancellable(t *testing.T) {
	sem := make(chan struct{}, 1)
	sem <- struct{}{} // fully occupied; nobody will ever release it

	ctx, cancel := context.WithCancel(context.Background())

	returned := make(chan bool, 1)
	go func() { returned <- acquire(ctx, sem) }()

	select {
	case <-returned:
		t.Fatal("acquire succeeded against a full semaphore")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case ok := <-returned:
		if ok {
			t.Fatal("acquire reported success after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled acquire stayed parked on the semaphore")
	}
}

// Winning a slot then finding the run cancelled must not strand it.
func TestAcquireReleasesTheSlotItTookWhenCancelled(t *testing.T) {
	sem := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if acquire(ctx, sem) {
		t.Fatal("acquire admitted a probe under a cancelled context")
	}
	if len(sem) != 0 {
		t.Fatal("the semaphore slot was not handed back")
	}
	if !acquire(context.Background(), sem) {
		t.Fatal("the semaphore was left unusable")
	}
}

// -- firmwareProvider.latest was read without the lock ----------------

// -race reproduction: IsCurrent read f.latest with no once.Do and no
// lock while LatestVersion wrote it from inside f.once.Do.
func TestFirmwareProviderIsCurrentDoesNotRaceLatestVersion(t *testing.T) {
	release := make(chan struct{})
	f := &firmwareProvider{provider: blockingProvider{release: release}}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.LatestVersion(t.Context())
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.IsCurrent("1.5-rolling")
		}()
	}
	close(release)
	wg.Wait()

	if !f.IsCurrent("1.5-rolling") {
		t.Fatal("IsCurrent does not see the resolved release tag")
	}
	if f.IsCurrent("1.4-rolling") {
		t.Fatal("IsCurrent matched the wrong version")
	}
}

// blockingProvider resolves a fixed tag once release is closed, widening
// the IsCurrent/LatestVersion overlap window.
type blockingProvider struct {
	release chan struct{}
}

func (p blockingProvider) LatestVersion(context.Context) (string, error) {
	<-p.release
	return "1.5-rolling", nil
}

func (p blockingProvider) IsCurrent(context.Context, string) (bool, error) {
	return false, errors.New("not used")
}

func (p blockingProvider) Image(context.Context) (firmware.Asset, error) {
	return firmware.Asset{}, errors.New("not used")
}

func (p blockingProvider) Signature(context.Context) (firmware.Asset, bool, error) {
	return firmware.Asset{}, false, errors.New("not used")
}

func (p blockingProvider) Download(context.Context, firmware.Asset) (string, error) {
	return "", errors.New("not used")
}
