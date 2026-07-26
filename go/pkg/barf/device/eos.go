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

// ManagedUsername is the account barf authenticates as on EOS devices.
const ManagedUsername = "admin"

// eosCommandPath only ever carries "runCmds", never "runConfigCmds".
const eosCommandPath = "/command-api"

// eosCommandAllowed is the write guard for the EOS read transport: only
// `show ...` and `enable` pass, and there is no bypass.
func eosCommandAllowed(cmd string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(cmd))
	if trimmed == "enable" {
		return true
	}
	// No `show ... | ...`: refusing pipes keeps the guard auditable and
	// blocks piping into a mutating alias.
	if !strings.HasPrefix(trimmed, "show ") {
		return false
	}
	return !strings.ContainsAny(trimmed, "|;")
}

// EOSReader reads an Arista EOS device over eAPI. Hand-rolled rather than
// goeapi, which has no context cancellation and whose Node.Config would put
// config-push one call away from a caller.
type EOSReader struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	creds eosCredentials

	versionOnce sync.Once
	versionData eosShowVersion
	versionErr  error
}

var _ Reader = (*EOSReader)(nil)

// NewEOS returns a read-only EOS reader for h.
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
	return r.creds.get(r.opts.Secrets, r.host.Hostname)
}

// eosCredentials caches one host's admin/enable secrets. Shared by reader and
// writer, which is safe where sharing a transport would not be: it only reads
// secrets, the request builders decide what is sent.
type eosCredentials struct {
	once   sync.Once
	admin  string
	enable string
	err    error
}

// get resolves both secrets once. A missing enable secret is deliberately NOT
// fatal (the device itself gives the better error); a missing admin one is.
func (c *eosCredentials) get(secrets Secrets, hostname string) (admin, enable string, err error) {
	c.once.Do(func() {
		c.admin, c.err = secrets.HostSecret(hostname, "admin-password")
		if c.err != nil {
			return
		}
		c.enable, _ = secrets.HostSecret(hostname, "enable-password")
	})
	return c.admin, c.enable, c.err
}

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

// runShow is the only read-side eAPI primitive; it refuses anything
// eosCommandAllowed rejects.
func (r *EOSReader) runShow(ctx context.Context, format string, cmds ...string) ([]json.RawMessage, error) {
	for _, cmd := range cmds {
		if !eosCommandAllowed(cmd) {
			return nil, &WriteAttemptError{What: fmt.Sprintf("EOS command %q", cmd)}
		}
	}

	// A read's budget; writes get their own, longer ones.
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
	// As pyeapi's node.enable(): the secret is the command's input.
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

	url := fmt.Sprintf("https://%s:%d%s", HostForURL(address), r.opts.port(), eosCommandPath)
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

type eosShowVersion struct {
	Version          string `json:"version"`
	ModelName        string `json:"modelName"`
	HardwareRevision string `json:"hardwareRevision"`
	// Pre-4.27 EOS omits uptime; a pointer distinguishes that from a real 0.
	Uptime *float64 `json:"uptime"`
}

// showVersion caches `show version`, so Status costs one round trip.
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

// Version is the running EOS version string.
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

// Uptime is the device uptime as EOS reports it.
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

// HumanizeUptime formats seconds as Python does: "482d 17h", "5h 12m", "42m".
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

// parseShowUptime pulls the uptime out of `show uptime` text, mirroring
// Python's split on " up " then on ", load" (which drops the trailing load
// averages).
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

// RunningConfig returns `show running-config all` as text; no config mode
// is entered and nothing is saved.
func (r *EOSReader) RunningConfig(ctx context.Context) (string, error) {
	return r.RunningConfigSection(ctx, "")
}

// RunningConfigSection returns a scoped read, or the whole config when name
// is empty.
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

// RunningConfigSections concatenates several scoped reads in one request.
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
