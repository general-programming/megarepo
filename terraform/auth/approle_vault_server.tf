# AppRole for the Vault server nodes themselves (fmt2-vault-00/01/02).
#
# These hosts run vault-agent purely to issue and renew their own listener
# certificate from pki_internal, replacing the annual manual HSM signing
# (the HSM-issued certs expired 2026-07-07 and the HSM is currently
# unreadable). vault-agent's pkiCert re-issues at 90% of the certificate
# lifetime and reloads Vault over SIGHUP; see nix/modules/vault-server.nix
# and docs/nix/secrets.md.
#
# Seeded onto machines by `just provision-vault` (nix/justfile). Deliberately
# NOT reusing `nixos-core`: that policy grants read on all of
# secret/data/infra/* - including the salt master private key - and the
# secret store's own nodes have no business holding it. This role can do
# exactly one thing.

resource "vault_policy" "vault_server" {
  name = "vault-server"

  policy = <<EOT
# Issue this node's own API listener certificate. The `genprog` role bounds
# what can be requested (generalprogramming.org subdomains, IP SANs, max
# one year); the certificate lifetime actually requested is 90d, kept short
# so the rotation path is exercised quarterly rather than annually.
path "pki_internal/issue/genprog" {
  capabilities = ["create", "update"]
}
EOT
}

resource "vault_approle_auth_backend_role" "vault_server" {
  backend        = "approle"
  role_name      = "vault-server"
  token_policies = ["default", vault_policy.vault_server.name]

  # fmt2 VLAN 5 only - these three nodes never move.
  secret_id_bound_cidrs = ["10.65.67.0/24"]
  token_bound_cidrs     = ["10.65.67.0/24"]
}
