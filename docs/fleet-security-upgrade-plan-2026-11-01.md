# Fleet kernel and package security upgrade plan

Snapshot: 2026-11-01, approximately 11:15–11:40 America/Los_Angeles. Status: proposed maintenance plan; live inventory reads completed. Fleet package updates, configuration changes and reboots require a scheduled rollout.

Runtime/access refresh: see the [current access and OS audit](fleet-access-os-audit-2026-11-01.md), checked through approximately 12:10. All fmt2 Talos nodes now run 1.14.5 / Kubernetes 1.37.2; sea1 nodes 1 and 2 also run Talos 1.14.5. Their earlier OS baseline work is already present. Use the refreshed checklist and audit for installed versions and access paths.

Start with **guest VMs**, accepting short application outages and guest reboots. Use `gameservers` as the first canary, then continue through the [VM order and service impact plan](vm-security-upgrade-impact-2026-11-01.md). Keep Vault, authentication replicas and routing peers one at a time; fmt2 Talos VMs require their own Kubernetes maintenance batch. Guest operating system maintenance does not require rebooting their physical hosts.

Then patch the physical hypervisors and sea1 Talos hosts. All five fmt2 Proxmox hosts still run `7.0.0-3-pve`, below the vendor's published fix for CVE-2026-99999, and the earlier boot-file inventory did not contain that fixed kernel. Guest updates leave that host remediation open. Prepare host packages and evacuation plans while the guest batches proceed so the VM-first order does not create an extended delay. Only `sea1-k8s-0` still reports Talos 1.13.7 / Linux 6.18.39 in the refreshed access audit. Keep Kubernetes minor upgrades and network migrations separate.

The [machine checklist](fleet-security-upgrade-inventory-2026-11-01.csv) contains all **96 NetBox records** plus **25 additional discovery or identity-reconciliation records**. Its 121 rows are tracking records, not a physical machine count. It records a target, wave, ownership scope and next action for every row, plus VM batch, expected interruption and guest-versus-host kernel action. Filter `scope` before scheduling: named tenants need coordination, and unassigned ownership needs confirmation.

## Coverage and inventory corrections

NetBox contains 34 devices (32 active, one planned, one inventory) and 62 VMs (59 active, three decommissioning). Three active devices are passive blanking plates; two VM records are IP allocation placeholders. Exclude those five from OS patch totals. Do not equate NetBox `active` with powered on.

The refreshed audit covers 11 Kubernetes/Talos nodes, 13 responding Salt minions, direct SSH, local facts, Proxmox/KubeVirt guest agents and Tailscale peers. Current OS/kernel facts cover 33 distinct running Linux machines. Package candidates and complete CVE applicability remain a per-host preflight task. NetBox platform labels alone do not establish installed OS versions.

| Inventory issue | Required correction before scheduling |
| --- | --- |
| fmt2 Proxmox reports 40 guests: 29 running, 11 stopped | Eleven guests have no matching NetBox name after linking `vault-proxy` to `fmt2-vault-proxy`. Verify that alias by VMID/address, then reconcile owners and retention. The CSV includes the eleven unmatched guests. |
| `router-portland` is decommissioning in NetBox but running, VMID 104, HA state started | Treat as active until its owner confirms dependencies and retirement. Do not power it off based on the inventory label. |
| `fmt2-netboot` and `opl-vods` are active in NetBox but stopped in Proxmox | Decide whether they are retained; patch retained images before bringing them back. |
| `fmt2-core-0` is labelled OPNSense | Live Salt minion `oob-backup` reports physical Ubuntu 24.04 / `6.8.0-111-generic`. Confirm identity and correct the platform. Keep separate from the `fmt2-core` NixOS configuration until deployment is verified. |
| Vault VMs are labelled Debian/Ubuntu; repo has NixOS `fmt2-vault-0/1/2` configurations | Match NetBox `00/01/02` to actual deployed OS before choosing apt or Nix. Configuration existence is not proof of a migration. |
| `sea420-core` configuration imports Proxmox LXC | Its kernel is the parent Proxmox kernel. A guest rebuild updates userland; it cannot independently remediate the host kernel. Confirm its current parent. |
| `10gbe`, Salt/Tailscale `10gbps` and KubeVirt `tmpim/tmpim`; KubeVirt `freeipa` and NetBox `sea1-ipa` | VM manifests establish these identities, including the matching 10GbE public IP and FreeIPA guest hostname. Track one upgrade per guest; the checklist marks the two additional 10GbE observations as aliases. |
| Proxmox `tmpim-beer` owns the address assigned to NetBox `boom-vm-1`; guest `colo-komo` is KubeVirt `dma-colo-erin-00` | Reconcile the former rename versus IP reuse before scheduling. Treat each current endpoint as one guest, and use the refreshed audit for its verified OS. |
| Personal configurations and tailnet-only peers are absent from NetBox | Reconcile `sea420-desktop`, `chickennugget`, two Darwin aliases, OCI/OpenWrt/other peers. Tailnet membership does not establish ownership; do not assume another person's Mac belongs in erin's rollout. |
| Four Salt targets did not respond | `sea1-hv-0/1/2` appear to be retired Proxmox names replaced by Talos; verify retirement. `sea420-core` needs access recovery/management reconciliation. |

Direct SSH verification of several fmt2 hosts stopped at unknown host keys; sea420 management addresses timed out. Verify fingerprints through trusted console/inventory and restore access before maintenance. Use [vssh](vssh.md) and retain host key checking. Vault access worked through the documented internal endpoint with hostname/SNI and TLS verification preserved; validate this access path before changing its host or proxy.

## Security targets

Use the latest supported vendor security release for each installed OS, including ordinary userland updates. Treat the following as prioritization evidence, not an exhaustive CVE list or a claim that every old version is exploitable. Vendor backports require exact package/changelog verification; a Debian/Ubuntu/Fedora ABI string cannot be compared directly with upstream stable version numbers.

Upstream baselines on this date are Linux **7.3.1**, LTS **6.18.60**, **6.12.115**, **6.6.161** and **6.1.191**. Linux **7.2.15 is EOL**; do not select 7.2 as a new general fleet baseline. A vendor-supported Proxmox 7.0 kernel follows the vendor's backport policy. [Kernel releases](https://www.kernel.org/).

| Priority evidence | Relevant upstream fix floor | Fleet implication |
| --- | --- | --- |
| [CVE-2026-99999](https://cveawg.mitre.org/api/cve/CVE-2026-99999), IPv4 checksum validation, used in the PwnEscape chain | 6.18.57 / 6.12.113 / 6.6.159 / 6.1.190 / 7.2.11 / 7.3 | Talos 6.18.39 and NixOS `sea1-core` 6.18.38 precede the upstream fix. Proxmox staff identify `proxmox-kernel-7.0.14-24-pve(-signed)` as containing the final fix. Install the latest supported vendor candidate, at least that floor. [Vendor statement](https://forum.proxmox.com/threads/cve-2026-99999-or-the-vercel-kvm-zero-day.187089/). |
| [CVE-2026-46300](https://cveawg.mitre.org/api/cve/CVE-2026-46300), shared fragment/page-cache corruption | 6.18.33 / 6.12.91 / 6.6.141 / 6.1.174 / 7.0.10 / 7.1 | Added to CISA's exploited catalog on October 28. The observed Talos and `sea1-core` 6.18 kernels already exceed this particular floor; they still need newer fixes. Verify distro backports elsewhere. [CISA catalog](https://github.com/cisagov/kev-data/blob/develop/known_exploited_vulnerabilities.json). |
| [CVE-2026-80844](https://cveawg.mitre.org/api/cve/CVE-2026-80844), IPv6 AH routing header validation | 6.18.49 / 6.12.108 / 6.6.156 / 6.1.187 / 7.2.3 / 7.3 | Check AH/XFRM exposure on VPN/networking hosts and older Talos nodes. |
| [CVE-2026-98231](https://cveawg.mitre.org/api/cve/CVE-2026-98231), XFRM state GC race | 6.18.54 / 6.12.112 / 6.6.158 / 7.2.8 / 7.3 | Check XFRM use and vendor fix status, especially VPN hosts. |
| [CVE-2026-72315](https://cveawg.mitre.org/api/cve/CVE-2026-72315), CIFS direct I/O/unmount | 6.18.54 / 6.12.112 / 7.1.5 / 7.2 | Inspect CIFS client use before assigning exposure; keep the broader security update baseline regardless. |

Talos **1.14.5** ships Linux **6.18.58** and is the target available on this date. Two fmt2 workers already run it. It exceeds the listed 6.18 fix floors but trails upstream 6.18.60; review vendor security advisories/SBOM and newer releases at execution time. **1.13.12 / 6.18.57** is a possible short bridge if a 1.14 migration blocker needs resolution, not the long-term destination. [1.14.5 release](https://github.com/siderolabs/talos/releases/tag/v1.14.5), [1.13.12 release](https://github.com/siderolabs/talos/releases/tag/v1.13.12).

Inspect actual loaded modules, build configuration and listeners for relevant attack surfaces: XFRM/AH/ESP, AF_ALG, nftables/ebtables, kTLS, CIFS and in-kernel SMB (`ksmbd`). Samba userland is distinct from `ksmbd`. If a vendor has not released a fix for an applicable disclosure, record the exception and disable or restrict the affected service through its supported configuration until patched; installing the newest kernel alone is not evidence that every disclosure is resolved.

## Observed operating systems

| Machines | Live version | Upgrade lane |
| --- | --- | --- |
| `fmt2-hv-14-07-52-100` through `104` | PVE manager 9.1.9, Debian 13, kernel `7.0.0-3-pve` | Wave 2, latest supported PVE packages/kernel; verify repositories and ZFS/DKMS compatibility. |
| `sea1-k8s-0` | Talos 1.13.7, kernel `6.18.39-talos`, kubelet 1.36.2 | Wave 3 physical node; Talos 1.14.5 baseline, preserve Kubernetes minor during OS maintenance. |
| `sea1-k8s-1/2` | Talos 1.14.5, kernel `6.18.58-talos`, kubelet 1.36.2 | Initial OS baseline already present; review newer applicable security releases before scheduling another reboot. |
| All eight fmt2 Talos VMs | Talos 1.14.5, kernel `6.18.58-talos`, kubelet 1.37.2 | V7 baseline already present; verify current vendor security fixes. Preserve live Kubernetes 1.37.2. |
| `sea1-core` | NixOS 26.11 build 20260902, kernel 6.18.38 | Wave 1 batch V5, updated infra closure and supported LTS; baseline 6.18.60. |
| `wob-app-cream` | Debian 13, `6.12.94+deb13-amd64` | Debian security packages plus Docker/NFS dependency checks. |
| `freepbx` | Debian 12, `6.1.0-51-amd64` | Debian security packages, telephony/vendor compatibility and call test. Verify actual kernel package's upstream revision. |
| `gameservers` | Ubuntu 24.04, `6.8.0-124-generic` | Supported Ubuntu kernel/security updates, save game state and verify services. |
| `fmt2-core-0` / `oob-backup` | Ubuntu 24.04, `6.8.0-111-generic` | Ubuntu security updates; protect OOB access. |
| `sea420-ipa-1`; `ipa-fremont-donut`; `sea1-ipa` | Fedora 44, respectively `7.0.9-205`, `7.1.4-204`, `7.2.13-200` | Current Fedora-supported kernel/security updates; one IPA replica at a time after replication checks. |
| Salt `10gbps` | Debian release grain unavailable, `7.1.3+deb14-amd64` | Identify actual suite and support policy; do not guess an apt distribution upgrade. |
| `sea420-desktop` | Running 7.2.9; generation 56 staged with 7.3.1 | Wave 5 reboot/verification. The staged ZFS 2.4.4 compatibility patches are local and experimental. |

All remaining machines have an explicit discovery/upgrade action in the checklist. Unknown platforms must pass discovery before a package command is selected.

## Maintenance waves

P1 means begin preparation immediately and take the earliest safe maintenance window. It is a scheduling priority, not a completed vulnerability assessment. Accept individual guest service outages; preserve quorum and recovery access. Resolve Kubernetes drain and storage blockers before their node batch rather than holding unrelated application VM updates behind those blockers. Independent sites can prepare in parallel, but finish and verify one layer of a cluster before changing its next layer.

| Wave | Machines and order | Entry and completion gates |
| --- | --- | --- |
| 0 | Per-guest ownership/access checks; decommissioning records; prepare later cluster maintenance | Verify console access, backup recovery, actual OS and supported packages for the next guest. Capture its service probes and dependencies. Resolve PDB/replica issues before V7 or physical-node maintenance. |
| 1 | Guest VMs and container userland: V1 apps, V2 data services, V3 IPA, V4 Vault/proxy, V5 site core, V6 VPN, V7 fmt2 Talos; V8 other guests as owner/role checks finish | Follow the [VM impact plan](vm-security-upgrade-impact-2026-11-01.md). Begin with `gameservers`; accept its service interruption, verify recovery, then continue. Replicated members stay serial; LXC kernel fixes remain assigned to the parent host. |
| 2 | Five fmt2 PVE hosts; then other hypervisors (`sea420-hv-2`, `sea420-hv-egg-irl`, `sea69-hv-puppy`, `laserjet`, `ursula.oak.en0.io`) after OS discovery | Check storage health and migrations. Evacuate and patch one host; reboot and validate before the next. Cluster quorum alone does not establish safe guest placement or local-storage migration. |
| 3 | `sea1-k8s-0/1/2`, one physical host at a time | Each is control plane plus Ceph OSD plus KubeVirt host. Require healthy etcd/Ceph, sufficient migration headroom, per-node PDB resolution and completed guest migrations. Wait for full recovery before the next. |
| 4 | Other physical Linux infrastructure, including Ubuntu `fmt2-core-0` / Salt `oob-backup`, after deployment checks | Guest infrastructure is already in wave 1. Protect OOB access and use supported physical-host packages; coordinate other owners. |
| 5 | Desktop, offline personal Linux machines and Macs | Desktop has a staged boot generation; others need identity/reachability and compatible security updates. macOS kernel updates come from Apple; Darwin flake updates only cover Nix packages. Home Assistant VM moves to V1. |
| 6 | Network/wireless/power firmware; stopped images; planned/stock equipment | Inventory firmware, consult exact model/vendor advisory and supported upgrade path, save configuration and verify console fallback. Patch retained images before restart; stock before activation. Escalate exposed vulnerable appliances to P1 instead of waiting for this wave. |

### Wave 2 Proxmox constraints

The fmt2 cluster `gp-fmt2-v2` has five votes, quorum three, and was quorate at capture. Only take **one host out at a time**. Current control-plane placement is CP0 on host100, CP1 on101 and CP2 on102. Vault00 is on100, Vault01 on101 and Vault02 on103. Proxy LXC120 is on101. Preserve this spread when migrating; do not gather an etcd or Vault quorum onto one host.

Host104 is a possible first canary only after checking storage/capacity, passthrough and network roles: it currently runs `dma-colo-fmt2-00`, VPN spine2 and Talos worker4. Two are already security-sensitive roles; it is not an empty host. Choose a different host if its guest migration/outage requirements are harder to satisfy.

Before installing, capture `pveversion -v`, exact installed/candidate kernel packages, apt sources and holds, bootloader/free ESP space, ZFS pool health, DKMS/module build status, VM/CT backups and migration feasibility. Use vendor PVE repositories and matching packages, not a generic Debian kernel replacement. Review the package transaction before execution. Keep a known-good boot entry; do not upgrade ZFS pool feature flags during patch maintenance. Inventory BIOS, BMC/iDRAC/CIMC, NIC and storage firmware too; handle required vendor updates in separate hardware windows. Stopped guests need no boot for inventory, but their retained images still need a patch-before-start gate. If a retained guest is Windows, use its supported Windows cumulative/security updates rather than a Linux package workflow.

### Waves 1 and 3 Talos constraints

Both site configs request Talos 1.14.5 and Kubernetes 1.37.2. Fmt2 now matches those versions; sea1 still runs Kubernetes 1.36.2 and one older Talos node. A blind regenerate/apply on sea1 would combine changes. Preserve each cluster's current Kubernetes minor during subsequent OS maintenance. Export and securely retain live machine configurations, diff intended OS changes, and use the exact current factory schematic/extensions and installer flavor for each node. Sea1 contains explicit non-Secure-Boot overrides; a common Secure Boot installer is unsafe there. Review multipath extension configuration migration if that extension is installed. Talos 1.14 supports Kubernetes 1.36. [Support matrix](https://docs.siderolabs.com/talos/v1.14/getting-started/support-matrix), [upgrade procedure](https://docs.siderolabs.com/talos/v1.14/configure-your-talos-cluster/lifecycle-management/upgrading-talos).

Take a fresh etcd snapshot and verify recovery access. Refresh node, pod, PDB and storage health immediately before each node. For sea1, require Ceph quorum and sufficient replicas, no degraded/undersized PGs, healthy monitors and tested VM migration. If maintenance needs `noout`, make it explicitly time-bounded and clear it afterward; do not suppress safety checks or force drain. Worker/node2 having fewer VMs does not make its Ceph role safe to interrupt automatically.

Seventeen **nonempty** PDBs allowed zero voluntary disruptions at the initial workload capture, before the later version refresh. Re-read these before maintenance. These are potential blockers on nodes hosting matching pods; primary database failover or recovery of replicas may make them drainable. Inspect placement and the database operator's supported failover procedure before scheduling each node.

| Cluster | PDBs allowing zero disruptions |
| --- | --- |
| fmt2, 3 | `attic/attic-db-primary`; `loki/loki-backend`; `loki/loki-write`. Both Loki sets have only 2 healthy of 3 expected pods and require 2 healthy. Restore redundancy before drain. |
| sea1, 14 | `akvorado/akvorado-clickhouse-0`, `akvorado/akvorado-keeper`; `authentik/authentik-db-primary`; `baserow/baserow-db-primary`; `coder/coder-db-primary`; `docspell/docspell-db-primary`; `erin-matrix/matrix-db-primary`, `erin-matrix/matrix-mas-db-primary`; `erin-scraper-projects/scraperdb-primary`; `harbor/harbor-db-primary`; `mastodon-coolmathgames/mastodon-db-primary`; `netbox/netbox-db-primary`; `shared-db/shared-db-primary`, `shared-db/shared-timescaledb-primary`. |

Two fmt2 Tempo PDBs with zero expected pods are not counted as active blockers. Never delete PDBs or use forced eviction just to pass this gate. If safe failover/replication is unavailable, schedule a specific application outage with its owner rather than imply zero downtime.

All nine sea1 VMIs currently report `LiveMigrate` and `LiveMigratable=True`. Node0 hosts core DNS/Salt, DMA, FreeIPA, FreePBX and VPN leaf2/spine2; node1 hosts VPN leaf1/spine1 and `tmpim`; node2 hosts none at capture. Verify migration completion and service traffic, not just the eligibility flag. Preserve redundant VPN and DNS paths during evacuation.

After OS maintenance, separately review security patches for the live Kubernetes minor on each cluster, Cilium compatibility and container runtime updates. Any sea1 1.37 minor upgrade remains a separate change with its own version-skew and API checks; fmt2 is already on 1.37.2. Historical [fmt2 Talos plan](fmt2-talos-k8s-update.md) is background; use current live state for execution.

### Guest infrastructure and packages

For NixOS, the [GitOps configuration](nix/gitops.md) polls every 600 seconds; merging to main deploys. Before changing `nix/flake.lock`, establish a tested temporary comin hold/pin on machines outside the canary, including Vault, and capture their current generations. Build/evaluate every affected configuration on a branch, populate the cache, stage the first machine, then release one machine at a time. The Vault0 configuration tracks `testing`; that is useful only after confirming it is actually deployed and is a healthy follower. Never infer that a branch makes simultaneous main-branch Vault updates safe.

Use the supported LTS kernel with stock compatible ZFS for infrastructure; baseline Linux 6.18.60 on this date. Check the resulting closure's actual kernel, OpenBao/Vault, OpenSSL, SSH, DNS/Consul and runtime versions. Do not copy the desktop's experimental Linux7.3/ZFS override across the fleet. Keep pool feature flags unchanged and retain old generations. [Machine lifecycle](nix/machines.md), [Vault runbook](nix/vault-server.md).

For Vault/OpenBao, verify a recoverable snapshot, unseal/auto-unseal dependencies and proxy reachability. Identify the leader and update one follower, wait for unsealed/healthy caught-up membership, update the other follower, then transfer leadership before updating the former leader. Maintain two healthy members throughout. Avoid a host reboot and a guest service upgrade in the same window. Do not update the proxy/access path while relying on it as the sole recovery route.

For IPA, check replication, DNS, Kerberos and time sync, update one replica and test authentication before the next. For DNS/DHCP/Consul core hosts, verify a working independent resolver, leases/config backups, Consul quorum and the management route before reboot. For VyOS, record boot image/config and BGP/VPN state, then update one redundant peer and test routing before its mate. Use vendor-supported VyOS/OPNSense/Home Assistant upgrade mechanisms.

For Debian/Ubuntu/Fedora/Alpine guests, identify the exact release and repository policy, refresh metadata during the authorized maintenance run, review candidate packages and removals, then apply supported release maintenance updates. Use an apt simulation for Debian/Ubuntu and an equivalent reviewed transaction for other package managers. Do not turn a security update into an unattended OS major upgrade or blindly run `autoremove`. Record release EOL exceptions and make a separate migration plan when a supported kernel cannot be obtained on the current release.

Do not run the existing Ansible `update_packages` role or a fleet-wide Salt highstate as the patch transaction: the role also installs packages and changes HashiCorp repositories, and broad states contain unrelated changes. Use explicit limited targets and reviewed package transactions. Update QEMU/PVE userspace as well as kernels, and confirm whether running VMs need a full stop/start to use the new QEMU process. A migration or guest OS reboot alone is not proof of that replacement.

Inventory application and container dependencies too: Docker/containerd/runc, PHP and web stacks, browsers, language runtimes, databases, images and firmware/microcode as applicable. Container images need rebuild/redeploy to update their userland; host patching does not change an image. Restart affected long-running services and verify the process uses the new libraries. Keep database major versions and incompatible application migrations out of the routine package batch.

### Wave 5 desktop and endpoints

`sea420-desktop` still runs **7.2.9**. Generation **56** already contains **7.3.1** and ZFS 2.4.4 with four local Linux7.3 compatibility patches; the ZFS boot/storage VM check passed. Reboot during a desktop window, then verify actual kernel, ZFS pools, datasets, GPU/network drivers and services. Retain generation55 as a recovery option. A passing VM check does not establish production soak time for the custom ZFS module. Upstream nixpkgs submission [579666](https://github.com/NixOS/nixpkgs/pull/579666) was closed without merge; the override remains local.

Bring offline personal machines online for identity/version checks before building their compatible closures. Map Darwin aliases to actual Macs, update macOS through Apple, and update Nix packages separately. Coordinate owners of other tailnet endpoints; exclude service peers from hardware totals.

## Execution checks and rollback

For each machine, attach a maintenance record with owner, window, console path, backup timestamp/restore test, current kernel and exact package/closure, reviewed target, service probes and rollback path. Stage packages/builds before the reboot window where practical. Re-evaluate vendor releases at execution time rather than freezing this dated snapshot indefinitely.

Useful read-only collection, after selecting a verified target and its management lane:

```sh
uname -r
cat /etc/os-release
# Proxmox: version, quorum, guest placement
pveversion -v
pvecm status
pvesh get /cluster/resources --type vm --output-format json
# Kubernetes: explicitly select the site kubeconfig
kubectl --kubeconfig "$FLEET_KUBECONFIG" get nodes -o wide
kubectl --kubeconfig "$FLEET_KUBECONFIG" get pdb -A
kubectl --kubeconfig "$FLEET_KUBECONFIG" get pods -A -o wide
# NixOS: distinguish running from selected system
readlink -f /run/current-system
readlink -f /nix/var/nix/profiles/system
```

Commands are lane-specific, not a script to run indiscriminately on every machine. Refresh results immediately before maintenance. SSH fingerprints, kubeconfigs and Vault material remain private; do not commit raw API/config dumps.

A machine is complete only after the **running** kernel and exact vendor package/closure are verified, affected userland processes/images are refreshed, no unexpected failed services appear, application/route/authentication probes pass and cluster/storage redundancy returns to baseline. Record the package/advisory evidence for the applicable CVEs; do not mark a machine fixed solely because an update command exited successfully.

Stop the wave for lost quorum, failed DNS/authentication, unexpected route changes, failed migration, storage degradation or boot/driver regressions. Recover the current machine before touching the next. Use the prior boot entry/Nix generation/Talos recovery procedure or backed-up appliance image as appropriate. Database/schema changes can prevent simple package rollback; handle them as separate changes. An emergency rollback to a vulnerable kernel is temporary recovery and reopens the remediation item.

Update the checklist fields `upgrade_status`, `target_package_version`, `verified_at` and `remaining_exceptions` after each window, along with the actual running version. Use `vm_batch`, `expected_disruption`, `kernel_action` and `canonical_guest` to schedule the guest phase without duplicate alias reboots. Then correct NetBox platform/owner/status/parent data with the verified facts. Reconcile both inventory-to-live and live-to-inventory so omitted machines and stopped images remain visible.
