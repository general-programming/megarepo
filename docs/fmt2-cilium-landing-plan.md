# FMT2 → Cilium: landing plan, derived from what SEA1 actually did

Companion to `docs/fmt2-cilium-migration.md` (the *what* and *why*).
This is the *how to land it*, reconstructed from the sea1 migration's real git
history rather than from its runbook — the runbook says what was planned, the
history says what happened.

**Headline:** sea1's entire flannel→Cilium cutover landed in **one day,
2026-08-04, in ~15 commits**, with **two reverts**. Both reverts are predictable
in advance and both are avoidable on fmt2. Plan for one working day of cutover
plus two short follow-up windows — not a multi-week project.

> **STATUS 2026-08-08 — this plan is now GATED behind
> `docs/fmt2-talos-k8s-update.md`.** Erin elected to do the Talos
> 1.13.7 / k8s 1.36.2 update plus the config reconciliation (KubePrism,
> dual-stack, registry removal, API server SANs) *first*. When that lands:
> **step 2.0 below is already done — delete it**, and step 1.2 can go back to a
> normal `just apply` because the destructive deltas will be gone.
>
> **Window 0 was executed. See
> `docs/fmt2-cilium-window0-findings.md`.** Two revisions apply to
> this document:
> - **Step 2.1 (`CiliumNodeConfig`) is deleted.** All 8 nodes are identical —
>   `ens18` is the only up ether device on every one — so a single Helm-level
>   `devices: ens18` covers the fleet.
> - **Step 1.2 changes from `just apply` to a surgical
>   `talosctl patch machineconfig`.** The drift gate failed with three deltas
>   (worker-only dual-stack split-brain, a never-applied `EthernetConfig`, and a
>   stale `install.image: …:v1.9.3` on secureboot nodes). A full talhelper apply
>   would drag a bootloader change into an 8-node reboot roll. Patch one field,
>   commit it the same step, and make the drift its own follow-up project.

---

## Part 1 — What sea1 actually taught

### L1. It is a one-day job, but a strictly ordered one

The real sequence on 2026-08-04:

| # | Commit | What |
|---|---|---|
| 1 | `e132043d` | decommission the off-L2 node (fmt2: **not needed**) |
| 2 | `82fc9882` | add the Cilium app — native routing, **sync deliberately manual** |
| 3 | *(live)* | `cni.name: none` applied by hand, flannel DS deleted, nodes rolled |
| 4 | `932dea14` | **write `cni.name: none` back into talconfig** |
| 5 | `f5d320af` | restore automated sync |
| 6 | `fbe55ab0` | drop flannel-era workarounds |
| 7 | `fc8f6dab` | enable kube-proxy replacement → **broke** |
| 8 | `d6d58430` | revert it |
| 9 | `2bbe354e` | pin direct-routing device per node group |
| 10 | `ea47a6ad` | enable KPR again → worked |
| 11 | `95feec08` | disable kube-proxy in talconfig, **applied live and committed in one step** |
| 12 | `ef1b060a` | NetworkPolicy in **audit mode** |
| 13 | `3b92ea78` | NetworkPolicy **enforced** |
| 14 | `4051b40d`, `d7bd7a70` | Hubble UI, metrics |
| 15 | `39d21375` | retire MetalLB (fmt2: **do not copy**) |

The value here is the granularity: every risky flip is its own commit, landed
and verified separately, so a failure names its own cause. `2bbe354e` says this
explicitly — *"Landed separately from the KPR flip so the two changes can be
told apart if either misbehaves."*

### L2. Both reverts were device/masquerade problems, not CNI problems

**Revert 1 — kube-proxy replacement without pinned devices.** `2bbe354e`:

> Enabling kubeProxyReplacement killed the agents on the bridged workers with
> "unable to determine direct routing device". Auto-detection is ambiguous
> here: every node carries bond0/dummy0/teql0 alongside the real NIC […]
> **Cilium tolerated that without KPR and refuses to start with it.**

That last clause is the trap: the device ambiguity is *invisible* through
Phases 1–2 and only bites at the KPR flip. On fmt2 the workpod I inspected
shows `bonding_masters`, `ip6tnl0`, `sit0`, `tunl0` present — so the same class
of ambiguity is plausible, and the fmt2 talconfig itself admits the NIC name is
unverified (*"confirm the NIC name on fmt2 … (assumed ens18)"*).

**Revert 2 — `bpf.masquerade`.** `aeff6eb0` → `7e420b62` → documented in
`a2d97255`. It was an attempt to fix a *real* bug and it broke DNS instead:
port 53 only, both UDP and TCP, while TCP/443 stayed fine — prime suspect Talos
hostDNS's :53 redirect not surviving the switch to BPF host routing. **Still
unresolved on sea1.** Do not walk into it on fmt2.

### L3. The git-vs-cluster divergence trap is the sharpest one

`cni.name: none` was applied live with `talosctl patch machineconfig` during the
cutover and **not written back to talconfig for hours**. `932dea14`:

> git and the running cluster disagreed: any `talhelper genconfig` +
> `talosctl apply-config` would have **reinstalled the flannel manifests
> underneath a running Cilium cluster** and broken pod networking a second time.

They learned from it — by `95feec08` the practice had changed to *"Applied live
to all five nodes and recorded here in the same step — the cni.name=none
divergence earlier this migration is the reason that matters."*

**This is the single most transferable lesson, and fmt2 is already in the
failure state before we start**: fmt2's committed talconfig declares dual-stack
pod/service subnets that have never been applied. sea1's divergence lasted
hours; fmt2's has lasted since the initial talhelper commit.

### L4. Drains evict the infrastructure you are draining through

`e132043d`:

> Draining it needed three passes for a non-obvious reason worth recording: the
> Tailscale connector advertising 10.3.2.0/23 was itself running on this node,
> so each drain evicted the operator's own route into the cluster.

fmt2 runs `tailscale-operator` too.

### L5. MTU is sea1's unfinished business

`341c652f` capped the IPv4 default route at 1500 on the nodes, and explicitly
did **not** fix pods:

> This does NOT fix pods: Cilium builds the pod netns with its own default route
> at the auto-detected 9000, so pod egress still black-holes to our own IPv4
> services. That needs a separate decision, because **Cilium exposes a single
> global MTU rather than a per-route one.**

Open on sea1 today.

### L6. NetworkPolicy landed in three inert-first steps

CIDRGroups first (`9458ff75`, deliberately referenced by nothing: *"a no-op on
its own … landing them first means the address lists get reviewed on their own
terms"*), then audit mode (`ef1b060a`), then enforcement (`3b92ea78`) only after
a 40-minute audit window returned zero legitimate drops. Never one flip.

---

## Part 2 — What this changes for fmt2

### D1. Pin Cilium's MTU to 8950 and make the swap MTU-neutral

**Measured today on fmt2**: a `kube-system/workpod` pod has `eth0` MTU **8950** —
i.e. the fabric is jumbo (9000) and flannel's VXLAN takes its 50 bytes. Node
links are 9000; fmt2's talconfig sets no MTU anywhere, so it comes from the
fabric.

Under `routingMode: native` there is no encapsulation, so Cilium would
auto-detect and hand pods **9000**. That is precisely the configuration that
black-holes IPv4 egress on sea1 and remains unfixed there.

**Therefore: set Cilium's global `MTU: 8950` explicitly in the fmt2 values.**
Pods keep the exact MTU they have today, the CNI swap introduces zero MTU
variables, and the "should pods be jumbo, and what breaks off-LAN" question
stays a separate project — exactly the discipline the sea1 doc applied to the
dual-stack flip. Revisit only after Phase 4.

This is the clearest case of fmt2 getting to skip a problem sea1 is still living
with, and it costs one line.

### D2. Expect the consul hostPort break, and do not reach for bpf.masquerade

sea1's `bpf.masquerade` attempt existed to fix this:

> the iptables path SNATs hostPort *replies* to a fresh source port for LAN
> peers that are not cluster nodes, which silently kills every consul RPC.

**fmt2 has the identical shape.** `consul/fmt2-client` is a DaemonSet running
**pod-networked** (`hostNetwork` unset, `dnsPolicy: ClusterFirst`) with
`hostPort` **8301, 8500, 8502**. Any non-cluster LAN peer doing consul RPC to a
node — i.e. fmt2-core — is exposed to the same bug the moment Cilium owns
masquerading.

Plan for it:
- Add "consul RPC from fmt2-core to a pod on the migrated node" to the
  **per-node** validation, not the end-of-migration validation. Catch it on
  `node-0` while seven nodes are still on flannel.
- If it bites, the answer is **not** `bpf.masquerade` — that is the known-bad
  path with an open DNS failure behind it. The cheap answers are: move the
  consul client to `hostNetwork`, or exclude the LAN peer's prefix from
  masquerading (`ipMasqAgent` / `nonMasqueradeCIDRs`). Decide which *before* the
  window, so it is a prepared branch and not an improvisation at 2am.

Other hostPort users on fmt2 — `metallb-system/frr-k8s` (7573/9140/9141),
`speaker` (7946/9120), `monitoring/node-exporter` (9100) — are all hostNetwork
or node-local, so they are not exposed the same way. There are **zero NodePort
services** on fmt2, which removes a whole verification category.

### D3. Reconcile the drift before anything, and never diverge again

Non-negotiable working rule for this migration, straight from L3:

> **Every live machine-config change is applied and committed in the same
> working step.** No exceptions, no "write it back later."

And because fmt2 starts divergent, Phase 0's `just diff-all fmt2` is a hard gate
rather than a formality.

### D4. fmt2 skips sea1's three worst days

sea1's 2026-08-04 was not only the CNI swap — it was tangled with converting
hosts to metal, moving data volumes, an SN200 discard bug, lldpd, and a node
decommission. fmt2 has none of that. The CNI swap is the only thing in the
window, which is why it should stay a genuinely one-day job.

---

## Part 3 — The landing sequence

Each numbered item is **one commit**, verified before the next. Mirrors sea1's
proven order with its two reverts pre-empted.

### Window 0 — preparation (no cluster changes)

- **0.1** Install `talosctl` for Talos v1.12.2; confirm `~/.talos/config.fmt2`
  reaches all 8 nodes.
- **0.2** `talosctl -n <each> get links` → record the real node-IP device per
  node. **This is the KPR revert, pre-empted.** If the 3 CPs and 5 workers
  differ, prepare a `CiliumNodeConfig` now, modelled on sea1's
  `nodeconfig-lan-bridge.yaml`.
- **0.3** `just gen fmt2` → **commit the rendered `clusterconfig/`** (sea1 has
  one, fmt2 does not) → `just diff-all fmt2`.
- **0.4** *Commit:* reconcile the drift — revert the `fc00:cafe:cafe:*`
  dual-stack stanza out of `talconfig.yaml`. Re-run `diff-all` until it is
  empty. **Gate: no window opens until this is clean.**
- **0.5** Decide the consul-hostPort fallback (D2) on paper.
- **0.6** Baseline: etcd snapshot, `talosctl support` bundle, and the full
  validation matrix **run on flannel**, including the consul RPC test and a
  cross-node 8950-byte transfer.
- **0.7** Confirm both tailscale-operator proxy replicas are not co-located, so
  the drain cannot evict its own route (L4).

### Window 1 — the cutover (one working day)

- **1.1** *Commit:* add `argocd/apps/infra/cilium/fmt2/` — Cilium 1.20.0,
  `routingMode: native`, `autoDirectNodeRoutes: true`, `ipam.mode: kubernetes`,
  **`MTU: 8950`** (D1), `devices`/`directRoutingDevice` from 0.2,
  `kubeProxyReplacement: false`, `policyEnforcementMode: never`,
  `cni.exclusive: false`, Talos cgroup + securityContext blocks.
  **Sync manual — no `automated:` block** (sea1 `82fc9882`).
  This also clears the stale `Application/cilium` sitting in `ComparisonError`.
  *Verify:* `05-cilium.conflist` on all 8 nodes, all agents healthy, **flannel
  still the active CNI.** Fully reversible: delete the app.
- **1.2** *Commit:* `cluster.network.cni.name: none`. **Apply with a surgical
  `talosctl patch machineconfig`, node by node — not `just apply`** (see Window
  0 findings, Change 2): a full talhelper apply would also rewrite
  `install.image` from the stale `v1.9.3` on secureboot/TPM-LUKS nodes, right
  before Window 1 reboots all eight. **Commit the same change to talconfig in
  the same working step** (L3). Running flannel DS untouched.
- **1.3** `kubectl -n kube-system delete ds kube-flannel`. Running pods keep
  working. **Last cheap checkpoint.**
- **1.4** Roll nodes one at a time: `node-0 → node-1 → node-2 → node-3 →
  node-4 → cp-0 → cp-1 → cp-2`. Workers first — inverting sea1, because fmt2's
  CPs carry the only irreplaceable state (3 etcd voters, zero spare) and the
  workers make the cheap first proof.

  Per node: drain → remove `/etc/cni/net.d/10-flannel.conflist` → `talosctl
  reboot` → Ready → `cilium status` clean → pods hold `10.244.x` from Cilium →
  uncordon → **validation matrix incl. the consul RPC test (D2)** → next.

  After **node-0** specifically: `flannel.1` and `cni0` gone; `ip route` shows
  peer podCIDRs on-link with no tunnel device; pod `eth0` MTU is **8950**, not
  9000.
- **1.5** *Commit:* restore `automated: {selfHeal: true, prune: true}` on the
  Cilium app (sea1 `f5d320af`).

**Abort criteria for Window 1:** if two nodes in a row need unplanned
intervention, stop and hold — do not push to the control planes. Rollback
through 1.3 is: revert `cni.name: none`, re-apply, Talos recreates the flannel
DS and rewrites `10-flannel.conflist`, reboot the node back. Pod addressing
never moved (`ipam.mode: kubernetes`), which is what keeps this cheap. Past
node-4, roll forward.

### Window 2 — kube-proxy replacement (after several days' soak)

Order matters and is sea1-proven: **Cilium takes over service handling first,
kube-proxy is removed second, so services are never unhandled by both.**

- **2.0** ~~*Commit:* **enable KubePrism**~~ — **DONE 2026-08-08** in Phase A of
  `docs/fmt2-talos-k8s-update.md`. Verified healthy on `127.0.0.1:7445` on all 8
  nodes. Original text kept below for context.
- **2.0 (original)** *Commit:* **enable KubePrism** — `machine.features.kubePrism
  {enabled: true, port: 7445}` — by surgical patch on all 8 nodes, committed to
  talconfig in the same step. **Window 0 found it is absent from fmt2's live
  config on every node**, and 2.2 depends on it entirely. sea1 already had it,
  so this step has no sea1 counterpart. Verify `localhost:7445` answers on every
  node before proceeding.
- **2.1** ~~`CiliumNodeConfig` device pinning~~ — **not needed.** Window 0
  confirmed all 8 nodes carry the node IP on `ens18`; the Helm-level `devices`
  value from 1.1 covers the fleet. (Retained as a heading so the numbering
  matches the sea1 sequence it was derived from.)
- **2.2** *Commit:* `kubeProxyReplacement: true`, `k8sServiceHost: localhost`,
  `k8sServicePort: 7445` (KubePrism — **not** a control-plane address).
  *Verify:* every agent healthy on all 8 nodes **before** proceeding. This is
  where sea1 broke; if it breaks here, the answer is 2.1, not a retry.
- **2.3** *Commit:* `cluster.proxy.disabled: true` in talconfig — **applied live
  and committed in the same step** (sea1 `95feec08`). Delete the kube-proxy DS;
  stale `KUBE-*` chains clear on reboot.
- **2.4** *Verify, fmt2-specific:* every MetalLB VIP in `10.3.4.0/23`; BGP
  sessions to both leaves (`bgpsessionstates`, `frrk8sconfigurations`);
  **source-IP preservation** on `vminsert`'s graphite/influx ports 2003 and 8089
  over TCP **and** UDP; and the shared `79.110.170.65` VIP serving both
  `traefik` and `znc-external`.

**MetalLB stays.** sea1's `39d21375` retired it, but sea1 was L2 — fmt2 is
BGP+frr-k8s with an off-L2 VIP pool and a shared VIP. Not in this project.

### Window 3 — collect the winnings

- **3.1** *Commit:* `CiliumCIDRGroup`s for fmt2 — inert on their own, reviewed
  on their own terms (sea1 `9458ff75`). Note only `CiliumNetworkPolicy` /
  `CiliumClusterwideNetworkPolicy` can use `cidrGroupRef`; the 11 existing
  native `networking.k8s.io/v1` policies cannot.
- **3.2** *Commit:* `policyAuditMode: true`. Observe with
  `hubble observe --verdict AUDIT` across a real window — sea1 used 40 minutes
  and required **zero legitimate hits** before proceeding.
- **3.3** *Commit:* `policyEnforcementMode: default`. Expect argocd's eight
  upstream-default policies to need work first. The hazard is unchanged from
  sea1: if one is wrong, ArgoCD is both the casualty and the repair tool.
- **3.4** *Commit:* Hubble UI on the tailnet (`hubble-fmt2`) + `VMPodScrape`
  metrics — fmt2 runs the VictoriaMetrics operator, not prometheus-operator, so
  **not** the chart's `serviceMonitor`. Hubble UI has no auth of its own;
  tailnet ACLs are the only control.
- **3.5** *Commit:* drop the `tx-checksum-ip-generic: false` `EthernetConfig`
  patch — with no encapsulation there is no inner checksum, so it only costs CPU
  (sea1 `fbe55ab0`). Test on one node with sustained cross-node UDP/DNS first.
- **3.6** Trim the migration commentary back to the repo's 1–2 line comment rule.

**Explicitly deferred, each its own project:** pod MTU 8950→9000 and the
off-LAN black-hole (sea1's open item, L5); dual-stack; Cilium BGP replacing
MetalLB or advertising podCIDRs to the leaves; a Talos ingress firewall for
fmt2.

---

## Part 4 — Doing better than sea1 did

Four things sea1 paid for that fmt2 can have for free:

1. **The KPR revert** — pre-empted by 0.2 and 2.1. sea1 discovered the device
   ambiguity by having agents die; fmt2 discovers it by reading `get links`.
2. **The git/cluster divergence** — pre-empted by the apply-and-commit-together
   rule and by gating on a clean `diff-all`. sea1 fixed this mid-migration;
   fmt2 adopts the fixed practice from the start.
3. **The MTU black-hole** — pre-empted by pinning `MTU: 8950`. One line, and
   fmt2 simply never enters the state sea1 is still in.
4. **The consul hostPort break** — sea1 met it after the fact and its attempted
   fix is reverted. fmt2 tests for it on node-0 with a decided fallback.

The one thing sea1 did that is worth copying verbatim: **write the reasoning
into the commit messages and the values file as you go.** The reason this plan
could be written at all is that `82fc9882`, `2bbe354e`, and `a2d97255` explain
*why*, not just *what*.

## Part 5 — Effort

| Window | Work | Wall clock |
|---|---|---|
| 0 | talosctl, device audit, **drift reconciliation**, baselines | half a day; the drift is the unknown |
| 1 | Cilium app, `cni: none`, delete flannel, roll 8 nodes | one working day |
| 2 | KPR + kube-proxy removal + MetalLB/BGP re-verify | ~2 hours plus soak |
| 3 | CIDRGroups → audit → enforce, Hubble, cleanup | open-ended by design |

Windows 1 and 2 want a maintenance window each. Window 0 wants nobody rushing
it — it is the only part where a mistake recreates every Service in the cluster.

---

## Execution log — cutover done 2026-08-08

**Cilium is live on all 8 nodes, native routing, no encapsulation.**
`cilium-dbg status`: `Routing: Network: Native`, cluster health **8/8
reachable**, masquerading IPTables/IPv4, `KubeProxyReplacement: False` (phase 2
still pending). Every node shows direct routes to each peer podCIDR via the
peer's node IP on `ens18`, and **no tunnel device exists anywhere**. Pod MTU is
**8950** as pinned. `flannel.1` and `cni0` are gone from all 8 nodes.

Sequence actually run: deploy Cilium -> `cni.name: none` (strategic-merge patch,
no reboot) -> delete `kube-flannel` DS -> remove `10-flannel.conflist` on every
node -> reboot all 8, one at a time -> verify.

### Correction: step 1.1 is NOT a passive install

This plan said to verify "flannel still the active CNI" after adding the Cilium
app. That was wrong. Cilium writes `05-cilium.conflist`, which sorts **before**
`10-flannel.conflist`, so containerd hands every *new* pod sandbox to Cilium the
moment the agents start. Existing pods keep flannel until their node reboots.

The consequence is real and was observed: for the ~90 minutes between installing
Cilium and finishing the roll, newly-created pods on un-rolled nodes had broken
networking -- hubble-relay could not reach CoreDNS, and ArgoCD, cert-manager and
CNPG pods entered CrashLoopBackOff as they were rescheduled. Unhealthy pods
peaked around 60 and fell back to 1 (a pre-existing crashloop) as nodes rolled.

**So step 1.1 starts the migration. Do not install Cilium and walk away.** Budget
one continuous window from install through the last node.

### PDBs cannot be satisfied mid-migration

Once pods start failing, PDBs report `healthy=0` and graceful eviction is
impossible -- `kubectl drain` blocks forever. The reboot is what repairs those
pods, so the roll used `--disable-eviction --force`. Worth knowing in advance
rather than discovering it at the first node.

### `directRoutingDevice` must be nested under `nodePort`

The chart only reads `.Values.nodePort.directRoutingDevice`. A top-level
`directRoutingDevice` renders nothing at all. Verified by templating 1.20.0 both
ways and diffing the generated `cilium-config`. **sea1 has the top-level form**
and is only saved by its `CiliumNodeConfig` setting the raw key -- worth fixing
there.

### BGP: one peer was already dead, one is remote-side

Checked against VictoriaMetrics history rather than guessed:

| Peer | Before | After |
|---|---|---|
| `10.65.67.34` leaf1 | **0/8** | 0/8 |
| `10.65.67.35` leaf2 | 8/8 | **8/8** |
| `10.65.67.36` external | 8/8 | **0/8** |

- **leaf1 was already down before any of this work** (>12h). From a node it is
  `Host is unreachable` and does not ping. Pre-existing, unrelated, and worth
  chasing separately.
- **The external peer dropped on the node reboots.** From a node it pings fine
  and routing is correct, but TCP/179 gives `Connection refused` -- its BGP
  daemon is not accepting. The same thing happened during the Talos roll earlier
  the same day (fell 8->0 at 02:30) and it **recovered on its own by 06:30**, so
  it self-heals in roughly two to three hours.
- Internal VIPs are unaffected and verified serving (`loki-gateway` on
  `10.3.4.1` returns 200). The external pool `79.110.170.65` (traefik, znc) is
  announced **only** via that external peer, so public ingress stays down until
  it re-accepts.

Next: phase 2 (kube-proxy replacement) and phase 3 (NetworkPolicy). KubePrism is
already enabled, so phase 2 needs only the Helm values.
