# attic

Self-hosted Nix binary cache ([Attic](https://github.com/zhaofengli/attic)),
deployed to **fmt2 only** (next to its radosgw). All NixOS hosts — sea1
included — pull from it over the WAN via
`https://attic.owo.me`.

Components:

- `attic` Deployment — the API server.
- `attic-gc` Deployment — garbage collector, prunes per the
  `[garbage-collection]` settings in `server.toml` (12h interval, 3 month
  default retention).
- `attic-db` — CNPG Postgres for metadata.
- Chunk storage — S3 bucket `attic` on the fmt2 radosgw
  (`radosgw.service.consul`).

The Nix-side counterpart is `nix/modules/nix-cache.nix` (client substituter
config, all machines). Population is automated by
[`nix-cache-builder`](../nix-cache-builder/README.md) — a CronJob on sea1 that
runs `just build_cache` whenever `nix/` or `go/` moves on `main`. Running
`just build_cache` in `nix/` by hand still works and does the same thing.

## Bootstrap runbook

1. **Create the S3 user and bucket** (on an fmt2 ceph node):

   ```sh
   radosgw-admin user create --uid=attic --display-name="Attic binary cache"
   # note access_key / secret_key from the output, then create the bucket
   # with any S3 client, e.g.:
   AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... \
     aws s3 mb s3://attic --endpoint-url http://radosgw.service.consul
   ```

2. **Generate the RS256 token-signing secret**:

   ```sh
   openssl genrsa -traditional 4096 | base64 -w0
   ```

3. **Seed vault** (keys become env vars via `attic-secrets`):

   ```sh
   bao kv put secret/app/attic-secrets \
     AWS_ACCESS_KEY_ID=... \
     AWS_SECRET_ACCESS_KEY=... \
     ATTIC_SERVER_TOKEN_RS256_SECRET_BASE64=...
   ```

4. **Sync the app**, then create the cache. Mint a root token from inside
   the attic pod and use the attic client from anywhere:

   ```sh
   kubectl -n attic exec deploy/attic -- \
     atticadm make-token -f /config/server.toml \
     --sub admin --validity 10y \
     --pull '*' --push '*' --create-cache '*' --configure-cache '*' \
     --configure-cache-retention '*' --destroy-cache '*'

   attic login general-programming https://attic.owo.me/ <admin-token>
   attic cache create general-programming:general-programming
   # public so hosts can substitute without auth; pushing still needs a token
   attic cache configure general-programming:general-programming --public
   # skip paths already served by upstream caches (pushes drop anything
   # signed by these keys — keeps NVIDIA/CUDA closures out of our S3)
   attic cache configure general-programming:general-programming \
     --upstream-cache-key-name cache.nixos.org-1 \
     --upstream-cache-key-name cache.nixos-cuda.org
   attic cache info general-programming:general-programming   # -> "Public Key: general-programming:..."
   ```

5. **Enable the client on all machines**: set `gpNixCache.enable = true` in
   `nix/machines/base.nix` and paste the public key from step 4.

6. **Populate the cache.** `nix-cache-builder` does this automatically; to
   push by hand, run this from `nix/` with the login from step 4:

   ```sh
   just build_cache
   ```

## Notes

- The ingress is deliberately **not** Cloudflare-proxied: NAR chunks stream
  through the API server, and proxying would cap uploads and burn CF
  bandwidth for LAN-adjacent traffic.
- Pin `ghcr.io/zhaofengli/attic` to a digest once bootstrapped; upstream
  only publishes moving tags.
- If the cache is down, hosts fall back to cache.nixos.org / local builds
  (`connect-timeout = 5` + `fallback = true` in `nix-cache.nix`).
