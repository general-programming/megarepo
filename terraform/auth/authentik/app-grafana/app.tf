resource "random_uuid" "grafana_oauth2_client_secret_random" {
}

resource "random_password" "grafana_admin_password" {
  length  = 32
  special = true
}

resource "vault_generic_secret" "grafana_oauth" {
  path      = "secret/app/grafana/fmt2"
  data_json = jsonencode({
    oauth_client_id     = "grafana"
    oauth_client_secret = random_uuid.grafana_oauth2_client_secret_random.result
    admin-user          = "admin"
    admin-password      = random_password.grafana_admin_password.result
  })
}

resource "authentik_application" "grafana" {
  name              = "Grafana"
  slug              = "grafana"
  meta_icon         = "https://raw.githubusercontent.com/grafana/grafana/main/public/img/grafana_icon.svg"
  meta_launch_url   = "https://grafana.generalprogramming.org"
  protocol_provider = authentik_provider_oauth2.grafana.id
}

resource "authentik_provider_oauth2" "grafana" {
  name               = "Grafana"
  client_id          = vault_generic_secret.grafana_oauth.data.oauth_client_id
  client_secret      = vault_generic_secret.grafana_oauth.data.oauth_client_secret
  authorization_flow = data.authentik_flow.authorization_implicit_flow.id
  invalidation_flow  = data.authentik_flow.invalidation_flow.id
  signing_key        = data.authentik_certificate_key_pair.generated.id
  property_mappings  = data.authentik_property_mapping_provider_scope.default_scopes.ids
  allowed_redirect_uris = [
    {
      matching_mode = "strict",
      url           = "https://grafana.generalprogramming.org/login/generic_oauth"
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
  description = "OAuth2 Client Secret for the Grafana application"
  value       = vault_generic_secret.grafana_oauth.data.oauth_client_secret
  sensitive   = true
}
