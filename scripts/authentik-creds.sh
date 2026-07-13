#!/usr/bin/env bash
# Fetch the durable authentik API token from Vault/OpenBao and print it as
# `export VAR=...` lines for a caller to `source <(...)`.
#
#   scripts/authentik-creds.sh
#
# Reads secret/infra/authentik (a non-expiring "terraform-infra" token minted
# in the authentik UI, not managed by Terraform -- terraform/auth fully
# replaces secret/app/authentik on every apply, so a token stored there would
# get wiped).
set -euo pipefail

command -v vault >/dev/null 2>&1 && VAULT_CMD=vault || VAULT_CMD=bao

token=$("$VAULT_CMD" kv get -field=token secret/infra/authentik)
url=$("$VAULT_CMD" kv get -field=url secret/infra/authentik)

echo "export AUTHENTIK_TOKEN=$token"
echo "export AUTHENTIK_URL=$url"
