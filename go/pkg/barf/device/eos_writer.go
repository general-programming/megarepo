package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// This file is the ONLY write path to an Arista device, and it is
// deliberately separate from eos.go.
//
// The read transport (EOSReader) stays read-only by construction: its one
// request primitive rejects anything that is not `enable` or `show ...`
// (eosCommandAllowed), and nothing here relaxes that. A caller holding a
// Reader still cannot write; writing requires constructing an EOSWriter,
// which requires Options.AllowWrites, which a caller has to set by name.
//
// The write surface is itself scoped. EOS is adopted one config slice at
// a time — the `admin` user, its ssh-keys, the enable password, the eAPI
// block — and the promise of that slice is that deploying it cannot
// disturb anything else on the device. So this writer does not accept
// arbitrary config: eosWriteCommandAllowed admits only the managed
// shapes, and in particular admits `no shutdown` (which the eAPI block
// needs) while rejecting every other negation. A deploy physically
// cannot carry a `no ip routing` or a `write erase`.
//
// Port of EosHost.push_rendered_config in
// projects/barf/barf/vendors/arista.py.

// -- shared write surface --------------------------------------------
//
// Writer, Op and ErrWritesNotAllowed are the cross-vendor write surface,
// declared here and used by both writers; device/writer.go (VyOS) reuses
// them rather than redeclaring them. Options.AllowWrites, the other half
// of the surface, lives in device.go.

// Op is one configuration operation to apply to a device. The two
// vendors' native shapes differ enough that Op carries both, and each
// writer reads only its own fields:
//
//   - EOS: Command, one config-mode line. The managed slice is expressed
//     as positive config lines only, so there is no verb: a *deletion* is
//     precisely what the EOS writer must never send. (EOS's own
//     `no shutdown` is a setting, not a removal.)
//   - VyOS: Verb (OpSet/OpDelete) plus Path, the `set ...` path tuple the
//     /configure API takes.
type Op struct {
	// Command is the EOS config-mode statement to apply.
	Command string

	// Verb is the VyOS operation: OpSet or OpDelete.
	Verb string
	// Path is the VyOS config path the verb applies to.
	Path []string
}

// EOSOps turns managed config lines (render.EOSManagedCommands output)
// into Ops for an EOSWriter.
func EOSOps(commands []string) []Op {
	ops := make([]Op, 0, len(commands))
	for _, command := range commands {
		ops = append(ops, Op{Command: command})
	}
	return ops
}

// Writer applies configuration to a device. Constructing one always
// requires an explicit opt-in; see Options.AllowWrites.
type Writer interface {
	// Configure applies ops. Implementations must be idempotent:
	// re-applying identical state is a no-op on the device.
	Configure(ctx context.Context, ops []Op) error
	// SaveConfig persists the running config across a reboot.
	SaveConfig(ctx context.Context) error
}

// The refusal errors live in errors.go.

// -- EOS writer -------------------------------------------------------

// EOSWriter applies the managed EOS slice over eAPI.
type EOSWriter struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	creds eosCredentials
}

var _ Writer = (*EOSWriter)(nil)

// NewEOSWriter returns a write-capable eAPI client for h.
//
// It fails unless opts.AllowWrites is set. That check is here, at
// construction, on purpose: a caller that has an *EOSWriter at all has
// already said in one named place that this run may change devices, so no
// deeper code has to remember to ask.
func NewEOSWriter(h *model.Host, opts Options) (*EOSWriter, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	if !opts.AllowWrites {
		return nil, &WritesNotAllowedError{What: fmt.Sprintf("an EOS writer for %s", h.Hostname)}
	}
	if opts.Secrets == nil {
		return nil, fmt.Errorf("device: %s: eos writer needs Options.Secrets for the admin password", h.Hostname)
	}
	return &EOSWriter{
		host:     h,
		opts:     opts,
		client:   opts.httpClient(),
		resolver: &endpointResolver{host: h, opts: opts},
	}, nil
}

func (w *EOSWriter) credentials() (admin, enable string, err error) {
	return w.creds.get(w.opts.Secrets, w.host.Hostname)
}

// eosWriteCommandAllowed reports whether cmd is inside the EOS managed
// scope and may therefore be sent in config mode.
//
// This is an allowlist of *shapes*, not a denylist of dangerous verbs: an
// unrecognised command is refused. Growing the managed scope means adding
// a shape here deliberately, which is exactly the review the scope
// promise deserves.
func eosWriteCommandAllowed(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false
	}
	// Every arm below is a prefix test, and a prefix test says nothing
	// about what follows on a *later line*. TrimSpace strips the ends,
	// not the interior, so `"username admin ssh-key ssh-ed25519 AAAA\nno
	// ip routing"` passed the username arm and then shipped a second,
	// entirely unreviewed command to the device — eAPI runs a multi-line
	// string as multiple config lines. This is reachable in practice: a
	// two-line authorized_keys blob pasted into one YAML value. A
	// semicolon is refused on the same principle.
	//
	// One op is one command. A caller with two commands passes two ops,
	// and each is then checked on its own.
	if strings.ContainsAny(trimmed, "\n\r;") {
		return false
	}
	switch {
	// The managed user and its ssh-keys. Only ManagedUsername: barf does
	// not own the other accounts on the box.
	case strings.HasPrefix(trimmed, "username "+ManagedUsername+" "):
		return true
	// The enable password.
	case strings.HasPrefix(trimmed, "enable password "), strings.HasPrefix(trimmed, "enable secret "):
		return true
	// The eAPI block and its sub-modes.
	case trimmed == "management api http-commands":
		return true
	case strings.HasPrefix(trimmed, "protocol https"):
		return true
	case strings.HasPrefix(trimmed, "vrf ") && len(strings.Fields(trimmed)) == 2:
		return true
	// The only negation the slice needs, and the only one permitted.
	// Bare `shutdown` is deliberately NOT here: inside `management api
	// http-commands` it turns eAPI off, which is the transport barf is
	// speaking over — the write would succeed and lock barf out of the
	// device for good. Nothing in the managed slice ever needs to
	// disable eAPI, so the shape is simply not admitted.
	case trimmed == "no shutdown":
		return true
	}
	return false
}

// Configure applies ops in config mode.
//
// Idempotent by nature of EOS: re-sending an identical managed line
// changes nothing. Only the managed lines are ever sent — this never
// emits a `no ...` for config outside the slice, so an unmanaged part of
// the device cannot be disturbed by a deploy.
func (w *EOSWriter) Configure(ctx context.Context, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	commands := make([]string, 0, len(ops)+2)
	commands = append(commands, "configure")
	for _, op := range ops {
		// A VyOS-shaped op (verb + path) is not an EOS command; refuse it
		// rather than guess at a translation. In particular OpDelete has
		// no meaning inside the EOS slice, and admitting it would be the
		// one way a deploy could remove config.
		if op.Verb != "" || len(op.Path) > 0 {
			return &UnmanagedCommandError{Command: op.Verb + " " + strings.Join(op.Path, " ")}
		}
		if !eosWriteCommandAllowed(op.Command) {
			return &UnmanagedCommandError{Command: op.Command}
		}
		commands = append(commands, op.Command)
	}
	commands = append(commands, "end")

	// A commit gets a commit's budget, not a read's: see the timeout
	// constants in device.go.
	ctx, cancel := w.opts.withOpTimeout(ctx, TimeoutConfigure)
	defer cancel()

	_, err := w.run(ctx, commands...)
	return err
}

// SaveConfig writes the running config to startup, so the managed slice
// survives a reload. Not a config-mode command; sent on its own.
func (w *EOSWriter) SaveConfig(ctx context.Context) error {
	ctx, cancel := w.opts.withOpTimeout(ctx, TimeoutSave)
	defer cancel()

	_, err := w.run(ctx, "copy running-config startup-config")
	return err
}

// run is the writer's single request primitive, deliberately separate
// from EOSReader.runShow so that neither guard can be loosened by editing
// the other.
func (w *EOSWriter) run(ctx context.Context, cmds ...string) ([]json.RawMessage, error) {
	admin, enable, err := w.credentials()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", w.host.Hostname, err)
	}
	address, err := w.resolver.resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: no address answering on %d (is eAPI enabled? see docs/network/arista-adoption.md): %w",
			w.host.Hostname, w.opts.port(), err)
	}

	payload := make([]any, 0, len(cmds)+1)
	if enable != "" {
		payload = append(payload, eosEnableCmd{Cmd: "enable", Input: enable})
	} else {
		payload = append(payload, "enable")
	}
	for _, cmd := range cmds {
		payload = append(payload, cmd)
	}

	body, err := json.Marshal(eosRequest{
		JSONRPC: "2.0",
		Method:  "runCmds",
		Params:  eosParams{Version: 1, Cmds: payload, Format: "json"},
		ID:      "barf",
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d%s", HostForURL(address), w.opts.port(), eosCommandPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(ManagedUsername, admin)

	resp, err := w.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: eAPI request failed: %w", w.host.Hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s: eAPI authentication failed for user %s", w.host.Hostname, ManagedUsername)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: eAPI HTTP %d", w.host.Hostname, resp.StatusCode)
	}

	var decoded eosResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed eAPI response: %w", w.host.Hostname, err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("%s: %w", w.host.Hostname, decoded.Error)
	}
	if len(decoded.Result) < len(cmds)+1 {
		return nil, fmt.Errorf("%s: eAPI returned %d results for %d commands",
			w.host.Hostname, len(decoded.Result), len(cmds))
	}
	// Drop the `enable` result.
	return decoded.Result[1:], nil
}
