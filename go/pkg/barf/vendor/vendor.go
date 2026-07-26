// Package vendor is the one place that says what barf can do to each
// kind of device.
//
// # Why this exists
//
// barf is split by CONCERN, not by vendor: render generates config and
// touches no I/O, device owns the transports and keeps read and write
// apart, scope compares managed slices. That split is load-bearing and
// this package does not undo it — every implementation still lives in
// its own package and still speaks its own small interface.
//
// What the split cost was COHESION of the *dispatch*. Four maps keyed by
// the same devicetype string (render.renderers, device.New's switch,
// scope.comparers, cli.writerFactories) plus a fifth hand-written copy
// of one of them (cli.wireReportsStatus) meant that adding a vendor was
// four or five edits in four packages, and forgetting one failed at
// RUNTIME, on a device, in front of an operator.
//
// So the four registries moved here, into one table with one row per
// devicetype. Nothing else moved. render still cannot see a socket;
// device still refuses to hand out a Writer without an explicit opt-in;
// scope is still a small consumer-side interface. This package only
// answers "which implementation, for which devicetype".
//
// # Capabilities are data, not type assertions
//
// A nil field is a statement, and it is greppable:
//
//	v.Renderer  == nil  barf generates no config for this devicetype
//	v.NewReader == nil  barf has no transport for it; it cannot be probed
//	v.NewWriter == nil  barf CANNOT DEPLOY TO IT. Not "not yet wired" --
//	                    there is no code path from this package to a
//	                    config change on that vendor.
//	v.Comparer  == nil  barf owns the whole config; diff compares all of it
//
// That is the point. `external` is not a special case in an if-statement
// any more, it is a row whose fields are all nil. And the fields are
// typed, so a row that names the wrong constructor does not compile.
package vendor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/device"
	"github.com/general-programming/megarepo/go/pkg/barf/model"
	"github.com/general-programming/megarepo/go/pkg/barf/render"
	"github.com/general-programming/megarepo/go/pkg/barf/scope"
)

// ReaderFactory builds a read-only client for a host. It is exactly
// device.NewEOS / device.NewVyOS's shape, so a row names the real
// constructor with no adapter in between.
type ReaderFactory func(h *model.Host, opts device.Options) (device.Reader, error)

// WriterFactory builds a write-capable client for a host. The
// constructors it wraps refuse to return one unless
// device.Options.AllowWrites is explicitly set; this type does not and
// cannot loosen that.
type WriterFactory func(h *model.Host, opts device.Options) (device.Writer, error)

// Vendor is everything barf knows how to do to one devicetype.
//
// It is a struct of small interfaces, not one fat interface: most
// vendors genuinely have only some of these, and a struct lets them say
// "no" by leaving a field nil instead of stubbing a method that returns
// ErrUnsupported.
type Vendor struct {
	// Type is the canonical devicetype as it appears in network.yml.
	Type string

	// Aliases are the other spellings that mean this vendor, which come
	// from NetBox platform slugs rather than from network.yml (Python
	// VENDOR_MAP carried both).
	Aliases []string

	// Renderer generates config text. nil means barf renders nothing for
	// this devicetype (`external`, the far end of a fabric link barf does
	// not manage). Python BaseHost.TEMPLATABLE.
	Renderer render.Renderer

	// Roles is the set of network.yml roles Renderer supports, or nil
	// when it supports any. Documentation only -- the renderer enforces
	// its own matrix and returns the same error Python's Jinja
	// TemplateNotFound did. Listed here so the matrix is readable in one
	// place; see the table below.
	Roles []string

	// NewReader builds the read-only transport. nil means barf has no
	// way to talk to this vendor at all, which is also the answer to
	// "does it report status" (Python REPORTS_STATUS).
	NewReader ReaderFactory

	// NewWriter builds the write transport. nil means this vendor cannot
	// be deployed to.
	NewWriter WriterFactory

	// Comparer compares only the slice of config barf manages. nil means
	// barf owns the whole config and `barf diff` compares all of it.
	Comparer scope.Comparer
}

// Templatable reports whether barf generates config for this vendor.
func (v Vendor) Templatable() bool { return v.Renderer != nil }

// ReportsStatus reports whether `barf status` can probe this vendor.
func (v Vendor) ReportsStatus() bool { return v.NewReader != nil }

// Deployable reports whether barf can change this vendor's config.
func (v Vendor) Deployable() bool { return v.NewWriter != nil }

// Scoped reports whether this vendor is adopted a slice at a time.
func (v Vendor) Scoped() bool { return v.Comparer != nil }

// vendors is THE table. One row per devicetype; adding a vendor is one
// row here plus the implementation in whichever packages it needs.
//
// The role column mirrors Python's dispatch, which is just
// `templates/<role>/<devicetype>.j2` existing on disk:
//
//	vyos, linux, mikrotik   vpn only (barf.configs.BLOCK_REGISTRY)
//	edgeos                  vpn only (templates/vpn/edgeos.j2)
//	cisco, dnos6, dnos9     network_devices only
//	eos                     any role -- arista.py renders a scoped
//	                        managed slice, dispatched to before role is
//	                        consulted
//
// Those are all the (role, devicetype) pairs that exist: there is no
// templates/vpn/vyos.j2, no templates/core/anything. A pair outside the
// table fails in Python with a Jinja TemplateNotFound and fails here
// with the renderer's own no-config error, which is the same answer said
// politely.
var vendors = []Vendor{
	{
		Type:     "eos",
		Renderer: render.EOS{},
		// Any role: the scoped managed slice is rendered before role is
		// consulted.
		NewReader: func(h *model.Host, o device.Options) (device.Reader, error) { return device.NewEOS(h, o) },
		// NewWriter is deliberately nil even though device.NewEOSWriter
		// exists and is tested. Wiring it here is the single line that
		// would make `barf deploy` able to change an Arista switch, and
		// that is a decision to take on purpose, not a registration to
		// forget. Under the old four-registry layout this gap was
		// invisible; here it is a nil field with a reason next to it.
		NewWriter: nil,
		// EOS is adopted a slice at a time: barf owns the admin user, its
		// ssh-keys, the enable password and the eAPI block, nothing else.
		Comparer: scope.EOS{},
	},
	{
		Type:      "vyos",
		Renderer:  render.VyOS{},
		Roles:     []string{"vpn"},
		NewReader: func(h *model.Host, o device.Options) (device.Reader, error) { return device.NewVyOS(h, o) },
		NewWriter: func(h *model.Host, o device.Options) (device.Writer, error) { return device.NewVyOSWriter(h, o) },
		// nil: barf owns the entire VyOS config, so a whole-config diff
		// is the correct comparison.
		Comparer: nil,
	},
	{
		Type:     "linux",
		Renderer: render.Linux{},
		Roles:    []string{"vpn"},
	},
	{
		Type:     "mikrotik",
		Renderer: render.MikroTik{},
		Roles:    []string{"vpn"},
	},
	{
		Type:     "edgeos",
		Renderer: render.EdgeOS{},
		Roles:    []string{"vpn"},
	},
	{
		Type:     "cisco",
		Aliases:  []string{"cisco-ios"},
		Renderer: render.Cisco{},
		Roles:    []string{"network_devices"},
	},
	{
		Type:     "dnos6",
		Aliases:  []string{"dnos-6"},
		Renderer: render.DNOS6{},
		Roles:    []string{"network_devices"},
	},
	{
		Type:     "dnos9",
		Aliases:  []string{"dnos-9"},
		Renderer: render.DNOS9{},
		Roles:    []string{"network_devices"},
	},
	{
		// external marks the far side of a fabric link barf does not
		// manage (a cloud VPN endpoint, a peer's router). Such hosts
		// exist in network.yml so links can name them and so their ASN
		// and endpoint address are known; barf does nothing to them.
		//
		// Every field is nil, which is the whole statement. It used to be
		// `deviceType != "external"` inline in render.Templatable.
		Type: "external",
	},
}

// byType indexes the table by canonical type and by alias. Built once at
// init so a duplicate key is a startup panic in every binary and every
// test, not a silently-wins map literal.
var byType = func() map[string]Vendor {
	index := make(map[string]Vendor, len(vendors)*2)
	for _, v := range vendors {
		if _, dup := index[v.Type]; dup {
			panic("vendor: duplicate devicetype " + v.Type)
		}
		index[v.Type] = v
		for _, alias := range v.Aliases {
			if _, dup := index[alias]; dup {
				panic("vendor: duplicate devicetype alias " + alias)
			}
			index[alias] = v
		}
	}
	return index
}()

// Get returns the vendor registered for a devicetype, resolving aliases.
// Lookup is case-insensitive, matching device.New's old strings.ToLower.
func Get(deviceType string) (Vendor, bool) {
	v, ok := byType[strings.ToLower(strings.TrimSpace(deviceType))]
	return v, ok
}

// All returns every vendor, ordered by canonical type.
func All() []Vendor {
	out := make([]Vendor, len(vendors))
	copy(out, vendors)
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Types returns every canonical devicetype, sorted. Aliases excluded.
func Types() []string {
	out := make([]string, 0, len(vendors))
	for _, v := range vendors {
		out = append(out, v.Type)
	}
	sort.Strings(out)
	return out
}

// TypesWhere returns the canonical devicetypes whose row satisfies pred,
// sorted. Used for the "deployable: [...]" style error messages, which
// previously each walked their own registry.
func TypesWhere(pred func(Vendor) bool) []string {
	out := make([]string, 0, len(vendors))
	for _, v := range vendors {
		if pred(v) {
			out = append(out, v.Type)
		}
	}
	sort.Strings(out)
	return out
}

// -- convenience predicates -------------------------------------------
//
// These are the questions the CLI actually asks. Each used to be a
// hand-written switch or a second copy of a registry; now each is one
// lookup against the one table, so they cannot disagree with each other.

// Templatable reports whether barf generates config for a devicetype.
// An unknown devicetype is not templatable.
func Templatable(deviceType string) bool {
	v, ok := Get(deviceType)
	return ok && v.Templatable()
}

// ReportsStatus reports whether `barf status` can probe a devicetype.
// Python REPORTS_STATUS.
func ReportsStatus(deviceType string) bool {
	v, ok := Get(deviceType)
	return ok && v.ReportsStatus()
}

// Deployable reports whether barf can change a devicetype's config.
func Deployable(deviceType string) bool {
	v, ok := Get(deviceType)
	return ok && v.Deployable()
}

// Renderer returns the renderer for a devicetype.
func Renderer(deviceType string) (render.Renderer, bool) {
	v, ok := Get(deviceType)
	if !ok || v.Renderer == nil {
		return nil, false
	}
	return v.Renderer, true
}

// Comparer returns the scoped comparer for a devicetype, if it has one.
// A devicetype with none gets the generic whole-config diff.
func Comparer(deviceType string) (scope.Comparer, bool) {
	v, ok := Get(deviceType)
	if !ok || v.Comparer == nil {
		return nil, false
	}
	return v.Comparer, true
}

// -- dispatch ----------------------------------------------------------

// Render renders one host with the renderer registered for its
// devicetype. Replaces render.Host, which had to carry its own registry
// to do this.
func Render(h *model.Host, n *model.Network, s render.SecretSource) (string, error) {
	if h == nil {
		return "", fmt.Errorf("vendor: nil host")
	}
	v, ok := Get(h.DeviceType)
	if !ok {
		return "", fmt.Errorf("%s: no renderer for device type %q", h.Hostname, h.DeviceType)
	}
	if v.Renderer == nil {
		return "", fmt.Errorf("%s: device type %q is unmanaged; barf renders no config for it",
			h.Hostname, h.DeviceType)
	}
	return v.Renderer.Render(h, n, s)
}

// NewReader returns the read-only client for h's devicetype. Replaces
// device.New, which had to carry its own vendor switch to do this.
func NewReader(h *model.Host, opts device.Options) (device.Reader, error) {
	if h == nil {
		return nil, fmt.Errorf("vendor: nil host")
	}
	v, ok := Get(h.DeviceType)
	if !ok || v.NewReader == nil {
		return nil, fmt.Errorf("device: %s: %w: devicetype %q does not report status",
			h.Hostname, device.ErrUnsupported, h.DeviceType)
	}
	return v.NewReader(h, opts)
}

// NewWriter returns the write client for h's devicetype, or explains
// that the vendor has none.
//
// It changes nothing about how a writer is guarded: the constructors it
// calls still refuse unless opts.AllowWrites is explicitly true, and
// this function has no way to set that flag on the caller's behalf.
func NewWriter(h *model.Host, opts device.Options) (device.Writer, error) {
	if h == nil {
		return nil, fmt.Errorf("vendor: nil host")
	}
	v, ok := Get(h.DeviceType)
	if !ok || v.NewWriter == nil {
		return nil, fmt.Errorf("no write implementation for devicetype %q (deployable: %v)",
			h.DeviceType, TypesWhere(Vendor.Deployable))
	}
	return v.NewWriter(h, opts)
}
