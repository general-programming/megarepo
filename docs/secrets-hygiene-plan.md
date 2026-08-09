# Secrets hygiene — Vault transport + committed credentials

Plan only; nothing here is applied. Everything below was read out of this repo
on 2026-08-09, plus the live-verified facts recorded in
`docs/fmt2-cilium-window0-findings.md` and `docs/nix/vault-server.md`.

**The short version:** the scary-sounding finding ("every secret crosses the
inter-site link as plain HTTP") is half true — the WAN hop is already inside
WireGuard, so the exposure is the two site LANs, not the internet. And the fix
is much cheaper than it looks: the Vault proxy already serves a valid
Let's Encrypt certificate on `:8200`, so moving both clusters to TLS is an
address change, not a PKI project. The committed credentials are two real
findings (traefik htpasswd, mastodon redis), both best handled as
rotate-and-accept-history.

---

## Where things actually are

**Vault topology.** Three NixOS raft nodes, `10.65.67.24–26`, HTTPS with
HSM-CA certs (`docs/nix/vault-server.md`). In front of them sits
`fmt2-vault-proxy` at **10.65.67.27**, which serves the API twice:

| Port | Transport | Certificate |
|---|---|---|
| `:8201` | **plain HTTP** | — |
| `:8200` | HTTPS | valid Let's Encrypt, SAN `vault-proxy.catgirls.dev` |

Every k8s client dials the plaintext one: `http://10.65.67.27:8201` appears in
`argocd/apps/infra/vault/{sea1,fmt2}/values_vault.yaml` (`externalVaultAddr`)
and `values_secrets_operator.yaml` (`defaultVaultConnection.address`). The
`:8200` HTTPS front was live-verified in the window-0 findings — right
hostname, right cert, no skip-verify needed.

**Who actually consumes it.** VSO only: 29 manifests use `VaultStaticSecret`.
The agent injector is `enabled: true` but **zero** workloads carry
`vault.hashicorp.com/` annotations; the CSI driver is installed but there are
**zero** `SecretProviderClass` objects. So the flip touches one real client
per cluster, and two idle ones.

**The inter-site path — investigated, and it downgrades the severity.** SEA1
nodes default-route to `10.3.2.1`, a VRRP VIP held by the `sea1-vpn-spine`/
`-leaf` VyOS guests. Those peer with `fmt2-vpn-spine-1/2` over **WireGuard**
links (`projects/barf/network.yml` `links:`, real WG keypairs per endpoint in
`cluster-secrets/wglink-*`), and fmt2-vpn-spine originates `10.65.67.0/24`
into that fabric. So SEA1→Vault traffic is encrypted across the WAN. What
remains cleartext: the SEA1 LAN leg (node → spine), the FMT2 LAN leg (spine →
proxy), and the routers themselves. That is still every secret *and every
ServiceAccount JWT used to log in* visible to anything on either L2 — which
includes all 166 hostNetwork pods that NetworkPolicy cannot cover
(`docs/netpol-phase3-sea1-fmt2.md`). Not internet-grade exposure; not
acceptable either.

**Auth.** `argocd/apps/infra/vault/base/vault-auth.yaml` mints a **non-expiring**
`kubernetes.io/service-account-token` Secret for the `vault-auth` SA and binds
it `system:auth-delegator`. Vault's k8s auth mounts use that token as
`token_reviewer_jwt`. A long-lived token with TokenReview power, stored in a
cluster Secret, is exactly the kind of credential bound tokens were built to
replace.

---

## Decision 1 — transport: use the HTTPS front that already exists

Options considered:

- **(a) End-to-end internal TLS / tailnet cutover.** Already designed as the
  "later phase" of `docs/nix/vault-server.md`: vault nodes on the tailnet,
  HSM-CA certs, proxy retired. Right end state, but it is gated on the
  declarative vault rebuild, and it needs CA distribution into both clusters
  (VSO `caCertSecretRef`, agent trust) plus tailnet reachability from pods,
  which today would mean egress proxies. Not the first move.
- **(b) Tailscale-fronted address now.** Same tailnet-reachability problem as
  (a) without its payoff; pods are not tailnet clients and the Connectors
  advertise LANs *into* the tailnet, not the reverse. Rejected.
- **(c) Accept, because the WAN hop is WireGuard.** Defensible against the
  original "cross-DC plaintext" framing, but it leaves secrets and
  reviewer-capable JWTs readable on two LANs by any hostNetwork pod or L2
  device, when the fix is one URL. Rejected.
- **(d) Flip clients to `https://10.65.67.27:8200`.** The cert is publicly
  trusted (LE), so there is **no CA to distribute and no rotation machinery to
  build** — renewal is the proxy's existing job. The cert's only SAN is
  `vault-proxy.catgirls.dev` and cluster DNS resolves that name to the public
  IP (79.110.170.57, holepunch-gated — see the window-0 findings), so clients
  must pin the name to the LAN IP: VSO `defaultVaultConnection.tlsServerName:
  vault-proxy.catgirls.dev` with the IP address keeps it to two values in one
  file per cluster. No DNS changes, no new components.

**Recommendation: (d) now, (a) later** exactly as `docs/nix/vault-server.md`
already sequences it — this change is even a listed step of that plan
("`externalVaultAddr`, also move off plaintext http"), done early instead of
last.

## Decision 2 — auth: retire the non-expiring reviewer token

Vault's kubernetes auth method uses the **login JWT itself** for TokenReview
when no `token_reviewer_jwt` is configured. VSO logs in with short-lived
projected tokens, so the path is: grant `system:auth-delegator` to the SAs
that actually log in (per-namespace `default` SAs via the
`defaultAuthMethod`, today), reconfigure the `kubernetes-sea1` / `kubernetes`
mounts without a reviewer JWT, then delete the static Secret from
`vault-auth.yaml`.

The honest tradeoff: auth-delegator moves from one dedicated SA to every SA
that authenticates, and the Vault-side mount config is out-of-band (not in
this repo), so it needs a maintenance note, not just a commit. If that trade
reads badly, the fallback is smaller but real: keep `vault-auth`, drop the
non-expiring Secret, and refresh the reviewer JWT from a bound token
periodically. Either way the permanent token goes.

## Decision 3 — committed credentials: rotate and accept history

`argocd/apps/infra/traefik/base/dashboard.yaml` was added in `6ab44f8c` (the
argp-apps migration), so **the hashes exist in the old repo's history too — a
rewrite here would not purge them**, and rewriting a shared monorepo breaks
every clone, open PR, and the commit hashes memorialized in these docs. A
history rewrite buys nothing and costs plenty. Treat every committed value as
burned, rotate it, and leave history alone.

---

## Sweep results

`grep` over `argocd/`, `infrastructure/`, `salt/`, `nix/`, plus a pass over
every `kind: Secret` manifest for non-empty `data`/`stringData`.

**Findings (2):**

| Where | What |
|---|---|
| `argocd/apps/infra/traefik/base/dashboard.yaml` | htpasswd for users `obw`, `genprog` (bcrypt `$2y$09`) and `nepeat` (**apr1/MD5** — offline-crackable) guarding the Traefik dashboard, both clusters |
| `argocd/apps/erin-apps/mastodon-coolmathgames/base/mastodon-values.yaml:173` | literal redis password (finding M-6 in `/home/erin/tmp/redis-auth-plan.md`) |

**Clean, checked and worth saying so:**

- The empty-shell pattern is used consistently: `Secret` with no data +
  `VaultStaticSecret` filling it (argocd-oidc, meilisearch, bsky-pds,
  tailscale-operator, rook-ceph shells).
- `infrastructure/talos/*/talsecret.sops.yaml` — sops-encrypted against Vault
  transit, no plaintext.
- `projects/barf/network.yml` — WireGuard **public** keys only; private keys
  live in `cluster-secrets/` paths in Vault.
- `salt/` and `nix/` — vault-agent templating throughout; no literals.
- `grafana` datasources Secret — URLs only. mastodon-values line 367
  (`password: "password"`) is a `secretKeys` *key name*, not a value.
- No PEM private key material anywhere outside `vendor/`.

---

## Sequence

### Stage 0 — verify the HTTPS front (read-only)

From a throwaway pod in each cluster: `openssl s_client` against
`10.65.67.27:8200` (SAN, `notAfter`), then `vault status` over HTTPS with the
name pinned. Confirms cert validity and that both clusters reach `:8200`
before anything changes.

*Rollback: none needed, read-only.*

### Stage 1 — transport flip, FMT2 first

FMT2: `values_vault.yaml` `externalVaultAddr: https://10.65.67.27:8200`,
`values_secrets_operator.yaml` `address` likewise +
`tlsServerName: vault-proxy.catgirls.dev`. Gate: every FMT2
`VaultStaticSecret` shows a fresh successful sync (`kubectl get
vaultstaticsecret -A`, status conditions) and Hubble shows zero remaining
flows to `10.65.67.27:8201`. Then SEA1, same change, same gate.

FMT2 leads for the same reason it always does: it is the cluster with less to
lose, and SEA1 hosts more of what would hurt.

*Rollback: `git revert`; selfHeal re-pushes the old address and VSO
reconnects within its next reconcile. Existing Secrets are never deleted by a
failing connection — see R1.*

### Stage 2 — write the new Vault keys (human, out-of-band)

New traefik dashboard passwords → fresh htpasswd (bcrypt for all three users;
the apr1 hash dies here) → `secret/app/traefik/dashboard`, key `users`. New
redis password → `secret/app/mastodon-coolmathgames/redis`, key
`redis-password`. **`kv patch`, never `kv put`**, per the redis-auth plan.
Inert until Stage 3/4 reference them. Runs after Stage 1 so the new values
never transit plaintext.

*Rollback: keys unreferenced; delete or ignore.*

### Stage 3 — traefik dashboard to VaultStaticSecret

In `traefik/base`: replace the Secret's `data` with an empty shell +
`VaultStaticSecret` (`destination.name: traefik-dashboard-auth`,
`create: false`, `refreshAfter: 1m`) — the argocd-oidc shape. The Middleware
is untouched. Gate: dashboard basic-auth accepts the new passwords and
rejects the old, both clusters.

*Rollback: `git revert` restores the committed hashes; the old passwords
still match them, so emergency access survives.*

### Stage 4 — mastodon redis to existingSecret

Drop `redis.auth.password`; set `redis.auth.existingSecret: mastodon-redis`
(bitnami subchart reads key `redis-password`; the mastodon chart wires the
same secret into `REDIS_PASSWORD` for web/sidekiq/streaming). Add the
`VaultStaticSecret` in `sea1/secrets.yaml` next to its four siblings. Expect
the sub-minute NOAUTH window the redis-auth plan documents; additionally this
redis has `master.persistence.enabled: false`, so the restart drops queued
sidekiq jobs and home feeds — regenerable, but say so in the PR.

*Rollback: `git revert`; chart falls back to the literal, which still matches
until Stage 2's value is set live — so do the Vault write and the values
change as one PR, revert as one PR.*

### Stage 5 — auth hardening

Per Decision 2, FMT2 mount first: grant auth-delegator to the logging-in SAs,
drop `token_reviewer_jwt` from the mount config (out-of-band), verify VSO
still syncs, then SEA1, then delete the Secret block from `vault-auth.yaml`.

*Rollback: re-add the reviewer JWT to the mount config; the SA and CRB never
left.*

### Follow-ups, out of scope here

The same `http://10.65.67.27:8201` address sits in `nix/justfile`,
`nix/modules/vault-agent.nix`, `nix/modules/salt-master/default.nix`,
`.sops.yaml`, and `talsecret.sops.yaml` — those flip with the tailnet cutover
already sequenced in `docs/nix/vault-server.md` (they run at bootstrap and
are the ones that can recreate the holepunch problem if flipped early). The
idle injector and CSI driver are removal candidates once confirmed unused for
a while longer.

---

## Risks

**R1 — `refreshAfter: 1m` + selfHeal make a bad address change propagate in
under a minute, everywhere.** Mitigated by VSO's failure mode: an unreachable
Vault means *stale* Secrets, not deleted ones — running pods keep their env
and mounts, and damage only lands when a pod restarts against a stale/missing
Secret. The revert path (git → ArgoCD) does not itself depend on Vault.
FMT2-first bounds the blast radius while the mechanism is proven.

**R2 — the proxy's LE renewal becomes load-bearing for both clusters.** Today
a lapsed cert on `:8200` inconveniences nobody; after Stage 1 it stops all
VSO syncs (stale, not broken — same R1 dynamics). Add a `notAfter` check to
monitoring at Stage 1, and the one-line rollback to `http` remains as the
break-glass.

**R3 — Stage 4 couples a Vault write with a values change.** The race
direction is documented in the redis-auth plan; the mitigation is landing
them together and reverting them together, plus the known
`rollout restart` unbreak if a client boots ahead of its config.

**R4 — the old credentials are burned, not retired.** Years of plaintext LAN
transit means the traefik passwords and redis password must be assumed
captured; rotation (Stage 2) is the fix, and reverting Stage 3/4 later must
not quietly resurrect the old values as the intended state.

## Verification

- `kubectl get vaultstaticsecret -A` all-synced on both clusters after each
  stage that touches transport or a Secret.
- Hubble: flows to `10.65.67.27:8201` drop to zero after Stage 1; `:8200`
  carries them instead.
- Traefik dashboard auth: new creds accepted, old rejected, both clusters.
- Mastodon: sidekiq draining, streaming connected, redis answering `NOAUTH`
  to unauthenticated `PING` from an unprivileged pod.
- Vault audit log: successful `kubernetes-sea1`/`kubernetes` logins after
  Stage 5, no auth errors trending.

## Rollback

Every stage is one `git revert` (Stage 5 adds one out-of-band mount-config
write). No stage requires a node reboot or touches workloads outside the two
apps being fixed, and no stage deletes a Vault key — the old values stay
readable until deliberately cleaned up after the plan lands.
