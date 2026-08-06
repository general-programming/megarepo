# Per-agent token policy. One policy + token PER AGENT -- substitute NODE_NAME
# with the agent's -node value, which must match exactly.
#
# Only ONE agent in sea1 needs this now: `sea1-core`. The hypervisors that used
# to need it are gone, and the k8s server agents take their name from the pod
# hostname (sea1-server-0/1/2) and get chart-minted tokens via
# global.acls.manageSystemACLs -- do not hand-mint those.
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

# sea1-core runs with enable_local_script_checks = true.
# Script checks execute locally and register their results against the node
# above, so they need no extra grant -- but it does mean a stolen agent token
# is enough to define a check that runs a command on that host. Treat the agent
# tokens as host-level secrets and keep them 0600 root-owned.
