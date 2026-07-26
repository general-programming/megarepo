// Package sshx is the SSH substrate the device-lifecycle commands run on.
//
// It is the Go port of projects/barf/barf/util/ssh.py (the DeviceSSH
// paramiko wrapper) and projects/barf/barf/util/vyos_scripts.py (the
// oneshot vbash stage scripts).
//
// Two things about this package are load-bearing and must not be
// "simplified" away:
//
//  1. ScriptOK. A VyOS oneshot script only passed if rc == 0 AND the
//     "<MARKER>-OK" line is present AND "<MARKER>-FAIL" is absent.
//     Sourcing VyOS's script-template replaces the `exit` builtin with
//     the config-mode `exit` command, so a script that hit a failure and
//     called `exit 1` keeps running and can still finish with rc 0 and
//     its OK line printed further down. Checking rc alone silently turns
//     failures into successes on a router.
//
//  2. Auth attempts are split. sshd counts every offered agent key
//     against MaxAuthTries, so one combined key+password attempt can get
//     the transport dropped before password auth is ever tried. Keys go
//     in one connection attempt, the password in a second, exactly as the
//     Python implementation does it.
//
// This package opens sessions and runs commands; it does not itself
// decide that a device should be changed. It is the transport, and the
// callers in barf/lifecycle hold the safety gates.
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

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// DefaultPort is the SSH port the fleet listens on.
const DefaultPort = 22

// DefaultConnectTimeout mirrors DeviceSSH's connect_timeout default.
const DefaultConnectTimeout = 10 * time.Second

// Credentials is a username/password pair. Values are used in memory
// only: nothing in this package logs, formats or persists a password.
type Credentials struct {
	Username string
	Password string
}

// CredentialSource supplies the shared "supertech" account, whose
// password lives in Vault at `supertech-credentials`. Declared here as a
// local interface so this package does not depend on go/client/vault
// (which another worker owns).
//
// Ports barf.actions.get_supertech_creditentials.
type CredentialSource interface {
	Supertech(ctx context.Context) (Credentials, error)
}

// StaticCredentials is a CredentialSource returning a fixed pair; tests
// and callers that already hold the credentials use it.
type StaticCredentials Credentials

// Supertech implements CredentialSource.
func (s StaticCredentials) Supertech(context.Context) (Credentials, error) {
	return Credentials(s), nil
}

// Options configures a Dial.
type Options struct {
	// Username logs in as this user with keys only, instead of the shared
	// supertech user (whose Vault password is the fallback). Mirrors
	// BaseHost.SSH_USERNAME: empty means "use supertech".
	Username string

	// Credentials resolves the supertech account. Required unless
	// Username is set.
	Credentials CredentialSource

	// Port is the SSH port; 0 means DefaultPort.
	Port int

	// ConnectTimeout bounds each auth attempt; 0 means
	// DefaultConnectTimeout.
	ConnectTimeout time.Duration

	// HostKeyCallback is the host-key policy. nil reproduces what the
	// Python implementation does with paramiko's AutoAddPolicy on a
	// throwaway (in-memory) host key store: accept whatever the device
	// presents. The fleet's host keys are reinstalled by every image
	// install, so pinning them here would break every update; the
	// management path is a private fabric. Set this to a real callback
	// (e.g. knownhosts.New) to opt into verification.
	HostKeyCallback ssh.HostKeyCallback

	// Agent enables SSH_AUTH_SOCK agent auth. Nil means enabled (Python
	// passes allow_agent=True).
	Agent *bool

	// IdentityFiles are private keys to offer. Nil means the usual
	// ~/.ssh/id_* set (Python's look_for_keys=True).
	IdentityFiles []string

	// Dial overrides the network dial (tests). nil means net.Dialer.
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
	// See Options.HostKeyCallback: this mirrors paramiko's AutoAddPolicy
	// against a non-persistent host key store.
	return ssh.InsecureIgnoreHostKey() // #nosec G106 -- documented policy parity with the Python implementation
}

// Client is one authenticated SSH connection to a device.
type Client struct {
	hostname string
	address  string
	username string

	client *ssh.Client

	closeOnce sync.Once
	closeErr  error
}

// Hostname is the device's name (for messages).
func (c *Client) Hostname() string { return c.hostname }

// Address is the address actually connected to.
func (c *Client) Address() string { return c.address }

// Username is the account the connection authenticated as.
func (c *Client) Username() string { return c.username }

// Close closes the connection. Safe to call more than once.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.client.Close() })
	return c.closeErr
}

// Dial connects to address as the right user for host.
//
// Ports DeviceSSH.__init__: agent/local keys first (the fleet is
// publickey-only), then — only when logging in as the shared supertech
// account — a second, separate attempt with its Vault password. Every
// attempt's error is collected so a total failure says what each method
// reported.
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

	// Keys and password go in separate connection attempts: sshd counts
	// every offered agent key against MaxAuthTries, so one combined
	// attempt can get the transport dropped ("saw EOF") before password
	// auth is ever tried -- and key-only fleets never need the password.
	var attempts []([]ssh.AuthMethod)
	if methods := keyAuthMethods(opts); len(methods) > 0 {
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
		client, err := dialOnce(ctx, target, username, methods, opts)
		if err == nil {
			return &Client{hostname: hostname, address: address, username: username, client: client}, nil
		}
		failures = append(failures, err.Error())
	}

	return nil, fmt.Errorf("sshx: all SSH auth methods failed for %s@%s: %s",
		username, address, strings.Join(failures, "; "))
}

func dialOnce(ctx context.Context, target, username string, methods []ssh.AuthMethod, opts Options) (*ssh.Client, error) {
	dial := opts.Dial
	if dial == nil {
		dial = func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: opts.connectTimeout()}
			return d.DialContext(ctx, network, address)
		}
	}

	conn, err := dial(ctx, "tcp", target)
	if err != nil {
		return nil, err
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
		return nil, err
	}
	// Clear the handshake deadline; long-running scripts must not trip it.
	_ = conn.SetDeadline(time.Time{})
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// keyAuthMethods is Python's allow_agent=True, look_for_keys=True: the
// agent when one is reachable, plus any readable default identity files.
func keyAuthMethods(opts Options) []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	if opts.agentEnabled() {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			if conn, err := net.Dial("unix", sock); err == nil {
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
			// Encrypted keys are the agent's job; skip them silently
			// rather than prompting mid-update.
			continue
		}
		signers = append(signers, signer)
	}
	if len(signers) > 0 {
		methods = append(methods, ssh.PublicKeys(signers...))
	}
	return methods
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
	// ExitStatus is the remote exit status. A command killed by a signal,
	// or a session that ended without a status, reports -1.
	ExitStatus int
	Stdout     string
	Stderr     string
	// Output is what the caller should check markers against: stdout,
	// plus stderr when the session ran under a pty (which merges them).
	Output string
}

// OK reports a clean exit. It is deliberately NOT enough on its own for
// a VyOS oneshot script — see ScriptOK.
func (r Result) OK() bool { return r.ExitStatus == 0 }

// DefaultRunTimeout matches DeviceSSH.run's default.
const DefaultRunTimeout = 60 * time.Second

// DefaultScriptTimeout matches DeviceSSH.run_script's default; image
// installs are slow.
const DefaultScriptTimeout = 15 * time.Minute

// Run executes command and captures its exit status, stdout and stderr.
// Ports DeviceSSH.run (which returns stdout only; stderr is captured
// here as well, because a lifecycle failure with an empty stdout is
// otherwise undebuggable).
func (c *Client) Run(ctx context.Context, command string, timeout time.Duration) (Result, error) {
	if timeout <= 0 {
		timeout = DefaultRunTimeout
	}
	return c.exec(ctx, command, timeout, execOptions{})
}

type execOptions struct {
	// pty requests a pseudo-terminal, which merges stderr into stdout and
	// keeps sudo happy (Python's get_pty=True).
	pty bool
	// stdin is fed to the command, then closed.
	stdin string
	// echo receives each output line as it arrives, prefixed.
	echo       io.Writer
	echoPrefix string
}

func (c *Client) exec(ctx context.Context, command string, timeout time.Duration, opts execOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	session, err := c.client.NewSession()
	if err != nil {
		return Result{ExitStatus: -1}, fmt.Errorf("%s: opening session: %w", c.hostname, err)
	}

	var stdout, stderr bytes.Buffer
	var stdoutSink io.Writer = &stdout
	if opts.echo != nil {
		stdoutSink = io.MultiWriter(&stdout, &lineEcho{w: opts.echo, prefix: opts.echoPrefix})
	}
	session.Stdout = stdoutSink
	session.Stderr = &stderr
	if opts.stdin != "" {
		session.Stdin = strings.NewReader(opts.stdin)
	}

	if opts.pty {
		modes := ssh.TerminalModes{ssh.ECHO: 0}
		if err := session.RequestPty("vt100", 24, 200, modes); err != nil {
			_ = session.Close()
			return Result{ExitStatus: -1}, fmt.Errorf("%s: requesting pty: %w", c.hostname, err)
		}
	}

	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()

	var runErr error
	select {
	case runErr = <-done:
	case <-ctx.Done():
		// Closing the session unblocks Run; take whatever it reports and
		// surface the timeout instead.
		_ = session.Close()
		<-done
		return Result{
			ExitStatus: -1,
			Stdout:     stdout.String(),
			Stderr:     stderr.String(),
			Output:     combined(stdout.String(), stderr.String(), opts.pty),
		}, fmt.Errorf("%s: command timed out after %s: %s", c.hostname, timeout, command)
	}
	_ = session.Close()

	result := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	result.Output = combined(result.Stdout, result.Stderr, opts.pty)

	var exitErr *ssh.ExitError
	switch {
	case runErr == nil:
		result.ExitStatus = 0
	case errors.As(runErr, &exitErr):
		// A non-zero exit is a result, not a transport error: the caller
		// decides what rc means (and for scripts, rc is not enough).
		result.ExitStatus = exitErr.ExitStatus()
	default:
		result.ExitStatus = -1
		return result, fmt.Errorf("%s: running %q: %w", c.hostname, command, runErr)
	}
	return result, nil
}

// combined is what marker checks run against. Under a pty the device has
// already merged stderr into stdout, so appending it again would double
// every line.
func combined(stdout, stderr string, pty bool) string {
	if pty || stderr == "" {
		return stdout
	}
	if stdout == "" {
		return stderr
	}
	return stdout + "\n" + stderr
}

// lineEcho forwards whole lines to w with a prefix, so progress from a
// long script appears as it happens instead of at the end.
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
	// Content is the whole script; everything a stage needs must be in
	// here, so we never dribble commands at a device over separate
	// channels.
	Content string
	// Marker is the stage marker ("PRECHECK", "INSTALL", ...). Empty
	// disables the marker check.
	Marker string
	// Timeout bounds the run; 0 means DefaultScriptTimeout.
	Timeout time.Duration
	// Echo receives the script's output line by line as it arrives.
	Echo io.Writer
	// EchoPrefix is prepended to each echoed line.
	EchoPrefix string
}

// ScriptOK reports whether a oneshot script really passed.
//
// THIS IS THE LOAD-BEARING CHECK. Requires a clean exit AND the OK
// marker AND no FAIL marker: VyOS's script-template hijacks `exit`, so a
// failed script can still finish with rc 0 and its OK line printed
// further down. Ports vendors/vyos.py `_script_ok` exactly.
func ScriptOK(rc int, output, marker string) bool {
	return rc == 0 &&
		strings.Contains(output, marker+"-OK") &&
		!strings.Contains(output, marker+"-FAIL")
}

// uploadScript writes a script to /tmp and makes it executable,
// returning its remote path.
//
// The Python implementation uses SFTP; this streams the script over the
// exec channel's stdin instead, which needs no extra dependency and no
// second subsystem, and fails identically (non-zero rc) when /tmp is not
// writable.
func (c *Client) uploadScript(ctx context.Context, name, content string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/ \t\n") {
		return "", fmt.Errorf("%s: bad script name %q", c.hostname, name)
	}
	remote := "/tmp/" + name

	command := fmt.Sprintf("cat > %s && chmod 0755 %s", Quote(remote), Quote(remote))
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
// returning the result and whether ScriptOK accepts it.
//
// Ports DeviceSSH.run_script plus the caller-side `_script_ok` check that
// always accompanied it, so no caller can forget the marker rule.
func (c *Client) RunScript(ctx context.Context, req ScriptRequest) (Result, bool, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = DefaultScriptTimeout
	}

	remote, err := c.uploadScript(ctx, req.Name, req.Content)
	if err != nil {
		return Result{ExitStatus: -1}, false, err
	}

	// A pty merges stderr into stdout and keeps sudo happy.
	result, err := c.exec(ctx, "vbash "+Quote(remote), timeout, execOptions{
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

// RunDetached uploads a script and launches it detached from this
// session, returning the remote log path.
//
// For scripts that sever our own connectivity (BGP drain + reboot): the
// exec returns immediately and the script keeps running on the device
// even after the session drops, logging to "<script>.log". Ports
// DeviceSSH.run_detached.
func (c *Client) RunDetached(ctx context.Context, name, content string) (string, error) {
	remote, err := c.uploadScript(ctx, name, content)
	if err != nil {
		return "", err
	}
	logPath := remote + ".log"

	command := fmt.Sprintf("nohup vbash %s > %s 2>&1 & echo DETACHED",
		Quote(remote), Quote(logPath))
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

// Quote is shlex.quote: POSIX single-quoting for a shell word.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '@' || r == '%' || r == '+' || r == '=' || r == ':' ||
			r == ',' || r == '.' || r == '/' || r == '-' || r == '_') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
