# NixOS machines: Vault secrets inventory

Every `secret/infra/*` KV path consumed by the NixOS modules via
vault-agent (`nix/modules/vault-agent.nix`, AppRole `nixos-core`, policy
grants read on `secret/data/infra/*`). Rendered secrets live under `/run`
(tmpfs) and never touch persistent disk. Salt-stack secrets are inventoried
separately in [../salt/secrets.md](../salt/secrets.md).

| Path | Fields | Consumer module | Rendered to | Rotation |
| --- | --- | --- | --- | --- |
| `secret/infra/netbox` | `api_key` | `dns` (NetBox DNS/DHCP refresh) | `/run/vault-agent/netbox.env` | Replace value; hourly timer picks it up |
| `secret/infra/salt-master/{approle,pki,autosign}` | see salt doc | `salt-master` | `/run/salt-master/*` | See [../salt/secrets.md](../salt/secrets.md) |
| `secret/infra/cloudflared/<hostname>` | `token` | `cloudflared` (per-host remotely-managed tunnel) | `/run/vault-agent/cloudflared.env` | Rotate the tunnel token in the CF dashboard, update KV; vault-agent restarts the tunnel |
| `secret/infra/tailscale` | `authkey` | `tailscale` (join key; per-host routing policy stays in machine configs) | `/run/vault-agent/tailscale-authkey` | Generate a new pre-auth key in the admin console, update KV. Only consumed while a node is logged out — existing nodes keep their state in `/var/lib/tailscale` |
| `secret/infra/dhcp-webhook` | `url` | `dhcp` (Discord lease webhook; URL contains the worker's secret path segment) | `/run/vault-agent/dhcp-webhook.env` (group `dnsmasq`, 0640) | Rotate the `HOOK_SECRET` on the `cfworker-dhcp-discord` worker, update KV **and** `salt/kv/dhcp_webhook` (legacy dhcpd hosts bake it at highstate) |
| `secret/infra/holepunch` | `key` | `holepunch` (hourly vault-proxy port knock) | `/run/vault-agent/holepunch.env` | Rotate on the punch server, update KV **and** `webscale-scrape/ansible_vars_server` (ansible cluster hosts use the same knock) |
| `secret/app/nix-builder-ssh` | ssh key | `nix-builder-client` | `/run/vault-agent/nix-builder-key` | Re-key the builder, update KV |

Host bootstrap: the vault-agent AppRole pair itself is seeded by
`just provision <machine>` / rotated by `just rekey <machine>`
(`nix/justfile`), landing in `/var/lib/vault-agent` (persisted).

Known trade-off: the `nixos-core` policy is shared by all NixOS core
machines, so any of them can read all of the above. Follow-up hardening:
per-role AppRoles (tracked in the salt doc too).
