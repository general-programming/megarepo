package model

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// deviceTypes mirrors the Python VENDOR_MAP: network.yml `type:` values
// barf knows how to model. Aliases map onto their canonical name.
var deviceTypes = map[string]string{
	"vyos":      "vyos",
	"edgeos":    "edgeos",
	"linux":     "linux",
	"external":  "external",
	"cisco":     "cisco",
	"cisco-ios": "cisco",
	"eos":       "eos",
	"dnos6":     "dnos6",
	"dnos-6":    "dnos6",
	"dnos9":     "dnos9",
	"dnos-9":    "dnos9",
	"mikrotik":  "mikrotik",
}

// hostKeys are the network.yml host keys Load translates into typed
// fields; everything else lands in Host.Raw so a partial port never
// silently drops configuration.
var hostKeys = map[string]bool{
	"type": true, "role": true, "site": true, "id": true, "asn": true,
	"address": true, "ip6_address": true, "interfaces": true,
	"nameservers": true, "networks": true, "extra_config": true,
	"cloud_init": true, "eapi_vrf": true, "location": true, "nat": true,
	"ospf": true, "static_routes": true, "bird": true, "bgp": true,
	"firewall": true,
}

// Load parses a network.yml file into hosts, links, and global metadata.
//
// It reproduces barf.util.network.load_network: profile anchors are
// merged into hosts, hosts inherit global nameservers, and links with no
// pinned port derive one from the two hosts' ids.
func Load(path string) (*Network, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("%s: empty document", path)
	}
	top := mapEntries(root.Content[0])

	global, err := parseGlobalMeta(lookup(top, "global_meta"))
	if err != nil {
		return nil, err
	}

	network := &Network{Global: global}
	for _, hostEntry := range mapEntries(lookup(top, "hosts")) {
		host, err := parseHost(hostEntry.key, hostEntry.val, global)
		if err != nil {
			return nil, err
		}
		network.Hosts = append(network.Hosts, *host)
	}

	links, err := parseLinks(lookup(top, "links"), network)
	if err != nil {
		return nil, err
	}
	network.Links = links

	return network, nil
}

func parseGlobalMeta(node *yaml.Node) (GlobalMeta, error) {
	entries := mapEntries(node)
	meta := GlobalMeta{
		SearchDomain: nodeString(lookup(entries, "search_domain")),
		Nameservers:  nodeStrings(lookup(entries, "nameservers")),
		SSHKeys:      nodeStrings(lookup(entries, "ssh_keys")),
		SNMPPublic:   nodeString(lookup(entries, "snmp_public")),
		SNMPContact:  nodeString(lookup(entries, "snmp_contact")),
		SNMPLocation: nodeString(lookup(entries, "snmp_location")),
	}
	if asn, ok := nodeInt(lookup(entries, "community_asn")); ok {
		meta.CommunityASN = asn
	}

	sites, err := parseSites(lookup(entries, "sites"))
	if err != nil {
		return meta, err
	}
	meta.Sites = sites
	if len(sites) > 0 && meta.CommunityASN == 0 {
		return meta, fmt.Errorf("global_meta: sites are defined but community_asn is" +
			" missing; site-origin large communities need a fabric-wide Global" +
			" Administrator value")
	}
	return meta, nil
}

func parseSites(node *yaml.Node) (map[string]Site, error) {
	sites := map[string]Site{}
	seen := map[int]string{}
	for _, siteEntry := range mapEntries(node) {
		fields := mapEntries(siteEntry.val)
		id, ok := nodeInt(lookup(fields, "id"))
		if !ok {
			return nil, fmt.Errorf("global_meta.sites: %q has no id", siteEntry.key)
		}
		if other, dup := seen[id]; dup {
			return nil, fmt.Errorf("global_meta.sites: id %d used by both %q and %q",
				id, other, siteEntry.key)
		}
		seen[id] = siteEntry.key

		coords := seqEntries(lookup(fields, "coords"))
		if len(coords) != 2 {
			return nil, fmt.Errorf("global_meta.sites: %q needs two coords", siteEntry.key)
		}
		var lat, lon float64
		if err := coords[0].Decode(&lat); err != nil {
			return nil, fmt.Errorf("global_meta.sites: %q latitude: %w", siteEntry.key, err)
		}
		if err := coords[1].Decode(&lon); err != nil {
			return nil, fmt.Errorf("global_meta.sites: %q longitude: %w", siteEntry.key, err)
		}
		sites[siteEntry.key] = Site{Name: siteEntry.key, ID: id, Coords: [2]float64{lat, lon}}
	}
	return sites, nil
}

func parseHost(hostname string, node *yaml.Node, global GlobalMeta) (*Host, error) {
	entries := mapEntries(node)

	rawType := nodeString(lookup(entries, "type"))
	deviceType, ok := deviceTypes[rawType]
	if !ok {
		return nil, fmt.Errorf("Invalid host type %s", rawType)
	}

	host := &Host{
		Hostname:     hostname,
		DeviceType:   deviceType,
		Role:         nodeString(lookup(entries, "role")),
		Site:         nodeString(lookup(entries, "site")),
		Networks:     nodeStrings(lookup(entries, "networks")),
		ExtraConfig:  nodeStrings(lookup(entries, "extra_config")),
		CloudInit:    nodeBool(lookup(entries, "cloud_init")),
		EAPIVRF:      nodeString(lookup(entries, "eapi_vrf")),
		SNMPLocation: nodeString(lookup(entries, "location")),
		Raw:          map[string]any{},
	}
	if id, ok := nodeInt(lookup(entries, "id")); ok {
		host.ID = id
	}
	if asn, ok := nodeInt(lookup(entries, "asn")); ok {
		host.ASN = asn
	}

	if host.Site != "" {
		if _, known := global.Sites[host.Site]; !known {
			return nil, fmt.Errorf("hosts: '%s' has unknown site '%s'", hostname, host.Site)
		}
	}

	var err error
	if host.AddressRaw = nodeString(lookup(entries, "address")); host.AddressRaw != "" {
		address, err := ParseAddress(host.AddressRaw)
		if err != nil {
			return nil, fmt.Errorf("hosts: %s: %w", hostname, err)
		}
		host.Address = &address
	}
	if host.IP6AddressRaw = nodeString(lookup(entries, "ip6_address")); host.IP6AddressRaw != "" {
		address, err := ParseAddress(host.IP6AddressRaw)
		if err != nil {
			return nil, fmt.Errorf("hosts: %s: %w", hostname, err)
		}
		host.IP6Address = &address
	}

	// Hosts without their own nameservers inherit the global set.
	host.Nameservers = nodeStrings(lookup(entries, "nameservers"))
	if lookup(entries, "nameservers") == nil {
		host.Nameservers = global.Nameservers
	}

	if host.Interfaces, err = parseInterfaces(hostname, lookup(entries, "interfaces")); err != nil {
		return nil, err
	}
	host.NATMasquerades, host.NATPortForwards = parseNAT(lookup(entries, "nat"))
	host.OSPF = parseOSPF(lookup(entries, "ospf"))
	host.StaticRoutes = parseStaticRoutes(lookup(entries, "static_routes"))
	host.Bird = parseBird(lookup(entries, "bird"))
	host.BGP = parseBGP(lookup(entries, "bgp"))
	host.Firewall = parseFirewall(lookup(entries, "firewall"))

	for _, e := range entries {
		if !hostKeys[e.key] {
			host.Raw[e.key] = nodeAny(e.val)
		}
	}

	return host, nil
}

func parseInterfaces(hostname string, node *yaml.Node) ([]Interface, error) {
	var out []Interface
	for _, item := range seqEntries(node) {
		fields := mapEntries(item)

		iface := Interface{
			Name:         nodeString(lookup(fields, "name")),
			Type:         "VPNLink",
			Description:  nodeString(lookup(fields, "description")),
			DHCP:         nodeBool(lookup(fields, "dhcp")),
			DHCPv6:       nodeBool(lookup(fields, "dhcpv6")),
			IPv6Autoconf: nodeBool(lookup(fields, "ipv6_autoconf")),
			Management:   nodeBool(lookup(fields, "management")),
			Members:      nodeStrings(lookup(fields, "members")),
			Enabled:      true,
			Wireguard:    nodeAnyMap(lookup(fields, "wireguard")),
			RA:           nodeAnyMap(lookup(fields, "ra")),
		}
		if typeNode := lookup(fields, "type"); typeNode != nil {
			iface.Type = nodeString(typeNode)
		}
		if enabled := lookup(fields, "enabled"); enabled != nil {
			iface.Enabled = nodeBool(enabled)
		}
		if mtu, ok := nodeInt(lookup(fields, "mtu")); ok {
			iface.MTU = mtu
		}
		if vid, ok := nodeInt(lookup(fields, "vlan")); ok {
			iface.UntaggedVLAN = &VLAN{VID: vid}
		}
		for _, tagged := range seqEntries(lookup(fields, "tagged_vlans")) {
			if vid, ok := nodeInt(tagged); ok {
				iface.TaggedVLANs = append(iface.TaggedVLANs, VLAN{VID: vid})
			}
		}

		// A single `address` and/or a list of `addresses`, any mix of
		// families; the merged list keeps network.yml order.
		raw := []string{nodeString(lookup(fields, "address"))}
		raw = append(raw, nodeStrings(lookup(fields, "addresses"))...)
		seen := map[string]bool{}
		for _, text := range raw {
			if text == "" || seen[text] {
				continue
			}
			seen[text] = true
			address, err := ParseAddress(text)
			if err != nil {
				return nil, fmt.Errorf("hosts: %s: interface %s: %w", hostname, iface.Name, err)
			}
			iface.Addresses = append(iface.Addresses, address)
		}

		out = append(out, iface)
	}
	return out, nil
}

func parseNAT(node *yaml.Node) ([]NATMasquerade, []PortForward) {
	entries := mapEntries(node)

	var masquerades []NATMasquerade
	rule := 10
	for _, item := range seqEntries(lookup(entries, "masquerade")) {
		fields := mapEntries(item)
		if explicit, ok := nodeInt(lookup(fields, "rule")); ok {
			rule = explicit
		}
		destinations := nodeStrings(lookup(fields, "destinations"))
		if destinations == nil {
			destinations = []string{nodeString(lookup(fields, "destination"))}
		}
		for _, destination := range destinations {
			masquerades = append(masquerades, NATMasquerade{
				Rule:        rule,
				Interface:   nodeString(lookup(fields, "interface")),
				Source:      nodeString(lookup(fields, "source")),
				Destination: destination,
			})
			rule++
		}
	}

	var forwards []PortForward
	rule = 100
	for _, item := range seqEntries(lookup(entries, "port_forwards")) {
		fields := mapEntries(item)
		if explicit, ok := nodeInt(lookup(fields, "rule")); ok {
			rule = explicit
		}
		protocols := nodeStrings(lookup(fields, "protocols"))
		if protocols == nil {
			protocol := nodeString(lookup(fields, "protocol"))
			if protocol == "" {
				protocol = "tcp"
			}
			protocols = []string{protocol}
		}
		port, _ := nodeInt(lookup(fields, "port"))
		translationPort, _ := nodeInt(lookup(fields, "translation_port"))
		for _, protocol := range protocols {
			forwards = append(forwards, PortForward{
				Rule:            rule,
				Name:            nodeString(lookup(fields, "name")),
				Interface:       nodeString(lookup(fields, "interface")),
				Protocol:        protocol,
				Port:            port,
				To:              nodeString(lookup(fields, "to")),
				TranslationPort: translationPort,
			})
			rule++
		}
	}

	return masquerades, forwards
}

func parseOSPF(node *yaml.Node) OSPFConfig {
	entries := mapEntries(node)
	var config OSPFConfig
	for _, item := range seqEntries(lookup(entries, "networks")) {
		fields := mapEntries(item)
		config.Networks = append(config.Networks, OSPFNetwork{
			Network: nodeString(lookup(fields, "network")),
			Area:    nodeString(lookup(fields, "area")),
		})
	}
	for _, item := range seqEntries(lookup(entries, "interfaces")) {
		fields := mapEntries(item)
		config.Interfaces = append(config.Interfaces, OSPFInterface{
			Name:      nodeString(lookup(fields, "name")),
			MTUIgnore: nodeBool(lookup(fields, "mtu_ignore")),
		})
	}
	for _, item := range seqEntries(lookup(entries, "redistribute")) {
		fields := mapEntries(item)
		metricType, _ := nodeInt(lookup(fields, "metric_type"))
		config.Redistribute = append(config.Redistribute, OSPFRedistribute{
			Protocol:   nodeString(lookup(fields, "protocol")),
			MetricType: metricType,
		})
	}
	return config
}

func parseStaticRoutes(node *yaml.Node) []StaticRoute {
	var out []StaticRoute
	for _, item := range seqEntries(node) {
		fields := mapEntries(item)
		out = append(out, StaticRoute{
			Network:   nodeString(lookup(fields, "network")),
			Interface: nodeString(lookup(fields, "interface")),
			NextHop:   nodeString(lookup(fields, "next-hop")),
		})
	}
	return out
}

func parseBird(node *yaml.Node) BirdConfig {
	entries := mapEntries(node)
	return BirdConfig{
		RouterID:            nodeString(lookup(entries, "router_id")),
		KrtPrefsrc:          nodeString(lookup(entries, "krt_prefsrc")),
		MergePaths:          nodeBool(lookup(entries, "merge_paths")),
		ImportFilter:        nodeString(lookup(entries, "import_filter")),
		ImportCheckFunction: nodeString(lookup(entries, "import_check_function")),
		LocalConf:           nodeString(lookup(entries, "local_conf")),
	}
}

func parseBGP(node *yaml.Node) BGPConfig {
	entries := mapEntries(node)
	config := BGPConfig{Instance: nodeString(lookup(entries, "instance"))}
	for _, item := range seqEntries(lookup(entries, "transit")) {
		fields := mapEntries(item)
		remoteAS, _ := nodeInt(lookup(fields, "remote_as"))
		config.Transit = append(config.Transit, TransitPeer{
			Name:           nodeString(lookup(fields, "name")),
			RemoteAS:       remoteAS,
			LinkNetwork:    nodeString(lookup(fields, "link_network")),
			RemoteAddress:  nodeString(lookup(fields, "remote_address")),
			ExportSupernet: nodeString(lookup(fields, "export_supernet")),
		})
	}
	return config
}

func parseFirewall(node *yaml.Node) FirewallGroups {
	groups := mapEntries(lookup(mapEntries(node), "groups"))

	var out FirewallGroups
	for _, groupEntry := range mapEntries(lookup(groups, "address")) {
		group := FirewallAddressGroup{Name: groupEntry.key}
		for _, member := range seqEntries(groupEntry.val) {
			if member.Kind == yaml.MappingNode {
				fields := mapEntries(member)
				group.Members = append(group.Members, FirewallAddressMember{
					Address: nodeString(lookup(fields, "address")),
					Comment: nodeString(lookup(fields, "comment")),
				})
				continue
			}
			group.Members = append(group.Members, FirewallAddressMember{Address: nodeString(member)})
		}
		out.Address = append(out.Address, group)
	}
	for _, groupEntry := range mapEntries(lookup(groups, "interface")) {
		group := FirewallInterfaceGroup{Name: groupEntry.key}
		if groupEntry.val.Kind == yaml.MappingNode {
			fields := mapEntries(groupEntry.val)
			group.Interfaces = nodeStrings(lookup(fields, "interfaces"))
			group.Include = nodeStrings(lookup(fields, "include"))
		} else {
			group.Interfaces = nodeStrings(groupEntry.val)
		}
		out.Interface = append(out.Interface, group)
	}
	return out
}

// parseLinks expands the nested links: {uplink_host: {peer_host: spec}}
// mapping. The INNER key is the uplink and becomes side A, keeping the
// historical spine-first ordering that decides numbered-link addressing.
func parseLinks(node *yaml.Node, network *Network) ([]Link, error) {
	hostNamed := func(name, context string) (*Host, error) {
		host, ok := network.Host(name)
		if !ok {
			return nil, fmt.Errorf("links: unknown host '%s' (in %s)", name, context)
		}
		return host, nil
	}

	var links []Link
	seenPorts := map[int]string{}
	for _, outer := range mapEntries(node) {
		sideB, err := hostNamed(outer.key, "links")
		if err != nil {
			return nil, err
		}
		for _, inner := range mapEntries(outer.val) {
			sideA, err := hostNamed(inner.key, outer.key)
			if err != nil {
				return nil, err
			}

			link := Link{A: sideA.Hostname, B: sideB.Hostname}

			var spec []entry
			pinnedPort, pinned := nodeInt(inner.val)
			if !pinned {
				spec = mapEntries(inner.val)
				pinnedPort, pinned = nodeInt(lookup(spec, "port"))
			}

			link.Pinned = pinned
			if pinned {
				link.Port = pinnedPort
			} else {
				if link.Port, err = derivedPort(sideA, sideB); err != nil {
					return nil, err
				}
			}
			link.Network = nodeString(lookup(spec, "network"))
			link.Secret = nodeString(lookup(spec, "secret"))
			link.IPsec = nodeBool(lookup(spec, "ipsec"))

			if other, dup := seenPorts[link.Port]; dup {
				return nil, fmt.Errorf("links: port %d used by both %s and %s",
					link.Port, other, outer.key)
			}
			seenPorts[link.Port] = outer.key

			links = append(links, link)
		}
	}
	return links, nil
}

// derivedPort is 51000 + min(id)*64 + max(id): collision-free by
// construction, stable forever, and both sides agree.
func derivedPort(sideA, sideB *Host) (int, error) {
	for _, host := range []*Host{sideA, sideB} {
		if host.ID == 0 {
			return 0, fmt.Errorf("links: '%s' needs an `id` to derive the %s<->%s port"+
				" (or pin the port explicitly)", host.Hostname, sideA.Hostname, sideB.Hostname)
		}
	}
	ids := []int{sideA.ID, sideB.ID}
	sort.Ints(ids)
	return 51000 + ids[0]*64 + ids[1], nil
}
