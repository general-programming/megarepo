# Cloudflare provider plumbing. The token comes from CLOUDFLARE_API_TOKEN,
# exported by scripts/cloudflare-creds.sh out of secret/infra/cloudflare-api --
# see that script for the permissions it needs and why terraform cannot own it.
provider "cloudflare" {
}

# The account id lives beside the state-bucket keypair. That keypair is
# account-wide (it can read terraform-state) and must never be handed to an
# application; only the id is reused here.
data "vault_kv_secret_v2" "cloudflare_r2" {
  mount = "secret"
  name  = "infra/cloudflare-r2"
}

locals {
  cloudflare_account_id = data.vault_kv_secret_v2.cloudflare_r2.data["account_id"]
  owo_me_zone_id        = "29ee6045cf256b02dbc8c5553a8807d0"
}
