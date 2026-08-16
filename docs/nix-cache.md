# Nix binary cache (Attic, FMT2)

`https://attic.owo.me` — self-hosted [Attic](https://github.com/zhaofengli/attic)
on the FMT2 cluster, next to its radosgw. One cache, `general-programming`,
public for pulls. Every NixOS host — SEA1 included — substitutes from it over
the WAN.

Deployment: `argocd/apps/infra/attic/` (bootstrap runbook in its README).
Population: `argocd/apps/infra/nix-cache-builder/` (see below). Client config:
`nix/modules/nix-cache.nix`.

## Using it

Fleet machines already have it: `gpNixCache.enable = true` in
`nix/machines/base.nix` wires the substituter and public key
(`general-programming:wrpHyA9Gfx0BSA3vlxeESq+VSP+wvr5zSAgC3rXLN+8=`). For a
non-fleet machine:

```nix
nix.settings = {
  substituters = [ "https://attic.owo.me/general-programming" ];
  trusted-public-keys = [ "general-programming:wrpHyA9Gfx0BSA3vlxeESq+VSP+wvr5zSAgC3rXLN+8=" ];
};
```

Pushing needs a token (pull is public):

```sh
attic login general-programming https://attic.owo.me/ <token>
attic push general-programming:general-programming ./result
```

The cache skips paths signed by `cache.nixos.org-1` and `cache.nixos-cuda.org`
— upstream-substitutable closures (NVIDIA/CUDA included) are deliberately not
stored.

## How it stays warm

`nix-cache-builder`, a CronJob **on SEA1**, watches `main` and runs
`just build_cache` (`nix/justfile`) whenever `nix/`, `go/`, or the Go module
files move — building every `nixosConfigurations.*` closure and pushing it. By
the time comin polls (600s), fleet hosts substitute instead of building.
`just build_cache` by hand from `nix/` still works and does the same thing.

## Architecture

```
nix clients ── https://attic.owo.me ── CF DNS (unproxied) ──▶ attic API (fmt2)
                    │                                             │
     307 presigned redirects for                         attic-db (CNPG)
     single-chunk NARs                                            │
                    ▼                                             ▼
     http://rgw-fmt2.generalprogramming.org ◀── s3://attic on fmt2 radosgw
```

- **Not Cloudflare-proxied**, deliberately: NAR chunks stream through the API
  server, and single-chunk NARs are served as 307 redirects to presigned URLs
  on `rgw-fmt2.generalprogramming.org` — clients must reach that endpoint
  directly, and proxying would cap uploads.
- Chunked + zstd-compressed storage; GC every 12h, 3-month default retention.
- Tokens are RS256; the admin secret and S3 credentials live in Vault at
  `secret/app/attic-secrets`, the builder's push-only token at
  `secret/app/nix-cache-builder`.

## Sharp edges

- **attic.owo.me drops ~1/3 of connections** (long-standing flakiness);
  clients ride it out via `connect-timeout = 5` + `fallback = true`, and
  pushes just need retrying. A down cache never blocks a deploy — hosts fall
  back to cache.nixos.org or local builds.
- Fresh Lix on the builder needs `/dev/net/tun` for pasta FOD fetches; broken
  in LXC/k8s without it (fallback: `pasta-path=""`).
- The cache is **public-read by name**: do not push closures containing
  secrets. (Fleet closures are already public-safe by policy — secrets come
  from Vault at runtime, never the store.)
