# Import blocks for adopting existing Authentik resources into OpenTofu state.
# Run `tofu plan` to review, then `tofu apply` to import.
# These blocks can be removed after successful import.

# ── Default Authentication Flow ───────────────────────────────────────────────

import {
  to = module.authentik.authentik_flow.default_authentication
  id = "default-authentication-flow"
}

# Stages

import {
  to = module.authentik.authentik_stage_identification.default_authentication_identification
  id = "a397d2fa-bd33-43a9-a9aa-d4bc9c3fb6bc"
}

import {
  to = module.authentik.authentik_stage_password.default_authentication_password
  id = "6ba2ac89-805f-4722-863a-ea4bfafb4d10"
}

import {
  to = module.authentik.authentik_stage_authenticator_validate.default_authentication_mfa_validation
  id = "45c7c182-47bd-45df-b48d-6fa80f5e56a9"
}

# Expression Policies

import {
  to = module.authentik.authentik_policy_expression.default_authentication_flow_password_stage
  id = "dd47bcf1-8347-49ed-8bc1-337ee6ee63f7"
}

import {
  to = module.authentik.authentik_policy_expression.default_authentication_flow_authenticator_validate_stage
  id = "1a2d2137-61d3-4777-89df-499002f95bdb"
}

# Flow Stage Bindings

import {
  to = module.authentik.authentik_flow_stage_binding.default_authentication_identification
  id = "1a44ec77-10d7-4282-a580-8d757c636974"
}

import {
  to = module.authentik.authentik_flow_stage_binding.default_authentication_password
  id = "e78a3ff3-eccb-426d-8dae-a20d546d8591"
}

import {
  to = module.authentik.authentik_flow_stage_binding.default_authentication_mfa_validation
  id = "8b961566-3fdb-4366-8083-bde60f551b8a"
}

import {
  to = module.authentik.authentik_flow_stage_binding.default_authentication_login
  id = "15a89808-53de-42a3-8b03-a16d334db14e"
}

# Policy Bindings

import {
  to = module.authentik.authentik_policy_binding.default_authentication_password_policy
  id = "10e9a178-b546-496f-9553-3887e111b459"
}

import {
  to = module.authentik.authentik_policy_binding.default_authentication_mfa_policy
  id = "64133c2d-e584-43a2-94a2-152fac6aa16a"
}

# ── Default Source Enrollment Flow ────────────────────────────────────────────

import {
  to = module.authentik.authentik_flow.default_source_enrollment
  id = "default-source-enrollment"
}

# Stages

import {
  to = module.authentik.authentik_stage_prompt_field.default_source_enrollment_username
  id = "5f486315-d761-4c9e-9a9f-bd92bfea6fcd"
}

import {
  to = module.authentik.authentik_stage_prompt.default_source_enrollment_prompt
  id = "95a36704-7114-4853-9264-86dc04ea69d7"
}

import {
  to = module.authentik.authentik_stage_user_write.default_source_enrollment_write
  id = "2ae5502c-96ce-4e63-88e0-d5c6266f551f"
}

import {
  to = module.authentik.authentik_stage_user_login.default_source_enrollment_login
  id = "0f523732-acff-45a3-a8a3-56ca2182ef2c"
}

# Expression Policies

import {
  to = module.authentik.authentik_policy_expression.default_source_enrollment_if_sso
  id = "6c533ed0-0103-48f2-8d22-01b13a45eae1"
}

import {
  to = module.authentik.authentik_policy_expression.default_source_enrollment_if_username
  id = "44be0ca7-5489-4715-9596-681bc5b4f5ea"
}

# Flow Stage Bindings

import {
  to = module.authentik.authentik_flow_stage_binding.default_source_enrollment_prompt
  id = "120e2045-f257-4bc5-a756-caf99164e985"
}

import {
  to = module.authentik.authentik_flow_stage_binding.default_source_enrollment_write
  id = "e8eedcfc-9bc4-460b-80bd-74298e75623b"
}

import {
  to = module.authentik.authentik_flow_stage_binding.default_source_enrollment_login
  id = "f66d9946-0630-48ec-9f4c-9b511ef2c689"
}

# Policy Bindings

import {
  to = module.authentik.authentik_policy_binding.default_source_enrollment_if_sso
  id = "3d85272b-ae1d-43f5-b0d0-1538a6578208"
}

import {
  to = module.authentik.authentik_policy_binding.default_source_enrollment_if_username
  id = "80ff4279-6459-46e7-81bc-65f6c0c46fe2"
}
