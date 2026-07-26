package render

import (
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// DNOS6 renders a Dell Networking OS 6 switch in the `network_devices` role
// (network_devices/dnos6.j2, i.e. common/dnos6.j2).
type DNOS6 struct{}

// DNOS9 renders a Dell Networking OS 9 switch: the SAME config as DNOS6,
// since dnos9.j2 also includes common/dnos6.j2. The separate common/dnos9.j2
// is dead code referencing an undefined `tsecrets.acacs_host`.
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

// RenderDNOS renders a NetBox-sourced Dell switch (common/dnos6.j2), the real
// entry point for both Dell vendors; see ios.go on why it takes an IOSDevice.
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
