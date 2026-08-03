#!/usr/bin/env bash
# Add the consul + consulserver tags to the three sea1 hypervisors in NetBox.
#
# APPLIED 2026-08-02 (by hand, not via this script). All three HVs now carry:
#   ansible_managed, consul, consulserver, nodeexporter, proxmox, service_proxmox
# Kept as documentation and as an idempotent re-assert. The NETBOX_API_KEY in
# .envrc_secret is READ-ONLY for dcim.device writes (PATCH -> 403), so running
# this needs a r/w token:
#
#   NETBOX_API_KEY=<r/w token> ./netbox-tag-sea1-hvs.sh
#
# Why: sea1-hv-0/1/2 ARE the sea1 consul servers (they hold the raft quorum),
# but neither the `consul` nor `consulserver` tag is on them. Compare:
#   consul       -> fmt2-core-0, fmt2-core-1, sea420-hv-egg-irl,
#                   + VMs saffron, sea1-core, sea420-core, wob-app-cream
#   consulserver -> sea420-core only
# So the sea1 servers are simply missing from the source of truth.
#
# Operationally INERT today, in both directions:
#   - No ansible playbook targets these groups (they target ansible_managed,
#     saltminion, saltmaster, all).
#   - salt's G@tags:consul still will not match, because the ansible ->
#     /etc/salt/grains path drops `tags` regardless of what NetBox holds
#     (the HVs already carry four other tags and the grains file still says
#     `tags: []`). That is a separate bug in grains.j2 / the nb_inventory
#     hostvar, and `tags` being a reserved Ansible variable name is the
#     likely mechanism.
#
# !! READ BEFORE FIXING THAT SECOND BUG !!
# Once these tags exist AND the grains path works, salt WILL apply
# salt/state/consul/ to all three HVs. Their /etc/consul.d/00-base.hcl is
# dated 2024-02-03 and does not match the current template (server/ui inline
# instead of in 00-server.hcl, no salt_managed header, concrete v6 bind_addr
# vs the pillar's "[::]"). That is a config rewrite plus a consul restart on
# three raft voters. Do it one node at a time with
# `consul operator raft list-peers` checked in between.

set -euo pipefail

: "${NETBOX_API_KEY:?set NETBOX_API_KEY to a token with dcim.device write}"
NETBOX_URL="${NETBOX_URL:-https://netbox.generalprogramming.org}"

# Existing tags are preserved explicitly -- the NetBox API REPLACES the tags
# array on PATCH, it does not merge. All three devices currently carry exactly
# these four (verified 2026-08-02).
read -r -d '' PAYLOAD <<'JSON' || true
{"tags":[
  {"slug":"ansible_managed"},
  {"slug":"nodeexporter"},
  {"slug":"proxmox"},
  {"slug":"service_proxmox"},
  {"slug":"consul"},
  {"slug":"consulserver"},
  {"slug":"saltminion"}
]}
JSON

# saltminion is the STILL-PENDING one and it is the actual root cause of the
# stale grains. provision_salt_minion.yml targets `hosts: saltminion` and is
# what templates /etc/salt/grains; the sea1 HVs are not tagged saltminion
# (it sits on fmt2-core-0 + ten VMs), so that playbook has never selected them
# and their grains file still reads `tags: []` from a stale render.
#
# The inventory and the template are both FINE -- verified 2026-08-02 by
# running the real inventory: `ansible-inventory -i netbox_inventory.yml
# --host sea1-hv-0` returns
#   tags:  [ansible_managed, consul, consulserver, nodeexporter, proxmox, service_proxmox]
#   sites: [sea1]
# and a bare `tags` hostvar renders correctly through grains.j2 in isolation
# (so the "reserved Ansible variable name" theory is refuted).
#
# After tagging, re-render grains with:
#   ansible-playbook -i inventory/netbox_inventory.yml provision_salt_minion.yml \
#     --limit 'sea1-hv-0,sea1-hv-1,sea1-hv-2' --check --diff     # then without --check

for id in 100 101 102; do   # sea1-hv-0, sea1-hv-1, sea1-hv-2
  echo "--- device $id ---"
  curl -sS -m 25 -X PATCH \
    -H "Authorization: Token ${NETBOX_API_KEY}" \
    -H "Content-Type: application/json" \
    "${NETBOX_URL}/api/dcim/devices/${id}/" \
    -d "${PAYLOAD}" \
  | python3 -c "
import sys, json
d = json.load(sys.stdin)
if 'name' in d:
    print(f\"  OK {d['name']}: {sorted(t['slug'] for t in d.get('tags', []))}\")
else:
    print('  FAILED:', json.dumps(d)[:300]); sys.exit(1)
"
done
