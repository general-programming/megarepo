# FMT2: flannel → Cilium

Companion to `docs/sea1-cilium-migration.md` in the megarepo. Every "current
state" claim below was read off the live fmt2 cluster on 2026-08-08, not
inferred from the repo.

**Verdict: yes, worth pursuing — and materially cheaper than sea1 was.** The
three risks that dominated the sea1 migration (Multus, KubeVirt, in-cluster
Ceph) do not exist on fmt2. The runbook is already written and already
survived contact with a harder cluster. The real work on fmt2 is a Talos
config-drift problem, not a CNI problem.

---

## Current state (verified)

| Thing | Value |
|---|---|
| Talos / k8s | v1.12.2 / v1.35.0 |
| Nodes | 8 — `cp-0/1/2` at 10.65.67.44/.45/.46, `node-0..4` at .47–.51 |
| CNI | Talos-default flannel `v0.27.4`, VXLAN port 4789, `--ip-masq` |
| Pod subnet | `10.244.0.0/16`, per-node /24 from kube-controller-manager |
| Service subnet | `10.96.0.0/12` |
| Address families | **single-stack v4** (see drift, below) |
| kube-proxy | present, 8 nodes, iptables |
| Pods | 228 total, 70 hostNetwork |
| NetworkPolicies | **11** (argocd ×8, monitoring ×1, victoriametrics ×2) — all inert |
| Multus | **none** — `argocd/apps/infra/multus/fmt2` is an intentionally empty overlay |
| KubeVirt | **none** — same, and the `core-vms`/`dma-vms`/`freeipa`/`freepbx`/`network-vpn`/`tmpim` apps are sea1-only empty overlays |
| Ceph | **external** — `CephCluster/ceph-external`, `Connected`, `HEALTH_OK`, **0 in-cluster mons/OSDs**; only CSI plugins + rook operator |
| LoadBalancer | MetalLB in **BGP mode with frr-k8s** (not L2 like sea1) |
| MetalLB peers | leaf1 10.65.67.34 / leaf2 10.65.67.35 (ASN 4280805526), external 10.65.67.36 (ASN 208590) |
| MetalLB pools | `fmt2-pool` 10.3.4.0/23 (auto-assign), `fmt2-pool-external` 79.110.170.65/32 |
| Ingress | traefik on 79.110.170.65 — **shared** with `znc/znc-external` |
| ArgoCD | megarepo, `infra` ApplicationSet, matrix of clusters × app dirs |

---

## Why do it

1. **11 NetworkPolicies are decorative.** flannel has no policy engine. The API
   server accepts these objects and nothing enforces them. This is the same
   finding class as the sea1 security review — the difference is that on fmt2
   nobody has ever been able to fix it, only write YAML that looks like a fix.

2. **fmt2 already carries a flannel-shaped workaround.** `talconfig.yaml` ships
   an `EthernetConfig` patch disabling `tx-checksum-ip-generic`, explicitly to
   dodge flannel-io/flannel#1279 (virtio_net + offload corrupts inner VXLAN
   checksums, silently dropping cross-node UDP/DNS). Native routing deletes the
   encapsulation and therefore the bug.

3. **All 8 nodes sit on one L2** (10.65.67.0/24, with the MetalLB BGP peers as
   on-link neighbours in the same /24). That is precisely the condition sea1 had
   to *manufacture* by decommissioning the wobscale node in a whole preparatory
   phase. fmt2 gets `routingMode: native` + `autoDirectNodeRoutes` for free —
   no encapsulation, no MTU games, full node MTU available to pods.

4. **No dataplane observability.** Hubble is the payoff; akvorado sees fabric,
   not overlay.

5. **One dataplane across the estate.** sea1 already runs Cilium 1.20.0 with a
   documented values file, a written runbook, and a hard-won list of things not
   to do. Running two CNIs across two clusters means every network question
   forks. `docs/sea1-cilium-migration.md` phase D item 5 literally reads
   "consider FMT2 next."

---

## Why it is cheaper here than on sea1

The sea1 doc's risk register mostly evaporates:

| sea1 risk | fmt2 status |
|---|---|
| R2 — Multus `clusterNetwork` hardcoded to `10-flannel.conflist` | **N/A**, no Multus |
| R3 — `cni.exclusive: false` mandatory to protect `00-multus.conf` | **N/A** (still set it, see below) |
| R4 — KubeVirt LAN guests silently attach to an empty bridge | **N/A**, no KubeVirt |
| R6 — 37 hostNetwork ceph pods, mons bound to node addresses | **Mostly N/A** — Ceph is external; only CSI plugins run here, and they are unaffected by a CNI swap beyond needing the node to reach the external cluster |
| Phase A — decommission an off-L2 node first | **N/A**, already one L2 |
| Dual-stack verification | **N/A**, single-stack v4 today |
| 462 pods | 228 |

What remains is essentially sea1's Phase B → C → D with the sharp edges removed.

Keep `cni: {exclusive: false}` anyway. It costs nothing, it matches sea1, and
it means a future Multus/KubeVirt rollout on fmt2 does not need this decision
revisited under pressure.

---

## What is actually harder on fmt2

### H1 — Talos config drift is the real blocker (do this first)

`infrastructure/talos/fmt2/talconfig.yaml` declares:

```yaml
cluster:
  network:
    podSubnets:     [10.244.0.0/16, fc00:cafe:cafe:1::/64]
    serviceSubnets: [10.96.0.0/12,  fc00:cafe:cafe:2::/64]
```

The live cluster is **single-stack v4**. kube-controller-manager runs
`--cluster-cidr=10.244.0.0/16 --service-cluster-ip-range=10.96.0.0/12`, nodes
have one `podCIDR` each, and `kubernetes` resolves to 10.96.0.1 only.

So the committed Talos config **has never been applied** in its current form
(the v6 stanza dates to `269b18b3`, the initial talhelper commit). Adding
`cni: {name: none}` and running `just apply-all fmt2` would apply *everything
else that is pending too* — including a service-subnet change that
**reallocates every ClusterIP and recreates every Service**.

This is the single biggest landmine in the whole project, and it has nothing to
do with Cilium.

Required before anything else:
- `just gen fmt2` then `just diff-all fmt2`, and read every line.
- Decide, per pending field, whether it lands now or gets reverted out of
  talconfig. Default answer for the dual-stack stanza: **revert it out.** Same
  reasoning as sea1's "out of scope — separate project" note. It must not ride
  along.
- Only once `diff-all` is clean-except-for-`cni: none` do you proceed.

### H2 — NIC name is unverified

fmt2's own talconfig carries the comment:

```
# NOTE: confirm the NIC name on fmt2 — `talosctl -n <node> get links` (assumed ens18).
```

Cilium wants `devices` / `directRoutingDevice` pinned, and `kubeProxyReplacement`
refuses to start if it cannot pick one unambiguously. Verify per node; if the
control planes and workers differ, use a `CiliumNodeConfig` override exactly as
sea1 does for its bridged workers (`nodeconfig-lan-bridge.yaml`).

### H3 — MetalLB is BGP here, not L2

sea1 replaced MetalLB with Cilium LB IPAM + L2 announcements. **Do not do that
on fmt2 in this change**, for three reasons:

- `speaker` and `frr-k8s` are hostNetwork and CNI-agnostic. MetalLB keeps
  working under Cilium; there is no forcing function.
- The VIP pool `10.3.4.0/23` is **not on the node L2** — it only exists because
  it is advertised via BGP to the leaves. That is a genuinely different design
  from sea1's on-link ARP, and porting it means standing up
  `CiliumBGPClusterConfig` peering with two leaves plus a third-party ASN peer.
- `79.110.170.65` is **shared** by `traefik/traefik-helm` and
  `znc/znc-external`. MetalLB's IP-sharing semantics and Cilium LB IPAM's are
  not the same thing; that migration deserves its own change window.

Cilium BGP advertising **pod CIDRs** to the leaves is a genuinely attractive
future option here — the fabric already speaks BGP, and it would remove the
single-L2 constraint entirely. It is not needed for this migration and should
not be bundled into it.

### H4 — kube-proxy replacement vs. the BGP VIPs

Phase 3 is where MetalLB and Cilium actually interact. Re-verify specifically:

- `externalTrafficPolicy` and **source-IP preservation** on every LoadBalancer.
  `victoriametrics/vminsert-…-additional-service` takes graphite/influx on
  2003 and 8089 over **both TCP and UDP**; anything keyed on client source
  address breaks quietly, exactly as the sea1 doc warns about akvorado.
- The shared-VIP pair on 79.110.170.65.
- BGP session stability across the reboot roll (`frrk8sconfigurations`,
  `bgpsessionstates`).

### H5 — three control planes, three etcd members, zero spare

sea1 rolled 5 nodes; fmt2 rolls 8, three of them etcd voters. One control plane
at a time, fully `Ready` and etcd healthy before the next. Non-negotiable.

### H6 — tooling and bootstrap

- `talosctl` is **not installed** on this workstation (`talhelper` and `helm`
  are). `~/.talos/config.fmt2` exists, but unlike sea1 there is no rendered
  `infrastructure/talos/fmt2/clusterconfig/` and no committed talosconfig.
  Resolve before touching machine config.
- Once `cni: none` lands, a from-scratch fmt2 rebuild needs Cilium installed by
  hand (helm or a Talos `inlineManifest`) **before** ArgoCD can start — ArgoCD
  cannot install its own CNI. Write this into the app README, as sea1 did. Do
  not discover it during a disaster.

### H7 — a stale `cilium` Application already exists on fmt2

`argocd/apps/infra/cilium` has a `sea1/` overlay only, so the `infra`
ApplicationSet matrix generates an `Application/cilium` for fmt2 that sits in
`Unknown` / `ComparisonError: app path does not exist`. Creating
`argocd/apps/infra/cilium/fmt2/` is step one of Phase 1 and incidentally clears
an existing wart. (Do not "fix" it with an empty overlay in the meantime — that
would just have to be undone.)

---

## Plan

### Phase 0 — prerequisites and drift reconciliation

Blocks everything. Nothing here touches the running cluster.

1. Install `talosctl` matching Talos v1.12.2; confirm `~/.talos/config.fmt2`
   reaches all 8 nodes.
2. `talosctl -n <each node> get links` → record the real NIC name(s). Note any
   node whose node-IP device differs from the rest.
3. `just gen fmt2` → commit the rendered `clusterconfig/` (sea1 does this) →
   `just diff-all fmt2`. Enumerate every pending change.
4. Reconcile the drift. Recommendation: revert the `fc00:cafe:cafe:*`
   dual-stack stanza out of `talconfig.yaml` and file it as its own project.
   Re-run `diff-all` until the only outstanding delta is one you intend.
5. Baseline capture: etcd snapshot, `talosctl support` bundle, and the full
   validation matrix **run on flannel** so post-cutover results are comparable.
6. Record current MTU end to end (node link MTU vs. what pods see through
   `flannel.1`). The delta is the native-routing payoff and needs a before
   number.

### Phase 1 — Cilium alongside flannel

Cluster keeps running on flannel throughout. Fully reversible.

1. Create `argocd/apps/infra/cilium/fmt2/` — `application.yaml` +
   `kustomization.yaml`, modelled on the sea1 overlay. Sync with `selfHeal`
   **off** for the migration.
2. Values, fmt2-specific (deltas from sea1 called out):

   ```yaml
   ipam: {mode: kubernetes}          # reuse existing podCIDRs; addressing does not move
   k8s: {requireIPv4PodCIDR: true}   # v4 only -- fmt2 is single-stack
   ipv4: {enabled: true}
   ipv6: {enabled: false}            # DELTA vs sea1
   routingMode: native
   autoDirectNodeRoutes: true        # valid: all 8 nodes on 10.65.67.0/24
   ipv4NativeRoutingCIDR: 10.244.0.0/16
   enableIPv4Masquerade: true
   devices: "<verified in Phase 0>"
   directRoutingDevice: "<same>"
   kubeProxyReplacement: false       # phase 3
   policyEnforcementMode: never      # phase 4
   cni: {exclusive: false}
   cgroup: {autoMount: {enabled: false}, hostRoot: /sys/fs/cgroup}
   securityContext:
     capabilities:
       ciliumAgent: [CHOWN,KILL,NET_ADMIN,NET_RAW,IPC_LOCK,SYS_ADMIN,SYS_RESOURCE,DAC_OVERRIDE,FOWNER,SETGID,SETUID]
       cleanCiliumState: [NET_ADMIN,SYS_ADMIN,SYS_RESOURCE]
   prometheus: {enabled: true}
   operator: {prometheus: {enabled: true}}
   hubble:
     relay: {enabled: true}
     ui:
       enabled: true
       service:
         annotations:
           tailscale.com/expose: "true"
           tailscale.com/hostname: "hubble-fmt2"
   ```

   Do **not** copy sea1's `l2announcements` block — MetalLB stays (H3).
   Do **not** copy `bpf.masquerade` — sea1 tried it, it broke port 53, and it
   was reverted (`aeff6eb0` → `7e420b62`). Same hostDNS exposure applies here.
   Scrape config follows sea1: `VMPodScrape`, not the chart's `serviceMonitor`
   (fmt2 runs the VictoriaMetrics operator).

3. Verify `05-cilium.conflist` lands on all 8 nodes and every `cilium-agent`
   is healthy, while flannel is still the active CNI.

### Phase 2 — cut over

1. Add `cluster: {network: {cni: {name: none}}}` to `talconfig.yaml`,
   `just gen fmt2`, `just diff-all fmt2`, then `just apply` node by node. This
   only stops Talos reconciling flannel; the running DaemonSet is untouched.
2. `kubectl -n kube-system delete ds kube-flannel`. Running pods keep working —
   their veths and routes already exist. **Last cheap checkpoint.**
3. Roll nodes one at a time, workers first, control planes last (fmt2 inverts
   sea1's order — sea1 led with CPs because they carried no VMs; here the CPs
   carry the only irreplaceable state, so they go last):
   `node-0 → node-1 → node-2 → node-3 → node-4 → cp-0 → cp-1 → cp-2`.

   Per node: drain → **remove `/etc/cni/net.d/10-flannel.conflist`**
   (`cni.exclusive: false` means nothing cleans it up, and `/etc/cni` is on a
   reboot-surviving overlay) → `talosctl reboot` → node `Ready`,
   `cilium status` clean, pods hold `10.244.x` addresses issued by Cilium →
   uncordon → validation matrix → next.

   After the **first** node: confirm `flannel.1` and `cni0` are gone, and that
   `ip route` shows peer podCIDRs via on-link node next-hops with no tunnel
   device anywhere.

4. Re-enable ArgoCD `selfHeal`.

### Phase 3 — kube-proxy replacement

Only after Phase 2 has been stable for several days.

1. `cluster: {proxy: {disabled: true}}` in talconfig.
2. Cilium `kubeProxyReplacement: true`, `k8sServiceHost: localhost`,
   `k8sServicePort: 7445` (KubePrism — **not** a control-plane address; without
   kube-proxy the agent cannot resolve the `kubernetes` Service).
3. Delete the kube-proxy DS; stale `KUBE-*` chains clear on reboot.
4. Re-verify H4 in full: every LoadBalancer, source-IP preservation on the
   vminsert UDP ports, the shared 79.110.170.65 VIP, and BGP session state.

### Phase 4 — collect the winnings

1. NetworkPolicy audit with `policyAuditMode: true` for a real observation
   window, `hubble observe --verdict DROPPED` against live traffic, fix what
   would break, then flip `policyEnforcementMode: default` as **its own
   change**. Expect argocd's eight upstream-default policies to need work —
   and note the hazard: if one is wrong, ArgoCD is both the casualty and the
   tool you would use to fix it.
2. Remove the `tx-checksum-ip-generic: false` `EthernetConfig` patch — moot
   without encapsulation. Test on one node with sustained cross-node UDP/DNS
   before doing it fleet-wide.
3. Hubble UI behind traefik. Note it has **no authentication of its own**;
   tailnet ACLs are the only control (same caveat as sea1).
4. Then, and only then, consider: Cilium BGP control plane replacing MetalLB,
   and/or Cilium BGP advertising pod CIDRs to the leaves.

---

## Validation matrix

Baseline on flannel in Phase 0, re-run after each node.

- pod → pod, same node and cross-node
- pod → ClusterIP
- pod → external (masquerade)
- hostNetwork → pod, and pod → hostNetwork (70 hostNetwork pods here)
- DNS: pod → CoreDNS → upstreams, including `.consul`
- **MTU proof:** large cross-node pod→pod transfer without fragmentation
- **direct-route proof:** `ip route` shows peer podCIDRs on-link; no tunnel
  device exists on any node
- traefik ingress on 79.110.170.65, and `znc-external` on the same VIP
- every MetalLB VIP in 10.3.4.0/23; BGP sessions up to both leaves
- **ceph-external `HEALTH_OK`**, and RBD/CephFS/NFS mount+IO from a test pod on
  the rebooted node
- ArgoCD: repo-server can reach git, application-controller can reach the API
- consul members

## Rollback

- **Through Phase 1:** delete the Cilium app. Nothing has moved.
- **Before deleting the flannel DS:** same — trivially reversible.
- **During the roll:** revert `cni.name: none` and re-apply; Talos recreates the
  flannel DS, which rewrites `10-flannel.conflist`; reboot the node back onto
  flannel. Pod addressing never changes (`ipam.mode: kubernetes`), which is what
  makes this cheap.
- **Past halfway:** roll forward. A cluster half on each CNI is worse than
  either.

## Effort

Phase 0 is the one that is easy to underestimate — the drift reconciliation is
real work and it is where a careless `apply-all` recreates every Service.
Phases 1–2 are a well-rehearsed evening; Phase 3 is a short change plus a soak;
Phase 4 is open-ended by design. Call it three change windows plus the policy
audit, assuming Phase 0 turns up nothing worse than the dual-stack stanza.
