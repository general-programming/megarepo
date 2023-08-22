# megarepo

Mega repo for General Programming business operations.

## Folders

* automation/ - Ansible + PyInfra playbooks for automating deployments and managements to core systems and disposable fleet.
* common/ - Files shared between automation playbooks and other scripts.
* infrastructure/ - Dockerfiles and Ansible playbooks to setup "important" services
* serverless/ - Various webhooks that handle events fired by other services.

## Points of Presence

We have many points of presence in our space.

* SEA420 - A single server colocated installed at Wobscale's (AS64241) colocation.
* SEA69 - @nepeat home network infrastructure.
* SEA4 - Komo Plaza, Seattle.
* FMT2 - Partnership with Lasagna, Ltd (AS208590) and many others with shared management of many resources.
* IAD2 - Oracle Cloud, Ashburn
* ORD1 - Oracle Cloud, Chicago
* Temporary Edge Sites
  * IAD1 - Virtual router running on Hetzner Cloud, America Region.
  * HEL1 - Virtual router running on Hetzner Cloud, Europe Region.
  * DFW1 - Virtual router running at a colocation facility in Dallas, Texas, USA.


## Vault setup

```sh
# Enable SSH secret engine and generate CA
vault secrets enable -path=ssh-client-signer ssh
vault write ssh-client-signer/config/ca generate_signing_key=true

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
vault write pki_internal/roles/genprog \
  allowed_domains="generalprogramming.org" \
  allow_subdomains=true \
  generate_lease=true \
  max_ttl="8766h"
```
