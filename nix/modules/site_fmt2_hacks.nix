{ ... }:

# site_fmt2_hacks — fmt2-specific networking workarounds that don't belong in
# the general-purpose modules. Keep each hack self-contained and heavily
# commented, with a note on what the *real* fix is, so it can be ripped out
# once the underlying issue is resolved.

# ---------------------------------------------------------------------------
# metallb external VIP (79.110.170.65) host route
# ---------------------------------------------------------------------------
# 79.110.170.65 is a MetalLB *BGP-mode* LoadBalancer VIP (pool
# `fmt2-pool-external`; see argocd/apps/infra/metallb/fmt2/bgp-external.yaml).
# It is a /32 that lives *inside* fmt2-core's directly-connected
# 79.110.170.0/24 on eno1, so the kernel treats it as on-link and ARPs for it
# on the primary segment. Nothing answers:
#   - BGP-mode MetalLB does not reply to ARP (that's L2 mode's job), and
#   - no k8s node has a leg on that L2 — the Talos nodes are all single-homed
#     on 10.65.67.0/24 (infrastructure/talos/fmt2/talconfig.yaml).
# Net result: the VIP is unreachable from this box even though it works fine
# from off-subnet clients.
#
# The address *is* routable: the upstream router 10.65.67.36 (BGPPeer
# `fmt2-bgp-external`, peerASN 208590) learns it over BGP from the speakers.
# fmt2-core reaches that router on-link via vlan5 (10.65.67.5/24), so we plant
# a more-specific /32 route pointing at it. The /32 beats the connected /24,
# so traffic to the VIP is handed to the router (-> node) instead of ARPing
# into the void. Egress via vlan5 also sources from 10.65.67.5, keeping the
# return path symmetric (the node's reply stays on the 10.65.67.0/24 L2).
#
# REAL FIX: move fmt2-pool-external to a /32 outside 79.110.170.0/24 so it is
# simply routed, and delete this module. Until then, this keeps the VIP
# reachable from fmt2-core itself.
#
# NOTE: this merges into the vlan5 network defined in
# machines/fmt2-core/configuration.nix ("20-vlan5"); `.routes` is a list
# option so this route is appended to the ones declared there.

{
  systemd.network.networks."20-vlan5".routes = [
    {
      Destination = "79.110.170.65/32";
      Gateway = "10.65.67.36";
    }
  ];
}
