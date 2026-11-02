# BPF masquerading on sea1

The sea1 Cilium Helm values enable `bpf.masquerade: true` and
`bpf.hostLegacyRouting: false`. This fixes LAN-to-hostPort Consul RPC replies
that the iptables path re-SNATs to an ephemeral source port.

Two Talos prerequisites live in `infrastructure/talos/sea1/talconfig.yaml`:

- `machine.features.hostDNS.forwardKubeDNSToHost: false`. CoreDNS already has
  explicit public upstreams and separate internal, reverse and Consul forwards;
  preserve that Corefile when changing Talos configuration.
- `net.bridge.bridge-nf-call-iptables: "0"` and
  `net.bridge.bridge-nf-call-ip6tables: "0"` in `machine.sysctls`. With these
  hooks enabled, BPF SNAT on `br0` breaks DNS and subnet routing. Guest L2
  traffic should remain transparent; Cilium enforces pod policy separately.

Check runtime state, not only the machine config: `talosctl get hostdnsconfigs`
should show only the loopback listener, and the two bridge sysctls must read
zero on the host. `cilium-dbg status` must report BPF masquerading on `br0`.
Disabling hostDNS forwarding alone is insufficient. Keeping legacy host routing
also failed the bridge-netfilter canary.

For future datapath changes, canary one node with `CiliumNodeConfig`, then
restart only that node's agent. Keep redundant CoreDNS and Tailscale connector
pods off the canary until tests pass. Changing Helm values alone does not
restart agents with this chart configuration; roll them explicitly one at a time.

Require cluster/public/internal/PTR DNS and direct UDP/TCP DNS over both IP
families, HTTPS TCP egress, node-API policy denial, Consul RPC from sea1-core,
and Cilium node/endpoint health. Observe guest services from both the LAN and
desktop, check monitor freshness, and compare guest boot IDs. Desktop subnet
failures do not establish a LAN outage. Stop on either failed validation or
unexplained guest restarts, and identify concurrent guest maintenance before
continuing.

IPv6 BPF masquerading remains an upstream beta feature. Keep its explicit
IPv6 DNS/egress checks in the maintenance gate.
