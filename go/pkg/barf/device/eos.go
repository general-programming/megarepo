package device

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// ManagedUsername is the account barf authenticates as on EOS devices;
// the Python side manages this same user (arista.MANAGED_USERNAME).
const ManagedUsername = "admin"

// eosCommandPath is the eAPI endpoint. There is exactly one, and it only
// ever carries the "runCmds" method (never "runConfigCmds").
const eosCommandPath = "/command-api"

// eosCommandAllowed reports whether cmd is a read verb this package is
// permitted to send.
//
// This is the write guard for the EOS transport: only `show ...` (in any
// of its scoped spellings) and the `enable` mode-entry command pass.
// `configure`, `config`, `copy running-config startup-config`, `write`,
// `reload`, `no ...` and every other mutating verb are rejected before a
// request is built. There is no bypass.
func eosCommandAllowed(cmd string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(cmd))
	if trimmed == "enable" {
		return true
	}
	// `show` only, and never `show ... | ...` piping into a mutating
	// alias. Piping to a filter is harmless, but keeping the surface
	// literal keeps the guard trivially auditable.
	if !strings.HasPrefix(trimmed, "show ") {
		return false
	}
	return !strings.ContainsAny(trimmed, "|;")
}

// EOSReader reads an Arista EOS device over eAPI (JSON-RPC/HTTPS).
//
// The JSON-RPC protocol is hand-rolled rather than delegated to
// goeapi/pyeapi: it is a single POST with a small, stable body, and
// hand-rolling (a) adds no dependency, (b) gives real context.Context
// cancellation, which goeapi does not expose, and — the reason that
// matters most here — (c) means the only code that can build a request is
// in this file, behind eosCommandAllowed. A vendored client would ship
// config-push methods (goeapi's Node.Config, pyeapi's node.config) that
// are one call away from a caller.
type EOSReader struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	credsOnce sync.Once
	admin     string
	enable    string
	credsErr  error

	versionOnce sync.Once
	versionData eosShowVersion
	versionErr  error
}

var _ Reader = (*EOSReader)(nil)

// NewEOS returns a read-only eAPI reader for h.
func NewEOS(h *model.Host, opts Options) (*EOSReader, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	if opts.Secrets == nil {
		return nil, fmt.Errorf("device: %s: eos needs Options.Secrets for the admin password", h.Hostname)
	}
	return &EOSReader{
		host:     h,
		opts:     opts,
		client:   opts.httpClient(),
		resolver: &endpointResolver{host: h, opts: opts},
	}, nil
}

func (r *EOSReader) credentials() (admin, enable string, err error) {
	r.credsOnce.Do(func() {
		r.admin, r.credsErr = r.opts.Secrets.HostSecret(r.host.Hostname, "admin-password")
		if r.credsErr != nil {
			return
		}
		// Some shows need enable mode; a missing enable secret is not
		// fatal, the request just goes unprivileged.
		r.enable, _ = r.opts.Secrets.HostSecret(r.host.Hostname, "enable-password")
	})
	return r.admin, r.enable, r.credsErr
}

// -- JSON-RPC wire types ---------------------------------------------

type eosRequest struct {
	JSONRPC string    `json:"jsonrpc"`
	Method  string    `json:"method"`
	Params  eosParams `json:"params"`
	ID      string    `json:"id"`
}

type eosParams struct {
	Version int    `json:"version"`
	Cmds    []any  `json:"cmds"`
	Format  string `json:"format"`
}

type eosEnableCmd struct {
	Cmd   string `json:"cmd"`
	Input string `json:"input"`
}

type eosResponse struct {
	Result []json.RawMessage `json:"result"`
	Error  *eosError         `json:"error"`
}

type eosError struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    []json.RawMessage `json:"data"`
}

func (e *eosError) Error() string {
	return fmt.Sprintf("eAPI error %d: %s", e.Code, e.Message)
}

// runShow sends one or more `show` commands and returns their results.
//
// This is the ONLY function in the package that performs an eAPI request,
// and it refuses anything eosCommandAllowed rejects. The method field is
// hardcoded to "runCmds"; "runConfigCmds" appears nowhere in this
// package.
func (r *EOSReader) runShow(ctx context.Context, format string, cmds ...string) ([]json.RawMessage, error) {
	for _, cmd := range cmds {
		if !eosCommandAllowed(cmd) {
			return nil, &ErrWriteAttempt{What: fmt.Sprintf("EOS command %q", cmd)}
		}
	}

	// A read's budget. Writes get their own, longer ones — see
	// eos_writer.go's run.
	ctx, cancel := r.opts.withOpTimeout(ctx, TimeoutShow)
	defer cancel()

	admin, enable, err := r.credentials()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.host.Hostname, err)
	}
	address, err := r.resolver.resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: no address answering on %d (is eAPI enabled? see docs/network/arista-adoption.md): %w",
			r.host.Hostname, r.opts.port(), err)
	}

	payload := make([]any, 0, len(cmds)+1)
	// pyeapi's node.enable() prepends the enable command with the secret
	// as its input; without a secret it is a bare "enable".
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
		Params:  eosParams{Version: 1, Cmds: payload, Format: format},
		ID:      "barf",
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://%s:%d%s", hostForURL(address), r.opts.port(), eosCommandPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(ManagedUsername, admin)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: eAPI request failed: %w", r.host.Hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s: eAPI authentication failed for user %s", r.host.Hostname, ManagedUsername)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: eAPI HTTP %d", r.host.Hostname, resp.StatusCode)
	}

	var decoded eosResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed eAPI response: %w", r.host.Hostname, err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("%s: %w", r.host.Hostname, decoded.Error)
	}
	if len(decoded.Result) < len(cmds)+1 {
		return nil, fmt.Errorf("%s: eAPI returned %d results for %d commands",
			r.host.Hostname, len(decoded.Result), len(cmds))
	}
	// Drop the `enable` result.
	return decoded.Result[1:], nil
}

// -- show version -----------------------------------------------------

type eosShowVersion struct {
	Version          string `json:"version"`
	ModelName        string `json:"modelName"`
	HardwareRevision string `json:"hardwareRevision"`
	// Pre-4.27 EOS omits uptime entirely; a pointer distinguishes that
	// from a genuine 0.
	Uptime *float64 `json:"uptime"`
}

// showVersion fetches and caches `show version`, so Status costs one
// round trip for version + model + uptime (as the Python side does).
func (r *EOSReader) showVersion(ctx context.Context) (eosShowVersion, error) {
	r.versionOnce.Do(func() {
		results, err := r.runShow(ctx, "json", "show version")
		if err != nil {
			r.versionErr = err
			return
		}
		r.versionErr = json.Unmarshal(results[0], &r.versionData)
	})
	return r.versionData, r.versionErr
}

// Version is the running EOS version, e.g. "4.34.2F".
func (r *EOSReader) Version(ctx context.Context) (string, error) {
	version, err := r.showVersion(ctx)
	if err != nil {
		return "", err
	}
	return version.Version, nil
}

// Model is the hardware SKU, or "?" when the device does not report one.
func (r *EOSReader) Model(ctx context.Context) (string, error) {
	version, err := r.showVersion(ctx)
	if err != nil {
		return "", err
	}
	return eosModel(version), nil
}

func eosModel(version eosShowVersion) string {
	if version.ModelName != "" {
		return version.ModelName
	}
	if version.HardwareRevision != "" {
		return version.HardwareRevision
	}
	return "?"
}

// Uptime is the device's human-readable uptime.
func (r *EOSReader) Uptime(ctx context.Context) (string, error) {
	version, err := r.showVersion(ctx)
	if err != nil {
		return "", err
	}
	if version.Uptime != nil {
		return HumanizeUptime(*version.Uptime), nil
	}
	// Pre-4.27 EOS omits uptime from `show version` json.
	results, err := r.runShow(ctx, "text", "show uptime")
	if err != nil {
		return "", err
	}
	var out struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(results[0], &out); err != nil {
		return "", err
	}
	return parseShowUptime(out.Output), nil
}

// HumanizeUptime formats an uptime in seconds the way the Python
// implementation does: "482d 17h", "5h 12m", "42m".
func HumanizeUptime(seconds float64) string {
	total := int64(seconds)
	minutes := total / 60
	hours := minutes / 60
	minutes %= 60
	days := hours / 24
	hours %= 24

	if days != 0 {
		return fmt.Sprintf("%dd %dh", days, hours)
	}
	if hours != 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

// parseShowUptime pulls the uptime out of `show uptime` text output.
func parseShowUptime(output string) string {
	if before, after, found := strings.Cut(output, " up "); found {
		_ = before
		if idx := strings.Index(after, ", load"); idx >= 0 {
			after = after[:idx]
		}
		return strings.Trim(after, ", \n")
	}
	return strings.TrimSpace(output)
}

// -- Reader -----------------------------------------------------------

// Status reports version, uptime and model in one round trip (two on
// pre-4.27 EOS, which needs the `show uptime` fallback).
func (r *EOSReader) Status(ctx context.Context) (Status, error) {
	version, err := r.showVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	uptime, err := r.Uptime(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Version: version.Version,
		Uptime:  uptime,
		Model:   eosModel(version),
	}, nil
}

// RunningConfig returns `show running-config all` as text — a read; the
// device is not asked to enter config mode and nothing is saved.
func (r *EOSReader) RunningConfig(ctx context.Context) (string, error) {
	return r.RunningConfigSection(ctx, "")
}

// RunningConfigSection returns `show running-config all section <name>`,
// or the whole config when name is empty. Mirrors the scoped reads
// arista.py's _device_managed_state does.
func (r *EOSReader) RunningConfigSection(ctx context.Context, name string) (string, error) {
	cmd := "show running-config all"
	if name != "" {
		cmd += " section " + name
	}
	results, err := r.runShow(ctx, "text", cmd)
	if err != nil {
		return "", err
	}
	var out struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(results[0], &out); err != nil {
		return "", err
	}
	return out.Output, nil
}

// RunningConfigSections concatenates several scoped reads in one request,
// as the Python managed-state read does.
func (r *EOSReader) RunningConfigSections(ctx context.Context, names ...string) (string, error) {
	cmds := make([]string, 0, len(names))
	for _, name := range names {
		cmds = append(cmds, "show running-config all section "+name)
	}
	results, err := r.runShow(ctx, "text", cmds...)
	if err != nil {
		return "", err
	}
	outputs := make([]string, 0, len(results))
	for _, result := range results {
		var out struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal(result, &out); err != nil {
			return "", err
		}
		outputs = append(outputs, out.Output)
	}
	return strings.Join(outputs, "\n"), nil
}
