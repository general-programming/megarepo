---
name: consul-sea1
description: >-
  Operate sea1 Consul, diagnose LAN-to-hostPort RPC and DNS failures, maintain
  the BPF masquerade prerequisites, and deliver scoped agent tokens securely.
  Use for Consul membership, raft, service discovery, ACL or topology changes.
---

# sea1 consul

## Topology (post Proxmox→Talos)

Servers were the three Proxmox hypervisors at `2602:fa6d:10:ffff::101/102/103`.
Those hosts are bare-metal Talos now and **cannot run a packaged consul**, so
the DC ran with zero servers. The servers are a chart-managed StatefulSet on the
same three machines: `argocd/apps/infra/consul/sea1/consul.yaml`,
`server.enabled: true`, `replicas: 3`, `bootstrapExpect: 3`,
`exposeGossipAndRPCPorts: true`, `storageClass: ceph-rbd-xfs`.

`client.enabled: false` — every node runs a server, and a server is a superset
of an agent. `consul-http` and `sea1-dns` select `app: consul, hasDNS: "true"`,
which the server pods carry, so both Services keep resolving.

The pool is **IPv4 on 10.3.2.0/23**. Nodes are `10.3.2.10/11/12`, sea1-core is
`10.3.2.6`. Non-k8s agents join those three node addresses
(`nix/machines/sea1-core/consul.nix`, `salt/pillar/consul/init.sls`).

fmt2 is unchanged and unrelated: servers still on its hypervisors,
`10.65.67.100-104`, and it is a separate unfederated DC
(`consul catalog datacenters` on a sea1 server returns only `sea1`).

## LAN-to-hostPort RPC requires the BPF path

The iptables masquerade failure looks like this on the agent:

```
[ERROR] agent: yamux: keepalive failed: i/o deadline reached
[ERROR] agent.client: RPC failed to server: method=Catalog.ListServices error="rpc error making call: EOF"
[WARN]  agent: (LAN) couldn't join: Failed to join 10.3.2.10:8301: i/o timeout
```

`consul members` still looks perfect on both sides — that converges over UDP,
and the servers can open TCP *to* sea1-core, which works. Only LAN→hostPort TCP
is broken, so this misreads as healthy.

Cause: Cilium runs `enable-ipv4-masquerade` in **iptables** mode
(`enable-bpf-masquerade` is unset). A hostPort reply leaves the pod still
carrying its pod IP, so `CILIUM_POST_nat` matches it:

```
-A CILIUM_POST_nat -s 10.244.2.0/24 -m set --match-set cilium_node_set_v4 dst -j ACCEPT
-A CILIUM_POST_nat -s 10.244.2.0/24 ! -d 10.244.0.0/16 ! -o cilium_+ -j MASQUERADE
```

and SNATs it to the node IP with a **fresh ephemeral source port**, destroying
the hostPort tuple. Visible in `cilium-dbg monitor --type trace`: the SYN-ACK
leaves correctly as `10.3.2.10:8301 -> 10.3.2.6:x`, then every later packet
leaves as `10.3.2.10:53204 -> 10.3.2.6:x` and the client ignores it.

The first rule is why nobody noticed: cluster **nodes** are exempted, so
node→hostPort and pod→hostPort both work. Only a LAN peer that is not a k8s
node — sea1-core, and any VM or salt host — hits it.

sea1 now uses `bpf.masquerade: true` and `bpf.hostLegacyRouting: false` in
`argocd/apps/infra/cilium/sea1/application.yaml`. Talos must disable pod
hostDNS forwarding **and both bridge-netfilter sysctls**. Otherwise the BPF
path breaks direct DNS and subnet routing even with legacy host routing.
See `docs/bpf-masquerade-sea1.md` for the canary and runtime checks. Change
this datapath deliberately, one agent at a time, with guest service probes.

Do not chase this as an MTU problem. Everything is 9000 end to end and
`ping -M do -s 8972` succeeds in both directions.

## Stale hostPort after a pod restart

Cilium sometimes fails to reprogram a hostPort when a server pod is replaced —
the entry keeps pointing at the dead pod IP, and that server is isolated
(`Voter false`, trailing the leader, `UDP probes failed` against every peer).
Check before assuming a consul fault:

```sh
kubectl -n kube-system exec <cilium-pod-on-that-node> -c cilium-agent -- \
  cilium-dbg service list | grep '0.0.0.0:830'
kubectl -n consul get pods -o wide     # backend IP must match the live pod
```

`kubectl -n consul delete pod sea1-server-N` reprograms it. Autopilot then
re-promotes the server to voter after its stabilization window — wait, do not
assume the delete failed.

## The chart traps

- **There is no `server.hostNetwork`.** The consul-k8s chart (2.0.x) exposes
  `client.hostNetwork` and `meshGateway.hostNetwork` only. So hostPort is the
  only way to reach the servers from the LAN, which is what makes the SNAT trap
  unavoidable rather than a design choice.
- **That is also why the pool is v4.** The v6 advertise trick the clients used —
  `ADVERTISE_IP: '{{ GetPublicInterfaces | include "type" "IPv6" | ... }}'` —
  only works inside the node netns. A server pod sees only the ULA
  `fd40:10:244::/56`, which go-sockaddr classifies as private, so the template
  resolves to nothing and **the agent refuses to start**. Servers take the chart
  default `ADVERTISE_IP: status.hostIP`, which is v4 here.
- **hostNetwork clients and hostPort servers cannot coexist.** The client
  DaemonSet binds `:8301` on the host; the server hostPort wants the same port
  on the same nodes. Enabling servers without disabling clients breaks both.
- Chart default server resources are `100m`/`200Mi` with limits == requests.
  That starves the agent exactly like it starved the clients (throttled >99% of
  CPU periods, dies on "timeout starting DNS servers"). Base sets 500m/512Mi.
- A hostPort inside the NodePort range (30000-32767) is silently refused —
  `node-port-bind-protection`. It never appears in `cilium-dbg service list`.
  Relevant when building a synthetic hostPort test; pick a port outside it *and*
  inside the Talos firewall allow list, or you will debug the wrong thing.

## Catalog contents are not automatic

`node_exporter` was in the sea1 catalog only because salt registered it on the
three hypervisors. They are gone, so **a healthy server cluster still yields an
empty `/v1/health/service/node_exporter`** until something registers it.
sea1-core does that via a `services` block in its `consul.nix`; base.nix already
runs the exporter on `:9100` and opens the port. The agent and config-file
registration tokens come from Vault Agent through systemd credentials, with `node_exporter` write permission in the agent policy.
Both tokens are needed: `acl.tokens.agent` alone does not authorize file-based
service registrations. The catalog should contain a passing `node_exporter`.

vmagent's consul SD (`argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml`,
the `VMScrapeConfig` misleadingly named `mongodb`) discovers an empty target
list silently — no error, just no series. Check the target count, not the logs.

sea1-core's NixOS firewall does not open `8301` by default, so servers cannot
probe it inbound and it flaps alive/failed. Its `consul.nix` opens 8301 tcp+udp
and nothing else — no 8300 (it is a client), no 8302 (sea1 is not federated).

## GitOps is a closed loop — you cannot hand-patch this

`consul-helm` is generated by the `consul` Application, which is generated by
the `infra` **ApplicationSet** from `main` on GitHub. Patching either
Application, or removing `spec.syncPolicy.automated` from the parent, is
reverted within seconds — the AppSet rewrites the child. There is no pause
short of disabling the ApplicationSet, which manages every infra app.

**A sea1 consul change lands only by committing and pushing to `main`.** Plan
for that; do not budget time for an imperative fix. A reverted imperative
attempt still leaves its PVCs behind — `data-consul-sea1-server-{0,1,2}` bind
and survive, so the next real sync reuses them.

Do not `kubectl apply -n argocd -f <kustomize output>` either: the sea1 overlay
includes `base/service.yaml`, so that creates a stray `consul-http` Service in
the `argocd` namespace.

## ACLs

**Enabled, permissive default.** `acl.enabled = true`, `default_policy = "allow"`,
set via `server.extraConfig` in the sea1 overlay. Explicit anonymous read rules
still deny node/service writes, so the agent token is required already.
Gossip encryption is still off; ACLs govern the API, not the serf pool.

sea1-core renders `secret/infra/consul-sea1/token-agent-sea1-core` into
`/run/vault-agent/consul-acl.json` (0600), then `LoadCredential` exposes it only
to the Consul service. `ConditionPathExists` skips startup until the first
render; the template callback starts/restarts Consul. Without that gate, rapid
missing-credential failures can exhaust systemd's start limit before rendering.

Bootstrapped. Tokens live in Vault under `secret/infra/consul-sea1/`, and the
`consul-bootstrap-acl-token` Secret in the `consul` namespace is staged for
phase 4. Policies `anonymous`, `metrics-discovery` and `agent-sea1-core` exist;
`anonymous` is attached to the built-in anonymous token.

`infrastructure/consul/acl/README.md` has the phase list and what breaks.
**`global.acls.manageSystemACLs` has no permissive setting — turning it on
writes `default_policy = "deny"`, so enabling it *is* the flip.** Do not reach
for it to "manage tokens".

## Verifying after a change

```sh
kubectl -n consul get pods,pvc
kubectl -n consul exec sea1-server-0 -c consul -- consul operator raft list-peers  # 3 voters
kubectl -n consul exec sea1-server-0 -c consul -- consul members                   # servers + sea1-core
kubectl -n consul exec sea1-server-0 -c consul -- \
  wget -qO- 'http://127.0.0.1:8500/v1/health/service/node_exporter?dc=sea1'
vssh 10.3.2.6 'consul members; consul catalog services'   # catalog needs working RPC
```

`consul members` agreeing on both sides proves only UDP gossip. **Prove RPC
separately** — `consul catalog services` from sea1-core is the cheap check, and
it is the one that catches the SNAT trap.

vmagent's active target count is the most sensitive signal that something
stopped being scraped; a consul SD regression shows up there and nowhere else.

Stale members from the migration prune with
`vssh 10.3.2.6 'consul force-leave -prune sea1-hv-0'`, but a sea1-core consul
restart under the new `bind_addr` already discards that serf state, so the
fossils usually clear themselves.
