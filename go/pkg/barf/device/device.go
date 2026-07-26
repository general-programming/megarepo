// Package device provides transports to network devices, split into a
// read surface (the default) and a separate, opt-in write surface.
//
// # Readers are read-only, structurally
//
// Every entry point reachable from a Reader maps to a device *read*:
// `show ...` operational commands and config *retrieval*. Readers have no
// configuration path — no eAPI `config()`/`runConfigCmds`, no VyOS
// `/configure` or `/config-file`, no `copy running-config
// startup-config`, no NETCONF edit-config. The read transports enforce
// this structurally rather than by convention:
//
//   - the eAPI client has exactly one request primitive, and it rejects
//     any command that is not `enable` or a `show ...` verb before the
//     request is built (see eosCommandAllowed);
//   - the VyOS client has exactly one request primitive, and it rejects
//     any endpoint other than `show` and `retrieve`, and any op other
//     than `show`/`showConfig` (see vyosRequestAllowed).
//
// Both guards are unconditional and have no bypass flag — Options.
// AllowWrites does not touch them. A caller cannot reach a write verb
// through a Reader, and a future edit that tries to add one has to delete
// a guard to do it.
//
// # Writers are separate and must be asked for by name
//
// writer.go adds the Writer interface and VyOSWriter, the only type in
// this package that can change a device. It is a different type with a
// different constructor and its own request primitive; it is never
// returned by New, and NewVyOSWriter errors out unless
// Options.AllowWrites was explicitly set. Nothing that holds a Reader can
// turn it into a Writer.
//
// See ../CONTRACT.md; this package is the Go port of
// projects/barf/barf/vendors/{arista,vyos}.py.
package device

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Status is the small fleet-table summary every vendor can report.
type Status struct {
	Version string
	Uptime  string
	Model   string
}

// Reader is a read-only view of a device. Implementations must never
// mutate device state.
type Reader interface {
	Status(ctx context.Context) (Status, error)
	// RunningConfig returns the device's config text (or a vendor-native
	// dump of it). It is a read: no config session is opened.
	RunningConfig(ctx context.Context) (string, error)
}

// Secrets resolves per-host secrets. Mirrors the Python
// BaseHost.secret(key) lookup against Vault kv `cluster-secrets` at path
// `host-<hostname>`. Declared locally (structurally satisfied by the real
// Vault client) so this package does not depend on go/vault.
type Secrets interface {
	HostSecret(hostname, key string) (string, error)
}

// GlobalSecrets resolves shared, non-host secrets. Mirrors the Python
// `VaultSecrets().<attr>` lookup, where the attribute name is dashed and
// used as the path in the default kv mount, reading its `secret` key:
// `VaultSecrets().vyos_api_password` -> GlobalSecret("vyos-api-password").
type GlobalSecrets interface {
	GlobalSecret(name string) (string, error)
}

// DefaultDomain is the search domain appended to a hostname to form the
// first endpoint candidate.
const DefaultDomain = "generalprogramming.org"

// DefaultPort is the HTTPS API port both vendors answer on.
//
// barf/cli probes it to pick a reachable address and barf/lifecycle
// builds its API base URL from it; both used to spell it 443 themselves.
const DefaultPort = 443

// MaxProbes is how many devices barf contacts concurrently, matching the
// Python ThreadPoolExecutor(max_workers=8) that backs safe_to_reboot and
// the status fan-out.
//
// It lives here because it is a property of talking to devices, and
// because barf/cli and barf/lifecycle both bound their fan-out by it and
// had each declared their own 8. Two independently tunable copies of one
// concurrency limit is how a fleet-wide run quietly ends up at 16.
const MaxProbes = 8

// Per-operation timeouts, mirroring the Python defaults in
// projects/barf/barf/util/vyos_api.py. They are per *operation*, not
// per client, because a commit is not a read: `/configure` on a busy
// router routinely takes tens of seconds, and bounding it with the
// 10s read budget aborted the HTTP request while the device went on to
// apply and commit the config anyway. The caller then saw "deploy
// failed" and skipped the save, leaving a router running config that
// would not survive a reboot.
//
// Python: vyos_api_show=10s, vyos_api_retrieve_config=30s,
// vyos_api_configure=120s, vyos_api_config_save=60s.
const (
	// TimeoutShow bounds an operational `show` read.
	TimeoutShow = 10 * time.Second
	// TimeoutRetrieve bounds a running-config retrieval.
	TimeoutRetrieve = 30 * time.Second
	// TimeoutConfigure bounds one atomic commit.
	TimeoutConfigure = 120 * time.Second
	// TimeoutSave bounds persisting running config to boot config.
	TimeoutSave = 60 * time.Second
)

// Options configures a Reader. The zero value is usable: it verifies TLS
// (which fleet devices, running self-signed certs, will fail — set
// InsecureSkipVerify explicitly for those), probes port 443 and gives
// each operation its own timeout (see TimeoutShow and friends).
type Options struct {
	// Secrets resolves per-host credentials (EOS admin/enable password).
	Secrets Secrets
	// GlobalSecrets resolves shared credentials (the VyOS API key).
	GlobalSecrets GlobalSecrets

	// InsecureSkipVerify disables TLS certificate verification. Fleet
	// devices ship self-signed certs, so this is normally on — but it is
	// an explicit, named opt-in, never a silent default.
	InsecureSkipVerify bool

	// Endpoint pins the address to talk to, skipping endpoint probing.
	Endpoint string
	// Port is the API port (probe target and request port). 0 means 443.
	Port int
	// Domain is the search domain for the FQDN candidate. "" means
	// DefaultDomain.
	Domain string

	// Timeout overrides the per-operation timeouts with one value for
	// every request, read or write. 0 (the default) means each operation
	// uses its own budget: TimeoutShow, TimeoutRetrieve,
	// TimeoutConfigure, TimeoutSave.
	//
	// It is applied as a per-request context deadline rather than
	// http.Client.Timeout so that a slow commit cannot be cut short by a
	// budget sized for a read.
	Timeout time.Duration

	// HTTPClient overrides the constructed client (tests, custom
	// transports). When set, InsecureSkipVerify does not apply to it;
	// operation timeouts still do, since they are context deadlines.
	HTTPClient *http.Client

	// AllowWrites opts in to constructing a Writer (see writer.go). It
	// has NO effect on any Reader: the read transports' guards are
	// unconditional and this flag cannot loosen them. Its only job is to
	// make a config-changing client impossible to build by accident —
	// NewVyOSWriter refuses to return one unless this is explicitly true.
	AllowWrites bool
}

func (o Options) port() int {
	if o.Port != 0 {
		return o.Port
	}
	return DefaultPort
}

func (o Options) domain() string {
	if o.Domain != "" {
		return o.Domain
	}
	return DefaultDomain
}

// opTimeout is the budget for one operation: the explicit override when
// Options.Timeout is set, otherwise the operation's own default.
func (o Options) opTimeout(fallback time.Duration) time.Duration {
	if o.Timeout != 0 {
		return o.Timeout
	}
	if fallback <= 0 {
		return TimeoutShow
	}
	return fallback
}

// withOpTimeout derives a context carrying the operation's budget. The
// caller's own (shorter) deadline still wins — context.WithTimeout never
// extends a deadline.
func (o Options) withOpTimeout(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, o.opTimeout(fallback))
}

func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if o.InsecureSkipVerify {
		// Devices run the self-signed default SSL profile; the Python
		// implementation uses ssl._create_unverified_context() here.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- opt-in, see Options.InsecureSkipVerify
	}
	// No client-wide Timeout: it would bound every request at one value,
	// and a commit needs a different budget than a read. Each request
	// primitive attaches its own context deadline instead.
	return &http.Client{Transport: transport}
}

// New returns the Reader for h's DeviceType.
//
// Supported: "eos" (Arista eAPI), "vyos" (VyOS HTTPS API). Every other
// devicetype returns ErrUnsupported — mirroring the Python
// REPORTS_STATUS flag, which is only set on those two vendors.
func New(h *model.Host, opts Options) (Reader, error) {
	if h == nil {
		return nil, fmt.Errorf("device: nil host")
	}
	switch strings.ToLower(h.DeviceType) {
	case "eos":
		return NewEOS(h, opts)
	case "vyos":
		return NewVyOS(h, opts)
	default:
		return nil, fmt.Errorf("device: %s: %w: devicetype %q does not report status",
			h.Hostname, ErrUnsupported, h.DeviceType)
	}
}

// The refusal errors and ErrUnsupported live in errors.go.
