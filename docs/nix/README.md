# NixOS infrastructure

The NixOS estate lives in [`nix/`](../../nix/) as a single flake. Machines are
defined under `nix/machines/<name>/`, share [`nix/machines/base.nix`](../../nix/machines/base.nix),
and pull reusable pieces from `nix/modules/` via the `self.lib.nixosModule`
helper. Per-machine constants (machine IDs, ports) live in
`nix/vars/machines.nix`.

We run [Lix](https://lix.systems) instead of upstream Nix on all machines.

Current machines:

| Machine         | Role                          |
| --------------- | ----------------------------- |
| `sea1-core`     | sea1 core services host       |
| `fmt2-core`     | fmt2 core services host       |
| `proxmox`       | hypervisor                    |
| `sea420-desktop`| desktop                       |

## Pages

- [Deploys (GitOps)](gitops.md) — how changes reach machines via comin
- [Binary cache](cache.md) — the self-hosted Attic cache and `just build_cache`
- [Machine lifecycle](machines.md) — provisioning, rebuilding, and rekeying
