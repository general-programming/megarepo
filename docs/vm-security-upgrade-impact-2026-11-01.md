# VM security upgrade order and service impact

Start with guest operating systems, then maintain physical hypervisors and the sea1 Talos hosts. Short service outages and guest reboots are acceptable for this rollout. Keep replicated infrastructure members one at a time so an individual VM outage does not become a cluster-wide outage.

Inventory and runtime evidence are from November 1, 2026. Refresh guest health and package candidates immediately before execution. The [fleet checklist](fleet-security-upgrade-inventory-2026-11-01.csv) assigns each VM record a `vm_batch`, expected impact and kernel action. The [fleet plan](fleet-security-upgrade-plan-2026-11-01.md) contains package targets, access gaps and recovery gates.

The later [access and OS audit](fleet-access-os-audit-2026-11-01.md) verifies privileged paths for `gameservers` and `freepbx`; `reddit-video-proxy` and `sea69-haas` remain unreachable on their tested management paths. All eight fmt2 Talos VMs already run the initial OS baseline, with Kubernetes 1.37.2. Use the audit's current versions before executing a batch.

## First maintenance window

1. Patch **`gameservers`** as the canary. Save game state and stop workloads cleanly, review the Ubuntu 24.04 package transaction, install security/kernel updates and reboot. Players disconnect; game services are unavailable until the guest and applications recover. Test a real connection before continuing.
2. Patch **`freepbx`**. Drain/end calls in the agreed window, preserve PBX configuration and compatible package versions, then update and reboot. Calls in progress can drop; registrations and new calls are unavailable during recovery. Verify registrations and an inbound/outbound call. Preserve existing web access restrictions; do not mix a PBX major upgrade into the OS update.
3. Patch **`reddit-video-proxy`** once its deployed OS and workload are verified. Requests through that proxy can fail or need retry while services restart. Its exact consumers are not yet inventoried, so check them before including it in this window.
4. Update **`sea69-haas`** through Home Assistant's supported maintenance process after taking its backup. Dashboards, integrations and centrally run automations pause; check important automations afterward. Confirm the current version and add-on compatibility first.

Only `gameservers` and `freepbx` have directly observed guest OS versions in this initial group. Discovery may move the other two to a later window without delaying the verified guests. Start serially; after a successful canary, unrelated application guests can use small batches with separate recovery checks.

## Ordered guest batches

Batch order applies to the corresponding service group. Owner/identity holds affect that guest, not every later batch. Physical host reboots remain after the guest phase. The first application canary can run while preparing the infrastructure batches.

| Batch | Guests | Expected interruption and recovery check |
| --- | --- | --- |
| V1 Application guests | `gameservers`, `freepbx`, `reddit-video-proxy`, `sea69-haas` | Game connections, calls, proxy requests or home automation pause as described above. Reboot one guest, test its service, then continue. |
| V2 Data and application services | `wob-app-cream`; `10gbe` = Salt/Tailscale `10gbps` = KubeVirt `tmpim/tmpim` | `wob-app-cream` has Docker and NFS roles: applications stop and NFS clients can block or fail. Identify consumers and quiesce writes before reboot. The 10GbE guest hosts Docker/lab/data workloads, Caddy/Meilisearch, a Tailscale exit node and CephFS mounts; those guest services and that exit path pause. Verify CephFS mounts and expected data before starting writers. These are guest/client updates, not a Ceph OSD/server reboot. |
| V3 Authentication replicas | `sea420-ipa-1`, `ipa-fremont-donut`, `sea1-ipa` (= KubeVirt `freeipa/freeipa`) | One LDAP/Kerberos/HTTPS endpoint is down at a time. Clients with functioning replica discovery can use the others; clients pinned to the rebooting replica may fail or wait. Check replication and client failover before the first, then authentication after each. The sea1 manifest declares no DNS role for its IPA guest; verify actual services at preflight. Choose actual replica order after health checks. |
| V4 Secrets and proxy | `fmt2-vault-00`, `fmt2-vault-01`, `fmt2-vault-02`; then `fmt2-vault-proxy` / PVE `vault-proxy` CT120 | Followers individually, then transfer leadership and patch the former leader. Single-node connections can drop; a leadership transfer can cause retries. Maintain two healthy unsealed members. The proxy is a separate LXC restart/userland window: clients using its front door can lose secrets, certificate and SSH-signing access while it restarts. Keep an independent verified access route and do not combine its outage with a Vault member outage. Confirm actual guest OS/backend first. |
| V5 Site core services | `sea1-core`; `sea420-core` LXC; `fmt2-core` only if a separate deployed guest is verified | DNS lookups, DHCP requests/renewals, Consul and management dependencies can be affected at each site. Existing DHCP leases generally persist, but new/renewing clients can time out. `sea1-core` also runs the Salt master and access/tunnel services, so prepare a recovery path independent of that guest. Confirm fallback DNS and Consul quorum, then take one site at a time. Gate comin before changing shared Nix inputs. |
| V6 Routing and VPN | `fmt2-vpn-leaf-1/2`, `fmt2-vpn-spine-1/2`, `fmt2-vpn-client-1`; `sea1-vpn-leaf-1/2`, `sea1-vpn-spine-1/2` | Tunnels/BGP sessions drop on the rebooting guest; route convergence can interrupt traffic. Reboot only one verified redundant member at a time and verify routes, tunnels and the management path. `fmt2-vpn-client-1` has no verified redundant mate: plan its affected client path as an outage. Router topology determines the precise consumers; do not promise seamless failover. |
| V7 fmt2 Kubernetes VMs | All eight already run Talos 1.14.5 / Linux 6.18.58 and Kubernetes 1.37.2. Verify applicable newer fixes before scheduling further OS work. | No additional reboot solely to install the earlier baseline. If newer updates apply, workers first and CP singly: drains/reboots can move or interrupt workloads, and API connections can drop. Refresh Loki/Attic PDB health, retain etcd quorum and preserve live Kubernetes 1.37.2/CNI. |
| V8 Other active guests | Named tenant guests and guests awaiting role/OS discovery, listed below | Their own applications/connections stop during a guest reboot. Identify shared dependencies before treating the outage as isolated. Coordinate with the tenant; do not infer an application's function from its hostname. These can proceed after the first canary as each guest's owner/OS checks finish. |

The aliases for `10gbe` and `sea1-ipa` are established by the [10GbE VM manifest](../argocd/apps/vms/tmpim/sea1/vm-tmpim.yaml) and [FreeIPA VM manifest](../argocd/apps/vms/freeipa/sea1/vm-freeipa.yaml), alongside the NetBox address and observed guest hostname. Count the three 10GbE records as **one guest update**. Its Debian release grain is unavailable; verify the actual apt suite before choosing packages. The [sea1 core configuration](../nix/machines/sea1-core/configuration.nix) and [sea420 core configuration](../nix/machines/sea420-core/configuration.nix) define the site-service dependencies.

## Other guests in scope

These guests receive an explicit OS/package review and reboot window once identity, ownership and workload checks pass. Running status is confirmed only where the live inventory established it. NetBox active status alone does not prove a guest is running.

| Owner or group | Guests | Impact to establish before reboot |
| --- | --- | --- |
| erin | `snakeroom-temp`, `sea69-bootbox` | Actual applications, active sessions and any boot/provisioning dependencies; roles have not been verified. |
| DMA guests | `dma-colo-erin-00`, `dma-colo-fmt2-00` | Guest workloads and remote access. The sea1 guest has encrypted root and persistent vTPM: verify unattended unlock/recovery before reboot. Confirm both ownership and actual workload scope. |
| Unassigned or missing inventory | NetBox `boom-vm-1` address now belongs to Proxmox `tmpim-beer`; also `genny-cascadia`, `lakvm` | Reconcile rename versus address reuse before scheduling one current guest at 79.110.170.52. Its Debian 12 OS and root guest-exec are verified. Application consumers remain unknown. `lakvm` is a running Proxmox guest without a matched NetBox record. |
| obw | `chichester`, `garden`, `ghast`, `poppy`, `robust-wizard`, `saffron` | Tenant applications and any shared endpoints; coordinate their restart order with the owner. |
| linuxgemini | `fmt2.moderateinfra.net`, `james-shreds`, `linear-pens.linuxgemini.space` | Tenant applications, routes and client sessions as established by workload discovery. |
| opl | `20w14s`, `opl-dump`, `opl-playground` | Tenant applications and storage/writer dependencies; identify actual workloads. `opl-vods` is in the stopped list below. |
| Zephy | `furry-slut`, `minecraft-silver`, `not-furry-shit`, `zephy-traefik` | Tenant applications; check game users and reverse-proxy dependents if those name-implied roles are confirmed. |
| brianna | `utsuho lays eggs` | Tenant applications and sessions; confirm installed Fedora release and workload. |

## Deferred and conditional guests

| Records | Planned handling and impact |
| --- | --- |
| Running `router-portland` | Decommissioning in NetBox but running/HA-started in Proxmox. Verify whether it still carries traffic. If retained, place it in V6 with its own routing impact; if retiring, identify dependencies before shutdown. No automatic removal. |
| `recursiveness`, `vrouter-nepwork` | Decommissioning, actual runtime/dependencies unverified. Confirm retention; patch if retained/running or retire safely. A routing outage remains possible for the latter if its role is confirmed. |
| Stopped `fmt2-netboot`, `opl-vods` | No current guest reboot outage while stopped. Confirm retention and patch before returning to service; startup services/consumers still need a check. |
| Stopped `nepbox-one-x`, `windowsserver-lasagnabranch`, `lavahut-runner3`, `berkeley`, `urf`, `vapidapp`, `lavahut-runner4`, `bezosbox`, `ziguana` | Nine Proxmox guests missing from NetBox. Do not boot merely to generate a reboot event. Identify owners/retention; patch or replace retained images before restart. Use supported Windows maintenance if Windows is verified. |
| `adhoc-temp-ips`, `fmt2-adhoc-ips` | IP allocation placeholders, no VM to reboot. Discover any real guests using their allocations separately. |
| `fmt2-core` configuration | Deployment/identity remains unverified. Add to V5 only if it is an actual guest; do not duplicate the physical Ubuntu `fmt2-core-0` / Salt `oob-backup`. |
| Tailnet-only `oci-app-arm64-1` and other unreconciled peers | Confirm ownership and whether each peer is a cloud VM, physical endpoint or service. If the OCI peer is a retained VM, add it to V8 with its actual OS and application impact. A tailnet name/OS label alone does not identify an upgrade target. |

## Shared kernels and physical hosts

`sea420-core` and `fmt2-vault-proxy` are LXC containers in the available configuration/resource evidence. Update their userland in the guest phase; their kernel remediation stays open until the parent Proxmox host boots its fixed kernel. Confirm actual parent/identity before recording inherited versions.

The three `sea1-k8s-0/1/2` nodes are physical Talos/Ceph/KubeVirt hosts and stay in the physical-host phase, even though they host VMs. The eight fmt2 Talos nodes are VMs and belong in V7; that batch has Kubernetes service impact in addition to the guest reboot.

After the VM windows, patch physical Proxmox hosts singly, then the sea1 Talos hosts with the fleet plan's storage/quorum checks. Guest OS upgrades do not fix the previously identified Proxmox host kernel issues. Avoid an extended gap before host maintenance; prepare host packages and evacuation plans while the guest batches proceed.

## Recovery and completion

Before each guest: verify console access, a recoverable guest/application backup, exact installed OS and supported package candidate, disk/boot space, critical dependencies and a clean shutdown/reboot method. OS updates do not require reprovisioning, disk wipes, Vault peer removal or database major upgrades. For ordinary guests, a guest OS reboot does not require draining the underlying Kubernetes node; V7 has separate node-drain requirements because the guest itself is a Kubernetes node.

For each guest, record its running kernel and package/closure after reboot, failed services, application probe and remaining exceptions. For LXC, record the updated userland and inherited kernel separately. For replicated services, verify recovery/replication before touching the next member. Stop that batch for a failed boot, failed mount, lost quorum or application regression; restore the current guest using its prior boot entry/closure or application-aware backup before continuing. Do not mark duplicate aliases as separate completed upgrades.
