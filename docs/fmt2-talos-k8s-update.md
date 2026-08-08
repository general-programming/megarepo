# FMT2: Talos + Kubernetes update, and the config reconciliation

Runs **before** the Cilium migration and replaces the "drift project" in
`docs/fmt2-cilium-window0-findings.md`. Companion docs:
`docs/fmt2-cilium-migration.md` (why Cilium), `docs/fmt2-cilium-landing-plan.md` (how),
`docs/fmt2-cilium-window0-findings.md` (what Window 0 found).

**Scope, as requested:** update Talos and Kubernetes, enable KubePrism, go
dual-stack, remove the custom registry, keep the API server SANs. Then hand back
to the Cilium thread.

**Target:** Talos **v1.13.7**, Kubernetes **v1.36.2** — exactly what sea1 runs.
Proven on the sibling cluster, and Cilium 1.20.0's support matrix is k8s
1.33–1.36, so this lands inside it rather than pushing past it.

---

## Two corrections to what I said earlier

**1. Dual-stack is much cheaper than I implied.** I said adding `serviceSubnets`
would recreate every Service. That is wrong, and it changes the scope of this
work. *Reordering* `serviceSubnets` flips the primary family and reallocates
every ClusterIP — that is the expensive operation sea1 ruled out. *Appending* a
second family is the documented, non-disruptive single→dual-stack path: existing
Services stay `SingleStack` IPv4 on their current ClusterIPs, and only Services
that explicitly opt into `ipFamilyPolicy: PreferDualStack`/`RequireDualStack`
get a second address. Existing pods likewise keep their v4 addresses until their
sandbox is recreated.

**2. The `EthernetConfig` is rendered fine.** My "talhelper silently dropped it"
claim was a bad grep. It renders into both generated files; it has simply never
been *applied* to the live cluster.

---

## The blocker nobody has hit yet: both committed IPv6 CIDRs are invalid

`infrastructure/talos/fmt2/talconfig.yaml` currently declares:

```yaml
podSubnets:     [10.244.0.0/16, fc00:cafe:cafe:1::/64]
serviceSubnets: [10.96.0.0/12,  fc00:cafe:cafe:2::/64]
```

**Neither of these can work.** This is why the split-brain has been harmless so
far — the half that would have failed is the half that never reached a control
plane.

### `serviceSubnets` — a /64 will be rejected outright

kube-apiserver caps the IPv6 Service CIDR at **/108**. A `/64` is refused at
startup (`specified --service-cluster-ip-range is not valid: too many IPs`).
Apply this to a control plane and **the API server does not come back.** With
three control planes rolled in sequence, the first one tells you — which is
survivable, but only if you are rolling one at a time and watching.

sea1 uses `/108` for exactly this reason.

### `podSubnets` — a /64 gives you one node out of eight

kube-controller-manager's `--node-cidr-mask-size-ipv6` defaults to **64**.
Carving /64s out of a /64 yields exactly one allocation: the first node to ask
gets it, and the other seven get no IPv6 podCIDR at all.

sea1 uses a **/56**, which yields 256 per-node /64s.

### Also: the prefix is in the wrong half of ULA space

`fc00::/7` is the ULA block, but it is split — `fd00::/8` is the half defined for
locally-generated prefixes, and `fc00::/8` is unassigned. sea1 correctly uses
`fd40:…`. Since these ranges have to be renumbered anyway to fix the sizes,
move to `fd00::/8` at the same time.

### Recommended replacement

```yaml
podSubnets:     [10.244.0.0/16, fd00:cafe:cafe:1::/56]
serviceSubnets: [10.96.0.0/12,  fd00:cafe:cafe:2::/108]
```

> **Do not copy sea1's service prefix literally.** Its talconfig reads
> `d40:10:96::/108`, which parses as `0d40:…` — outside ULA entirely, almost
> certainly a typo for `fd40:10:96::/108`. Worth raising on sea1 separately.

---

## Pre-flight blockers found on the live cluster

### P1 — three PDBs allow zero disruptions and will stall the roll

| PDB | Allowed disruptions |
|---|---|
| `attic/attic-db-primary` | **0** |
| `tempo/tempo-helm-distributor` | **0** |
| `tempo/tempo-helm-ingester` | **0** |

Every phase below reboots all eight nodes. A PDB at 0 blocks `kubectl drain`
indefinitely.

The two `tempo-helm-*` ones look like **orphans**: `tempo` also has
`tempo-distributor` and `tempo-ingester` PDBs which allow 1, i.e. the same
workloads covered twice from an older chart install. Verify and delete the
stale pair. `attic/attic-db-primary` is a CNPG primary at one replica — scale
it, or accept a brief failover and drain with a deliberate override.

**Fix these before the first reboot, not during it.**

### P2 — storage is unusually cooperative

All 27 PVs are on **external** Ceph (13 `ceph-rbd`, 11 `cephfs`, 3
`ceph-rbd-xfs`); **zero `local-path` PVs**. Nothing is pinned to a node by
storage, so drains move freely. This is a better position than sea1, which had
two immovable local-path replicas to rebuild before it could drain anything.

### P3 — the usual suspects

- 3 etcd voters, **zero spare** — one control plane at a time, always.
- `node-1` carries 5 of the 6 `ts-*` pods; expect a tailnet blip when it drains.
  The two connector replicas are on node-1 and node-3, so the route survives.
- `ceph-external` is `HEALTH_OK` — precondition for every reboot.

### P4 — secureboot + TPM-sealed LUKS

`install.image` moves from `installer-secureboot:…:v1.9.3` to
`metal-installer-secureboot:…:v1.12.2+` (Talos renamed these in the 1.11 era),
`bootloader: true` is dropped and `grubUseUKICmdline: true` appears. On nodes
whose disks are LUKS2 sealed to the TPM, a bad bootloader transition is the one
failure here that is not a quick rollback.

**Do the first node alone, prove it boots and unseals, then continue.** Pick a
worker — never a control plane — as the canary.

---

## Phases

Each numbered item is one commit. `just diff <cluster> <node>` before every
apply, always.

### Phase A — make `diff-all` say only what you mean

No version changes yet. The goal is a generated config whose diff against live
contains **nothing destructive**.

- **A.1** *Commit:* add `additionalApiServerCertSans: [kube.generalprogramming.org]`
  to `talconfig.yaml`. **This is the fix for the worst delta** — without it the
  generated config drops the SAN and the API server stops being valid for the
  endpoint every client uses.
- **A.2** *Commit:* fix the IPv6 CIDRs to `fd00:cafe:cafe:1::/56` and
  `fd00:cafe:cafe:2::/108` — but **leave them commented out or otherwise
  inactive for now**; they land in Phase D. Landing the corrected values as dead
  config here means the sizing gets reviewed on its own terms, the way sea1
  landed its CIDRGroups before anything referenced them.
- **A.3** *Commit:* add `machine.features.kubePrism {enabled: true, port: 7445}`.
  This is the Cilium prerequisite, and it is useful on its own.
- **A.4** *Decide and commit:* `machine.features.hostDNS`. The generated config
  wants `{enabled: true, forwardKubeDNSToHost: true}`. **sea1 deliberately runs
  `forwardKubeDNSToHost: false`** and named Talos hostDNS as prime suspect in the
  port-53 breakage behind its reverted `bpf.masquerade`. Match sea1 unless there
  is a reason not to.
- **A.5** *Commit:* remove the custom registry. Verified safe: **zero** running
  pods and **zero** workload specs reference `registry.generalprogramming.org`,
  there are **zero** `dockerconfigjson` secrets, the `registry` namespace is
  **empty**, and the endpoint does not respond. Deleting `registries.config` is
  pure cleanup — just do it deliberately, in this commit, rather than as a
  silent side effect of a generated apply.
- **A.6** Run `just gen fmt2 && just diff-all fmt2`. **Gate:** the remaining diff
  must be only — installer image/bootloader (Phase B), `HostnameConfig` +
  `EthernetConfig` additions, `diskQuotaSupport`, worker `machine.certSANs`,
  `clusterName`, and the CP `exclude-from-external-load-balancers` label. If
  anything else appears, stop and account for it.

  > To run `genconfig` from erin's workstation, sops needs
  > `vault-proxy.catgirls.dev` to resolve to `10.65.67.27` — see the recipe in
  > `docs/fmt2-cilium-window0-findings.md` §0.1. `VAULT_ADDR` does not work.

- **A.7** Fix P1 (the three zero-disruption PDBs). Not a talconfig change, but it
  gates every reboot from here on.
- **A.8** Apply Phase A, **one node at a time**, worker-first, canary alone
  (P4). Baseline first: etcd snapshot + `talosctl support` bundle.
  Verify per node: Ready, disks unsealed, `localhost:7445` answers (A.3),
  ceph `HEALTH_OK`, and the API server still presents a cert valid for
  `kube.generalprogramming.org` (A.1).

### Phase B — Talos v1.12.2 → v1.13.7

One minor version; within the supported upgrade path.

- **B.1** *Commit:* `talosVersion: v1.13.7` in `talconfig.yaml`.
- **B.2** Confirm the schematic ID `ce4c9805…` still resolves on
  `factory.talos.dev` for 1.13.7 **with** the `qemu-guest-agent` extension, and
  that the secureboot variant exists. Do this before touching a node.
- **B.3** `just upgrade fmt2 <node>` one at a time, workers first, control planes
  last. Drain first — `just upgrade` reboots. Ceph `HEALTH_OK` and full etcd
  quorum between each.
- **B.4** After the canary: confirm it boots, TPM unseals the LUKS volumes, and
  `talosctl version` reports v1.13.7. Only then continue.

### Phase C — Kubernetes v1.35.0 → v1.36.2

- **C.1** *Commit:* `kubernetesVersion: v1.36.2`.
- **C.2** `talosctl upgrade-k8s --to 1.36.2`. This rolls the control-plane static
  pods and kubelets; it does not need a node reboot.
- **C.3** Verify: all 8 nodes `v1.36.2`, all control-plane components healthy,
  ArgoCD reconciling, no CRD/API-removal fallout. **k8s 1.36 removes APIs
  deprecated in earlier releases** — before running this, sweep the megarepo and
  the live cluster for anything still on a removed version.
- **C.4** Soak. Do not stack Phase D on the same day.

### Phase D — dual-stack

- **D.1** *Commit:* activate the corrected subnets from A.2:
  `podSubnets: [10.244.0.0/16, fd00:cafe:cafe:1::/56]`,
  `serviceSubnets: [10.96.0.0/12, fd00:cafe:cafe:2::/108]`.
  IPv4 stays **first**, therefore primary. Do not reorder.
- **D.2** Apply to **one control plane first and watch the API server come
  back.** This is where an oversized service CIDR would have killed it; the
  corrected /108 is what makes this safe. Then the other two, then the workers.
- **D.3** Verify: every node gets an IPv6 `podCIDR`; kube-controller-manager
  shows both ranges; a test Service with `ipFamilyPolicy: PreferDualStack` gets
  two ClusterIPs; every **existing** Service is untouched.

> **Worth considering: slide Phase D into the Cilium cutover instead.**
> Enabling v6 while flannel is still the CNI means Talos wires up a v6 VXLAN
> overlay — the exact thing that needed `mss-clamp` on sea1 — and you are about
> to delete it. Doing D after Cilium is up costs nothing extra: Cilium takes
> `ipv6.enabled: true` + `ipv6NativeRoutingCIDR` with no encapsulation and no
> MSS games. Phase D is written last here precisely so it can slide by one step
> without disturbing anything else. Your call; the addressing decisions in A.2
> are the same either way.

### Phase E — hand back to the Cilium thread

State on completion: Talos 1.13.7, k8s 1.36.2, KubePrism on, SANs kept, registry
gone, dual-stack (here or at cutover), and a talconfig whose `diff-all` is clean.

That clears every prerequisite the Cilium plan was blocked on:

- **Landing-plan step 2.0 (enable KubePrism) is already done** — delete it.
- Landing-plan step 1.2 can go back to a normal `just apply` instead of a
  surgical patch, because there is no longer a destructive delta riding along.
- Cilium values gain `ipv6: {enabled: true}` and
  `ipv6NativeRoutingCIDR: fd00:cafe:cafe:1::/56` if Phase D landed here.
- `MTU: 8950` still stands, unchanged.

---

## Rollback

- **Phase A:** revert the commit and re-apply. Config-only, no version movement.
  The one-way door is A.5 if the registry credential is not recoverable — copy it
  out of the live config before deleting (it is in `machine.registries.config`).
- **Phase B:** Talos keeps the previous install; `talosctl rollback` returns a
  node to it. Per node, so the canary is a genuine test.
- **Phase C:** `upgrade-k8s` is reversible by re-running at the old version, but
  treat it as forward-only once workloads have started using 1.36 APIs.
- **Phase D:** removing the second family from `serviceSubnets` after Services
  have taken v6 addresses is messy. Land it only when you are content to keep it.

## Sequencing summary

| Phase | Change | Reboots | Gate |
|---|---|---|---|
| A | config reconciliation (SANs, KubePrism, hostDNS, registry) | 8, rolling | clean `diff-all`; PDBs fixed |
| B | Talos 1.12.2 → 1.13.7 | 8, rolling | canary boots + unseals |
| C | k8s 1.35.0 → 1.36.2 | none | no removed-API fallout |
| D | dual-stack | 8, rolling | one CP first, API server returns |
| E | → Cilium thread | — | — |
