# Fleet access validation and current OS versions

Checked November 1, 2026, from approximately 11:53 to 12:10 America/Los_Angeles. This is an access and version audit; it does not mark package upgrades complete.

All **96 NetBox records** and **25 additional discovery/configuration records** have a result in the [full CSV](fleet-access-os-audit-2026-11-01.csv). The 121 records include aliases, passive hardware, stopped guests and unverified configuration targets. Current OS/kernel versions are verified for **33 distinct running machines**. **21** have a verified root/sudo/guest-exec command path; **11** have authenticated Talos Version reads via a trusted PVE host. The remaining verified OS-only guest is visible through KubeVirt; its privileged guest login is not validated.

A reachable TCP listener is not an authenticated login. An OS read through a hypervisor is not a successful guest SSH login. A timeout does not establish that a machine is powered off. Inventory platform labels and SSH banners are not treated as verified OS versions.

## Important changes from the earlier plan

- All eight fmt2 Talos nodes now run Talos 1.14.5, Linux 6.18.58 and Kubernetes 1.37.2. Sea1 nodes 1 and 2 run Talos 1.14.5 / Linux 6.18.58; node0 remains 1.13.7 / 6.18.39. Sea1 Kubernetes is 1.36.2. These changes were already present when read; this audit performed no upgrade.
- `snakeroom-temp` is Fedora Linux 44, not its NetBox Debian/Ubuntu label. The Vault proxy CT120 runs Ubuntu 26.04.1 LTS and shares Proxmox kernel 7.0.0-3-pve.
- The current Proxmox guest `tmpim-beer` (VM153) owns 79.110.170.52, assigned to `boom-vm-1` in NetBox. Its current OS is Debian 12 with kernel 6.1.0-17-amd64. Reconcile a rename versus reused address before scheduling; do not count it as two current guests.
- `colo-komo` is the guest hostname of KubeVirt `dma-colo-erin-00`. Both DMA guests report NixOS 26.11 with kernel 6.18.59.
- `10gbe`, `10gbps` and KubeVirt `tmpim/tmpim` are one Debian forky/sid guest. `freeipa/freeipa` is `sea1-ipa`. Alias rows remain visible for reconciliation.

## Verified current versions

| Current machine | OS | Running kernel | Access established |
| --- | --- | --- | --- |
| 10gbe | Debian GNU/Linux forky/sid | 7.1.3+deb14-amd64 | Salt root command; SSH localadmin |
| boom-vm-1 | Debian GNU/Linux 12 (bookworm) | 6.1.0-17-amd64 | Proxmox guest-exec uid=0 |
| dma-colo-erin-00 | NixOS 26.11 (Zokor) | 6.18.59 | KubeVirt guest-agent OS read |
| dma-colo-fmt2-00 | NixOS 26.11 (Zokor) | 6.18.59 | Proxmox guest-exec uid=0 |
| fmt2-core-0 | Ubuntu 24.04.4 LTS | 6.8.0-111-generic | Salt root command; SSH localadmin |
| fmt2-hv-14-07-52-100 | Debian GNU/Linux 13 (trixie) | 7.0.0-3-pve | Salt root command; SSH localadmin |
| fmt2-hv-14-07-52-101 | Debian GNU/Linux 13 (trixie) | 7.0.0-3-pve | Salt root command; SSH localadmin |
| fmt2-hv-14-07-52-102 | Debian GNU/Linux 13 (trixie) | 7.0.0-3-pve | Salt root command; SSH localadmin |
| fmt2-hv-14-07-52-103 | Debian GNU/Linux 13 (trixie) | 7.0.0-3-pve | Salt root command; SSH localadmin |
| fmt2-hv-14-07-52-104 | Debian GNU/Linux 13 (trixie) | 7.0.0-3-pve | Salt root command; SSH localadmin |
| fmt2-k8s-cp-0 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-cp-1 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-cp-2 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-node-0 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-node-1 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-node-2 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-node-3 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-k8s-node-4 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| fmt2-vault-proxy | Ubuntu 26.04.1 LTS | 7.0.0-3-pve (shared host kernel) | Proxmox LXC root command |
| fmt2.moderateinfra.net | Debian GNU/Linux 13 (trixie) | 6.12.107+deb13-amd64 | Proxmox guest-exec uid=0 |
| freepbx | Debian GNU/Linux 12 (bookworm) | 6.1.0-51-amd64 | Salt root command; SSH localadmin |
| gameservers | Ubuntu 24.04.4 LTS | 6.8.0-124-generic | Salt root command; SSH localadmin |
| ipa-fremont-donut | Fedora Linux 44 (Server Edition) | 7.1.4-204.fc44.x86_64 | Salt root command; SSH localadmin |
| linear-pens.linuxgemini.space | Debian GNU/Linux 13 (trixie) | 6.12.107+deb13-rt-amd64 | Proxmox guest-exec uid=0 |
| sea1-core | NixOS 26.11 (Zokor) | 6.18.38 | SSH root |
| sea1-ipa | Fedora Linux 44 (Server Edition) | 7.2.13-200.fc44.x86_64 | Salt root command; SSH localadmin |
| sea1-k8s-0 | Talos (v1.13.7) | 6.18.39-talos | Talos authenticated API read |
| sea1-k8s-1 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| sea1-k8s-2 | Talos (v1.14.5) | 6.18.58-talos | Talos authenticated API read |
| sea420-desktop | NixOS 26.11 (Zokor) | 7.2.9 | Local sudo |
| sea420-ipa-1 | Fedora Linux 44 (Server Edition) | 7.0.9-205.fc44.x86_64 | Salt root command |
| snakeroom-temp | Fedora Linux 44 (Server Edition) | 7.0.9-205.fc44.x86_64 | Proxmox guest-exec uid=0 |
| wob-app-cream | Debian GNU/Linux 13 (trixie) | 6.12.94+deb13-amd64 | Salt root command; SSH localadmin |

NixOS release labels alone do not identify the pinned closure: the local desktop build is 26.11.20261015.5dc9e3b; sea1-core build is 26.11.20260902.9387b3f. These are running systems. The desktop 7.3.1 generation remains staged while the running kernel is 7.2.9. The five fmt2 PVE hosts report Debian 13; their live SSH os-release also reports DEBIAN_VERSION_FULL=13.4.

## Access paths and remaining blockers

- The five fmt2 PVE hosts accept the existing admin-principal SSH certificate as `localadmin`, with passwordless sudo verified. Salt root commands also work. Host keys were pinned from the authenticated Salt channel into a private temporary known_hosts file; existing SSH trust settings were not weakened.
- Direct SSH plus sudo/root works on `sea1-core`, `10gbe`, `freepbx`, `gameservers`, `ipa-fremont-donut`, `sea1-ipa`, `wob-app-cream` and physical Ubuntu `fmt2-core-0`. `sea420-ipa-1` accepts Salt root commands, but its tested direct SSH routes time out.
- Root guest-exec works for `snakeroom-temp`, `dma-colo-fmt2-00`, current guest `tmpim-beer`, `linear-pens.linuxgemini.space` and `fmt2.moderateinfra.net`; root pct exec works for Vault proxy CT120. SSH credentials are rejected on the first three despite verified host keys; the latter two tested IPv4 SSH paths time out. The local desktop has local sudo access, but rejects this SSH certificate for root.
- Kubernetes API reads work for both clusters. Direct Talos API traffic from this workstation times out. From authenticated PVE host100, each of the eleven nodes accepted an mTLS Version RPC with its existing site credentials, with certificate verification enabled. Credentials existed only in process memory on the trusted management host during that read. A successful read does not test an upgrade write.
- Vault VM00/01/02 SSH listeners respond, but their host keys are not yet trusted by this audit and their QEMU guest agents are not running. Their exact OS versions remain unknown; SSH banners are not used to infer an Ubuntu release. The repository NixOS configuration does not establish that migration happened.
- Other missing SSH fingerprints, unreachable management routes and missing addresses are listed per record below. All SEA69N management targets tested here timed out. Sea420 results vary: some global IPv6 paths work while private IPv4 paths do not.
- `fmt2-cor-r-140752-1` HTTPS responds with a self-signed certificate that fails verification; no password was sent. `fmt2-pdu-140752-1` fails SSH cipher negotiation with the current client. `ursula.oak.en0.io` has a reachable CIMC address, but that does not validate its host OS login.
- Eleven fmt2 guests are confirmed stopped. They were not started to collect versions. Planned/stock hardware, two Darwin configuration aliases, passive blanking plates and IP allocation placeholders have separate non-running/noncomputer outcomes.

## Result counts by inventory record

| Result | Records |
| --- | --- |
| SSH host trust missing | 26 |
| SSH legacy negotiation blocked | 1 |
| authenticated OS read only | 2 |
| authenticated Talos read verified | 11 |
| configuration alias only | 2 |
| excluded noncomputer | 5 |
| management path unreachable | 30 |
| no usable management address | 7 |
| not in service | 2 |
| privileged access verified | 24 |
| stopped guest | 11 |

Counts in this table include alias records; use canonical_machine in the CSV for distinct machines. OS unknown means this audit could not verify a current installed version; it does not substitute an inventory guess.

## Every inventory and discovery record

### Other

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| Alyx’s MacBook Air (2) | unknown | management path unreachable |
| chickennugget | unknown | management path unreachable |
| colo-komo → dma-colo-erin-00 | NixOS 26.11 (Zokor) / 6.18.59 | authenticated OS read only |
| dma-con-sw-0 | unknown | SSH host trust missing |
| L7VKHR95G6 (2) | unknown | management path unreachable |
| linuxgemini-home-router | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| localhost | unknown | management path unreachable |
| mx100-openwrt | unknown | management path unreachable |
| newPersonal | unknown | configuration alias only |
| oci-app-arm64-1 | unknown | SSH host trust missing |
| personal | unknown | configuration alias only |

### SEA69N

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| dorkhouse-wlc-ng-01 | unknown | management path unreachable |
| ptx1k-inventory-00 | unknown | not in service |
| sea69-acc-v-a-1 | unknown (inventory: EdgeOS) | management path unreachable |
| sea69-bootbox | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| sea69-cor-r-a-1 | unknown | management path unreachable |
| sea69-haas | unknown (inventory: HassOS) | management path unreachable |
| sea69-hv-puppy | unknown (inventory: Proxmox) | management path unreachable |
| sea69-wireless-a-1 | unknown | management path unreachable |

### fmt2

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| berkeley | unknown | stopped guest |
| bezosbox | unknown | stopped guest |
| boom-vm-1 | Debian GNU/Linux 12 (bookworm) / 6.1.0-17-amd64 | privileged access verified |
| chichester | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| dma-colo-fmt2-00 | NixOS 26.11 (Zokor) / 6.18.59 | privileged access verified |
| fmt2-acc-v-140752-1 | unknown (inventory: RouterOS) | SSH host trust missing |
| fmt2-adhoc-ips | not applicable | excluded noncomputer |
| fmt2-con-sw-140752-1 | unknown (inventory: Cisco IOS) | SSH host trust missing |
| fmt2-cor-r-140752-1 | unknown (inventory: Arista EOS) | SSH host trust missing |
| fmt2-core | unknown | no usable management address |
| fmt2-core-0 | Ubuntu 24.04.4 LTS / 6.8.0-111-generic | privileged access verified |
| fmt2-core-1 | unknown (inventory: Debian based / Ubuntu) | not in service |
| fmt2-hv-14-07-52-100 | Debian GNU/Linux 13 (trixie) / 7.0.0-3-pve | privileged access verified |
| fmt2-hv-14-07-52-101 | Debian GNU/Linux 13 (trixie) / 7.0.0-3-pve | privileged access verified |
| fmt2-hv-14-07-52-102 | Debian GNU/Linux 13 (trixie) / 7.0.0-3-pve | privileged access verified |
| fmt2-hv-14-07-52-103 | Debian GNU/Linux 13 (trixie) / 7.0.0-3-pve | privileged access verified |
| fmt2-hv-14-07-52-104 | Debian GNU/Linux 13 (trixie) / 7.0.0-3-pve | privileged access verified |
| fmt2-k8s-cp-0 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-cp-1 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-cp-2 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-node-0 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-node-1 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-node-2 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-node-3 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-k8s-node-4 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| fmt2-netboot | unknown (inventory: Debian based / Ubuntu) | stopped guest |
| fmt2-pdu-140752-1 | unknown | SSH legacy negotiation blocked |
| fmt2-pdu-140752-2 | unknown | management path unreachable |
| fmt2-vault-00 | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| fmt2-vault-01 | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| fmt2-vault-02 | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| fmt2-vault-proxy | Ubuntu 26.04.1 LTS / 7.0.0-3-pve (shared host kernel) | privileged access verified |
| fmt2-vpn-client-1 | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| fmt2-vpn-leaf-1 | unknown (inventory: VyOS) | SSH host trust missing |
| fmt2-vpn-leaf-2 | unknown (inventory: VyOS) | SSH host trust missing |
| fmt2-vpn-spine-1 | unknown (inventory: VyOS) | SSH host trust missing |
| fmt2-vpn-spine-2 | unknown (inventory: VyOS) | SSH host trust missing |
| fmt2.moderateinfra.net | Debian GNU/Linux 13 (trixie) / 6.12.107+deb13-amd64 | privileged access verified |
| garden | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| genny-cascadia | unknown | management path unreachable |
| ipa-fremont-donut | Fedora Linux 44 (Server Edition) / 7.1.4-204.fc44.x86_64 | privileged access verified |
| lakvm | unknown | no usable management address |
| laserjet | unknown (inventory: Proxmox) | SSH host trust missing |
| lavahut-runner3 | unknown | stopped guest |
| lavahut-runner4 | unknown | stopped guest |
| linear-pens.linuxgemini.space | Debian GNU/Linux 13 (trixie) / 6.12.107+deb13-rt-amd64 | privileged access verified |
| nepbox-one-x | unknown | stopped guest |
| oob-fmt2-shitpissfuckcuntcocksuckermotherfuckertits | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| opl-vods | unknown (inventory: Debian based / Ubuntu) | stopped guest |
| recursiveness | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| robust-wizard | unknown (inventory: Debian based / Ubuntu) | SSH host trust missing |
| router-portland | unknown (inventory: VyOS) | SSH host trust missing |
| shootingstar.oak.en0.io | unknown | SSH host trust missing |
| snakeroom-temp | Fedora Linux 44 (Server Edition) / 7.0.9-205.fc44.x86_64 | privileged access verified |
| tmpim-beer → boom-vm-1 | Debian GNU/Linux 12 (bookworm) / 6.1.0-17-amd64 | privileged access verified |
| urf | unknown | stopped guest |
| ursula.oak.en0.io | unknown (inventory: Proxmox) | SSH host trust missing |
| vapidapp | unknown | stopped guest |
| vrouter-nepwork | unknown (inventory: OPNSense) | SSH host trust missing |
| windowsserver-lasagnabranch | unknown | stopped guest |
| ziguana | unknown | stopped guest |

### sea1

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| 10gbe | Debian GNU/Linux forky/sid / 7.1.3+deb14-amd64 | privileged access verified |
| dma-colo-erin-00 | NixOS 26.11 (Zokor) / 6.18.59 | authenticated OS read only |
| freepbx | Debian GNU/Linux 12 (bookworm) / 6.1.0-51-amd64 | privileged access verified |
| r1.sea01.i.cofractal.net | unknown (inventory: Arista EOS) | no usable management address |
| r2.sea01.i.cofractal.net | unknown (inventory: Arista EOS) | no usable management address |
| SEA1-BLANK-SERVER | not applicable | excluded noncomputer |
| SEA1-CABLEVENT-39 | not applicable | excluded noncomputer |
| SEA1-CABLEVENT-40 | not applicable | excluded noncomputer |
| sea1-core | NixOS 26.11 (Zokor) / 6.18.38 | privileged access verified |
| sea1-ipa | Fedora Linux 44 (Server Edition) / 7.2.13-200.fc44.x86_64 | privileged access verified |
| sea1-k8s-0 | Talos (v1.13.7) / 6.18.39-talos | authenticated Talos read verified |
| sea1-k8s-1 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| sea1-k8s-2 | Talos (v1.14.5) / 6.18.58-talos | authenticated Talos read verified |
| sea1-vpn-leaf-1 | unknown (inventory: VyOS) | SSH host trust missing |
| sea1-vpn-leaf-2 | unknown (inventory: VyOS) | SSH host trust missing |
| sea1-vpn-spine-1 | unknown (inventory: VyOS) | SSH host trust missing |
| sea1-vpn-spine-2 | unknown (inventory: VyOS) | SSH host trust missing |
| tmpim/tmpim → 10gbe | Debian GNU/Linux forky/sid / 7.1.3+deb14-amd64 | privileged access verified |

### sea1?

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| 10gbps → 10gbe | Debian GNU/Linux forky/sid / 7.1.3+deb14-amd64 | privileged access verified |

### sea420

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| 20w14s | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| furry-slut | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| gameservers | Ubuntu 24.04.4 LTS / 6.8.0-124-generic | privileged access verified |
| ghast | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| james-shreds | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| minecraft-silver | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| not-furry-shit | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| opl-dump | unknown (inventory: Debian based / Ubuntu) | management path unreachable |
| opl-playground | unknown (inventory: Debian based / Ubuntu) | no usable management address |
| poppy | unknown (inventory: Alpine) | management path unreachable |
| reddit-video-proxy | unknown | management path unreachable |
| saffron | unknown (inventory: Alpine) | SSH host trust missing |
| sea420-acc-v-hv2 | unknown (inventory: RouterOS) | management path unreachable |
| sea420-core | unknown | management path unreachable |
| sea420-desktop | NixOS 26.11 (Zokor) / 7.2.9 | privileged access verified |
| sea420-hv-2 | unknown (inventory: Proxmox) | management path unreachable |
| sea420-hv-egg-irl | unknown (inventory: Proxmox) | SSH host trust missing |
| sea420-ipa-1 | Fedora Linux 44 (Server Edition) / 7.0.9-205.fc44.x86_64 | privileged access verified |
| utsuho lays eggs | unknown (inventory: Fedora Server) | no usable management address |
| wob-app-cream | Debian GNU/Linux 13 (trixie) / 6.12.94+deb13-amd64 | privileged access verified |
| zephy-traefik | unknown (inventory: Debian based / Ubuntu) | no usable management address |

### unassigned

| Record | Current OS and kernel | Access result |
| --- | --- | --- |
| adhoc-temp-ips | not applicable | excluded noncomputer |

## Next actions

Use the VM-first rollout with the updated facts. `gameservers` and `freepbx` have a verified privileged path; complete the package/backup preflight before their windows. Resolve unknown identities and SSH trust per guest without blocking unrelated verified guests. Preserve the working Salt and Proxmox guest-exec routes while repairing access. Do not remove SSH host verification or replace credentials merely to make the audit pass.

The fmt2 Talos baseline in the original plan is already present; reassess current vendor fixes before another OS reboot and preserve live Kubernetes 1.37.2. The remaining older sea1 node is physical. LXC kernel remediation remains assigned to its physical parent. No guest/host package installation, reboot, NetBox edit or access-control change was performed by this audit.
