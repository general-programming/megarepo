# sea1 Consul ACLs

**Status: phases 1-3 applied, except sea1-core's agent token. Phase 4 not
started and gated on human sign-off.**

| phase | state |
|---|---|
| 1 — permissive | done. `acl.enabled = true`, `default_policy = "allow"`, in the sea1 overlay's `server.extraConfig`. Verified: 3 raft voters, agent banner reads `ACL Enabled: true` / `Default Policy: allow`. |
| 2 — bootstrap | done. SecretID in Vault at `secret/infra/consul-sea1/bootstrap-token`, and in the `consul-bootstrap-acl-token` Secret in the `consul` namespace for phase 4. |
| 3 — policies and tokens | policies `anonymous`, `metrics-discovery`, `agent-sea1-core` created; `anonymous` attached to the built-in anonymous token; both tokens minted and in Vault under `secret/infra/consul-sea1/`. vmagent is wired and verified. **sea1-core is not wired** — see below. |
| 4 — flip to deny | **not started. Do not start it.** |

`bpf.masquerade` blocks the rest. sea1-core's consul agent can gossip but every
RPC fails, because a hostPort reply from a server pod is re-SNAT'd to an
ephemeral source port — see `.claude/skills/consul-sea1/SKILL.md`. Until that is
fixed, sea1-core cannot register `node_exporter`, the catalog is empty, and
there is no way to confirm sea1-core presents an agent token. Wiring its token
blind would mean discovering at the phase 4 flip whether it works, which is
exactly what the permissive window exists to avoid.

**Phase 4 preconditions, all of which must hold:**

1. `bpf.masquerade` landed and sea1-core's `consul catalog services` succeeds.
2. sea1-core holds `acl.tokens.agent` (token already minted; render it through
   `nix/modules/vault-agent.nix`, never in `consul.nix` directly) and its
   `node_exporter` registration is back in the catalog.
3. vmagent's consul SD returns a non-empty target list *with* its token.

## What changed since this was first written

The original plan targeted three salt-managed Proxmox hypervisors
(`sea1-hv-0/1/2`) that held the raft quorum. Those hosts were rebuilt as
bare-metal Talos nodes, which cannot run a packaged consul, and the DC ran with
zero servers until the servers were moved into Kubernetes as a chart-managed
StatefulSet on the same three machines
(`argocd/apps/infra/consul/sea1/consul.yaml`).

Three consequences, all of which make this work *easier* than the first draft
assumed:

1. **The chart can manage ACLs now.** `global.acls.manageSystemACLs` drives a
   `server-acl-init` job that has to reach the servers. It could not reach the
   salt hypervisors; it owns the servers outright today. Chart-managed tokens
   are now the default answer, not the fallback.
2. **The agent HTTP API is no longer exposed on the LAN.** The old client
   DaemonSet ran `hostNetwork: true` and published a wide-open `:8500` on every
   node. That DaemonSet is gone — every node runs a server instead, and the
   server pods publish only 8300/8301/8302/8502 as hostPorts. `:8500` is
   reachable only through the `consul-http` ClusterIP, from inside the cluster.
   The single largest pre-ACL exposure closed itself.
3. **firewalld is not the network control any more.** Salt's `managed_firewall`
   tag has nothing left to apply to in sea1. Talos' own ingress firewall
   replaces it — see `talos-ingress-firewall.draft.yaml`.

The gossip pool is now IPv4 on `10.3.2.0/23`; the servers bind `0.0.0.0` and
advertise `status.hostIP`. Anything below that names an address uses that.

## Why bother

The agent banner before any of this:

```
Gossip Encryption: false
      ACL Enabled: false
ACL Default Policy: allow
        HTTPS TLS: Verify Incoming: false, Verify Outgoing: false
 Internal RPC TLS: Verify Incoming: false, Verify Outgoing: false
```

Anything that can reach the API can read and write the KV store, register or
deregister catalog entries, and force other agents to leave. Permissive mode
does not close that — only phase 4 does. What phases 1-3 buy is the ability to
reach phase 4 without an outage.

## Phase 1 — enable ACLs in permissive mode — DONE

Lives in `argocd/apps/infra/consul/sea1/consul.yaml` as `server.extraConfig`.
`acl.enabled = true` with `default_policy = "allow"`, so nothing breaks yet.
`acl-values.draft.yaml` keeps the same block for reference.

This is a rolling restart of the server StatefulSet. Take one pod at a time and
confirm quorum between each:

```sh
kubectl -n consul exec sea1-server-0 -- consul operator raft list-peers
```

## Phase 2 — bootstrap, still permissive — DONE

`consul acl bootstrap` prints the SecretID exactly once and cannot be repeated.
Losing it before phase 4 means a `consul acl bootstrap-reset` against the raft
index — recoverable, but only with server filesystem access. It is stored twice:

- Vault, `secret/infra/consul-sea1/bootstrap-token` (`secret_id`, `accessor_id`)
- the `consul-bootstrap-acl-token` Secret in the `consul` namespace, which is
  what `global.acls.bootstrapToken` reads at phase 4

The Secret is deliberately **not** in git and not ArgoCD-managed; it is
annotated with its Vault source.

## Phase 3 — policies and tokens, still permissive — vmagent DONE, sea1-core NOT

```sh
consul acl policy create -name anonymous         -rules @policies/anonymous.hcl
consul acl policy create -name metrics-discovery -rules @policies/metrics-discovery.hcl

# sea1-core is the one agent the chart does not manage
# (NixOS, nix/machines/sea1-core/consul.nix)
consul acl policy create -name agent-sea1-core \
  -rules @<(sed 's/NODE_NAME/sea1-core/' policies/agent-node.hcl)
consul acl token create -description "agent sea1-core" -policy-name agent-sea1-core
```

Attach the anonymous policy to the built-in anonymous token:

```sh
consul acl token update -id 00000000-0000-0000-0000-000000000002 -policy-name anonymous
```

All three policies and both tokens exist, and `anonymous` is attached to the
built-in anonymous token (`00000000-0000-0000-0000-000000000002`). Tokens are in
Vault under `secret/infra/consul-sea1/token-*`.

Then wire the two consumers, both of which are repo changes:

- **vmagent** — DONE. `tokenRef` in
  `argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml`, fed by a
  `VaultStaticSecret` from `secret/app/consul-sea1/metrics-discovery`.
  Verified: vmagent's generated config carries the minted SecretID as
  `bearer_token`, and `consul acl token read -self` with it returns the
  `metrics-discovery` policy.
- **sea1-core** — NOT DONE, and deliberately so. It would go in
  `acl.tokens.agent`; the token is a secret so it does not belong in
  `consul.nix` directly — render it through `nix/modules/vault-agent.nix`,
  which sea1-core already runs. The token is minted and in Vault. It is not
  wired because sea1-core's agent cannot currently complete any RPC (the
  `bpf.masquerade` blocker at the top), so there is no way to observe it using
  the token. Wiring it now would defer that discovery to the phase 4 flip.

Because the default is still `allow`, both keep working with or without the
token. That is the entire point of the permissive window: it is the only chance
to confirm each consumer is *using* its token rather than coasting on the
permissive default.

## Phase 4 — flip to deny — NOT STARTED, needs sign-off

Do not begin until all three preconditions at the top of this file hold. This is
the lockout step: it is the only phase that can take `.consul` DNS down
fleet-wide, and it does so silently.

Swap the phase 1 block for the phase 4 block in `acl-values.draft.yaml`
(`global.acls.manageSystemACLs: true` plus `bootstrapToken`). **There is no
permissive setting of `manageSystemACLs` — turning it on writes
`default_policy = "deny"` into the server config. Enabling it *is* the flip.**

### What breaks if phase 3 is incomplete

Registered state is small, which keeps the blast radius narrow: the only
service in the catalog is `node_exporter`, and the only agent outside the chart
is `sea1-core`.

1. **`.consul` DNS, fleet-wide.** Highest blast radius. Consul DNS answers
   unauthenticated queries as the anonymous token, and every NixOS dnsmasq
   forwards `/consul/` and `/consul.generalprogramming.org/` to
   `127.0.0.1#8600` (`nix/modules/dns/default.nix`). CoreDNS in sea1 forwards
   `consul:53` to sea1-core, which forwards to its own agent. If the anonymous
   token loses `node_prefix`/`service_prefix` read, resolution stops — and not
   only in sea1.
2. **node_exporter scrape targets, silently.** vmagent's consul SD discovers an
   empty target list and stops scraping with no error and no series. Confirm
   targets after the flip; do not assume.
3. **sea1-core's anti-entropy.** Without its own token it cannot sync its node
   registration or checks. Symptom is `Coordinate update error` and its
   `node_exporter` registration quietly leaving the catalog — which is item 2
   again, by a different route.

Rollback is the phase 1 block and a rolling restart. Policies and tokens can
stay in place.

### Checked and NOT a problem

- **Gossip.** ACLs govern the API, not the serf pool. Enabling them will not
  affect membership — and will not secure it either. See `gossip-encryption.md`.
- **Consul sessions.** None exist (`/v1/session/list` is empty).
- **Patroni KV (`postgresql-common/16-sea1/`).** Looks abandoned rather than
  live: zero sessions, static `status`, `ModifyIndex` untouched for about half
  the cluster's history. Consistent with Postgres having moved to CNPG.
  **Confirm before trusting this** — if it were live, denying
  `key_prefix "postgresql-common/"` would break leader election. If it is dead
  it should be deleted; today it is world-writable.

## Source networks

Prefixes below are from NetBox (`ipam/prefixes`), not guessed.

| network | v4 | v6 | consul agents |
|---|---|---|---|
| sea1 internal | `10.3.2.0/23` | `2602:fa6d:10:ffff::/116` | 3 servers, sea1-core |
| sea1 k8s pods | `10.244.0.0/16` | `fd40:10:244::/56` | the server pods themselves |
| sea1 public (Cofractal) | `199.255.18.160/27` | — | none |
| fmt2 | `10.3.4.0/23`, `10.65.67.0/24`, `10.255.1.0/24` | `2a0d:1a43:{f,dab,c010,4242}::/48` | separate DC |
| Wobscale | `10.3.6.0/27`, `209.251.245.0/24` | `2620:fc:c000::/48` | none since `sea1-k8s-103-0` left |

The pod network has to be in any allow list even though it looks like it should
not: the server pods sit on it and reach each other by pod IP through the
headless service before they learn each other's advertised node address.

**On including fmt2.** It was asked for, and it is listed above, but it does not
belong in an allow list yet. fmt2 is a separate and *unfederated* consul DC —
`/v1/catalog/datacenters` on a sea1 server returns `["sea1"]` and nothing else,
so no fmt2 host has any reason to reach sea1's API or its serf pool. With gossip
encryption still off, opening serf to a WAN-reachable prefix lets anything in
that prefix join the pool as a trusted member. Revisit when either the DCs are
actually federated or `encrypt` is on. Cofractal is excluded for a different
reason: it is sea1's own *public* transit, not a separate internal site, and a
public /27 in a write allow list is strictly worse than leaving it out.

**Consul ACL tokens cannot be CIDR-bound.** There is no `token_bound_cidrs`
equivalent — a token is valid from anywhere it is presented. Network scoping
comes from two other places:

1. `http_config.allow_write_http_from`, which restricts the *write* HTTP
   endpoints by source CIDR. It is in the phase 1 block of
   `acl-values.draft.yaml`. It gates HTTP writes only — not RPC (`:8300`) and
   not gossip (`:8301`).
2. The Talos ingress firewall, for 8300/8301/8302. See
   `talos-ingress-firewall.draft.yaml`, and read its pre-flight note first:
   `ingress: block` is node-wide default-deny, not consul-scoped.

## Not covered here, but needed

ACLs authenticate the **API**. With `encrypt` unset, any host that can reach
`:8301` can still join the LAN pool and be trusted as a member. On a flat LAN
that is the bigger of the two holes. See `gossip-encryption.md`.

RPC/HTTP TLS (`verify_incoming`, `verify_outgoing`) is a larger lift and wants
the GP Root CA / Vault PKI behind it. Separate piece of work.

## Files

| file | purpose |
|---|---|
| `acl-values.draft.yaml` | the helm values, split into the phase 1 and phase 4 blocks |
| `talos-ingress-firewall.draft.yaml` | the network half; replaces the firewalld plan |
| `gossip-encryption.md` | the other half of securing the pool |
| `policies/anonymous.hcl` | keeps `.consul` DNS resolving under deny; withholds KV and all writes |
| `policies/agent-node.hcl` | per-agent token template; only sea1-core needs it now |
| `policies/metrics-discovery.hcl` | vmagent consul SD |
