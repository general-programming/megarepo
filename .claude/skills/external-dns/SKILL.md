---
name: external-dns
description: Diagnose ExternalDNS reconciliation and storage endpoint DNS in fmt2 and sea1.
---

# ExternalDNS

Both clusters share `argocd/apps/infra/external-dns/base/external-dns-deployment.yaml`.
Resources use `external-dns.alpha.kubernetes.io/` annotations. ExternalDNS 0.22
changed its default prefix; preserve the explicit
`--annotation-prefix=external-dns.alpha.kubernetes.io/` until all annotations
are migrated together. Ignoring target annotations can publish an ingress VIP
instead of private storage addresses, or attempt an A record over an existing
CNAME (Cloudflare error 81054).

Before changing DNS reconciliation, render each overlay and run the controller
with its existing credentials and `--dry-run --once`. Review the proposed
provider changes, then verify the live deployment arguments and Argo sync.
Never print the Cloudflare token.

`rgw-fmt2.generalprogramming.org` must resolve to all three RGW addresses:
`10.65.67.100`, `10.65.67.101`, `10.65.67.102`. Check an external resolver as
well as cluster DNS. Each address should serve Ceph Object Gateway on port 80.
An Attic storage HTTP 500 can originate from this DNS target; verify S3 bucket
access with Attic's credentials in memory, then a cache upload and NAR download.
Restart Attic only if corrected DNS leaves stale HTTP connections.
