// Package vendor says what barf can do to each kind of device: one table,
// one row per devicetype, composing render/device/scope rather than
// replacing them. Capabilities are data, not type assertions: a nil field
// is a deliberate, greppable statement — a nil NewWriter means barf CANNOT
// DEPLOY to that vendor. `external` is a row whose fields are all nil.
//
// This package imports render, device and scope; none of them may import it.
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

// ReaderFactory builds a read-only client; deliberately device.NewEOS's shape,
// so a row names the real constructor.
type ReaderFactory func(h *model.Host, opts device.Options) (device.Reader, error)

// WriterFactory builds a write-capable client for a host. The constructors
// it wraps refuse unless device.Options.AllowWrites is explicitly set.
type WriterFactory func(h *model.Host, opts device.Options) (device.Writer, error)

// Vendor is everything barf knows how to do to one devicetype; each
// capability is its own field, so a vendor says "no" with a nil.
type Vendor struct {
	// Type is the canonical devicetype as it appears in network.yml.
	Type string

	// Aliases come from NetBox platform slugs, not network.yml.
	Aliases []string

	// Renderer generates config text; nil renders nothing for this devicetype.
	Renderer render.Renderer

	// Roles is documentation only: the renderer enforces its own matrix.
	Roles []string

	// NewReader builds the read-only transport; nil means barf cannot talk to
	// this vendor at all, and so cannot report status.
	NewReader ReaderFactory

	// NewWriter builds the write transport; nil means not deployable.
	NewWriter WriterFactory

	// Comparer compares only the slice of config barf manages; nil means barf
	// owns the whole config and `barf diff` compares all of it.
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

// vendors is THE table: one row per devicetype. Roles mirror which Python
// `templates/<role>/<devicetype>.j2` existed.
var vendors = []Vendor{
	{
		Type:     "eos",
		Renderer: render.EOS{},
		// No Roles: the scoped managed slice is rendered before role is consulted.
		NewReader: func(h *model.Host, o device.Options) (device.Reader, error) { return device.NewEOS(h, o) },
		// Deliberately nil though device.NewEOSWriter exists and is tested:
		// this line is what would let `barf deploy` change an Arista switch.
		NewWriter: nil,
		// barf owns the admin user, ssh-keys, enable password and eAPI block.
		Comparer: scope.EOS{},
	},
	{
		Type:      "vyos",
		Renderer:  render.VyOS{},
		Roles:     []string{"vpn"},
		NewReader: func(h *model.Host, o device.Options) (device.Reader, error) { return device.NewVyOS(h, o) },
		NewWriter: func(h *model.Host, o device.Options) (device.Writer, error) { return device.NewVyOSWriter(h, o) },
		// nil: barf owns the entire VyOS config, so a whole-config diff is right.
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
		// external is the far side of an unmanaged fabric link, in network.yml
		// only so links can name it. Every field nil.
		Type: "external",
	},
}

// byType indexes the table by canonical type and alias, at init so a
// duplicate key panics at startup rather than silently winning.
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

// Get returns the vendor for a devicetype, resolving aliases, case-insensitively.
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

// TypesWhere returns the sorted canonical devicetypes whose row satisfies pred.
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

// Templatable reports whether barf generates config for a devicetype.
func Templatable(deviceType string) bool {
	v, ok := Get(deviceType)
	return ok && v.Templatable()
}

// ReportsStatus reports whether `barf status` can probe a devicetype.
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
func Comparer(deviceType string) (scope.Comparer, bool) {
	v, ok := Get(deviceType)
	if !ok || v.Comparer == nil {
		return nil, false
	}
	return v.Comparer, true
}

// -- dispatch ----------------------------------------------------------

// Render renders one host with its devicetype's registered renderer.
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

// NewReader returns the read-only client for h's devicetype.
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

// NewWriter returns the write client for h's devicetype; the constructors it
// calls still refuse unless opts.AllowWrites is explicitly true.
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
