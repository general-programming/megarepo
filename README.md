# megarepo
Mega repo for General Programming business operations.

## Repos
* Infrastructure - Dockerfiles and Ansible playbooks to setup "important" services
* Bankwatch - Watches bank accounts and pushes out Discord notifications for new transactions and daily total balance updates.

## Vault setup
```sh

# Create PKI engines.
vault secrets enable -path=pki_internal pki
vault secrets enable -path=pki_nomad pki

# Set PKI engine variables.
vault secrets tune -max-lease-ttl=43800h pki_internal
vault secrets tune -max-lease-ttl=43800h pki_nomad

# Create CSRs.
vault write -format=json pki_nomad/intermediate/generate/internal common_name="General Programming Nomad Intermediate Authority" ttl="43800h" | jq -r '.data.csr' > pki_nomad.csr
vault write -format=json pki_internal/intermediate/generate/internal common_name="General Programming Internal Services Intermediate Authority" ttl="43800h" | jq -r '.data.csr' > pki_internal.csr

# Create an internal root CA and sign the CSRs if no HSM.
vault secrets enable pki
vault secrets tune -max-lease-ttl=87600h pki
vault write -format=json pki/root/sign-intermediate csr=@pki_nomad.csr format=pem_bundle ttl="43800h" | jq -r '.data.certificate' > pki_nomad.cert.pem
vault write -format=json pki/root/sign-intermediate csr=@pki_internal.csr format=pem_bundle ttl="43800h" | jq -r '.data.certificate' > pki_internal.cert.pem

# Install the certificates.
vault write pki_nomad/intermediate/set-signed certificate=@pki_nomad.cert.pem
vault write pki_internal/intermediate/set-signed certificate=@pki_internal.cert.pem

# Setup roles
vault write pki_nomad/roles/nomad-cluster allowed_domains=global.nomad allow_subdomains=true max_ttl=86400s require_cn=false generate_lease=true
vault write pki_internal/roles/consul-fmt2-vault \
  allowed_domains="fmt2-vault.consul" \
  allow_subdomains=true \
  generate_lease=true \
  max_ttl="8766h"
```
