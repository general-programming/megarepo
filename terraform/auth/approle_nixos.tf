# AppRole for core NixOS machines (fmt2-core, sea1-core, ...).
# Seeded onto machines by `just provision` / `just rekey` (nix/justfile).
#
# The `approle` auth mount itself predates terraform (it also hosts the
# ansible-managed `cluster-node` role), so it is referenced by path here
# instead of as a resource.

resource "vault_policy" "nixos_core" {
  name = "nixos-core"

  policy = <<EOT
# Infra-level secrets for core machines: NetBox API key and other
# credentials used for dynamic keying of node services.
path "secret/data/infra/*" {
  capabilities = ["read"]
}

path "secret/metadata/infra/*" {
  capabilities = ["read", "list"]
}
EOT
}

resource "vault_approle_auth_backend_role" "nixos_core" {
  backend        = "approle"
  role_name      = "nixos-core"
  token_policies = ["default", vault_policy.nixos_core.name]

  # Machines only ever talk to Vault from the internal network.
  # Intentionally broad for now; tighten per-site later.
  secret_id_bound_cidrs = ["10.0.0.0/8"]
  token_bound_cidrs     = ["10.0.0.0/8"]
}
