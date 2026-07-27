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

## Who trusts the CA

The role (`ssh-client-signer/roles/administrator-role`) issues exactly one
principal: `admin`. `vssh root@host` asks the CA for a `root` cert and is
refused — always connect as `admin`.

- **NixOS hosts** trust it via `nix/modules/ssh-ca`, imported fleet-wide from
  `machines/base.nix`. These boxes have no `admin` user, so root's
  `AuthorizedPrincipalsFile` lists `admin` and an admin cert lands on root
  directly. Disable per-host with `sshCa.rootPrincipals = [ ]`.
- **Salt-managed hosts** trust it via `salt/state/sshd_config`
  (`TrustedUserCAKeys /etc/ssh/ssh_vault_ca.pub`,
  `AuthorizedPrincipalsFile /etc/ssh/auth_principals/%u`), with
  `salt/state/admin_user` creating the `admin` user, its NOPASSWD sudoers entry,
  and `/etc/ssh/auth_principals/admin`.

## Known-broken: certificate auth on the salt fleet

As of 2026-07-27, `vssh admin@fmt2-core-0` fails with `Permission denied
(publickey,...)`. The client side is fine — `ssh -vvv` shows the certificate
loaded and offered, and the server rejecting it without comment:

```
debug1: Offering public key: .../id_ed25519-cert.pub ED25519-CERT ... explicit
debug1: Authentications that can continue: publickey,gssapi-keyex,...
```

All the pieces exist in the repo, so this is drift on the host rather than a
missing state. Diagnose on the box (needs the break-glass agent path):

1. `ls -l /etc/ssh/ssh_vault_ca.pub` — most likely culprit. The state that
   writes it is wrapped in `{% if has_vault_ssh %}` precisely because
   "vault_ssh disappears when a salt upgrade wipes salt-pip packages". When the
   module is gone the state is skipped silently, so `TrustedUserCAKeys` can
   point at a stale or absent file while highstate still reports success.
2. `sshd -T | grep -iE 'trusteduserca|authorizedprincipals'` — confirms the
   directives actually took effect. Note the managed fragment lives in
   `/etc/sshd/sshd_config.d/` (not `/etc/ssh/`), pulled in by an `Include` that
   `file.append` adds to the *end* of `/etc/ssh/sshd_config`; anything landing
   after a `Match` block would be scoped to that block.
3. `cat /etc/ssh/auth_principals/admin` — must contain `admin`.
4. `id admin` — the account must exist.
5. `journalctl -u ssh -n 50` during an attempt — sshd logs the real reason.

Until that is resolved, treat vssh as working against NixOS hosts and unproven
against the salt fleet.

## Related

- `infrastructure/vault/ssh-admin.sh` — original role bootstrap. Note the
  heredoc in it contains typographic quotes and is not valid JSON; the live
  role was created some other way. Do not re-run it as-is.
- `docs/salt/secrets.md` — CA rotation caveats.
