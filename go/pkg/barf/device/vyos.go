package device

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/common/pytext"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vyoswire"
)

// VyOSAPIKeySecret holds the VyOS HTTPS API key (`vyos_api_password`).
const VyOSAPIKeySecret = "vyos-api-password"

// The only endpoints a reader may talk to, and the only ops each accepts;
// /configure and /config-file live in writer.go, /image is not ported.
var vyosAllowedRequests = map[string]map[string]bool{
	"show":     {"show": true},
	"retrieve": {"showConfig": true},
}

// vyosRequestAllowed is the write guard: a pair not in vyosAllowedRequests
// never reaches the wire.
func vyosRequestAllowed(endpoint, op string) bool {
	return vyosAllowedRequests[endpoint][op]
}

// vyosReadTimeout is the per-endpoint read budget (Python: show=10s,
// retrieve_config=30s).
func vyosReadTimeout(endpoint string) time.Duration {
	if endpoint == "retrieve" {
		return TimeoutRetrieve
	}
	return TimeoutShow
}

// VyOSReader reads a VyOS device over its HTTPS API with a hand-rolled
// form-POST: that is the whole protocol, and it keeps the endpoint set closed.
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

// NewVyOS returns a read-only VyOS reader for h.
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

// request is the ONLY function performing a VyOS read request, and it refuses
// whatever vyosRequestAllowed rejects. That guard is unrelated to
// Options.AllowWrites, and vyoswire shares the mechanics, not the allowlist.
func (r *VyOSReader) request(ctx context.Context, endpoint, op string, payload any) (json.RawMessage, error) {
	if !vyosRequestAllowed(endpoint, op) {
		return nil, &WriteAttemptError{What: fmt.Sprintf("VyOS %s request op=%q", endpoint, op)}
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

	target := fmt.Sprintf("https://%s:%d/%s", HostForURL(address), r.opts.port(), endpoint)
	return vyoswire.Post(ctx, r.client, r.host.Hostname, target, key, payload)
}

// Show runs an operational `show` command. Ports vyos_api_show.
func (r *VyOSReader) Show(ctx context.Context, path ...string) (string, error) {
	if path == nil {
		path = []string{}
	}
	data, err := r.request(ctx, "show", "show", map[string]any{"op": "show", "path": path})
	if err != nil {
		return "", err
	}
	// vyoswire.Text surfaces a non-string payload raw rather than dropping it.
	return vyoswire.Text(data), nil
}

// RetrieveConfig fetches the running config as a JSON tree via `/retrieve`.
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
	// A JSON `null` unmarshals into a nil map without error, and a nil map
	// looks like an empty config downstream: a failed read would print a
	// full-reconfigure diff. Python refuses it too (`isinstance(data, dict)`).
	if tree == nil {
		return nil, fmt.Errorf("%s: unexpected /retrieve payload: %s is not a config tree",
			r.host.Hostname, retrievePayloadDescription(data))
	}
	return tree, nil
}

// retrievePayloadDescription names what came back instead of an object.
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

// ParseVyOSVersion pulls the version out of `show version`, stripping the
// "VyOS " prefix so it matches upstream release tags.
func ParseVyOSVersion(output string) string {
	for _, line := range pytext.SplitLines(output) {
		if strings.HasPrefix(strings.ToLower(line), "version:") {
			_, value, _ := strings.Cut(line, ":")
			return strings.TrimPrefix(strings.TrimSpace(value), "VyOS ")
		}
	}
	if strings.TrimSpace(output) == "" {
		return "-"
	}
	// pytext.SplitLines, not Split(_, "\n"): this branch returns the line
	// verbatim, so a CRLF answer must give "1.4.2", not "1.4.2\r".
	first := pytext.SplitLines(strings.TrimSpace(output))[0]
	return strings.TrimPrefix(first, "VyOS ")
}

// ParseVyOSModel pulls the SMBIOS model out of `show version`, prefixing the
// vendor only when the model does not repeat it.
func ParseVyOSModel(output string) string {
	var vendor, hardware string
	for _, raw := range pytext.SplitLines(output) {
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
	for _, raw := range pytext.SplitLines(output) {
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

// SystemImage is one entry from `show system image`.
type SystemImage struct {
	Name        string
	DefaultBoot bool
	Running     bool
}

var vyosNumberedImage = regexp.MustCompile(`^\s*\d+:\s+(\S+)(.*)$`)

// ParseSystemImages parses `show system image` in both the modern table and
// the legacy numbered-list format.
func ParseSystemImages(output string) []SystemImage {
	lines := pytext.SplitLines(output)

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

// columnYes handles rows shorter than the header.
func columnYes(row string, start, end int) bool {
	if start >= len(row) {
		return false
	}
	if end > len(row) {
		end = len(row)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(row[start:end])), "yes")
}

// versionOutput fetches `show version` once, so version+model cost one trip.
func (r *VyOSReader) versionOutput(ctx context.Context) (string, error) {
	r.versionOnce.Do(func() {
		r.versionOut, r.versionErr = r.Show(ctx, "version")
	})
	return r.versionOut, r.versionErr
}

// Version is the running VyOS version string.
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

// Uptime is the device uptime as VyOS reports it.
func (r *VyOSReader) Uptime(ctx context.Context) (string, error) {
	output, err := r.Show(ctx, "system", "uptime")
	if err != nil {
		return "", err
	}
	return ParseVyOSUptime(output), nil
}

// SystemImages lists the installed images; deletion is not ported here.
func (r *VyOSReader) SystemImages(ctx context.Context) ([]SystemImage, error) {
	output, err := r.Show(ctx, "system", "image")
	if err != nil {
		return nil, err
	}
	return ParseSystemImages(output), nil
}

// Status reports version, uptime and model in two round trips.
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

// RunningConfig returns the config as the device's own pretty-printed JSON
// tree: VyOS has no text `show running-config` over the API.
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
