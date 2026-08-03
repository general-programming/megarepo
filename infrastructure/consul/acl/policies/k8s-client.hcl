# Fallback policy for the sea1 k8s consul clients, for the case where we mint
# their tokens by hand instead of letting the helm chart do it.
#
# Prefer the chart: global.acls.manageSystemACLs = true with
# global.acls.bootstrapToken pointed at a Secret holding the bootstrap token
# from the manual server bootstrap. The chart then creates and rotates a
# per-node client token itself and mounts it into the DaemonSet, which is
# strictly better than a shared static token checked into a Secret.
#
# Use this only if manageSystemACLs turns out not to work against external
# (non-chart-managed) servers -- the HVs are salt-managed, and the chart's
# server-acl-init job assumes it can reach them.
#
# node_prefix is used rather than a single node "" because one policy backs all
# four DaemonSet pods. That is weaker than per-node write: any one compromised
# client can deregister any node in the DC. Accept it only as a stopgap.

node_prefix "sea1-k8s-" {
  policy = "write"
}

service_prefix "" {
  policy = "read"
}
