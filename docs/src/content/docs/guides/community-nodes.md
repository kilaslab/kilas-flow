---
title: Community nodes
description: The two paths for third-party nodes — native WASM packs and the JavaScript sidecar — and the two SDKs, which are not each other. Neither host half ships yet; this page says exactly what exists and what does not.
---

## Two SDKs, different audiences

Two unrelated things in this repository share the word SDK. They have
different audiences, different runtimes, and neither is a version of the
other:

| | Guest SDK | Host SDK |
|---|---|---|
| Import | `github.com/kilaslab/kilas-flow/pkg/sdk` (Go, the module path in `go.mod`) | `@kilasflow/sdk` (npm, TypeScript — not published yet, see the [SDK README](https://github.com/kilaslab/kilas-flow/blob/main/sdk/README.md)) |
| Audience | Node **authors** shipping a pack | Developers **embedding** KilasFlow in a host app |
| Runs | Inside a WASM module, one function per call — the author side is written, but no host loads packs yet | Outside the server, against its HTTP API |
| Ships in | The pack author's `.wasm` module | The host application's `node_modules` |

If you are writing a node, you want the left column. If you are driving
workflows from your own product, you want the right one.

The guest SDK is a real package with a real example, described below. The
host half that would load a pack and run it is not built — see
[What is not shipped yet](#what-is-not-shipped-yet).

## Path one: native packs on WebAssembly

The **author side** of this path exists. A pack author imports `pkg/sdk`,
writes a run function over workflow items, and builds a `wasip1` module:

```sh
GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o pack.wasm ./...
```

```go
func run(items []sdk.Item) ([]sdk.Item, error) { /* ... */ }

func main() { sdk.Main(run) }
```

The package is implemented, not a placeholder. `Main` reads one JSON document
from stdin, calls `run`, and writes one envelope document to stdout; `Handle`
is the same step as pure bytes-in/bytes-out, so a pack's logic is unit-testable
without a sandbox. The contract is the Code node's batch contract, and the
hand-built example pack proving the path lives at `pkg/sdk/example/echo`.

What keeps the capability surface auditable is that the guest has nothing but
WASI's standard streams. `internal/runcode` instantiates
`wasi_snapshot_preview1` and a module config carrying stdin, stdout, stderr
and the system clocks — no filesystem, no environment, no arguments, and no
host module. A pack therefore cannot call an API, read a credential, or reach
anything beyond the items it was handed. That is deliberate for the Code node,
and it is exactly the property the pack path would have to extend on purpose.

The **host side ships now**: a pack directory whose manifest carries a `module`
object is loaded, audited and run, so an operator can install one and use its
node like any other. The manifest, the limits, the capabilities and the
credential rules are in [the pack format reference](/reference/node-packs/#module-packs);
what is still missing is the build tooling that would compile a guest for you
and the declared-property conveniences a native node has — the gaps are listed
under [What is not shipped yet](#what-is-not-shipped-yet).

## Path two: the JavaScript sidecar

`sidecar/` is the host side of this boundary, and the server now wires it.
Programmatic community nodes — the ones with a real `execute()` a declarative
pack cannot replicate — run in Node.js processes outside the server binary,
and an operator turns that on with the `sidecar` configuration section
(disabled by default; see [the JavaScript
sidecar](/operate/javascript-sidecar/) for what to install). Host and sidecar
speak NDJSON over a Unix socket;
the child's stdout and stderr are diagnostics only and never parsed, so a
package that prints a startup banner cannot corrupt a message. The protocol is
clean-room: no community package, framework, or type definition crosses into
this repository.

Three rules govern the boundary, and each is implemented in the package:

- **One process per tenant.** `Pool` is keyed by tenant: a process started for
  tenant A is unreachable from a request for tenant B by construction. Decrypted
  secrets cross the socket into third-party JavaScript, and process isolation is
  the only isolation left once they do.
- **Deny by default.** A child frame the protocol does not expect is a host
  call, and no host call is served at this layer: the first one fails the run.
  Outbound HTTP from a community node must eventually be proxied back through
  the host's egress policy, and until that proxy exists the boundary refuses
  rather than silently widening.
- **Node failure, not engine failure.** A sidecar that crashes, hangs, or
  exceeds its memory, wall-clock, output, or frame limits fails that node run
  with a named diagnostic. The rest of the execution and the host process are
  intact.

The operator surface exists: a community package is loaded through its
`package.json` `n8n` manifest, its nodes appear in the catalogue tagged
`sidecar` (distinct from `builtin` and `pack`), and their outbound HTTP goes
through the same egress policy a native node's does — the credential's allowed
domains, the redirect chain and the response cap included. It is off until the
`sidecar` section enables it, and it needs a Node binary the operator
supplies.

## What is not shipped yet

Neither community path is an operator path today. Each statement below was
checked against this tree:

- **A module pack is a directory you build yourself.** There is no tooling that
  compiles a guest for you: you build the `.wasm` with
  `GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build`, pin its digest in the
  manifest, and install the directory. `nodepackgen validate` checks the
  manifest, not the build.
- **The module's properties are the manifest's.** A module pack declares its
  parameters like any pack, but it cannot register load-options, dynamic
  property kinds or a custom editor surface the way a built-in node can: the
  node's shape is what its manifest says, and the module sees resolved
  parameters.
- **The ABI carries whole-numbered versions only.** A node type version such as
  `4.2` is legal in the format but not on the pack ABI, which carries an
  integer; a module pack at a fractional version is refused rather than
  truncated.
- **The sidecar is not in the default image.** The runtime image stays a
  single `CGO_ENABLED=0` Go binary on distroless. Enabling the sidecar means
  running an image with Node in it and installing the packages yourself; a
  deployment that does not enable it is unchanged.

The pack path above is an author-side contract today, not an operator path.
The sidecar path is an operator path now — off by default, documented in
[the JavaScript sidecar](/operate/javascript-sidecar/).

## What the deployment looks like

The runtime image today is a single `CGO_ENABLED=0` Go binary on distroless,
and that stays the default. The WASM pack path is not wired, so it changes
nothing in a deployment; the sidecar path is wired and off by default, so it
changes nothing either until an operator enables it. The pack install path that does exist
today is the declarative one: a directory of `pack.json` manifests loaded at
composition through `packs.dir`, with a `pack.sha256` checksum pinning each
manifest, and it has no `.wasm` story.

Wiring the sidecar would end the single-binary promise for that deployment — a
second image or stage carrying Node 24 LTS and the operator's chosen packages
— which is why it is opt-in by design. The host reference for that work is
`sidecar/` in the repository: protocol, per-tenant pool, limits, and the
`fixture/echo.js` node that proves the path headless.

## Licence position

The sidecar is operator-installed, never KilasFlow-distributed. KilasFlow
ships no Node.js runtime, no dependency tree, and no community package;
the operator installs Node and the packages they choose. (The Code node's
JavaScript runs on an engine linked into the binary, in worker processes the
server starts from its own executable, and never in the sidecar.) Nothing in the
protocol derives from Sustainable-Use-Licensed code, and the repository's
licence boundary (see `.pine/memory/licensing.md`) holds: no foreign source
in the tree, no foreign package in any manifest, no foreign bytes in any
shipped artifact.
