package device

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// eosWriteServer records every request body and answers with a plausible
// eAPI success. NOTHING in this package's tests ever talks to a real
// device; the production router these commands target is a core router
// and a live deploy is a supervised human action.
type eosWriteServer struct {
	*httptest.Server
	bodies []eosRequest
	status int
	fail   *eosError
}

func newEOSWriteServer(t *testing.T) *eosWriteServer {
	t.Helper()
	s := &eosWriteServer{status: http.StatusOK}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req eosRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Errorf("malformed request: %v", err)
		}
		s.bodies = append(s.bodies, req)

		if s.status != http.StatusOK {
			w.WriteHeader(s.status)
			return
		}
		if s.fail != nil {
			_ = json.NewEncoder(w).Encode(eosResponse{Error: s.fail})
			return
		}
		results := make([]json.RawMessage, len(req.Params.Cmds))
		for i := range results {
			results[i] = json.RawMessage(`{}`)
		}
		_ = json.NewEncoder(w).Encode(eosResponse{Result: results})
	}))
	t.Cleanup(s.Close)
	return s
}

func writerOptions(t *testing.T, s *eosWriteServer) Options {
	t.Helper()
	opts := testOptions(t, s.Server)
	opts.AllowWrites = true
	return opts
}

// The opt-in is the whole guarantee: without it there is no writer to
// call, so no code path can push config by accident.
func TestNewEOSWriterRequiresAllowWrites(t *testing.T) {
	s := newEOSWriteServer(t)
	opts := writerOptions(t, s)
	opts.AllowWrites = false

	w, err := NewEOSWriter(testHost("fmt2-cor-r-140752-1", "eos"), opts)
	if err == nil {
		t.Fatal("a writer was built without AllowWrites")
	}
	if w != nil {
		t.Fatal("a writer value came back alongside the error")
	}
	var notAllowed *ErrWritesNotAllowed
	if !errors.As(err, &notAllowed) {
		t.Fatalf("err = %v, want ErrWritesNotAllowed", err)
	}
	if len(s.bodies) != 0 {
		t.Fatal("a request was sent while refusing to build a writer")
	}
}

func TestNewEOSWriterNeedsSecrets(t *testing.T) {
	_, err := NewEOSWriter(testHost("h", "eos"), Options{AllowWrites: true, Endpoint: "127.0.0.1"})
	if err == nil || !strings.Contains(err.Error(), "Options.Secrets") {
		t.Fatalf("err = %v", err)
	}
}

// Setting AllowWrites must not turn a Reader into something that writes.
func TestAllowWritesDoesNotLoosenTheReader(t *testing.T) {
	s := newEOSWriteServer(t)
	r, err := NewEOS(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.runShow(context.Background(), "json", "configure")
	var attempt *ErrWriteAttempt
	if !errors.As(err, &attempt) {
		t.Fatalf("err = %v, want ErrWriteAttempt even with AllowWrites set", err)
	}
	if len(s.bodies) != 0 {
		t.Fatal("the reader sent a request for a write verb")
	}
}

func managedOps() []Op {
	return EOSOps([]string{
		"username admin privilege 15 role network-admin secret sha512 $6$salt$hash",
		"username admin ssh-key ssh-ed25519 AAAAkey erin@devbox",
		"enable password sha512 $6$salt$otherhash",
		"management api http-commands",
		"protocol https port 443",
		"no shutdown",
		"vrf internal",
		"no shutdown",
	})
}

func TestConfigureSendsManagedSliceInConfigMode(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("fmt2-cor-r-140752-1", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(context.Background(), managedOps()); err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) != 1 {
		t.Fatalf("%d requests, want 1", len(s.bodies))
	}
	req := s.bodies[0]
	if req.Method != "runCmds" {
		t.Errorf("method = %q, want runCmds", req.Method)
	}

	sent := commandStrings(t, req)
	// enable, configure, the eight managed lines, end.
	if sent[1] != "configure" {
		t.Errorf("second command = %q, want configure", sent[1])
	}
	if sent[len(sent)-1] != "end" {
		t.Errorf("last command = %q, want end", sent[len(sent)-1])
	}
	body := strings.Join(sent, "\n")
	for _, want := range []string{"username admin privilege 15", "enable password sha512", "management api http-commands", "vrf internal"} {
		if !strings.Contains(body, want) {
			t.Errorf("managed line %q not sent:\n%s", want, body)
		}
	}
	// The only negation in the payload is the eAPI block's own.
	for _, cmd := range sent {
		if strings.HasPrefix(cmd, "no ") && cmd != "no shutdown" {
			t.Errorf("deploy carried an unmanaged negation: %q", cmd)
		}
	}
}

// Re-sending the same slice is what makes deploy safe to run repeatedly:
// EOS treats an identical line as a no-op, and barf sends nothing else.
func TestConfigureIsIdempotentlyShaped(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.Configure(ctx, managedOps()); err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(ctx, managedOps()); err != nil {
		t.Fatal(err)
	}
	first := strings.Join(commandStrings(t, s.bodies[0]), "\n")
	second := strings.Join(commandStrings(t, s.bodies[1]), "\n")
	if first != second {
		t.Errorf("a repeat deploy sent different commands:\n%s\n---\n%s", first, second)
	}
}

func TestConfigureRefusesCommandsOutsideTheManagedScope(t *testing.T) {
	forbidden := []string{
		"no ip routing",
		"no username erin",
		"username erin privilege 15 secret sha512 $6$x$y",
		"write erase",
		"reload",
		"interface Ethernet1",
		"no interface Vlan99",
		"default snmp-server enable traps",
		"copy running-config startup-config",
		"vrf instance internal extra",
		"",
	}
	for _, cmd := range forbidden {
		s := newEOSWriteServer(t)
		w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
		if err != nil {
			t.Fatal(err)
		}
		err = w.Configure(context.Background(), EOSOps([]string{cmd}))
		var unmanaged *ErrUnmanagedCommand
		if !errors.As(err, &unmanaged) {
			t.Errorf("Configure(%q) err = %v, want ErrUnmanagedCommand", cmd, err)
		}
		if len(s.bodies) != 0 {
			t.Errorf("Configure(%q) sent a request before rejecting", cmd)
		}
	}
}

// One bad op poisons the whole batch: nothing is sent, so a deploy is
// never half-applied because of a scope violation.
func TestConfigureRejectsTheWholeBatch(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	ops := append(managedOps(), EOSOps([]string{"no ip routing"})...)
	if err := w.Configure(context.Background(), ops); err == nil {
		t.Fatal("batch with an unmanaged command was accepted")
	}
	if len(s.bodies) != 0 {
		t.Fatal("a partial batch was sent")
	}
}

// A delete has no meaning in the EOS slice, and admitting one would be
// the single way a deploy could remove config from the device.
func TestConfigureRefusesDeleteOps(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	op := Op{Verb: OpDelete, Path: []string{"username", ManagedUsername}}
	if err := w.Configure(context.Background(), []Op{op}); err == nil {
		t.Fatal("a delete op was accepted")
	}
	if len(s.bodies) != 0 {
		t.Fatal("a delete op reached the wire")
	}
}

func TestConfigureNoOps(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(s.bodies) != 0 {
		t.Fatal("an empty deploy still hit the device")
	}
}

func TestSaveConfig(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.SaveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	sent := commandStrings(t, s.bodies[0])
	if len(sent) != 2 || sent[1] != "copy running-config startup-config" {
		t.Errorf("commands = %v", sent)
	}
	// Not a config-mode command: no `configure` wrapper.
	for _, cmd := range sent {
		if cmd == "configure" {
			t.Error("SaveConfig entered config mode")
		}
	}
}

func TestWriterSurfacesEAPIErrors(t *testing.T) {
	s := newEOSWriteServer(t)
	s.fail = &eosError{Code: 1002, Message: "CLI command 3 of 4 'no shutdown' failed"}
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	err = w.Configure(context.Background(), managedOps())
	if err == nil || !strings.Contains(err.Error(), "1002") {
		t.Fatalf("err = %v, want the eAPI error", err)
	}
}

func TestWriterSurfacesAuthFailure(t *testing.T) {
	s := newEOSWriteServer(t)
	s.status = http.StatusUnauthorized
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	err = w.Configure(context.Background(), managedOps())
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("err = %v", err)
	}
}

// The EOS writer shares the VyOS writer's problem and its fix: a commit
// and a save each get their own budget instead of the read client's 10s.
func TestEOSWriterGivesEachOperationItsOwnTimeout(t *testing.T) {
	s := newEOSWriteServer(t)
	opts, recorder := recordingOptions(writerOptions(t, s))
	w, err := NewEOSWriter(testHost("h", "eos"), opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Configure(context.Background(), EOSOps([]string{"no shutdown"})); err != nil {
		t.Fatal(err)
	}
	if got := recorder.budget(eosCommandPath); got != TimeoutConfigure {
		t.Errorf("Configure budget = %v, want %v", got, TimeoutConfigure)
	}
	if err := w.SaveConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := recorder.budget(eosCommandPath); got != TimeoutSave {
		t.Errorf("SaveConfig budget = %v, want %v", got, TimeoutSave)
	}
}

func TestEOSWriteCommandAllowed(t *testing.T) {
	allowed := []string{
		"username admin privilege 15 role network-admin secret sha512 $6$a$b",
		"username admin ssh-key ssh-ed25519 AAAA x@y",
		"username admin ssh-key secondary ssh-ed25519 AAAA x@y",
		"enable password sha512 $6$a$b",
		"management api http-commands",
		"protocol https port 443",
		"vrf internal",
		"no shutdown",
	}
	for _, cmd := range allowed {
		if !eosWriteCommandAllowed(cmd) {
			t.Errorf("managed command rejected: %q", cmd)
		}
	}
	// `username admin` prefixed accounts must not smuggle others in.
	if eosWriteCommandAllowed("username administrator privilege 15 secret x") {
		t.Error("a different account matched the admin prefix")
	}
}

// Regression: bare `shutdown` used to be allowlisted alongside
// `no shutdown`. Inside `management api http-commands` it disables eAPI —
// the transport barf itself speaks — so the write succeeds and then locks
// barf out of the device. Nothing in the managed slice needs it.
func TestEOSWriteCommandRefusesBareShutdown(t *testing.T) {
	if eosWriteCommandAllowed("shutdown") {
		t.Error("bare `shutdown` is allowed; it disables eAPI and locks barf out")
	}
	if !eosWriteCommandAllowed("no shutdown") {
		t.Error("`no shutdown` must stay allowed: it is what enables eAPI")
	}
}

// Regression: TrimSpace strips the ends of a command, not its interior,
// and every allowlist arm is a prefix test — so a newline inside one
// "command" smuggled a second, unreviewed line past the guard. eAPI runs
// a multi-line string as multiple config lines. Reachable in practice via
// a two-line authorized_keys blob pasted into one YAML value.
func TestEOSWriteCommandRefusesEmbeddedSeparators(t *testing.T) {
	smuggled := []string{
		"username admin ssh-key ssh-ed25519 AAAA x\nno ip routing",
		"enable password sha512 $6$x$y\nwrite erase",
		"management api http-commands\nshutdown",
		"username admin ssh-key ssh-ed25519 AAAA x\r\nno ip routing",
		"vrf internal\rshutdown",
		"no shutdown;write erase",
		"protocol https port 443; reload",
	}
	for _, cmd := range smuggled {
		if eosWriteCommandAllowed(cmd) {
			t.Errorf("command with an embedded separator was allowed: %q", cmd)
		}
	}
}

// The guard must also stop the smuggled command at Configure, not merely
// at the predicate — nothing may reach the wire.
func TestEOSWriterConfigureRejectsEmbeddedNewline(t *testing.T) {
	s := newEOSWriteServer(t)
	w, err := NewEOSWriter(testHost("h", "eos"), writerOptions(t, s))
	if err != nil {
		t.Fatal(err)
	}
	err = w.Configure(context.Background(), EOSOps([]string{
		"username admin ssh-key ssh-ed25519 AAAA x\nno ip routing",
	}))
	var unmanaged *ErrUnmanagedCommand
	if !errors.As(err, &unmanaged) {
		t.Fatalf("err = %v, want ErrUnmanagedCommand", err)
	}
	if len(s.bodies) != 0 {
		t.Error("a request reached the device despite the refusal")
	}
}

// commandStrings flattens the request's cmds, replacing the enable entry
// (which carries the secret) with the literal "enable" so no test output
// can ever contain a credential.
func commandStrings(t *testing.T, req eosRequest) []string {
	t.Helper()
	out := make([]string, 0, len(req.Params.Cmds))
	for _, cmd := range req.Params.Cmds {
		switch value := cmd.(type) {
		case string:
			out = append(out, value)
		case map[string]any:
			out = append(out, "enable")
		default:
			t.Fatalf("unexpected command payload %T", cmd)
		}
	}
	return out
}
