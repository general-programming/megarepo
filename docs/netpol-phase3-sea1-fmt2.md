# NetworkPolicy enforcement across SEA1 + FMT2

Phase 3 of `docs/fmt2-cilium-landing-plan.md`, scoped as one project across both
clusters. Every number below was read off the live clusters on 2026-08-08.

**The short version:** the naive reading of "warn mode" produces a green window
that proves nothing, both clusters flipping together is an unnecessary risk when
one of them has nothing to lose, and roughly a third of the workload cannot be
covered by NetworkPolicy at all. All three are addressed below.

---

## Where the two clusters actually are

| | SEA1 | FMT2 |
|---|---|---|
| Pods | 306 (86 hostNetwork) | 241 (80 hostNetwork) |
| Namespaces | 56 | 57 |
| `NetworkPolicy` | **33**, over 14 namespaces | **11**, over 3 namespaces |
| `CiliumNetworkPolicy` | 2 | 0 |
| `CiliumCIDRGroup` | 5 | 0 |
| `enable-policy` | **`default`** (enforcing) | **`never`** (inert) |
| `policy-audit-mode` | `false` | — |
| `enable-host-firewall` | **`false`** | **`false`** |
| kube-proxy replacement | true | true |
| Hubble policy correlation | **on** | **on** |

Combined: **547 pods, 113 namespaces, 46 policy objects covering 17 of them.**
SEA1's uncovered namespaces number 42; FMT2's, 54.

Two properties of that table drive everything else.

**`enable-policy: never` on FMT2 means it has no enforcement to lose.** Anything
we do there is a strict gain. SEA1 is genuinely enforcing 33 policies today.

**`enable-host-firewall: false` on both** means `policyEnforcementMode: always`
will not touch hostNetwork pods — it cannot break them, and it cannot observe
them either.

---

## Decision 1 — `always`, not `default`

`policyEnforcementMode: default` only evaluates endpoints **already selected by
some policy**. That is 14 namespaces on SEA1 and 3 on FMT2. The other **96
namespaces would emit no audit signal at all** — no drops, no verdicts, nothing.

An audit window run that way comes back clean, and the cleanliness is
meaningless: it says "the policies you already wrote don't break the pods they
already select". It tells you nothing about the 96 namespaces you still need to
write policies for, which is the entire job.

**`policyEnforcementMode: always` + `policyAuditMode: true`** makes every
endpoint default-deny, so every flow that *would* be denied is reported as
`AUDIT` while still being forwarded. That is the dataset the work needs.

## Decision 2 — FMT2 first, not both at once

The instruction was to treat the clusters as a single unit. That is right for the
*policy library* — shared CIDRGroup vocabulary, shared review, shared idioms, and
the cross-cluster flows in §Risks mean policies on one side imply policies on the
other. It is not right for the *mode flip*.

Flipping both simultaneously means that if `policyAuditMode` has any gap — a path
where a verdict is enforced rather than audited — both clusters break at the same
moment, and one of them is the cluster that hosts the ArgoCD that would fix it.

FMT2 is the natural canary precisely because `enable-policy: never` means it
carries no enforcement today. Going to `always` + audit there is a pure gain: new
visibility, no enforcement gained or lost. Once the mechanism is proven on FMT2
under real traffic, SEA1 follows.

**SEA1 does stop enforcing while it is in audit mode.** Its 33 policies still
evaluate and still report, but violations are permitted. That is an accepted,
reversible cost of the authoring window, and it is the reason SEA1 goes second and
spends as little time there as possible.

## Decision 3 — hostNetwork is a different mechanism, and it is not Cilium

**166 of 547 pods (~30%) are hostNetwork** — 86 on SEA1, 80 on FMT2. They live in
the host network namespace, where CiliumNetworkPolicy does not apply. No amount
of `always` reaches them. That includes ceph CSI, frr-k8s and the MetalLB
speakers, node-exporter, the consul clients on FMT2, and every static
control-plane pod.

Two ways to cover them:

- **Cilium host firewall** (`enable-host-firewall: true` + a
  `CiliumClusterwideNetworkPolicy` selecting the host endpoint). Powerful and
  in-band with the rest of the policy work — and the fastest way to lock yourself
  out of the API server and the Talos API simultaneously, on every node at once.
- **Talos ingress firewall.** Already proven on SEA1, with a skill in this repo
  documenting it (`.claude/skills/talos-ingress-firewall`). It auto-allows the
  pod and service subnets and established connections, so it only needs rules for
  sources outside the cluster, and it sits at nftables priority `-140`, ahead of
  the DNAT hook, so it is not bypassed by Cilium's BPF.

**Recommendation: Talos ingress firewall for the host half.** It is the proven
path on SEA1, it fails safe, and it keeps the two problems separate so a mistake
in one does not compound the other. **FMT2 has no ingress firewall today** — that
is its own piece of work and belongs after the pod-level policies land, not
tangled into them.

So "policies for everything" means, honestly: **CiliumNetworkPolicy for the 381
pod-networked workloads, Talos ingress firewall for the 166 hostNetwork ones.**

---

## Sequence

### Stage 0 — instrumentation (prerequisite, in flight)

FMT2's Cilium and Hubble metrics were **not being scraped at all** — there is no
VMPodScrape for cilium on that cluster, so `hubble_flows_processed_total`,
`cilium_drop_count_total` and `cilium_policy_endpoint_enforcement_status` only
ever had series for `cluster="sea1-k8s"`. Fixed by moving the scrape into
`victoriametrics/base/`, since both clusters run Cilium now.

Gate: flow, drop and enforcement series present for **both** clusters, and
`hubble.eventBufferCapacity` raised from the 4095 default before Stage 1 collects
anything — see R4. SEA1's measured loss is only 0.088%, so this is a quality
gate rather than a blocker, but it cannot be applied retroactively to a graph
already gathered.

Sized at **32767** (8× the default, ~32 MB per agent). FMT2's control planes have
**8 GB** of RAM, which is the constraint worth checking before going higher — its
workers have 32 GB and SEA1's nodes have 791 GB, where the cost is irrelevant.
Raise further if ring-buffer loss persists after a day.

### Stage 1 — observe before changing anything

**No config change. Zero risk.** Hubble reports flows regardless of policy mode,
so the traffic graph can be built right now, from both clusters, without touching
enforcement.

Collect `hubble observe --verdict FORWARDED -o jsonpb` streamed from relay, and
reduce to the tuple that policy is written in:

```
(src namespace, src workload/labels) -> (dst namespace, dst workload/labels), dport/proto
```

plus egress-to-world grouped by destination CIDR and FQDN.

**Window length is the single biggest determinant of policy quality, and the most
commonly rushed step.** A one-hour sample misses nightly backups, cert renewals,
weekly reports, log rotation, and every cron in the cluster. **Run at least 7
days.** Anything that only fires monthly must be enumerated by hand from
CronJobs, not discovered from flows.

### Stage 2 — shared vocabulary, landed inert

CIDRGroups are cluster-scoped and do nothing until a policy references them, so
they can land early and be reviewed on their own terms — exactly how SEA1 landed
its own (`9458ff75`).

SEA1 already has five: `bogons`, `link-local-metadata`, `sea1-rfc1918`,
`sea1-nodes` (`10.3.2.0/24`, `2602:fa6d:10:ffff::/64`), `sea1-loadbalancers`
(`10.3.3.0/28`, `2602:fa6d:10:ffff::e00/120`).

FMT2 needs its counterparts — `fmt2-nodes` (`10.65.67.0/24`), `fmt2-loadbalancers`
(`10.3.4.0/23`, `79.110.170.65/32`) — and the three cluster-agnostic ones
(`bogons`, `link-local-metadata`, `rfc1918`) should move to a shared base so the
two clusters cannot drift apart on what "bogon" means.

Note the constraint that shapes the whole policy library: **only
`CiliumNetworkPolicy` and `CiliumClusterwideNetworkPolicy` can use
`cidrGroupRef`.** Native `networking.k8s.io/v1` NetworkPolicy cannot. The 44
existing native policies therefore cannot share this vocabulary, which is an
argument for writing new work as CNP and converting the natives over time.

### Stage 3 — author, reviewed as a set

Per namespace, from the Stage 1 graph. Order matters:

1. Leaf workloads with narrow, obvious egress (deluge, ipfs — SEA1 already has
   `*-internet-only` CNPs to copy).
2. Datastores (postgres/CNPG, scylla, clickhouse, redis) — usually a small,
   well-defined client set.
3. Platform services (loki, tempo, victoriametrics, traefik).
4. **kube-system and argocd last.** See R1 and R3.

### Stage 4 — warn mode, FMT2 then SEA1

FMT2: `policyEnforcementMode: always`, `policyAuditMode: true`. Land the drafts
(they were inert under `never` anyway). Iterate against
`hubble observe --verdict AUDIT` until the only audits left are ones you have
consciously decided to deny.

Then SEA1, same flip, same iteration — with its existing 33 policies now
evaluated against a default-deny baseline rather than a default-allow one, which
will surface gaps in them too.

### Stage 5 — enforce

`policyAuditMode: false`, one cluster at a time, FMT2 first. This is its own
change, landed alone, with the audit stream watched for the first hour.

---

## Risks

**R1 — ArgoCD is both the casualty and the repair tool.** SEA1 carries 8 ArgoCD
policies, FMT2 also 8; they are upstream defaults written against a different
topology. If one is wrong, the thing that would push the fix is what broke.
*Mitigation:* argocd policies land last, after everything else is stable, and
verify `kubectl` against the API directly (which does not depend on ArgoCD) before
and after. Keep the direct `talosctl`/kubeconfig path — it does not traverse any
of this.

**R2 — audit mode is trusted, not proven.** The entire safety of Stage 4 rests on
`policyAuditMode` genuinely forwarding what it reports. *Mitigation:* FMT2 first,
where a failure costs visibility rather than enforcement, and watch
`cilium_drop_count_total` — under audit it must stay flat. Any rise means audit is
not doing what it claims, and that is an immediate revert.

**R3 — DNS is load-bearing for everything.** A wrong policy in kube-system takes
out CoreDNS and therefore the cluster. *Mitigation:* kube-system stays
unrestricted until the very end, and gets an explicit allow-all-egress-to-DNS
rule everywhere before anything else is tightened.

**R4 — the flow graph has gaps, and they are exactly the flows that matter.**
Measured on SEA1 on 2026-08-08:

```
hubble_flows_processed_total                              282,226,791
hubble_lost_events_total{source="hubble_ring_buffer"}         248,611   = 0.088%
hubble_lost_events_total{source="observer_events_queue"}            0
hubble_lost_events_total{source="perf_event_ring_buffer"}           0
hubble-event-buffer-capacity                              unset (chart default 4095)
```

The loss is specifically the **Hubble ring buffer** — the BPF perf ring and the
observer queue are both clean, so `hubble.eventBufferCapacity` is the right knob
and the only one that needs touching.

**0.088% is low, and it would be dishonest to call this an outage-grade problem.**
Bulk traffic is comprehensively covered. It matters for a narrower reason:
policy authoring does not care about the 99.9% of events that are the same
handful of chatty paths repeated. It cares about the *rare* flow — the nightly
cron that opens one connection, the failover path used twice a month. A single
lost event can erase a distinct `(src → dst:port)` tuple from the graph entirely,
and that tuple becomes a missing rule that fails open in Stage 4 and closed in
Stage 5.

The loss is also skewed and bursty rather than uniform — one SEA1 node accounts
for 170k of the 249k — which is consistent with bursts overrunning the ring, and
bursts are precisely when a rare flow is most likely to be the thing dropped.

*Mitigation:* raise `hubble.eventBufferCapacity` on both clusters before Stage 1
collection. This is a quality gate, not a blocker — worth doing first because it
is cheap and cannot be retrofitted onto a graph already collected.

**R5 — cross-cluster traffic is real and easy to miss.** These clusters are not
independent: FMT2 runs a `vmselect-sea1-egress`, a tailnet-exposed
`svc-vmselect-sea1`, and VictoriaMetrics scrapes `sea1-core`; SEA1 pushes to
`vminsert-fmt2`. Consul spans both. Policies written per-cluster in isolation will
deny the other cluster. *Mitigation:* this is the real reason to treat them as one
unit — the CIDRGroup vocabulary must include the peer cluster's node and pod
ranges and the tailnet, and both sides land together.

**R6 — identity is label-based.** Policies key on labels, so a workload that
changes labels silently falls out of its policy — failing open before Stage 5 and
closed after. *Mitigation:* prefer stable selectors, and add an alert on
`cilium_policy_endpoint_enforcement_status` for endpoints unexpectedly landing in
the unenforced bucket once Stage 5 is done.

---

## Verification

- `cilium_policy_endpoint_enforcement_status` by cluster — the count of enforced
  endpoints should jump to ~all pod-networked endpoints the moment `always`
  lands, on both clusters.
- `cilium_drop_count_total` flat throughout Stage 4 — the audit-is-real check.
- `hubble observe --verdict AUDIT` trending to zero as policies land.
- `hubble_lost_events_total` flat throughout.
- Per cluster, after Stage 5: ArgoCD reconciling, CoreDNS resolving from a pod in
  a restricted namespace, ceph `HEALTH_OK`, cross-cluster metrics still flowing
  (the FMT2↔SEA1 vmselect/vminsert path is the canary that covers R5).

## Rollback

Every stage is a single value:

- Stage 4 → `policyEnforcementMode: never` restores today's FMT2 behaviour, or
  `default` restores today's SEA1 behaviour.
- Stage 5 → `policyAuditMode: true` returns to warn without unwinding any policy.

Nothing here requires a node reboot, a pod recycle, or a CNI change. That is the
one genuinely comfortable property of this phase, and it is worth preserving by
never bundling a policy change with anything that does.

## CiliumCIDRGroups

Named lists of *external* CIDRs (argocd/apps/infra/cilium/sea1/cidr-groups.yaml),
referenced from CiliumNetworkPolicy / CiliumClusterwideNetworkPolicy so an
address list is written once instead of copied into every policy. Adding a
group on its own is a no-op — a CIDRGroup is inert until a policy references
it, which is why the lists can land and be reviewed before any policy depends
on them.

Two things to know before using them:

1. They are **cluster-scoped** (`kubectl api-resources` → NAMESPACED=false), so
   there is no namespace to put them in and they are visible to policies in
   every namespace automatically.
2. Only CNP and CCNP can reference them. Native networking.k8s.io/v1
   NetworkPolicy has no equivalent — its only address construct is
   `ipBlock.cidr`. A native policy that needs one of these lists has to be
   converted to a CNP first.

Referenced as:

```yaml
toCIDRSet:
  - cidrGroupRef: sea1-rfc1918
# or by label, which lets a policy pick up new groups without being edited:
toCIDRSet:
  - cidrGroupSelector:
      matchLabels:
        sea1.cidr/class: private
```

**What a CIDR selector does NOT match** — the thing that makes these subtle:
Cilium is identity-based, so a CIDR only matches a peer that carries a CIDR
identity. Pods (10.244.0.0/16, fd40:10:244::/56) each have their own identity;
ClusterIPs (10.96.0.0/12, fd40:10:96::/108) are DNAT'd before policy is
evaluated; and the nodes carry host/remote-node identity. None of those are
matched by a CIDRGroup. For in-cluster peers use `toEndpoints`; for nodes use
`toEntities: [host, remote-node]`.
