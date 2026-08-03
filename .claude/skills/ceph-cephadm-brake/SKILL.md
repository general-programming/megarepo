---
name: ceph-cephadm-brake
description: Safely stop cephadm and Rook from acting on the sea1 ceph cluster — graduated, reversible brakes for the cephadm→Rook migration
---

# Stopping cephadm and Rook from acting

sea1's ceph cluster (fsid `dfbdcde6-2df8-4e91-bc80-d7305a598cf4`) is being
migrated from cephadm to Rook **in place, as one cluster**. During that, two
controllers can each act on it. Both need brakes that are graduated and
reversible.

Reach any hypervisor with `bin/vssh localadmin@<addr>`. `sea1-hv-2`'s DNS
resolves **wrongly** to hv-1 — use the literals: hv-0 `2602:fa6d:10:ffff::101`,
hv-1 `::102`, hv-2 `::103`.

## cephadm brakes, weakest to strongest

**1. Pause the orchestrator** — the default brake. cephadm stops acting;
every daemon keeps running.

```bash
ceph orch pause      # verify: ceph orch status -> Paused: Yes
ceph orch resume
```

Verified 2026-08-03: through a full pause/resume cycle, mon quorum, 21 OSDs,
mgr and MDS all stayed up and the cluster stayed `HEALTH_OK`.

**Consequence while paused: `ceph orch <anything>` no longer works**, including
`ceph orch daemon stop`. Per-daemon control moves to systemd on the host (below).
Expect a `CEPHADM_PAUSED` health warning — that is the brake working, not a fault.

**2. Drop the orchestrator backend** — `ceph orch` stops resolving at all.

```bash
ceph orch set backend ""          # restore: ceph orch set backend cephadm
```

**3. Disable the module** — strongest, and what to use once Rook is authoritative.

```bash
ceph mgr module disable cephadm   # restore: ceph mgr module enable cephadm
```

## Per-daemon control (works while paused)

Units are `ceph-<fsid>@<daemon>.service`, grouped under `ceph-<fsid>.target`:

```bash
systemctl list-units 'ceph-*' --no-legend            # what runs here
systemctl stop ceph-dfbdcde6-2df8-4e91-bc80-d7305a598cf4@osd.0.service
systemctl stop ceph-dfbdcde6-2df8-4e91-bc80-d7305a598cf4.target   # whole host
```

Set `ceph osd set noout` (or `ceph osd add-noout <host>`) **before** stopping
OSDs, or ceph marks them out and starts rebalancing.

## Already applied to this cluster

- **The OSD service spec is gone.** `ceph orch rm osd.iops_optimized --force`
  (2026-08-03). It had `host_pattern: '*'` + `model: SSDPE2KX020T8`, so it would
  auto-claim and re-encrypt a rebuilt host's disks and race Rook. The service now
  reads `<unmanaged>` with all 21 OSDs still up. Backup: Vault
  `secret/rook-sea1/cephadm-backup-20260803`, field `orch-ls-osd`.
- **`osd_crush_update_on_start = false`.** Stops an OSD relocating itself into a
  new CRUSH bucket when the host's name changes. Revert to `true` after the
  migration or genuinely new OSDs will not auto-place.

## Rook brakes — different, and needed for the same window

Rook's mon health checker **removes mons that are not its own**
(`health.go:250`), one per ~45s. `healthCheck.daemonHealth.mon.disabled` is dead
config — Rook never reads it. Do not rely on it.

**1. Skip-reconcile label** — the precise brake. `checkHealth` returns before any
removal logic if any Deployment labelled `app=rook-ceph-mon` carries it
(`health.go:169-176`), and `checkHealth` is the *only* path to
`removeMon`/`failMon`. A `replicas: 0` dummy Deployment with labels
`app=rook-ceph-mon`, `mon=killswitch`, `ceph.rook.io/do-not-reconcile: ""` is
enough, because the lookup lists Deployments, not Pods.

**2. Scale the operator to 0** — the health checker is a goroutine in the
operator process (`monitoring.go:85-104`), so this stops it, along with
everything else Rook does.

```bash
kubectl -n ceph scale deploy rook-ceph-operator --replicas=0
```

Use it *before* entering a risky state, never as a panic button mid-cycle —
`removeMon` is not transactional and a SIGKILL can land halfway through.

**3. `healthCheck.daemonHealth.mon.timeout: 0s`** — this one *is* read
(`health.go:70-79`) and disables mon failover (`health.go:286-289`). It does
**not** stop the removal at line 250; only `mon.externalMonIDs` does that.

## Order of operations for any risky mon change

1. `kubectl -n ceph scale deploy rook-ceph-operator --replicas=0`
2. Verify `spec.mon.externalMonIDs` lists every cephadm mon, and that
   `rook-ceph-mon-endpoints` has a matching `externalMons` key
3. Apply the skip-reconcile Deployment
4. Make the change
5. Remove the skip-reconcile Deployment, scale the operator back to 1, and watch
   `ceph quorum_status` plus the operator log for `"not in source of truth"`
