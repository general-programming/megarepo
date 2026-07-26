package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/general-programming/megarepo/go/client/netbox"
	"github.com/general-programming/megarepo/go/client/vault"
	"github.com/spf13/cobra"
)

// `barf generate dns` / `barf generate dhcp`. These began as ports of the
// NixOS renderers refresh_dns.py and refresh_kea.py, whose output they had to
// match byte-for-byte; they have since replaced them on the core machines.

// defaultDNSDomain is overridden by DNS_DOMAIN, matching refresh_dns.py.
const defaultDNSDomain = "generalprogramming.org"

// dnsmasqHeader is byte-identical to refresh_dns.py's first line.
const dnsmasqHeader = "# Generated from NetBox by netbox-dnsmasq. Do not edit."

// netboxSource is declared locally, like deps.go's seams, so tests can fake it.
type netboxSource interface {
	FetchDNS(ctx context.Context) (*netbox.DNSResult, error)
	FetchDHCP(ctx context.Context) (*netbox.DHCPResult, error)
}

var newNetbox func(ctx context.Context) (netboxSource, error) = wireNetbox

// wireNetbox uses the token in NETBOX_API_KEY, else Vault's
// secret/infra/netbox api_key. The token is never logged.
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

// dnsDomain resolves --domain, else DNS_DOMAIN, else defaultDNSDomain.
func dnsDomain(flag string) string {
	if flag != "" {
		return flag
	}
	if env := os.Getenv("DNS_DOMAIN"); env != "" {
		return env
	}
	return defaultDNSDomain
}

// isNonStaticHost matches names that get no static record, as Python does.
func isNonStaticHost(hostname string) bool {
	return strings.Contains(hostname, "-ap-") ||
		strings.Contains(hostname, "-phone-") ||
		strings.Contains(hostname, "-temp-") ||
		strings.HasPrefix(hostname, "das-")
}

func reverseARPA(ipv4 string) string {
	octets := strings.Split(ipv4, ".")
	for i, j := 0, len(octets)-1; i < j; i, j = i+1, j-1 {
		octets[i], octets[j] = octets[j], octets[i]
	}
	return strings.Join(octets, ".") + ".in-addr.arpa"
}

// warnSkip reports a declined NetBox row; an empty hostname is spelled out.
func warnSkip(warn func(hostname, reason string), hostname, reason string) {
	if warn == nil {
		return
	}
	if hostname == "" {
		hostname = "<unnamed>"
	}
	warn(hostname, reason)
}

// dnsLines renders the dnsmasq address= / ptr-record= lines. Hosts with no
// primary address are warned about and skipped, as refresh_dns.py does.
func dnsLines(hosts []netbox.Host, domain string, warn func(hostname, reason string)) []string {
	var out []string
	for _, host := range hosts {
		// An unnamed device would emit `address=/.domain/10.0.0.5` with exit
		// 0; one bad row must not cost the good ones.
		if strings.TrimSpace(host.Name) == "" {
			warnSkip(warn, "", "device has no name in NetBox")
			continue
		}
		if isNonStaticHost(host.Name) {
			continue
		}

		ipv4, ipv6 := host.IPv4(), host.IPv6()
		if ipv4 == "" && ipv6 == "" {
			warnSkip(warn, host.Name, "no primary ip")
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
		// The IPMI PTR deliberately points at the bare fqdn, not ipmi.<fqdn>:
		// internal tooling reverse-resolves BMC addresses to the host.
		if ipmi := host.IPMIAddress(); ipmi != "" {
			out = append(out,
				fmt.Sprintf("address=/ipmi.%s/%s", fqdn, ipmi),
				fmt.Sprintf("ptr-record=%s,%s", reverseARPA(ipmi), fqdn),
			)
		}
	}
	return out
}

// dhcpHostname gives the primary interface the bare device name: clients
// taking their hostname from DHCP (Talos) must not be renamed to
// device-interface, which re-registers kubelets as new nodes. Secondaries keep
// the suffix so names stay unique.
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

// reservation is one DHCP host reservation; never both families empty.
type reservation struct {
	MAC      string `json:"mac"`
	Hostname string `json:"hostname"`
	IPv4     string `json:"ipv4,omitempty"`
	IPv6     string `json:"ipv6,omitempty"`
}

// DnsmasqLine renders dhcp-host=MAC,v4,[v6],hostname. dnsmasq >= 2.81 matches
// DHCPv6 clients by MAC on directly attached subnets, so both families key on
// the MAC and v6 takes the bracketed form.
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

// reservations collapses NetBox interfaces into at most one reservation per
// MAC — dnsmasq and Kea both reject duplicates — keeping the first interface
// carrying a usable address. A MAC with no addresses is not consumed.
func reservations(interfaces []netbox.Interface, warn func(hostname, reason string)) []reservation {
	seen := map[string]bool{}
	var out []reservation

	for _, iface := range interfaces {
		mac := iface.PrimaryMACAddress.MAC()
		if mac == "" {
			continue
		}
		owner := iface.OwnerRef()
		if owner == nil || strings.TrimSpace(owner.Name) == "" {
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
		primaryIP := owner.PrimaryIP4.IP()
		isPrimary := primaryIP != "" && ipv4 == primaryIP
		// A null secondary name would render a trailing "-". Only secondaries
		// use the name, and the skip precedes claiming the MAC so a sibling
		// interface on the same MAC can still be rendered.
		if !isPrimary && strings.TrimSpace(iface.Name) == "" {
			warnSkip(warn, owner.Name, "secondary interface has no name in NetBox")
			continue
		}
		seen[strings.ToLower(mac)] = true

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
// hw-address, not DUID: Talos mints a fresh DUID-LLT every lease cycle.
type keaHost6 struct {
	HWAddress   string   `json:"hw-address"`
	IPAddresses []string `json:"ip-addresses"`
	Hostname    string   `json:"hostname"`
}

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

// dnsJSON is `generate dns --json`, in the shape Python emitted.
// DHCPReservations is only populated by --with-dhcp; omitted otherwise, so
// the plain `--json` shape is unchanged.
type dnsJSON struct {
	Addresses        []dnsAddressJSON `json:"addresses"`
	PTRRecords       []dnsPTRJSON     `json:"ptr_records"`
	DHCPReservations []reservation    `json:"dhcp_reservations,omitempty"`
}

type dnsAddressJSON struct {
	FQDN string `json:"fqdn"`
	IP   string `json:"ip"`
}

type dnsPTRJSON struct {
	ReverseARPA string `json:"reverse_arpa"`
	FQDN        string `json:"fqdn"`
}

// dnsJSONFromLines re-reads the rendered lines, so the outputs cannot drift.
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

// writeOut writes text to path, or to stdout when path is empty or "-".
func (o *Options) writeOut(path, text string) error {
	if path == "" || path == "-" {
		_, err := io.WriteString(o.Out, text)
		return err
	}
	return os.WriteFile(path, []byte(text), 0o644)
}

// pythonJSON marshals byte-identically to Python's json module, so the "did
// reservations change?" gate does not flap against refresh_kea.py's files. Go
// HTML-escapes <, > and & (Python does not), and Python's ensure_ascii=True
// escapes non-ASCII runes as \uXXXX (surrogate pairs above the BMP).
func pythonJSON(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	// Encode always appends a newline; the caller decides.
	return asciiEscape(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// asciiEscape matches Python's ensure_ascii=True. Structural JSON characters
// are all ASCII, so a flat scan can only touch string contents.
func asciiEscape(body []byte) []byte {
	if isASCII(body) {
		return body
	}
	var out bytes.Buffer
	out.Grow(len(body))
	for _, r := range string(body) {
		switch {
		case r < utf8.RuneSelf:
			out.WriteByte(byte(r))
		case r > 0xFFFF:
			r -= 0x10000
			fmt.Fprintf(&out, "\\u%04x\\u%04x",
				0xD800+(r>>10), 0xDC00+(r&0x3FF))
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.Bytes()
}

func isASCII(body []byte) bool {
	for _, b := range body {
		if b >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// writeJSON writes a trailing newline, as `print(json.dumps(...))` gives; Kea
// array files must not — see writeJSONNoNewline.
func (o *Options) writeJSON(path string, value any, indent string) error {
	body, err := pythonJSON(value, indent)
	if err != nil {
		return err
	}
	return o.writeOut(path, string(body)+"\n")
}

// writeJSONNoNewline omits the trailing newline, exactly as refresh_kea.py's
// `json.dump(hosts, f, indent=1)` does: the Kea files feed a change
// comparison, so a spurious byte makes every run look like a change.
func (o *Options) writeJSONNoNewline(path string, value any, indent string) error {
	body, err := pythonJSON(value, indent)
	if err != nil {
		return err
	}
	return o.writeOut(path, string(body))
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
			"machine, plus ipmi.<host> records for out-of-band interfaces. This is\n" +
			"what the core machines' dnsmasq include is refreshed from.\n\n" +
			"--with-dhcp appends dhcp-host reservations. Kea serves DHCP now and\n" +
			"dnsmasq ignores dhcp-host without a dhcp-range, so that is only useful\n" +
			"for a dnsmasq that actually hands out leases.",
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
	lines := dnsLines(dns.Hosts(), dnsDomain(domain), func(hostname, reason string) {
		log.Warn("skipping netbox record", "host", hostname, "reason", reason)
	})
	// An empty response would wipe internal DNS; keep the last-good file.
	if len(lines) == 0 {
		return fmt.Errorf("NetBox returned no usable DNS records; refusing to render")
	}

	// Fetched before the --json branch: --with-dhcp --json used to silently
	// drop the DHCP reservations because the JSON return happened first.
	var dhcpReservations []reservation
	if withDHCP {
		dhcp, err := client.FetchDHCP(ctx)
		if err != nil {
			return err
		}
		dhcpReservations = reservations(dhcp.AllInterfaces(), func(hostname, reason string) {
			log.Warn("skipping netbox record", "host", hostname, "reason", reason)
		})
	}

	if jsonOut {
		out := dnsJSONFromLines(lines)
		if withDHCP {
			out.DHCPReservations = dhcpReservations
		}
		return o.writeJSON(output, out, "  ")
	}

	body := lines
	if withDHCP {
		body = append([]string{dnsmasqHeader}, lines...)
		for _, r := range dhcpReservations {
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

	log := o.Logger()
	res := reservations(dhcp.AllInterfaces(), func(hostname, reason string) {
		log.Warn("skipping netbox record", "host", hostname, "reason", reason)
	})
	// An empty response would wipe every reservation; keep the last-good file.
	if len(res) == 0 {
		return fmt.Errorf("NetBox returned no usable DHCP reservations; refusing to render")
	}

	switch format {
	case "json":
		return o.writeJSON(output, res, "  ")

	case "kea":
		hosts4, hosts6 := keaReservations(res)
		// refresh_kea.py writes the arrays as separate files, indent=1.
		if output4 != "" || output6 != "" {
			if output4 != "" {
				if err := o.writeJSONNoNewline(output4, hosts4, " "); err != nil {
					return err
				}
			}
			if output6 != "" {
				if err := o.writeJSONNoNewline(output6, hosts6, " "); err != nil {
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
