# Deploys (GitOps)

NixOS machines deploy themselves with [comin](https://github.com/nlewo/comin),
pull-based GitOps configured in
[`nix/modules/gitops.nix`](../../nix/modules/gitops.nix). There is no push
step: merging to `main` **is** the deploy.

## How a change rolls out

1. A commit touching `nix/` lands on `main`.
2. (Optional but recommended) run `just build_cache` so the closures are in
   the [binary cache](cache.md) before machines notice the commit.
3. Each machine's comin polls the repo (every 600s by default), sees the new
   commit, evaluates `nix#nixosConfigurations.<hostname>` from the repo's
   `nix/` subdir, builds or substitutes it, and switches to it.

Machines opt in with:

```nix
gitops.enable = true;
```

comin requires the machine's `machineID` to match — set it in
`nix/vars/machines.nix` (it guards against a config switching the wrong
box). Deployment state is exported to Prometheus via the comin exporter
(port from `vars.ports.comin-exporter`, scraped over Tailscale).

## Caveats

- A broken eval on `main` means machines silently keep running the last good
  generation — check the comin exporter metrics or
  `journalctl -u comin` on the machine.
- comin deploys from GitHub, not your checkout; local uncommitted changes
  never deploy. For hands-on iteration use `just update <machine>`
  (see [Machine lifecycle](machines.md)).
