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

## Trap: jumbo pod MTU black-holes our own Traefik

Cilium gives sea1 pods a jumbo default route (`default via ... dev eth0 mtu
9000`) and nothing clamps it on egress. TLS to `attic.owo.me` (and to sea1's
own Traefik) then hangs immediately after the ClientHello — the oversized
reply is dropped and no ICMP fragmentation-needed comes back. Plain HTTP to
the same IP returns instantly, and `cache.nixos.org` and `github.com` happen
to survive it, which makes this look like an Attic problem rather than an MTU
problem.

`build.sh` clamps its own netns to 1500 (it is privileged, so it can) and
fails fast on a probe if that did not help. **Note that both the link and the
route need clamping** — `ip link set eth0 mtu 1500` alone changes nothing
while the route carries an explicit `mtu 9000`.

This is not specific to this workload; any sea1 pod talking HTTPS to our
Traefik hits it. The real fix belongs in the CNI config, at which point the
clamp here can go.

## Trap: never let the build start with Attic unreachable

`nix-cache.nix` sets `fallback = true`, so an unreachable Attic does not fail a
build — it silently builds instead. That is right for a fleet host and wrong
here: the fleet's `nix.package` is **Lix**, built from a git flake input that
no public cache carries, and Lix's `installCheckPhase` (pytest functional2)
**deadlocks in this container** — a worker blocks forever in `fifo_open` /
`wait_for_partner` at 0% CPU. The first run wedged for 40 minutes that way,
purely because the MTU bug above hid Attic while nix was making its plan.

Hence the `curl -sSf` probe in `build.sh` between the MTU clamp and the build:
if Attic is not reachable, fail the run in seconds rather than discover it as a
hung Lix an hour later. `activeDeadlineSeconds: 14400` is the backstop.

If a Lix bump ever lands that nothing has built yet, this builder cannot be the
first to build it. Push it from a fleet host that already has it:

```sh
nix run nixpkgs#attic-client -- push general-programming:general-programming \
  "$(readlink -f /run/current-system)"
```

## Trap: the nix image has almost no userland

`nixos/nix` ships nix, coreutils, git and curl — but **no `sed`, no `awk`**.
Resolve anything else from the pinned nixpkgs rather than assuming it is on
PATH.

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
