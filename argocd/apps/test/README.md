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
- `kubectl -n kube-system exec -it ds/workpod -- bash` — the existing DaemonSet
- a Coder workspace, for anything longer-lived

If you add an app here, remember that the `test` AppProject is wildcarded
(`clusterResourceWhitelist: '*'`, `destinations: '*/*'`), so anything in this
directory can create cluster-scoped resources in any namespace on any cluster.
