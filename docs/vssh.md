# vssh — SSH via the Vault SSH CA

`bin/vssh` is the preferred way to get a shell on a managed host. It mints a
throwaway ed25519 keypair, has the OpenBao SSH CA sign it, and hands the
certificate straight to `ssh`.

```sh
vssh admin@fmt2-core-0        # same argument shape as ssh
vssh fmt2-core-0 -- uptime    # defaults to the admin principal
vssh --status                 # inspect the cached cert
vssh --logout                 # drop the cache
```

`bin/` is already on `PATH` inside the devenv shell — `.envrc` appends both
`bin/` and `scripts/`, so `direnv allow` is all that is needed.

## Why not just use an agent

Nothing in this repo keeps a long-lived SSH private key on disk. Humans have a
forwarded agent, which works fine interactively but is useless to automation:
if the key was added with `ssh-add -c` the agent refuses to sign without a
confirmation prompt, and a non-interactive caller just hangs until it times
out. vssh depends on a Vault token instead, which agents already have.

The private key never outlives the certificate — each signing round generates a
fresh one — so the cache directory is not a standing credential.

## Configuration

| Env | Default | Meaning |
| --- | --- | --- |
| `VSSH_MOUNT` | `ssh-client-signer` | CA secrets mount |
| `VSSH_ROLE` | `administrator-role` | signing role |
| `VSSH_PRINCIPAL` | `admin` | principal / remote login |

`BAO_ADDR`/`BAO_TOKEN` are used if set; otherwise `VAULT_ADDR`/`VAULT_TOKEN`,
then `~/.bao-token`, then `~/.vault-token`. Certificates are cached under
`$XDG_RUNTIME_DIR/vssh-$UID` and re-signed when under five minutes remain.

The role issues 30-minute certs carrying `permit-pty` only — no agent
forwarding, no port forwarding.

## Current limitations

**Read this before assuming vssh can reach a given host.**

- The role (`ssh-client-signer/roles/administrator-role`) allows exactly one
  principal: `admin`. There is no `root` principal, so `vssh root@host` will be
  refused by the CA.
- Only the salt-managed fleet trusts the CA. `salt/state/sshd_config` installs
  `TrustedUserCAKeys /etc/ssh/ssh_vault_ca.pub` and sets
  `AuthorizedPrincipalsFile /etc/ssh/auth_principals/%u`.
- **NixOS hosts do not trust the CA at all.** `nix/machines/base.nix` sets only
  `users.users.root.openssh.authorizedKeys.keys`, with no `TrustedUserCAKeys`.
  That covers fmt2-core, sea1-core, sea420-core, and sea1-nix-builder — so vssh
  cannot currently reach the very boxes the DNS/DHCP work happens on.

Closing the gap needs two decisions that are deliberately not made here:
whether NixOS hosts should trust the client CA, and whether the CA should be
allowed to issue a `root` principal (versus provisioning an `admin` user with
sudo, matching the salt fleet). Both widen who can reach root on core
infrastructure and should be chosen explicitly, not inherited from a helper
script.

Until then the break-glass path is the operator's forwarded agent.

## Related

- `infrastructure/vault/ssh-admin.sh` — original role bootstrap. Note the
  heredoc in it contains typographic quotes and is not valid JSON; the live
  role was created some other way. Do not re-run it as-is.
- `docs/salt/secrets.md` — CA rotation caveats.
