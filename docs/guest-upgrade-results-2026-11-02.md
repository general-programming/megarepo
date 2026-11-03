# Guest security upgrades — 2026-11-02

Last reconciled: 2026-11-02T23:28-08:00 (America/Los_Angeles).

13 guests completed an OS/package upgrade and/or a required reboot; 10 additional guests were already current. Physical hosts were excluded from package and boot changes. This is not a claim that every non-metal inventory record is remediated.

The [full current status and OS inventory](fleet-upgrade-status-2026-11-02.csv) contains all 121 tracking rows, including physical exclusions and aliases. Rows are not a distinct-machine count. Among the 62 NetBox VM records: 13 completed, 34 blocked, 3 excluded, 10 already current, 2 held.

## Completed guests

| Guest | Verified current OS | Running kernel | Service verification |
| --- | --- | --- | --- |
| 10gbe | Debian GNU/Linux forky/sid | 7.2.15+deb14-amd64 | Both CephFS mounts respond; four original containers, Caddy and Tailscale active |
| boom-vm-1 | Debian GNU/Linux 12.15 (bookworm) | 6.1.0-54-amd64 | Actual guest beer / tmpim-beer; CephFS responds; Docker 29.9.2 and original tunnel active |
| dma-colo-erin-00 | NixOS 26.11 (Zokor) | 6.18.60 | Secure Boot and encrypted root intact; NetBox, SQL, Redis, NetBird, web/backend and monitoring healthy |
| dma-colo-fmt2-00 | NixOS 26.11 (Zokor) | 6.18.60 | Secure Boot and encrypted root intact; web/backend and monitoring healthy |
| fmt2.moderateinfra.net | Debian GNU/Linux 13.7 (trixie) | 6.12.115+deb13.1-amd64 | GitLab Runner, Docker and Tailscale active; no failed units |
| freepbx | Debian GNU/Linux 12.15 (bookworm) | 6.1.0-54-amd64 | PBX, MariaDB, MongoDB, Redis, Apache and HAProxy active; no failed units; no call test |
| gameservers | Ubuntu 24.04.5 LTS | 6.8.0-147-generic | Minecraft TCP 25565 reachable; original workload restored |
| ipa-fremont-donut | Fedora Linux 44 (Server Edition) | 7.2.14-200.fc44.x86_64 | All IPA roles active; enabled link to sea1 reports successful replication |
| linear-pens.linuxgemini.space | Debian GNU/Linux 13.7 (trixie) | 6.12.115+deb13.1-rt-amd64 | RT kernel retained; PM2, Caddy and Tor active; no failed units |
| sea1-core | NixOS 26.11 (Zokor) | 6.18.60 | DNS, DHCPv4/v6, Consul, Salt, Vault agent and SSH active; ZFS healthy; no failed units |
| sea420-ipa-1 | Fedora Linux 44 (Server Edition) | 7.2.14-200.fc44.x86_64 | All IPA roles active; enabled outbound replication succeeded; no failed units |
| snakeroom-temp | Fedora Linux 44 (Server Edition) | 7.2.14-200.fc44.x86_64 | Caddy, Sidewinder, PostgreSQL 17 and Valkey active; no failed units |
| wob-app-cream | Debian GNU/Linux 13.7 (trixie) | 6.12.115+deb13.1-amd64 | Traefik running; both NFS mounts restored |

## Already current

- `sea1-ipa`: Fedora 44, kernel 7.2.14-200.fc44.x86_64; refreshed DNF metadata found no updates.
- `fmt2-vault-proxy` (LXC 120): Ubuntu 26.04.1 LTS; refreshed APT metadata found no updates; HAProxy configuration validates and HAProxy/Redis run. Its 7.0.0-3-pve kernel belongs to the excluded physical host.
- Eight fmt2 Talos VMs: Talos 1.14.5, kernel 6.18.58-talos, Kubernetes 1.37.2; all Ready. These versions were already installed. The three physical sea1 Talos nodes report the same baseline and were only read.

## Remaining blockers and exceptions

- **Access:** Vault 00/01/02 root access was successfully retried with the reconnected administrator agent, verifying Ubuntu 24.04.4 and running kernel 6.8.0-111. The agent then stopped responding and its socket disappeared. Their upgrade is now also blocked by unhealthy transit auto-unseal and invalid snapshots; see the retry record below. VPN guests are explicitly deferred to the user. Other guests retain their recorded access/inventory blockers.
- **Stopped guests:** 11 discovered Proxmox guests remain stopped. Retention and duplicate-service risks are unresolved; do not blindly boot old migrated DNS/DHCP or tenant machines. They must be patched before activation.
- **FreePBX:** 16 upgrades are kept back under vendor holds: Node 18 runtime, supporting libraries/modules and `sangoma-pbx17`. The installer holds the Node packages and PBX metapackage; related libraries are kept back by their dependencies. The exact same-major transaction was simulated, but not forced through the vendor hold; this is an explicit package-remediation exception. See the [vendor installer hold list](https://github.com/FreePBX/sng_freepbx_debian_install/blob/master/sng_freepbx_debian_install.sh).
- **Consul:** wob-app-cream retains 1.22.7 under `/etc/apt/preferences.d/fleet-consul-maintenance`; sea1-core retains server-compatible 2.0.4. A coordinated server-first migration remains required. Consul 1.22 reached EOL October 31; it is not fully remediated. See the [release notes](https://developer.hashicorp.com/consul/docs/release-notes/consul/v2_0_x) and [upgrade order](https://developer.hashicorp.com/consul/docs/upgrade).
- **NixOS continuous deployment:** comin/GitOps is disabled on sea1-core and both DMA guests to keep shared main from rolling their isolated security generations back. Reconcile the maintenance source and re-enable it. The GP change is saved on local branch `maintenance/guests-20261102`, commit `30ee6a78`; shared main was not published. Do not merge that branch unchanged: its shared lock also affects physical-host and Vault configurations. Scope the new input to approved guests before publishing. DMA source is retained on each guest under `/var/lib/fleet-security-20261102/source`.
- **IPA replication:** Fremont-to-sea420 was already disabled. Enabled sea1/Fremont links and sea420 outbound replication report successful updates; this does not establish full bidirectional topology health. ADTRUST and KRA roles are split between hosts. Native online backups explicitly cover individual replicas; none is presented as a complete single-archive cluster backup.
- **Inventory correction:** `79.110.170.3` is physical Dell PowerEdge R340 `fmt2-core` (NixOS 26.11, running 6.18.26). The old NetBox VM name `vrouter-nepwork` is stale. It was read and excluded. `boom-vm-1` and `tmpim-beer` refer to one guest; `10gbe`/`10gbps`/KubeVirt `tmpim` also refer to one guest.

## Recovery state and resolved maintenance issues

Traditional Linux guests retain root-only configuration and package-state backups in `/var/backups/fleet-upgrade-20261102`. FreePBX has a verified native SQL dump and Asterisk data archive. Gameservers has an application-consistent Minecraft data archive; its original command runs in the sysadmin user's `tmux -L fleet-upgrade` session `cabin`.

The DMA guests retain read-only Btrfs root and srv snapshots, a LUKS header backup, boot/signing-file archive and signed old/new UKI entries. Their TPM PCR policies were preserved (fmt2: 0+7; sea1: 7). Both booted 6.18.60 with Secure Boot enabled. sea1-core retains its old generation, a ZFS snapshot and a GC root for rollback. Its immutable machine-ID commit condition was corrected after boot, and final service checks show no failed units.

10gbe's old kernel hung during filesystem unmount after writers stopped. The guest was reset through KubeVirt, and both CephFS mounts were verified before writers resumed. nginx took Caddy's port while Caddy was gated; nginx was stopped to restore the original Caddy workload. No failed units remained. tmpim-beer's CephFS was cleanly unmounted before reboot and verified afterward; its `pre-security-20261102` Proxmox disk snapshot remains available.

wob-app-cream's HashiCorp signing key was replaced after checking the vendor's published fingerprint; the original keyring is backed up. linear-pens' unused Mailpile repository had a SHA-1 signature rejected by current APT. Neither Mailpile nor Moggie was installed; the source file was archived in the guest backup. Signature verification was retained.

Pre-existing SSSD socket failures remain on gameservers and wob-app-cream. Other completed guests passed their recorded service checks. The GP NetBox API responds after maintenance (4.5.4, two RQ workers). Service and listener checks do not establish end-to-end phone calls or every application workflow.

The temporary Vault public-key audit used read-only snapshot mappings and read-only debugfs access to public OS/SSH files. Its mappings and snapshots were removed; no guest disk writes or Vault service changes were made. Other snapshot backups described above are intentionally retained.

## Vault retry — 2026-11-03 UTC / November 2 Pacific

Root SSH succeeded on all three `fmt2-vault-00/01/02` guests using the existing administrator agent key, with strict host verification. All run Ubuntu 24.04.4, Linux 6.8.0-111-generic and Vault 2.0.1. Linux 6.8.0-147-generic is already installed. All three are healthy Raft voters; .26 is leader. No VPN guests were changed.

Preparation created root-only `/etc` archives (including the Vault configuration), package/hold inventories and APT metadata logs under `/var/backups/fleet-upgrade-20261102` on each guest. APT refresh failed strict signature verification for the rotated HashiCorp key; signature checks were not bypassed. No packages were installed, and no Vault service was restarted or VM rebooted.

The maintenance preconditions uncovered existing recovery failures:

- `/sys/seal-backend-status` reports transit unhealthy since **2026-10-08 20:09 UTC**, with `fully_wrapped=false`. This is separate from the currently unsealed, healthy Raft state. The configured external seal target is `oci-app-arm64-1.tail285d8f.ts.net:8200`. A credential-free TLS probe also fails hostname validation at that target; whether that explains the server-side seal failure is unverified without guest diagnostics.
- Two completed HTTP 200 snapshot downloads produced the same **27,516,573-byte truncated gzip stream**; archive and gzip checks failed. These files are **not valid recovery backups**. No restore was attempted.
- The Vault API listener leaf certificate observed on .26 expired **2026-10-23 11:40 UTC**. Its internal intermediate CA remains valid through June 2027. The trusted proxy frontend continues to serve requests. The existing PKI role permits renewing the leaf with IP SANs; deployment/reload still needs working guest access.
- After successful initial SSH/preparation, the forwarded agent stopped responding and its socket disappeared. An agent reconnect was requested.

Do not restart/reboot a Vault member until the external transit encrypt/decrypt path and a complete native snapshot are verified. Restore administrator SSH access, diagnose/fix the existing seal dependency, renew API certificates and the verified HashiCorp package key, then resume follower-first rolling maintenance with healthy membership checks between nodes. HashiCorp documents [seal-backend status](https://developer.hashicorp.com/vault/api-docs/system/seal-backend-status), [manual HA upgrades](https://developer.hashicorp.com/vault/docs/upgrade/vault-ha-upgrade), [TLS reload through SIGHUP](https://support.hashicorp.com/hc/en-us/articles/5767318985107-Vault-SIGHUP-Behavior) and the [current package-key fingerprint](https://www.hashicorp.com/en/trust/security).
