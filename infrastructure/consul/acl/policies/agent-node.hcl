# Per-agent token policy. One policy + token PER AGENT -- substitute NODE_NAME
# with the agent's -node value, which must match exactly:
#
#   sea1-hv-0
#   sea1-hv-1.generalprogramming.org   <- note: FQDN, unlike its siblings
#   sea1-hv-2
#   sea1-core
#
# The k8s clients take their name from spec.nodeName (sea1-k8s-0, sea1-k8s-1,
# sea1-k8s-2, sea1-k8s-103-0); prefer letting the helm chart mint those --
# see k8s-client.hcl and the README.
#
# node:write on its own node is what anti-entropy needs to sync the node's
# registration and its health checks. Without it the agent logs
# "Coordinate update error" and its services silently fall out of the catalog.
#
# service_prefix read is needed so the agent can answer catalog/DNS queries it
# proxies on behalf of local callers.

node "NODE_NAME" {
  policy = "write"
}

service_prefix "" {
  policy = "read"
}

# The sea1 HVs and sea1-core both run with enable_local_script_checks = true.
# Script checks execute locally and register their results against the node
# above, so they need no extra grant -- but it does mean a stolen agent token
# is enough to define a check that runs a command on that host. Treat the agent
# tokens as host-level secrets and keep them 0600 root-owned.
