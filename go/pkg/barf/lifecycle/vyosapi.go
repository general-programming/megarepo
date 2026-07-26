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

// The VyOS HTTPS API client used by the lifecycle commands.
//
// go/pkg/barf/device's VyOS *reader* rejects every endpoint but /show and
// /retrieve, and its *writer* accepts only /configure and /config-file.
// Image deletion (`/image`, op "delete") is in neither allowlist, so it
// lives here instead, behind APIOptions.AllowWrites — a third,
// independent, equally closed guard (see apiEndpoint.write).
//
// The read halves and the parsers were originally re-implemented here
// rather than imported, so this package would depend on nothing another
// worker was editing. That reason is gone, and the copies had already
// drifted: the parsers split on "\n" where device (and Python's
// str.splitlines) split on any line boundary, and the response body cap
// was 16 MiB here against 64 MiB in device, so a large /retrieve
// truncated on this path only. The parsers now come from device and the
// wire mechanics from vyoswire; only the /image guard is local.

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
	// Port is the API port; 0 means device.DefaultPort.
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
		port = device.DefaultPort
	}
	return fmt.Sprintf("https://%s:%d", device.HostForURL(c.opts.Address), port)
}

// request is the ONLY function here that performs an API request. Write
// endpoints are refused outright unless AllowWrites is set, so a dry run
// cannot reach one even through a bug in a caller.
//
// This guard is this package's own. vyoswire supplies the form encoding,
// the body limit and the response envelope, and holds no allowlist of
// its own, so sharing it with device's reader and writer does not let any
// of the three reach an endpoint its own guard would refuse.
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

// SystemImage is one installed VyOS system image.
//
// An alias, not a copy: cleanup.go and update.go name this type in their
// interfaces and cli/devicelifecycle.go names it too, so aliasing keeps
// those signatures while making a device.SystemImage and a
// lifecycle.SystemImage the same type rather than two structs that merely
// look alike.
type SystemImage = device.SystemImage

// SystemImages lists the installed system images, per `show system
// image`. Read-only. Ports VyOSHost.system_images.
func (c *APIClient) SystemImages(ctx context.Context) ([]SystemImage, error) {
	output, err := c.Show(ctx, "system", "image")
	if err != nil {
		return nil, err
	}
	return device.ParseSystemImages(output), nil
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
	return device.ParseVyOSVersion(output), nil
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
