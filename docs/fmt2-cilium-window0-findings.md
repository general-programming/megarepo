# FMT2 → Cilium: Window 0 results

Executed 2026-08-08 against the live cluster. Read-only throughout — **no cluster
or repo changes were made.** Companions:
`docs/fmt2-cilium-migration.md` (what/why),
`docs/fmt2-cilium-landing-plan.md` (how).

**Bottom line:** the device audit came back clean and *simplifies* the plan. The
drift gate came back dirty in three separate ways, one of which is a live
split-brain and one of which is a new sharpest-edge risk. **Window 1 should not
open yet.**

---

## 0.1 Tooling — done, with one blocker

`talosctl` was absent. Obtained ephemerally via `nix shell nixpkgs#talosctl`
(v1.13.7) rather than mutating the profile. Client is newer than the v1.12.2
servers; fine for reads, and it warns on every call.

`~/.talos/config.fmt2` works: context `genprog-fmt2`, role `os:admin`, cert good
until 2033.

**sops/Vault — RESOLVED.** `.sops.yaml` encrypts to HashiCorp Vault transit at
`https://vault-proxy.catgirls.dev:8200` (public IP 79.110.170.57), which does not
respond from this host. `VAULT_ADDR` does **not** help: sops uses the
`vault_address` embedded in the file's metadata for *decryption*, not the
environment.

The same Vault is reachable at `https://10.65.67.27:8200`, and it serves a valid
Let's Encrypt certificate whose only SAN is `vault-proxy.catgirls.dev`. So the
only thing missing was name resolution. Fix, requiring no root and changing
nothing on the workstation:

```bash
cp /etc/hosts /tmp/hosts.override
echo "10.65.67.27 vault-proxy.catgirls.dev" >> /tmp/hosts.override
export VAULT_TOKEN=$(cat ~/.vault-token)
unshare --map-root-user --mount bash -c \
  'mount --bind /tmp/hosts.override /etc/hosts; cd infrastructure/talos/fmt2 && talhelper genconfig'
```

TLS validates properly (right hostname, right cert) — no `-k`, no skip-verify.
`genconfig` then succeeds, and `talosctl apply-config --dry-run` gives the real
diff. Everything below comes from that.

---

## 0.2 Device audit — clean, and it *removes* a step

All eight nodes are **identical**:

| | |
|---|---|
| Only `up` physical ether device | `ens18` |
| Node IP | on `ens18`, `10.65.67.44–.51/24` |
| `ens18` MTU | **9000** |
| Default gateway | `10.65.67.1` via `ens18`, **no MTU cap on the route** |
| On-link route | `10.65.67.0/24` dev `ens18` |

The ambiguity set that killed sea1's KPR — `bond0`, `dummy0`, `teql0`, plus
`sit0`, `tunl0`, `ip6tnl0` — **is present on every fmt2 node**, all `down`. So
the hazard is real and pinning is still mandatory. But because every node is
identical:

> **A single `devices: ens18` / `directRoutingDevice: ens18` works fleet-wide.
> fmt2 needs no `CiliumNodeConfig`. Landing-plan step 2.1 can be deleted.**

sea1 needed the per-node-group override because its node IP lived on `ens18` on
the VM control planes and `br0` on the bridged workers. fmt2 has no such split.

Two side effects: the talconfig comment *"confirm the NIC name on fmt2 (assumed
ens18)"* is now **confirmed correct**, and `routingMode: native` +
`autoDirectNodeRoutes` is confirmed legal — all eight node IPs are on one L2,
reachable on-link.

---

## 0.3 / 0.4 Drift gate — **FAILED. Ten deltas, four of them destructive.**

`talhelper genconfig` + `talosctl apply-config --dry-run` against live cp-0 and
node-0. Talos itself summarises the apply as *"Applied configuration with a
reboot"* — this is not a no-reboot change.

**Applying the generated config as-is would, on every node:**

| # | Delta | Severity |
|---|---|---|
| 1 | `apiServer.certSANs: [kube.generalprogramming.org]` **REMOVED** (CP) | **destructive** |
| 2 | `registries.config` **REMOVED** — the `registry.generalprogramming.org` credentials are deleted | **destructive** |
| 3 | `install.image` `installer-secureboot:v1.9.3` → `metal-installer-secureboot:v1.12.2`; `bootloader: true` dropped, `grubUseUKICmdline: true` added | **destructive** |
| 4 | `podSubnets` + `serviceSubnets` gain `fc00:cafe:cafe:1::/64` / `:2::/64` **on the control planes** | **destructive** |
| 5 | `features.kubePrism {enabled: true, port: 7445}` **added** (absent today) | important — see below |
| 6 | `features.hostDNS {enabled: true, forwardKubeDNSToHost: true}` added | important |
| 7 | `features.diskQuotaSupport: true` added | benign |
| 8 | `machine.certSANs` **gains** `kube.generalprogramming.org` (workers) | benign |
| 9 | New `HostnameConfig` (`auto: stable`) and `EthernetConfig` (`tx-checksum-ip-generic: false`) documents appended | benign/desired |
| 10 | `nodeLabels: node.kubernetes.io/exclude-from-external-load-balancers` (CP); `clusterName` (workers) | benign |

### The four that would break the cluster

**Δ1 — the API server would stop being valid for its own endpoint.** The cluster
endpoint is `https://kube.generalprogramming.org:6443` and that name is in the
live `apiServer.certSANs`. talconfig only sets `additionalMachineCertSans`, not
`additionalApiServerCertSans`, so the generated config drops it. Every kubectl,
every in-cluster client using that endpoint, fails TLS.

**Δ2 — private registry credentials vanish.** `registries.config` for
`registry.generalprogramming.org` exists live but is absent from talconfig
(it was set out-of-band). Applying deletes it, and image pulls from that
registry start failing on the next pull.

**Δ3 — a bootloader change on secureboot + TPM-sealed-LUKS nodes**, and Window 1
reboots all eight in sequence. This is exactly the interaction the plan revision
was written to avoid, now confirmed by Talos's own dry-run summary.

**Δ4 — resolving the split-brain in the dangerous direction.** The control planes
would gain the dual-stack `serviceSubnets`, which reallocates ClusterIPs and
recreates every Service.

### Correction to my earlier reading

I previously wrote that talhelper had "silently dropped" the `EthernetConfig`.
**That was wrong** — a bad recursive grep on my part. talhelper renders it
correctly into both generated files (Δ9). The accurate statement is narrower and
still true: the `EthernetConfig` has **never been applied to the live cluster**
(zero `ethernetspecs`, one config document per node), so the flannel checksum
workaround has never taken effect.

### The genuinely useful discovery: **KubePrism is not enabled on fmt2**

`features.kubePrism` is **absent from the live machine config on every node.**

Window 2 sets Cilium's `k8sServiceHost: localhost` / `k8sServicePort: 7445`,
which *is* KubePrism. Without it, the Cilium agent has no node-local endpoint to
reach the API server once kube-proxy is gone — and this is precisely the failure
mode sea1 hit from a different direction.

**So Window 2 has a hard machine-config prerequisite that sea1 did not have**
(sea1 already ran KubePrism). It must be enabled — and enabled *before* the KPR
flip, verified healthy on all 8 nodes — as its own surgical patch.

Related: Δ6 wants `hostDNS.forwardKubeDNSToHost: true`. sea1 deliberately runs
this **false**, and named Talos hostDNS as prime suspect in the port-53 breakage
behind the reverted `bpf.masquerade`. Do not let this one ride in unexamined.

### Delta 1 — live split-brain on pod/service subnets

This is worse than "committed but never applied". The dual-stack stanza *was*
applied — **to the workers only**:

| Node | podSubnets | serviceSubnets |
|---|---|---|
| cp-0 / cp-1 / cp-2 | `10.244.0.0/16` | `10.96.0.0/12` |
| node-0 … node-4 | `10.244.0.0/16`, **`fc00:cafe:cafe:1::/64`** | `10.96.0.0/12`, **`fc00:cafe:cafe:2::/64`** |

The cluster behaves single-stack because kube-controller-manager runs on the
control planes and allocates from their view (`--cluster-cidr=10.244.0.0/16`).
So the five workers carry a dual-stack declaration that nothing honours, and has
never had any effect. It is inert today — but it is *live inconsistent config*,
not merely uncommitted intent, and any future full apply resolves it in one
direction or the other.

**This must not be resolved as a side effect of the CNI migration.** Reordering
or adding `serviceSubnets` reallocates ClusterIPs and recreates every Service.

### Delta 2 — the flannel checksum workaround has never been applied

`talconfig.yaml` carries an `EthernetConfig` document disabling
`tx-checksum-ip-generic` on `ens18`, committed 2026-05-28 (`389ff149`) to dodge
the flannel VXLAN inner-checksum corruption bug.

Live: **every node has exactly one config document (`v1alpha1`), and
`talosctl get ethernetspecs` returns nothing on any node.** The
`EthernetConfig` was never applied.

Which means fmt2 has been running flannel VXLAN over virtio_net with tx checksum
offload **enabled** this whole time — exposed to precisely the bug that patch
was written to prevent, while the repo reads as though it is protected.

This strengthens the case for migrating: native routing has no encapsulation and
therefore no inner checksum, so the bug's precondition disappears rather than
being worked around.

### Delta 3 — stale installer image (the new sharpest edge)

All eight nodes pin:

```
install.image: factory.talos.dev/installer-secureboot/ce4c9805…:v1.9.3
```

…while actually running **Talos v1.12.2** and talconfig declaring
`talosVersion: v1.12.2`. The nodes were upgraded with `talosctl upgrade --image`,
which does not rewrite `install.image`.

**Why this is dangerous for this migration specifically:** a `talhelper
genconfig` + `apply` will rewrite `install.image` to a v1.12.2 schematic. Window
1 then reboots all eight nodes in sequence. A config-driven installer change
interacting with a full-fleet reboot roll, on **secureboot + TPM-sealed LUKS2**
nodes, is a much larger blast radius than "swap the CNI" — and it would be
happening for reasons unrelated to Cilium.

---

## 0.6 / 0.7 Preflight — green

- All 8 nodes `Ready`; 3 apiservers; 3 etcd voters, zero spare.
- `ceph-external`: **HEALTH_OK**, `Connected`. No in-cluster mons/OSDs.
- Pod MTU today: **8950** (9000 fabric − 50 VXLAN), read off a live
  `kube-system/workpod` pod. Confirms the `MTU: 8950` recommendation.
- Tailscale (sea1's L4 self-eviction trap): connector replicas are on
  **node-1 and node-3** — not co-located, so the roll order never drops both.
  But note **node-1 carries 5 of the 6 `ts-*` pods** (loki-gateway,
  vmselect-sea1, vmalert, vminsert, and a connector). Draining node-1 moves most
  tailnet plumbing at once; expect a visible blip, and do that node when you can
  watch it.

---

## What this changes in the plan

### Change 1 — drop the `CiliumNodeConfig` step

Landing-plan **2.1 is unnecessary**. Helm-level `devices: ens18` covers all eight
nodes. One fewer moving part at the exact step where sea1 broke.

### Change 2 — do NOT run `just apply-all fmt2` during this migration

This is the significant revision. The original plan gated on "make `diff-all`
clean, then apply." Given Deltas 1–3, making `diff-all` clean means resolving a
service-subnet split-brain *and* a bootloader-image change on secureboot nodes —
neither of which has anything to do with Cilium, and both of which are riskier
than the CNI swap itself.

**Instead, do what sea1 actually did, and then do what sea1 learned:**

- Apply `cluster.network.cni.name: none` with a surgical
  `talosctl patch machineconfig`, node by node. It touches exactly one field and
  cannot drag `install.image` or the subnets along with it.
- **Commit the same change to `talconfig.yaml` in the same working step** —
  sea1's `932dea14` lesson, honoured without paying for a full regeneration.
- Add a comment in `talconfig.yaml` recording that fmt2 is *known* to diverge
  from live on `install.image` and the worker dual-stack stanza, so the next
  person does not "fix" it with an unguarded `apply-all`.

The three drift deltas then become **their own follow-up project**, sequenced
after the CNI work, where a full `genconfig` + per-node `diff` + rolling apply
gets the attention it deserves. That is the same discipline the sea1 doc used to
keep the primary-family flip out of its CNI migration.

### Change 3 — Window 2 gains a prerequisite step

New **2.0**, before the KPR flip:

> Enable KubePrism (`machine.features.kubePrism {enabled: true, port: 7445}`)
> via surgical patch on all 8 nodes; commit the same to talconfig; verify
> `localhost:7445` answers on every node. Only then do 2.2.

Landing-plan step 2.1 was deleted (no `CiliumNodeConfig` needed) — 2.0 takes its
place as the thing that must land separately so a failure names its own cause.

### Change 4 — the drift is its own project, and it is bigger than "drift"

Δ1–Δ4 are not tidy-ups; they are four independent production incidents waiting in
a single command. That project needs, at minimum:

- `additionalApiServerCertSans: [kube.generalprogramming.org]` added to talconfig
  (fixes Δ1 by making the generated config match reality)
- the `registries.config` block moved into talconfig, with the credential in
  `talsecret.sops.yaml` (fixes Δ2)
- a decision on the installer/bootloader change, executed as a deliberate Talos
  upgrade, not as a side effect (Δ3)
- a decision on the dual-stack split-brain — resolve *down* (strip it from the
  workers) or *up* (accept every Service being recreated). Stripping is almost
  certainly right, since nothing uses it (Δ4)

**Sequence it after the CNI migration.** Bundling is exactly the mistake sea1's
doc warns against — and the CNI work now needs nothing from it except the
KubePrism patch, which is one field.

### Window 0 status

| Item | State |
|---|---|
| 0.1 talosctl | done (ephemeral `nix shell`) |
| 0.1 sops/Vault | **resolved** — hosts override in a mount namespace |
| 0.2 device audit | **done, clean** — `ens18` fleet-wide, no `CiliumNodeConfig` |
| 0.3 genconfig | **done** |
| 0.4 drift enumerated | **done — gate FAILED, 10 deltas** |
| 0.4 drift *reconciled* | **not done — deliberately deferred to its own project** |
| 0.5 consul fallback decided | outstanding — a paper decision |
| 0.6 baselines | partial (MTU 8950, ceph HEALTH_OK, 8/8 Ready, 3 etcd); the full matrix still wants a run |
| 0.7 tailscale placement | done — replicas on node-1/node-3, not co-located |

**One decision needed from you:** confirm the drift project is deferred and that
`cni.name: none` goes in as a surgical patch. With that, Window 1 can proceed —
step 1.1 (add the Cilium app, unsynced) is non-destructive and reversible by
deletion, and needs nothing from the drift work.

**Do not run `just apply-all fmt2` on this cluster until Δ1–Δ4 are fixed in
talconfig.** That is the single most important sentence in this document.
