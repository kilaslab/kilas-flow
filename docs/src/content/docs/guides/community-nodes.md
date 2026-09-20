---
title: Community nodes
description: The two paths for third-party nodes — native WASM packs and the JavaScript sidecar — and the two SDKs, which are not each other.
---

## Two SDKs, different audiences

Two unrelated things in this repository share the word SDK. They have
different audiences, different runtimes, and neither is a version of the
other:

| | Guest SDK | Host SDK |
|---|---|---|
| Import | `github.com/kilaslabs/kilas-flow/pkg/sdk` (Go, the module path in `go.mod`) | `@kilasflow/sdk` (npm, TypeScript — not published yet, see the [SDK README](https://github.com/kilaslab/kilas-flow/blob/main/sdk/README.md)) |
| Audience | Node **authors** shipping a pack | Developers **embedding** KilasFlow in a host app |
| Runs | Inside the WASM sandbox, one function per call | Outside the server, against its HTTP API |
| Ships in | The pack author's `.wasm` module | The host application's `node_modules` |

If you are writing a node, you want the left column. If you are driving
workflows from your own product, you want the right one.

## Path one: native packs on WebAssembly

A pack author imports `pkg/sdk`, writes a run function over workflow items,
and builds a `wasip1` module:

```sh
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o pack.wasm ./...
```

```go
func run(items []sdk.Item) ([]sdk.Item, error) { /* ... */ }

func main() { sdk.Main(run) }
```

The contract is the Code node's batch contract: items arrive as one JSON
document on stdin, one envelope document leaves on stdout. The module needs
nothing beyond WASI's standard streams, which is what keeps the capability
surface auditable — a pack that declares no host capabilities is granted
none. An example pack proving the path lives at `pkg/sdk/example/echo` in
the repository.

Per-call limits are enforced on the host side — wall clock, linear memory,
output bytes — and exceeding one fails that node run with a named error,
never the process. The pack format (manifest plus `.wasm` modules, checksum
pinning, composition-time install) is owned by the pack loader; this page
covers only the author's side of it.

## Path two: the JavaScript sidecar

Programmatic community nodes — the ones with a real `execute()` a
declarative pack cannot replicate — run in Node.js processes outside the
server binary. Host and sidecar speak NDJSON over a Unix socket; the child's
stdout and stderr are diagnostics only and never parsed, so a package that
prints a startup banner cannot corrupt a message. The protocol is clean-room:
no community package, framework, or type definition crosses into this
repository.

Three rules govern the boundary:

- **One process per tenant.** Decrypted secrets cross the socket into
  third-party JavaScript, and process isolation is the only isolation left
  once they do. A run for tenant B never reaches a process started for
  tenant A.
- **Deny by default.** Outbound HTTP from a community node must be proxied
  back through the host's egress policy; until that proxy exists, a node
  that reaches past the boundary has its run refused rather than silently
  widened.
- **Node failure, not engine failure.** A sidecar that crashes, hangs, or
  exceeds its memory, wall-clock, output, or host-call limit fails that node
  run with a diagnostic naming the cause. The rest of the execution and the
  host process are intact.

## What the deployment looks like

The runtime image today is a single `CGO_ENABLED=0` Go binary on distroless,
and that stays the default: without a sidecar configured, sidecar-tagged
runs fail with a diagnostic naming what the operator must install, and
everything else is unchanged. Enabling the sidecar ends the single-binary
promise for that deployment — a second image or stage carrying Node 24 LTS
and the operator's chosen packages — which is why it is opt-in. The host
reference is `sidecar/` in the repository: protocol, per-tenant pool,
limits, and the `fixture/echo.js` node that proves the path headless.

## Licence position

The sidecar is operator-installed, never KilasFlow-distributed. KilasFlow
ships no JavaScript runtime, no dependency tree, and no community package;
the operator installs Node and the packages they choose. Nothing in the
protocol derives from Sustainable-Use-Licensed code, and the repository's
licence boundary (see `.pine/memory/licensing.md`) holds: no foreign source
in the tree, no foreign package in any manifest, no foreign bytes in any
shipped artifact.
