# Binary cache (Attic)

We run a self-hosted [Attic](https://github.com/zhaofengli/attic) binary
cache so machines substitute pre-built closures instead of compiling. Every
NixOS machine trusts it via the `gpNixCache` block in
[`nix/machines/base.nix`](../../nix/machines/base.nix)
([`nix/modules/nix-cache.nix`](../../nix/modules/nix-cache.nix)).

| What                | Where                                                  |
| ------------------- | ------------------------------------------------------ |
| Substituter         | `https://attic.owo.me/general-programming`              |
| Public key          | `general-programming:wrpHyA9Gfx0BSA3vlxeESq+VSP+wvr5zSAgC3rXLN+8=`      |
| API endpoint        | `https://attic.owo.me/`                |
| Deployment          | `argocd/apps/infra/attic` (fmt2 k8s only)              |
| Admin token         | Vault `secret/app/attic-admin`                         |
| S3 creds + JWT key  | Vault `secret/app/attic-secrets`                       |

The cache is **public for pulls** — no credentials needed to substitute.
Pushing requires a token.

## Architecture

- `attic` Deployment — API server, behind traefik at
  `attic.owo.me`. NAR uploads/downloads stream through it.
- `attic-gc` Deployment — garbage collector. Prunes objects not accessed
  within the retention period (3 months, `[garbage-collection]` in
  `server.toml`), every 12 hours.
- `attic-db` — CNPG Postgres for chunk/NAR metadata.
- Chunk storage — S3 bucket `attic` on the fmt2 ceph radosgw, reached as
  `rgw-fmt2.generalprogramming.org` (A records for the rgw nodes, published
  by the `rgw-dns` Ingress annotation).

Two behaviors worth knowing:

- **Single-chunk NARs are served as 307 redirects** to pre-signed S3 URLs on
  the rgw endpoint. That's why the S3 endpoint has a real DNS name: cache
  clients must be able to resolve and reach it (it's on-net only, so caching
  works from the LAN/Tailscale but not the public internet).
- **Failure is soft.** Clients set `connect-timeout = 5` and `fallback =
  true`, so if the cache is down builds fall back to cache.nixos.org and
  local compilation. Nothing hard-depends on it.

## Populating the cache

Builds are pushed manually (no CI/timer yet). One-time setup:

```sh
attic login general-programming https://attic.owo.me/ \
  "$(vault kv get -field=token secret/app/attic-admin)"
```

Then, from `nix/`:

```sh
just build_cache
```

This builds every `nixosConfiguration` in the flake (status streamed through
nix-output-monitor) and pushes each closure. Run it after merging nix
changes to main — comin polls every 10 minutes, so pushing promptly means
machines substitute instead of building.

Ad-hoc paths can be pushed too: `attic push general-programming:general-programming ./result`. Pushes skip
chunks the server already has, so repeats are cheap.

Pushes also skip any path signed by an upstream cache key — the cache is
configured with `cache.nixos.org-1` and `cache.nixos-cuda.org` as upstream
keys (`attic cache info general-programming:general-programming`), so NVIDIA/CUDA closures substituted from
[cache.nixos-cuda.org](https://cache.nixos-cuda.org) are never re-uploaded.
This only works for paths that carry the upstream signature, i.e. ones the
pushing host substituted rather than built locally — so the host running
`just build_cache` should have the CUDA cache in its substituters
(`nix/modules/nvidia.nix`).

## Tokens

Tokens are stateless JWTs signed with the RS256 secret — there is no
revocation, so prefer short validities for anything scripted. Mint scoped
tokens from inside the pod:

```sh
kubectl -n attic exec deploy/attic -- \
  atticadm make-token -f /config/server.toml \
  --sub builder --validity 1y --pull general-programming --push general-programming
```

Rotating `ATTIC_SERVER_TOKEN_RS256_SECRET_BASE64` in
`secret/app/attic-secrets` invalidates **all** issued tokens, including the
admin token.

## Troubleshooting

- **`nix copy`/substitution says the path doesn't exist right after a
  push**: nix negative-caches missing narinfos; retry with `--refresh`.
- **HTTP 307 + "Could not resolve hostname" on NAR downloads**: the client
  can't resolve `rgw-fmt2.generalprogramming.org` — it must use a resolver
  that sees the public zone.
- **Pods crashlooping after a secret change**: check
  `kubectl -n attic logs deploy/attic`; DB migrations run in an
  initContainer, config parse errors print the offending TOML line.
- Full bootstrap runbook (bucket creation, cache creation, key extraction):
  [`argocd/apps/infra/attic/README.md`](../../argocd/apps/infra/attic/README.md).
