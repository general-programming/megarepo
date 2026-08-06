# DHCPv6 instability on the sea1 k8s nodes

**Resolved for consul, twice over.** The three nodes now hold static v6 in
`infrastructure/talos/sea1/talconfig.yaml` (`::110`, `::111`, `::112`), which
takes DHCPv6 out of the path; and consul no longer advertises v6 at all — the
sea1 gossip pool moved to IPv4 on `10.3.2.0/23` when the servers moved into
k8s. Kept for the Kea findings at the bottom, which are still live and are not
consul-specific.

The original problem, for context: advertising a DHCPv6 address that moves
every reboot would have replaced consul's gossip flap with an address-churn
flap.

## Symptom

Two of the three sea1 k8s node v6 addresses changed *during a single working
session* (~1h):

| node | earlier | now | NetBox reservation |
|---|---|---|---|
| sea1-k8s-0 | `::407` | `::40f` | **`::110`** |
| sea1-k8s-1 | `::111` | `::111` | `::111` ✓ |
| sea1-k8s-2 | `::405` | `::40c` | **`::112`** |

Only `sea1-k8s-1` is actually holding its reserved address. The others sit on
dynamic pool addresses (`::200-::fff`) and move.

## Cause: the DUID changes every boot

`/var/lib/kea/dhcp6.leases` holds three leases for one MAC
(`bc:24:11:6a:62:b3` = sea1-k8s-0), each under a **different DUID**:

```
::40f  duid=00:01:00:01:32:02:b4:4d:bc:24:11:6a:62:b3  state=0  (active)
::407  duid=00:01:00:01:32:02:98:2d:bc:24:11:6a:62:b3  state=2
::110  duid=00:01:00:01:32:02:8a:1d:bc:24:11:6a:62:b3  state=2  valid_lifetime=0
```

These are DUID-LLT: `0001` type, `0001` hwtype, then a **timestamp**, then the
MAC. The timestamp differs per boot (`32:02:8a:1d` / `98:2d` / `b4:4d`), so
Talos is minting a fresh DUID every time rather than persisting one.

The reservation itself is fine — correct MAC, single address, file is loaded,
and `host-reservation-identifiers` is `["hw-address","duid"]` so hw-address is
tried first:

```json
{ "hw-address": "BC:24:11:6A:62:B3",
  "ip-addresses": ["2602:fa6d:10:ffff::110"],
  "hostname": "sea1-k8s-0" }
```

Note the third lease: the node **did** hold `::110` once. The most consistent
explanation is that `::110` remains in the lease DB bound to the *old* DUID, so
when the node returns as a "new" client Kea finds its reserved address already
leased to someone else and falls back to the pool. Each reboot compounds it.

## Recommended fix: static v6 in talconfig

Rather than chase DUID persistence, give the four k8s nodes static v6 in
`infrastructure/talos/sea1/talconfig.yaml`, matching how the HVs and sea1-core
are already addressed. That removes DHCPv6 from the path entirely, makes the
consul advertise address stable, and does not depend on the renderer below.
Use the addresses NetBox already reserves (`::110`, `::111`, `::112`);
`sea1-k8s-103-0` is SLAAC EUI-64 and already stable — leave it.

Alternative if DHCPv6 is preferred: persist the DUID on the Talos side and
clear the stale conflicting leases for the affected MACs.

## Two other things found in passing

1. **The hourly NetBox → Kea renderer has been dead for 8 days.**
   `/var/lib/kea/netbox/hosts6.json` is dated Jul 25 20:01, `hosts4.json`
   Jul 25 19:36, against a config comment promising hourly renders. Not the
   cause of the churn above (the stale file contains the correct reservation),
   but anything added to NetBox since Jul 25 is not in DHCP.

2. **sea1's DHCP server carries other sites' reservations.** `hosts6.json` on
   sea1-core includes fmt2/lasagna entries — `fmt2-netboot-external`
   (`2a0d:1a43:dddd:cccc::/…`), `fmt2-vpn-spine-1-external`
   (`2a0d:1a43:8008:420::1`), `fmt2-moderateinfra-net`. The renderer is not
   filtering by site.

3. **The Kea v6 pool overlaps static infra space.** Pool is
   `2602:fa6d:10:ffff::200 - ::fff`, which swallows the `::f00/116` range where
   static infrastructure lives — `sea1-core` is `::f00`, and NetBox records
   `::f0b/::f0c/::f0d` against the k8s nodes. Kea could hand out an address
   already in static use. The pool should stop below `::f00`.
