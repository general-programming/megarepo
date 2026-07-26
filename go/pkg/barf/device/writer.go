package device

// ############################################################
// #                                                          #
// #   THIS FILE CAN CHANGE A LIVE ROUTER. Everything else     #
// #   in package device is read-only; this is the exception. #
// #                                                          #
// ############################################################
//
// Port of vyos_api_configure and vyos_api_config_save, driven as
// VyOSHost.push_rendered_config drives them: one atomic /configure call
// (deletes first, then sets), then /config-file save. Image delete is NOT
// ported.
//
// Three load-bearing properties any edit here must preserve:
//
//  1. vyos.go's read guard is untouched — this file has its own separate
//     primitive, so VyOSReader still cannot reach /configure.
//  2. NewVyOSWriter fails unless Options.AllowWrites is explicitly true,
//     and no reader constructor returns a Writer.
//  3. This file's primitive is allowlisted too, to `configure` and
//     `config-file`+save, so /image (delete) is unreachable here.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/vyoswire"
)

// Op and Writer are declared in eos_writer.go; VyOS reads Op.Verb/Op.Path.

// The valid op verbs; anything else is rejected before a request is built.
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

// vyosWireOp is the exact JSON Python sends to /configure; Op carries EOS
// fields too, so it is not marshaled directly.
type vyosWireOp struct {
	Op   string   `json:"op"`
	Path []string `json:"path"`
}

// The only endpoints the writer may talk to; `/image` (delete) is absent.
// `/configure` takes a list, so vyosWriteOpAllowed checks its per-op verbs.
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

// vyosWriteTimeout is the per-endpoint write budget (configure=120s,
// config_save=60s): the 10s read budget would abort the request while the
// router commits anyway, so the deploy would report failure and skip the save.
func vyosWriteTimeout(endpoint string) time.Duration {
	if endpoint == "configure" {
		return TimeoutConfigure
	}
	return TimeoutSave
}

// VyOSWriter applies configuration to a VyOS device over its HTTPS API.
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

// NewVyOSWriter returns a config writer for h, refusing with
// ErrWritesNotAllowed unless opts.AllowWrites is true.
func NewVyOSWriter(h *model.Host, opts Options) (*VyOSWriter, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	if !opts.AllowWrites {
		return nil, &WritesNotAllowedError{What: "a VyOS config writer for " + h.Hostname}
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

// request is the ONLY function here that performs a request. It shares the
// wire mechanics with VyOSReader.request via vyoswire but carries its own,
// equally closed, allowlist — vyoswire deliberately holds none, since this
// guard is all that stands between a caller and /configure.
func (w *VyOSWriter) request(ctx context.Context, endpoint, op string, payload any) (string, error) {
	if !vyosWriteRequestAllowed(endpoint, op) {
		return "", &WriteAttemptError{What: fmt.Sprintf("VyOS %s request op=%q", endpoint, op)}
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

	target := fmt.Sprintf("https://%s:%d/%s", HostForURL(address), w.opts.port(), endpoint)
	data, err := vyoswire.Post(ctx, w.client, w.host.Hostname, target, key, payload)
	if err != nil {
		return "", err
	}
	return vyoswire.Text(data), nil
}

// Configure applies ops via `/configure`, one atomic commit where a failing op
// rolls back all of them. An empty list never reaches the wire: a deploy with
// nothing to do must not open a commit.
func (w *VyOSWriter) Configure(ctx context.Context, ops []Op) error {
	if len(ops) == 0 {
		return nil
	}
	wire := make([]vyosWireOp, 0, len(ops))
	for i, op := range ops {
		if !vyosWriteOpAllowed(op.Verb) {
			return &WriteAttemptError{What: fmt.Sprintf("VyOS configure op[%d] verb %q", i, op.Verb)}
		}
		if len(op.Path) == 0 {
			// A path-less delete would target the whole config root.
			return fmt.Errorf("%s: refusing %s op with an empty path", w.host.Hostname, op.Verb)
		}
		if op.Command != "" {
			// An EOS-shaped op here is a wiring bug, not something to drop.
			return fmt.Errorf("%s: refusing op[%d]: EOS Command set on a VyOS op", w.host.Hostname, i)
		}
		wire = append(wire, vyosWireOp{Op: op.Verb, Path: op.Path})
	}
	// Serialized as a bare JSON array, exactly as Python sends it.
	_, err := w.request(ctx, "configure", "", wire)
	return err
}

// SaveConfig persists the running config to the boot config.
func (w *VyOSWriter) SaveConfig(ctx context.Context) error {
	_, err := w.request(ctx, "config-file", "save", map[string]any{"op": "save"})
	return err
}
