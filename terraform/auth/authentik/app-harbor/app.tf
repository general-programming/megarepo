locals {
  harbor_client_id = "harbor"
  # Bare issuer, trailing slash included -- Harbor appends the well-known path.
  harbor_oidc_endpoint = "https://auth.generalprogramming.org/application/o/${local.harbor_client_id}/"
}

resource "random_uuid" "harbor_oauth2_client_secret" {
}

# Harbor's auth settings live in its database, not the chart. CONFIG_OVERWRITE_JSON
# is the supported way in -- core reads it at startup and writes it to the config
# table, which also makes every user-scope setting read-only in the UI and API.
resource "vault_generic_secret" "harbor_oidc" {
  path = "secret/app/harbor/oidc"

  data_json = jsonencode({
    oidc_client_id     = local.harbor_client_id
    oidc_client_secret = random_uuid.harbor_oauth2_client_secret.result

    CONFIG_OVERWRITE_JSON = jsonencode({
      auth_mode          = "oidc_auth"
      oidc_name          = "authentik"
      oidc_endpoint      = local.harbor_oidc_endpoint
      oidc_client_id     = local.harbor_client_id
      oidc_client_secret = random_uuid.harbor_oauth2_client_secret.result
      # offline_access is required; OIDC login fails without a refresh token.
      oidc_scope         = "openid,offline_access,email,profile"
      oidc_verify_cert   = true
      oidc_auto_onboard  = true
      oidc_user_claim    = "preferred_username"
    })
  })
}

resource "authentik_application" "harbor" {
  name              = "Harbor"
  slug              = local.harbor_client_id
  meta_icon         = "https://raw.githubusercontent.com/goharbor/website/main/static/img/logos/harbor-icon-color.png"
  meta_launch_url   = var.domain
  protocol_provider = authentik_provider_oauth2.harbor.id
}

resource "authentik_provider_oauth2" "harbor" {
  name               = "harbor"
  client_id          = local.harbor_client_id
  client_secret      = vault_generic_secret.harbor_oidc.data.oidc_client_secret
  authorization_flow = data.authentik_flow.authorization_implicit_flow.id
  invalidation_flow  = data.authentik_flow.invalidation_flow.id
  signing_key        = data.authentik_certificate_key_pair.generated.id
  property_mappings  = data.authentik_property_mapping_provider_scope.default_scopes.ids
  allowed_redirect_uris = [
    {
      matching_mode = "strict",
      url           = "${var.domain}/c/oidc/callback"
    }
  ]
}

data "authentik_property_mapping_provider_scope" "default_scopes" {
  managed_list = [
    "goauthentik.io/providers/oauth2/scope-openid",
    "goauthentik.io/providers/oauth2/scope-email",
    "goauthentik.io/providers/oauth2/scope-profile",
    "goauthentik.io/providers/oauth2/scope-offline_access",
  ]
}

data "authentik_flow" "authorization_implicit_flow" {
  slug = "default-provider-authorization-implicit-consent"
}

data "authentik_flow" "invalidation_flow" {
  slug = "default-provider-invalidation-flow"
}

data "authentik_certificate_key_pair" "generated" {
  name              = "authentik Self-signed Certificate"
  fetch_certificate = true
  fetch_key         = false
}

output "oauth2_client_secret" {
  description = "OAuth2 Client Secret for the Harbor application"
  value       = vault_generic_secret.harbor_oidc.data.oidc_client_secret
  sensitive   = true
}
