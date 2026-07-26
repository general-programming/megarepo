package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/general-programming/megarepo/go/client/netbox"
	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/spf13/cobra"
)

// This file ports `barf generate dns` / `barf generate dhcp`. Where the
// old Python barf (projects/barf/barf/cli/generate.py) and the newer
// production renderers (nix/modules/dns/refresh_dns.py,
// nix/modules/kea/refresh_kea.py) disagree, the production semantics win:
// one reservation per MAC, a bare device name for the primary interface,
// either address family optionally absent, and a refusal to render an
// empty result so a bad NetBox response cannot wipe a live config.

// defaultDNSDomain is the zone barf generates records under; DNS_DOMAIN
// overrides it, matching refresh_dns.py.
const defaultDNSDomain = "generalprogramming.org"

// dnsmasqHeader is the first line of a rendered dnsmasq include, kept
// byte-identical to refresh_dns.py so the two implementations produce
// comparable files.
const dnsmasqHeader = "# Generated from NetBox by netbox-dnsmasq. Do not edit."

// netboxSource is the read-only NetBox surface the generate commands
// need. Declared locally (like the seams in deps.go) so tests can supply
// a fake without a live instance.
type netboxSource interface {
	FetchDNS(ctx context.Context) (*netbox.DNSResult, error)
	FetchDHCP(ctx context.Context) (*netbox.DHCPResult, error)
}

// newNetbox builds the NetBox client. Tests replace it.
var newNetbox func(ctx context.Context) (netboxSource, error) = wireNetbox

// wireNetbox resolves an API token — NETBOX_API_KEY first, then Vault's
// secret/infra/netbox api_key — and returns a client for it. The token is
// never logged or echoed.
func wireNetbox(ctx context.Context) (netboxSource, error) {
	token := os.Getenv("NETBOX_API_KEY")
	if token == "" {
		v, err := vault.New(vault.Options{})
		if err != nil {
			return nil, fmt.Errorf("no NETBOX_API_KEY and no Vault client: %w", err)
		}
		token, err = v.Get(ctx, vault.MountSecret, "infra/netbox", "api_key")
		if err != nil {
			return nil, fmt.Errorf("reading the NetBox token from Vault: %w", err)
		}
	}
	return netbox.NewFromEnv(netbox.Options{Token: token})
}

// dnsDomain is the zone to render into: --domain, else DNS_DOMAIN, else
// defaultDNSDomain.
func dnsDomain(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv("DNS_DOMAIN"); env != "" {
		return env
	}
	return defaultDNSDomain
}

// isNonStaticHost reports whether a NetBox name belongs to a device that
// gets no static record: access points, phones, temporary kit and the
// das- prefix. Ported unchanged from both Python implementations.
func isNonStaticHost(hostname string) bool {
	return strings.Contains(hostname, "-ap-") ||
		strings.Contains(hostname, "-phone-") ||
		strings.Contains(hostname, "-temp-") ||
		strings.HasPrefix(hostname, "das-")
}

// reverseARPA renders an IPv4 address as its in-addr.arpa name.
func reverseARPA(ipv4 string) string {
	octets := strings.Split(ipv4, ".")
	for i, j := 0, len(octets)-1; i < j; i, j = i+1, j-1 {
		octets[i], octets[j] = octets[j], octets[i]
	}
	return strings.Join(octets, ".") + ".in-addr.arpa"
}

// dnsLines renders the dnsmasq address= / ptr-record= lines for every
// host. Hosts with no primary address are reported through warn and
// skipped, as refresh_dns.py does on stderr.
func dnsLines(hosts []netbox.Host, domain string, warn func(hostname string)) []string {
	var out []string
	for _, host := range hosts {
		if isNonStaticHost(host.Name) {
			continue
		}

		ipv4, ipv6 := host.IPv4(), host.IPv6()
		if ipv4 == "" && ipv6 == "" {
			if warn != nil {
				warn(host.Name)
			}
			continue
		}

		fqdn := strings.ToLower(netbox.CleanHostname(host.Name)) + "." + domain

		if ipv6 != "" {
			out = append(out, fmt.Sprintf("address=/%s/%s", fqdn, ipv6))
		}
		if ipv4 != "" {
			out = append(out,
				fmt.Sprintf("address=/%s/%s", fqdn, ipv4),
				fmt.Sprintf("ptr-record=%s,%s", reverseARPA(ipv4), fqdn),
			)
		}
		// The IPMI PTR deliberately points at the bare fqdn, not
		// ipmi.<fqdn>: both Python implementations do this and internal
		// tooling reverse-resolves BMC addresses to the host.
		if ipmi := host.IPMIAddress(); ipmi != "" {
			out = append(out,
				fmt.Sprintf("address=/ipmi.%s/%s", fqdn, ipmi),
				fmt.Sprintf("ptr-record=%s,%s", reverseARPA(ipmi), fqdn),
			)
		}
	}
	return out
}

// dhcpHostname is the hostname handed to the DHCP client for one
// reservation.
//
// The primary interface gets the bare device name: clients that take
// their hostname from DHCP (Talos, notably) must not be renamed to
// device-interface, which re-registers kubelets as brand-new nodes.
// Secondary interfaces keep the interface suffix so names stay unique.
func dhcpHostname(device, iface string, isPrimary bool) string {
	name := strings.ToLower(netbox.CleanHostname(device))
	if !isPrimary {
		name += "-" + strings.ToLower(netbox.CleanHostname(iface))
	}

	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return b.String()
}

// reservation is one DHCP host reservation. Either address family may be
// empty, but never both.
type reservation struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IPv4     string `json:"ipv4,omitempty"`
	IPv6     string `json:"ipv6,omitempty"`
}

// DnsmasqLine renders the reservation as a dnsmasq dhcp-host= line.
// dnsmasq (>= 2.81) matches DHCPv6 clients by MAC on directly attached
// subnets, so both families key on the MAC and the v6 address takes the
// bracketed form: dhcp-host=MAC,v4,[v6],hostname.
func (r reservation) DnsmasqLine() string {
	parts := []string{r.MAC}
	if r.IPv4 != "" {
		parts = append(parts, r.IPv4)
	}
	if r.IPv6 != "" {
		parts = append(parts, "["+r.IPv6+"]")
	}
	parts = append(parts, r.Hostname)
	return "dhcp-host=" + strings.Join(parts, ",")
}

// reservations collapses NetBox interfaces into at most one reservation
// per MAC — dnsmasq and Kea both reject duplicates — keeping the first
// interface that carries a usable address. An interface with a MAC but
// no addresses does not consume the MAC.
func reservations(interfaces []netbox.Interface) []reservation {
	seen := map[string]bool{}
	var out []reservation

	for _, iface := range interfaces {
		mac := iface.PrimaryMACAddress.MAC()
		if mac == "" {
			continue
		}
		owner := iface.OwnerRef()
		if owner == nil || owner.Name == "" {
			continue
		}
		if seen[strings.ToLower(mac)] {
			continue
		}

		var ipv4, ipv6 string
		for i := range iface.IPAddresses {
			ip := iface.IPAddresses[i].IP()
			switch {
			case ip == "":
			case strings.Contains(ip, ":"):
				if ipv6 == "" {
					ipv6 = ip
				}
			default:
				if ipv4 == "" {
					ipv4 = ip
				}
			}
		}
		if ipv4 == "" && ipv6 == "" {
			continue
		}
		seen[strings.ToLower(mac)] = true

		primaryIP := owner.PrimaryIP4.IP()
		isPrimary := primaryIP != "" && ipv4 == primaryIP
		out = append(out, reservation{
			MAC:      mac,
			Hostname: dhcpHostname(owner.Name, iface.Name, isPrimary),
			IPv4:     ipv4,
			IPv6:     ipv6,
		})
	}
	return out
}

// keaHost4 is one entry of Kea's Dhcp4 global reservation array.
type keaHost4 struct {
	HWAddress string `json:"hw-address"`
	IPAddress string `json:"ip-address"`
	Hostname  string `json:"hostname"`
}

// keaHost6 is one entry of Kea's Dhcp6 global reservation array. Keyed on
// hw-address, not DUID: Talos mints a fresh DUID-LLT every lease cycle,
// which would otherwise rotate addresses.
type keaHost6 struct {
	HWAddress   string   `json:"hw-address"`
	IPAddresses []string `json:"ip-addresses"`
	Hostname    string   `json:"hostname"`
}

// keaReservations splits reservations into Kea's per-family arrays,
// matching nix/modules/kea/refresh_kea.py.
func keaReservations(res []reservation) ([]keaHost4, []keaHost6) {
	hosts4 := []keaHost4{}
	hosts6 := []keaHost6{}
	for _, r := range res {
		if r.IPv4 != "" {
			hosts4 = append(hosts4, keaHost4{HWAddress: r.MAC, IPAddress: r.IPv4, Hostname: r.Hostname})
		}
		if r.IPv6 != "" {
			hosts6 = append(hosts6, keaHost6{HWAddress: r.MAC, IPAddresses: []string{r.IPv6}, Hostname: r.Hostname})
		}
	}
	return hosts4, hosts6
}

// dnsJSON is the structured form of `generate dns --json`, kept in the
// shape the Python implementation emitted.
type dnsJSON struct {
	Addresses  []dnsAddressJSON `json:"addresses"`
	PTRRecords []dnsPTRJSON     `json:"ptr_records"`
}

type dnsAddressJSON struct {
	FQDN string `json:"fqdn"`
	IP   string `json:"ip"`
}

type dnsPTRJSON struct {
	ReverseARPA string `json:"reverse_arpa"`
	FQDN        string `json:"fqdn"`
}

// dnsJSONFromLines re-reads the rendered dnsmasq lines into the JSON
// shape, so both outputs can never drift apart.
func dnsJSONFromLines(lines []string) dnsJSON {
	out := dnsJSON{Addresses: []dnsAddressJSON{}, PTRRecords: []dnsPTRJSON{}}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "address=/"):
			fields := strings.Split(strings.TrimPrefix(line, "address=/"), "/")
			if len(fields) == 2 {
				out.Addresses = append(out.Addresses, dnsAddressJSON{FQDN: fields[0], IP: fields[1]})
			}
		case strings.HasPrefix(line, "ptr-record="):
			name, fqdn, ok := strings.Cut(strings.TrimPrefix(line, "ptr-record="), ",")
			if ok {
				out.PTRRecords = append(out.PTRRecords, dnsPTRJSON{ReverseARPA: name, FQDN: fqdn})
			}
		}
	}
	return out
}

// writeOut writes text to path, or to the command's stdout when path is
// empty or "-".
func (o *Options) writeOut(path, text string) error {
	if path == "" || path == "-" {
		_, err := io.WriteString(o.Out, text)
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// writeJSON marshals value to path (or stdout) with the indentation the
// rest of the CLI uses.
func (o *Options) writeJSON(path string, value any, indent string) error {
	body, err := json.MarshalIndent(value, "", indent)
	if err != nil {
		return err
	}
	return o.writeOut(path, string(body)+"\n")
}

func newGenerateDNSCmd(o *Options) *cobra.Command {
	var (
		domain   string
		output   string
		withDHCP bool
		jsonOut  bool
	)

	cmd := &cobra.Command{
		Use:   "dns",
		Short: "Render dnsmasq DNS records from NetBox",
		Long: "dns emits an address=/ptr-record= line per NetBox device and virtual\n" +
			"machine, plus ipmi.<host> records for out-of-band interfaces. With\n" +
			"--with-dhcp it renders the whole dnsmasq include that\n" +
			"nix/modules/dns/refresh_dns.py writes today, DHCP reservations and all.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGenerateDNS(cmd.Context(), o, domain, output, withDHCP, jsonOut)
		},
	}
	f := cmd.Flags()
	f.StringVar(&domain, "domain", "", "DNS zone to render into (default: $DNS_DOMAIN, else "+defaultDNSDomain+")")
	f.StringVarP(&output, "output", "o", "-", "file to write; '-' for stdout")
	f.BoolVar(&withDHCP, "with-dhcp", false, "also emit dhcp-host reservations, producing a complete dnsmasq include")
	f.BoolVar(&jsonOut, "json", false, "emit JSON instead of dnsmasq directives")
	return cmd
}

func runGenerateDNS(ctx context.Context, o *Options, domain, output string, withDHCP, jsonOut bool) error {
	client, err := newNetbox(ctx)
	if err != nil {
		return err
	}
	dns, err := client.FetchDNS(ctx)
	if err != nil {
		return err
	}

	log := o.Logger()
	lines := dnsLines(dns.Hosts(), dnsDomain(domain), func(hostname string) {
		log.Warn("missing primary ip", "host", hostname)
	})
	// An empty NetBox response renders a valid-but-empty config that would
	// wipe internal DNS; fail instead and keep the last-good file.
	if len(lines) == 0 {
		return fmt.Errorf("NetBox returned no usable DNS records; refusing to render")
	}

	if jsonOut {
		return o.writeJSON(output, dnsJSONFromLines(lines), "  ")
	}

	body := lines
	if withDHCP {
		dhcp, err := client.FetchDHCP(ctx)
		if err != nil {
			return err
		}
		body = append([]string{dnsmasqHeader}, lines...)
		for _, r := range reservations(dhcp.AllInterfaces()) {
			body = append(body, r.DnsmasqLine())
		}
	}
	return o.writeOut(output, strings.Join(body, "\n")+"\n")
}

func newGenerateDHCPCmd(o *Options) *cobra.Command {
	var (
		format  string
		output  string
		output4 string
		output6 string
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "dhcp",
		Short: "Render DHCP host reservations from NetBox",
		Long: "dhcp emits one reservation per interface MAC: dnsmasq dhcp-host lines\n" +
			"by default, Kea global-reservation JSON with --format kea. The primary\n" +
			"interface's reservation carries the bare device name, because clients\n" +
			"that take their hostname from DHCP must not be renamed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if jsonOut {
				format = "json"
			}
			return runGenerateDHCP(cmd.Context(), o, format, output, output4, output6)
		},
	}
	f := cmd.Flags()
	f.StringVar(&format, "format", "dnsmasq", "output format: dnsmasq, kea or json")
	f.StringVarP(&output, "output", "o", "-", "file to write; '-' for stdout")
	f.StringVar(&output4, "output4", "", "kea only: write the Dhcp4 array here instead of stdout")
	f.StringVar(&output6, "output6", "", "kea only: write the Dhcp6 array here instead of stdout")
	f.BoolVar(&jsonOut, "json", false, "shorthand for --format json")
	return cmd
}

func runGenerateDHCP(ctx context.Context, o *Options, format, output, output4, output6 string) error {
	switch format {
	case "dnsmasq", "kea", "json":
	default:
		return fmt.Errorf("unknown --format %q (want dnsmasq, kea or json)", format)
	}

	client, err := newNetbox(ctx)
	if err != nil {
		return err
	}
	dhcp, err := client.FetchDHCP(ctx)
	if err != nil {
		return err
	}

	res := reservations(dhcp.AllInterfaces())
	// An empty response would wipe every reservation; refuse so the
	// last-good file survives.
	if len(res) == 0 {
		return fmt.Errorf("NetBox returned no usable DHCP reservations; refusing to render")
	}

	switch format {
	case "json":
		return o.writeJSON(output, res, "  ")

	case "kea":
		hosts4, hosts6 := keaReservations(res)
		// refresh_kea.py writes the two arrays as separate files with
		// json.dump(..., indent=1); match that when paths are given.
		if output4 != "" || output6 != "" {
			if output4 != "" {
				if err := o.writeJSON(output4, hosts4, " "); err != nil {
					return err
				}
			}
			if output6 != "" {
				if err := o.writeJSON(output6, hosts6, " "); err != nil {
					return err
				}
			}
			return nil
		}
		return o.writeJSON(output, struct {
			Hosts4 []keaHost4 `json:"hosts4"`
			Hosts6 []keaHost6 `json:"hosts6"`
		}{hosts4, hosts6}, "  ")
	}

	lines := make([]string, 0, len(res))
	for _, r := range res {
		lines = append(lines, r.DnsmasqLine())
	}
	return o.writeOut(output, strings.Join(lines, "\n")+"\n")
}
