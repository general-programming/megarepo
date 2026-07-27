# Retiring `dns_server` and `dhcp_server` from Salt

Assessment as of **2026-07-27**, while cutting FMT2 over to the NixOS DNS
servers. Short answer: **`dhcp_server` looks removable, `dns_server` does
not — not yet.**

## Where things stand

| Role | Salt state | NixOS replacement | Status |
| --- | --- | --- | --- |
| DHCPv4 | `dhcp_server` (isc-dhcp-server) | `nix/modules/kea` on fmt2-core, sea1-core, sea420-core | Cut over |
| DHCPv6 | `dhcp_server` (isc-dhcp-server6) | Kea `dhcp6` on sea1-core | Cut over (fmt2 v6 was never real — see `4fd66877`) |
| DNS | `dns_server` (dnsmasq) | `nix/modules/dns` on fmt2-core (10.65.67.5) | **Still live on 10.65.67.6** |

Both states are targeted by grain, in `salt/state/top.sls`:

```
'G@tags:dnsserver':  - dns_server
'G@tags:dhcpserver': - dhcp_server
```

Grains live on the minions, not in this repo, so the authoritative list of
tagged hosts has to come from the master:

```sh
salt -G 'tags:dnsserver'  grains.get id
salt -G 'tags:dhcpserver' grains.get id
```

## `dhcp_server` — removable, with one check

On `fmt2-core-0` (10.65.67.6), verified directly:

- `isc-dhcp-server` is **inactive**, and `/etc/dhcp/` has no v4 `dhcpd.conf` at
  all — only the stale `dhcpd6.conf` from 2020.
- Nothing is listening on port 67.

Kea on fmt2-core now serves all three fmt2 subnets (`79.110.170.0/24`,
`10.65.67.0/24`, `10.255.1.0/24`) at isc-dhcpd parity including the PXE chain,
per `4fd66877`. sea1 moved in `baa0f7a6`, sea420 in `766269f0`.

**Before deleting:** `4fd66877` names `fmt2-core-0/oob-backup` as the pair Kea
took over from. `oob-backup` was not reachable during this assessment — confirm
its `isc-dhcp-server` is also stopped and that no other minion carries
`tags:dhcpserver`.

Removal touches: `salt/state/dhcp_server/`, its `top.sls` entry, the
`salt/kv/dhcp_webhook` row in `docs/salt/secrets.md`, and the `dhcpserver` tag
description in `automation/ansible/README.md`.

## `dns_server` — not yet

`10.65.67.6` is still answering real queries. Query logging is currently
enabled there (`/etc/dnsmasq.d/98-query-logging.conf` →
`/var/log/dnsmasq-queries.log`), added specifically to find stragglers before
retirement. Over ~44h it logged ~106,500 queries:

| Client | Queries | What it is |
| --- | ---: | --- |
| `10.3.6.3` | 103,089 | `sea1-k8s-103-0` — pinned in `infrastructure/talos/sea1/talconfig.yaml` |
| `79.110.170.32` | 1,586 | Not in NetBox; general web traffic, looks like a VPN client |
| `10.255.1.1` | 347 | vlan1000 gateway |
| `10.255.1.24` | 319 | `fmt2-vpn-leaf-2` |
| `10.255.1.21` | 185 | `fmt2-vpn-spine-1` |
| `10.255.1.23` | 127 | `fmt2-vpn-leaf-1` |
| `10.255.1.22` | 90 | `fmt2-vpn-spine-2` |
| `10.255.1.102` | 80 | `ipmi.fmt2-hv-14-07-52-102` |
| `10.3.0.4` / `10.3.0.2` / `10.3.0.3` | 111 | `sea420-ipa-1`, `sea420-hv-2`, `sea420-core` |
| `10.3.2.13` | 6 | Not in NetBox |
| `10.65.67.13` | 2 | `router-portland` |

The FMT2 Kubernetes cluster is **not** in that list — it was already fully on
10.65.67.5, confirmed via CoreDNS's own `coredns_proxy_*{to=...}` metrics.

### Order of operations

1. Land the `talconfig.yaml` change pointing `sea1-k8s-103-0` at 10.65.67.5,
   and `talosctl apply` it. That is ~97% of the load. *(Config change is in;
   the apply is still outstanding.)*
2. Repoint the network gear (spines, leaves, IPMI, `router-portland`). These
   carry static resolver config and will never move via DHCP.
3. Chase `79.110.170.32` and `10.3.2.13` — neither is in NetBox.
4. Re-read the query log. When only `127.0.0.1` remains, retire.
5. Remove `salt/state/dns_server/`, its `top.sls` entry, **and** the
   `dnsserver` branch in `salt/pillar/firewalld/init.sls:72` that opens port 53.
6. Delete `98-query-logging.conf` and `/var/log/dnsmasq-queries.log` from the
   box before decommissioning — it is a full record of internal query traffic.

## Unrelated finding

35% of `sea1-k8s-103-0`'s queries (36,606) are PTR lookups for
`1.2.244.10.in-addr.arpa` — that is `10.244.2.1`, a flannel pod gateway — and
every one returns NXDOMAIN. Repointing the node moves that noise to
10.65.67.5 rather than removing it; worth fixing separately.
