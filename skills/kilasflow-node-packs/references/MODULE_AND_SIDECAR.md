# Module packs, tenant scope and the sidecar

The three parts of a pack that are not the declarative manifest: a WebAssembly
module (the only pack kind whose behaviour is code this repository did not
write), the scope that decides which tenants see a type, and the JavaScript
sidecar that runs programmatic community nodes. Sources: internal/nodepack,
internal/wasmpack, internal/node/visibility.go, internal/config,
cmd/kilasflow/sidecar.go.

## Module packs

A wasip1 module this repository did not write, plus a manifest that says what it
may do. It declares its own parameters like any pack, and a parameter that scopes
itself to resources or operations is refused, as is `requestDefaults`: a module
has no cascade to scope by and makes its own requests.

| Field | Required | Meaning |
| --- | --- | --- |
| `file` | yes | Bare `.wasm` filename beside the manifest: no separator, no `..`. |
| `sha256` | yes | Lowercase hex SHA-256 of that file. The operator approves a digest, not a filename. |
| `abi` | yes | Must equal `v1`, the SDK's `ABIVersion`; a module built against another ABI is refused rather than run against an interface it was not compiled for. |
| `mode` | no | `item` (default: once per input item) or `batch` (once with every item). |
| `outputs` | no | At most 16 ports, lowerCamelCase and unique; defaults to one `main`. |
| `capabilities` | no | `http`, `binary.read`, `binary.write`. Empty means the module can reshape items and nothing else. |
| `credentials` | no | Credential types the module may read non-secret fields from. Each must exist in this build **and** sign an HTTP request: an authenticated request is a module's only way out. |
| `limits` | no | `timeoutSeconds`, `memoryPages`, `maxOutputBytes`, `maxHostCalls`, each at or below `Ceilings()`; a field left out takes `DefaultLimits`, never "unbounded" (internal/wasmpack/limits.go). |

What the loader does, in order (module.go; internal/wasmpack/registry.go, `Add`;
audit.go): read the manifest and its checksum, read the module file, check its
SHA-256 against `module.sha256`, compile it once through the deployment's
translation cache and read its imports, then register the node bound to the pack
executor. Every host function it reaches must be one its capabilities grant, and
that refusal happens at boot naming the pack and the missing capability — not at
run time, where it would surface as a workflow error. A duplicate
`{type, version}` is refused rather than replaced, and a pack declaring no
capability imports no host function at all.

The guest side is `pkg/sdk`: `sdk.Main(run)` reads one invocation envelope and
writes one document, over the same item shape the Code node uses, and the host
module it may import from is `kilasflow_v1` (pkg/sdk/abi.go; pkg/sdk/doc.go).
Saving a workflow never invokes a module — validation at save time is the
declarative one, so a save never runs third-party code.

## Tenant scope

`visibleTo` scopes a node **type** to a list of tenant IDs. The scope belongs to
the type rather than one version of it: two versions must declare the same set,
and a version declaring another set is refused at registration
(internal/node/visibility.go, `checkScopeAgrees`). The operator overrides it with
`packs.visible_to` (`KILASFLOW_PACKS_VISIBLE_TO`), entries written
`"<node type>=<tenant id>"`: that list replaces the manifest's set rather than
merging with it, applies to any registered pack including one embedded in the
binary, has no wildcard, and needs one entry per type — a trigger is a type of
its own, so a WAHA install names `pack.waha` and `pack.wahaTrigger`
(internal/config/config.go, `Packs.VisibleTo`; internal/config/packs_visibility.go).

Refusals, all at boot and all failing closed: a malformed entry reported as
`packs.visible_to[N]` (a missing `=`, an empty node type, an empty tenant ID, a
tenant ID containing `=`); an entry naming a type that is not registered; an
entry naming a built-in, because the engine names some of them
(`ApplyVisibility`); an empty `visibleTo` in a manifest.

A tenant not in the set sees the type exactly as if it were not registered:
absent from `list-node-types`, and the same 404 from the icon route and the
option loaders, with no response naming the tenants a type is reserved for
(internal/api/handlers/nodes.go, `List`, `Icon`). A scoped type's icon is served
`private`, because a shared cache holding one tenant's artwork is a cross-tenant
read. A document referencing the type is refused with `node.not_available`,
distinct from a type that does not exist, and the worker re-checks at claim —
which is why every process needs the same `packs.visible_to`.

## The JavaScript sidecar

Programmatic community nodes — a real `execute()` a declarative pack cannot
replicate — run in Node processes outside the server binary, off by default
(internal/config/config.go, `Sidecar`; cmd/kilasflow/sidecar.go).

| Key (env `KILASFLOW_SIDECAR_*`) | Default | Meaning |
| --- | --- | --- |
| `enabled` | `false` | Off is byte-for-byte the deployment the section did not change: no Node process, no runtime directory, no PATH lookup. |
| `node_path` | `""` = PATH | The operator-installed Node binary. |
| `packages_dir`, `packages` | `""`, empty | The directory holding the packages and the allowlist to load; both required when enabled, and transitive dependencies are never loaded. |
| `runtime_dir`, `wrapper` | `./data/sidecar`, empty | The extracted runner and one socket directory per process (writable, local), and an argv prefix for an isolation tool — the deployment's own boundary, not a guarantee this build makes. |
| `timeout`, `spawn_timeout`, `idle_timeout` | 30s, 15s, 5m | One warm run, one cold start, and how long a tenant's process stays warm. |
| `max_heap_mb`, `max_rss_mb`, `max_processes`, `max_output_bytes` | 256, 512, 16, 4194304 | The child's heap ceiling, the resident-set watchdog (0 disables it), the pool's process count, and the payload one run may return. |

The catalogue is read from the packages at boot, so every process that boots with
the sidecar on needs Node and the packages then. Its nodes are tagged `sidecar`,
distinct from `builtin` and `pack` (`SourceSidecar`, internal/node/registry.go).
One process per tenant, so a process started for one tenant is unreachable from
another by construction; a child that crashes, hangs or exceeds a limit fails
that node run with a named diagnostic while the rest of the execution and the
host process stay intact, and a deployment that cannot run them reports the node
as `unavailable` with the reason rather than hiding it (cmd/kilasflow/sidecar.go,
`Availability`).
