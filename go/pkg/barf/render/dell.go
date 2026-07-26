package render

import (
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// DNOS6 renders a Dell Networking OS 6 switch in the `network_devices`
// role: projects/barf/barf/templates/network_devices/dnos6.j2, which is
// `{% include 'common/dnos6.j2' %}`.
type DNOS6 struct{}

// DNOS9 renders a Dell Networking OS 9 switch.
//
// It is the SAME config as DNOS6: network_devices/dnos9.j2 also
// includes common/dnos6.j2, and nothing in that template branches on
// devicetype. The separate common/dnos9.j2 (a DNOS9-native `tacacs+`
// AAA block) is dead -- no template includes it, and it references an
// undefined `tsecrets.acacs_host`, a typo for `secrets.tacacs_host`
// that would render empty. Ported as the alias Python actually is,
// rather than as the file's name suggests.
type DNOS9 struct{}

// Render returns the full DNOS6 config text for h.
func (DNOS6) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "network_devices" {
		return "", noTemplateError(h)
	}
	return RenderDNOS(IOSDeviceFromHost(h), n.Global, s)
}

// Render returns the full DNOS9 config text for h.
func (DNOS9) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "network_devices" {
		return "", noTemplateError(h)
	}
	return RenderDNOS(IOSDeviceFromHost(h), n.Global, s)
}

// RenderDNOS renders a NetBox-sourced Dell switch (common/dnos6.j2).
// This is the real entry point for both Dell vendors: see ios.go on why
// these devices are an IOSDevice rather than a model.Host.
func RenderDNOS(d *IOSDevice, global model.GlobalMeta, s SecretSource) (string, error) {
	adminPassword, err := s.HostSecret(d.Hostname, "admin-password")
	if err != nil {
		return "", err
	}
	// Unlike the Cisco template, DNOS emits the key unconditionally.
	key, err := tacacsKey(s, d.Hostname)
	if err != nil {
		return "", err
	}

	lines := []string{
		"hostname " + d.Hostname,
		"enable password " + adminPassword,
		"username admin password " + adminPassword + " privilege 15",
		"!",
		"ip ssh server",
		"!",
		"aaa new-model",
		"",
		"tacacs-server key " + key,
	}
	for _, server := range d.TacacsServers {
		lines = append(lines, "tacacs-server host "+server, "exit", "!")
	}
	lines = append(lines,
		"aaa authentication login default tacacs local",
		"aaa authentication enable default tacacs enable",
		"ip http authentication tacacs local",
		"ip https authentication tacacs local",
		"",
		"aaa authorization exec default tacacs local",
		"",
		"aaa accounting exec default start-stop tacacs",
		"aaa accounting commands default start-stop tacacs",
		"!",
	)

	lines = append(lines, iosVLANStanzas(d)...)
	lines = append(lines, iosInterfaceStanzas(d)...)

	if d.DefaultRoute != "" {
		lines = append(lines, "ip route default "+d.DefaultRoute)
	}
	lines = append(lines, "!")
	lines = append(lines, iosSNMP(d, global)...)

	return strings.Join(lines, "\n") + "\n", nil
}
