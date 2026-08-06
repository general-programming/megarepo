# nix-cache-builder

Keeps the [Attic](../attic/README.md) binary cache ahead of the fleet. A
CronJob on **sea1** watches `main`, and when anything under `nix/` or `go/`
moves it runs `just build_cache` (`nix/justfile`) — building every
`nixosConfigurations.*` system closure and pushing it to
`general-programming`. By the time comin polls (every 600s,
`nix/modules/gitops.nix`) the closures are already substitutable, so no fleet
host builds.

This replaces running `just build_cache` on a laptop.

## Shape

- `build.sh` — fetch `main`, skip unless `nix/`, `go/`, `go.mod`, `go.sum` or
  `vendor/` changed since the last built ref, then `just build_cache`. The
  last built ref lives at `/nix/var/nix-cache-builder/last-built-ref` on the
  PVC.
- `seed-store.sh` — the PVC mounts over `/nix` and would hide the image's own
  Nix, so an initContainer copies the image store onto the PVC first.
- `nix-store` PVC — 300Gi `ceph-rbd-xfs`. A warm store only saves downloads:
  `build_cache` passes `--always-allow-substitutes` and every substituter is
  trusted, so **losing this PVC costs a slow run, not a fleet rebuild.**
  Delete it freely.
- Push token — `secret/app/nix-cache-builder` in Vault, key `ATTIC_TOKEN`,
  surfaced by VSO. Push-only on one cache, unlike the admin token in
  `secret/app/attic-admin`.

## Why a CronJob and not actions-runner-controller

ARC would reuse `.github/workflows/nix.yaml`'s trigger semantics, but it needs
a GitHub App or PAT registered as `githubConfigSecret`, and no such credential
exists in Vault. A CronJob needs none: the repo is public over HTTPS
(`git ls-remote https://github.com/general-programming/megarepo.git` works
unauthenticated — this is also how comin clones it), and change detection is a
`git diff --quiet` against a stored ref.

Push-triggering also buys less than it looks like. A closure build takes tens
of minutes, so no trigger — webhook or poll — reliably finishes inside comin's
600s window for a large change. A five-minute tick is well inside the noise.

If a GitHub App ever exists, switching is a small change: add a `build-cache`
job to `nix.yaml` gated on `main`, point `runs-on` at the scale set, and drop
this CronJob.

## Scheduling

The builder is soft-preferred onto `sea1-k8s-1` and soft-steered off
`sea1-k8s-2` (which hosts every CNPG primary). Both are control-plane nodes
with 7 OSDs each, so requests/limits are explicit — 8/32Gi requested, 48
cores / 256Gi capped, with `max-jobs`/`cores` in `nix.conf` pinned to match,
because Nix reads the host CPU count and not the cgroup's.

The namespace is `pod-security enforce: privileged`: Nix's build sandbox wants
mount and user namespaces. The alternative is `sandbox = false`, which is
worse.

## Runbook

Force a run now:

```sh
kubectl -n nix-cache-builder create job --from=cronjob/nix-cache-builder manual-$(date +%s)
kubectl -n nix-cache-builder logs -f job/manual-...
```

Force a rebuild of an already-built ref:

```sh
kubectl -n nix-cache-builder exec -it job/... -- rm /nix/var/nix-cache-builder/last-built-ref
```

Rotate the push token (from fmt2, where Attic runs):

```sh
kubectl -n attic exec deploy/attic -c attic -- atticadm make-token \
  -f /config/server.toml --sub nix-cache-builder --validity 10y \
  --pull general-programming --push general-programming
bao kv put secret/app/nix-cache-builder ATTIC_TOKEN=...
```

The current token expires **2036-08**.
