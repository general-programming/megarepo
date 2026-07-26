package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Regression tests for the two ways this package used to wedge or leak.
// Everything here talks to in-process servers over loopback.

// -- finding 1: a black-holed path used to hang forever ---------------

// blackHole is a TCP proxy that stops forwarding without closing
// anything, mimicking a drained BGP path: the socket stays "up" and no
// FIN or RST ever arrives.
type blackHole struct {
	listener net.Listener
	upstream string
	frozen   atomic.Bool
	wg       sync.WaitGroup

	mu    sync.Mutex
	conns []net.Conn
}

func (b *blackHole) track(conns ...net.Conn) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.conns = append(b.conns, conns...)
}

// shutdown closes every proxied connection: a frozen pump is parked in
// Read with nothing else to wake it.
func (b *blackHole) shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, c := range b.conns {
		_ = c.Close()
	}
	b.conns = nil
}

func newBlackHole(t *testing.T, upstream string) *blackHole {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &blackHole{listener: listener, upstream: upstream}

	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", upstream)
			if err != nil {
				_ = client.Close()
				continue
			}
			b.track(client, server)
			b.wg.Add(2)
			go b.pump(client, server)
			go b.pump(server, client)
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		b.shutdown()
		b.wg.Wait()
	})
	return b
}

func (b *blackHole) freeze() { b.frozen.Store(true) }

func (b *blackHole) addr() (string, int) {
	host, port, _ := net.SplitHostPort(b.listener.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p
}

func (b *blackHole) pump(from, to net.Conn) {
	defer b.wg.Done()
	buf := make([]byte, 32*1024)
	for {
		n, err := from.Read(buf)
		if n > 0 && !b.frozen.Load() {
			if _, werr := to.Write(buf[:n]); werr != nil {
				return
			}
		}
		// Frozen: bytes are dropped silently, nothing for the peer to notice.
		if err != nil {
			return
		}
	}
}

// Regression: exec blocked unconditionally on <-done and dialOnce clears
// the socket deadline after the handshake, so a black-holed path (what a
// drain-and-reboot produces) left Run wedged past its timeout.
func TestRunReturnsOnABlackHoledPath(t *testing.T) {
	release := make(chan struct{})
	server := newTestServer(t, "pw", func(string, string) (string, string, int) {
		<-release
		return "", "", 0
	})
	t.Cleanup(func() { close(release) })

	host, port := server.addr()
	hole := newBlackHole(t, net.JoinHostPort(host, strconv.Itoa(port)))
	proxyHost, proxyPort := hole.addr()

	no := false
	client, err := Dial(t.Context(), "test-device", proxyHost, Options{
		Port:          proxyPort,
		Credentials:   StaticCredentials{Username: "supertech", Password: "pw"},
		Agent:         &no,
		IdentityFiles: []string{},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	client.closeGrace = 200 * time.Millisecond

	// The handshake is done; now the path goes away underneath us.
	hole.freeze()

	const timeout = 300 * time.Millisecond
	// Generous: the point is "bounded", not "fast".
	bound := timeout + 4*client.grace() + 2*time.Second

	returned := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := client.Run(t.Context(), "/config/scripts/drain-and-reboot", timeout)
		returned <- err
	}()

	select {
	case err := <-returned:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected a timeout error, got %v", err)
		}
		t.Logf("Run returned after %s", time.Since(start))
	case <-time.After(bound):
		t.Fatalf("Run has not returned %s after its %s timeout: "+
			"a black-holed path still wedges the caller", bound, timeout)
	}

	// The transport was torn down, not merely the session.
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := client.Run(t.Context(), "echo", time.Second); err == nil {
			t.Error("expected the second command to fail on a dead transport")
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the transport was not closed: a second command wedged too")
	}
}

// The other half of the fix: bounding the wait must not abandon the
// session goroutine.
func TestTimedOutRunDoesNotLeakItsGoroutine(t *testing.T) {
	release := make(chan struct{})
	server := newTestServer(t, "pw", func(string, string) (string, string, int) {
		<-release
		return "", "", 0
	})
	t.Cleanup(func() { close(release) })

	host, port := server.addr()
	hole := newBlackHole(t, net.JoinHostPort(host, strconv.Itoa(port)))
	proxyHost, proxyPort := hole.addr()

	no := false
	client, err := Dial(t.Context(), "test-device", proxyHost, Options{
		Port:          proxyPort,
		Credentials:   StaticCredentials{Username: "supertech", Password: "pw"},
		Agent:         &no,
		IdentityFiles: []string{},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client.closeGrace = 100 * time.Millisecond
	hole.freeze()

	// Baseline with the connection up, so proxy pumps and the ssh mux are
	// counted on both sides.
	settle()
	before := runtime.NumGoroutine()

	if _, err := client.Run(t.Context(), "wedge", 100*time.Millisecond); err == nil {
		t.Fatal("expected a timeout")
	}
	_ = client.Close()

	// Transport gone, so the session goroutine's read fails and it retires
	// (buffered channel, no reader needed). Give the runtime time to reap.
	deadline := time.Now().Add(10 * time.Second)
	for {
		settle()
		if got := runtime.NumGoroutine(); got <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines leaked: %d before the timed-out command, %d after",
				before, runtime.NumGoroutine())
		}
	}
}

func settle() {
	for range 3 {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
}

// -- finding 3: one agent socket fd leaked per Dial -------------------

// fakeAgent is a real SSH agent on a unix socket; the leak shows up as a
// gap between the opened and client-closed counts.
type fakeAgent struct {
	path   string
	keys   agent.Agent
	opened atomic.Int64
	closed atomic.Int64
	wg     sync.WaitGroup

	mu    sync.Mutex
	conns []net.Conn
}

// shutdown closes connections the client left open, so a FAILING test
// reports its assertion instead of hanging in cleanup.
func (a *fakeAgent) shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, c := range a.conns {
		_ = c.Close()
	}
	a.conns = nil
}

func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	// Unix socket paths are length-limited; keep the dir name short.
	dir, err := os.MkdirTemp("", "sshxag")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	a := &fakeAgent{path: filepath.Join(dir, "s"), keys: agent.NewKeyring()}
	listener, err := net.Listen("unix", a.path)
	if err != nil {
		t.Skipf("unix sockets unavailable: %v", err)
	}

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			a.opened.Add(1)
			a.mu.Lock()
			a.conns = append(a.conns, conn)
			a.mu.Unlock()
			a.wg.Add(1)
			go func() {
				defer a.wg.Done()
				// ServeAgent returns when the client closes its end.
				_ = agent.ServeAgent(a.keys, conn)
				_ = conn.Close()
				a.closed.Add(1)
			}()
		}
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		a.shutdown()
		a.wg.Wait()
	})
	t.Setenv("SSH_AUTH_SOCK", a.path)
	return a
}

// Regression: keyAuthMethods dialled SSH_AUTH_SOCK and never closed the
// conn it handed to agent.NewClient(...).Signers, leaking an fd per Dial.
func TestDialClosesTheAgentSocket(t *testing.T) {
	ag := newFakeAgent(t)

	// Nothing listening: every dial fails at connect, the worst leak path.
	dead := deadPort(t)

	const dials = 50
	for i := range dials {
		_, err := Dial(t.Context(), "test-device", "127.0.0.1", Options{
			Port:           dead,
			Username:       "operator",
			IdentityFiles:  []string{},
			ConnectTimeout: time.Second,
		})
		if err == nil {
			t.Fatalf("dial %d unexpectedly succeeded against a dead port", i)
		}
	}

	// A unix net.Dial returns before the agent's Accept runs, so poll.
	waitForCount(t, "agent connections opened", dials, &ag.opened)
	waitForCount(t, "agent sockets closed", dials, &ag.closed)
}

// The other side of the fix: the agent connection must survive until the
// handshake has pulled its signers out of it.
func TestAgentAuthStillWorks(t *testing.T) {
	ag := newFakeAgent(t)

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	if err := ag.keys.Add(agent.AddedKey{PrivateKey: private}); err != nil {
		t.Fatalf("adding key to the agent: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	wanted := signer.PublicKey().Marshal()

	// Accept exactly the agent's key, and nothing else.
	server := newTestServerWith(t, "", func(command, _ string) (string, string, int) {
		return command + "\n", "", 0
	}, func(config *ssh.ServerConfig) {
		config.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(wanted) {
				return &ssh.Permissions{}, nil
			}
			return nil, errors.New("unknown key")
		}
	})

	host, port := server.addr()
	client, err := Dial(t.Context(), "test-device", host, Options{
		Port:          port,
		Username:      "operator",
		IdentityFiles: []string{},
	})
	if err != nil {
		t.Fatalf("agent auth failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	result, err := client.Run(t.Context(), "show version", 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(result.Output, "show version") {
		t.Fatalf("unexpected output %q", result.Output)
	}

	// The session outlived the handshake; the agent socket did not.
	deadline := time.Now().Add(5 * time.Second)
	for ag.closed.Load() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the agent socket was never closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func deadPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	_ = listener.Close()
	p, _ := strconv.Atoi(port)
	return p
}

func waitForCount(t *testing.T, what string, want int64, counter *atomic.Int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := counter.Load()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %d, want %d", what, got, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
