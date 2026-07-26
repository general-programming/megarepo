# fmt2-core-0 retirement checklist

`fmt2-core-0` (79.110.170.4 / vlan5 10.65.67.6 / vlan1000 10.255.1.8,
Ubuntu 24.04) is being dismantled. DHCP already moved to the NixOS
`fmt2-core` (Kea, 2026-07-26, commit 4fd66877). This is the inventory of
everything else the box still does, in rough migration order. Each item
should land as a Nix module / k8s workload (or be declared dead) before the
host is powered off.

## Still-live duties

- [ ] **DNS on 10.65.67.6** (dnsmasq). NixOS fmt2-core already serves an
      identical config on 10.65.67.5; DHCP now hands out .5. Remaining
      static `.6` consumers are being enumerated via query logging
      (`/etc/dnsmasq.d/98-query-logging.conf` →
      `/var/log/dnsmasq-queries.log`, enabled 2026-07-26). Known so far:
      10.255.1.23, 10.255.1.24 (static resolv on OOB gear), plus
      sea420-core's dhcpd options (`10.3.0.3,10.65.67.6` — fix in salt
      pillar when touched). Retire .6 only after the log goes quiet.
- [ ] **TACACS+** — docker container `tacacs`, bound to `10.65.67.6:49`.
      Network gear authenticates against this IP. Needs a new home
      (container on fmt2-core or k8s) and a gear-side IP update — or keep
      the IP alive by moving .6 to the successor (conflicts with the
      "everything to .5" decision; gear configs must be touched either way).
- [ ] **LibreNMS** — nginx vhost `nms.as208590.net` + mariadb + php/librenms
      crons. Decide: migrate to k8s (chart exists upstream) with a mariadb
      dump/restore, or retire if monitoring has moved elsewhere.
- [ ] **OOB reverse proxies** — nginx vhosts `oob-apc-a/b`,
      `oob-laserjet-ilo`, `oob-tik` (`.as208590.net`, port 80 + 8001
      default): thin proxies to management interfaces on vlan1000. Port to
      traefik/nginx on fmt2-core or an ingress.
- [ ] **WireGuard `wg0`** — 172.19.69.4/24, peer at 209.251.245.111
      (sea420 side), routes 192.168.3.0/24. Map what rides this tunnel;
      recreate on fmt2-core (systemd-networkd wg) or fold into tailscale.
- [ ] **Consul agent** — rejoin/retire; fmt2-core already runs one
      (consul.nix). Check services registered by this node first.
- [ ] **certbot cron** — which certs? Likely the nginx vhosts; moves with
      LibreNMS/OOB proxies.
- [ ] **node_exporter :9100** — re-point whatever scrapes it (LibreNMS or
      prometheus) at fmt2-core's exporter.

## Almost-certainly-dead weight (verify, then ignore)

- [ ] **bird6** — config is router-id + kernel/device stanzas only; no
      protocols, announces nothing. Confirm no kernel routes imported,
      then let it die with the host.
- [ ] **ceph-crash** — client remnant, no OSDs here.
- [ ] **rpcbind** — nothing NFS-mounted or exported (fstab/exports empty).
- [ ] **fail2ban, lldpad, chrony, tailscale** — host-local; die with host.
- [ ] **IPA client** (hostname `fmt2-core-0.ipa.generalprogramming.org`) —
      `ipa host-del` when decommissioned.

## Order of operations

1. DNS `.6` consumer hunt (in progress) → flip stragglers to `.5`.
2. TACACS+ rehome + gear config update (highest blast radius: locked-out
   switches if botched — stage a local-auth fallback first).
3. LibreNMS + OOB proxy + certbot as one nginx-shaped migration.
4. WireGuard tunnel recreate; consul leave; exporter re-point.
5. Final: week of silence on dnsmasq log + tcpdump port 49/80/443, then
   power off, `ipa host-del`, NetBox status → decommissioned, remove from
   salt if enrolled.
