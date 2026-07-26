# Akvorado in SEA1

Flow collector (NetFlow/IPFIX/sFlow) for the barf-managed fabric, deployed on
the SEA1 Talos cluster via Argo CD. Complements LibreNMS (`nms.generalprogramming.org`),
which does SNMP counters — Akvorado does per-flow traffic analysis.

## Architecture (Akvorado 2.x)

Akvorado 2.0 split the old monolithic inlet into two services:

```
devices ──UDP 2055/4739/6343──▶ inlet ──▶ Kafka ──▶ outlet ──▶ ClickHouse ──▶ console
                                                  │
                                                  ├── SNMP poll ──▶ devices
                                                  └── BMP :10179 ◀── routers
                            orchestrator ─ serves config, owns CH schema + Kafka topics
```

- **inlet** — receives flows over UDP, pushes them to Kafka *undecoded*. Latency
  and loss sensitive; this is the only component that must see the real exporter
  source IP.
- **outlet** — consumes Kafka, decodes, enriches (SNMP metadata + BMP routing),
  batches into ClickHouse. SNMP polling and the BMP receiver both live here as
  of 2.0, not in the inlet.
- **orchestrator** — serves configuration to the other services, manages the
  ClickHouse schema/migrations and Kafka topics. HTTP only.
- **console** — the UI. Delegates auth to a reverse proxy via `Remote-User`.

Dependencies: **Kafka** (KRaft mode, required — it is the buffer between inlet
and outlet) and **ClickHouse**.

## Placement decisions

| Concern | Decision |
| --- | --- |
| Cluster | SEA1 Talos (`argocd/apps/erin-apps/akvorado/`), alongside librenms |
| ClickHouse | Single-replica `ClickHouseCluster` + `KeeperCluster` via the existing `clickhouse-operator` (same CRDs sentry uses) |
| Kafka | Single-broker KRaft StatefulSet in-namespace, 2h retention. It is a buffer, not a system of record — no Strimzi. |
| Flow ingress | MetalLB L2 VIP `10.3.3.1` in SEA1, `externalTrafficPolicy: Local` |
| Console | Tailscale ingress only, never public |
| Secrets | `VaultStaticSecret` (Vault Secrets Operator), same as tailscale-operator |
| Storage | `ceph-rbd-xfs` throughout |
| Image | `quay.io/akvorado/akvorado:2.4.1` — note the tag has no `v`, and ghcr.io denies anonymous pulls |

ClickHouse runs **one** replica in non-cluster mode. Akvorado's `clickhousedb.cluster`
setting is a one-way door — it switches the orchestrator to replicated +
distributed tables with no supported migration in either direction. Flow history
is TTL'd and rebuildable, so single-replica is the cheap tradeoff; moving to
cluster mode later means starting the database from scratch. Keeper exists only
because `ClickHouseCluster` requires `keeperClusterRef`.

## Safely exposing the collector

### Why this needs care

Akvorado keys every flow by the **exporter source IP**. If the packet is SNAT'd
on the way in, every device collapses into one bogus exporter and the data is
worthless. That constraint drives the whole ingress design:

- `externalTrafficPolicy: Local` on the inlet Service — no cross-node SNAT.
- It also keeps flow UDP off flannel's VXLAN overlay entirely, which this
  cluster already has scar tissue around (see the `tx-checksum-ip-generic`
  workaround in `infrastructure/talos/sea1/talconfig.yaml` and
  `argocd/apps/infra/mss-clamp/`).

### 1. MetalLB L2 in SEA1 (new)

SEA1 has no MetalLB today — traefik there is ClusterIP behind the `sea1-traefik`
cloudflared tunnel, and only FMT2 has a MetalLB overlay (BGP to the fmt2 leaves).
We add an **L2** overlay for SEA1, which needs no router-side config:

- `argocd/apps/infra/metallb/sea1/` → `IPAddressPool` + `L2Advertisement`.
- Pool: **`10.3.3.0/28`**, carved out of the SEA1 `10.3.2.0/23` LAN. Free by
  inspection: sea1-core's Kea pool is `10.3.3.128 - 10.3.3.254`
  (`nix/machines/sea1-core/configuration.nix`), and the statics live down in
  `10.3.2.x` (nodes `.10-.12`, `.16-.18`, VRRP `.1`, leaf `.4/.5/.21`).
- **Node selection matters.** `sea1-k8s-103-0` is at `10.3.6.3` in the wobscale
  rack — a different L2 segment. It must be excluded from the L2Advertisement
  or it will ARP for an address it cannot own.

  This is expressed as a node label, `generalprogramming.org/no-lan-vip`, set in
  `infrastructure/talos/sea1/talconfig.yaml` and selected with `DoesNotExist` —
  the same shape as the existing `no-database` label. Not reused: `no-database`
  happens to sit on the right node today, but it means "no databases here", so
  keying VIP placement off it would break silently the moment it moves for
  storage reasons. A hostname `NotIn` list was the other option; the label
  generalises to a second off-LAN node without touching every consumer.

  `DoesNotExist` is deliberate: it selects every node until the label is
  applied, so a talconfig that has not been rolled out yet degrades to today's
  behaviour instead of leaving the DaemonSet unschedulable. The talconfig change
  needs a `talosctl` apply — it is not gitops-delivered, so it will not happen
  as a side effect of merging this.

Inlet service:

```yaml
metadata:
  annotations:
    metallb.universe.tf/address-pool: sea1-pool
    metallb.universe.tf/loadBalancerIPs: 10.3.3.1
spec:
  type: LoadBalancer
  externalTrafficPolicy: Local
  ports:
    - 2055/UDP   # NetFlow v9
    - 4739/UDP   # IPFIX
    - 6343/UDP   # sFlow
```

With `Local`, MetalLB only announces from nodes actually running an inlet pod,
so run the inlet as a DaemonSet pinned to the `10.3.2.x` workers (or ≥2 replicas
with an anti-affinity spread).

### 2. Reachability for the rest of the fabric

- **Same-LAN SEA1 devices** reach `10.3.3.1` directly.
- **Everything else** (fmt2 leaves, sea420 Mikrotik, ord/iad) reaches it over
  the existing WireGuard/BGP fabric, provided `10.3.3.0/28` is inside the
  `10.3.2.0/23` prefix the SEA1 leaf already originates. Worth confirming per
  site rather than assuming — that is a validation step below, not an assertion.
- **Roaming/off-fabric** devices are covered by the Tailscale connector, which
  already advertises `10.3.2.0/23` (`argocd/apps/infra/tailscale-operator/sea1/connector.yaml`).

### 3. Lock the listener down — and what actually enforces it

Flow records are unauthenticated UDP by design: anyone who can reach the port can
inject fabricated traffic data. The obvious control would be a `NetworkPolicy`
restricting UDP 2055/4739/6343 to the device management prefixes.

**That control does not exist in this cluster.** SEA1 is Talos with the default
flannel CNI, and flannel has no NetworkPolicy enforcement — there is no Cilium,
Calico, or kube-router anywhere in `argocd/` or `infrastructure/`. A
NetworkPolicy here is accepted by the API server and silently ignored. (The one
in `argocd/apps/infra/monitoring/base/upstream_node_exporter/` is upstream
boilerplate and is equally inert.) Shipping one would have looked like a control
while enforcing nothing, so this change deliberately ships none.

What actually constrains reach today:

- The VIP is RFC1918 on the SEA1 LAN, with no route from the internet and no
  presence in the cloudflared tunnel.
- Reachability is bounded by the WireGuard fabric and the per-device firewall
  groups barf already renders (`projects/barf/network.yml` → `firewall.groups`).

If real ingress filtering is wanted, the honest options are a policy-capable CNI
(a cluster-wide change well beyond this deploy) or filtering on the SEA1 leaf.
Worth deciding separately — not silently assumed.

### 4. SNMP, in reverse

The **outlet** polls each exporter over SNMP to resolve interface names. That is
egress from a pod to device management IPs across the fabric, which is the part
most likely to need a nudge:

- The read-only community is the one barf already uses
  (`projects/barf/network.yml` → `global_meta.snmp_public`). It comes from Vault,
  not the kustomization, and is not reproduced here — `barf config diff` now
  redacts this value specifically (d536a692), so it should not be pasted into
  new docs either.

  **This must be seeded before the orchestrator will start.** Akvorado's SNMP
  provider keys credentials by exporter subnet, and an env-var override cannot
  express a map key like `::/0` — so the secret is a YAML *fragment* that
  `akvorado.yaml` pulls in with `!include`. Write a single key named
  `snmp-credentials.yaml` at `secret/app/akvorado`:

  ```yaml
  ::/0:
    communities: <global_meta.snmp_public from barf>
  ```

  `!include` resolves relative to the config file's own directory
  (akvorado does `os.DirFS(dirname)`), which is why the orchestrator mounts the
  ConfigMap and the Vault-backed Secret through a single **projected** volume —
  two separate mounts would not put the files in the same directory.
- Verify pod → `10.255.1.0/24` and `10.3.0.0/23` egress actually works before
  trusting interface names in the UI. If pod-network egress to the fabric is
  blocked, `hostNetwork` on the outlet is the escape hatch.

### 5. Console

Tailscale ingress, `ingressClassName: tailscale` — the operator is already in
SEA1. This gives a MagicDNS name with tailnet ACLs as the authorization
boundary, and no public DNS record. Deliberately *not* behind the cloudflared
tunnel: the console renders your entire traffic matrix, peering relationships,
and internal addressing.

Akvorado's own auth is header-based (`Remote-User`), so with Tailscale-only
exposure it runs in single-user mode. If per-user identity in the console is
wanted later, add a traefik `forwardAuth` middleware against an authentik proxy
provider — none exists in the repo yet, so that is its own change.

### 6. BMP (optional, phase 2)

The outlet can accept BMP on TCP 10179 to learn AS paths and prefixes, which
makes the console's AS-level views actually useful. Source would be the VyOS/bird
leaves. Needs a second LoadBalancer service (or an extra port on the same VIP)
and is strictly additive — leave it out of the first cut.

## Rollout phases

**Phase 0 — MetalLB in SEA1**
- Apply the talconfig label first (`talosctl`/talhelper) so
  `generalprogramming.org/no-lan-vip` exists on `sea1-k8s-103-0`. Until then the
  selectors match every node, including one that cannot own the VIP.
- `argocd/apps/infra/metallb/sea1/{kustomization,l2.yaml}` — pool `10.3.3.0/28`
  + L2Advertisement.
- Validate with a throwaway `LoadBalancer` Service before Akvorado depends on it:
  confirm the VIP ARPs, and confirm it pings from a device in another site.

**Phase 1 — Namespace + data stores**
- `argocd/apps/infra/namespace/base/akvorado.yaml` + kustomization entry.
- `ClickHouseCluster` + `KeeperCluster` (1 replica each, see above).
- Single-broker KRaft Kafka StatefulSet (`apache/kafka` — not Bitnami, those
  images moved behind a paywall), 2h retention.
- Valkey for the console's query cache.

**Phase 2 — Akvorado**
- `argocd/apps/erin-apps/akvorado/base/` — orchestrator, inlet, outlet, console
  + config ConfigMap.
- `argocd/apps/erin-apps/akvorado/sea1/` — `VaultStaticSecret`, LoadBalancer
  service with the pinned VIP, Tailscale ingress, and the DaemonSet patch that
  keeps the inlet off `sea1-k8s-103-0`.
- **Seed Vault first** (see SNMP above) — the orchestrator will not start
  without `snmp-credentials.yaml` present.
- Let the orchestrator create the ClickHouse schema; do not hand-write it.

**Phase 3 — Validate with one device**
- Point a single VyOS leaf at `10.3.3.1:2055` by hand
  (`set system flow-accounting netflow server 10.3.3.1 port 2055`).
- Check: flows land, and the exporter shows up as its *own* IP (this is the
  `externalTrafficPolicy` check — if every flow attributes to a node IP, the
  ingress path is wrong).
- Check interface names resolve, which proves the SNMP egress path.

**Phase 4 — barf codegen (deferred, separate commit)**
- Model flow export in `projects/barf/network.yml`, render per vendor (VyOS
  `flow-accounting`, RouterOS `/ip traffic-flow`, Arista sFlow), with pytest
  coverage under `projects/barf/tests/`. Out of scope for this change.

## What was verified before merge

No cluster access was available, so everything below is offline verification —
none of it proves the deploy works, only that it is not wrong on its face:

- All three kustomizations build, and render valid Kubernetes objects
  (`kubeconform -strict`, k8s 1.32 schemas).
- `ClickHouseCluster`, `KeeperCluster`, `IPAddressPool` and `L2Advertisement`
  validate against the actual CRD OpenAPI schemas vendored in this repo.
- The orchestrator config was run through Akvorado's own validator —
  `orchestrator --check --dump` on `quay.io/akvorado/akvorado:2.4.1` — with the
  Vault fragment stubbed in. It exits 0, and the dump confirms the `!include`
  resolves (the community lands under `::/0`) and that the persist-file,
  resolution, and routing settings apply as written.
- The image tag was checked against both registries: `2.4.1` exists, `v2.4.1`
  does not, and ghcr.io refuses anonymous pulls where quay.io serves them.

## Open risks

1. **Pod egress to fabric management IPs** for SNMP is unverified — phase 3 is
   the go/no-go for it.
2. **`10.3.3.0/28` reachability from other sites** depends on the SEA1 leaf
   originating the full `/23`; confirm before rendering the collector IP into
   any device config.
3. **Sampling rate.** Akvorado needs to know each exporter's sampling rate to
   scale counters. Wrong rate means confidently wrong numbers rather than an
   obvious failure — set it explicitly per exporter, do not rely on defaults.
4. **ClickHouse growth.** Retention is set here to raw 7d / 1m 14d / 5m 3mo /
   1h 1y against a 200Gi PVC. Raw flows dominate; if the volume fills, cut the
   `interval: 0` TTL first. Growing Ceph RBD later is doable but annoying.
5. **Sync ordering.** The MetalLB pool lands via the `infra` ApplicationSet and
   Akvorado via `erin-apps`; nothing sequences them. If Akvorado syncs first,
   `akvorado-flows` sits in `Pending` until the pool exists — harmless, but do
   not read it as a failure.
6. **The fmt2 Application will error.** `erin-apps` is a matrix of clusters ×
   app directories, so FMT2 gets an Application pointing at
   `argocd/apps/erin-apps/akvorado/fmt2`, which does not exist. This is the same
   state librenms is already in — noted so it is not mistaken for a regression
   introduced here.
