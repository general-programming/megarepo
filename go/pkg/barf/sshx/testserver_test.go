package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// An in-process SSH server, so every test here talks real SSH over a
// loopback socket and NOTHING in this package is ever pointed at a device.

// execHandler answers one exec request. It receives the command line and
// whatever the client wrote to stdin.
type execHandler func(command, stdin string) (stdout, stderr string, rc int)

type testServer struct {
	t        *testing.T
	listener net.Listener
	config   *ssh.ServerConfig
	handler  execHandler

	mu sync.Mutex
	// commands records every exec command line the server was asked to
	// run, in order.
	commands []string
	// files records what was written via `cat > path`.
	files map[string]string
	// ptys counts pty-req requests.
	ptys int

	wg sync.WaitGroup
}

// newTestServer starts a server accepting the given password and any
// public key. handler answers exec requests.
func newTestServer(t *testing.T, password string, handler execHandler) *testServer {
	t.Helper()
	return newTestServerWith(t, password, handler, nil)
}

// newTestServerWith is newTestServer with a chance to adjust the server
// config BEFORE it starts accepting. Mutating s.config afterwards races
// the accept loop.
func newTestServerWith(t *testing.T, password string, handler execHandler, configure func(*ssh.ServerConfig)) *testServer {
	t.Helper()

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}

	s := &testServer{t: t, handler: handler, files: map[string]string{}}
	s.config = &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, given []byte) (*ssh.Permissions, error) {
			if password != "" && string(given) == password {
				return &ssh.Permissions{}, nil
			}
			return nil, io.EOF
		},
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, io.EOF // keys always rejected: exercises the fallback
		},
	}
	s.config.AddHostKey(signer)
	if configure != nil {
		configure(s.config)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s.listener = listener

	s.wg.Add(1)
	go s.accept()
	t.Cleanup(func() {
		_ = listener.Close()
		s.wg.Wait()
	})
	return s
}

func (s *testServer) addr() (string, int) {
	host, port, _ := net.SplitHostPort(s.listener.Addr().String())
	p := 0
	for _, r := range port {
		p = p*10 + int(r-'0')
	}
	return host, p
}

func (s *testServer) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *testServer) file(path string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[path]
}

func (s *testServer) ptyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ptys
}

func (s *testServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(conn)
		}()
	}
}

func (s *testServer) serve(conn net.Conn) {
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.config)
	if err != nil {
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.session(channel, requests)
		}()
	}
}

func (s *testServer) session(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for req := range requests {
		switch req.Type {
		case "pty-req":
			s.mu.Lock()
			s.ptys++
			s.mu.Unlock()
			_ = req.Reply(true, nil)
		case "exec":
			command := decodeString(req.Payload)
			_ = req.Reply(true, nil)
			s.runExec(channel, command)
			return
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *testServer) runExec(channel ssh.Channel, command string) {
	stdin, _ := io.ReadAll(channel)

	s.mu.Lock()
	s.commands = append(s.commands, command)
	// Emulate the upload command well enough to record the script.
	if strings.HasPrefix(command, "cat > ") {
		path := strings.TrimSpace(strings.TrimPrefix(command, "cat > "))
		path = strings.SplitN(path, " ", 2)[0]
		s.files[strings.Trim(path, "'")] = string(stdin)
	}
	s.mu.Unlock()

	stdout, stderr, rc := "", "", 0
	if s.handler != nil {
		stdout, stderr, rc = s.handler(command, string(stdin))
	}
	if stdout != "" {
		_, _ = io.WriteString(channel, stdout)
	}
	if stderr != "" {
		_, _ = io.WriteString(channel.Stderr(), stderr)
	}
	_, _ = channel.SendRequest("exit-status", false, encodeUint32(uint32(rc))) // #nosec G115 -- test rc
	_ = channel.CloseWrite()
}

func decodeString(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}
	n := binary.BigEndian.Uint32(payload[:4])
	if int(n)+4 > len(payload) {
		return ""
	}
	return string(payload[4 : 4+n])
}

func encodeUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

// dialTest connects to the test server as the supertech user, with the
// agent and identity files disabled so the run is hermetic.
func dialTest(t *testing.T, s *testServer, password string) *Client {
	t.Helper()
	host, port := s.addr()
	no := false
	client, err := Dial(t.Context(), "test-device", host, Options{
		Port:          port,
		Credentials:   StaticCredentials{Username: "supertech", Password: password},
		Agent:         &no,
		IdentityFiles: []string{},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}
