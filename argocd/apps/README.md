# Apps

Each app lives at `apps/<project>/<app>/`, where `<project>` matches an AppProject + ApplicationSet pair defined in `projects/`.

## Layout

```
apps/<project>/<app>/base/       # shared manifests (kustomize base)
apps/<project>/<app>/<cluster>/  # per-cluster overlay (fmt2, sea1, ...)
```

The ApplicationSets in `projects/` generate one Application per app directory per registered cluster, sourcing `apps/<project>/<app>/<cluster name>`. The cluster name is whatever the in-cluster was registered as on that datacenter's Argo instance (`argocd cluster set in-cluster --name <datacenter>`).

## Adding an app

1. Create `apps/<project>/<app>/base/` with a `kustomization.yaml` holding the shared manifests.
2. Create an overlay directory named after each cluster the app should run on, with a `kustomization.yaml` referencing `../base` plus any cluster-specific resources/patches.
3. The Application deploys into a namespace named after the app directory. Register the namespace in `apps/infra/namespace/base/` so it is tracked.

Note: every app directory generates an Application on **every** cluster. If an app has no overlay directory for a cluster, that cluster's Argo will show a broken Application for it. Add an overlay dir with an empty `kustomization.yaml` to silence it (`allowEmpty` is enabled everywhere).

## Secrets

Never commit plaintext secrets. Put the secret in Vault and reference it with a `VaultStaticSecret` resource (see any existing `secret.yaml` for the pattern).
