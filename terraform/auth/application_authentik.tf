resource "random_password" "authentik_secret_key" {
  length  = 60
  special = false
}

resource "vault_kv_secret_v2" "authentik" {
  mount = "secret"
  name  = "app/authentik"

  data_json = jsonencode({
    secret_key = random_password.authentik_secret_key.result
  })
}
