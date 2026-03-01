# Default Source Enrollment Flow
# Converted from blueprints/default-source-enrollment.yaml

resource "authentik_flow" "default_source_enrollment" {
  slug               = "default-source-enrollment"
  name               = "Welcome to authentik! Please select a username."
  title              = "Welcome to authentik! Please select a username."
  designation        = "enrollment"
  authentication     = "none"
  compatibility_mode = false
  denied_action      = "message_continue"
  layout             = "stacked"
  policy_engine_mode = "any"
}

# ── Stages ────────────────────────────────────────────────────────────────────

resource "authentik_stage_prompt_field" "default_source_enrollment_username" {
  field_key   = "username"
  label       = "Username"
  name        = "default-source-enrollment-field-username"
  type        = "username"
  required    = true
  order       = 100
  placeholder = "Username"
  sub_text    = ""
}

resource "authentik_stage_prompt" "default_source_enrollment_prompt" {
  name   = "default-source-enrollment-prompt"
  fields = [authentik_stage_prompt_field.default_source_enrollment_username.id]
}

resource "authentik_stage_user_write" "default_source_enrollment_write" {
  name                     = "default-source-enrollment-write"
  create_users_as_inactive = false
  user_creation_mode       = "always_create"
  user_type                = "external"
}

resource "authentik_stage_user_login" "default_source_enrollment_login" {
  name             = "default-source-enrollment-login"
  remember_device  = "days=30"
  session_duration = "seconds=0"
}

# ── Expression Policies ───────────────────────────────────────────────────────

resource "authentik_policy_expression" "default_source_enrollment_if_sso" {
  name = "default-source-enrollment-if-sso"
  expression = <<-EOF
    # This policy ensures that this flow can only be used when the user
    # is in a SSO Flow (meaning they come from an external IdP)
    return ak_is_sso_flow
  EOF
}

resource "authentik_policy_expression" "default_source_enrollment_if_username" {
  name = "default-source-enrollment-if-username"
  expression = <<-EOF
    # Check if we've not been given a username by the external IdP
    # and trigger the enrollment flow
    return 'username' not in context.get('prompt_data', {})
  EOF
}

# ── Flow Stage Bindings ───────────────────────────────────────────────────────

resource "authentik_flow_stage_binding" "default_source_enrollment_prompt" {
  target                  = authentik_flow.default_source_enrollment.uuid
  stage                   = authentik_stage_prompt.default_source_enrollment_prompt.id
  order                   = 0
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

resource "authentik_flow_stage_binding" "default_source_enrollment_write" {
  target                  = authentik_flow.default_source_enrollment.uuid
  stage                   = authentik_stage_user_write.default_source_enrollment_write.id
  order                   = 1
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

resource "authentik_flow_stage_binding" "default_source_enrollment_login" {
  target                  = authentik_flow.default_source_enrollment.uuid
  stage                   = authentik_stage_user_login.default_source_enrollment_login.id
  order                   = 2
  evaluate_on_plan        = false
  invalid_response_action = "retry"
  policy_engine_mode      = "any"
  re_evaluate_policies    = true
}

# ── Policy Bindings ───────────────────────────────────────────────────────────

# SSO check bound to the flow itself
resource "authentik_policy_binding" "default_source_enrollment_if_sso" {
  target         = authentik_flow.default_source_enrollment.uuid
  policy         = authentik_policy_expression.default_source_enrollment_if_sso.id
  order          = 0
  failure_result = false
  enabled        = true
  negate         = false
  timeout        = 30
}

# Username check bound to the prompt stage binding
resource "authentik_policy_binding" "default_source_enrollment_if_username" {
  target         = authentik_flow_stage_binding.default_source_enrollment_prompt.id
  policy         = authentik_policy_expression.default_source_enrollment_if_username.id
  order          = 0
  failure_result = false
  enabled        = true
  negate         = false
  timeout        = 30
}
