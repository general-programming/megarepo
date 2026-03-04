data "authentik_flow" "default_authorization_flow" {
  slug = "default-provider-authorization-implicit-consent"
}

data "authentik_flow" "default_provider_invalidation_flow" {
  slug = "default-provider-invalidation-flow"
}

data "authentik_certificate_key_pair" "generated" {
  name              = "authentik Self-signed Certificate"
  fetch_certificate = true
  fetch_key         = false
}

data "authentik_property_mapping_provider_saml" "default_scopes" {
  managed_list = [
    "goauthentik.io/providers/saml/email",
    "goauthentik.io/providers/saml/groups",
    "goauthentik.io/providers/saml/name",
    "goauthentik.io/providers/saml/upn",
    "goauthentik.io/providers/saml/uid",
    "goauthentik.io/providers/saml/username",
  ]
}

resource "authentik_provider_saml" "sentry" {
  name               = "sentry"
  authorization_flow = data.authentik_flow.default_authorization_flow.id
  acs_url            = "https://sentry.generalprogramming.org/saml/acs/sentry/"
  audience           = "https://sentry.generalprogramming.org/saml/metadata/sentry/"
  signing_kp        = data.authentik_certificate_key_pair.generated.id
  sp_binding         = "post"
  invalidation_flow = data.authentik_flow.default_provider_invalidation_flow.id

  property_mappings  = data.authentik_property_mapping_provider_saml.default_scopes.ids
}

resource "authentik_application" "sentry" {
  name              = "sentry"
  slug              = "sentry"
  protocol_provider = authentik_provider_saml.sentry.id
  meta_icon         = "https://sentry.io/_static/getsentry/images/favicon.png"
}
