# For the victoriametrics / vmagent consul service-discovery token.
#
# argocd/apps/infra/victoriametrics/sea1/sea1_scrape.yaml points at
#   http://consul-http.consul.svc.cluster.local:8500
# and discovers the "node_exporter" service. Today that call is
# unauthenticated; under default_policy = "deny" it returns an empty target
# list and every node_exporter series silently stops -- no error, just a
# scrape config that discovers nothing. Verify targets are still present
# after the flip.
#
# agent:read is required by Prometheus-style consul SD (it calls /v1/agent/self
# to determine the datacenter) in addition to the catalog reads.

agent_prefix "" {
  policy = "read"
}

node_prefix "" {
  policy = "read"
}

service_prefix "" {
  policy = "read"
}
