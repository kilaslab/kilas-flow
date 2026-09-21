# Pack format

A pack is a JSON manifest plus a checksum sidecar, one directory per pack, loaded
at boot. This file is the directory layout, the manifest fields and the refusals
the loader produces; the module and sidecar halves are in
MODULE_AND_SIDECAR.md. Source of every rule: `internal/nodepack`.

## The directory

```
<packs dir>/<name>/pack.json    the manifest Decode reads
<packs dir>/<name>/pack.sha256  the hex SHA-256 of pack.json
<packs dir>/<name>/<file>.wasm  only for a module pack: the compiled module
```

`LoadDir` walks each immediate subdirectory of the packs directory (`packs.dir`,
`KILASFLOW_PACKS_DIR` — internal/config/config.go, `Packs`), in filename order.
An absent or empty directory is a silent no-op, so the default deployment is
unchanged; everything else — an unreadable manifest, a missing or mismatched
checksum, a malformed pack, a duplicate registration — names the pack and refuses
the boot, because a half-registered catalogue leaves workflows naming a type that
can never activate. The manifest is decoded with unknown fields refused, so a
field written against a newer format fails at load rather than being dropped in
silence (`Decode`, nodepack.go).

## The manifest

A pack is exactly one of three kinds and the manifest says which: a resource pack
(`resources`), a trigger pack (`trigger`) or a module pack (`module`). Declaring
two of them, or none, is refused (`Load` and `checkModule`, nodepack.go and
module.go).

| Field | Required | Meaning |
| --- | --- | --- |
| `type` | yes | Node type string. Must not claim the reserved `kilasflow.` namespace (`BuiltinPrefix`, internal/node/registry.go). |
| `version` | yes | Type version; the registry resolves downward to the nearest lower registration. |
| `displayName`, `category` | yes | Picker name and top-level section. |
| `description`, `documentationUrl`, `iconColor` | no | Picker text, link, accent. |
| `icon` | no | A `builtin:<name>` glyph or shipped artwork. Artwork is refused at registration if it carries a script, a `foreignObject`, an event-handler attribute or an external URL. |
| `subtitle` | no | Template under the node's name; the only root it may read is `$parameter` (`SubtitleRoot`, `validateSubtitle`, internal/node/registry.go). |
| `credentialType` | no | The credential type this pack authenticates with, by name. The pack never carries a value. |
| `visibleTo` | no | Tenant IDs this node type is visible to; absent means every tenant, empty is refused. |
| `requestDefaults` | action packs | `baseURL` plus the `headers`/`qs`/`body`/`path` every operation shares, templated over non-secret `$credentials` fields. |
| `trigger` | trigger packs | The event table instead of resources. |
| `parameters` | yes | The node's properties, one per key; duplicates are refused. |
| `resources` | action packs | Resource groups, each a list of `operations`. |
| `generator` | yes | Provenance: `tool`, `source`, `sourceTitle`, `sourceVersion`, `sourceDigest`. Do not copy a generated pack's provenance. |
| `module` | module packs | The WebAssembly declaration, in MODULE_AND_SIDECAR.md. |

A resource accepts `name`, `description`, `operations`; an operation accepts
`name`, `description`, `method`, `url`, `sends`, `output`, `pagination`. The
`url` is appended to the request defaults' base URL, with `{name}` placeholders
filled from path parameters, one escaped segment each.

## Parameters

A parameter is one property plus where it shows: `key`, `label`, `description`,
`kind`, `required`, `default`, `options`, `typeOptions`, `resources`,
`operations`. `key` must not be `resource` or `operation` — those belong to the
cascade the loader builds (`ResourceKey`, `OperationKey`, nodepack.go) — and a
parameter used by several operations is one property whose `resources` and
`operations` lists say where it shows, because an operation name alone does not
identify an operation: names repeat across resources. The loader adds the
`resource` and `operation` pickers itself, with one internal options loader per
pack keyed by type and version, so the operation list narrows to the chosen
resource (`OperationsLoaderName`). Kinds are a closed set — `string`, `number`,
`boolean`, `options`, `multiOptions`, `collection`, `fixedCollection`, `notice`,
`json`, `dateTime`, `keyValue`, `conditions`, `assignmentCollection`,
`resourceLocator`, `resourceMapper` (internal/property/property.go).

## Trigger packs

A trigger declares `events` (in output index order, which is the contract — a
connection names an output index), `catchAll` (where an unrecognised event goes),
`eventPath`, `shape`, `webhook`, `hmac` (only `sha512` is implemented),
`lifecycle` (optional auto-registration), `media` and `notice`. It carries no
routing description — a trigger makes no outbound request — and its executor is
the fan-out `core.packTrigger` (trigger.go). A pack never names its own executor.

## Checksums and boot refusals

`pack.sha256` holds the hex SHA-256 of `pack.json` in `sha256sum` shape — the
first whitespace-delimited field is the digest, the rest is ignored — so the
check is against something a human approved rather than the file's claim about
itself (loaddir.go, `checkPackDigest`).

| Refusal | Meaning |
| --- | --- |
| `pack.json is missing` | the directory has no manifest |
| `pack.sha256 is missing` / `holds no digest` / `is not a SHA-256 hex digest` | the sidecar is absent or unreadable |
| `pack.json no longer matches its recorded digest` | the manifest changed after approval: regenerate the sidecar or restore the approved bytes |
| `does not match the sha256` | a module's bytes differ from `module.sha256` |
| `not in the pack directory` | `module.file` is not a bare filename beside the manifest |
| `no WebAssembly pack runtime` | a module pack reached a loader with no module registrar |
| duplicate registration | two packs claim one `{type, version}` |

## Validating

`kilasflow pack validate <dir|pack.json>` runs the real loader and the real
registration against throwaway registries, so what it accepts is what the server
would register (internal/cli/verbs_pack.go; `registerThrowaway` in validate.go).
A directory is checked the way an install reads it: sidecar first, then manifest.
Every issue carries `severity` (`error`: everything it reports would stop an
install), `file`, the JSON `path` inside that file (for example
`$.resources[0].operations[1].method`) and the `message`. A missing path is a
usage error rather than an invalid pack — nothing was checked — and a pack with
issues exits 1 with `error.code = "invalid_pack"` plus the complete list under
`error.detail.issues`.

The same rules are reachable without a server, from the pack generator beside the
CLI: `nodepackgen validate <pack dir or pack.json>...`, `nodepackgen scaffold
-dir <packs dir>/<name>` to write a minimal working pack, and `nodepackgen pack
-dir <pack dir>` to regenerate the checksum sidecar after a deliberate edit
(cmd/nodepackgen/authorcmd.go).
