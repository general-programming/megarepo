package lifecycle

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/vyoswire"
)

// The VyOS HTTPS API client used by the lifecycle commands. Image deletion
// (`/image`, op "delete") is in neither device allowlist, so it lives here
// behind APIOptions.AllowWrites — a third, independent, equally closed guard.

// apiEndpoint names one endpoint plus whether reaching it can change the
// device.
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
	// Address is the already-probed address (IPv6 literals are bracketed).
	Address string
	// Key is the VyOS API key (Vault `vyos-api-password`). Never logged.
	Key string
	// Port is the API port; 0 means device.DefaultPort.
	Port int
	// Timeout bounds one request; 0 means 30s.
	Timeout time.Duration
	// InsecureSkipVerify accepts the device's self-signed certificate, as
	// Python's ssl._create_unverified_context does.
	InsecureSkipVerify bool
	HTTPClient         *http.Client
	// BaseURL overrides the scheme+authority entirely (tests, httptest).
	BaseURL string

	// AllowWrites permits the write endpoints; without it DeleteImage refuses
	// before any request is built.
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
		port = device.DefaultPort
	}
	return fmt.Sprintf("https://%s:%d", device.HostForURL(c.opts.Address), port)
}

// request is the ONLY function here that performs an API request. Write
// endpoints are refused unless AllowWrites is set, so a dry run cannot reach
// one even through a caller bug; vyoswire holds no allowlist of its own.
func (c *APIClient) request(ctx context.Context, endpoint apiEndpoint, payload any) (json.RawMessage, error) {
	if endpoint.write && !c.opts.AllowWrites {
		return nil, fmt.Errorf("%s: /%s: %w", c.hostname, endpoint.name, ErrWritesNotAllowed)
	}
	return vyoswire.Post(ctx, c.client, c.hostname,
		c.baseURL()+"/"+endpoint.name, c.opts.Key, payload)
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
	return vyoswire.Text(data), nil
}

// SystemImage is an alias, so device.SystemImage is the same type.
type SystemImage = device.SystemImage

// SystemImages lists the installed system images. Read-only.
func (c *APIClient) SystemImages(ctx context.Context) ([]SystemImage, error) {
	output, err := c.Show(ctx, "system", "image")
	if err != nil {
		return nil, err
	}
	return device.ParseSystemImages(output), nil
}

// DeleteImage removes an installed image. THIS IS A WRITE: it requires
// APIOptions.AllowWrites.
func (c *APIClient) DeleteImage(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("%s: refusing to delete an unnamed image", c.hostname)
	}
	_, err := c.request(ctx, endpointImageDelete, map[string]any{"op": "delete", "name": name})
	return err
}

// Version is the running VyOS version, or "" for a placeholder. Read-only.
func (c *APIClient) Version(ctx context.Context) (string, error) {
	output, err := c.Show(ctx, "version")
	if err != nil {
		return "", err
	}
	return device.ParseVyOSVersion(output), nil
}

// VerifyRouting warns when BGP is still administratively down after a reboot,
// meaning the drain's shutdown got saved; "" means healthy.
func (c *APIClient) VerifyRouting(ctx context.Context, hasASN bool) string {
	if !hasASN {
		return ""
	}
	summary, err := c.Show(ctx, "bgp", "summary")
	if err != nil {
		// Best-effort: failing to check is a warning, not a failed update.
		return fmt.Sprintf("could not check BGP summary: %v", err)
	}
	if strings.Contains(summary, "Idle (Admin)") {
		return "BGP still administratively down after reboot; the shutdown may have been saved"
	}
	return ""
}
