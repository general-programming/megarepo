# Docker registry (Harbor, SEA1)

`https://hub.generalprogramming.org` — Harbor v2.15.2 on the SEA1 Talos
cluster. Image blobs live in an S3 bucket on SEA1's shared radosgw; metadata in
a dedicated CNPG postgres. Login is OIDC against Authentik; the local `admin`
account exists for bootstrap and API automation only.

Deployment lives in `argocd/apps/core-services/harbor/` (bootstrap runbook in
its README). The Authentik application and Harbor's OIDC config blob are
Terraform: `terraform/auth/authentik/app-harbor`.

## Using it

```sh
docker login hub.generalprogramming.org      # username + CLI secret, see below
docker push hub.generalprogramming.org/library/myimage:tag
```

OIDC users authenticate the CLI with a **CLI secret**, not their Authentik
password: log into the web UI once, then top-right menu → User Profile → CLI
secret. Kubernetes `imagePullSecrets` and robot accounts use the same registry
endpoint.

Any push bigger than a laptop upload is fine: the registry path has **no
Cloudflare in front** and a 30-minute per-request read timeout. A 150MB
incompressible layer pushed in ~15s from the LAN.

## Architecture

```
v4:  client ── 199.255.18.162/.163:443 ── vpn-leaf DNAT ──▶ node :443
v6:  client ── 2602:fa6d:10:ffff::110/111/112:443 ──────────▶ node :443
                                        (traefik-direct, hostNetwork)
                                                │ LE cert (cert-manager DNS-01)
                                                ▼
                              harbor-nginx ▶ core/portal/registry/jobservice
                                                │
                    harbor-db (CNPG) ◀──────────┼──────────▶ harbor-valkey
                                                ▼
                          s3://harbor on rook-ceph-rgw-shared.ceph.svc:7480
```

- **Ingress is `traefik-direct`**, a hostNetwork traefik instance — not the
  main one. Every cilium-translated path (LB VIP / NodePort / hostPort)
  mangles replies to non-cluster clients on this cluster; see
  `docs/bpf-masquerade-sea1.md` and the comments in
  `argocd/apps/infra/traefik/sea1/traefik-direct.yaml`. Routes opt in with
  `ingressClassName: traefik-direct`.
- **DNS**: 2 A records (the vpn-leaf public addresses, each DNATing to a
  different node) + 3 AAAA (node GUAs), unproxied, owned by
  `harbor/sea1/ingress-public.yaml`. The cloudflared path still exists as a
  manual fallback: re-point hub at `sea1-traefik-k8s.generalprogramming.org`,
  proxied.
- **Storage**: bucket `harbor` on the shared CephObjectStore
  (`argocd/apps/infra/ceph/sea1/cephobjectstore.yaml`), claimed by an
  ObjectBucketClaim. Registry redirects are disabled — clients cannot resolve
  the in-cluster rgw endpoint, so the registry streams blobs itself.
- **Config**: Harbor's auth settings come from `CONFIG_OVERWRITE_JSON`
  (Terraform → Vault → env), which makes every user-scope setting read-only in
  the UI and API. Change them in Terraform and restart core.

## Sharp edges

- **The `library` project is public**: anyone on the internet can pull from it
  by name (the catalog itself needs auth). Push anything non-public to a
  private project.
- hub is a direct, un-proxied endpoint — no Cloudflare WAF. Internet scanners
  already probe the admin login; OIDC-only auth and the vaulted admin password
  are the controls.
- The registry token signing key must be PKCS#1 and the chart-generated
  secrets are all pinned in Vault — regenerating any of them wrongly breaks
  `docker login` (500) or robot tokens. Runbook and traps:
  `argocd/apps/core-services/harbor/README.md`.
