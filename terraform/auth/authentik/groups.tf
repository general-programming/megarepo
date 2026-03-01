resource "authentik_group" "users" {
  name = "users"
}

resource "authentik_group" "admins" {
  name         = "admins"
  parents      = [ authentik_group.users.id ]
  is_superuser = true
}

output "group_uuids" {
  description = "UUIDs of the Authentik groups"
  value = {
    users               = authentik_group.users.id
    admins              = authentik_group.admins.id
  }
}
