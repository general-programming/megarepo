---
name: comin-deploy
description: Deploy nix/ changes to fleet machines via comin gitops — trigger a manual fetch instead of waiting for the poller, watch build/switch progress, verify, and troubleshoot stale mirrors. Use whenever a commit to main must land on a comin-managed NixOS host (sea1-core, fmt2-core, ...) or a deploy seems stuck.
---

# comin deploy: poke, watch, verify

comin (nlewo/comin) polls `https://github.com/general-programming/megarepo.git`
every **600s** (`gitops.interval`, nix/modules/gitops.nix) and deploys
`nix/#nixosConfigurations.<host>` when `main` moves. Don't sit through the
poll window — poke it.

All commands below run **on the target machine** (e.g. `ssh root@10.3.2.6`
for sea1-core). The `comin` CLI is in the system profile and talks to the
agent over gRPC at `/var/lib/comin/grpc.sock`.

## Deploy flow after pushing to main

```sh
comin fetch      # trigger an immediate fetch of all remotes
comin status     # Fetcher / Builder / Deployment sections; commit id + eval/build state
```

`comin status` shows the pipeline: fetched → Evaluation succeeded →
Built → deployment. A busy build also shows as many `nix` processes.
`comin watch` streams events live if you want to follow along.

Wait for the switch by grepping the journal for the commit sha:

```sh
until journalctl -u comin --since "-30min" --no-pager | grep -q <sha>; do sleep 15; done
journalctl -u comin --since "-30min" --no-pager | grep -iE "deployment|error|failed" | tail
```

Then verify the services the commit touched (`systemctl is-active ...`).

## Quick health check (Prometheus exporter)

Each machine exposes metrics on localhost — port comes from
`vars'.ports.comin-exporter` in nix/vars (sea1-core: `127.0.0.1:41001`).
This is the ONLY http surface; there is no REST API.

```sh
curl -s http://127.0.0.1:41001/metrics | grep -vE '^#'
```

Key gauges: `comin_deployment_info{commit_id,status}` (last COMPLETED
deploy — it does not update mid-build), `comin_last_eval_failed`,
`comin_last_build_failed`, `comin_last_deployment_failed`,
`comin_is_suspended`, `comin_need_to_reboot`.

## Other useful subcommands

```sh
comin deployment list      # recent deployments
comin deployment latest
comin suspend | comin resume   # pause/unpause build+deploy (fetch still runs)
```

## Troubleshooting

- **NEVER run CLI git write operations inside `/var/lib/comin/repository`.**
  comin uses go-git; a CLI `git fetch` in its mirror desyncs go-git's ref
  bookkeeping and every later comin pull fails with
  `Pull from remote 'origin' failed: reference has changed concurrently`.
  (Read-only inspection like `git log` is fine.)
- **Poller fetches but nothing deploys / "reference has changed
  concurrently"**: re-clone the mirror —
  `systemctl stop comin && rm -rf /var/lib/comin/repository /root/.cache/nix/gitv3 && systemctl start comin && comin fetch`.
  If eval then fails with `Cannot find Git revision ... in ref ...`, that's
  the unborn-HEAD bug hitting the fresh clone (preStart ran before the
  clone existed): `systemctl restart comin && comin fetch` fixes it.
- **Wrong ref / unborn HEAD**: comin's mirror refspec only updates
  `refs/remotes/origin/*`; HEAD can be unborn and poison lix's fetchGit
  cache. gitops.nix has a preStart workaround (symlinks HEAD, drops
  `/root/.cache/nix/gitv3`). If eval resolves a stale rev, restart the
  `comin` unit to re-run it.
- **Failed build = safe**: comin never switches to a failed build; the old
  generation (and its services) keep running. Fix forward or `git revert`
  and poke again.
- **A commit outside nix/ still triggers a deploy** — the flake lives in
  `nix/` (`repositorySubdir`), but any new sha on main gets evaluated; a
  no-op closure deploys instantly.
