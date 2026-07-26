# Arista EOS adoption — slice 1 bootstrap

barf now manages Arista devices one config slice at a time
(`go/pkg/barf/scope/eos.go`, `go/pkg/barf/device/eos_writer.go`). Slice 1 owns exactly three
things: the `admin` user (privilege 15, network-admin, sha512 secret), its
SSH key(s) from `global_meta.ssh_keys`, and the `enable` password. barf
talks eAPI (HTTPS JSON-RPC) as that same `admin` user, so each device
needs a one-time manual bootstrap to break the chicken-and-egg. Nothing
outside the slice is ever read into a diff or written by a deploy.

Devices in scope today:

| device | model | mgmt | state |
|---|---|---|---|
| fmt2-cor-r-140752-1 | DCS-7050QX-32S (40G) | 10.65.67.1 | in network.yml; needs bootstrap |
| fmt2 100G router | ? | ? | **not in NetBox or network.yml — needs identification first** |

## Step 0 — prerequisites per device

- **Privileged CLI access.** For fmt2-cor-r-140752-1 the enable secret is
  effectively lost (every candidate in `cluster-secrets/host-…` was
  rejected on 2026-07-26). If you don't remember it, do EOS password
  recovery from the console: power-cycle, `Ctrl-C` into Aboot, boot with
  `console_speed`… standard Aboot recovery (`password_reset` / boot with
  `sfe` flag per Arista KB) — physical/console access required. `erin`'s
  SSH key works for unprivileged login if you only need to look around.
- For the 100G router: first tell barf/NetBox it exists — hostname,
  model, mgmt IP — then fill in the `TODO` placeholder in
  `projects/barf/network.yml` and create the NetBox device.

## Step 1 — mint the credentials in Vault (on the devbox)

Let barf create them (it auto-mints missing secrets on first render):

```sh
cd ~/src/megarepo
go run ./go/cmd/barf generate fmt2-cor-r-140752-1
vault kv get -mount=cluster-secrets host-fmt2-cor-r-140752-1
```

Note the `admin-password` and `enable-password` values. To *rotate* later:
delete those keys from the Vault secret and re-run generate + deploy.

(NB: the old, rejected values live in earlier versions of the same secret
path — kv v2 keeps history, nothing was destroyed.)

## Step 2 — bootstrap the device (console/privileged session)

```text
configure
username admin privilege 15 role network-admin secret <admin-password from Vault>
enable password <enable-password from Vault>
management api http-commands
   no shutdown
end
copy running-config startup-config
```

eAPI notes for fmt2-cor-r-140752-1 specifically: `show management api
http-commands` currently reports `SSL Profile: main, invalid` (no server
certificate) with 443 closed. Clear the broken profile binding so the
default self-signed certificate is used:

```text
configure
management api http-commands
   no protocol https ssl profile
   protocol https
   no shutdown
end
```

**Certificate gotcha (fmt2-cor-r hit this):** eAPI will not start without a
valid server certificate, and "no profile bound" does NOT mean "use a
self-signed default" — the API sits in `HTTPS server: starting` forever
with `! No server certificate configured`. The stock `main` profile on
fmt2-cor-r was invalid (`ssl_host.crt` expired in 2022). Mint a fresh
self-signed cert and bind it:

```text
security pki key generate rsa 2048 eapi.key
security pki certificate generate self-signed eapi.crt key eapi.key \
   generate rsa 2048 validity 3650 parameters common-name <hostname>
configure
management security
   ssl profile eapi
      certificate eapi.crt key eapi.key
!
management api http-commands
   protocol https ssl profile eapi
end
copy running-config startup-config
```

Both the profile validation and the HTTPS listener take ~10-30s to settle
after each change — `show management api http-commands` may still say
`starting` / `VRFs: None` immediately afterwards. Wait and re-check before
concluding it failed.

**VRF gotcha (this bit is what makes 443 answer on the internal side):**
EOS serves eAPI only in the default VRF unless each VRF is enabled
explicitly. fmt2-cor-r's internal addresses (10.65.67.1, 10.255.1.1) live
in `vrf internal`, so also:

```text
configure
management api http-commands
   vrf internal
      no shutdown
end
copy running-config startup-config
```

(The provider manages this too — `eapi_vrf: internal` in network.yml —
so once reachable, barf keeps it enforced.)

Verify on-device: `show management api http-commands` → `Enabled: Yes`,
`HTTPS server: running, port 443`, and the `internal` VRF listed as
running.

## Step 3 — verify from the devbox

```sh
cd ~/src/megarepo
go run ./go/cmd/barf diff fmt2-cor-r-140752-1
```

Expected: `no changes` — the provider verifies the Vault passwords
against the device's hashes (salt differences don't count as drift), and
the ssh key matches `global_meta.ssh_keys`. If you bootstrapped with
different values, the diff shows exactly which managed line will change;
`go run ./go/cmd/barf deploy fmt2-cor-r-140752-1` applies it and saves.

## Guarantees / limits of slice 1

- deploys send only `username admin …` / `enable password …` lines plus
  `copy running-config startup-config`; never any `no …` for other
  config, never other users (`erin` etc. are untouched).
- diffs read only the `username`/`enable` sections; the rest of the
  running config is invisible to barf.
- EOS holds at most a primary + secondary ssh-key per user, so
  `global_meta.ssh_keys` may list at most two keys.
- known follow-up slices, deliberately not in scope yet: name-server /
  DNS (the 10.255.1.8→10.255.1.9 fix from the fmt2-core-0 retirement),
  DHCP relay helpers, syslog, TACACS/AAA, NTP, SNMP.
