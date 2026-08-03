# Building a KubeVirt guest from an ISO, and driving its console

For guests with no disk to copy — a new VyOS router, a fresh appliance. The
migration path in `SKILL.md` is easier; use this only when there is nothing to
migrate from.

## Getting the ISO in

Two options, in order of preference.

**containerDisk** — wrap the ISO in a scratch image and let KubeVirt pull it.
Immutable, versioned, and needs no CDI:

```dockerfile
FROM scratch
ADD vyos-1.5-rolling.iso /disk/boot.iso
```

Reference it as a cdrom, and give the guest an empty PVC to install onto:

```yaml
domain:
  devices:
    disks:
      - name: rootdisk
        disk: {bus: scsi}
        bootOrder: 2
      - name: installer
        cdrom: {bus: sata, readonly: true}
        bootOrder: 1
volumes:
  - name: rootdisk
    persistentVolumeClaim: {claimName: <name>-root}
  - name: installer
    containerDisk: {image: <registry>/<name>-installer:<tag>}
```

**CDI upload** — `virtctl image-upload` into a DataVolume. Needs CDI installed
and streams through your workstation; only worth it when you cannot push an
image to a registry.

**Remove the cdrom and its `bootOrder` once installed.** Leaving it means an
interrupted boot can drop back into the installer, and on a router that reads
as a hardware fault rather than a boot-order mistake.

## Talking to the console

An installer is interactive, so the serial console is the whole job.

`virtctl console <vm> -n <ns>` is the tool, and it ships in the devenv — the
nixpkgs attribute is `kubevirt`, not `virtctl`. Keep it aligned with the
cluster (`kubectl -n kubevirt get kubevirt kubevirt -o jsonpath='{.status.observedKubeVirtVersion}'`);
a client that drifts from virt-api fails obscurely rather than cleanly.

```bash
virtctl console sea1-vpn-leaf-2 -n network-vpn      # ctrl-] to detach
virtctl vnc     sea1-vpn-leaf-2 -n network-vpn      # graphical installers
```

Notes that cost time if you learn them the hard way:

- **Detach is `ctrl-]`.** `ctrl-c` goes to the guest.
- **The console is single-attach.** A forgotten session elsewhere makes the
  next one fail with a lock error rather than anything descriptive.
- **Nothing is buffered.** Attach *before* starting the VM or the installer's
  first prompts are gone. `kubectl logs <virt-launcher-pod> -c guest-console-log`
  replays what was printed, which is enough to see where a boot stopped but no
  use for interacting.
- The guest must have a serial console on `ttyS0`. VyOS and most appliance
  ISOs do; some desktop distros only enable it with a kernel argument.

**Read-only alternative**, no virtctl needed — enough to answer "did it boot":

```bash
kubectl -n <ns> logs $(kubectl -n <ns> get pod -l kubevirt.io=virt-launcher -o name | head -1) \
  -c guest-console-log | tail -40
```

That is how the `No bootable option or device was found` EFI failures in
`SKILL.md` were diagnosed.

## Sequence for a from-scratch guest

1. **Define it in `network.yml`** and `barf validate`.
2. **Mint its secrets** — a fresh host fails every barf command until they
   exist:
   ```bash
   go run ./go/cmd/barf secrets mint <host> admin-password
   ```
   Each WireGuard link also needs a keypair at
   `cluster-secrets/wglink-<a>--<b>-<endpoint>` holding `private_key` and
   `public_key` — **two per link, one per endpoint**. These are real WireGuard
   keys, not random tokens, so do not hand-write them with a generic minting
   tool.
3. **Create PVC + VM** with the cdrom attached, `runStrategy: Always`.
4. **Attach the console before it boots**, install to the virtio-scsi disk.
5. **Remove the cdrom**, reboot, confirm it comes up off the PVC.
6. **`barf generate` then `barf deploy`** the real config.
7. **Verify against a sibling**: `barf status` should show
   `CONFIG CONSISTENT: yes`, and for a router `show bgp summary` should reach
   the same neighbours with comparable prefix counts.

## Ordering, for anything that routes

Bring a new router up **passive** — no VRRP, no NAT, no upstream route
pointing at it. Let it peer, confirm it holds its addresses and learns the
right prefixes, and only then promote it. A router that half-works while
holding a gateway address is worse than one that is plainly absent.
