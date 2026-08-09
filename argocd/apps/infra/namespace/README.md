# Namespaces and Pod Security Admission

One file per namespace in `base/`, and **everything in `base/` lands on both
clusters** — verify against both before merging.

A namespace that exists on only one cluster goes in that cluster's overlay
instead (`fmt2/znc.yaml` is the only one today), and must *not* also be in
`base/`, or it gets created empty on the other cluster.

## The carve-out, in one line

PSA level is a namespace label. To grant an exception, change one word in one
file:

```yaml
pod-security.kubernetes.io/enforce: baseline   # -> privileged
```

Put a comment above it saying *which workload* needs it and *which control* it
trips, like `akvorado.yaml` does. That comment is the carve-out's justification
and the thing that lets someone remove it later.

There is deliberately no cluster-wide exemption list. Cluster-level PSA
exemptions live in Talos `AdmissionConfiguration`, which means a node config
change and a control-plane restart to grant one — a namespace label is the cheap
lever, so it is the only one we use.

## An unlabelled namespace is not unprotected

Talos ships an `AdmissionConfiguration` that neither cluster overrides:

```yaml
defaults:   { enforce: baseline, warn: restricted, audit: restricted }
exemptions: { namespaces: [kube-system] }
```

So a namespace with no labels is already enforced at **baseline**. Labelling it
buys tightening to `restricted`, explicitness in review, and an enumerable list
of `privileged` carve-outs — not "closing an open door". Read it off a node
with:

```sh
talosctl read /system/config/kubernetes/kube-apiserver/admission-control-config.yaml
```

**`kube-system` is exempt**, so labelling it does nothing. Do not add a file for
it — one that looks like coverage but is inert is worse than none.

Namespaces Kubernetes creates itself (`default`, `kube-public`,
`kube-node-lease`) live in `kube-builtins.yaml` and are adopted purely for their
labels, with `argocd.argoproj.io/sync-options: Prune=false` so removing them
from git can never delete the namespace.

## One namespace, one app — this is not negotiable

**If another app already declares a Namespace, its PSA labels must be patched in
that app. A second declaration here cannot win.**

ArgoCD applies every Application under a *single shared* server-side-apply field
manager, `argocd-controller`. SSA co-ownership is per-manager, not per-app, so
two apps declaring the same Namespace are the same manager applying twice — and
an SSA apply removes fields absent from the config it applies. Whichever app
syncs last silently strips the other's labels, and the loser sits `OutOfSync`
forever while reporting `Succeeded`.

Confirm ownership before adding a file here:

```sh
kubectl get ns <ns> -o json --show-managed-fields \
  | jq -r '.metadata.managedFields[]|"\(.manager) \(.fieldsV1["f:metadata"]["f:labels"]|keys)"'
kubectl get ns <ns> -o jsonpath='{.metadata.annotations.argocd\.argoproj\.io/tracking-id}'
```

A `tracking-id` naming another app means: patch it there, not here. Today that is
`cert-manager` (vendored manifest → `cert-manager/base/patch-namespace-psa.yaml`),
`victoriametrics` and `scylla-operator` (both patched in their own
`base/kustomization.yaml`). Editing a vendored `upstream/` file directly is also
wrong — it is lost on the next bump.

## Tiers

Each namespace carries `enforce` plus `warn`/`audit` set **one tier tighter**.
Nothing is blocked by `warn`/`audit`; they surface what a future tightening
would break, so the next step is always visible rather than needing a survey.

| enforce | meaning | warn/audit |
|---|---|---|
| `restricted` | hardened default: non-root, no privilege escalation, all caps dropped, `RuntimeDefault` seccomp | `restricted` |
| `baseline` | blocks the known privilege escalations; allows running as root and extra volume types | `restricted` |
| `privileged` | unrestricted — an explicit carve-out, always with a comment | `baseline` |

## How these levels were chosen

Not by inspection. Each namespace was evaluated with PodSecurity's own admission
check against the live pods in **both** clusters:

```sh
kubectl label --dry-run=server --overwrite ns <ns> \
  pod-security.kubernetes.io/enforce=restricted
```

A namespace was assigned the tightest level that produced no violation warning,
then the **looser** of the sea1 and fmt2 results was taken, because `base/` is
shared. Re-run the sweep after adding workloads.

Two limits of that method, worth knowing before you trust it:

1. **It only evaluates pods that exist right now.** A CronJob between runs has no
   pod and is invisible to the check. The only CronJobs in this repo live in
   `mastodon-coolmathgames` and `nix-cache-builder`, both of which stay
   `privileged`, so nothing was missed here — but re-check if that changes.
2. **`enforce` does not evict anything.** It gates admission, so a level that is
   too tight does not break running workloads; it blocks the *next* restart,
   rescheduling, or rollout. Failures show up late and look unrelated. That is
   what `warn`/`audit` are for.
3. **An empty namespace passes every level trivially.** Zero pods means zero
   violations, which reads as "restricted is fine" when it means "no evidence".
   `bsky-pds` (since removed), `kube-state-metrics` and `promtail` were empty on both clusters and
   were initially mislabelled `restricted` on exactly that false signal — the
   real answers, read out of their manifests, are `baseline`, `restricted` and
   `privileged` respectively. **For an empty namespace, read the workload spec;
   do not trust the sweep.** Those namespaces carry a comment saying so.

## Adding a namespace

Copy the nearest existing file, add it to `base/kustomization.yaml`, and run the
dry-run above against both clusters to pick the level. Default to `restricted`
and loosen only with a comment explaining why.
