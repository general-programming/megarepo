# vssh — SSH via the Vault SSH CA

`bin/vssh` is the preferred way to get a shell on a managed host. It mints a
throwaway ed25519 keypair, has the OpenBao SSH CA sign it, and hands the
certificate straight to `ssh`.

```sh
vssh fmt2-core                  # NixOS host: logs in as root
vssh localadmin@fmt2-hv-...     # salt host: log in as localadmin
vssh fmt2-core uptime           # trailing args go to ssh
vssh --status                   # inspect the cached cert
vssh --logout                   # drop the cache
```

**The login user and the certificate principal are different things.** The CA
only ever issues the `admin` principal; who you log in *as* is separate:

| Fleet | Log in as | Why |
| --- | --- | --- |
| NixOS | `root` (the default) | no `admin` account exists; root's `AuthorizedPrincipalsFile` lists `admin` |
| Salt | `localadmin` or `admin` | both accounts exist, both principals files list `admin` |

Deriving one from the other is wrong in both directions — `vssh root@host` must
still ask the CA for an `admin` certificate, because a `root` principal is
refused.

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
| `VSSH_PRINCIPAL` | `admin` | certificate principal |
| `VSSH_LOGIN` | `root` | default login user when none is given |

`BAO_ADDR`/`BAO_TOKEN` are used if set; otherwise `VAULT_ADDR`/`VAULT_TOKEN`,
then `~/.bao-token`, then `~/.vault-token`. Certificates are cached under
`$XDG_RUNTIME_DIR/vssh-$UID` and re-signed when under five minutes remain.

The role issues 30-minute certs carrying `permit-pty` only — no agent
forwarding, no port forwarding.

## Who trusts the CA

The role (`ssh-client-signer/roles/administrator-role`) issues exactly one
principal: `admin`.

- **NixOS hosts** trust it via `nix/modules/ssh-ca`, imported fleet-wide from
  `machines/base.nix`. These boxes have no `admin` user, so root's
  `AuthorizedPrincipalsFile` lists `admin` and an admin cert lands on root
  directly. Disable per-host with `sshCa.rootPrincipals = [ ]`.
- **Salt-managed hosts** trust it via `salt/state/sshd_config`
  (`TrustedUserCAKeys /etc/ssh/ssh_vault_ca.pub`,
  `AuthorizedPrincipalsFile /etc/ssh/auth_principals/%u`), with
  `salt/state/admin_user` creating the account, its NOPASSWD sudoers entry, and
  the matching principals file. Note the account name comes from the
  `admin_user:username` pillar and is **`localadmin`** in practice, not `admin`
  — `/etc/ssh/auth_principals/localadmin` is what contains `admin`. An `admin`
  account also exists on IPA-joined hosts, but that one comes from IPA, not
  Salt.

## Fixed 2026-07-27: what was actually broken

Two independent faults, neither of them in the client:

**1. NixOS: the directives never took effect.** `nix/modules/ssh-ca` first set
them via `services.openssh.extraConfig`, which renders *below* NixOS's own
`AuthorizedPrincipalsFile none`. OpenSSH keeps the first value for a keyword,
so the setting was visibly present in the file and completely inert. Fixed by
going through `settings`; the `sshd-ca-config` flake check now asserts the
effective value.

**2. Salt: `TrustedUserCAKeys` pointed at a file that does not exist.** Roughly
half the fleet had a stale `/etc/sshd/sshd_config.d/10-genprog.conf` naming
`/etc/ssh/vault_ssh_ca.pub`, while the CA is actually written to
`/etc/ssh/ssh_vault_ca.pub` (note the transposition). The template in this repo
was correct all along — those hosts simply had not run the state since the path
changed. Converged with `salt <targets> state.apply sshd_config`; all 16
connected minions now agree.

`fmt2-core-0` still has the stale path and cannot be fixed this way: it is not
an enrolled minion, so highstate never reaches it. It is being decommissioned
(`docs/fmt2/fmt2-core-0-retirement.md`), so it was left alone.

The third fault was mine and lived in vssh itself: it derived the certificate
principal from the login user, so `vssh root@host` asked the CA for a `root`
principal and was refused. Principal and login user are now independent.

If certificate auth breaks again, check in this order:

1. `sshd -T | grep -iE 'trusteduserca|authorizedprincipalsfile'` — the
   *effective* values, not what any one config file says.
2. `ls -l $(sshd -T | awk '/trustedusercakeys/ {print $2}')` — does the file
   sshd actually wants exist?
3. `cat /etc/ssh/auth_principals/<login-user>` — must contain `admin`.
4. `journalctl -u ssh -n 50` during an attempt. `Invalid user X` means the
   account does not exist, which is a login-user problem, not a cert problem.

Beware `Include` ordering: `/etc/ssh/sshd_config` pulls in
`/etc/ssh/sshd_config.d/` at line 13 and `/etc/sshd/sshd_config.d/` (the
salt-managed one) at line 125, so anything in the former wins.

## Declarative pieces

| What | Where |
| --- | --- |
| CA mount + `administrator-role` | `terraform/auth/ssh_ca.tf` |
| NixOS trust (`TrustedUserCAKeys`, root principals) | `nix/modules/ssh-ca` |
| Salt trust (`TrustedUserCAKeys`, `admin` user, principals) | `salt/state/sshd_config`, `salt/state/admin_user` |
| CA public key | `nix/modules/ssh-ca/client-ca.pub` |
| Client | `bin/vssh`, tests in `bin/tests/` |

The Terraform resources adopt the existing mount and role — import them before
the first apply, per the header comment in `ssh_ca.tf`. The CA *signing key* is
intentionally not Terraform-managed: a destroy/recreate would invalidate every
issued certificate and lock the fleet out.

This replaced `infrastructure/vault/ssh-admin.sh` and `ssh-role.json`, which
were removed. The shell script's heredoc used typographic quotes and was never
valid JSON, so the live role had been created by hand and drifted from both.

## Related

- `docs/salt/secrets.md` — CA rotation caveats.
