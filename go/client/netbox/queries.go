package netbox

import "context"

// DNSQuery backs FetchDNS, field-compatible with refresh_dns.py.
const DNSQuery = `
query {
  device_list {
    name
    primary_ip4 { address }
    primary_ip6 { address }
    interfaces {
      name
      ip_addresses { address }
    }
  }
  virtual_machine_list {
    name
    primary_ip4 { address }
    primary_ip6 { address }
  }
}
`

// DHCPQuery backs FetchDHCP.
const DHCPQuery = `
query {
  interface_list {
    primary_mac_address { mac_address }
    name
    device { name primary_ip4 { address } }
    ip_addresses { address }
  }
  vm_interface_list {
    primary_mac_address { mac_address }
    name
    virtual_machine { name primary_ip4 { address } }
    ip_addresses { address }
  }
}
`

// DeviceConfigQuery backs DevicesByTag: Python's `_CONFIG_QUERY` on the NetBox
// 4.5 schema, where `device_list` takes `filters: DeviceFilter` instead of
// `tag:` and a cable termination exposes a `termination` union, not `_device`.
const DeviceConfigQuery = `
query ($filters: DeviceFilter) {
  device_list(filters: $filters) {
    name
    serial
    asset_tag
    config_context
    primary_ip4 { address }
    platform { slug name }
    tags { name slug }
    interfaces {
      name
      type
      description
      mode
      primary_mac_address { mac_address }
      lag { name }
      tagged_vlans { name vid }
      untagged_vlan { name vid }
      cable {
        id
        terminations {
          cable_end
          termination {
            ... on InterfaceType {
              name
              device { name }
            }
          }
        }
      }
      ip_addresses { address }
      vrf { name }
    }
  }
}
`

// DNSResult is the raw shape of the DNS query response.
type DNSResult struct {
	Devices         []Host `json:"device_list"`
	VirtualMachines []Host `json:"virtual_machine_list"`
}

// Hosts concatenates devices and VMs, devices first, as refresh_dns.py does.
func (r DNSResult) Hosts() []Host {
	out := make([]Host, 0, len(r.Devices)+len(r.VirtualMachines))
	out = append(out, r.Devices...)
	out = append(out, r.VirtualMachines...)
	return out
}

// DHCPResult is the raw shape of the DHCP query response.
type DHCPResult struct {
	Interfaces   []Interface `json:"interface_list"`
	VMInterfaces []Interface `json:"vm_interface_list"`
}

// AllInterfaces concatenates device and VM interfaces, devices first.
func (r DHCPResult) AllInterfaces() []Interface {
	out := make([]Interface, 0, len(r.Interfaces)+len(r.VMInterfaces))
	out = append(out, r.Interfaces...)
	out = append(out, r.VMInterfaces...)
	return out
}

// FetchDNS returns every device and VM with primary addresses, plus device
// interfaces (so IPMI addresses can be derived).
func (c *Client) FetchDNS(ctx context.Context) (*DNSResult, error) {
	var out DNSResult
	if err := c.Query(ctx, DNSQuery, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FetchDHCP returns every interface with a primary MAC, its IPs and its owner's
// primary IPv4.
func (c *Client) FetchDHCP(ctx context.Context) (*DHCPResult, error) {
	var out DHCPResult
	if err := c.Query(ctx, DHCPQuery, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DevicesByTag returns devices with any of the given tag slugs, plus
// interfaces, VLANs and cabling; no tags means every device.
func (c *Client) DevicesByTag(ctx context.Context, tagSlugs ...string) ([]Device, error) {
	var vars map[string]any
	if len(tagSlugs) > 0 {
		vars = map[string]any{
			"filters": map[string]any{
				"tags": map[string]any{
					"slug": map[string]any{"in_list": tagSlugs},
				},
			},
		}
	}

	var out struct {
		Devices []Device `json:"device_list"`
	}
	if err := c.Query(ctx, DeviceConfigQuery, vars, &out); err != nil {
		return nil, err
	}
	return out.Devices, nil
}
