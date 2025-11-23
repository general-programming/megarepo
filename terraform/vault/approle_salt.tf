resource "vault_auth_backend" "salt_master" {
  type = "approle"
  path = "salt-master"
}


resource "vault_auth_backend" "salt_minions" {
  type = "approle"
  path = "salt-minions"
}


resource "vault_policy" "salt_master_approle" {
  name = "salt-master-approle-policy"

  policy = <<EOT
# This is the required Salt master policy for issuing AppRoles.
# Note that credentials should be issued from a distinct mount,
# not the one the Salt master AppRole is configured at.
# This separate mount is called `salt-minions` by default.

# List existing AppRoles
path "auth/salt-minions/role" {
  capabilities = ["list"]
}

# Manage AppRoles
# This enables the Salt Master to create roles with arbitrary policies.
path "auth/salt-minions/role/*" {
  capabilities = ["read", "create", "update", "delete"]
}

# Lookup mount accessor
path "sys/auth/salt-minions" {
  capabilities = ["read", "sudo"]
}

# Lookup entities by alias name (role-id) and alias mount accessor
path "identity/lookup/entity" {
  capabilities = ["create", "update"]
  allowed_parameters = {
    "alias_name" = []
    "alias_mount_accessor" = ["${vault_auth_backend.salt_minions.accessor}"]
  }
}

# Manage entities with name prefix salt_minion_
path "identity/entity/name/salt_minion_*" {
  capabilities = ["read", "create", "update", "delete"]
}

# Create entity aliases – you can restrict the mount_accessor.
# This might allow privilege escalation in case the Salt master
# is compromised and the attacker knows the entity ID of an
# entity with relevant policies attached - although you might
# have other problems at that point.
path "identity/entity-alias" {
  capabilities = ["create", "update"]
  allowed_parameters = {
    "id" = []
    "canonical_id" = []
    "mount_accessor" = ["${vault_auth_backend.salt_minions.accessor}"]
    "name" = []
  }
}
EOT
}

resource "vault_approle_auth_backend_role" "salt_master_approle_role" {
    backend        = vault_auth_backend.salt_master.path
    role_name      = "salt-master-role"
    token_policies = ["default", vault_policy.salt_master_approle.name]
}


resource "vault_policy" "salt_minions" {
  name = "saltstack/minions"
    policy = <<EOT
path "salt/kv/*" {
  capabilities = ["read"]
}

path "salt/data/roles/{{identity.entity.metadata.role}}" {
  capabilities = ["read"]
}
EOT
}
