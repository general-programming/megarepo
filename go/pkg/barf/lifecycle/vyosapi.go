package lifecycle

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// The VyOS HTTPS API client used by the lifecycle commands.
//
// go/pkg/barf/device deliberately has no write path at all — its VyOS
// transport rejects every endpoint but /show and /retrieve, on purpose,
// and that guard must stay. Image deletion (`/image`, op "delete") is a
// write, so it lives here instead, behind Options.AllowWrites.
//
// The read halves (Show, SystemImages, version/BGP parsing) are
// re-implemented rather than imported so this package depends on nothing
// another worker is currently editing. They are small and pure; if the
// duplication outlives the port, fold them back into device and have
// this file embed a device.VyOSReader for reads.

// APIEndpoint names one VyOS API endpoint plus whether reaching it can
// change the device.
type apiEndpoint struct {
	name  string
	write bool
}

var (
	endpointShow        = apiEndpoint{name: "show"}
	endpointImageDelete = apiEndpoint{name: "image", write: true}
)

// APIOptions configures an APIClient.
type APIOptions struct {
	// Address is the already-probed address to talk to (an IPv6 literal
	// is bracketed automatically).
	Address string
	// Key is the VyOS API key (Vault `vyos-api-password`). Never logged.
	Key string
	// Port is the API port; 0 means 443.
	Port int
	// Timeout bounds one request; 0 means 30s.
	Timeout time.Duration
	// InsecureSkipVerify accepts the device's self-signed certificate,
	// as the Python implementation does.
	InsecureSkipVerify bool
	// HTTPClient overrides the constructed client (tests).
	HTTPClient *http.Client
	// BaseURL overrides the scheme+authority entirely (tests, httptest).
	BaseURL string

	// AllowWrites permits the write endpoints. Without it DeleteImage
	// refuses before any request is built.
	AllowWrites bool
}

// APIClient talks to one device's VyOS HTTPS API.
type APIClient struct {
	hostname string
	opts     APIOptions
	client   *http.Client
}

// NewAPIClient returns a client for hostname at opts.Address.
func NewAPIClient(hostname string, opts APIOptions) (*APIClient, error) {
	if opts.Address == "" && opts.BaseURL == "" {
		return nil, fmt.Errorf("lifecycle: %s: no address for the VyOS API", hostname)
	}
	client := opts.HTTPClient
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if opts.InsecureSkipVerify {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- opt-in; devices serve the self-signed default profile
		}
		client = &http.Client{Transport: transport, Timeout: apiTimeout(opts)}
	}
	return &APIClient{hostname: hostname, opts: opts, client: client}, nil
}

func apiTimeout(opts APIOptions) time.Duration {
	if opts.Timeout > 0 {
		return opts.Timeout
	}
	return 30 * time.Second
}

// Hostname is the device this client talks to.
func (c *APIClient) Hostname() string { return c.hostname }

func (c *APIClient) baseURL() string {
	if c.opts.BaseURL != "" {
		return strings.TrimSuffix(c.opts.BaseURL, "/")
	}
	port := c.opts.Port
	if port == 0 {
		port = 443
	}
	return fmt.Sprintf("https://%s:%d", hostForURL(c.opts.Address), port)
}

// hostForURL wraps a bare IPv6 literal in brackets.
func hostForURL(address string) string {
	if ip, err := netip.ParseAddr(address); err == nil && ip.Is6() && !ip.Is4In6() {
		return "[" + address + "]"
	}
	return address
}

type apiResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   any             `json:"error"`
}

// request is the ONLY function here that performs an API request. Write
// endpoints are refused outright unless AllowWrites is set, so a dry run
// cannot reach one even through a bug in a caller.
func (c *APIClient) request(ctx context.Context, endpoint apiEndpoint, payload any) (json.RawMessage, error) {
	if endpoint.write && !c.opts.AllowWrites {
		return nil, fmt.Errorf("%s: /%s: %w", c.hostname, endpoint.name, ErrWritesNotAllowed)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	form := url.Values{"data": {string(body)}, "key": {c.opts.Key}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL()+"/"+endpoint.name, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: vyos api request failed: %w", c.hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}

	var decoded apiResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: malformed vyos api response (HTTP %d): %w",
			c.hostname, resp.StatusCode, err)
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
		return nil, fmt.Errorf("%s: vyos api: %s", c.hostname, message)
	}
	return decoded.Data, nil
}

// Show runs an operational `show` command. Read-only.
func (c *APIClient) Show(ctx context.Context, path ...string) (string, error) {
	if path == nil {
		path = []string{}
	}
	data, err := c.request(ctx, endpointShow, map[string]any{"op": "show", "path": path})
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return string(data), nil
	}
	return text, nil
}

// SystemImage is one installed VyOS system image.
type SystemImage struct {
	Name        string
	DefaultBoot bool
	Running     bool
}

// SystemImages lists the installed system images, per `show system
// image`. Read-only. Ports VyOSHost.system_images.
func (c *APIClient) SystemImages(ctx context.Context) ([]SystemImage, error) {
	output, err := c.Show(ctx, "system", "image")
	if err != nil {
		return nil, err
	}
	return ParseSystemImages(output), nil
}

// DeleteImage removes an installed system image. THIS IS A WRITE: it
// requires Options/APIOptions AllowWrites. Ports
// VyOSHost._api_image_delete.
func (c *APIClient) DeleteImage(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("%s: refusing to delete an unnamed image", c.hostname)
	}
	_, err := c.request(ctx, endpointImageDelete, map[string]any{"op": "delete", "name": name})
	return err
}

// Version is the running VyOS version, or "" when the device reports a
// placeholder. Read-only.
func (c *APIClient) Version(ctx context.Context) (string, error) {
	output, err := c.Show(ctx, "version")
	if err != nil {
		return "", err
	}
	return ParseVersion(output), nil
}

// VerifyRouting warns when BGP is still administratively down after a
// reboot, which means the drain's shutdown got saved. It returns a
// human-readable warning, or "" when healthy. Ports
// VyOSHost.verify_routing. Read-only.
//
// hasASN mirrors the Python `if not self.asn: return None` guard.
func (c *APIClient) VerifyRouting(ctx context.Context, hasASN bool) string {
	if !hasASN {
		return ""
	}
	summary, err := c.Show(ctx, "bgp", "summary")
	if err != nil {
		// Verification is best-effort: a failure to check is a warning,
		// not a reason to call the update failed.
		return fmt.Sprintf("could not check BGP summary: %v", err)
	}
	if strings.Contains(summary, "Idle (Admin)") {
		return "BGP still administratively down after reboot; the shutdown may have been saved"
	}
	return ""
}

// -- parsing ----------------------------------------------------------

// ParseVersion pulls the version out of `show version` output, stripping
// the "VyOS " prefix so it lines up with upstream release tags.
func ParseVersion(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "version:") {
			_, value, _ := strings.Cut(line, ":")
			return strings.TrimPrefix(strings.TrimSpace(value), "VyOS ")
		}
	}
	if strings.TrimSpace(output) == "" {
		return "-"
	}
	first := strings.SplitN(strings.TrimSpace(output), "\n", 2)[0]
	return strings.TrimPrefix(first, "VyOS ")
}

var numberedImage = regexp.MustCompile(`^\s*\d+:\s+(\S+)(.*)$`)

// ParseSystemImages parses `show system image` output, handling both the
// modern table format and the legacy numbered list.
func ParseSystemImages(output string) []SystemImage {
	lines := strings.Split(output, "\n")

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
		match := numberedImage.FindStringSubmatch(line)
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

func columnYes(row string, start, end int) bool {
	if start >= len(row) {
		return false
	}
	if end > len(row) {
		end = len(row)
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(row[start:end])), "yes")
}
