// Package netbox is a small read-only client for the NetBox GraphQL API. It
// only ever issues *queries*; no mutation helpers may be added. Two gotchas:
// NetBox >= 4.5 v2 tokens must be sent as `Authorization: Bearer <token>`, not
// the legacy `Token <token>`; and the instance sits behind Cloudflare, which
// blocks Go's default User-Agent with error 1010, so a custom one is required.
package netbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultEndpoint is the megarepo NetBox GraphQL endpoint.
const DefaultEndpoint = "https://netbox.generalprogramming.org/graphql/"

// DefaultUserAgent is used when a caller sets none; it may never be empty.
const DefaultUserAgent = "megarepo-go-netbox (github.com/general-programming/megarepo)"

// DefaultTimeout bounds a single GraphQL round trip.
const DefaultTimeout = 60 * time.Second

// Options configures a Client. Only Token is required.
type Options struct {
	Endpoint   string        // full GraphQL URL; empty means DefaultEndpoint
	Token      string        // API token, sent as a Bearer credential
	UserAgent  string        // must be non-empty if set
	Timeout    time.Duration // ignored when HTTPClient is supplied
	HTTPClient *http.Client  // its own Timeout is respected as-is
}

// Client is a read-only NetBox GraphQL client, safe for concurrent use.
type Client struct {
	endpoint  string
	token     string
	userAgent string
	http      *http.Client
}

// New builds a Client. It fails without a token; everything else defaults.
func New(opts Options) (*Client, error) {
	if strings.TrimSpace(opts.Token) == "" {
		return nil, fmt.Errorf("netbox: an API token is required")
	}

	endpoint := opts.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}

	ua := opts.UserAgent
	if ua == "" {
		ua = DefaultUserAgent
	}

	hc := opts.HTTPClient
	if hc == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		hc = &http.Client{Timeout: timeout}
	}

	return &Client{
		endpoint:  endpoint,
		token:     opts.Token,
		userAgent: ua,
		http:      hc,
	}, nil
}

// NewFromEnv builds a Client from NETBOX_API_KEY (required) and NETBOX_URL,
// as the Python tooling does; fields set on opts win.
func NewFromEnv(opts Options) (*Client, error) {
	if opts.Token == "" {
		opts.Token = os.Getenv("NETBOX_API_KEY")
	}
	if opts.Token == "" {
		return nil, fmt.Errorf("netbox: NETBOX_API_KEY is not set")
	}
	if opts.Endpoint == "" {
		opts.Endpoint = os.Getenv("NETBOX_URL")
	}
	return New(opts)
}

// Endpoint reports the GraphQL URL the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// GraphQLError is a single entry of a GraphQL `errors` array.
type GraphQLError struct {
	Message string           `json:"message"`
	Path    []any            `json:"path,omitempty"`
	Extra   map[string]any   `json:"extensions,omitempty"`
	Locs    []map[string]int `json:"locations,omitempty"`
}

func (e GraphQLError) Error() string { return e.Message }

// GraphQLErrors is the whole `errors` array. Use errors.As to inspect it.
type GraphQLErrors []GraphQLError

func (e GraphQLErrors) Error() string {
	msgs := make([]string, 0, len(e))
	for _, err := range e {
		msgs = append(msgs, err.Message)
	}
	return "netbox: GraphQL errors: " + strings.Join(msgs, "; ")
}

// HTTPError is a non-2xx from NetBox or from what sits in front of it (e.g. a
// Cloudflare 1010 block page).
type HTTPError struct {
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPError) Error() string {
	body := e.Body
	const max = 512
	if len(body) > max {
		body = body[:max] + "..."
	}
	return fmt.Sprintf("netbox: HTTP %s: %s", e.Status, body)
}

// Query runs a raw GraphQL query and unmarshals `data` into out; exported for
// callers with their own documents, read-only by contract.
func (c *Client) Query(ctx context.Context, query string, variables map[string]any, out any) error {
	payload := map[string]any{"query": query}
	if len(variables) > 0 {
		payload["variables"] = variables
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("netbox: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("netbox: building request: %w", err)
	}
	// v2 tokens require Bearer; legacy "Token" auth is not used.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// Mandatory: Cloudflare error 1010 blocks default agents.
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("netbox: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("netbox: reading response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Status: resp.Status, Body: string(raw)}
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors GraphQLErrors   `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("netbox: decoding response: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return envelope.Errors
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("netbox: response contained no data")
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("netbox: decoding data: %w", err)
	}
	return nil
}
