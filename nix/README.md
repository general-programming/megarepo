# nix

NixOS flake configs for general-programming machines. Imported from
[general-programming/nixos](https://github.com/general-programming/nixos)
with history via `git subtree`.

## Machines

| Name             | Role                                                   |
| ---------------- | ------------------------------------------------------ |
| `proxmox`        | Base Proxmox VM template                               |
| `sea1-core`      | Core VM in sea1 (DNS, consul, ZFS mirror)              |
| `fmt2-core`      | Core VM in fmt2 (DNS, consul, ZFS mirror, GRUB mirror) |
| `sea420-desktop` | KDE Plasma desktop workstation                         |

## Usage

All commands run from this directory (or use `./nix#<machine>` from the repo root).

```bash
# Evaluate everything
nix flake check

# Build a machine without switching
nix build .#nixosConfigurations.fmt2-core.config.system.build.toplevel

# Deploy a machine
nix run 'nixpkgs#nixos-rebuild' -- --flake .#fmt2-core --target-host root@<host> switch

# Update inputs and deploy fmt2-core
./update.sh
```

GitOps via [comin](https://github.com/nlewo/comin) is wired in
`modules/gitops.nix` (pulls this repo, subdir `nix`) but currently disabled on
all machines.
