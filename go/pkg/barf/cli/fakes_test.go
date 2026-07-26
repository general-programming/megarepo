package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// fakeNetwork is the network.yml every command test runs against.
func fakeNetwork() *model.Network {
	addr := func(s string) *model.Address {
		return &model.Address{IP: netip.MustParseAddr(s), Prefix: 32}
	}
	return &model.Network{
		Global: model.GlobalMeta{SearchDomain: "example.invalid"},
		Hosts: []model.Host{
			{Hostname: "sea1-vpn-0", DeviceType: "vyos", Role: "vpn", Site: "sea1", Address: addr("10.0.0.1")},
			{Hostname: "fmt2-core", DeviceType: "eos", Role: "core", Site: "fmt2", Address: addr("10.0.0.2")},
			{Hostname: "ext-peer", DeviceType: "external", Role: "external", Site: "sea1"},
		},
	}
}

type fakeSecrets struct{}

func (fakeSecrets) HostSecret(hostname, key string) (string, error) {
	return "SECRET-host-" + hostname + "-" + key, nil
}

type fakeRenderer struct {
	text string
	err  error
}

func (f fakeRenderer) Render(h *model.Host, _ *model.Network, _ SecretSource) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return "set system host-name " + h.Hostname + "\n" + f.text, nil
}

type fakeReader struct {
	status    DeviceStatus
	statusErr error
	running   string
	configErr error
}

func (f fakeReader) Status(context.Context) (DeviceStatus, error) {
	return f.status, f.statusErr
}

func (f fakeReader) RunningConfig(context.Context) (string, error) {
	return f.running, f.configErr
}

// fakeConn is the connection the fake dialer hands back; nothing is ever
// read from or written to it, the CLI only cares that dialing worked.
type fakeConn struct{ net.Conn }

func (fakeConn) Close() error { return nil }

// harness installs fake backends over the package seams and restores
// them afterwards, so no test ever touches Vault or a real device.
type harness struct {
	net      *model.Network
	renderer fakeRenderer
	readers  map[string]fakeReader
	// reachable maps a candidate address to whether it "answers" on 443.
	reachable map[string]bool
	secretErr error
	out       bytes.Buffer
	errOut    bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		net:       fakeNetwork(),
		readers:   map[string]fakeReader{},
		reachable: map[string]bool{},
	}

	oldLoad, oldSecrets, oldRenderer := loadNetwork, newSecrets, newRenderer
	oldReader, oldStatus, oldTemplatable := newReader, reportsStatus, isTemplatable
	oldDial := dialContext
	t.Cleanup(func() {
		loadNetwork, newSecrets, newRenderer = oldLoad, oldSecrets, oldRenderer
		newReader, reportsStatus, isTemplatable = oldReader, oldStatus, oldTemplatable
		dialContext = oldDial
	})

	loadNetwork = func(string) (*model.Network, error) { return h.net, nil }
	newSecrets = func() (SecretSource, error) {
		if h.secretErr != nil {
			return nil, h.secretErr
		}
		return fakeSecrets{}, nil
	}
	newRenderer = func(string) (Renderer, error) { return h.renderer, nil }
	newReader = func(host *model.Host, _ string, _ SecretSource) (DeviceReader, error) {
		r, ok := h.readers[host.Hostname]
		if !ok {
			return nil, errors.New("no reader configured")
		}
		return r, nil
	}
	reportsStatus = func(dt string) bool { return dt == "vyos" || dt == "eos" }
	isTemplatable = func(dt string) bool { return dt != "external" }
	dialContext = func(_ context.Context, _, address string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if h.reachable[host] {
			return fakeConn{}, nil
		}
		return nil, errors.New("connection refused")
	}
	return h
}

// options returns Options wired to the harness buffers, forced plain.
func (h *harness) options() *Options {
	o := &Options{Out: &h.out, ErrOut: &h.errOut, Plain: true}
	o.SetInteractive(false)
	return o
}

// run executes the barf command tree with the harness's options.
func (h *harness) run(t *testing.T, args ...string) error {
	t.Helper()
	o := h.options()
	root := NewRootCmd(o)
	// Pin the path so the test never depends on the working directory;
	// loadNetwork is faked, so the file is never opened.
	root.SetArgs(append(args, "--network", "fake.yml"))
	root.SetOut(&h.out)
	root.SetErr(&h.errOut)
	return root.Execute()
}
