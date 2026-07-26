// Package device provides transports to network devices, split into a
// read surface (the default) and a separate, opt-in write surface.
//
// Each Reader has one request primitive that rejects writes before a request
// is built: eosCommandAllowed admits only `enable` and `show ...`,
// vyosRequestAllowed only show/showConfig on `show`/`retrieve`. Both guards
// are unconditional — Options.AllowWrites does not touch them, so adding a
// write verb to a Reader means deleting a guard. Writers (writer.go,
// eos_writer.go) are separate types with their own constructors and
// primitives; no reader constructor returns one, and they refuse unless
// Options.AllowWrites was set. See ../CONTRACT.md; the devicetype dispatch in
// ../vendor cannot relax any of this.
package device

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"
)

// Status is the small fleet-table summary every vendor can report.
type Status struct {
	Version string
	Uptime  string
	Model   string
}

// Reader is a read-only view; implementations must never mutate a device.
type Reader interface {
	Status(ctx context.Context) (Status, error)
	// RunningConfig opens no config session.
	RunningConfig(ctx context.Context) (string, error)
}

// Secrets resolves per-host secrets: Vault kv `cluster-secrets`, path
// `host-<hostname>`.
type Secrets interface {
	HostSecret(hostname, key string) (string, error)
}

// GlobalSecrets resolves shared secrets from the default kv mount, reading
// each path's `secret` key.
type GlobalSecrets interface {
	GlobalSecret(name string) (string, error)
}

// DefaultDomain is appended to a hostname to form the first candidate.
const DefaultDomain = "generalprogramming.org"

// DefaultPort is the HTTPS API port both vendors answer on.
const DefaultPort = 443

// MaxProbes bounds concurrent device contacts; do not declare your own.
const MaxProbes = 8

// Per-operation timeouts, not per client: cutting a commit at the 10s read
// budget aborts the request while the device commits anyway, so the caller
// reports failure and skips the save, leaving config that dies on reboot.
const (
	TimeoutShow      = 10 * time.Second  // one operational `show` read
	TimeoutRetrieve  = 30 * time.Second  // a running-config retrieval
	TimeoutConfigure = 120 * time.Second // one atomic commit
	TimeoutSave      = 60 * time.Second  // running config -> boot config
)

// Options configures a Reader. The zero value verifies TLS (which
// self-signed fleet devices fail), probes port 443 and uses the budgets above.
type Options struct {
	Secrets       Secrets       // per-host: EOS admin/enable password
	GlobalSecrets GlobalSecrets // shared: the VyOS API key

	// InsecureSkipVerify stays an explicit opt-in, never a silent default.
	InsecureSkipVerify bool

	Endpoint string // pins the address, skipping endpoint probing
	Port     int    // probe and request port; 0 means DefaultPort
	Domain   string // FQDN search domain; "" means DefaultDomain

	// Timeout replaces every per-operation budget with one value; 0 keeps them.
	// A context deadline, so a slow commit is never cut by a read-sized budget.
	Timeout time.Duration

	// HTTPClient overrides the constructed client; InsecureSkipVerify does not
	// apply to it, but operation timeouts still do.
	HTTPClient *http.Client

	// AllowWrites opts in to constructing a Writer. It has NO effect on any
	// Reader: those guards are unconditional and this cannot loosen them.
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

func (o Options) opTimeout(fallback time.Duration) time.Duration {
	if o.Timeout != 0 {
		return o.Timeout
	}
	if fallback <= 0 {
		return TimeoutShow
	}
	return fallback
}

// withOpTimeout applies the operation's budget; a shorter caller deadline wins.
func (o Options) withOpTimeout(ctx context.Context, fallback time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, o.opTimeout(fallback))
}

func (o Options) httpClient() *http.Client {
	if o.HTTPClient != nil {
		return o.HTTPClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if o.InsecureSkipVerify {
		// Python uses ssl._create_unverified_context() here.
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- opt-in, see Options.InsecureSkipVerify
	}
	// No client-wide Timeout: it would bound reads and commits at one value.
	return &http.Client{Transport: transport}
}
