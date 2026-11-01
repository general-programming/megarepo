---
name: talos-rolling-upgrades
description: Upgrade the fmt2 and sea1 Talos clusters while preserving live configuration, Ceph availability, and KubeVirt guest continuity.
---

# Rolling Talos and Kubernetes upgrades

Commit version targets in `infrastructure/talos/<cluster>/talconfig.yaml`.
Check current upstream compatibility for Talos, Kubernetes, Cilium and KubeVirt;
latest stable versions need not have overlapping tested support.

## Reachability and backups

Direct Talos port 50000 may be unreachable from the desktop even when the
Kubernetes API works. A hostNetwork Cilium pod provides a management path:

```sh
kubectl --kubeconfig "$kc" -n kube-system port-forward pod/<cilium-on-safe-control-plane> 15000:50000
talosctl --talosconfig "$tc" -e 127.0.0.1:15000 -n <node-ip> version
```

The endpoint can proxy to other nodes. Switch its anchor before rebooting that
host **or restarting its kubelet**: exec/port-forward streams use the kubelet
even when the hostNetwork container stays running. Tailscale connector eviction
can also reset these connections; reconnect and inspect state before retrying.

Save an etcd snapshot, live machine configs, extensions, nodes, PDBs, PVs and VMI
identities outside git. MachineConfig `spec` is a secret-bearing YAML **string**,
not a mapping. Select resource ID `v1alpha1`; newer clients may also return
`persistent`. Never print the raw configuration or a dry-run's entire output.
Use private files and inspect only the needed fields.

## OS roll

Preserve the committed factory schematic and secure-boot installer variant.
sea1's live schematic may differ from the install schematic because the boot
ISO added `talos.config=metal-iso`; compare customizations before replacing it.
Pre-pull the chosen installer to every node.

Patch only `machine.install.image` with `--mode=no-reboot`, dry-run first, and
compare parsed live configuration against the backup allowing only that field.
Do not regenerate and apply full configs with an older talhelper: it has dropped
network fields before. Upgrade one node at a time with a matching talosctl.

- Cordon the node. Move CNPG primaries to Ready replicas on other hosts with
  `kubectl cnpg promote`, then wait for the cluster's full replica count.
- On sea1, explicitly live-migrate all guests off the source and verify their
  original UIDs, service health and BGP state; see `../kubevirt-vms/SKILL.md`.
- Set and verify Ceph `noout` before draining an OSD host. Do not bypass PDBs.
  Singleton ClickHouse/Keeper budgets can prohibit all voluntary disruption;
  resolve that maintenance policy deliberately rather than forcing the drain.
- Drain normally, run `talosctl upgrade`, and verify the expected OS, Ready and
  schedulable state. Wait for every Deployment, StatefulSet and DaemonSet.
- Require etcd quorum and Ceph recovery before the next node. sea1 has 21 OSDs,
  three mons and 314 PGs at this writing. Remove `noout` after all PGs are
  active+clean and require HEALTH_OK; leave maintenance stopped if recovery fails.

## Ceph CLI inside Rook mgr

The default admin keyring path may not exist. Read-only commands can use
`--name mgr.a --keyring /etc/ceph/keyring-store/keyring` with the pod's
`ROOK_CEPH_MON_HOST`. For admin maintenance, stream the
`ceph/rook-ceph-admin-keyring` secret into a mode-600 temporary file in a Ready
mgr pod, remove it with a shell trap, and **unset CEPH_ARGS** before invoking
`ceph --name client.admin --keyring <file> --mon-host "$ROOK_CEPH_MON_HOST"`.
Inherited CEPH_ARGS can override the supplied identity and cause auth failures.
Never put the key in command arguments or print it.

## Kubernetes roll

Review `talosctl upgrade-k8s --dry-run --to <version>`, including bootstrap
manifest diffs. sea1's CoreDNS ConfigMap is managed by Argo and has explicit
upstreams, internal/reverse zones and `.consul` forwarding. The automatic
upgrader replaces it with Talos defaults, breaking those lookups. Until
bootstrap ownership is separated, use the documented manual image patches for
API server, controller manager and scheduler, one component/node at a time;
verify each mirror pod's image and Ready state. Keep the Corefile unchanged.

Handle sea1 kubelets one host at a time after explicitly migrating its guests
off. Patch only `machine.kubelet.image` with `--mode=no-reboot`, verify the new
kubelet version and healthy workloads, then uncordon. VM continuity needs both
LAN and desktop service probes plus unchanged guest boot IDs/uptimes; desktop
Tailscale failover can fail private-address probes while LAN services stay up.

A Helm-hook Deployment can report no sync status while the Application is
Synced and ignore desired updates. Check `.status.resources[].hook` and live
annotations when a committed probe/config change does not land. Persistent
exporters should be ordinary resources: remove Helm hook metadata in Kustomize
and adopt the live object with the same committed metadata before verifying it.

Do not gate the next host solely on pod phase. Require full storage recovery,
VM services and router BGP peer states, prefix counts and session uptimes.
