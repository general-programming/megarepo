data "authentik_flow" "default_source_authentication" {
  slug = "default-source-authentication"
}

data "authentik_flow" "default_source_enrollment" {
  slug = "default-source-enrollment"
}

data "vault_kv_secret_v2" "github_oauth" {
  mount = "secret"
  name  = "app/authentik/github"
}

resource "authentik_policy_expression" "github_org_check" {
  name       = "Github Org Check"

  expression = <<-EOF
    from authentik.sources.oauth.models import OAuthSource

    # Set this value
    accepted_org = "general-programming"

    # Ensure flow is only run during OAuth logins via GitHub
    if not isinstance(context['source'], OAuthSource) or context["source"].provider_type != "github":
        return True

    # Get the user-source connection object from the context, and get the access token
    connection = context["goauthentik.io/sources/connection"]
    access_token = connection.access_token

    # We also access the user info authentik already retrieved, to get the correct username
    github_username = context["oauth_userinfo"]

    # GitHub does not include organizations in the userinfo endpoint, so we have to call another URL
    orgs_response = requests.get(
        "https://api.github.com/user/orgs",
        auth=(github_username["login"], access_token),
        headers={
            "accept": "application/vnd.github.v3+json"
        }
    )
    orgs_response.raise_for_status()
    orgs = orgs_response.json()

    # `orgs` will be formatted like this
    # [
    #     {
    #         "login": "goauthentik",
    #         [...]
    #     }
    # ]
    user_matched = any(org['login'] == accepted_org for org in orgs)
    if not user_matched:
        ak_message(f"User is not member of {accepted_org}.")
    return user_matched
  EOF
}

resource "authentik_source_oauth" "github" {
  name                = "GitHub"
  slug                = "github"
  authentication_flow = data.authentik_flow.default_source_authentication.id
  enrollment_flow     = data.authentik_flow.default_source_enrollment.id
  provider_type       = "github"

  # Match users to emails as GitHub is outside our control. GitHub validates emails.
  user_matching_mode = "email_link"

  # GitHub OAuth app credentials from Vault
  # PREREQUISITE: GitHub OAuth credentials must be stored in Vault first
  # Run: vault kv put secret/app/authentik/github client_id="..." client_secret="..."
  # https://docs.github.com/en/apps/oauth-apps/building-oauth-apps
  oidc_jwks_url   = "https://token.actions.githubusercontent.com/.well-known/jwks"
  consumer_key    = data.vault_kv_secret_v2.github_oauth.data.client_id
  consumer_secret = data.vault_kv_secret_v2.github_oauth.data.client_secret
}

resource "authentik_policy_binding" "github_org_check" {
  target = authentik_source_oauth.github.uuid
  policy = authentik_policy_expression.github_org_check.id
  order = 0
}
