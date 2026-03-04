resource "authentik_application" "argocd" {
  name              = "ArgoCD (${var.cluster})"
  slug              = var.slug
  meta_launch_url   = var.domain
  protocol_provider = authentik_provider_oauth2.argocd.id
}

resource "random_uuid" "grafana_oauth2_client_secret_random" {
}

resource "vault_generic_secret" "oidc_secret" {
  path      = "secret/app/argocd/${var.slug}/oidc"
  data_json = jsonencode({
    oidc_id     = var.slug
    oidc_secret = random_uuid.grafana_oauth2_client_secret_random.result
  })
}

resource "authentik_provider_oauth2" "argocd" {
  name               = var.slug
  client_id          = var.slug
  client_secret      = vault_generic_secret.oidc_secret.data.oidc_secret
  authorization_flow = data.authentik_flow.authorization_implicit_flow.id
  invalidation_flow  = data.authentik_flow.invalidation_flow.id
  signing_key        = data.authentik_certificate_key_pair.generated.id
  property_mappings  = data.authentik_property_mapping_provider_scope.default_scopes.ids
  allowed_redirect_uris = [
    {
      matching_mode = "strict",
      url           = "${var.domain}/auth/callback"
    },
    {
      matching_mode = "strict",
      url           = "${var.domain}/api/dex/callback"
    },
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
  description = "OAuth2 Client Secret for the ArgoCD application"
  value       = vault_generic_secret.oidc_secret.data.oidc_secret
  sensitive   = true
}
