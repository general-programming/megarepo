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

// The ONLY write path to an Arista device, kept separate from eos.go so
// neither guard can be loosened by editing the other. It is scoped to the
// managed slice — the `admin` user, its ssh-keys, the enable password, the
// eAPI block — so eosWriteCommandAllowed admits only those shapes plus `no
// shutdown`: a deploy cannot carry `no ip routing` or `write erase`.
//
// Port of EosHost.push_rendered_config.

// Op is one configuration operation; it and Writer are the cross-vendor write
// surface, each writer reading only its own fields. EOS has no verb because a
// deletion is exactly what the EOS writer must never send (`no shutdown` is a
// setting, not a removal).
type Op struct {
	Command string // EOS config-mode statement

	Verb string   // VyOS: OpSet or OpDelete
	Path []string // VyOS config path the verb applies to
}

// EOSOps turns managed config lines (render.EOSManagedCommands) into Ops.
func EOSOps(commands []string) []Op {
	ops := make([]Op, 0, len(commands))
	for _, command := range commands {
		ops = append(ops, Op{Command: command})
	}
	return ops
}

// Writer applies configuration; constructing one requires Options.AllowWrites.
type Writer interface {
	// Configure applies ops. Implementations must be idempotent.
	Configure(ctx context.Context, ops []Op) error
	// SaveConfig persists the running config across a reboot.
	SaveConfig(ctx context.Context) error
}

// EOSWriter applies the managed EOS slice over eAPI.
type EOSWriter struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	creds eosCredentials
}

var _ Writer = (*EOSWriter)(nil)

// NewEOSWriter returns a write-capable eAPI client for h, failing unless
// opts.AllowWrites is set — at construction, so holding an *EOSWriter already
// means one named place allowed this run to write.
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

// eosWriteCommandAllowed is an allowlist of *shapes*, not a denylist of verbs:
// an unrecognised command is refused, so growing the managed scope means
// deliberately adding a shape here.
func eosWriteCommandAllowed(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false
	}
	// Every arm below is a prefix test and eAPI runs a multi-line string as
	// multiple config lines, so "username admin ssh-key ...\nno ip routing"
	// would pass the username arm and ship an unreviewed second command —
	// reachable from a two-line authorized_keys blob in one YAML value.
	// Semicolons are refused on the same principle.
	if strings.ContainsAny(trimmed, "\n\r;") {
		return false
	}
	switch {
	// The managed user and its ssh-keys; barf owns no other account.
	case strings.HasPrefix(trimmed, "username "+ManagedUsername+" "):
		return true
	case strings.HasPrefix(trimmed, "enable password "), strings.HasPrefix(trimmed, "enable secret "):
		return true
	// The eAPI block and its sub-modes.
	case trimmed == "management api http-commands":
		return true
	case strings.HasPrefix(trimmed, "protocol https"):
		return true
	case strings.HasPrefix(trimmed, "vrf ") && len(strings.Fields(trimmed)) == 2:
		return true
	// The only negation permitted. Bare `shutdown` is deliberately absent:
	// inside `management api http-commands` it turns off eAPI, so the write
	// would succeed and lock barf out of the device for good.
	case trimmed == "no shutdown":
		return true
	}
	return false
}

// Configure applies ops in config mode. Idempotent, and only managed lines are
// ever sent, so a deploy cannot disturb an unmanaged part of the device.
func (w *EOSWriter) Configure(ctx context.Context, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	commands := make([]string, 0, len(ops)+2)
	commands = append(commands, "configure")
	for _, op := range ops {
		// A VyOS-shaped op is not an EOS command; refuse rather than guess.
		// OpDelete would be the one way a deploy could remove config.
		if op.Verb != "" || len(op.Path) > 0 {
			return &UnmanagedCommandError{Command: op.Verb + " " + strings.Join(op.Path, " ")}
		}
		if !eosWriteCommandAllowed(op.Command) {
			return &UnmanagedCommandError{Command: op.Command}
		}
		commands = append(commands, op.Command)
	}
	commands = append(commands, "end")

	// A commit gets a commit's budget, not a read's.
	ctx, cancel := w.opts.withOpTimeout(ctx, TimeoutConfigure)
	defer cancel()

	_, err := w.run(ctx, commands...)
	return err
}

// SaveConfig writes running to startup so the slice survives a reload; not a
// config-mode command, so it is sent on its own.
func (w *EOSWriter) SaveConfig(ctx context.Context) error {
	ctx, cancel := w.opts.withOpTimeout(ctx, TimeoutSave)
	defer cancel()

	_, err := w.run(ctx, "copy running-config startup-config")
	return err
}

// run is the writer's single request primitive, separate from runShow.
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
