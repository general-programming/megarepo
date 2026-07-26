# Declarative Vault servers (fmt2-vault-0/1/2)

Vault now runs as three NixOS machines managed like the rest of the fleet
(`nix/machines/fmt2-vault-{0,1,2}/`, module `nix/modules/vault-server/`),
replacing the hand-provisioned VMs whose only record was the manual "Vault
setup" block in the repo `README.md`. Storage is **integrated Raft**;
unseal is **transit auto-unseal**; TLS is signed by the **offline Nitrokey
HSM** CA (`~/nocloud/hsm`).

The three nodes keep their original IPs — **10.65.67.24 / .25 / .26** — so
the existing `vault-proxy.catgirls.dev` front (which targets those IPs) and
every current client keep working untouched. The proxy/holepunch teardown
and tailnet-only hardening are a **separate, later phase** (see the end).

## Design invariants (don't break these)

- **Peers are addressed by IP, never DNS.** The HSM leaf certs
  (`bin/gen_vault`) carry `IP:<node>`, `IP:127.0.0.1`, `DNS:localhost`
  SANs only. `retry_join`, `api_addr`, and `cluster_addr` all use
  `https://10.65.67.N:8200|8201`.
- **These hosts never set `vaultAgent.enable`.** A Vault server can't fetch
  its own secrets through vault-agent (circular). The normal
  Vault-delivered tailscale key path (`gpTailscale`) therefore doesn't
  apply here either.
- **Raft data lives at `/var/lib/vault` and is persisted explicitly.**
  Impermanence rolls the root back to `zroot@blank` every boot; the module
  adds `/var/lib/vault` (0700 vault:vault) to
  `impermanence.extraPersistDirectories`. If that bind mount is ever
  missing, the node boots with an empty raft store.
- **The seal token is never in the Nix store.** The `seal "transit"` stanza
  lives in a seeded `/var/lib/vault/seal.hcl`, merged at runtime via
  `services.vault.extraSettingsPaths`.

## Prerequisites / confirm before executing

1. **Current storage backend.** On an existing VM:
   `grep -r storage /etc/vault.d/*.hcl`. If it's already **raft**, use the
   live one-at-a-time reinstall below (near-zero downtime). If it's
   **consul**, you cannot do it incrementally — see "Consul → Raft" at the
   bottom.
2. **Transit-unseal target.** Confirm where the transit engine that unseals
   these lives. If it is *not* one of these three nodes, cold-start is fine.
   If it is self-hosted on this cluster, a full-cluster cold start can't
   bootstrap — document/keep an external unseal path.
3. **Per-VM facts the machine configs stub with TODOs:** NIC name (configs
   match `en*`), the segment's default gateway, and single- vs dual-disk
   (`disk/zfs-single` vs `disk/zfs-mirror`). Fix these in
   `nix/machines/fmt2-vault-*/configuration.nix` before installing.

## Out-of-band material seeded per node

Nix references paths only; you seed the bytes after each reinstall (same
pattern as the vault-agent AppRole via `just provision`). Into
`/var/lib/vault/tls/` (create `0700 vault:vault`):

| File | Contents | Source |
| --- | --- | --- |
| `server.crt` | leaf + intermediate chain | `bin/gen_vault <IP>` output (`<IP>_combined`) |
| `server.key` | leaf private key (RSA-2048) | same |
| `ca.crt` | root + intermediate chain | HSM repo (`int_ca.crt` + root) |

And `/var/lib/vault/seal.hcl` (`0600 vault:vault`):

```hcl
seal "transit" {
  address     = "https://<transit-vault>:8200"
  token       = "<periodic-token-with-transit-encrypt/decrypt>"
  key_name    = "autounseal"
  mount_path  = "transit/"
  tls_ca_cert = "/var/lib/vault/tls/ca.crt"
}
```

> **Update `bin/gen_vault`'s deploy target.** It currently scp's to
> `/etc/vault.d/server.{crt,key}`, which impermanence does **not** persist.
> Point it at `/var/lib/vault/tls/server.{crt,key}` (and drop `ca.crt`
> alongside), matching the module defaults.

## In-place migration (current backend = raft)

Cluster tolerates one member down (2/3 quorum), so reinstall **one node at
a time**, never touching the next until the previous is back as a healthy
voter. Do the **canary (fmt2-vault-0) first** — it tracks `testing`.

Per node N (start with .24):

1. **Snapshot first** (every time): on a healthy node,
   `vault operator raft snapshot save premigration-$(date +%F).snap`. This
   is the only recovery if things go sideways — keep a copy off-cluster.
2. **Drop the old member:** `vault operator raft remove-peer <old-node-id>`
   (find IDs with `vault operator raft list-peers`).
3. **Reinstall the OS in place, same IP:** `nixos-anywhere` the machine
   config (`just` install recipe / the fleet's standard flow). Disko wipes
   the disk — fine, raft data resyncs from peers.
4. **Seed** the four files above (`tls/{server.crt,server.key,ca.crt}`,
   `seal.hcl`).
5. **Boot.** Vault comes up, `retry_join`s the surviving two, transit
   auto-unseals it, and raft streams a snapshot to it.
6. **Verify it's a voter** before moving on:
   `vault operator raft list-peers` shows N as `voter`, and
   `curl --cacert ca.crt https://10.65.67.N:8200/v1/sys/health` returns 200
   (initialized, unsealed, active/standby).
7. Repeat for .25, then .26.

No `vault operator init` anywhere — the data and its root of trust
(including `sops/keys/firstkey`) migrate with the raft store; you are only
replacing the OS under the same cluster.

### Post-migration verification (all engines intact)

```
vault secrets list                 # ssh-client-signer, pki, pki_internal, pki_nomad, sops
vault write ssh-client-signer/sign/... # sign a throwaway key
vault write pki_internal/issue/...     # issue a throwaway cert
sops -d <some file>                    # transit sops/keys/firstkey works
```

## comin rollout safety

- `services.vault` upstream sets `restartIfChanged = false` — comin
  activations pull new config/units but **never restart vault** (a restart
  would seal storage). Config/package changes take effect only on a
  deliberate `systemctl restart vault`, after which transit auto-unseals it.
- **fmt2-vault-0 tracks `testing`; fmt2-vault-1/2 track `main`.** Workflow
  for any `vault-server` change:
  1. Push to `testing` → fmt2-vault-0 activates.
  2. `systemctl restart vault` on fmt2-vault-0; confirm it rejoins
     (`raft list-peers`, `sys/health`).
  3. Merge to `main` → fmt2-vault-1/2 activate.
  4. Restart + verify them **one at a time**, waiting for a healthy voter
     between.
- Never reboot two nodes concurrently (Proxmox maintenance included): one
  sealed/absent node is fine, two breaks quorum.

## Footguns

- **Simultaneous cert expiry.** `gen_vault` certs are 365-day. Three certs
  issued the same day die together and take the listeners + raft joins with
  them. **Stagger the issue/expiry dates** across nodes and add a
  monitoring check on the cert `notAfter`. Renew via `just generate-vault`
  (re-run yearly).
- **sops-transit bootstrap circularity.** `.sops.yaml` decrypts against
  `sops/keys/firstkey` *inside this Vault*. If the cluster is ever wiped you
  can't decrypt the repo's sops files (incl. Talos secrets) to rebuild it.
  The raft snapshot is the backup of last resort — keep an encrypted copy
  off-infra. Consider adding a periodic `raft snapshot` timer to the module.
- **Port confusion.** The old proxy served the API on **8201**; here 8201 is
  the raft *cluster* port and 8200 is the API. Grep clients for `8201`.

## Later phase — tailnet cutover + proxy teardown

Only after the three are declaratively healthy on raft. In dependency-safe
order:

1. Join the three to the tailnet out-of-band (manual `tailscale up` or a
   seeded static authkey — **not** `gpTailscale`, which needs vaultAgent).
2. Narrow `networking.firewall` in the module from
   `allowedTCPPorts = [8200 8201]` to
   `interfaces.tailscale0.allowedTCPPorts`.
3. Flip clients off the proxy to the tailnet address, verifying tailnet
   reachability/CA trust at each step **before** the sops/Talos ones (those
   run at bootstrap and can re-create the holepunch problem if flipped too
   early):
   - `nix/modules/vault-agent.nix` (`address` default) + add CA trust
   - `nix/modules/salt-master/default.nix` (address default)
   - `nix/justfile` `VAULT_ADDR`
   - `argocd/apps/infra/vault/{fmt2,sea1}/values_vault.yaml`
     (`externalVaultAddr`, also move off plaintext http)
   - `.sops.yaml` (`hc_vault_transit_uri`)
   - `infrastructure/talos/{fmt2,sea1}/talsecret.sops.yaml` (`vault_address`)
4. Retire `nix/modules/holepunch.nix` + its call sites, the
   `secret/infra/holepunch` KV path, the `vault_holepunch` ansible cron,
   the punch service, and the proxy box.

## Consul → Raft (only if prereq #1 says consul)

Not incremental. Snapshot consul, take a maintenance window, stop the old
Vault servers, and on fmt2-vault-0 run
`vault operator migrate -config migrate.hcl` (`storage_source "consul"` →
`storage_destination "raft" { path = "/var/lib/vault" node_id =
"fmt2-vault-0" }`), unseal with the existing transit seal, then bring up
.25/.26 which `retry_join` .24. Everything else (verification, rollout,
teardown) is identical.
