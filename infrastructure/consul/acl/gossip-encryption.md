# sea1 gossip encryption — draft

**Status: draft. Not applied.** Companion to `README.md` (ACLs).

Today: `Gossip Encryption: false` on every sea1 agent. Any host that can reach
`:8301` can join the LAN pool and be trusted as a member — ACLs do nothing about
this, they govern the API only. With the consul clients moving to `hostNetwork`
this is arguably the more important of the two controls.

## The ordering that matters

A gossip key cannot be switched on atomically across a running pool. Do it in
two passes or the DC partitions:

**Pass 1 — accept both.** Every agent gets the key *and* is told to keep
accepting unencrypted traffic:

```hcl
encrypt = "<32-byte base64>"
encrypt_verify_incoming = false
encrypt_verify_outgoing = false
```

Roll this to all agents. Mixed-state agents keep talking throughout.

**Pass 2 — require it.** Once every member is confirmed on the key, flip both
verify flags to `true` (their defaults) and restart again.

Skipping pass 1 splits the pool into encrypted and cleartext halves that cannot
see each other — on a 3-server raft that risks quorum.

## Generating the key

```sh
consul keygen        # 32-byte base64
```

One key for the whole DC. fmt2 is a **separate, unfederated** cluster
(`/v1/catalog/datacenters` → `["sea1"]`), so it needs its own key and is not
affected.

## Where it goes

| agent | mechanism |
|---|---|
| sea1-hv-0/1/2 | salt — `salt/state/consul/`, but see the blocker below |
| sea1-core | nix — `nix/machines/sea1-core/consul.nix` |
| k8s clients | helm — `global.gossipEncryption.secretName` / `.secretKey` |

The key is a secret: Vault for the salt/nix side, a Kubernetes Secret for the
chart. Do **not** put it in the pillar or the argocd values in cleartext. Note
`global.gossipEncryption.autoGenerate` exists in the chart but is wrong here —
it would mint a *different* key than the servers use.

## Blocker

The three HVs cannot be reached by salt: `top.sls` targets `G@tags:consul` and
their grains still read `tags: []`. NetBox is now correct (the `consul`,
`consulserver` and pending `saltminion` tags), but `/etc/salt/grains` will not
be re-rendered until `provision_salt_minion.yml` runs against them — see
`netbox-tag-sea1-hvs.sh`. Until then pass 1 on the servers is a manual file
placement.

## Rekeying later

Once encryption is on, rotation is `consul keyring -install` → `-use` →
`-remove`, which is online and does not need restarts. Getting to *that* state
is the one-time cost above.

## Verify

```sh
consul keyring -list          # every member should report the same key
consul members                # all alive, no partition
consul operator raft list-peers
```

Check `consul members` after **each** server restart, not just at the end.
