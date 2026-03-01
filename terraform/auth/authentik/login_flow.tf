resource "authentik_stage_user_login" "default_authentication_login" {
  name             = "default-authentication-login"
  geoip_binding    = "no_binding"
  network_binding  = "no_binding"
  remember_device  = "days=30"
  session_duration = "hours=20" # what google uses internally :)
}
