# SSH client certificate authority.
#
# Consumed by bin/vssh, which signs an ephemeral key against
# ssh-client-signer/sign/administrator-role and hands the certificate to ssh.
# Trusted by the salt fleet (salt/state/sshd_config) and by NixOS hosts
# (nix/modules/ssh-ca). See docs/vssh.md.
#
# This replaces infrastructure/vault/ssh-admin.sh, whose heredoc contained
# typographic quotes and was never valid JSON — the live mount and role were
# created by hand. Import before the first apply so Terraform adopts them
# instead of trying to recreate:
#
#   terraform import vault_mount.ssh_client_signer ssh-client-signer
#   terraform import vault_ssh_secret_backend_role.administrator \
#       ssh-client-signer/roles/administrator-role
#
# The CA signing key is deliberately NOT managed here: generate_signing_key is
# a one-shot bootstrap, and letting Terraform own it means a destroy/recreate
# silently invalidates every issued certificate and locks the fleet out. Rotate
# by hand, then re-export the public half to nix/modules/ssh-ca/client-ca.pub.

resource "vault_mount" "ssh_client_signer" {
  path = "ssh-client-signer"
  type = "ssh"

  description = "SSH client CA; signs short-lived user certificates for vssh."

  lifecycle {
    prevent_destroy = true
  }
}

resource "vault_ssh_secret_backend_role" "administrator" {
  name     = "administrator-role"
  backend  = vault_mount.ssh_client_signer.path
  key_type = "ca"

  allow_user_certificates = true

  # `admin` is the only principal the fleet grants. On salt hosts it maps to
  # the admin user (NOPASSWD sudo); on NixOS hosts root's
  # AuthorizedPrincipalsFile lists it, so an admin cert lands on root.
  allowed_users = "admin"
  default_user  = "admin"

  # Short enough that a leaked certificate expires before it is useful, long
  # enough to finish a maintenance session. vssh re-signs automatically.
  # Written in seconds: Vault normalizes durations and the provider reads back
  # the canonical form, so "30m0s" would never settle.
  ttl = "1800"

  # No agent or port forwarding — an interactive shell is all vssh needs.
  allowed_extensions = ""
  default_extensions = {
    permit-pty = ""
  }
}
