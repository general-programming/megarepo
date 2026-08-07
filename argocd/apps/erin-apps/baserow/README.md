# baserow

`base.owo.me` (frontend) / `base-api.owo.me` (backend) / `base-objects.owo.me`
(uploads). Upstream chart, pinned, with postgres, minio and caddy swapped out
for CNPG, R2 and traefik.

## Not in this directory

- **`terraform/auth/app_baserow.tf`** — the R2 bucket `baserow-objects`, its
  `base-objects.owo.me` custom domain, its CORS rules, the bucket-scoped R2
  token, and `secret/app/baserow`. A custom domain makes the bucket
  world-readable over that hostname, and uploads are served unsigned
  (`AWS_QUERYSTRING_AUTH=false`) — obscure object keys are the only thing
  gating them.

Every hostname here is one label under `owo.me` on purpose. Cloudflare's
Universal SSL stops at one level, this zone has no Advanced Certificate
Manager, and a proxied `api.base.owo.me` fails the TLS handshake at the edge
before it ever reaches traefik — no origin certificate can fix that.

## Chart traps

- The chart's migration Job is a Helm hook. ArgoCD runs PostSync hooks only
  after everything is healthy, and the backends are unhealthy until the schema
  exists — a fresh install deadlocks. The Job is disabled; `baserow-backend-wsgi`
  carries `MIGRATE_ON_STARTUP=true` instead, so it must stay at one replica.
- `generateJwtSecret` mints keys through a cluster `lookup` ArgoCD cannot do, so
  it would remint `SECRET_KEY` on every sync. Off; Vault holds both keys.
- Every Deployment gets a random `rollme` pod annotation per render, and the
  config resources are `pre-install` hooks. Both are patched out in
  `base/kustomization.yaml`; check them again after a chart bump.
