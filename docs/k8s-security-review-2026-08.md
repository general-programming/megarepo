# Security review — SEA1 + FMT2, 2026-08-09

A fresh review of both clusters, scoped to what is *not* already covered by
`docs/netpol-phase3-sea1-fmt2.md` (network policy) and
`docs/secrets-hygiene-plan.md` (Vault transport, committed credentials). Every
number was read off the live clusters on 2026-08-09.

**The short version:** the preventive baseline is genuinely good — Talos gives
you anonymous-auth off, etcd encryption at rest, LUKS2 system disks, audit
policy, NodeRestriction, and `cluster-admin` bound to nothing but
`system:masters`. The gap is not in prevention, it is in **detection**: the API
audit log is written to an ephemeral partition and then thrown away, at
~7.3 GB/day on SEA1, of which 96% is noise from one misbehaving client. That
matters more as the netpol work tightens, because it is the only record of what
a stolen credential did.

Two findings in the first draft of this review were overstated, and the
corrections are recorded in place rather than quietly dropped:

- **The break-glass credential was not expired.** I tested a gitignored
  leftover; the real one is valid to 2027-07-26. The genuine defect was stale
  endpoints, now fixed (Finding 2).
- **Unlabelled namespaces were not unprotected.** Talos already defaults every
  namespace to `enforce: baseline`. Labelling them buys tightening to
  `restricted` and reviewability, not door-closing (Finding 3).

**Since this review was written**, PSA now carries an explicit level on every
namespace on both clusters except `kube-system` (Talos-exempt), and 13 empty
namespaces have been deleted. The live blocker is that sops cannot decrypt
from the workstation, so Talos configs cannot be regenerated and FMT2 has no
local break-glass at all.

---

## What is already right

Worth stating, because it bounds the rest of the review.

| Control | SEA1 | FMT2 |
|---|---|---|
| `--anonymous-auth` | `false` | `false` |
| `--encryption-provider-config` (etcd at rest) | set | set |
| `--enable-admission-plugins` | `NodeRestriction` | `NodeRestriction` |
| audit policy + log | configured | configured |
| `systemDiskEncryption` | LUKS2, state + ephemeral | LUKS2, state + ephemeral |
| `cluster-admin` subjects | `Group/system:masters` only | `Group/system:masters` only |
| RBAC `escalate`/`bind`/`impersonate` | built-in `admin`/`edit` only | built-in `admin`/`edit` only |
| `nodes/proxy` grants | `promtail-helm` only | `promtail-helm` only |
| Talos ingress firewall | yes | **yes** |
| Public LoadBalancer IPs | none | `79.110.170.65` |

Two corrections to the standing docs:

- **`docs/netpol-phase3-sea1-fmt2.md` is stale on the host half.** It says
  "FMT2 has no ingress firewall today". FMT2 got one in `3d6aa526`; both
  clusters now carry `NetworkRuleConfig`. The hostNetwork story is in better
  shape than that doc implies.
- The `system:anonymous` probe is rejected on both clusters, so the API plane
  is not the soft edge.

Policy objects have also moved since that doc was written: SEA1 is now 34
NetworkPolicy / 9 CNP / 3 CCNP / 6 CIDRGroup, FMT2 11 / 7 / 3 / 6. FMT2 gained
CNP and CIDRGroup coverage it did not have.

---

## Finding 1 — the API audit log is written to a node and then discarded

**This is the highest-value gap in the review.**

Both API servers run with:

```
--audit-log-path=/var/log/audit/kube/kube-apiserver.log
--audit-log-maxage=30 --audit-log-maxbackup=10 --audit-log-maxsize=100
```

Nothing collects that file. A `grep` across `argocd/`, `nix/` and
`infrastructure/` for `audit/kube` or `kube-apiserver.log` returns **zero**
hits; Alloy/promtail scrapes container stdout, not the host audit path, and
there is no `machine.logging.destinations` in either `talconfig.yaml`.

So the retention is 10 × 100 MB **per control plane node**, on a Talos node
whose `/var` is the ephemeral partition. Every `talosctl upgrade`, every
reinstall, every node replacement takes the history with it. SEA1's control
planes are 2–3 days old — whatever they saw before that is already gone.

### Measured volume, and what shipping it actually costs

Rates measured directly on the SEA1 control planes over a 180 s window:

| Node | Rate | Per day | Retention at 1 GB ring |
|---|---|---|---|
| `sea1-k8s-0` | 15.6 MB/h | 0.37 GB | ~2.7 days |
| `sea1-k8s-1` | ~123 MB/h | 2.9 GB | ~8 hours |
| `sea1-k8s-2` | 173 MB/h | 4.1 GB | ~6 hours |

**≈7.3 GB/day for SEA1**, wildly uneven across nodes. The busiest node holds
**six hours** of history, not the thirty days the `maxage` flag implies.

The load question has a reassuring answer: **shipping the log adds no writes to
the control planes.** Those 7.3 GB/day are already being written today — Alloy
would only *read* the file. The new writes land on Loki's Ceph-backed store,
and audit JSON is extremely repetitive (~1044 bytes/event, one `level`, a
handful of usernames), so it compresses roughly 10–15× in Loki's chunks. That
is **~600 MB/day compressed for SEA1**, call it ~1 GB/day for both clusters,
~2 GB/day physical at the `size=2` pool replication. Thirty days is ~60 GB.
That is not a meaningful ask of this Ceph cluster, and it is nothing next to
what the SSDs are already absorbing to write the logs in the first place.

### It should be much quieter than it is

Sampling 4000 consecutive events, **96% of the audit log is noise**:

| Subject | Share |
|---|---|
| `system:serviceaccount:loki:alloy` | **46%** |
| `system:node:*` | 32% |
| `system:apiserver` | 18% |
| everything else | 4% |

Every event is `level: Metadata` — there are no request bodies inflating this.
It is purely volume.

Alloy's 46% is a genuine pathology, not normal operation: 502 of its sampled
requests are `get /version` (a healthcheck hot loop), and most of the remainder
are pod-log `follow=true` reconnects hammering two `nix-cache-builder` pods —
the same pod re-opened dozens of times, i.e. a log tail reconnecting in a
storm. That is worth fixing on its own merits; it is load on the API server as
well as on the disk.

**So the order matters: tune the audit policy first, then ship.** Adding drop
rules for `system:node:*`, `system:apiserver`, and Alloy's `/version` probe
cuts volume by roughly 75% **at the source** — taking SEA1 from ~7.3 GB/day to
under 2 GB/day. That reduces SSD writes that are happening *right now*, before
a single byte goes to Loki. Done in that order, adding audit shipping is a net
**reduction** in disk pressure, and the log that lands in Loki is the 4% that
actually carries security signal.

The asymmetry is stark and worth naming: Hubble gives you flow-level
observability with policy verdict correlation across 547 pods, and it is
genuinely good. On the API plane — where "who bound themselves cluster-admin",
"who read that Secret", "who created a privileged pod" lives — there is
**nothing after ~10 files of rotation**. Netpol work reduces what an attacker
can reach; it does not tell you what they did with the credential they already
have. Those Vault ServiceAccount JWTs in `docs/secrets-hygiene-plan.md` that
have been crossing two LANs in plaintext for years are exactly the credential
class where you would want to answer "was this ever used, and by whom".

**Recommendation.** Ship the audit log to Loki. Alloy already runs as a
DaemonSet on every node with `/var/log` hostPath mount support in the chart
(`alloy.mounts.varlog`); it needs the mount enabled on control planes plus a
`local.file_match` + `loki.source.file` for `/var/log/audit/kube/*.log`, and a
`loki.process` stage to parse the JSON so `user.username`, `verb`,
`objectRef.resource` and `responseStatus.code` become labels.

Two things to check while doing it:

- **Talos's default audit policy is thin.** Confirm what
  `/system/config/kubernetes/kube-apiserver/auditpolicy.yaml` actually records
  before building alerts on it — if Secret reads land at `Metadata` level or
  are dropped, shipping the file faithfully preserves a log that does not answer
  the question. The policy is overridable via a Talos machine config patch.
- **Volume.** Set the Loki stream up with its own retention; audit is chatty and
  you do not want it competing with app logs.

Then the alerts that make it worth having: `create` on
`clusterrolebindings`, any `escalate`/`bind`/`impersonate`, `pods/exec` and
`pods/attach`, `get`/`list` on `secrets` by a subject outside the known
controller set, and anything from `system:anonymous` (which should be
identically zero, so it alerts cleanly).

---

## Finding 2 — break-glass worked, but pointed at three dead IPs *(resolved)*

**An earlier draft of this review reported the SEA1 break-glass credential as
expired. That was wrong, and the correction matters.** The expired cert was
`infrastructure/talos/sea1/talosconfig` — a gitignored *leftover* from Feb
2024. The real, talhelper-generated config lives at
`sea1/clusterconfig/talosconfig` and its cert is valid to **2027-07-26**.

Both files are gitignored deliberately, and correctly: they are admin
credentials. "FMT2 has no talosconfig committed" is the intended state, not a
gap — it is regenerated on demand by `talhelper genconfig`.

The genuine defect was smaller and entirely operational. The generated config's
baked-in endpoint list was **`10.3.2.16/17/18`**, from before the control-plane
replacement; the live nodes are `10.3.2.10/11/12`. A bare `talosctl version`
returned `no route to host` on all three. Break-glass worked only if you
already knew to override `-n`/`-e` — which is not a property you want in the
tool you reach for when the cluster is down at 3am.

**Fixed.** Repointed with `talosctl config endpoint` / `config node`; all three
nodes now answer flag-free. The stale expired file was removed (backed up
first), and `.claude/skills/talos-ingress-firewall/SKILL.md` was corrected — it
told readers to `export TALOSCONFIG=infrastructure/talos/sea1/talosconfig`,
i.e. exactly the dead file.

Two things remain:

- **This fix is a stopgap.** `clusterconfig/` is regenerated by `talhelper
  genconfig`, which will re-derive endpoints from `talconfig.yaml` — that file
  already carries the correct `10.3.2.10/11/12`, so the next genconfig is
  self-correcting. Run it to confirm rather than trusting this hand-edit.
- **FMT2 has no generated `clusterconfig/` locally at all**, so it has *no*
  working Talos break-glass on this workstation. Generating it is blocked —
  see below.

**Blocker worth its own attention.** `talhelper genconfig` fails at sops
decryption: the key metadata pins `https://vault-proxy.catgirls.dev:8200`, that
name resolves to the public `79.110.170.57`, and it is holepunch-gated and
unreachable from the workstation. This is the same name-pinning problem
`docs/secrets-hygiene-plan.md` Decision 1 solves for in-cluster clients — but
sops reads the address from the file's own metadata, so `tlsServerName`
pinning does not help here. **Anything requiring sops decryption of
`talsecret.sops.yaml` is currently blocked from this workstation**, which
includes regenerating either cluster's Talos configs. That is a bigger deal
than the endpoint drift was, and it should be resolved before the netpol
stages that can cause lockout.

Still recommended regardless: add a `notAfter` check on the Talos client cert
to monitoring, next to the Vault-proxy cert check that
`docs/secrets-hygiene-plan.md` R2 already calls for. The current cert quietly
expires in July 2027.

---

## Finding 3 — PSA was unlabeled on 31 namespaces (resolved)

`pod-security.kubernetes.io/enforce` tally:

| | restricted | baseline | privileged | **no label** |
|---|---|---|---|---|
| SEA1 | 7 | 21 | 18 | **11** |
| FMT2 | 5 | 21 | 12 | **20** |

**Correction to an earlier draft:** this is *not* "PSA does nothing there".
Talos ships a cluster-wide `AdmissionConfiguration` — confirmed on-node, and
neither `talconfig.yaml` overrides it — that already defaults every namespace
to:

```yaml
defaults:  { enforce: baseline, warn: restricted, audit: restricted }
exemptions: { namespaces: [kube-system] }
```

So an unlabeled namespace is enforced at **baseline**, not left open. The one
true hole is `kube-system`, which is exempted outright — and that is the
conventional, near-unavoidable choice.

That materially lowers the severity of this finding. The remaining value is
real but narrower than "close an open door":

1. **Tightening past the default.** `baseline` still permits running as root
   and extra volume types. Namespaces that pass `restricted` should say so —
   that is a genuine gain the fallback cannot give you.
2. **Explicitness.** An implicit level is invisible in review and silently
   changes if the Talos default ever does.
3. **Enumerable exceptions.** A `privileged` carve-out with a comment is
   reviewable; an unlabeled namespace is not distinguishable from one nobody
   has assessed.

SEA1 unlabeled: `cert-manager cilium-secrets csi-secrets-store default
kube-node-lease kube-public kube-system metallb namespace scylla-operator
victoriametrics`

FMT2 unlabeled: the same set plus `bitnami-seal bsky-relay
eightyeightthirtyone html-render-server nginx-test test-nginx registry
webhook-mail znc grafana`-adjacent leftovers.

Some of these genuinely need `privileged` (`kube-system`, `csi-secrets-store`,
`metallb`, `cilium-secrets`) — the point is to *say so with a label* rather
than leave it implicit, because then the exceptions are enumerable and
reviewable. The interesting ones are the operator namespaces that do **not**
need it: `cert-manager`, `scylla-operator`, `victoriametrics`, and
`namespace`. Those hold controllers with cluster-wide Secret read (see the
RBAC sweep) and are a natural target; PSA at `baseline` costs nothing there
and closes the "compromised operator schedules a privileged pod" step.

`default` deserves its own mention: it is empty and unlabeled on both
clusters, which makes it the single most convenient landing zone in the
cluster. Label it `restricted`.

### The orphan

`argocd/apps/infra/namespace/base/scylla-operator.yaml` exists, carries correct
labels, and **is not listed in `base/kustomization.yaml`** — so it has never
been applied. The live namespace is instead created by the scylla-operator app
with no labels. A file that looks like coverage and is not is worse than an
absent one, because it defeats exactly the `grep` someone would use to check.

### What was done

Levels picked by the method `argocd/apps/infra/namespace/README.md` prescribes
— `kubectl label --dry-run=server` against live pods on **both** clusters,
taking the looser result, and reading the workload spec rather than trusting
the sweep for empty namespaces:

| Namespace | Level | Why |
|---|---|---|
| `scylla-operator` | `baseline` | orphan registered in the kustomization; sweep agrees on both |
| `victoriametrics` | `baseline` | 12/14 pods run as root; `restricted` fails both |
| `cilium-secrets` | `restricted` | secret store, holds no pods and is not meant to |
| `cert-manager` | `restricted` | all 3 pods pass on both clusters |
| `default` | `restricted` | nothing should run here; debug belongs in `workpod` |
| `kube-public` | `restricted` | ConfigMap only, never runs pods |
| `kube-node-lease` | `restricted` | Lease objects only, never runs pods |

`cert-manager`'s namespace is declared inside the *vendored* upstream manifest,
so it is set by a kustomize patch (`patch-namespace-psa.yaml`) rather than
duplicated into the namespace app — editing `upstream/` would be lost on the
next vendor bump.

### Post-merge correction: one namespace, one app

Five of the seven landed on both clusters on the first sync. **`scylla-operator`
and `victoriametrics` did not**, and the reason is worth recording because it
contradicts the assumption this change was written on.

I expected `ServerSideApply` to let the namespace app co-own the PSA labels
alongside whichever app declares the Namespace. It cannot. **ArgoCD applies
every Application under one shared field manager, `argocd-controller`**, so two
apps declaring the same object are the *same* manager applying twice — and an
SSA apply removes fields absent from the config being applied. The app that
syncs last silently strips the other's labels. Observed exactly that: the
namespace app reported `Succeeded` and `OutOfSync` simultaneously, and
`managedFields` showed a single manager owning only the four non-PSA labels.

Fixed by moving both namespaces' labels into their owning apps
(`victoriametrics/base/kustomization.yaml` extends its existing Namespace patch;
`scylla-operator/base/kustomization.yaml` gains one) and deleting the duplicate
files from the namespace app. The rule is now written into
`argocd/apps/infra/namespace/README.md` with the `managedFields`/`tracking-id`
commands to check ownership before adding a file there. The three Kubernetes built-ins are adopted with
`argocd.argoproj.io/sync-options: Prune=false`, so removing them from git can
never delete the namespace.

`kube-system` is deliberately **not** labelled: Talos exempts it cluster-wide,
so a label would be silently ignored, and a file implying otherwise would be
the same trap as the orphan.

Server-side dry-run against both live clusters: **FMT2 clean, SEA1's only
warning is three `Completed` netshoot debug pods in `default`** (`dnscheck0`,
`netcheck`, `recheck`, left over from the 2026-08-06 control-plane
replacement). `enforce` gates admission and does not evict, so they are
unaffected; they should be deleted as cleanup, and the label is what stops the
next one landing there instead of in `workpod`.

### Still to do

`metallb`, `csi-secrets-store` and `namespace` were deliberately **not**
labelled — they are empty on both clusters and are deletion candidates, not
labelling candidates (see Finding 4). `metallb` is vestigial on SEA1, which
moved to Cilium L2; FMT2 uses `metallb-system`. `csi-secrets-store` is
vestigial because the driver deploys into `kube-system`. `namespace` is a
namespace literally called `namespace`, created 2024-02-11 from an unrendered
template, empty on both clusters ever since.

---

## Finding 4 — dead namespaces, some three years old (resolved)

| Namespace | Pods | Created |
|---|---|---|
| `bitnami-seal` | 0 | 2023-05-27 |
| `html-render-server` | 0 | 2023-08-23 |
| `webhook-mail` | 0 | 2023-08-23 |
| `registry` | 0 | 2023-08-22 |
| `test-nginx` | 0 | 2023-08-21 |
| `nginx-test` | 0 | 2023-09-05 |
| `bsky-relay` | 0 | 2024-02-26 |

All empty, all unlabeled for PSA, none in ArgoCD. Two of them are *the same
nginx test* under transposed names, which is a good indicator of how
deliberate their continued existence is.

Empty namespaces are not dangerous in themselves. They matter because they are
unlabeled, un-netpol'd, and un-owned: they are where a `kubectl apply` lands
when someone is in a hurry, and they inflate the namespace count the netpol
plan reasons about (57 on FMT2 at the time that doc was written, of which 54
uncovered).

**One caveat, measured after the fact:** deleting them did *not* reduce the
count of namespaces that still need a policy, which is 20 on SEA1 and 13 on
FMT2 — that figure counts namespaces running pod-networked workloads, and
these had none. What shrank is the total namespace count (FMT2 57 → 46) and
the number of unowned places something can be scheduled. The policy-authoring
workload in `docs/netpol-phase3-sea1-fmt2.md` is unchanged.

### Done — 13 namespaces deleted

Checked exhaustively before deleting: every namespaced API resource type from
`kubectl api-resources --namespaced`, not just `kubectl get all`, which misses
CRDs like `VMPodScrape`, `CiliumNetworkPolicy` and `VaultStaticSecret`. All
thirteen contained nothing but the auto-created `kube-root-ca.crt` ConfigMap
and `default` ServiceAccount. `registry` held no pull credential.

Three more were deleted alongside the seven, having turned up during the PSA
sweep as empty on **both** clusters:

- **`metallb`** — vestigial. SEA1 retired MetalLB for Cilium L2 IPAM and its
  overlay is a deliberately empty kustomization; FMT2 uses `metallb-system`.
- **`csi-secrets-store`** — vestigial. The driver deploys into `kube-system`
  (`csi-secrets-store/base/kustomization.yaml` sets `namespace: kube-system`).
- **`namespace`** — a namespace literally called `namespace`, created
  2024-02-11 from an unrendered template and empty ever since.

Nothing recreates any of them. No finalizers hung; both clusters stayed
healthy. SEA1 went 56 → 53 namespaces, FMT2 57 → 46.

Also deleted the three `Completed` netshoot debug pods left in SEA1 `default`
(`dnscheck0`, `netcheck`, `recheck`) from the 2026-08-06 control-plane
replacement.

**Both clusters now have an explicit PSA level on every namespace except
`kube-system`**, which Talos exempts cluster-wide and where a label would be
silently inert.

---

## Finding 5 — `znc-external` is on the public IP as raw L4

FMT2's only public LoadBalancer IP, `79.110.170.65`, carries:

- `traefik/traefik-helm` — 80/443, expected
- `znc/znc-external` — **16969/TCP**

Everything else in both clusters reaches the internet through Traefik. This one
service is a direct L4 path from the internet to a pod, with no Traefik in
front, so no WAF, no Cloudflare, no rate limiting, no request logging, and
Traefik's access log does not see it. ZNC's own TLS and password auth is the
entire control.

That may be exactly the intent — ZNC is an IRC bouncer and IRC does not go
through an HTTP reverse proxy. The ask is that it be a *recorded* decision
rather than an artifact, in the same way `docs/traefik/` records the
cloudflare-realip pod-CIDR trust as an accepted risk. If it stays: confirm ZNC
is TLS-only on that port, that the image is current (it is on a mutable tag —
see Finding 6), and add an explicit CNP so its ingress is scoped and its egress
is not `world`.

---

## Finding 6 — supply chain: 15 mutable image tags, 19 digest pins out of 204

| | unique images | mutable tag | digest-pinned |
|---|---|---|---|
| SEA1 | 116 | 13 | 10 |
| FMT2 | 88 | 2 | 9 |

Mutable tags in use:

```
busybox:stable                      docker.io/clickhouse/clickhouse-keeper:latest
crazymax/rrdcached:edge             library/debian:latest
lscr.io/linuxserver/deluge:latest   matrixdotorg/synapse:latest
nepeat/librenms:latest              nicolaka/netshoot:latest
registry-1.docker.io/bitnami/redis:latest
ghcr.io/alex1989hu/kubelet-serving-cert-approver:main
ghcr.io/general-programming/cerebro:main
ghcr.io/general-programming/megarepo/coder-nix-workspace:latest
ghcr.io/volcengine/openviking:latest
ghcr.io/zhaofengli/attic:latest
ghcr.io/general-programming/megarepo/eightyeightthirtyone-scraper:latest
```

Two distinct problems. **Integrity:** a mutable tag means the image that runs
after the next restart is whatever the registry serves then, which makes a
registry or account compromise a silent code-execution path into the cluster —
and `bitnami/redis:latest` is notable given Bitnami's 2025 change to stop
publishing free tags, so that reference is both mutable and semantically
unstable. **Reproducibility:** you cannot answer "what was running last
Tuesday", and a crash-loop after an unrelated eviction becomes an unexplained
outage.

Renovate is already configured with `lockFileMaintenance` (`e73f83d3`).
Extending it to container digests is the low-effort path: pin
`image: repo:tag@sha256:...` and let Renovate raise the bump as a reviewable
commit. Prioritize `library/debian`, `busybox` and `netshoot` (base/debug
images with broad reach) and the three `general-programming` images (your own
build pipeline is the thing an attacker would target to reach both clusters).

---

## Finding 7 — six privileged containers that are avoidable

The privileged inventory is mostly legitimate and unavoidable — Cilium agent,
ceph OSDs and CSI node plugins, virt-handler, multus, secrets-store CSI,
tailscale connectors, smartctl-exporter. `nix-cache-builder` is privileged with
a documented, correct justification (the Nix sandbox needs mount and user
namespaces; `sandbox = false` is worse) — `argocd/apps/infra/nix-cache-builder/README.md`
already argues this properly and it should stay.

The exception is Elasticsearch in `mastodon-coolmathgames`. Six pods each run
a privileged `sysctl` init container whose entire job is:

```sh
sysctl -w vm.max_map_count=262144
sysctl -w fs.file-max=65536
```

The main `elasticsearch` container is already `privileged=false`. This is the
stock Bitnami `sysctlImage` pattern, and on Talos it is pure waste: set both
sysctls once in `machine.sysctls` in `talconfig.yaml` for the worker nodes,
then set `sysctlImage.enabled: false` in the chart values. Six privileged
containers become zero, and the namespace becomes eligible for PSA `baseline`
instead of needing `privileged`.

---

## Finding 8 — every pod gets a ServiceAccount token it does not use

`automountServiceAccountToken` is left on for **278 of 306** pods on SEA1 and
**228** on FMT2. The overwhelming majority of these workloads — deluge, znc,
ipfs, mastodon, the scrapers — never call the Kubernetes API.

Each mounted token is a credential sitting in a container filesystem, readable
by anything that achieves file read in that pod (a path traversal, an SSRF that
reaches `file://`, a debug endpoint). Modern projected tokens are audience-bound
and short-lived, which caps the damage a long way below the old
non-expiring-token era — this is a defence-in-depth item, not an urgent one.

It pairs with the Vault work, though: `docs/secrets-hygiene-plan.md` Decision 2
retires the non-expiring reviewer token, and the same reasoning applies to the
token every pod is handed by default. Set `automountServiceAccountToken: false`
on the `default` ServiceAccount in application namespaces, then re-enable
per-workload where something genuinely talks to the API. Doing it at the SA
level rather than per-pod means new workloads inherit the safe default.

---

## Finding 9 — ArgoCD defaults every authenticated user to `readonly`

`argocd-rbac-cm` on both clusters:

```
policy.csv:      g, admins, role:admin
policy.default:  role:readonly
scopes:          [groups, email]
```

`role:readonly` is not nothing: it reads every `Application`, its full rendered
manifests, and repository metadata across both clusters. Whether that matters
depends entirely on who can complete an Authentik login — if Authentik fronts
only a closed user set, this is fine and should be written down; if it has any
broader enrolment path, `policy.default` should be `""` (deny) with explicit
group grants, since ArgoCD is the most valuable read target in either cluster
(its application controller holds wildcard cluster RBAC on both).

Worth confirming against the Authentik provider config rather than assuming.

---

## Finding 10 — no policy engine, and netpol phase 3 has not reached stage 4

Both clusters run only operator-owned admission webhooks (cert-manager,
kubevirt, rook, scylla, VSO — 8+6 on SEA1, 7+4 on FMT2). There is **no
general-purpose policy engine**: nothing enforces "no `:latest`", "no
privileged outside this allowlist", "every namespace has a NetworkPolicy",
"images come from these registries". PSA is the only general admission control
in play, and Finding 3 shows it is unlabeled where it matters.

Related: Cilium is still `enable-policy: default`, `policy-audit-mode: false`
on **both** clusters. That is netpol phase 3 stage 3 — stages 4 and 5 have not
run. Uncovered namespaces with pod-networked workloads: **21 on SEA1**, **13 on
FMT2**.

I would **not** add Kyverno or Gatekeeper right now. Finish PSA labelling
(Finding 3), which is free and covers most of what a policy engine's first ten
rules would say, and finish netpol phase 3, which is already planned and
underway. A policy engine is the right *next* investment after both, at which
point its rules can be written to lock in what you already achieved rather than
to discover it. Adding it earlier means a second half-configured admission
layer competing with the first.

---

## Recommended order

Ranked by (risk closed × cheapness), and sequenced so that the things which
make the netpol work safe come before the netpol work.

| # | Item | Finding | Effort | Status |
|---|---|---|---|---|
| 1 | Repoint SEA1 `talosconfig` at live nodes; drop the expired leftover | 2 | — | **done** |
| 2 | PSA labels on every namespace but `kube-system`, both clusters | 3 | — | **done** (#585, #586, #588) |
| 3 | Delete 13 empty namespaces + 3 leftover debug pods | 4 | — | **done** |
| 4 | Unblock sops/Vault from the workstation; regenerate both `clusterconfig/` | 2 | ~1 h | **blocker** |
| 5 | Audit policy drop rules for `system:node:*`, `system:apiserver`, Alloy `/version` | 1 | ~2 h | next |
| 6 | Fix Alloy's `/version` hot loop and pod-log reconnect storm | 1 | ~2 h | next |
| 7 | Ship kube audit logs to Loki + the 5 alert rules | 1 | ~1 d | |
| 8 | Elasticsearch sysctls → Talos `machine.sysctls`, drop `sysctlImage` | 7 | ~1 h | |
| 9 | Decide + document `znc-external`; add its CNP | 5 | ~1 h | |
| 10 | Renovate digest pinning, base and first-party images first | 6 | ~half d | |
| 11 | Confirm the Authentik enrolment path behind ArgoCD `readonly` | 9 | ~30 m | |
| 12 | `automountServiceAccountToken: false` on app-namespace default SAs | 8 | incremental | |
| 13 | Netpol phase 3 stages 4–5 (already planned) | 10 | per that doc | |
| 14 | Policy engine — *after* 2 and 13, not before | 10 | later | |

**Item 4 is the one to take seriously.** It is not itself a security hole, but
it means the Talos configs cannot be regenerated from this workstation, and
FMT2 has no working break-glass here at all. Everything in the netpol plan
whose failure mode is lockout should wait behind it.

Items 5–7 are sequenced deliberately: **tune the audit policy before shipping
the log.** Done in that order the change is a net reduction in disk writes
(~7.3 → under 2 GB/day on SEA1) and what reaches Loki is signal rather than
Alloy healthchecks.

## Verification

- `TALOSCONFIG=infrastructure/talos/sea1/clusterconfig/talosconfig talosctl version`
  answers from all three nodes **with no `-n`/`-e` flags**. (Done.) The same for
  FMT2 once item 4 unblocks `talhelper genconfig`.
- The only namespace lacking an `enforce` label on either cluster is
  `kube-system` (Talos-exempt, intentional) — **verified, both clusters**:
  `kubectl get ns -o json | jq -r '.items[]|select(.metadata.labels["pod-security.kubernetes.io/enforce"]==null)|.metadata.name'`
- Audit volume drops below ~2 GB/day/cluster after the policy drop rules, measured
  the same way (file size delta over a fixed window), *before* Loki shipping is enabled.
- A Loki query for `{job="kube-audit"}` returns events from all 3 SEA1 and 3 FMT2 control planes.
- `kubectl get pods -n mastodon-coolmathgames -o json | jq '[.items[].spec.initContainers[]?|select(.securityContext.privileged==true)]|length'` returns `0`.
- A deliberate test `clusterrolebinding` create fires the audit alert, then is deleted.

## Out of scope, tracked elsewhere

Vault plaintext transport, the non-expiring reviewer JWT, and the two committed
credentials are `docs/secrets-hygiene-plan.md`. Network policy authoring,
enforcement mode, and hostNetwork coverage are
`docs/netpol-phase3-sea1-fmt2.md` — with the correction that FMT2's Talos
ingress firewall now exists.
