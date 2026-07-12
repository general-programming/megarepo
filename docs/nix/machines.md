# Machine lifecycle

All recipes live in [`nix/justfile`](../../nix/justfile) and are run from
`nix/`. `provision` and `rekey` need a Vault token that can read the AppRole
role_id and mint secret_ids (`VAULT_TOKEN` or `~/.vault-token`).

## Fresh install

```sh
just provision <machine> <target-ip> [role=nixos-core]
```

Runs [nixos-anywhere](https://github.com/nix-community/nixos-anywhere)
against the target with the machine's disko layout, pre-seeding Vault
AppRole credentials into `/var/lib/vault-agent` (or `/persist/...` for
impermanent machines) so `vault-agent` works on first boot.

Before provisioning a new machine:

1. Create `nix/machines/<name>/configuration.nix` (+ `disko.nix`).
2. Register it in `nixosConfigurations` in `nix/flake.nix`.
3. Add its `machineID` to `nix/vars/machines.nix` (generate with
   `uuidgen | tr -d -` or take `/etc/machine-id` after install).

## Rebuild an existing machine

Normally you don't — merging to `main` deploys via [GitOps](gitops.md).
For hands-on iteration from your checkout:

```sh
just update <machine> [target-host]
```

which is `nixos-rebuild switch --flake .#<machine> --target-host root@...`
(defaults to `<machine>.generalprogramming.org`).

## Vault credentials

Machines authenticate to Vault (OpenBao) with AppRole via
[`nix/modules/vault-agent.nix`](../../nix/modules/vault-agent.nix); the
agent keeps a token sink at `/run/vault-agent/token`. secret_ids expire —
re-seed a running machine with:

```sh
just rekey <machine> [target-host] [role=nixos-core]
```

## Sanity checks

- `just netbox-check` — validates the NetBox API key in Vault by rendering
  and testing the dnsmasq config used by the DNS module.
- `nix flake check` / `nix eval .#nixosConfigurations.<machine>.config.system.build.toplevel.drvPath`
  — cheap eval check before pushing; remember flakes only see git-tracked
  files (`git add -N` new files first).
