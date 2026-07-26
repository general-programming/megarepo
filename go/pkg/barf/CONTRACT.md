# barf-go: package contract

Go port of `projects/barf` (Python), read surfaces only. This document is
the coordination contract between parallel implementation efforts: each
package below is owned by exactly one worker, and cross-package types are
frozen here so packages can be written independently.

Module root: `github.com/general-programming/megarepo`
Go: 1.25 (toolchain 1.26 available)

## Hard constraints (all packages)

1. **Modifying a network device is always explicit, never implicit.**
   This was originally "nothing may modify a device"; deploy has since
   landed, so the rule is now structural rather than absolute:
   - Every write path is gated on an explicit `AllowWrites` opt-in whose
     zero value writes nothing.
   - Write capability lives in named types (`device.VyOSWriter`,
     `device.EOSWriter`, `lifecycle`), never in a reader. `device.New`
     returns a `Reader` and can never return a writer.
   - Each write surface has its own closed allowlist of endpoints and
     command shapes. See "The write guards" below — those are the
     load-bearing detail, and they must not be merged.

   Readers remain read-only verbs only (`show ...`, config *retrieve*).
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

Rule 4 ("stay inside your own package directory") was a *coordination*
rule for the parallel build, not a design rule. It is now retired: the
packages are merged, and the local interfaces and re-implementations it
produced are being folded back together.

### Shared packages added during deduplication

| package | holds | why there |
|---|---|---|
| `go/common/pytext` | `SplitLines`, `ShellSplit`, `ShellQuote` | Go equivalents of Python stdlib text primitives, differentially validated against CPython. Domain-free, so monorepo-wide is correct. A leaf package, NOT the `go/common` root: that root pulls in redis and zap, which barf must not inherit. |
| `go/pkg/barf/vyoswire` | the VyOS form-POST wire protocol | Vendor-specific, so it stays inside barf. Codec and HTTP call only — it holds no allowlist (see below). |

`go/common` is **monorepo-wide** and shared with non-barf Go tools. Only
genuinely generic, domain-free utilities may go there. Network-automation
or vendor-specific code gets deduplicated *within* `go/pkg/barf` instead;
`vyoswire` is the worked example.

## The write guards (do not merge these)

Three independent, closed allowlists decide whether anything can change a
device. They are separate on purpose and must stay separate:

| guard | package | permits |
|---|---|---|
| `vyosRequestAllowed` | `device` (reader) | `show`, `retrieve` only |
| `vyosWriteRequestAllowed` | `device` (writer) | `configure`, `config-file`+save only; reachable only via `NewVyOSWriter`, which requires `Options.AllowWrites` |
| `apiEndpoint.write` | `lifecycle` | `/image` delete, gated on `APIOptions.AllowWrites` |

`device.Options.AllowWrites` has no effect on the **read** guard, and no
option turns a reader into something that can name a write endpoint.

All three now share their transport plumbing via `vyoswire`, which is
safe precisely because `vyoswire` has no allowlist of its own: it takes a
fully-formed URL and posts to it. Each guard still runs in its own
package, before it builds that URL. **Do not move a guard into
`vyoswire`** — that would collapse three separate decisions into one
switch, which is the property worth keeping.

### Refusal errors

`device.ErrWritesNotAllowed` and `device.ErrWriteAttempt` are sentinels;
`WritesNotAllowedError`, `WriteAttemptError` and `UnmanagedCommandError`
carry the detail and `Unwrap()` to them (the shape `client/vault` already
used). `lifecycle.ErrWritesNotAllowed` **is** the device sentinel, so
`errors.Is` works across both packages. `device.IsRefusal(err)` answers
the safety-critical question — "was the device left untouched?" —
regardless of which guard refused.

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
