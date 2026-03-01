# GitHub OAuth Source Setup for Authentik

This configuration sets up GitHub as an OAuth source for Authentik authentication.

## Prerequisites

You'll need to create a GitHub OAuth application first.

### Step 1: Create GitHub OAuth Application

1. Go to [GitHub Settings > Developer settings > OAuth Apps](https://github.com/settings/developers)
2. Click "New OAuth App"
3. Fill in the application details:
   - **Application name**: Authentik (or your preferred name)
   - **Homepage URL**: Your Authentik instance URL (e.g., `https://auth.example.com`)
   - **Authorization callback URL**: `https://your-authentik-instance/source/oauth/callback/github/`
     - Replace `your-authentik-instance` with your actual Authentik domain
4. Click "Register application"
5. You'll be shown your:
   - **Client ID** (consumer_key)
   - **Client Secret** (consumer_secret) - keep this secret!

### Step 2: Store Credentials in Vault (REQUIRED BEFORE TERRAFORM)

**⚠️ This step is a prerequisite and must be completed before running Terraform.**

The GitHub OAuth credentials must be pre-stored in Vault at `secret/app/authentik/github`. Terraform will read them from Vault but will not manage them to avoid storing secrets in flat files.

Store them using the Vault CLI:

```bash
vault kv put secret/app/authentik/github \
  client_id="your-github-client-id" \
  client_secret="your-github-client-secret"
```

Verify they were stored correctly:

```bash
vault kv get secret/app/authentik/github
```

**Note**: These credentials should be managed through your secret management process, not through Terraform variables or state files.

### Step 3: Deploy with Terraform

```bash
terraform plan
terraform apply
```

## Configuration Details

- **provider_type**: `github`
- **Authentication Flow**: Uses the default source authentication flow
- **Enrollment Flow**: Uses the default source enrollment flow
- **User Matching**: Uses email matching to link GitHub accounts to existing users
- **Credentials**: Client ID and Client Secret are sourced from Vault at `secret/app/authentik/github`

## Optional: Restrict to GitHub Organization

To restrict authentication to members of the `general-programming` GitHub organization:

1. After deploying this source, open the Authentik admin panel
2. Navigate to **Directory > Sources > GitHub**
3. Add a property mapping that checks GitHub organization membership
4. Or manually configure OIDC scopes to include `read:org` and add custom logic in your enrollment flow

For more details, consult the [Authentik OAuth Source Documentation](https://docs.goauthentik.io/users-sources/sources/protocols/oauth/)

## Troubleshooting

### Users not getting created/enrolled
- Ensure the default enrollment flow is properly configured
- Check that user property mappings are set up in Authentik

### Callback URL mismatch error
- Verify the authorization callback URL in your GitHub OAuth app matches your Authentik instance URL
- The callback URL should be exactly: `https://your-authentik-instance/source/oauth/callback/github/`

### Organization membership not enforced
- If you want to restrict to a specific organization, ensure `github_oauth_organization` is set in your terraform variables
- The user's GitHub account must be a public member of the specified organization

## References

- [Authentik Documentation - Sources](https://docs.goauthentik.io/docs/sources/)
- [Authentik Terraform Provider - source_oauth](https://registry.terraform.io/providers/goauthentik/authentik/latest/docs/resources/source_oauth)
- [GitHub OAuth Apps Documentation](https://docs.github.com/en/apps/oauth-apps/)
