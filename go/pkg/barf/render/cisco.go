package render

import (
	"strings"

	"github.com/general-programming/megarepo/go/pkg/barf/model"
)

// Cisco renders a Cisco IOS switch in the `network_devices` role. Port of
// network_devices/cisco.j2, i.e. common/cisco_ios.j2.
type Cisco struct{}

// Render returns the full IOS config text for h.
func (Cisco) Render(h *model.Host, n *model.Network, s SecretSource) (string, error) {
	if h.Role != "network_devices" {
		return "", noTemplateError(h)
	}
	return RenderCiscoIOS(IOSDeviceFromHost(h), n.Global, s)
}

// RenderCiscoIOS renders a NetBox-sourced IOS switch, the real entry point
// for the vendor; see ios.go on why it takes an IOSDevice.
func RenderCiscoIOS(d *IOSDevice, global model.GlobalMeta, s SecretSource) (string, error) {
	adminPassword, err := s.HostSecret(d.Hostname, "admin-password")
	if err != nil {
		return "", err
	}

	// common/shared_ios_common.j2.
	lines := []string{
		"hostname " + d.Hostname,
		// EOS carries its DNS domain as `dns domain` in eos.j2 instead.
		"ip domain-name " + iosDomainName,
		"enable password " + adminPassword,
		"user admin secret " + adminPassword + " privilege 15",
		"",
	}

	lines = append(lines, iosVLANStanzas(d)...)
	lines = append(lines, iosInterfaceStanzas(d)...)

	if d.DefaultRoute != "" {
		lines = append(lines,
			"ip route 0.0.0.0 0.0.0.0 "+d.DefaultRoute,
			"ip default-gateway "+d.DefaultRoute,
		)
	}

	if len(d.TacacsServers) > 0 {
		tacacsKey, err := tacacsKey(s, d.Hostname)
		if err != nil {
			return "", err
		}
		lines = append(lines, ciscoTacacs(d.TacacsServers, tacacsKey)...)
	}

	lines = append(lines,
		"ntp server 0.pool.ntp.org",
		"ntp server 1.pool.ntp.org",
		// Discovery.
		"cdp run",
		"lldp run",
	)
	lines = append(lines, iosSNMP(d, global)...)
	lines = append(lines, "ip scp server enable")

	return strings.Join(lines, "\n") + "\n", nil
}

// ciscoTacacs is the AAA half of common/cisco_ios.j2, unreachable because
// BaseHost.tacacs_servers is hardcoded empty upstream.
func ciscoTacacs(servers []string, key string) []string {
	lines := []string{
		"aaa new-model",
		"!",
		"tacacs-server key " + key,
		"aaa group server tacacs+ consul",
		"!",
	}
	for _, server := range servers {
		lines = append(lines, "    server "+server)
	}
	lines = append(lines, "!")
	for _, server := range servers {
		lines = append(lines, "tacacs-server host "+server)
	}
	return append(lines,
		"aaa authentication login default group consul local",
		"aaa authentication enable default group consul enable",
		"!",
		"aaa authorization exec default group consul local",
		"aaa authorization commands 0 default group consul local",
		"aaa authorization commands 1 default group consul local",
		"aaa authorization commands 15 default group consul local",
		"!",
		"aaa accounting exec default start-stop group consul",
		"aaa accounting commands 0 default start-stop group consul",
		"aaa accounting commands 1 default start-stop group consul",
		"aaa accounting commands 15 default start-stop group consul",
	)
}
