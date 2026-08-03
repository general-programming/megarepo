---
name: kubevirt-vms
description: Run and migrate virtual machines on the sea1 Kubernetes cluster with KubeVirt — move a Proxmox guest onto ceph-backed storage, attach it to the LAN bridge, enable live migration, and avoid the EFI/NIC/prune traps that silently brick guests. Use when migrating a VM off Proxmox, creating a new KubeVirt guest, or debugging one that will not boot, has no network, or will not live-migrate.
---

# KubeVirt guests on sea1

Proven migrating `freepbx` (Proxmox VM 911) and `freeipa` (VM 113) off
`sea1-hv-1`. Everything is GitOps: guests live in `argocd/apps/vms/<name>/`
under the `vms` AppProject. Never `kubectl apply` a guest.

## Layout

```
argocd/projects/vms.yaml              AppProject + ApplicationSet (prune: false)
argocd/apps/vms/<name>/sea1/          namespace.yaml, nad-br0-lan.yaml, vm-<name>.yaml
argocd/apps/vms/<name>/fmt2/          empty kustomization (see "one overlay per cluster")
```

One Application per VM, and because every ApplicationSet here sets
`namespace: '{{path.basename}}'`, **one namespace per VM too**. Name the
directory for the *service* (`freeipa`), not the host (`sea1-ipa`) — the
guest keeps its own hostname internally regardless.

`NetworkAttachmentDefinition`s are namespaced, so each VM carries its own
copy of `br0-lan`. Six lines beats cross-namespace coupling.

**One overlay per cluster, always.** The infra/vms ApplicationSets are a
matrix of clusters × directories, so a missing `fmt2/` yields an Application
pointing at a path that does not exist, stuck in `Unknown/ComparisonError`
forever. Ship an empty `kustomization.yaml` with `resources: []`.

## Cluster prerequisites (already in place)

- KubeVirt v1.8.4 — `argocd/apps/infra/kubevirt/`
- Multus v4.3.0, confined to nodes labelled
  `generalprogramming.org/lan-bridge=true` — `argocd/apps/infra/multus/`
- `br0` bridge on `sea1-k8s-{0,1,2}` (talconfig `BridgeConfig`), giving guests
  real L2 on the sea1 LAN
- `sea1-k8s-103-0` carries `generalprogramming.org/no-virt`; the KubeVirt CR's
  `workloads.nodePlacement` keeps virt-handler off it, so guests cannot land
  there (it has no `br0`)
- `ceph-rbd-retain` StorageClass — `reclaimPolicy: Retain`,
  `imageFeatures: layering`

## Migrating a Proxmox guest

### 1. Survey before touching anything

```bash
./bin/vssh localadmin@sea1-hv-1.generalprogramming.org 'sudo qm config <vmid>'
```

Check for, in order of how badly they hurt:

- **PCI passthrough** — a hard blocker, stop here.
- **Disk location.** `scsi0: pool:vm-<id>-disk-N` means it is already an RBD
  image in this ceph cluster, so the copy is intra-ceph (~45–90s for 32–64
  GiB). `local-zfs:` means a real transfer.
- **Guest addressing.** Grab it while the guest still runs:
  ```bash
  sudo qm guest cmd <vmid> network-get-interfaces     # works even when exec is blocked
  sudo qm guest exec <vmid> -- cat /etc/network/interfaces
  ```
  On Fedora, SELinux blocks `guest exec` (`Failed to execute child process
  "ip" (Permission denied)`) — `guest cmd network-get-interfaces` still works.
- **Is the address static or a DHCP reservation?** Decides whether you need
  the NIC-name pin below:
  ```bash
  ./bin/vssh 10.3.2.6 'grep -i -A3 -B1 "<MAC>" /var/lib/kea/netbox/hosts4.json'
  ```
- **Replicas / blast radius.** `freeipa` was safe to take down because two
  other IPA servers were verified serving first.

### 2. Stage the app while the guest is still up

Commit the namespace, NAD, PVC and VM with **`runStrategy: Halted`**. The PVC
provisions during uptime, and Halted stops ArgoCD booting the VM off an empty
disk the moment the claim appears.

Always **RWX Block on `ceph-rbd-retain` from the start**. `accessModes` is
immutable on a bound claim, so getting this wrong means a second PVC and
another copy (which is exactly what freepbx cost).

```yaml
spec:
  storageClassName: ceph-rbd-retain
  volumeMode: Block            # raw guest disk; the SC fstype param is ignored
  accessModes: [ReadWriteMany] # required for live migration
```

VM spec essentials — carry the identity across:

```yaml
spec:
  runStrategy: Halted          # flip to Always after the copy
  template:
    spec:
      evictionStrategy: LiveMigrate
      affinity:                # soft preference for the bridged nodes
        nodeAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
            - weight: 100
              preference:
                matchExpressions:
                  - {key: generalprogramming.org/lan-bridge, operator: In, values: ["true"]}
      domain:
        firmware:
          uuid: <smbios1 uuid from qm config>
          bootloader: {efi: {secureBoot: false}}
        devices:
          autoattachPodInterface: false      # LAN only
          disks: [{name: rootdisk, disk: {bus: scsi}}]   # matches virtio-scsi-single
          interfaces:
            - name: lan
              bridge: {}
              macAddress: "<MAC from qm config>"          # see below
      networks: [{name: lan, multus: {networkName: br0-lan}}]
```

**Preserve the MAC.** It carries DHCP reservations, and any public `/32`
routed to the guest depends on the upstream router's ARP entry for it.

### 3. Shut down, snapshot, copy

```bash
sudo qm shutdown <vmid> --timeout 150
sudo rbd snap create pool/vm-<id>-disk-N@pre-kubevirt
sudo rbd snap protect pool/vm-<id>-disk-N@pre-kubevirt      # instant rollback
```

Get the target image from the bound PV, then copy inside ceph:

```bash
kubectl -n <ns> get pvc <name>-root -o jsonpath='{.spec.volumeName}'
kubectl get pv <pv> -o jsonpath='{.spec.csi.volumeAttributes.imageName}'
```
```bash
SRC=$(sudo rbd map --read-only pool/vm-<id>-disk-N)
DST=$(sudo rbd map k8s-rbd/csi-vol-<uuid>)
sudo dd if=$SRC of=$DST bs=64M conv=sparse status=none && sync
sudo cmp -n 2147483648 $SRC $DST      # spot-check first 2 GiB
```

Use map+dd rather than `rbd deep-copy` — it preserves the ceph-csi image
metadata. **Always `rbd unmap` both when done**; a stale map keeps a watcher
on the image and blocks `rbd rm` later.

### 4. Pre-boot fixes on the *target* image — both silently brick the guest

These live on the image you copied **to**. Re-copying from the source loses
them; that bit once already.

> **Copy once, boot once.** Re-copying after the guest has run rolls it back
> in time. Harmless for a stateless box, corrupting for anything that
> replicates — it cost a FreeIPA replication agreement (`Missing data
> encountered`, peer RUV ahead of the restored changelog, fixed only by
> `ipa-replica-manage re-initialize --from=<healthy peer>`). If you must
> redo a guest (renaming its namespace, say), destroy the first attempt
> *before* it ever boots, or plan to re-initialize it against its peers
> afterwards.

**EFI** — only when `qm config` shows `bios: ovmf`. A SeaBIOS guest (no
`bios:` line, no `efidisk`) needs nothing at all: leave `firmware.bootloader`
unset and KubeVirt defaults to BIOS.

KubeVirt supplies a *fresh* OVMF NVRAM, so any guest that booted
from a vendor path plus an NVRAM entry fails at
`No bootable option or device was found`. Mount the ESP (partition 1) and
ensure the removable-media path exists **with grub beside it** — shim
chainloads `grubx64.efi` from its *own* directory:

```bash
sudo mount ${DST}p1 /mnt/esp
sudo mkdir -p /mnt/esp/EFI/BOOT
sudo cp /mnt/esp/EFI/<debian|fedora>/shimx64.efi /mnt/esp/EFI/BOOT/BOOTX64.EFI
sudo cp /mnt/esp/EFI/<debian|fedora>/grubx64.efi /mnt/esp/EFI/BOOT/grubx64.efi
```

Debian ships no `EFI/BOOT` at all. Fedora ships `BOOTX64.EFI` + `fbx64.efi`
but *not* `grubx64.efi`, so it falls through to `fbx64.efi`, which recreates
an NVRAM entry and reboots — and KubeVirt's NVRAM does not persist, so that
repeats on every start. Copying grub in makes it deterministic.

**Multi-NIC guests**: declare the interfaces in the same order as Proxmox's
`net0`, `net1`, … and preserve every MAC. Check first whether the guest pins
them itself — VyOS carries `hw-id` per interface in `config.boot`, which binds
`eth0`/`eth1` by MAC regardless of PCI order and removes the risk entirely.
Without such a pin, a swap silently puts the external config on the internal
port.

**NIC renaming.** The card lands on a different PCI slot, so a guest whose
config is keyed to an interface name comes up with **no network and no
console**. Only needed if the guest configures statically by name (freepbx,
`enp6s18`); guests on a DHCP reservation (freeipa, `ens18`) just work off the
preserved MAC. When needed, mount the root filesystem and add:

```ini
# /etc/systemd/network/10-lan.link
[Match]
MACAddress=bc:24:11:xx:xx:xx
[Link]
Name=<original name>
```

udev applies `.link` files even when networkd is not managing the interface,
so this works under ifupdown and NetworkManager alike. Note Fedora Server
puts root on LVM, so reaching it means activating the guest VG on the
hypervisor — avoid if the DHCP check says you can.

`fstab` is normally UUID-based, so the virtio-scsi → `bus: scsi` change needs
nothing.

### 5. Start and verify

Commit `runStrategy: Always`, then:

```bash
kubectl -n argocd annotate application <name> argocd.argoproj.io/refresh=hard --overwrite
kubectl -n <ns> get vmi <name> -o wide
kubectl -n <ns> logs <virt-launcher-pod> -c guest-console-log | tail
```

Check the guest agent view — it proves the interface name and addresses:

```bash
kubectl -n <ns> get vmi <name> -o jsonpath='{.status.interfaces}'
```

Then reachability from a throwaway pod (`kubectl run ... --image=busybox:1.36`)
for each address and service port the guest is supposed to serve.

Ports answering is not validation, and neither is a status string — a
FreeIPA agreement read `Replica acquired successfully` for seven days while
dead. **Capture the service's own health before you shut the guest down**, so
"after" has something to equal. For a barf-managed VyOS device that is
`show bgp summary` per neighbour with prefix counts; the migration is good
when every session returns with the same counts.

That baseline also catches asymmetry the migration did not cause: it is how
sea1-vpn-spine-1 turned out to be the only spine holding a peer up, making it
the riskier half of a "redundant" pair.

`vssh` may fail on non-fleet guests (its CA certs exhaust `MaxAuthTries`);
plain `ssh -o BatchMode=yes root@<ip>` usually works where vssh does not. For
network devices use barf — `go run ./go/cmd/barf` from the repo root (it was
ported from Python; `.venv/bin/barf` is a stale shim). `barf status` gives
uptime, version and config drift, and piping a command into
`barf device ssh <host>` works for read-only queries.

Leave the Proxmox VM **defined but stopped** and the `@pre-kubevirt` snapshot
in place for at least a week.

## Live migration

Needs both, or it silently will not happen:

- **RWX Block PVC.** RWO gives `LiveMigratable=False /
  DisksNotLiveMigratable`; the image is mapped on source and target at once
  during handover. RWX is only about the handover — the guest filesystem
  stays single-writer.
- **`evictionStrategy: LiveMigrate`.** Default `None` means a drain deletes
  virt-launcher and the VM cold-boots elsewhere: a reboot, not a migration.

**A cordon does nothing.** It only blocks new scheduling; running guests are
untouched. Only a *drain* evicts. (No PodDisruptionBudget appears in KubeVirt
1.8 — protection is via the eviction path, not a PDB.)

Trigger one by hand — a migration is a transient operation, like a Job, so it
is imperative by nature and does not belong in git. A failed migration is
non-destructive; the guest stays on the source node.

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachineInstanceMigration
metadata: {name: <vm>-migrate, namespace: <ns>}
spec: {vmiName: <vm>}
```
```bash
kubectl -n <ns> get vmim <vm>-migrate -o jsonpath='{.status.phase}'
kubectl -n <ns> get vmi <vm> -o jsonpath='{.status.nodeName}'
```

## ArgoCD traps

- **`prune: false` on the `vms` project is deliberate.** A pruned VM takes its
  PVC — and the RBD image and the guest's data — with it. Removing a VM from
  git leaves it running and needs a deliberate `kubectl delete`. Combined with
  `reclaimPolicy: Retain`, moving a guest between Applications is survivable;
  with the defaults it is data loss.
- **A Halted `VirtualMachine` reports health `Suspended`**, which ArgoCD never
  treats as Healthy. A sync waiting on one hangs forever and blocks the whole
  app. Clear it with:
  ```bash
  kubectl -n argocd patch application <app> --type json -p '[{"op":"remove","path":"/operation"}]'
  ```
- **StorageClass `parameters` are immutable.** Changing them needs
  `argocd.argoproj.io/sync-options: Force=true,Replace=true`. `Replace=true`
  alone is `kubectl replace` and hits the same error.
- ArgoCD's poll interval lags; a hard refresh annotation is what forces it.
  **Check the live object, not just app status** — `Synced` has been reported
  against the right commit while the cluster object was still stale.

## Cleaning up orphan volumes

`Retain` means deleting a PVC/PV leaves the RBD image behind on purpose. To
actually reclaim:

```bash
kubectl get pv -o json | jq -r '.items[]|select(.status.phase!="Bound")|
  "\(.metadata.name) \(.spec.csi.volumeAttributes.imageName)"'
kubectl delete pv <pv>
sudo rbd rm k8s-rbd/<image>
```

If `rbd rm` reports the image is still open, check `rbd status k8s-rbd/<img>`
for watchers. A watcher on a hypervisor address is usually your own forgotten
`rbd map` (`sudo rbd showmapped`, then `rbd unmap /dev/rbdN` — the device is
the *last* column). A watcher on a Talos node address is a stale CSI mapping
that clears when the nodeplugin reconciles.
