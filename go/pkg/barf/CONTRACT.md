# barf-go: package contract

Go port of `projects/barf` (Python), read surfaces only. This document is
the coordination contract between parallel implementation efforts: each
package below is owned by exactly one worker, and cross-package types are
frozen here so packages can be written independently.

Module root: `github.com/general-programming/megarepo`
Go: 1.25 (toolchain 1.26 available)

## Hard constraints (all packages)

1. **Nothing may modify a network device.** No config push, no `configure`
   session, no `copy running-config startup-config`, no NETCONF edit-config,
   no eAPI `config()` calls. Read-only verbs only (`show ...`, config
   *retrieve*). This is a read-surface port; deploy lands later.
2. **Do not modify `projects/barf` (the Python implementation).** It stays
   authoritative. Read it freely as the reference.
3. **Do not run any `git` command.** Write files only; the orchestrator
   commits.
4. **Stay inside your own package directory.** If you need something from
   another package that does not exist yet, define a *local interface* for
   it (Go interfaces are structural) and note it in your summary.
5. Secrets never get logged or written to disk. Vault values are used in
   memory only.

## Layout and ownership

| package | path | owner |
|---|---|---|
| `netbox` | `go/netbox` | worker B |
| `vault` | `go/vault` | worker B |
| `barf/model` | `go/barf/model` | worker A |
| `barf/render` | `go/barf/render` | worker A |
| `barf/device` | `go/barf/device` | worker C |
| `barf/cli` | `go/barf/cli` | worker D |
| `barf/tui` | `go/barf/tui` | worker D |
| `barf/cmd/barf` | `go/barf/cmd/barf` | worker D |

## Frozen types (`go/barf/model`) — worker A defines, everyone else imports

These mirror `projects/barf/network.yml` and the Python `BaseHost`. Field
names are fixed; add fields freely, do not rename these.

```go
package model

type Site struct {
    Name   string
    ID     int
    Coords [2]float64
}

type GlobalMeta struct {
    SearchDomain string
    Nameservers  []string
    SSHKeys      []string
    SNMPPublic   string
    SNMPContact  string
    SNMPLocation string
    CommunityASN int
    Sites        map[string]Site
}

type Address struct {  // an IP with prefix length
    IP     netip.Addr
    Prefix int
}

type VLAN struct {
    VID  int
    Name string
}

type Interface struct {
    Name          string
    Description   string
    Addresses     []Address
    DHCP          bool
    IPv6Autoconf  bool
    Management    bool
    MTU           int
    Members       []string
    UntaggedVLAN  *VLAN
    TaggedVLANs   []VLAN
    Wireguard     map[string]any
    RA            map[string]any
}

type Host struct {
    Hostname    string
    DeviceType  string   // "vyos" | "eos" | "linux" | ...
    Role        string   // "vpn" | "core" | ...
    Site        string
    ID          int      // network.yml `id`, 0 when absent
    ASN         int      // 0 when absent
    Address     *Address
    IP6Address  *Address
    Interfaces  []Interface
    Nameservers []string // inherits GlobalMeta when unset in yaml
    Networks    []string
    ExtraConfig []string
    CloudInit   bool
    EAPIVRF     string   // eos only: network.yml `eapi_vrf`
    Raw         map[string]any // untranslated yaml for not-yet-ported knobs
}

type Link struct {  // wireguard fabric link
    A, B    string  // hostnames; A is the uplink side (side_a in Python)
    Port    int
    Network string
    Secret  string
    IPsec   bool
}

type Network struct {  // a parsed network.yml
    Global GlobalMeta
    Hosts  []Host
    Links  []Link
}

func Load(path string) (*Network, error)
func (n *Network) Host(hostname string) (*Host, bool)
```

## Frozen interfaces

```go
// go/barf/render
type Renderer interface {
    // Render returns the device config text for h.
    Render(h *model.Host, n *model.Network, s SecretSource) (string, error)
}

// SecretSource resolves per-host secrets (Vault in production, a
// deterministic fake in tests). Mirrors Python BaseHost.secret().
type SecretSource interface {
    HostSecret(hostname, key string) (string, error)
}

// go/barf/device — READ ONLY
type Status struct {
    Version string
    Uptime  string
    Model   string
}

type Reader interface {
    Status(ctx context.Context) (Status, error)
    // RunningConfig returns device config text (or a vendor-native dump).
    RunningConfig(ctx context.Context) (string, error)
}
```

`SecretSource` lives in `render` so `render` has no dependency on `vault`;
`cli` wires the concrete Vault implementation in.

## Golden parity — the acceptance test for `render`

`projects/barf/tests/golden/*.conf` are byte-exact renders produced by the
Python implementation with deterministic fake secrets:

- host secrets render as `SECRET-host-<hostname>-<key>`
- `VaultSecrets` attribute lookups render as `VAULT-<key>`
- wireguard keys render as `PUB-...`/`PRIV-...`

A Go render of the same host with the same fake `SecretSource` must match
its golden byte-for-byte. Where full parity is not reachable in scope, get
as far as possible and **report precisely which goldens match and which do
not, and why** — a partial port with an honest parity table is the desired
outcome, an inaccurate claim of parity is not.

## Verification each worker must run

```sh
cd <worktree>
go build ./go/...
go vet ./go/...
go test ./go/...
```

Everything must build and vet clean before you report done. Add unit tests
for your own package; table-driven tests preferred.
