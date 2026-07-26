package sshx

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestScriptOK(t *testing.T) {
	// VyOS's script-template hijacks `exit`, so rc alone is a lie; each
	// case is a real shape a device can produce.
	cases := []struct {
		name   string
		rc     int
		output string
		want   bool
	}{
		{"clean pass", 0, "doing things\nPRECHECK-OK\n", true},
		{"nonzero exit", 1, "PRECHECK-OK\n", false},
		{"no marker at all", 0, "doing things\n", false},
		{
			// Load-bearing: `exit 1` after sourcing script-template is
			// config-mode exit, not the builtin, so the script fell
			// through and printed its OK line anyway with rc 0.
			name:   "FAIL then OK with rc 0",
			rc:     0,
			output: "PRECHECK-FAIL: unsaved config\nsome noise\nPRECHECK-OK\n",
			want:   false,
		},
		{"OK then FAIL", 0, "PRECHECK-OK\nPRECHECK-FAIL: late failure\n", false},
		{"other stage's marker", 0, "INSTALL-OK\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScriptOK(tc.rc, tc.output, "PRECHECK"); got != tc.want {
				t.Fatalf("ScriptOK(%d, %q) = %v, want %v", tc.rc, tc.output, got, tc.want)
			}
		})
	}
}

func TestDialUsesPasswordAfterKeysFail(t *testing.T) {
	server := newTestServer(t, "hunter2", nil)

	// A real (rejected) key makes the key-only attempt happen, so the
	// fallback path is genuinely exercised.
	home := t.TempDir()
	writeTestKey(t, filepath.Join(home, "id_ed25519"))

	host, port := server.addr()
	no := false
	client, err := Dial(t.Context(), "test-device", host, Options{
		Port:          port,
		Credentials:   StaticCredentials{Username: "supertech", Password: "hunter2"},
		Agent:         &no,
		IdentityFiles: []string{filepath.Join(home, "id_ed25519")},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	if client.Username() != "supertech" {
		t.Fatalf("username = %q, want supertech", client.Username())
	}
}

func TestDialExplicitUsernameNeverUsesThePassword(t *testing.T) {
	// BaseHost.SSH_USERNAME means "keys only": with no usable key the dial
	// must fail rather than quietly trying the shared account's password.
	server := newTestServer(t, "hunter2", nil)
	host, port := server.addr()
	no := false

	_, err := Dial(t.Context(), "test-device", host, Options{
		Port:          port,
		Username:      "root",
		Credentials:   StaticCredentials{Username: "supertech", Password: "hunter2"},
		Agent:         &no,
		IdentityFiles: []string{},
	})
	if err == nil {
		t.Fatal("expected the dial to fail with no auth methods")
	}
	if !strings.Contains(err.Error(), "no usable authentication methods") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDialReportsEveryFailure(t *testing.T) {
	server := newTestServer(t, "hunter2", nil)
	host, port := server.addr()
	no := false

	_, err := Dial(t.Context(), "test-device", host, Options{
		Port:          port,
		Credentials:   StaticCredentials{Username: "supertech", Password: "wrong"},
		Agent:         &no,
		IdentityFiles: []string{},
	})
	if err == nil {
		t.Fatal("expected an auth failure")
	}
	if !strings.Contains(err.Error(), "all SSH auth methods failed for supertech@") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Passwords must never appear in an error message.
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("the error leaked the password: %v", err)
	}
}

func TestRunCapturesStatusStdoutAndStderr(t *testing.T) {
	server := newTestServer(t, "pw", func(command, _ string) (string, string, int) {
		if command == "false" {
			return "out\n", "boom\n", 3
		}
		return "hello\n", "", 0
	})
	client := dialTest(t, server, "pw")

	result, err := client.Run(t.Context(), "true", 0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.ExitStatus != 0 || result.Stdout != "hello\n" {
		t.Fatalf("unexpected result: %+v", result)
	}

	result, err = client.Run(t.Context(), "false", 0)
	if err != nil {
		t.Fatalf("a non-zero exit is a result, not an error: %v", err)
	}
	if result.ExitStatus != 3 {
		t.Fatalf("exit status = %d, want 3", result.ExitStatus)
	}
	if result.Stderr != "boom\n" {
		t.Fatalf("stderr = %q", result.Stderr)
	}
	if !strings.Contains(result.Output, "boom") || !strings.Contains(result.Output, "out") {
		t.Fatalf("combined output missing a stream: %q", result.Output)
	}
}

func TestRunScriptUploadsAndChecksMarkers(t *testing.T) {
	server := newTestServer(t, "pw", func(command, _ string) (string, string, int) {
		if strings.HasPrefix(command, "vbash ") {
			return "starting\nPRECHECK-OK\n", "", 0
		}
		return "", "", 0
	})
	client := dialTest(t, server, "pw")

	var echoed bytes.Buffer
	result, ok, err := client.RunScript(t.Context(), ScriptRequest{
		Name:       "barf-precheck.sh",
		Content:    "#!/bin/vbash\ntrue\n",
		Marker:     MarkerPrecheck,
		Echo:       &echoed,
		EchoPrefix: "[dev]   ",
	})
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if !ok {
		t.Fatalf("expected the script to pass: %+v", result)
	}
	if got := server.file("/tmp/barf-precheck.sh"); got != "#!/bin/vbash\ntrue\n" {
		t.Fatalf("script not uploaded verbatim: %q", got)
	}
	if !strings.Contains(echoed.String(), "[dev]   PRECHECK-OK") {
		t.Fatalf("echo missing prefixed line: %q", echoed.String())
	}
	if server.ptyCount() == 0 {
		t.Fatal("run_script must request a pty (it merges stderr and keeps sudo happy)")
	}
	commands := server.recorded()
	if len(commands) != 2 || !strings.HasPrefix(commands[0], "cat > ") ||
		commands[1] != "vbash /tmp/barf-precheck.sh" {
		t.Fatalf("unexpected commands: %v", commands)
	}
}

func TestRunScriptRejectsFailMarkerDespiteCleanExit(t *testing.T) {
	// The production hazard: rc 0 with both OK and FAIL lines. A router
	// reporting this has NOT passed.
	server := newTestServer(t, "pw", func(command, _ string) (string, string, int) {
		if strings.HasPrefix(command, "vbash ") {
			return "PRECHECK-FAIL: committed but unsaved configuration changes:\n" +
				"+set foo\n" +
				"PRECHECK-OK\n", "", 0
		}
		return "", "", 0
	})
	client := dialTest(t, server, "pw")

	result, ok, err := client.RunScript(t.Context(), ScriptRequest{
		Name:    "barf-precheck.sh",
		Content: "#!/bin/vbash\n",
		Marker:  MarkerPrecheck,
	})
	if err != nil {
		t.Fatalf("run script: %v", err)
	}
	if result.ExitStatus != 0 {
		t.Fatalf("precondition: rc should be 0, got %d", result.ExitStatus)
	}
	if ok {
		t.Fatal("a script printing a FAIL marker must never be accepted, rc 0 or not")
	}
}

func TestRunDetachedLaunchesAndReturnsLogPath(t *testing.T) {
	server := newTestServer(t, "pw", func(command, _ string) (string, string, int) {
		if strings.Contains(command, "nohup") {
			return "DETACHED\n", "", 0
		}
		return "", "", 0
	})
	client := dialTest(t, server, "pw")

	logPath, err := client.RunDetached(t.Context(), "barf-reboot.sh", "#!/bin/vbash\n")
	if err != nil {
		t.Fatalf("run detached: %v", err)
	}
	if logPath != "/tmp/barf-reboot.sh.log" {
		t.Fatalf("log path = %q", logPath)
	}
	commands := server.recorded()
	last := commands[len(commands)-1]
	if !strings.Contains(last, "nohup vbash /tmp/barf-reboot.sh > /tmp/barf-reboot.sh.log 2>&1 &") {
		t.Fatalf("unexpected launch command: %q", last)
	}
}

func TestRunDetachedFailsWithoutTheMarker(t *testing.T) {
	server := newTestServer(t, "pw", func(string, string) (string, string, int) {
		return "", "", 0 // exec succeeded but printed nothing
	})
	client := dialTest(t, server, "pw")

	if _, err := client.RunDetached(t.Context(), "barf-reboot.sh", "#!/bin/vbash\n"); err == nil {
		t.Fatal("expected a failure when DETACHED is missing")
	}
}

func TestRunTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := newTestServer(t, "pw", func(string, string) (string, string, int) {
		<-release
		return "", "", 0
	})
	// Registered after the server so it runs first (t.Cleanup is LIFO);
	// otherwise the server waits on a handler waiting for this channel.
	t.Cleanup(func() { close(release) })
	client := dialTest(t, server, "pw")

	_, err := client.Run(t.Context(), "sleep 600", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout, got %v", err)
	}
}

func TestRejectsBadScriptNames(t *testing.T) {
	server := newTestServer(t, "pw", nil)
	client := dialTest(t, server, "pw")

	for _, name := range []string{"", "../etc/passwd", "a b"} {
		if _, _, err := client.RunScript(t.Context(), ScriptRequest{Name: name, Content: "x"}); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

// Several sessions on one connection; `go test -race` is the point.
func TestConcurrentRuns(t *testing.T) {
	server := newTestServer(t, "pw", func(command, _ string) (string, string, int) {
		return command + "\n", "", 0
	})
	client := dialTest(t, server, "pw")

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := client.Run(t.Context(), "echo", 0); err != nil {
				t.Errorf("concurrent run %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	// Close is idempotent and safe under concurrency too.
	var closers sync.WaitGroup
	for i := 0; i < 4; i++ {
		closers.Add(1)
		go func() { defer closers.Done(); _ = client.Close() }()
	}
	closers.Wait()
}

func writeTestKey(t *testing.T, path string) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(private, "")
	if err != nil {
		t.Fatalf("marshalling key: %v", err)
	}
	if err := os.WriteFile(path, pemEncode(block), 0o600); err != nil {
		t.Fatalf("writing key: %v", err)
	}
}

func pemEncode(block *pem.Block) []byte { return pem.EncodeToMemory(block) }
