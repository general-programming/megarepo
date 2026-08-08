# bpf.masquerade on sea1: canary findings and prerequisites

Status: reverted 2026-08-06 (tried in aeff6eb0, reverted in 7e420b62). This
records why, and what the next attempt must do differently.

## Why we want it

The iptables masquerade path SNATs hostPort *replies* to a fresh source port
for LAN peers that are not cluster nodes, which silently kills every consul
RPC from sea1-core. bpf.masquerade fixes that.

## What happened on the k8s-0 canary

Port 53 broke, and only port 53. From a pod on that node, TCP to 1.1.1.1:443
was fine while BOTH udp/53 and tcp/53 to 1.1.1.1 and 8.8.8.8 timed out — so
this is not "UDP egress is broken", it is something intercepting :53.

## The unmet prerequisite

The canary ran with `forwardKubeDNSToHost` **true** on all three nodes, not
false as the plan assumed. It is a talhelper/Talos default and was never
overridden — there is no hostDNS block in
`infrastructure/talos/sea1/talconfig.yaml`, and all three rendered configs
under `clusterconfig/` carry:

```yaml
hostDNS:
  enabled: true
  forwardKubeDNSToHost: true
```

Confirmed live: the host nftables `table inet talos` ingress chain is policy
drop, with explicit accepts for pod CIDR → 169.254.116.108:53 (udp handle
1397, tcp handle 1404) — those rules exist only because forwardKubeDNSToHost
is on. 169.254.116.108:53 answers from a pod today, and Talos init holds the
listener.

Ruled out while confirming this: there is no DNAT/redirect for :53 in any
table, and cilium's DNS proxy is not the interceptor — the TPROXY rules are
mark-gated and sit at 0 packets, with zero toFQDNs policies cluster-wide.

## Next attempt

1. Set `forwardKubeDNSToHost: false` first (the actual prerequisite) and give
   CoreDNS explicit upstreams so '.' no longer depends on the node resolver.
2. Confirm pod DNS still resolves.
3. Only then canary `bpf.masquerade` on one node.

Do not jump to `hostDNS.enabled: false` — that is a bigger hammer than the
evidence justifies. Note bpf.masquerade also flips host routing Legacy → BPF
and makes IPv6 masquerade (upstream BETA) load-bearing for all pod v6 egress.
