# argocd
This repo stores all the Argo configuration files for our Kubernetes clusters.

## Deploying ArgoCD
```
kubectl create namespace argocd
kubectl apply -k argocd/<datacenter>

# start port forward
kubectl port-forward svc/argocd-server -n argocd 8080:443

# add ssh key and add root
nix shell nixpkgs#argocd
argocd login localhost:8080 --username admin --password $(kubectl -n argocd get -o json secret argocd-initial-admin-secret | jq .data.password -r | base64 -d)
argocd repo add git@github.com:general-programming/megarepo.git --ssh-private-key-path fmt2-argo
argocd cluster set in-cluster --name <datacenter>
kubectl apply -f bootstrap.yaml
```

## get temp pass
```
kubectl -n argocd get -o json secret argocd-initial-admin-secret | jq .data.password -r | base64 -d
```

## Accessing ArgoCD
- SEA1: https://sea1-argo.generalprogramming.org
- FMT2: https://fmt2-argo.generalprogramming.org

TODO: Add real authenication too.

## Adding a namespace
Namespaces should be added in apps/infra/namespaces in order to keep track of all the namespaces that is sprayed in our cluster.
