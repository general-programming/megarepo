#!/bin/sh

# Create roles
vault write /auth/token/roles/nomad-cluster @nomad-cluster-role.json
vault write /auth/token/roles/vault-consul @vault-consul-role.json

# Create policies
vault policy write admins policy-admins.hcl
vault policy write vault-consul-tls-policy vault-consul-tls-policy.hcl
vault policy write nomad-tls-policy nomad-tls-policy.hcl
vault policy write nomad-server nomad-server-policy.hcl
vault policy write vault-consul-tls-policy vault-consul-tls-policy.hcl
