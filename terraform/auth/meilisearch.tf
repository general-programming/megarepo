resource "random_password" "meilisearch_master_key" {
  length  = 64
  special = false
}

resource "vault_generic_secret" "meilisearch_sea1" {
  path = "secret/app/meilisearch/sea1"
  data_json = jsonencode({
    MEILI_MASTER_KEY = random_password.meilisearch_master_key.result
  })
}
