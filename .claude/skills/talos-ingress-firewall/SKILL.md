---
name: talos-ingress-firewall
description: Filter the sea1 Talos nodes' host services with the Talos ingress firewall — what Talos generates for you, what the rules do and do not cover, and the rollout that avoids locking yourself out. Use when adding or changing a NetworkRuleConfig, when a node service is unexpectedly reachable or unreachable, or when a new hostNetwork workload appears on these nodes.
---

# Talos ingress firewall (sea1)

The sea1 nodes carry globally routable GUAs (`2602:fa6d:10:ffff::110/111/112`) and
nothing filters upstream. Without this firewall every host service is on the
public internet. Config lives in `infrastructure/talos/sea1/talconfig.yaml` under
the cluster-wide `patches:` list.

## What Talos generates that you must not duplicate

`ingress: block` produces more than your rules. Verified in the live chain:

```
policy: drop
  - matchIIfName: [lo, siderolink, kubespan]
  - matchConntrackState: [established, related]   <- all node egress works
  - matchConntrackState: [invalid]                <- dropped
  - matchSourceAddress: [10.244.0.0/16, fd40:10:244::/56,
                         10.96.0.0/12, d40:10:96::/108]
```

So **node egress, pod→node, and pod→pod need no rules**. Talos auto-allows the
pod and service subnets for native-routing CNIs. Write rules only for sources
outside the cluster.

## What it does and does not cover

- **Covers** anything terminating in the host netns: apid, trustd, etcd,
  apiserver, kubelet, and every `hostNetwork` pod. Rook runs ceph entirely
  hostNetwork, which is why mon/osd/mds/mgr bind the node GUAs directly.
- **Covers NodePort/hostPort too.** The chain sits at priority `-140`, ahead of
  the DNAT hook at `-110`, so Cilium's BPF does not bypass it despite
  `kube-proxy-replacement: true`. Older writeups say otherwise; that was fixed
  before 1.13.
- **Does not cover** bridged KubeVirt guests — their traffic never enters the
  host input chain. Firewalling a node does not touch the VMs on it.
- **Does not cover** the pod network. Use CNI network policy there. A
  `NetworkPolicy` on a `hostNetwork` pod does nothing at all.

## Traps

- **ICMP is rate-limited to 5/s and you cannot opt out.** Block mode always adds
  `meta l4proto ipv6-icmp limit rate 5/second accept`. That budget covers NDP
  *and* PMTUD. On MTU 9000 nodes this is the first thing to suspect when v6
  neighbours flap or large flows stall.
- **`Apply was skipped: no changes detected`** after a `--mode=try` apply is
  expected — the try config is already live and identical. It persists past the
  window anyway. Verify with `talosctl get nftableschains`, not by trusting the
  message.
- **A "blocked" port may just have no listener.** `:9283` is closed on any node
  not currently running ceph-mgr. Check `talosctl netstat --listening` before
  concluding the firewall did it.
- **Ceph mons bind v6 only.** `10.3.2.x:6789` refuses regardless of firewall
  state.
- **`80/443` is load-bearing only on the node announcing the traefik VIP**
  (`2602:fa6d:10:ffff::e00`, Cilium L2). Node IPs never listen on 443. Firewall
  that node last, and use the VIP as the canary.

## Rollout

Never all three at once. Order: a VM-free node, then a VM node, then the
VIP-announcing node.

```bash
export TALOSCONFIG=infrastructure/talos/sea1/talosconfig
talosctl -n 10.3.2.10 patch machineconfig --mode=try --timeout=5m -p @fw.yaml
```

`--mode=try` reverts itself, so a bad config self-heals. It does not protect
against a valid-but-wrong rule — have BMC before starting.

Verify, then repeat with `--mode=no-reboot`, then land it in `talconfig.yaml`
and confirm the repo renders what is live:

```bash
talhelper genconfig -c talconfig.yaml -s talsecret.sops.yaml -o /tmp/gen
grep -c 'kind: NetworkRuleConfig' /tmp/gen/sea1-sea1-k8s-0.yaml   # expect 5
```

## Verification set

Run all of these; the first three are the ones that actually catch mistakes.

```bash
# 1. blocked from outside (run from off-network, NOT over tailscale)
python3 -c "import socket;s=socket.socket(socket.AF_INET6);s.settimeout(4);\
print(s.connect_ex(('2602:fa6d:10:ffff::110',8500)))"   # nonzero == blocked

# 2. still reachable internally
nc -z 10.3.2.10 50000 && nc -z 10.3.2.10 6443

# 3. public web unaffected (VIP node only)
curl -sk -o /dev/null -w '%{http_code}\n' https://auth.generalprogramming.org/

talosctl -n 10.3.2.10 etcd members                  # 3 members
kubectl -n ceph get cephcluster                     # HEALTH_OK
kubectl get vmi -A                                  # all Running
kubectl top node sea1-k8s-0                         # apiserver -> kubelet:10250
kubectl logs -n <ns> <pod-on-node> --tail=1         # apiserver -> kubelet
# vmagent target count must not move
```

A dropped vmagent target count is the most sensitive early signal — it catches
broken scrape paths that health checks miss.
