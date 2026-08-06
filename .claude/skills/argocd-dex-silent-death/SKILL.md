---
name: argocd-dex-silent-death
description: ArgoCD's dex container reports 1/1 Running with a dead dex process — SSO breaks and its :5558 metrics target refuses connections, with no restart and no event. Use when vmagent alerts on argocd-dex-server connection refused, when ArgoCD SSO login stops working, or before assuming a scrape target is misconfigured.
---

# argocd dex dies without dying

## The trap

`argocd-dex-server` runs `/shared/argocd-dex rundex`, a wrapper that renders
`dex.config` from `argocd-cm` and execs dex. If dex fails to initialize, the
wrapper stays alive. The container never exits, so:

- pod is `1/1 Running`, `Ready=True`, `Events: <none>`
- 5556/5557/5558 are all dead — nothing is listening
- SSO login is broken and nothing in k8s says so

Upstream (including `argocd-autopilot/manifests/ha`) ships the dex container
with **no probes at all**, so this can last for days.

The usual first symptom is vmagent: `dial tcp <podIP>:5558: connect: connection
refused`, job `argocd-dex-server`. That target is legitimate — the cluster-wide
`VMServiceScrape victoriametrics/services` scrapes any Service with a port
named `metrics`, and `argocd-dex-server` has one. Do not "fix" the scrape.

## Diagnose

```sh
kubectl logs -n argocd -l app.kubernetes.io/name=argocd-dex-server -c dex --tail=30
```

A dead dex ends with, and then logs nothing ever again:

```
"level":"ERROR","msg":"server: Failed to open connector","id":"authentik","err":"... 404 Not Found ..."
failed to initialize server: server: failed to open all connectors (1/1)
```

`dexserver.connector.failure.continue` (`DEX_CONTINUE_ON_CONNECTOR_FAILURE`)
does **not** save you: with a single connector, 1/1 failing is still "all
connectors failed" and dex aborts. It only tolerates partial failure.

Connector open happens **once, at startup, with no retry**. A momentary blip in
reaching the OIDC issuer — a CNI/ClusterIP outage, an authentik restart — kills
dex permanently.

Healthy dex logs three listeners:

```
"msg":"listening on","server":"telemetry","address":"0.0.0.0:5558"
"msg":"listening on","server":"https","address":"0.0.0.0:5556"
"msg":"listening on","server":"grpc","address":"0.0.0.0:5557"
```

Before blaming the connector, confirm the issuer is reachable *from inside the
cluster* (node DNS and pod DNS differ at sea1):

```sh
kubectl run -n argocd c --rm -i --restart=Never --image=curlimages/curl:latest -- \
  -sS -o /dev/null -w '%{http_code}\n' \
  "$(kubectl get secret -n argocd argocd-oidc -o jsonpath='{.data.oidc_issuer}' | base64 -d).well-known/openid-configuration"
```

## Recover

```sh
kubectl rollout restart deploy -n argocd argocd-dex-server
```

That is the only fix once dex has aborted — it will not self-heal.

## Permanent guard

`argocd/argocd/base/patch_dex_probes.yaml` (wired into
`argocd/argocd/base/kustomization.yaml`) adds probes to the dex container so
kubelet kills a dead dex instead of trusting the wrapper:

- `readinessProbe` → `GET :5558/healthz/ready`
- `livenessProbe` → `GET :5558/healthz/live`

Port 5558 is dex's telemetry server: always plain HTTP regardless of
`dexserver.disable.tls`, and it serves `/metrics`, `/healthz`, `/healthz/live`,
`/healthz/ready`. Probing 5556 would have to deal with dex's self-signed TLS.
Since 5558 is exactly the port the scrape needs, a green probe means a green
scrape target.
