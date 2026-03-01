resource "authentik_system_settings" "settings" {
  avatars                      = "gravatar,initials"
  event_retention              = "days=30"
  default_token_duration       = "hours=1"
  default_user_change_name     = true
  default_user_change_email    = true
  default_user_change_username = false # TODO: evaluate before enabling
}
