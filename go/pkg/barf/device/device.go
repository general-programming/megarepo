// Package device provides READ-ONLY transports to network devices.
//
// Every exported entry point in this package maps to a device *read*:
// `show ...` operational commands and config *retrieval*. There is
// deliberately no configuration path — no eAPI `config()`/`runConfigCmds`,
// no VyOS `/configure` or `/config-file`, no `copy running-config
// startup-config`, no NETCONF edit-config. The transports enforce this
// structurally rather than by convention:
//
//   - the eAPI client has exactly one request primitive, and it rejects
//     any command that is not `enable` or a `show ...` verb before the
//     request is built (see eosCommandAllowed);
//   - the VyOS client has exactly one request primitive, and it rejects
//     any endpoint other than `show` and `retrieve`, and any op other
//     than `show`/`showConfig` (see vyosRequestAllowed).
//
// Both guards are unconditional and have no bypass flag. A caller cannot
// reach a write verb through this package's API, and a future edit that
// tries to add one has to delete a guard to do it.
//
// See ../CONTRACT.md; this package is the Go port of the read surfaces of
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
const DefaultPort = 443

// Options configures a Reader. The zero value is usable: it verifies TLS
// (which fleet devices, running self-signed certs, will fail — set
// InsecureSkipVerify explicitly for those), probes port 443 and uses a
// 10s timeout.
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

	// Timeout bounds a single request and each probe attempt. 0 means 10s.
	Timeout time.Duration

	// HTTPClient overrides the constructed client (tests, custom
	// transports). When set, InsecureSkipVerify and Timeout do not apply
	// to it.
	HTTPClient *http.Client
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

func (o Options) timeout() time.Duration {
	if o.Timeout != 0 {
		return o.Timeout
	}
	return 10 * time.Second
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
	return &http.Client{Transport: transport, Timeout: o.timeout()}
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

// ErrUnsupported is returned for devicetypes with no read transport.
var ErrUnsupported = errUnsupported{}

type errUnsupported struct{}

func (errUnsupported) Error() string { return "unsupported devicetype" }

// ErrWriteAttempt is returned when a command or request that could change
// the device is passed to a transport. It is the package's structural
// guarantee: callers cannot talk a Reader into writing.
type ErrWriteAttempt struct {
	What string
}

func (e *ErrWriteAttempt) Error() string {
	return fmt.Sprintf("device: refusing %s: this package is read-only", e.What)
}
