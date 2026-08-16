# harbor

Container registry ([Harbor](https://goharbor.io)) at
`https://hub.generalprogramming.org`, deployed to **sea1 only**. Image blobs
live in an S3 bucket on sea1's shared radosgw; login is OIDC against Authentik.

Components:

- `harbor-core` / `harbor-portal` / `harbor-nginx` — API, UI, and the router in
  front of both.
- `harbor-registry` + `harbor-registryctl` — the distribution registry.
- `harbor-jobservice` — replication, GC, scan jobs.
- `harbor-trivy` — vulnerability scanner (its own PVC on `ceph-rbd-xfs`).
- `harbor-db` — CNPG Postgres, metadata only.
- `harbor-valkey` — job queue and cache.
- Blob storage — S3 bucket `harbor` on the sea1 rgw
  (`rook-ceph-rgw-shared.ceph.svc`), claimed by the OBC in `base/obc.yaml`.

The rgw itself is `argocd/apps/infra/ceph/sea1/cephobjectstore.yaml`. The
Authentik application and the OIDC config blob are Terraform, in
`terraform/auth/authentik/app-harbor`.

## Bootstrap runbook

Order matters: the rgw must exist before the OBC can bind, and the OIDC secret
must exist before core first starts or Harbor comes up in `db_auth` mode.

1. **Sync the rgw** (`infra/ceph` on sea1) and confirm it is up. Note the Talos
   change in `infrastructure/talos/sea1/talconfig.yaml` opening port 7480 —
   apply it per `.claude/skills/talos-ingress-firewall` before expecting
   anything to reach the gateway.

   ```sh
   kubectl -n ceph get cephobjectstore shared
   kubectl -n ceph get pods -l app=rook-ceph-rgw
   ```

2. **Generate the token signing keypair** and the shared secrets. The lengths
   are not advisory: `secretKey` must be exactly 16 characters and `CSRF_KEY`
   exactly 32, or core fails at runtime.

   ```sh
   openssl genrsa -out token.key 4096
   openssl req -new -x509 -key token.key -out token.crt -days 3650 \
     -subj "/CN=harbor-token-ca"
   bao kv put secret/app/harbor/token tls.key=@token.key tls.crt=@token.crt

   REGISTRY_PASSWD="$(openssl rand -hex 16)"
   bao kv put secret/app/harbor/config \
     HARBOR_ADMIN_PASSWORD="$(openssl rand -base64 24)" \
     secretKey="$(openssl rand -hex 8)" \
     secret="$(openssl rand -hex 8)" \
     CSRF_KEY="$(openssl rand -hex 16)" \
     JOBSERVICE_SECRET="$(openssl rand -hex 8)" \
     REGISTRY_HTTP_SECRET="$(openssl rand -hex 8)" \
     REGISTRY_PASSWD="$REGISTRY_PASSWD" \
     REGISTRY_HTPASSWD="$(htpasswd -nbB harbor_registry_user "$REGISTRY_PASSWD")"
   ```

3. **Create the Authentik application** and write the OIDC config to Vault:

   ```sh
   cd terraform/auth && tofu apply
   ```

   This writes `secret/app/harbor/oidc`, including the `CONFIG_OVERWRITE_JSON`
   blob harbor-core reads at startup.

4. **Sync the app once** to create the ObjectBucketClaim, then copy the
   credentials Rook generated into the config secret. Harbor needs them under
   its own key names, which is why this hop exists:

   ```sh
   kubectl -n harbor get secret harbor-bucket \
     -o jsonpath='{.data.AWS_ACCESS_KEY_ID}' | base64 -d
   kubectl -n harbor get secret harbor-bucket \
     -o jsonpath='{.data.AWS_SECRET_ACCESS_KEY}' | base64 -d

   bao kv patch secret/app/harbor/config \
     REGISTRY_STORAGE_S3_ACCESSKEY=... \
     REGISTRY_STORAGE_S3_SECRETKEY=...
   ```

   `kv patch`, **not** `kv put` — put replaces the whole secret and would drop
   `secretKey`, which decrypts every robot token Harbor has issued.

5. **Restart core and registry** to pick up the storage credentials, then log in
   at `https://hub.generalprogramming.org`. The Authentik login is the only one
   offered; the `admin` account still works via
   `https://hub.generalprogramming.org/account/sign-in?always_sign_in=true`.

## Notes

- **Harbor's settings are read-only in the UI.** `CONFIG_OVERWRITE_JSON` sets
  `readOnlyForAll` in core, so every user-scope setting — not just the OIDC
  ones — rejects updates from the UI and the API with "current config is init by
  env variable". Change them in `terraform/auth/authentik/app-harbor` and
  restart core.
- **The ingress is deliberately not Cloudflare-proxied.** CF caps request bodies
  at 100MB and image layers routinely exceed it; proxying returns 413 on push.
- **Registry redirects are disabled** (`disableredirect: true`). Otherwise the
  registry answers layer GETs with a 307 to a presigned URL on an in-cluster
  Service name that no external docker client can resolve.
- **valkey has no password**, because the chart can only read one through Helm's
  `lookup`, which is empty under Argo's render. The NetworkPolicy in
  `sea1/netpol.yaml` is what protects it.
- Every chart-generated secret is pinned to an existing secret. The chart falls
  back to `randAlphaNum` and a freshly minted token CA when its `lookup` misses,
  which under GitOps means new crypto on every sync. If you add a chart
  component, check whether it generates a secret and pin that too — verify with
  two renders and a diff.
