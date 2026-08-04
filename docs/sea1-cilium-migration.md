# SEA1: decommission wobscale, then flannel → Cilium (native routing)

Migration plan for the `sea1` Talos cluster. Every "current state" claim was read
off the live cluster, not inferred from the repo.

## Why

- **NetworkPolicy is inert.** flannel has no policy engine. SEA1 carries 16
  `NetworkPolicy` objects (argocd ×8, mastodon-coolmathgames ×4, sentry,
  victoriametrics ×2, monitoring) that the API server accepts and nothing
  enforces. `docs/akvorado.md` had to *skip* shipping a policy for the
  unauthenticated flow-collector UDP ports for exactly this reason.
- **Two standing workarounds are flannel-shaped.** `mss-clamp` (reapplies
  `ip6tables TCPMSS` on `flannel-v6.1` every 60s) and the
  `tx-checksum-ip-generic: false` `EthernetConfig` patch on every VM node
  (flannel-io/flannel#1279).
- **No dataplane observability.** Hubble gives flow-level visibility that
  akvorado cannot — akvorado sees fabric, not overlay.

## The decisive finding

**`sea1-k8s-103-0` (wobscale rack) is the only reason SEA1 needs an overlay.**
It is the sole node off the site L2, in *both* address families:

| Node | v4 | v6 |
|---|---|---|
| k8s-0 / k8s-1 / k8s-2 | `10.3.2.10/.11/.12` | `2602:fa6d:10:ffff::110/111/112` |
| cp-0 / cp-1 | `10.3.2.16/.17` | `2602:fa6d:10:ffff::116/117` |
| **103-0** | **`10.3.6.3`** | **`2620:fc:c000:0:be24:11ff:fe63:ec8e`** |

Remove it and all five survivors share one L2 per family, which unlocks
`routingMode: native` + `autoDirectNodeRoutes`: pod→pod goes direct via an
on-link next-hop, pod→elsewhere masquerades to the node address, and **the router
is never involved.** No BGP, no encapsulation, no MTU games.

This is why the drain comes first: doing it after a tunnel-mode install would
cost a second full pod-recycle to convert.

## v6-first posture

Node IPv6 addresses are the real addressing; IPv4 is a stopgap for single-stack
v4 clients.

**In scope:**
- Both `ipv4NativeRoutingCIDR` and `ipv6NativeRoutingCIDR` set. Cilium installs
  direct node routes **per family**, so v6 pod traffic resolves to v6 node
  next-hops with no v4 in the path. Verify on the wire; do not assume.
- Phase C's `k8sServiceHost` is a v6 literal or v6-resolving name.
- Verification runs v6 first, v4 second.
- Rook's `network.rook.io/mon-ip` v6 annotations stay — already the v6-first
  idiom, and what stops mons binding the v4 address.

**Out of scope — separate project.** Flipping the *Kubernetes primary family*
(reordering `podSubnets` / `serviceSubnets`). v4 is first today, hence primary,
which is precisely why Rook needs those annotations. Reordering `serviceSubnets`
reallocates every ClusterIP, meaning **every Service is recreated**. Bigger and
riskier than the CNI swap; it must not ride along.

**Later:** the real v6 endgame is globally routable pod v6 out of
`2602:fa6d:10::/48` instead of ULA `fd40:10:244::/56` — no v6 masquerade, true
end-to-end v6. With five nodes on one L2 that needs five static routes on the
router, no BGP. It pairs naturally with native routing but renumbers every pod,
so it belongs after this settles.

## Current state (verified)

| Thing | Value |
|---|---|
| Talos / k8s | v1.13.7 / v1.36.2 |
| CNI | Talos-default flannel, VXLAN port 4789, `EnableIPv6: true` |
| Pod subnets | `10.244.0.0/16`, `fd40:10:244::/56` (per-node /24 + /64 from kube-controller-manager) |
| Service subnets | `10.96.0.0/12`, `d40:10:96::/108` (v4 primary) |
| kube-proxy | present, 6 nodes, iptables |
| etcd members | 3 — `cp-2` was nuked in the hyperconverged merger onto `k8s-2` |
| Pods / VMIs | 462 pods (92 hostNetwork) / 6 VMIs |
| Multus | thick DS, `lan-bridge=true` nodes only (`k8s-0/1/2`) |
| Multus `clusterNetwork` | **hardcoded** `/host/etc/cni/net.d/10-flannel.conflist` |
| `/etc/cni` | overlay on `/var/system/overlays/etc-cni-diff` → EPHEMERAL, **survives reboot** |
| Cilium version | **1.20.0** — supports k8s 1.33–1.36, verified against the release requirements matrix |

### Decommission cost, measured

- **Load:** 104 pods, 15.7 CPU / 44 GB requests (65% / 32% of the node).
- **Where it goes:** `k8s-2` sits at **4% CPU / 0% memory** of 68 cores /
  778 GiB. It absorbs all of 103-0 unaided. `cp-0`/`cp-1` are 4 CPU / 8 GB and
  `NoSchedule` — they take nothing.
- **Immovable data: exactly two local-path PVs**, both replicas of replicated
  stores, neither a singleton:
  - `librenms/storage-mariadb-librenms-2` (16Gi) — siblings `-0`/`-1` on k8s-0/1
  - `shared-db/data-scylladb-sea1-sea1-1` (100Gi) — Scylla replica
- **No ceph OSDs** on 103-0, only CSI plugins. Ceph is untouched by the drain.
- **Real cost:** the off-rack failure domain. All five survivors end in one rack.
  The control plane already lost this when `cp-2` was nuked, so for the CP it is
  bookkeeping; for workloads it is a genuine regression, accepted deliberately.

## Risks

**R1 — 16 NetworkPolicies would go live the instant Cilium enforces.**
Never enforced before; argocd's eight are upstream defaults written against a
different topology. If one is wrong, ArgoCD — the thing that would fix it — is
what breaks.
*Decision:* the policy review is deliberately deferred to phase D and gates only
the enforcement flip. Install `policyEnforcementMode: never`, run the whole
migration that way, then audit with Hubble against real observed traffic
(`hubble observe --verdict DROPPED`) rather than by reading YAML.

**R2 — the Multus delegate is a hardcoded path to flannel's conflist.** Sharpest
edge in the migration. Read live off `sea1-k8s-0`:

```json
{"name":"multus-cni-network","type":"multus-shim",
 "clusterNetwork":"/host/etc/cni/net.d/10-flannel.conflist", ...}
```

`00-multus.conf` sorts *first*, so on `k8s-0/1/2` the CRI calls Multus, and
Multus calls whatever that absolute path names — baked in at generation time, not
a live lookup. Therefore:

- Deleting `10-flannel.conflist` **before** repointing Multus breaks pod
  networking outright on all three bridge nodes.
- Leaving it pointed at flannel after flannel is gone is equally broken.
- We run `cni.exclusive: false` (below), so nothing cleans that file up for us,
  and it lives on a reboot-surviving overlay. It must be removed deliberately,
  per node.

*Mitigation:* pin `clusterNetwork` to `05-cilium.conflist` **explicitly** in
`argocd/apps/infra/multus/base/talos-fixes.yaml` rather than trusting
auto-detect. Ordering: Cilium up → repoint + restart Multus → verify → remove the
flannel conflist → reboot.

**R3 — `cni.exclusive: false` is mandatory.** Multus stays, because KubeVirt LAN
guests depend on the bridge (L2) NADs. The chart default `true` makes Cilium move
every foreign conflist aside — **including `00-multus.conf`** — stripping the LAN
interface off every VM guest on `k8s-0/1/2`.

**R4 — KubeVirt LAN guests are what R2/R3 actually break.** A Multus misfire
attaches a VM to an empty, uplink-less bridge and it *looks* perfectly healthy.
"VMI is Running" is not evidence. Verify by DHCP lease and real traffic, per
guest, per node.

**R5 — every pod must be re-networked.** Pods keep flannel IPs until their
sandbox is recreated: a rolling node-by-node reboot, not a hot swap. etcd is 3
members with **zero spare** — one CP at a time, fully Ready before the next,
never while ceph is degraded.

**R6 — ceph.** 37 hostNetwork pods; mons bind node v6 addresses via the
`mon-ip` annotations. Unaffected in principle, but `HEALTH_OK` is a precondition
for every node reboot.

## Phase A — decommission wobscale

Blocks everything else.

1. Rebuild `shared-db/data-scylladb-sea1-sea1-1` elsewhere via Scylla's
   **replace-node** procedure — not a plain PVC delete. Wait for full stream and
   `nodetool status` UN.
2. Rebuild `librenms/storage-mariadb-librenms-2` via mariadb-operator; confirm
   replication caught up.
3. `kubectl drain sea1-k8s-103-0 --ignore-daemonsets --delete-emptydir-data`.
   Confirm the other 102 pods rescheduled healthy — especially
   `argocd-application-controller-0`, netbox, the sentry statefulsets, and the
   `ts-*` tailscale-operator proxies.
4. `kubectl delete node sea1-k8s-103-0`, `talosctl reset` the box, remove its
   stanza from `infrastructure/talos/sea1/talconfig.yaml`.
5. Confirm no `10.3.6.x` / `2620:fc:c000::` address survives in cluster state.

The `no-database` / `no-lan-vip` / `no-virt` labels existed solely for 103-0.
With it gone their `DoesNotExist` selectors match everything, so ~10 apps carry
now-inert affinity blocks. Leave them — harmless, and useful if another odd node
ever appears.

## Phase B — Cilium 1.20.0, native

1. etcd snapshot, fresh `talosctl support` bundle, and capture the validation
   matrix **baseline on flannel** so post-cutover results are comparable.
2. Add the ArgoCD app at `argocd/apps/infra/cilium/{base,sea1}` following the
   `traefik-helm` / `consul` pattern. Sync with `selfHeal` **off** for the
   migration. Key values:

   ```yaml
   ipam: {mode: kubernetes}            # reuse existing podCIDRs -- addressing does not move
   k8s: {requireIPv4PodCIDR: true, requireIPv6PodCIDR: true}
   ipv4: {enabled: true}
   ipv6: {enabled: true}
   routingMode: native                 # no encapsulation at all
   autoDirectNodeRoutes: true          # valid only because 103-0 is gone
   ipv4NativeRoutingCIDR: 10.244.0.0/16
   ipv6NativeRoutingCIDR: fd40:10:244::/56
   enableIPv4Masquerade: true
   enableIPv6Masquerade: true
   kubeProxyReplacement: false         # phase C
   policyEnforcementMode: never        # phase D
   cni: {exclusive: false}             # MANDATORY -- Multus coexistence, R3
   cgroup: {autoMount: {enabled: false}, hostRoot: /sys/fs/cgroup}
   securityContext:
     capabilities:
       ciliumAgent: [CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,PERFMON,BPF,DAC_OVERRIDE,FOWNER,SETGID,SETUID]
       cleanCiliumState: [NET_ADMIN,SYS_ADMIN,SYS_RESOURCE]
   hubble: {relay: {enabled: true}, ui: {enabled: true}}
   ```

   The `cgroup` and `securityContext` blocks are the Talos requirement — Cilium
   cannot mount cgroups itself there.

   Bootstrap caveat worth writing into the app's README: this creates a
   dependency where a from-scratch SEA1 rebuild needs Cilium installed by hand
   (or as a Talos inline manifest) *before* ArgoCD can run. Do not discover that
   during a disaster.

3. Add `cluster.network.cni.name: none` to talconfig, `talhelper genconfig`,
   **diff the generated config** (talhelper has silently dropped fields before —
   see the bridgePort note in that file), then `talosctl apply-config`. This only
   stops reconciliation; the running flannel DS is untouched.

4. Verify `05-cilium.conflist` exists on every node **and** `00-multus.conf`
   still exists on `k8s-0/1/2`. If Multus's conf vanished, `cni.exclusive` was
   wrong — stop.

5. Repoint Multus `clusterNetwork` → `05-cilium.conflist`, sync, rollout restart,
   and confirm the on-disk file actually changed on all three nodes. **Last
   fully-reversible moment.**

6. `kubectl -n kube-system delete ds kube-flannel`. Running pods keep working —
   their veths and routes already exist.

7. Roll nodes one at a time: `cp-0` → `cp-1` → `k8s-0` → `k8s-1` → `k8s-2`
   (metal; VMs + ceph + CP; last). Control planes lead because they carry no VMs
   and no OSDs, making the first native-routing proof cheap.

   Per node: ceph `HEALTH_OK` → live-migrate VMIs off → drain → **remove
   `/etc/cni/net.d/10-flannel.conflist`** → `talosctl reboot` → Ready,
   `cilium status` clean, pods hold `10.244.x`/`fd40:…` from Cilium → uncordon →
   validation matrix → next.

   After the first node also confirm `flannel.1` / `flannel-v6.1` / `cni0` are
   gone, and that direct node routes exist for **both** families with v6 routes
   using v6 next-hops.

8. Per KubeVirt guest as its node returns: LAN interface up, DHCP lease held,
   real traffic passing, live migration works (R4). The 2× vpn-spine / 2×
   vpn-leaf guests carry the site VRRP — most care there.

9. ArgoCD `selfHeal` back on.

## Phase C — kube-proxy replacement (optional)

Only after phase B is stable for a few days.

- talconfig `cluster: {proxy: {disabled: true}}`
- Cilium `kubeProxyReplacement: true`, `k8sServiceHost`/`k8sServicePort` (v6)
- Delete the kube-proxy DS; clear stale `KUBE-*` chains via reboot
- Re-verify MetalLB L2 for `akvorado-flows` (10.3.3.1) and its source-IP
  preservation specifically — akvorado keys on exporter source address, and
  getting it wrong collapses the fabric into one bogus exporter.

## Phase D — collect the winnings

1. NetworkPolicy review via Hubble against live traffic → fix what would drop →
   `policyEnforcementMode: default` as its own change. Expect argocd's eight to
   need work.
2. Ship the akvorado NetworkPolicy `docs/akvorado.md` had to omit; correct that
   section of the doc.
3. Delete `argocd/apps/infra/mss-clamp/` — with 103-0 gone and no encapsulation,
   its entire reason for existing is gone.
4. Remove the `vm-ens18-checksum` patches from both `talconfig.yaml` files — moot
   without encapsulation, but test on one node first with sustained cross-node
   UDP/DNS before doing it fleet-wide.
5. Hubble UI via traefik; consider `bpf.masquerade`; consider FMT2 next.
6. The two v6-first follow-ons: primary-family flip, and globally routable pod v6.

## Validation matrix

Baseline on flannel first, then re-run after each node. **v6 first, v4 second.**

- pod → pod, same node and cross node, v6 and v4
- pod → ClusterIP, v6 and v4
- pod → external, v6 and v4 (masquerade)
- hostNetwork → pod, and pod → hostNetwork
- DNS: pod → CoreDNS → sea1-core for `generalprogramming.org`, the NetBox
  reverse zones, `.consul`
- **9000-MTU proof:** large cross-node pod→pod transfer without fragmentation —
  the native-routing payoff and the mss-clamp replacement check
- **direct-route proof:** `ip -6 route` on each node shows peer podCIDRs via
  `2602:fa6d:10:ffff::…` on-link next-hops, and no tunnel device exists anywhere
- traefik ingress over v6; `akvorado-flows` over MetalLB L2
- ceph `HEALTH_OK`, mon quorum, no OSD flapping
- consul members (record the known v4/v6 gossip-split state *before*, so it is
  not blamed on Cilium)
- all 6 VMIs: LAN up, DHCP lease, live migration

## Rollback

- **Phase A** is one-way once the box is reset. The reversible checkpoint is
  *before* `kubectl delete node` — up to there, uncordon restores it.
- **Before B.6** (flannel DS still present): delete the Cilium app, revert the
  Multus `clusterNetwork`, restart Multus. Nothing has moved.
- **During the roll:** revert `cni.name: none` and re-apply (Talos recreates the
  flannel DS), revert the Multus `clusterNetwork`, reboot the node back onto
  flannel. It needs `10-flannel.conflist` back, which the restored DS writes. Pod
  addressing is unchanged throughout, which is what makes this cheap.
- **Past halfway:** roll forward. A cluster half on each CNI is worse than
  either, and the Multus `clusterNetwork` path can only point one way at a time.
