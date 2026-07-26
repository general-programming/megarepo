package device

// ############################################################
// #                                                          #
// #   THIS FILE CAN CHANGE A LIVE ROUTER. Everything else     #
// #   in package device is read-only; this is the exception. #
// #                                                          #
// ############################################################
//
// It is the port of exactly two Python functions —
// projects/barf/barf/util/vyos_api.py `vyos_api_configure` and
// `vyos_api_config_save` — driven the way `VyOSHost.push_rendered_config`
// drives them: one atomic /configure call (deletes first, then sets),
// then /config-file save. `vyos_api_image_delete` is deliberately NOT
// ported.
//
// Three properties are load-bearing and must survive any edit here:
//
//  1. The read guard in vyos.go is untouched. VyOSReader still cannot
//     reach /configure; this file has its own separate primitive.
//  2. A Writer cannot be built by accident. NewVyOSWriter fails unless
//     Options.AllowWrites is explicitly true, and New() never returns
//     one.
//  3. The writer's own primitive is allowlisted too, to `configure` and
//     `config-file`+save only — so /image (delete) stays unreachable
//     from here as well.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// -- shared write surface ---------------------------------------------
//
// Op, Writer and ErrWritesNotAllowed are the cross-vendor write surface
// and are declared in device/eos_writer.go; this file reuses them rather
// than redeclaring them. VyOS reads Op.Verb and Op.Path.

// The valid VyOS op verbs. Anything else is rejected before a request is
// built.
const (
	OpSet    = "set"
	OpDelete = "delete"
)

// VyOSOps turns config paths into Ops carrying verb.
func VyOSOps(verb string, paths [][]string) []Op {
	ops := make([]Op, 0, len(paths))
	for _, path := range paths {
		ops = append(ops, Op{Verb: verb, Path: path})
	}
	return ops
}

// vyosWireOp is the exact JSON the Python implementation sends to
// /configure: {"op": "set"|"delete", "path": [...]}. Op itself carries
// EOS fields too, so the payload is built explicitly rather than by
// marshaling Op.
type vyosWireOp struct {
	Op   string   `json:"op"`
	Path []string `json:"path"`
}

// -- VyOS writer ------------------------------------------------------

// The only two VyOS API endpoints the writer may talk to, and the only
// ops each accepts. `/image` (delete) is absent: vyos_api_image_delete is
// not ported. `/configure` takes a list rather than a single op, so its
// per-op verbs are checked separately (see vyosWriteOpAllowed).
var vyosWriteAllowedRequests = map[string]map[string]bool{
	"configure":   {"": true},
	"config-file": {"save": true},
}

func vyosWriteRequestAllowed(endpoint, op string) bool {
	return vyosWriteAllowedRequests[endpoint][op]
}

func vyosWriteOpAllowed(op string) bool {
	return op == OpSet || op == OpDelete
}

// vyosWriteTimeout is the per-endpoint budget for a write, matching the
// Python defaults (vyos_api_configure=120s, vyos_api_config_save=60s).
// A commit is emphatically not a read: bounding it with the 10s read
// budget aborted the HTTP request while the router went on to apply the
// config, so the deploy reported failure and skipped the save.
func vyosWriteTimeout(endpoint string) time.Duration {
	if endpoint == "configure" {
		return TimeoutConfigure
	}
	return TimeoutSave
}

// VyOSWriter applies configuration to a VyOS device over its HTTPS API.
//
// Construct it with NewVyOSWriter, which requires Options.AllowWrites.
type VyOSWriter struct {
	host     *model.Host
	opts     Options
	client   *http.Client
	resolver *endpointResolver

	keyOnce sync.Once
	key     string
	keyErr  error
}

var _ Writer = (*VyOSWriter)(nil)

// NewVyOSWriter returns a config writer for h.
//
// It returns ErrWritesNotAllowed unless opts.AllowWrites is true. That
// check is the whole point of the constructor: a caller that has not
// thought about writing to a router cannot end up with one of these.
func NewVyOSWriter(h *model.Host, opts Options) (*VyOSWriter, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	if !opts.AllowWrites {
		return nil, &ErrWritesNotAllowed{What: "a VyOS config writer for " + h.Hostname}
	}
	if !strings.EqualFold(h.DeviceType, "vyos") {
		return nil, fmt.Errorf("device: %s: %w: NewVyOSWriter called for devicetype %q",
			h.Hostname, ErrUnsupported, h.DeviceType)
	}
	if opts.GlobalSecrets == nil {
		return nil, fmt.Errorf("device: %s: vyos needs Options.GlobalSecrets for the %s key",
			h.Hostname, VyOSAPIKeySecret)
	}
	return &VyOSWriter{
		host:     h,
		opts:     opts,
		client:   opts.httpClient(),
		resolver: &endpointResolver{host: h, opts: opts},
	}, nil
}

func (w *VyOSWriter) apiKey() (string, error) {
	w.keyOnce.Do(func() {
		w.key, w.keyErr = w.opts.GlobalSecrets.GlobalSecret(VyOSAPIKeySecret)
	})
	return w.key, w.keyErr
}

// request is the ONLY function in this file that performs a request. It
// mirrors VyOSReader.request (form-POST of `data`+`key`, {success, data,
// error} reply) but carries the writer's own, equally closed, endpoint
// allowlist. It is separate on purpose: the read primitive's guard stays
// untouched and unbypassable.
func (w *VyOSWriter) request(ctx context.Context, endpoint, op string, payload any) (string, error) {
	if !vyosWriteRequestAllowed(endpoint, op) {
		return "", &ErrWriteAttempt{What: fmt.Sprintf("VyOS %s request op=%q", endpoint, op)}
	}

	ctx, cancel := w.opts.withOpTimeout(ctx, vyosWriteTimeout(endpoint))
	defer cancel()

	key, err := w.apiKey()
	if err != nil {
		return "", fmt.Errorf("%s: %w", w.host.Hostname, err)
	}
	address, err := w.resolver.resolve(ctx)
	if err != nil {
		return "", fmt.Errorf("%s: no reachable address: %w", w.host.Hostname, err)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	form := url.Values{"data": {string(data)}, "key": {key}}

	target := fmt.Sprintf("https://%s:%d/%s", hostForURL(address), w.opts.port(), endpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := w.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s: vyos api request failed: %w", w.host.Hostname, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", err
	}

	var decoded vyosResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("%s: malformed vyos api response (HTTP %d): %w",
			w.host.Hostname, resp.StatusCode, err)
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
		return "", fmt.Errorf("%s: vyos api: %s", w.host.Hostname, message)
	}

	if len(decoded.Data) == 0 {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(decoded.Data, &text); err != nil {
		return string(decoded.Data), nil
	}
	return text, nil
}

// Configure applies ops via `/configure`, which commits them atomically:
// the whole list is one commit and any failing op rolls back all of them.
// Ports vyos_api_configure.
//
// An empty op list is a no-op that never reaches the wire — a deploy with
// nothing to do must not open a commit.
func (w *VyOSWriter) Configure(ctx context.Context, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	wire := make([]vyosWireOp, 0, len(ops))
	for i, op := range ops {
		if !vyosWriteOpAllowed(op.Verb) {
			return &ErrWriteAttempt{What: fmt.Sprintf("VyOS configure op[%d] verb %q", i, op.Verb)}
		}
		if len(op.Path) == 0 {
			// A path-less delete would target the whole config root.
			return fmt.Errorf("%s: refusing %s op with an empty path", w.host.Hostname, op.Verb)
		}
		if op.Command != "" {
			// An EOS-shaped op reaching the VyOS writer is a wiring bug,
			// not something to silently drop.
			return fmt.Errorf("%s: refusing op[%d]: EOS Command set on a VyOS op", w.host.Hostname, i)
		}
		wire = append(wire, vyosWireOp{Op: op.Verb, Path: op.Path})
	}
	// Serialized as a bare JSON array, exactly as Python sends it.
	_, err := w.request(ctx, "configure", "", wire)
	return err
}

// SaveConfig persists the running config to the boot config via
// `/config-file`. Ports vyos_api_config_save.
func (w *VyOSWriter) SaveConfig(ctx context.Context) error {
	_, err := w.request(ctx, "config-file", "save", map[string]any{"op": "save"})
	return err
}
