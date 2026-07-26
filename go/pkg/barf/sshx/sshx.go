// Package sshx is the SSH transport the device-lifecycle commands run on,
// porting barf/util/ssh.py and barf/util/vyos_scripts.py. The safety gates
// live in barf/lifecycle.
package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/common/pytext"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// DefaultPort is the SSH port the fleet listens on.
const DefaultPort = 22

// DefaultConnectTimeout mirrors DeviceSSH's connect_timeout default.
const DefaultConnectTimeout = 10 * time.Second

// Credentials is a username/password pair; nothing here logs or persists it.
type Credentials struct {
	Username string
	Password string
}

// CredentialSource supplies the shared "supertech" account from Vault. Local
// interface so this package does not depend on go/client/vault.
type CredentialSource interface {
	Supertech(ctx context.Context) (Credentials, error)
}

// StaticCredentials is a CredentialSource returning a fixed pair.
type StaticCredentials Credentials

// Supertech implements CredentialSource.
func (s StaticCredentials) Supertech(context.Context) (Credentials, error) {
	return Credentials(s), nil
}

// Options configures a Dial.
type Options struct {
	// Username logs in with keys only; empty means the supertech user.
	Username string

	// Credentials resolves supertech. Required unless Username is set.
	Credentials CredentialSource

	// Port is the SSH port; 0 means DefaultPort.
	Port int

	// ConnectTimeout bounds each auth attempt; 0 means the default.
	ConnectTimeout time.Duration

	// HostKeyCallback is the host-key policy; nil accepts whatever the device
	// presents, because every image install reinstalls the fleet's host keys
	// and the management path is a private fabric.
	HostKeyCallback ssh.HostKeyCallback

	// Agent enables SSH_AUTH_SOCK agent auth; nil means enabled.
	Agent *bool

	// IdentityFiles are private keys to offer; nil means ~/.ssh/id_*.
	IdentityFiles []string

	// Dial overrides the network dial (tests); nil means net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

func (o Options) port() int {
	if o.Port != 0 {
		return o.Port
	}
	return DefaultPort
}

func (o Options) connectTimeout() time.Duration {
	if o.ConnectTimeout > 0 {
		return o.ConnectTimeout
	}
	return DefaultConnectTimeout
}

func (o Options) agentEnabled() bool {
	return o.Agent == nil || *o.Agent
}

func (o Options) hostKeyCallback() ssh.HostKeyCallback {
	if o.HostKeyCallback != nil {
		return o.HostKeyCallback
	}
	return ssh.InsecureIgnoreHostKey() // #nosec G106 -- see Options.HostKeyCallback
}

// DefaultCloseGrace bounds how long a cancelled command waits for its session
// to unwind before the socket is torn down: past the handshake the transport
// has no deadline, so nothing else frees a black-holed read.
const DefaultCloseGrace = 2 * time.Second

// Client is one authenticated SSH connection to a device.
type Client struct {
	hostname string
	address  string
	username string

	client *ssh.Client
	// conn is kept so a timed-out command can tear the transport down;
	// closing only the session is not enough once the peer stops answering.
	conn net.Conn
	// closeGrace overrides DefaultCloseGrace (tests).
	closeGrace time.Duration

	closeOnce sync.Once
	closeErr  error
}

func (c *Client) grace() time.Duration {
	if c.closeGrace > 0 {
		return c.closeGrace
	}
	return DefaultCloseGrace
}

// Hostname is the device's name (for messages).
func (c *Client) Hostname() string { return c.hostname }

// Address is the address actually connected to.
func (c *Client) Address() string { return c.address }

// Username is the account authenticated as.
func (c *Client) Username() string { return c.username }

// Close closes the connection. Safe to call more than once.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.client.Close()
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
	return c.closeErr
}

// closeTransport drops the socket under the ssh client, the only thing that
// unblocks a read from a dead peer. Unlike Close it must work mid-session.
func (c *Client) closeTransport() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
	_ = c.client.Close()
}

// Dial connects to address as the right user for host: keys in one attempt,
// then — only for supertech — a SECOND attempt with its Vault password,
// because sshd counts every offered agent key against MaxAuthTries and drops
// a combined attempt before the password.
func Dial(ctx context.Context, hostname, address string, opts Options) (*Client, error) {
	if address == "" {
		return nil, fmt.Errorf("sshx: %s: no address to connect to", hostname)
	}

	username := opts.Username
	var password string
	passwordAttempt := false
	if username == "" {
		if opts.Credentials == nil {
			return nil, fmt.Errorf("sshx: %s: no Username and no CredentialSource", hostname)
		}
		creds, err := opts.Credentials.Supertech(ctx)
		if err != nil {
			return nil, fmt.Errorf("sshx: %s: supertech credentials: %w", hostname, err)
		}
		username, password = creds.Username, creds.Password
		passwordAttempt = password != ""
	}

	var attempts []([]ssh.AuthMethod)
	// The agent connection must stay open for all of Dial (signers are pulled
	// per handshake) and closed on every exit path: leaked fds exhaust us.
	methods, closers := keyAuthMethods(opts)
	defer closeAll(closers)
	if len(methods) > 0 {
		attempts = append(attempts, methods)
	}
	if passwordAttempt {
		attempts = append(attempts, []ssh.AuthMethod{ssh.Password(password)})
	}
	if len(attempts) == 0 {
		return nil, fmt.Errorf("sshx: %s: no usable authentication methods for %s@%s",
			hostname, username, address)
	}

	target := net.JoinHostPort(address, strconv.Itoa(opts.port()))
	var failures []string
	for _, methods := range attempts {
		client, conn, err := dialOnce(ctx, target, username, methods, opts)
		if err == nil {
			return &Client{
				hostname: hostname, address: address, username: username,
				client: client, conn: conn,
			}, nil
		}
		failures = append(failures, err.Error())
	}

	return nil, fmt.Errorf("sshx: all SSH auth methods failed for %s@%s: %s",
		username, address, strings.Join(failures, "; "))
}

func dialOnce(ctx context.Context, target, username string, methods []ssh.AuthMethod, opts Options) (*ssh.Client, net.Conn, error) {
	dial := opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: opts.connectTimeout()}
			return d.DialContext(ctx, network, address)
		}
	}

	conn, err := dial(ctx, "tcp", target)
	if err != nil {
		return nil, nil, err
	}

	config := &ssh.ClientConfig{
		User:            username,
		Auth:            methods,
		HostKeyCallback: opts.hostKeyCallback(),
		Timeout:         opts.connectTimeout(),
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(opts.connectTimeout()))
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, target, config)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	// Clear the handshake deadline; long-running scripts must not trip it.
	// Nothing bounds a read from here, which is why a timed-out exec has to
	// close the conn itself (see Client.exec).
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), conn, nil
}

// keyAuthMethods offers the agent when reachable plus readable identity
// files. The returned closers own the agent connection and must outlive every
// handshake using these methods (signers are read lazily).
func keyAuthMethods(opts Options) ([]ssh.AuthMethod, []io.Closer) {
	var methods []ssh.AuthMethod
	var closers []io.Closer

	if opts.agentEnabled() {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if conn, err := net.Dial("unix", sock); err == nil {
				closers = append(closers, conn)
				methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
			}
		}
	}

	var signers []ssh.Signer
	for _, path := range identityFiles(opts) {
		raw, err := os.ReadFile(path) // #nosec G304 -- fixed set of user identity paths
		if err != nil {
			continue
		}
		signer, err := ssh.ParsePrivateKey(raw)
		if err != nil {
			// Encrypted keys are the agent's job; never prompt mid-update.
			continue
		}
		signers = append(signers, signer)
	}
	if len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	return methods, closers
}

func closeAll(closers []io.Closer) {
	for _, c := range closers {
		_ = c.Close()
	}
}

func identityFiles(opts Options) []string {
	if opts.IdentityFiles != nil {
		return opts.IdentityFiles
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var out []string
	for _, name := range []string{"id_ed25519", "id_ecdsa", "id_rsa"} {
		out = append(out, filepath.Join(home, ".ssh", name))
	}
	return out
}

// Result is the outcome of one remote command.
type Result struct {
	// ExitStatus is -1 for a signal kill or a session with no status.
	ExitStatus int
	Stdout     string
	Stderr     string
	// Output is what markers are checked against: stdout, plus stderr unless
	// a pty already merged them.
	Output string
}

// OK reports a clean exit. Deliberately NOT enough for a VyOS oneshot
// script — see ScriptOK.
func (r Result) OK() bool { return r.ExitStatus == 0 }

// DefaultRunTimeout matches DeviceSSH.run's default.
const DefaultRunTimeout = 60 * time.Second

// DefaultScriptTimeout matches DeviceSSH.run_script's default; image
// installs are slow.
const DefaultScriptTimeout = 15 * time.Minute

// Run executes command and captures its exit status, stdout and stderr.
// DeviceSSH.run returns stdout only, but a failure with empty stdout is
// undebuggable.
func (c *Client) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	return c.exec(ctx, command, timeout, execOptions{})
}

type execOptions struct {
	// pty merges stderr into stdout and keeps sudo happy (get_pty=True).
	pty        bool
	stdin      string
	echo       io.Writer
	echoPrefix string
}

func (c *Client) exec(ctx context.Context, command string, timeout time.Duration, opts execOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// NewSession, RequestPty and the exec request each wait unbounded for a
	// peer reply, so device I/O runs on one goroutine select can abandon.
	out := &capture{}
	if opts.echo != nil {
		out.echo = &lineEcho{w: opts.echo, prefix: opts.echoPrefix}
	}

	// Buffered so the goroutine retires with nobody waiting; sessions hands
	// the session to a timed-out exec to close.
	done := make(chan error, 1)
	sessions := make(chan *ssh.Session, 1)

	go func() {
		session, err := c.client.NewSession()
		if err != nil {
			done <- fmt.Errorf("opening session: %w", err)
			return
		}
		sessions <- session
		defer func() { _ = session.Close() }()

		session.Stdout = out.stdoutWriter()
		session.Stderr = out.stderrWriter()
		if opts.stdin != "" {
			session.Stdin = strings.NewReader(opts.stdin)
		}
		if opts.pty {
			modes := ssh.TerminalModes{ssh.ECHO: 0}
			if err := session.RequestPty("vt100", 24, 200, modes); err != nil {
				done <- fmt.Errorf("requesting pty: %w", err)
				return
			}
		}
		done <- session.Run(command)
	}()

	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		// Closing the session only unblocks Run once the peer answers -- and
		// this path exists because it stopped (the drain cuts the BGP paths
		// this session rides). So: grace, then drop the socket.
		closeSession(sessions)
		if !waitFor(done, c.grace()) {
			c.closeTransport()
			_ = waitFor(done, c.grace())
		}
		// Stop accepting output before reading it: the session goroutine may
		// still be unwinding.
		stdout, stderr := out.release()
		return Result{
			ExitStatus: -1,
			Stdout:     stdout,
			Stderr:     stderr,
			Output:     combined(stdout, stderr, opts.pty),
		}, fmt.Errorf("%s: command timed out after %s: %s", c.hostname, timeout, command)
	}

	stdoutText, stderrText := out.release()
	result := Result{
		Stdout: stdoutText,
		Stderr: stderrText,
	}
	result.Output = combined(result.Stdout, result.Stderr, opts.pty)

	var exitErr *ssh.ExitError
	switch {
	case runErr == nil:
		result.ExitStatus = 0
	case errors.As(runErr, &exitErr):
		// A non-zero exit is a result, not a transport error.
		result.ExitStatus = exitErr.ExitStatus()
	default:
		result.ExitStatus = -1
		return result, fmt.Errorf("%s: running %q: %w", c.hostname, command, runErr)
	}
	return result, nil
}

// closeSession closes the session if opened yet: the polite first move on a
// timeout, enough while the peer still answers.
func closeSession(sessions <-chan *ssh.Session) {
	select {
	case session := <-sessions:
		_ = session.Close()
	default:
	}
}

// waitFor reports whether done fired within d.
func waitFor(done <-chan error, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// capture collects one session's output. Writes arrive on the session
// goroutine, which can outlive a timed-out exec, so access is guarded and
// post-release writes are dropped, including to the caller's echo.
type capture struct {
	mu       sync.Mutex
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	echo     *lineEcho
	released bool
}

func (c *capture) stdoutWriter() io.Writer { return captureWriter{c: c} }
func (c *capture) stderrWriter() io.Writer { return captureWriter{c: c, stderr: true} }

// release returns the output collected so far and stops accepting more.
func (c *capture) release() (string, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.released = true
	return c.stdout.String(), c.stderr.String()
}

type captureWriter struct {
	c      *capture
	stderr bool
}

func (w captureWriter) Write(p []byte) (int, error) {
	c := w.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.released {
		// Discard but report success; an error would only make the abandoned
		// session log a confusing write failure.
		return len(p), nil
	}
	if w.stderr {
		return c.stderr.Write(p)
	}
	n, err := c.stdout.Write(p)
	if c.echo != nil {
		_, _ = c.echo.Write(p)
	}
	return n, err
}

// combined is what marker checks run against; under a pty the device already
// merged stderr in, so appending it would double every line.
func combined(stdout, stderr string, pty bool) string {
	if pty || stderr == "" {
		return stdout
	}
	if stdout == "" {
		return stderr
	}
	return stdout + "\n" + stderr
}

// lineEcho forwards whole lines to w with a prefix, so a long script's
// progress appears as it happens.
type lineEcho struct {
	w      io.Writer
	prefix string
	buf    bytes.Buffer
}

func (e *lineEcho) Write(p []byte) (int, error) {
	e.buf.Write(p)
	for {
		line, err := e.buf.ReadString('\n')
		if err != nil {
			// Incomplete line: put it back for the next write.
			e.buf.Reset()
			e.buf.WriteString(line)
			break
		}
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			fmt.Fprintf(e.w, "%s%s\n", e.prefix, line)
		}
	}
	return len(p), nil
}

// ScriptRequest is one oneshot script execution.
type ScriptRequest struct {
	// Name is the basename the script gets under /tmp.
	Name string
	// Content is the whole script: a stage never dribbles commands at a device
	// over separate channels.
	Content string
	// Marker is the stage marker ("PRECHECK", ...); empty disables the check.
	Marker string
	// Timeout bounds the run; 0 means DefaultScriptTimeout.
	Timeout time.Duration
	// Echo receives the script's output line by line as it arrives.
	Echo       io.Writer
	EchoPrefix string
}

// ScriptOK reports whether a oneshot script really passed. LOAD-BEARING:
// clean exit AND OK marker AND no FAIL marker, because script-template
// hijacks `exit`, so a failed script can finish rc 0 with its OK line.
func ScriptOK(rc int, output, marker string) bool {
	return rc == 0 &&
		strings.Contains(output, marker+"-OK") &&
		!strings.Contains(output, marker+"-FAIL")
}

// uploadScript writes a script to /tmp and makes it executable, returning its
// remote path. Python uses SFTP; this streams over the exec channel's stdin,
// needing no second subsystem.
func (c *Client) uploadScript(ctx context.Context, name, content string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/ \t\n") {
		return "", fmt.Errorf("%s: bad script name %q", c.hostname, name)
	}
	remote := "/tmp/" + name

	command := fmt.Sprintf("cat > %s && chmod 0755 %s", pytext.ShellQuote(remote), pytext.ShellQuote(remote))
	result, err := c.exec(ctx, command, DefaultRunTimeout, execOptions{stdin: content})
	if err != nil {
		return "", err
	}
	if result.ExitStatus != 0 {
		return "", fmt.Errorf("%s: uploading %s failed (rc %d): %s",
			c.hostname, remote, result.ExitStatus, strings.TrimSpace(result.Output))
	}
	return remote, nil
}

// RunScript uploads a script and executes it in a single exec channel,
// returning the result and whether ScriptOK accepts it (folded in so no
// caller forgets the marker rule).
func (c *Client) RunScript(ctx context.Context, req ScriptRequest) (Result, bool, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultScriptTimeout
	}

	remote, err := c.uploadScript(ctx, req.Name, req.Content)
	if err != nil {
		return Result{ExitStatus: -1}, false, err
	}

	result, err := c.exec(ctx, "vbash "+pytext.ShellQuote(remote), timeout, execOptions{
		pty:        true,
		echo:       req.Echo,
		echoPrefix: req.EchoPrefix,
	})
	if err != nil {
		return result, false, err
	}
	if req.Marker == "" {
		return result, result.ExitStatus == 0, nil
	}
	return result, ScriptOK(result.ExitStatus, result.Output, req.Marker), nil
}

// RunDetached uploads a script and launches it detached, returning the remote
// log path. For scripts that sever our own connectivity: the exec returns at
// once and the script outlives the session.
func (c *Client) RunDetached(ctx context.Context, name, content string) (string, error) {
	remote, err := c.uploadScript(ctx, name, content)
	if err != nil {
		return "", err
	}
	logPath := remote + ".log"

	command := fmt.Sprintf("nohup vbash %s > %s 2>&1 & echo DETACHED",
		pytext.ShellQuote(remote), pytext.ShellQuote(logPath))
	result, err := c.exec(ctx, command, 30*time.Second, execOptions{})
	if err != nil {
		return "", err
	}
	if result.ExitStatus != 0 || !strings.Contains(result.Output, "DETACHED") {
		return "", fmt.Errorf("%s: failed to launch %s detached (rc %d)",
			c.hostname, name, result.ExitStatus)
	}
	return logPath, nil
}
