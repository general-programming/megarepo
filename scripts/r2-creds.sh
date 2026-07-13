#!/usr/bin/env bash
# Fetch the static R2 credential from Vault/OpenBao and print it as
# `export VAR=...` lines for a caller to `source <(...)`.
#
#   scripts/r2-creds.sh
#
# Reads secret/infra/cloudflare-r2, matching the vault/bao lookup pattern
# used by nix/justfile. No minting/expiry: Cloudflare's R2 "Manage API
# Tokens" flow only issues a static S3-style access key id/secret (no
# Bearer-capable token comes with it, so the temp-access-credentials API
# isn't reachable from it) -- Vault access is the trust boundary here, same
# as every other secret in this repo.
set -euo pipefail

command -v vault >/dev/null 2>&1 && VAULT_CMD=vault || VAULT_CMD=bao

access_key_id=$("$VAULT_CMD" kv get -field=access_key_id secret/infra/cloudflare-r2)
secret_access_key=$("$VAULT_CMD" kv get -field=access_key_secret secret/infra/cloudflare-r2)
account_id=$("$VAULT_CMD" kv get -field=account_id secret/infra/cloudflare-r2)

echo "export AWS_ACCESS_KEY_ID=$access_key_id"
echo "export AWS_SECRET_ACCESS_KEY=$secret_access_key"
echo "export CLOUDFLARE_ACCOUNT_ID=$account_id"
