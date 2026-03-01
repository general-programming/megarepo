# Default Authentication Flow
# Converted from blueprints/default-authentication-flow.yaml

resource "authentik_flow" "default_authentication" {
  slug               = "default-authentication-flow"
  name               = "Welcome to authentik!"
  title              = "Welcome to authentik!"
  designation        = "authentication"
  authentication     = "none"
  compatibility_mode = false
  denied_action      = "message_continue"
  layout             = "stacked"
  policy_engine_mode = "any"
}

# ── Stages ────────────────────────────────────────────────────────────────────
# Note: The login stage is defined in login_flow.tf as
# authentik_stage_user_login.default_authentication_login

resource "authentik_stage_identification" "default_authentication_identification" {
  name                      = "default-authentication-identification"
  case_insensitive_matching = true
  user_fields               = ["username", "email"]
  pretend_user_exists       = true
  show_matched_user         = true
  show_source_labels        = false
  sources                   = [
    resource.authentik_source_oauth.github.uuid
  ]
}

resource "authentik_stage_password" "default_authentication_password" {
  name = "default-authentication-password"
  backends = [
    "authentik.core.auth.InbuiltBackend",
    "authentik.sources.kerberos.auth.KerberosBackend",
    "authentik.sources.ldap.auth.LDAPBackend",
    "authentik.core.auth.TokenBackend",
  ]
  failed_attempts_before_cancel = 5
  configure_flow                = "b301f36d-5efd-48e6-adcb-5dda1379d210"
}

resource "authentik_stage_authenticator_validate" "default_authentication_mfa_validation" {
  name                       = "default-authentication-mfa-validation"
  device_classes             = ["static", "totp", "webauthn", "duo", "sms", "email"]
  not_configured_action      = "skip"
  last_auth_threshold        = "seconds=0"
  webauthn_user_verification = "preferred"
}

# ── Expression Policies ───────────────────────────────────────────────────────

resource "authentik_policy_expression" "default_authentication_flow_password_stage" {
  name = "default-authentication-flow-password-stage"
  expression = <<-EOF
    flow_plan = request.context.get("flow_plan")
    if not flow_plan:
        return True
    # If the user does not have a backend attached to it, they haven't
    # been authenticated yet and we need the password stage
    return not hasattr(flow_plan.context.get("pending_user"), "backend")
  EOF
}

resource "authentik_policy_expression" "default_authentication_flow_authenticator_validate_stage" {
  name = "default-authentication-flow-authenticator-validate-stage"
  expression = <<-EOF
    flow_plan = request.context.get("flow_plan")
    if not flow_plan:
        return True
    # if the authentication method is webauthn (passwordless), then we skip the authenticator
    # validation stage by returning false (true will execute the stage)
    return not (flow_plan.context.get("auth_method") == "auth_webauthn_pwl")
  EOF
}

# ── Flow Stage Bindings ───────────────────────────────────────────────────────

resource "authentik_flow_stage_binding" "default_authentication_identification" {
  target                  = authentik_flow.default_authentication.uuid
  stage                   = authentik_stage_identification.default_authentication_identification.id
  order                   = 10
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

resource "authentik_flow_stage_binding" "default_authentication_password" {
  target                  = authentik_flow.default_authentication.uuid
  stage                   = authentik_stage_password.default_authentication_password.id
  order                   = 20
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

resource "authentik_flow_stage_binding" "default_authentication_mfa_validation" {
  target                  = authentik_flow.default_authentication.uuid
  stage                   = authentik_stage_authenticator_validate.default_authentication_mfa_validation.id
  order                   = 30
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

resource "authentik_flow_stage_binding" "default_authentication_login" {
  target                  = authentik_flow.default_authentication.uuid
  stage                   = authentik_stage_user_login.default_authentication_login.id
  order                   = 100
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

# ── Policy Bindings ───────────────────────────────────────────────────────────

resource "authentik_policy_binding" "default_authentication_password_policy" {
  target         = authentik_flow_stage_binding.default_authentication_password.id
  policy         = authentik_policy_expression.default_authentication_flow_password_stage.id
  order          = 10
  failure_result = true
  enabled        = true
  negate         = false
  timeout        = 30
}

resource "authentik_policy_binding" "default_authentication_mfa_policy" {
  target         = authentik_flow_stage_binding.default_authentication_mfa_validation.id
  policy         = authentik_policy_expression.default_authentication_flow_authenticator_validate_stage.id
  order          = 10
  failure_result = true
  enabled        = true
  negate         = false
  timeout        = 30
}
