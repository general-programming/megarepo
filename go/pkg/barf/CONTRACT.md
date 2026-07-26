# barf-go: package contract

barf renders and deploys network device configs from
`projects/barf/network.yml`. It began as a port of the Python `projects/barf`,
which has since been deleted — `git log` before this commit is the only copy.
This document holds the invariants that are not expressible in the code itself.

Module root: `github.com/general-programming/megarepo`
Go: 1.25 (toolchain 1.26 available)

## Hard constraints

1. **Modifying a network device is always explicit, never implicit.**
   - Every write path is gated on an explicit `AllowWrites` opt-in whose
     zero value writes nothing.
   - Write capability lives in named types (`device.VyOSWriter`,
     `device.EOSWriter`, `lifecycle`), never in a reader. `vendor.NewReader`
     returns a `Reader` and can never return a writer; `vendor.NewWriter`
     is a separate function whose constructors still refuse without
     `AllowWrites`.
   - Each write surface has its own closed allowlist of endpoints and
     command shapes (see below). Readers permit read-only verbs only
     (`show ...`, config *retrieve*).
2. **`projects/barf/network.yml` is the fleet's source of truth.** It is all
   that remains of `projects/barf`; barf reads it and never rewrites it.
3. Secrets never get logged or written to disk. Vault values are used in
   memory only.

## Shared packages

| package | holds | why there |
|---|---|---|
| `go/common/pytext` | `SplitLines`, `ShellSplit`, `ShellQuote` | Go equivalents of Python stdlib text primitives, differentially validated against CPython. Domain-free, so monorepo-wide is correct. A leaf package, NOT the `go/common` root: that root pulls in redis and zap, which barf must not inherit. |
| `go/pkg/barf/vyoswire` | the VyOS form-POST wire protocol | Vendor-specific, so it stays inside barf. Codec and HTTP call only — it holds no allowlist (see below). |
| `go/pkg/barf/vendor` | the devicetype → capability table | One row per vendor, replacing four registries (`render.For`, `device.New`, `scope.For`, `cli.writerFactories`) that were one fact split four ways. It **composes** the small interfaces rather than replacing them. Imports `render`+`device`+`scope`; nothing below it may import it. |

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

All three share transport plumbing via `vyoswire`, which is safe precisely
because `vyoswire` has no allowlist of its own: it takes a fully-formed URL
and posts to it. Each guard runs in its own package, before it builds that
URL. **Do not move a guard into `vyoswire`** — that would collapse three
separate decisions into one switch, which is the property worth keeping.

### Refusal errors

`device.ErrWritesNotAllowed` and `device.ErrWriteAttempt` are sentinels;
`WritesNotAllowedError`, `WriteAttemptError` and `UnmanagedCommandError`
carry the detail and `Unwrap()` to them. `lifecycle.ErrWritesNotAllowed`
**is** the device sentinel, so `errors.Is` works across both packages.
`device.IsRefusal(err)` answers the safety-critical question — "was the
device left untouched?" — regardless of which guard refused.

## Frozen types (`go/pkg/barf/model`)

`model.Host`, `Interface`, `Network` et al mirror `projects/barf/network.yml`
and the Python `BaseHost`. The definitions live in `model/types.go`; field
names there are fixed by the yaml. Add fields freely, do not rename.

## Frozen interfaces

`render.Renderer` (`Render(h, n, SecretSource) (string, error)`) and
`device.Reader` (`Status`, `RunningConfig`) are the two cross-package
seams. `render.SecretSource` (`HostSecret(hostname, key)`, mirroring Python
`BaseHost.secret()`) lives in `render` so that `render` has no dependency
on `vault`; `cli` wires the concrete Vault implementation in.

## Render snapshots — the acceptance test for `render`

`go/pkg/barf/render/testdata/` holds byte-exact renders with deterministic
fake secrets:

- host secrets render as `SECRET-host-<hostname>-<key>`
- `VaultSecrets` attribute lookups render as `VAULT-<key>`
- wireguard keys render as `PUB-...`/`PRIV-...`

`golden/` covers every renderable host in `network.yml` — adding a host fails
the suite until its render has been reviewed once, and a snapshot whose host
disappeared fails too. `edgeos/` and `ios/` cover vendors the fleet has no
host for. The corpus was captured from the Python implementation and has been
authoritative from Go since it was deleted; it is a drift detector, not a
correctness oracle.

Changing a rendered config is expected to fail these. Regenerate and read the
diff — that diff is the review of what would reach a device:

```sh
go test ./go/pkg/barf/render -update && git diff go/pkg/barf/render/testdata
```

## Verification

```sh
go build ./go/...
go vet ./go/...
go test ./go/...
```

Everything must build and vet clean. Table-driven tests preferred.
