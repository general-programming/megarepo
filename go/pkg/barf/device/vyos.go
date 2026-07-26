package device

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// VyOSAPIKeySecret is the shared-secret name holding the VyOS HTTPS API
// key. Mirrors Python `VaultSecrets().vyos_api_password`, whose attribute
// name is dashed into this path.
const VyOSAPIKeySecret = "vyos-api-password"

// The only two VyOS API endpoints this package may talk to, and the only
// ops each accepts. `/configure`, `/config-file` (save) and `/image`
// (delete) are writes and are deliberately absent — vyos_api_configure,
// vyos_api_config_save and vyos_api_image_delete are NOT ported.
var vyosAllowedRequests = map[string]map[string]bool{
	"show":     {"show": true},
	"retrieve": {"showConfig": true},
}

// vyosRequestAllowed is the write guard for the VyOS transport: an
// endpoint/op pair not in vyosAllowedRequests never reaches the wire.
func vyosRequestAllowed(endpoint, op string) bool {
	return vyosAllowedRequests[endpoint][op]
}

// vyosReadTimeout is the per-endpoint budget for a read, matching the
// Python defaults (vyos_api_show=10s, vyos_api_retrieve_config=30s).
func vyosReadTimeout(endpoint string) time.Duration {
	if endpoint == "retrieve" {
		return TimeoutRetrieve
	}
	return TimeoutShow
}

// VyOSReader reads a VyOS device over its HTTPS API.
//
// Transport is a hand-rolled form-POST because that is the whole
// protocol: `data` (a JSON op) plus `key`, with a
// {success, data, error} JSON reply. There is no Go client library for
// it, and writing the two read calls directly keeps the endpoint set
// closed.
type VyOSReader struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	keyOnce sync.Once
	key     string
	keyErr  error

	versionOnce sync.Once
	versionOut  string
	versionErr  error
}

var _ Reader = (*VyOSReader)(nil)

// NewVyOS returns a read-only HTTPS-API reader for h.
func NewVyOS(h *model.Host, opts Options) (*VyOSReader, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	if opts.GlobalSecrets == nil {
		return nil, fmt.Errorf("device: %s: vyos needs Options.GlobalSecrets for the %s key",
			h.Hostname, VyOSAPIKeySecret)
	}
	return &VyOSReader{
		host:     h,
		opts:     opts,
		client:   opts.httpClient(),
		resolver: &endpointResolver{host: h, opts: opts},
	}, nil
}

func (r *VyOSReader) apiKey() (string, error) {
	r.keyOnce.Do(func() {
		r.key, r.keyErr = r.opts.GlobalSecrets.GlobalSecret(VyOSAPIKeySecret)
	})
	return r.key, r.keyErr
}

type vyosResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   any             `json:"error"`
}

// request is the ONLY function in the package that performs a VyOS API
// request. It refuses any endpoint/op pair vyosRequestAllowed rejects, so
// no caller can reach /configure, /config-file or /image through it.
func (r *VyOSReader) request(ctx context.Context, endpoint, op string, payload any) (json.RawMessage, error) {
	if !vyosRequestAllowed(endpoint, op) {
		return nil, &ErrWriteAttempt{What: fmt.Sprintf("VyOS %s request op=%q", endpoint, op)}
	}

	ctx, cancel := r.opts.withOpTimeout(ctx, vyosReadTimeout(endpoint))
	defer cancel()

	key, err := r.apiKey()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.host.Hostname, err)
	}
	address, err := r.resolver.resolve(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: no reachable address: %w", r.host.Hostname, err)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	form := url.Values{"data": {string(data)}, "key": {key}}

	target := fmt.Sprintf("https://%s:%d/%s", hostForURL(address), r.opts.port(), endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: vyos api request failed: %w", r.host.Hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}

	var decoded vyosResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed vyos api response (HTTP %d): %w",
			r.host.Hostname, resp.StatusCode, err)
	}
	if !decoded.Success {
		message := "unknown API error"
		if decoded.Error != nil {
			if s, ok := decoded.Error.(string); ok && s != "" {
				message = s
			} else {
				message = fmt.Sprint(decoded.Error)
			}
		}
		return nil, fmt.Errorf("%s: vyos api: %s", r.host.Hostname, message)
	}
	return decoded.Data, nil
}

// Show runs an operational `show` command, e.g. Show(ctx, "version") or
// Show(ctx, "system", "uptime"). Ports vyos_api_show.
func (r *VyOSReader) Show(ctx context.Context, path ...string) (string, error) {
	if path == nil {
		path = []string{}
	}
	data, err := r.request(ctx, "show", "show", map[string]any{"op": "show", "path": path})
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		// Non-string payloads are unexpected for op-mode; surface them raw.
		return string(data), nil
	}
	return text, nil
}

// RetrieveConfig fetches (part of) the running config as a JSON tree via
// `/retrieve` — a read-only op ("showConfig"). Ports
// vyos_api_retrieve_config.
func (r *VyOSReader) RetrieveConfig(ctx context.Context, path ...string) (map[string]any, error) {
	if path == nil {
		path = []string{}
	}
	data, err := r.request(ctx, "retrieve", "showConfig", map[string]any{"op": "showConfig", "path": path})
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("%s: unexpected /retrieve payload: %w", r.host.Hostname, err)
	}
	// A JSON `null` (or an absent data field) unmarshals into a nil map
	// without error, and a nil map is indistinguishable from an empty
	// config downstream: the running set collapses to nothing and the
	// operator is shown a full-reconfigure diff for what was really a
	// failed read. Python refuses it explicitly —
	// vyos_api.py: `if not isinstance(data, dict): raise`.
	if tree == nil {
		return nil, fmt.Errorf("%s: unexpected /retrieve payload: %s is not a config tree",
			r.host.Hostname, retrievePayloadDescription(data))
	}
	return tree, nil
}

// retrievePayloadDescription names what came back instead of an object,
// without echoing a whole config into an error message.
func retrievePayloadDescription(data json.RawMessage) string {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return "an empty response"
	}
	if trimmed == "null" {
		return "JSON null"
	}
	return trimmed
}

// -- parsing ----------------------------------------------------------

// ParseVyOSVersion pulls the version out of `show version` output. The
// "VyOS " prefix is stripped so the value lines up with upstream release
// tags (e.g. "2026.06.30-0048-rolling").
func ParseVyOSVersion(output string) string {
	for _, line := range splitLines(output) {
		if strings.HasPrefix(strings.ToLower(line), "version:") {
			_, value, _ := strings.Cut(line, ":")
			return strings.TrimPrefix(strings.TrimSpace(value), "VyOS ")
		}
	}
	if strings.TrimSpace(output) == "" {
		return "-"
	}
	// splitLines, not Split(_, "\n"): this branch returns the line
	// verbatim, so a CRLF device answer would otherwise come back as
	// "1.4.2\r" where Python's splitlines() gives "1.4.2".
	first := splitLines(strings.TrimSpace(output))[0]
	return strings.TrimPrefix(first, "VyOS ")
}

// ParseVyOSModel pulls the SMBIOS hardware model out of `show version`
// output. The vendor is prefixed only when the model does not already
// repeat it.
func ParseVyOSModel(output string) string {
	var vendor, hardware string
	for _, raw := range splitLines(output) {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "hardware vendor:"):
			_, value, _ := strings.Cut(line, ":")
			vendor = strings.TrimSpace(value)
		case strings.HasPrefix(lower, "hardware model:"):
			_, value, _ := strings.Cut(line, ":")
			hardware = strings.TrimSpace(value)
		}
	}

	if vendor != "" && hardware != "" &&
		!strings.HasPrefix(strings.ToLower(hardware), strings.ToLower(vendor)) {
		return vendor + " " + hardware
	}
	if hardware != "" {
		return hardware
	}
	if vendor != "" {
		return vendor
	}
	return "?"
}

// ParseVyOSUptime pulls a human uptime out of `show system uptime`.
func ParseVyOSUptime(output string) string {
	for _, raw := range splitLines(output) {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "uptime:") {
			_, value, _ := strings.Cut(line, ":")
			return strings.TrimSpace(value)
		}
		return line
	}
	return "-"
}

// SystemImage is one installed VyOS system image.
type SystemImage struct {
	Name        string
	DefaultBoot bool
	Running     bool
}

var vyosNumberedImage = regexp.MustCompile(`^\s*\d+:\s+(\S+)(.*)$`)

// ParseSystemImages parses `show system image` output. Handles both the
// modern table format (Name / Default boot / Running columns) and the
// legacy numbered list ("1: name (default boot) (running image)").
func ParseSystemImages(output string) []SystemImage {
	lines := splitLines(output)

	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Name") && strings.Contains(line, "Running") {
			defaultCol := strings.Index(line, "Default")
			runningCol := strings.Index(line, "Running")
			if defaultCol < 0 || runningCol < defaultCol {
				break
			}

			images := []SystemImage{}
			for _, row := range lines[i+1:] {
				trimmed := strings.TrimSpace(row)
				if trimmed == "" || strings.Trim(trimmed, "- ") == "" {
					continue
				}
				images = append(images, SystemImage{
					Name:        strings.Fields(row)[0],
					DefaultBoot: columnYes(row, defaultCol, runningCol),
					Running:     columnYes(row, runningCol, len(row)),
				})
			}
			return images
		}
	}

	images := []SystemImage{}
	for _, line := range lines {
		match := vyosNumberedImage.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		images = append(images, SystemImage{
			Name:        match[1],
			DefaultBoot: strings.Contains(match[2], "default boot"),
			Running:     strings.Contains(match[2], "running"),
		})
	}
	return images
}

// columnYes reports whether row[start:end] starts with "yes", tolerating
// rows shorter than the header.
func columnYes(row string, start, end int) bool {
	if start >= len(row) {
		return false
	}
	if end > len(row) {
		end = len(row)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(row[start:end])), "yes")
}

// -- Reader -----------------------------------------------------------

// versionOutput fetches `show version` once per reader, so version and
// model together cost one round trip (as the Python side caches).
func (r *VyOSReader) versionOutput(ctx context.Context) (string, error) {
	r.versionOnce.Do(func() {
		r.versionOut, r.versionErr = r.Show(ctx, "version")
	})
	return r.versionOut, r.versionErr
}

// Version is the running VyOS version.
func (r *VyOSReader) Version(ctx context.Context) (string, error) {
	output, err := r.versionOutput(ctx)
	if err != nil {
		return "", err
	}
	return ParseVyOSVersion(output), nil
}

// Model is the SMBIOS hardware model the platform advertises.
func (r *VyOSReader) Model(ctx context.Context) (string, error) {
	output, err := r.versionOutput(ctx)
	if err != nil {
		return "", err
	}
	return ParseVyOSModel(output), nil
}

// Uptime is the device's human-readable uptime.
func (r *VyOSReader) Uptime(ctx context.Context) (string, error) {
	output, err := r.Show(ctx, "system", "uptime")
	if err != nil {
		return "", err
	}
	return ParseVyOSUptime(output), nil
}

// SystemImages lists the installed system images (read-only; image
// deletion is deliberately not ported).
func (r *VyOSReader) SystemImages(ctx context.Context) ([]SystemImage, error) {
	output, err := r.Show(ctx, "system", "image")
	if err != nil {
		return nil, err
	}
	return ParseSystemImages(output), nil
}

// Status reports version, uptime and model; two round trips (version is
// cached and shared by version+model, uptime is its own show).
func (r *VyOSReader) Status(ctx context.Context) (Status, error) {
	version, err := r.versionOutput(ctx)
	if err != nil {
		return Status{}, err
	}
	uptime, err := r.Uptime(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Version: ParseVyOSVersion(version),
		Uptime:  uptime,
		Model:   ParseVyOSModel(version),
	}, nil
}

// RunningConfig returns the running config as the device's own JSON tree,
// pretty-printed. VyOS has no text `show running-config` over the API;
// `/retrieve` is the vendor-native dump. Callers that want `set` command
// paths should use RetrieveConfig and flatten the tree themselves.
func (r *VyOSReader) RunningConfig(ctx context.Context) (string, error) {
	tree, err := r.RetrieveConfig(ctx)
	if err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return "", err
	}
	return string(encoded) + "\n", nil
}
