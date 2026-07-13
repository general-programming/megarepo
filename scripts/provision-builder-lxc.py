"""Upload the NixOS `sea1-nix-builder` LXC tarball to a Proxmox node and create/start
the container from it.

Build the image first:

    just -f nix/justfile build_builder_image

Then run this script with the resulting store path:

    python3 scripts/provision-builder-lxc.py \
        --node sea1-01 --vmid 9000 \
        --tarball /nix/store/.../tarball/nixos-image-lxc-....tar.xz \
        --vault-path app/proxmox/sea1-erin \
        --proxmox-host proxmox.service.sea1.consul

Needs a Vault KV v2 secret at --vault-path with `user`, `token_name`,
`token_value` for a Proxmox API token scoped to VM.Allocate /
VM.Config.* / Datastore.AllocateSpace / Datastore.AllocateTemplate on
the target node+storage (the existing proxmox-cert-updater token is
scoped for certificate management only and should not be reused here).
"""

import argparse
import os
import time

import hvac
from proxmoxer import ProxmoxAPI


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--node", required=True, help="Proxmox node to create the container on")
    parser.add_argument("--vmid", type=int, required=True, help="VMID for the new container")
    parser.add_argument("--tarball", required=True, help="Path to the built LXC tarball")
    parser.add_argument(
        "--vault-path",
        default="app/proxmox-builder-provisioner",
        help="Vault KV v2 path (under secret/) holding user/token_name/token_value",
    )
    parser.add_argument("--hostname", default="sea1-nix-builder")
    parser.add_argument("--storage", default="local", help="Storage for the vztmpl upload")
    parser.add_argument("--rootfs-storage", default="local-lvm", help="Storage for the container's rootfs")
    parser.add_argument("--rootfs-size-gb", type=int, default=32)
    parser.add_argument("--bridge", default="vmbr0")
    parser.add_argument("--cores", type=int, default=0, help="0 = all cores visible on the node")
    parser.add_argument("--cpulimit", type=int, default=0, help="0 = unlimited")
    parser.add_argument("--cpuunits", type=int, default=512, help="Scheduler weight; default is 1024")
    parser.add_argument("--memory-mb", type=int, default=32768)
    parser.add_argument("--proxmox-host", default="proxmox.service.fmt2.consul")
    return parser.parse_args()


def main() -> None:
    args = parse_args()

    vault = hvac.Client()
    secrets = vault.secrets.kv.v2.read_secret_version(path=args.vault_path)["data"]["data"]

    proxmox = ProxmoxAPI(
        args.proxmox_host,
        user=secrets["user"],
        token_name=secrets["token_name"],
        token_value=secrets["token_value"],
        verify_ssl=False,
    )
    node = proxmox.nodes(args.node)

    tarball_name = os.path.basename(args.tarball)
    print(f"Uploading {tarball_name} to {args.node}:{args.storage} ...")
    with open(args.tarball, "rb") as f:
        node.storage(args.storage).upload.post(content="vztmpl", filename=f)

    cores = args.cores or node.status.get()["cpuinfo"]["cpus"]
    print(f"Creating CT {args.vmid} ({args.hostname}) on {args.node}: "
          f"cores={cores} cpulimit={args.cpulimit} cpuunits={args.cpuunits} memory={args.memory_mb}MB")

    node.lxc.post(
        vmid=args.vmid,
        hostname=args.hostname,
        ostemplate=f"{args.storage}:vztmpl/{tarball_name}",
        arch="amd64",
        unprivileged=1,
        cores=cores,
        cpulimit=args.cpulimit,
        cpuunits=args.cpuunits,
        memory=args.memory_mb,
        swap=0,
        rootfs=f"{args.rootfs_storage}:{args.rootfs_size_gb}",
        net0=f"name=eth0,bridge={args.bridge},ip=dhcp",
        # Modern systemd units (systemd-networkd, journald, tmpfiles-setup-dev,
        # ...) use mount-namespace sandboxing that Proxmox's default
        # unprivileged-LXC AppArmor profile blocks without nesting enabled.
        features="nesting=1",
        start=1,
    )

    for _ in range(30):
        status = node.lxc(args.vmid).status.current.get()
        print(f"CT {args.vmid} status: {status['status']}")
        if status["status"] == "running":
            break
        time.sleep(2)


if __name__ == "__main__":
    main()
