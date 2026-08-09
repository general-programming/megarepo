# test

Intentionally empty.

The `test` ApplicationSet (`argocd/projects/test.yaml`) generates one Application
per **directory** under `argocd/apps/test/*`. With no subdirectories it generates
none, which is the desired state. This file exists only so the path itself stays
in the repo — it is a file, not a directory, so the generator does not match it.

`busyboxtest` used to live here: a `busybox:latest` Deployment that had been
running for 172 days in a namespace with no Pod Security Admission label and an
automounted default ServiceAccount token. Because SEA1 has no default-deny
NetworkPolicies, it could open a socket to every datastore in the cluster, the
kubelet, etcd, and the Talos API. It was removed in favour of the dev-shell paths
that already exist:

- `kubectl debug node/<node>` — preferred; nothing standing, scoped to one session
- `kubectl -n workpod exec -it ds/workpod -- bash` — the existing DaemonSet
- a Coder workspace, for anything longer-lived

If you add an app here, name the directory `test-*`: the `test` AppProject only
allows destinations matching `test-*`, and it has no `clusterResourceWhitelist`,
so test apps cannot create cluster-scoped resources.
