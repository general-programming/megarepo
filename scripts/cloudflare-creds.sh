#!/usr/bin/env bash
# Fetch the Cloudflare API token terraform manages Cloudflare with, and print
# it as `export VAR=...` lines for a caller to `source <(...)`.
#
#   scripts/cloudflare-creds.sh
#
# Reads secret/infra/cloudflare-api -- a hand-minted account token, not managed
# by terraform, for the same reason as secret/infra/authentik: it is the
# credential terraform authenticates with, so terraform cannot own it.
#
# It needs, on the `general programming` account:
#   Account | Account API Tokens  | Edit   (to mint per-app service tokens)
#   Account | Workers R2 Storage  | Edit   (buckets, CORS, custom domains)
#   Zone    | DNS                 | Edit   (hostnames for R2 custom domains)
#
# Account API Tokens:Edit can mint any account-scoped token, so treat this as
# an account-admin credential regardless of how the rest is scoped.
#
# secret/infra/cloudflare-r2 is a different thing: the static S3 keypair for
# the terraform/pulumi state buckets (scripts/r2-creds.sh).
set -euo pipefail

command -v vault >/dev/null 2>&1 && VAULT_CMD=vault || VAULT_CMD=bao

api_token=$("$VAULT_CMD" kv get -field=api_token secret/infra/cloudflare-api)

echo "export CLOUDFLARE_API_TOKEN=$api_token"
