# Disabled: provider >= 2026.2 fails refreshing this resource because our
# server omits enterprise-gated fields (core_default_app_access, ...) from
# GET /admin/settings/ and the provider treats them as required.
# https://github.com/goauthentik/terraform-provider-authentik/issues/920
# The resource was `tofu state rm`'d; re-enable (and re-import) once fixed.
#
# resource "authentik_system_settings" "settings" {
#   avatars                      = "gravatar,initials"
#   event_retention              = "days=30"
#   default_token_duration       = "hours=1"
#   default_user_change_name     = true
#   default_user_change_email    = true
#   default_user_change_username = false # TODO: evaluate before enabling
# }
