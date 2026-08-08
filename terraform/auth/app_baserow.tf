# Baserow (base.owo.me, sea1). secret/app/baserow is read by the
# vault-baserow / vault-baserow-db VaultStaticSecrets in
# argocd/apps/erin-apps/baserow/sea1.
#
# cloudflare_r2_custom_domain cannot be imported. If one is ever created out of
# band it has to be removed before terraform will take over, and the replacement
# serves 403 "no available server" for a minute or two while ownership goes from
# pending to active.

resource "cloudflare_r2_bucket" "baserow_objects" {
  account_id = local.cloudflare_account_id
  name       = "baserow-objects"
  location   = "wnam"
}

# A custom domain is what makes the bucket readable over base-objects.owo.me.
# Uploads are served unsigned, so object keys are the only thing gating them.
resource "cloudflare_r2_custom_domain" "baserow_objects" {
  account_id  = local.cloudflare_account_id
  bucket_name = cloudflare_r2_bucket.baserow_objects.name
  domain      = "base-objects.owo.me"
  zone_id     = local.owo_me_zone_id
  enabled     = true
  min_tls     = "1.2"
}

# The frontend fetches attachments with XHR (DOWNLOAD_FILE_VIA_XHR).
resource "cloudflare_r2_bucket_cors" "baserow_objects" {
  account_id  = local.cloudflare_account_id
  bucket_name = cloudflare_r2_bucket.baserow_objects.name

  rules = [{
    id = "baserow-frontend"
    allowed = {
      origins = ["https://base.owo.me"]
      methods = ["GET", "PUT", "HEAD"]
      headers = ["*"]
    }
    expose_headers  = ["Content-Length", "Content-Type", "ETag"]
    max_age_seconds = 3600
  }]
}

# If either of these returns nothing the permission group got renamed; list the
# real names with GET /accounts/{id}/tokens/permission_groups.
data "cloudflare_account_api_token_permission_groups_list" "r2_bucket_item_read" {
  account_id = local.cloudflare_account_id
  name       = "Workers%20R2%20Storage%20Bucket%20Item%20Read"
}

data "cloudflare_account_api_token_permission_groups_list" "r2_bucket_item_write" {
  account_id = local.cloudflare_account_id
  name       = "Workers%20R2%20Storage%20Bucket%20Item%20Write"
}

resource "cloudflare_account_token" "baserow_r2" {
  account_id = local.cloudflare_account_id
  name       = "baserow-objects"

  policies = [{
    effect = "allow"
    permission_groups = [
      { id = data.cloudflare_account_api_token_permission_groups_list.r2_bucket_item_read.result[0].id },
      { id = data.cloudflare_account_api_token_permission_groups_list.r2_bucket_item_write.result[0].id },
    ]
    # Objects in this one bucket, nothing else in the account.
    resources = jsonencode({
      "com.cloudflare.edge.r2.bucket.${local.cloudflare_account_id}_default_${cloudflare_r2_bucket.baserow_objects.name}" = "*"
    })
  }]
}

resource "random_password" "baserow_database" {
  length  = 40
  special = false
}

# Django's signing key. Rotating it invalidates sessions and password reset
# links; rotating the JWT key logs everyone out.
resource "random_password" "baserow_secret_key" {
  length  = 60
  special = false
}

resource "random_password" "baserow_jwt_signing_key" {
  length  = 60
  special = false
}

resource "random_password" "baserow_redis" {
  length  = 40
  special = false
}

resource "vault_kv_secret_v2" "baserow" {
  mount = "secret"
  name  = "app/baserow"

  data_json = jsonencode({
    DATABASE_PASSWORD       = random_password.baserow_database.result
    SECRET_KEY              = random_password.baserow_secret_key.result
    BASEROW_JWT_SIGNING_KEY = random_password.baserow_jwt_signing_key.result
    REDIS_PASSWORD          = random_password.baserow_redis.result

    # R2's S3 credentials are derived from the API token, not separate secrets:
    # the access key id is the token id and the secret is the SHA-256 of the
    # token value.
    AWS_ACCESS_KEY_ID     = cloudflare_account_token.baserow_r2.id
    AWS_SECRET_ACCESS_KEY = sha256(cloudflare_account_token.baserow_r2.value)
    AWS_S3_ENDPOINT_URL   = "https://${local.cloudflare_account_id}.r2.cloudflarestorage.com"
  })
}
