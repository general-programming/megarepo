# Salt stack: Vault secrets inventory

Every Vault path the Salt estate (masters, minions, provisioning) depends
on. Vault server: `http://10.65.67.27:8201` (`VAULT_ADDR` in `nix/justfile`
and the ansible roles).

## Auth backends and policies

| Path | Purpose | Consumers | Rotation |
| --- | --- | --- | --- |
| `auth/salt-master` (AppRole mount), role `salt-master-role` | The masters' own Vault identity; token policy `salt-master-approle-policy` lets them issue minion AppRoles | All salt masters (saltext-vault) | `terraform apply -replace=vault_approle_auth_backend_role_secret_id.salt_master` in `terraform/auth/`; vault-agent re-renders and restarts salt-master automatically on NixOS masters |
| `auth/salt-minions` (AppRole mount) | Per-minion AppRoles issued by the masters (saltext-vault `issue` config); entity metadata carries `minion-id` and `role` | Minions, via `peer_run` `vault.get_config` / `vault.generate_secret_id` through a master | Automatic (issued/renewed by the masters) |
| Policy `salt-master-approle-policy` | Grants AppRole/entity management on `auth/salt-minions` | Master tokens | Terraform-managed (`terraform/auth/approle_salt.tf`) |
| Policy `saltstack/minions` | Grants minions `read` on `salt/kv/*` and `salt/data/roles/{{identity.entity.metadata.role}}` | Minion tokens | Terraform-managed |
| `auth/approle/role/nixos-core`, policy `nixos-core` | vault-agent host identity on the NixOS masters (reads `secret/data/infra/*`) | `vault-agent-node` on NixOS core machines | `just rekey <machine>` (`nix/justfile`) |

## KV secrets

| Path | Fields | Purpose | Consumers | Rotation |
| --- | --- | --- | --- | --- |
| `secret/infra/salt-master/approle` | `role_id`, `secret_id` | Delivery of the salt-master AppRole creds to NixOS masters | vault-agent template → `/run/salt-master/master.d/vault.conf` | Terraform-minted (see above); rotation cascades automatically |
| `secret/infra/salt-master/pki` | `master_pem`, `master_pub` | Shared master keypair so all masters are interchangeable to minions | vault-agent template → seeded into `/var/lib/salt/pki/master/` at service start | Coordinated fleet event: rotate the KV pair, restart all masters, then every minion must drop its cached `minion_master.pub` and restart. Avoid unless compromised |
| `secret/infra/salt-master/autosign` | `key` | The `vault_key` autosign grain value the masters compare against | vault-agent template → `/run/salt-master/autosign_grains/vault_key` | The autosign file accepts one value per line: add the new key alongside the old, roll minion grains (`/etc/salt/grains`), then remove the old line |
| `secret/salt_grain_key` (legacy) | `key` | Same autosign key, original location; still read by ansible minion provisioning (`grains.j2`, `autosign_grain.j2`) | `automation/ansible/roles/setup_saltstack` | Keep in sync with `secret/infra/salt-master/autosign` until ansible is repointed, then delete |
| `salt/kv/dhcp_webhook` | `url` | Discord webhook for DHCP lease notifications | `salt/state/dhcp_server/files/dhcpd_discordhook.py.j2` (minion render) | Replace value; re-run highstate on `G@tags:dhcpserver` |
| `salt/kv/data/netbox_ro` | `secret` | Read-only NetBox API token for the custom `netbox` execution module | `salt/state/_modules/netbox.py` | Replace value; takes effect on next module call |
| `salt/data/roles/<role>` | per-role | Role-scoped minion secrets (readable only by minions whose entity metadata `role` matches) | Minion states via `vault.read_secret` | Per-secret |

## Other mounts

| Path | Purpose | Consumers | Rotation |
| --- | --- | --- | --- |
| `ssh-client-signer` (SSH CA mount) | SSH client CA public key baked into `TrustedUserCAKeys` | `salt/state/sshd_config/init.sls` via `vault_ssh.read_ca` | Rotating the CA invalidates all issued certs; re-run highstate fleet-wide afterwards |
| `secret/data/infra/netbox` | NetBox API key for the NixOS dnsmasq refresh (not salt, but shares the `nixos-core` policy with the master hosts) | `nix/modules/dns` | Replace value; hourly timer picks it up |

## Notes / known trade-offs

- The Terraform-minted `secret_id` for `salt-master-role` lives in the
  Terraform state (R2-backed) — accepted trade-off for hands-off rotation.
- The `nixos-core` policy grants **every** NixOS core machine read access
  to `secret/data/infra/salt-master/*`, including the master private key.
  Follow-up hardening: a dedicated AppRole/policy for salt-master hosts.
