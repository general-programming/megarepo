# sea1 Consul ACLs — draft

**Status: draft. Nothing here has been applied.**

Written 2026-08-02 alongside the decision to move the sea1 k8s consul clients to
`hostNetwork: true` so they can gossip natively over IPv6.

## Why

Today the whole sea1 DC runs wide open. Straight from a live agent's banner:

```
Gossip Encryption: false
      ACL Enabled: false
ACL Default Policy: allow
        HTTPS TLS: Verify Incoming: false, Verify Outgoing: false
 Internal RPC TLS: Verify Incoming: false, Verify Outgoing: false
```

The agent HTTP API binds `0.0.0.0:8500` and is already published on every k8s
node via `hostPort`, so anyone who can reach a node on the LAN can today read
and write the KV store, register or deregister catalog entries, and force other
agents to leave. `hostNetwork` does not create that exposure, but it does widen
it (the agent gains the host's full network namespace, including the `:8600`
DNS listener), and it is a good forcing function to close it.

## Order of operations

Do the ACL work **before** flipping `hostNetwork`, not after. The hostNetwork
change is a one-line helm value and is easy to land once the DC is locked down;
doing it first just widens the open window.

## Phase 1 — enable ACLs in permissive mode

On each of the three HV servers (`sea1-hv-0/1/2`), add to `/etc/consul.d/`:

```hcl
acl {
  enabled                  = true
  default_policy           = "allow"   # permissive: nothing breaks yet
  enable_token_persistence = true
  down_policy              = "extend-cache"
}
```

Restart the servers one at a time, confirming `consul operator raft list-peers`
is healthy between each. Then bootstrap **once** from any server:

```sh
consul acl bootstrap        # prints the SecretID -- store it in Vault immediately
```

Note `salt/state/consul/` cannot deploy this today — `salt/state/top.sls`
targets `G@tags:consul` and the HVs have an empty `tags` grain, so the state
has never matched them (their `/etc/consul.d/00-base.hcl` is dated 2024-02-03).
Either fix the grain first or place the file by hand and reconcile later.

## Phase 2 — create policies and tokens, still permissive

```sh
consul acl policy create -name anonymous          -rules @policies/anonymous.hcl
consul acl policy create -name metrics-discovery  -rules @policies/metrics-discovery.hcl

# one per agent; substitute NODE_NAME in agent-node.hcl each time
consul acl policy create -name agent-sea1-hv-0 -rules @<(sed 's/NODE_NAME/sea1-hv-0/' policies/agent-node.hcl)
consul acl token  create -description "agent sea1-hv-0" -policy-name agent-sea1-hv-0
```

Set each agent's token (`consul acl set-agent-token agent <SecretID>`, or
`acl.tokens.agent` in config) and attach the anonymous policy to the built-in
anonymous token:

```sh
consul acl token update -id 00000000-0000-0000-0000-000000000002 -policy-name anonymous
```

Because the default policy is still `allow`, everything keeps working whether or
not a token is present. That is the window to verify each agent is actually
using its token rather than coasting on the permissive default.

## Phase 3 — flip to deny

Change `default_policy = "deny"` on the servers, restart one at a time.

### Expected breakages

Inventoried against the live DC on 2026-08-02. Registered state is small: the
only service in the catalog is `node_exporter` (on the three HVs), and there are
11 health checks — 8 × `serfHealth` plus 3 × `service:node_exporter`. The k8s
clients and sea1-core register no services at all. That keeps the blast radius
narrow.

**Will break unless Phase 2 is complete:**

1. **`.consul` DNS, fleet-wide.** Highest blast radius — see below.
2. **node_exporter scrape targets.** Silent — see below.
3. **Agent anti-entropy.** Any agent without its own token cannot sync its node
   registration or checks once the default is `deny`. Symptom is
   `Coordinate update error` plus the node's services quietly leaving the
   catalog. This is why every agent gets a token in Phase 2 *while still
   permissive* — the permissive window exists precisely so you can confirm each
   agent is using its token rather than coasting on `allow`.
4. **vmagent needs a token, which is an argocd change too.** Granting the
   `metrics-discovery` policy is only half of it; the scrape config in
   `argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml` has no token today
   and needs one wired in (Secret + token ref). Don't flip to deny before that
   lands or item 2 fires.

**Checked and NOT a problem:**

- **Gossip.** ACLs govern the API, not the serf pool. Enabling them will not
  affect membership, and equally will not fix the v4/v6 flapping.
- **`consul.service.consul` in the k8s clients' `retry_join`.** It resolves out
  through dnsmasq → consul DNS → anonymous token, so it *does* depend on the
  anonymous policy — but the chart also lists the three v6 server literals as
  fallback, so a join still succeeds even if that name fails to resolve.
- **Consul sessions.** None exist (`/v1/session/list` is empty), so no
  session-based locking to preserve.
- **Patroni KV (`postgresql-common/16-sea1/`).** Looks abandoned rather than
  live: zero sessions, `status` static across samples, and `ModifyIndex`
  11126544 against a current commit index of ~21758656 — untouched for about
  half the cluster's history. Consistent with Postgres having moved to CNPG.
  **Confirm before trusting this**, because if it *were* live, denying
  `key_prefix "postgresql-common/"` would break leader election. If it is
  genuinely dead it should be deleted — today it is world-writable.

**Watch out for, if `allow_write_http_from` is adopted:**

- In-cluster callers reach the agent from **pod** addresses
  (`10.244.0.0/16`, `fd40:10:244::/56`), not node addresses. Any pod that needs
  to *write* to the agent API must have the pod CIDR in the list, or the write
  is refused before ACLs are even consulted. Nothing writes from a pod today
  (vmagent only reads), but it is an easy trap later. Note `hostNetwork` changes
  this — those agents' own traffic becomes node-sourced.

### The two silent ones, in detail

1. **`.consul` DNS, fleet-wide.** Consul DNS on `:8600` answers as the anonymous
   token, and every NixOS dnsmasq forwards `/consul/` and
   `/consul.generalprogramming.org/` to `127.0.0.1#8600`
   (`nix/modules/dns/default.nix`). If the anonymous token lacks
   `node_prefix`/`service_prefix` read, `.consul` resolution stops — and not
   only in sea1. This is the highest-blast-radius item on the page.
2. **node_exporter metrics.** The vmagent consul SD
   (`argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml`) will discover an
   empty target list and stop scraping *silently* — no error, just no series.
   Confirm targets after the flip, don't assume.

Rollback is `default_policy = "allow"` and a restart; tokens and policies can
stay in place.

## Source networks

Consul members are not all on the sea1 LAN, so anything network-scoped has to
name these explicitly or `sea1-k8s-103-0` (wobscale) drops out.

| network | v6 | v4 | consul members today |
|---|---|---|---|
| sea1 (internal) | `2602:fa6d:10:ffff::/116` | `10.3.2.0/23` | hv-0/1/2, sea1-core, k8s-0/1/2 |
| sea1 (public, Cofractal transit) | — | `199.255.18.160/27` | **none** |
| Wobscale | `2620:fc:c000::/48` | `209.251.245.0/24`, `10.3.6.0/27` | sea1-k8s-103-0 |
| Lasagna = **fmt2** | `2a0d:1a43::/32` (genprog pool `2a0d:1a43:8008::/48`) | — | **separate DC** |

Only **Wobscale** belongs in the sea1 allow list. The other two do not, for
different reasons:

- **Cofractal is not a separate site** — it is sea1's own public v4 transit. The
  /27 holds `sea1-vpn-leaf-1`, `sea1-vpn-leaf-2` and `10gbe`, none of which
  appear in `consul members`. Putting a public /27 in a *write* allow-list would
  permit consul writes sourced from sea1's internet-facing addresses, which is
  strictly worse than leaving it out.
- **Lasagna is fmt2**, a separate and *unfederated* consul cluster —
  `/v1/catalog/datacenters` on a sea1 server returns `["sea1"]` and nothing
  else. fmt2 agents are not in sea1's pool and have no reason to reach its API.
  Revisit only if the two DCs are ever WAN-federated.

**Consul ACL tokens cannot be CIDR-bound.** There is no `token_bound_cidrs`
equivalent here the way there is in Vault — an ACL token is valid from anywhere
it is presented. Network scoping has to come from two other places:

1. `http_config.allow_write_http_from` in the agent config, which restricts the
   *write* HTTP endpoints by CIDR. This is the closest thing Consul has to a
   network ACL and it belongs in the same config change as the `acl {}` block:

   ```hcl
   http_config {
     allow_write_http_from = [
       "127.0.0.1/32", "::1/128",
       "10.3.2.0/23", "2602:fa6d:10:ffff::/116",   # sea1 internal
       "10.3.6.0/27", "2620:fc:c000::/48",         # wobscale (sea1-k8s-103-0)

       # Deliberately NOT included -- see the table above:
       #   "199.255.18.160/27"    sea1 public transit; no agents, and a public
       #                          /27 in a write allow-list is a real exposure
       #   "2a0d:1a43:8008::/48"  fmt2/lasagna; separate unfederated DC
     ]
   }
   ```

   Note this gates HTTP writes only — it does not touch the RPC (`:8300`) or
   gossip (`:8301`) ports.

2. firewalld, for `:8300`/`:8301`/`:8500`. Salt manages this via the
   `managed_firewall` tag and `salt/pillar/firewalld/init.sls` — but the sea1
   HVs are **not** tagged `managed_firewall` (only two hosts in the fleet are),
   so today there is no managed firewall on them at all. Tagging them pulls in
   the `firewalld` state on a production hypervisor; treat that as its own
   change with its own window, not a rider on the ACL work.

## Not covered here, but needed

ACLs authenticate the **API**. They do nothing for the **gossip pool** — with
`encrypt` unset, any host that can reach `:8301` can still join the LAN pool and
be trusted as a member. For a host-networked agent on a flat LAN that is the
bigger hole of the two.

- `encrypt = "<32-byte base64>"` on the HVs and sea1-core, plus
  `global.gossipEncryption.secretName`/`secretKey` on the helm side. Rolling it
  out needs `encrypt_verify_incoming = false` first so mixed-state agents can
  still talk, then a second pass to turn verification on.
- RPC/HTTP TLS (`verify_incoming`, `verify_outgoing`) is a larger lift and wants
  the GP Root CA / Vault PKI behind it. Separate piece of work.

## Files

| file | purpose |
|---|---|
| `policies/anonymous.hcl` | keeps `.consul` DNS resolving under deny; withholds KV and all writes |
| `policies/agent-node.hcl` | per-agent token template, `NODE_NAME` substituted per host |
| `policies/k8s-client.hcl` | fallback only — prefer chart-managed tokens |
| `policies/metrics-discovery.hcl` | vmagent consul SD |
