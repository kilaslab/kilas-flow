---
title: Node pack format
description: Every field of a node pack manifest, what the loader does with it, and what each refusal means.
sidebar:
  order: 3
---

A pack is a JSON manifest plus a checksum sidecar, laid out as one directory
per pack. The manifest is what `nodepack.Decode` reads with unknown fields
rejected; the sidecar is what the loader verifies before anything else. The
tutorial that builds one from nothing is
[Authoring a community node pack](/guides/node-authoring/); this page is the
reference it points at.

The on-disk unit:

```text
<packs dir>/<name>/pack.json    the manifest Decode reads
<packs dir>/<name>/pack.sha256  the hex SHA-256 of pack.json
<packs dir>/<name>/<file>.wasm  only for a module pack: the compiled module
```

A pack is one of three kinds, and the manifest says which: a **resource pack**
(`resources`), a **trigger pack** (`trigger`), or a **module pack** (`module`).
Exactly one of the three, because they are three different ways of being a
node rather than three features of one.

## Manifest fields

The top-level object accepts exactly these fields — anything else fails the
strict decoder. Required unless marked optional.

| Field | Required | Meaning |
|---|---|---|
| `type` | yes | Node type string. Must not claim the reserved `kilasflow.` namespace. |
| `version` | yes | Positive type version. The registry resolves downward: requesting a version with no exact registration gets the nearest lower one. |
| `displayName` | yes | Picker name. |
| `description` | no | Picker description. |
| `category` | yes | Top-level picker section. |
| `icon` | no | `builtin:<name>` glyph, or shipped artwork. |
| `iconColor` | no | Picker accent. |
| `subtitle` | no | Template over the node's own parameters only — `{{ $parameter.<key> }}`. Anything else is refused. |
| `documentationUrl` | no | Link shown in the editor. |
| `credentialType` | no | Credential type this pack authenticates with, named by string. The operator binds a real credential after installation; the pack never carries one. |
| `visibleTo` | no | Tenant IDs this pack's node type is visible to. Absent means every tenant; an empty list is refused. See [Tenant-scoped nodes](/guides/tenant-scoped-nodes/). |
| `module` | no | Makes this a module pack: a WebAssembly module beside the manifest. See [Module packs](#module-packs). Mutually exclusive with `resources` and `trigger`. |
| `requestDefaults` | yes | `baseURL`, plus `headers`/`qs`/`body`/`path` shared by every operation. Templates over non-secret `$credentials` fields. |
| `trigger` | no | When set, this is a webhook trigger node: events instead of resources, no routing description. |
| `parameters` | yes | The node's properties, one per key (duplicates refused). |
| `resources` | no | Required for action nodes; absent for triggers. |
| `generator` | yes | Provenance block: `tool`, `source`, `sourceTitle`, `sourceVersion`, `sourceDigest`. Hand-written packs carry `{"tool": "nodepackgen", "source": "scaffold"}`. Do not copy a generated pack's provenance. |

A resource accepts `name`, `description`, `operations`. An operation accepts
`name`, `description`, `method`, `url`, `sends`, `output`, `pagination`.
`url` is appended to the request defaults' base URL; `{name}` placeholders are
filled from `path` parameters, one escaped segment each.

A parameter accepts `key`, `label`, `description`, `kind`, `required`,
`default`, `options`, `typeOptions`, `resources`, `operations`. Both
`resources` and `operations` are required scoping: an operation name alone
does not identify an operation, because names repeat across resources.
`options` entries are `value`/`label` pairs. `key` must not be `resource` or
`operation` — those are reserved for the pack's own cascade.

A trigger accepts `events`, `catchAll`, `eventPath`, `shape`, `webhook`,
`hmac`, `lifecycle`, `media`, `notice`. Events are in output-index order with
the catch-all last; `hmac.algorithm` is `sha512`; `lifecycle.set` is the
method/URL/headers/body/credentialType the server PUTs on activation.

Property kinds are a closed set: `string`, `number`, `boolean`, `options`,
`multiOptions`, `collection`, `fixedCollection`, `notice`, `json`, `dateTime`,
`resourceLocator`, `resourceMapper`, `keyValue`, `conditions`,
`assignmentCollection`.

`typeOptions` accepts `password`, `rows`, `minValue`, `maxValue`,
`numberPrecision`, `multipleValues`, `multipleValueButtonText`, `editor` and
`editorLanguage`; any other key is refused. `"editor": "code"` renders a
`string` parameter as a source editor with line numbers, indentation and
highlighting, and needs `editorLanguage`: one of `javaScript`, `go`,
`python` or `json`. A code parameter never switches to expression mode,
because a `{{ }}` or a leading `=` in a program is part of the program.

## Install and distribution

Each immediate subdirectory of the packs directory (`packs.dir`,
`KILASFLOW_PACKS_DIR`) is one pack. Loading happens at composition, before
the registry is shared; an absent or empty directory is a normal, silent
condition. The checksum sidecar is generated by the tooling
(`nodepackgen pack -dir <pack dir>` writes it in `sha256sum`-compatible
shape), never by hand — the check is against something a human approved
rather than against the file's own claim about itself.

Every failure names the pack and the reason and refuses the whole boot: an
unreadable manifest, a missing sidecar, a digest mismatch, a malformed pack,
or a duplicate registration. The server never runs with a half-registered
catalogue. Hot reload and remote installation are out of scope: the registry
is read-only once serving begins, and boot never fetches over the network.

Licence position, matching the executable-sidecar rule: packs are
operator-installed, never KilasFlow-distributed. You distribute your JSON; an
operator places it and approves its checksum. Keep the format-versus-code line
while writing — parameter shapes and routing metadata are interoperability
facts, implementation source is not.

### Per-tenant visibility

A pack can be scoped to a set of tenants so that only they see or run its node
type: give the manifest a `visibleTo` array of tenant IDs. The scope belongs to
the node *type*, so every version of the type shares it, and an absent field
keeps the type visible to every tenant. An operator can also override the list
at startup with `packs.visible_to`, which replaces the manifest's set for a
type and applies to packs embedded in the binary as well as directory packs.
Editing `pack.json` changes its bytes, so regenerate the `pack.sha256` sidecar
before restarting. The declaration, the override, and what a scoped-away tenant
sees are in [Tenant-scoped nodes](/guides/tenant-scoped-nodes/).

## Troubleshooting

`nodepackgen validate` collects every problem at once with file and JSON path.
These are the errors authors actually hit, with the diagnostics verbatim:

Unknown field — usually `properties` where the schema wants `parameters`, or
a routing block at the wrong depth:

```text
pack.json: $.: unknown field "properties": want one of type, version, displayName, description, category, icon, iconColor, subtitle, documentationUrl, credentialType, visibleTo, requestDefaults, trigger, parameters, resources, generator
```

Reserved type namespace — only built-in nodes may use it:

```text
pack.json: $.type: node type "kilasflow.evil" claims the reserved "kilasflow." namespace, which only built-in nodes may use
```

Reserved cascade key — `resource` and `operation` belong to the generated
cascade:

```text
pack.json: $.parameters[2].key: "resource" is reserved for the pack's own cascade
```

Duplicate parameter — one property per key:

```text
pack.json: $.parameters[2].key: node pack "pack.example" declares parameter "chatId" twice
```

Unknown property kind — the closed set is listed:

```text
pack.json: $.parameters[0].kind: parameter "chatId" has unknown kind "fancy": want one of string, number, boolean, options, multiOptions, collection, fixedCollection, notice, json, dateTime, resourceLocator, resourceMapper, keyValue, conditions, assignmentCollection
```

Refused routing hook — JavaScript closures have no data representation:

```text
pack.json: $: routing for pack.example: resource "message" operation "sendMessage": preSend hooks are JavaScript and are not supported; remove scrub
```

Unsupported post-receive action — only `rootProperty`, `setKeyValue`,
`limit` and `binaryData` are implemented:

```text
pack.json: $: routing for pack.example: resource "message" operation "sendMessage": postReceive action "" is not supported; use rootProperty, setKeyValue, limit or binaryData
```

Subtitle reading anything but parameters:

```text
pack.json: $: node definition "pack.example" subtitle may only read $parameter.<key>, got "$json.foo"
```

Checksum mismatch — the manifest changed after approval:

```text
pack.sha256: $: pack "acme": pack.json no longer matches its recorded digest: regenerate it or restore the approved manifest
```

Icon artwork is refused at registration for containing a script element, a
`foreignObject`, an event handler attribute, or an external or `javascript:`
URL. Prefer `builtin:<name>` glyphs, which ship no bytes at all.

## Module packs

A module pack is a wasip1 module the repository did not write, plus the
manifest that says what it may do. It is the only pack kind that runs code, so
every rule below is enforced twice: once by `nodepackgen validate` while the
author is writing it, and once by the loader at boot. A manifest the tool
accepts is one the server accepts.

```json
{
  "type": "acme.enrich",
  "version": 1,
  "displayName": "Enrich",
  "category": "Transform",
  "parameters": [{ "key": "url", "label": "URL", "kind": "string", "required": true }],
  "module": {
    "file": "enrich.wasm",
    "sha256": "9f2c…",
    "abi": "v1",
    "mode": "item",
    "outputs": [{ "name": "main", "displayName": "Main" }],
    "capabilities": ["http"],
    "credentials": [{ "type": "httpHeaderAuth", "required": true }],
    "limits": { "timeoutSeconds": 30, "memoryPages": 512, "maxOutputBytes": 8388608, "maxHostCalls": 100 }
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `file` | yes | The module's filename beside the manifest. A bare name ending `.wasm`: no directory separators, no `..`. |
| `sha256` | yes | Lowercase hex SHA-256 of that file. The loader refuses a module whose bytes do not match: the operator approves a digest, not a filename. |
| `abi` | yes | The host ABI the module was built against. Must equal the SDK's `ABIVersion`; a module built against another one is refused rather than run against an interface it was not compiled for. |
| `mode` | no | `item` (default) calls the module once per input item; `batch` calls it once with every item. |
| `outputs` | no | Output ports, at most 16, names `lowerCamelCase` and unique. Defaults to one `main` port. A module that writes to more ports than are declared is refused. |
| `capabilities` | no | What the module may call: `http`, `binary.read`, `binary.write`. Empty means it can reshape items and nothing else. |
| `credentials` | no | Credential types the module may name and read non-secret fields from. Each must exist in this build **and** sign an HTTP request — a module's only way out is an authenticated request, so a database credential is refused with that reason. |
| `limits` | no | Bounds for one run, each at or below its ceiling (`Ceilings()` in `internal/wasmpack`): wall clock, memory pages, output bytes, host calls. A field left out takes the shipped default. |

### What the loader does with a module

1. Reads the manifest and its checksum, then the module file.
2. Checks the module's SHA-256 against `module.sha256`.
3. Compiles it once through the deployment's translation cache and reads its
   imports: every host function it reaches must be one its `capabilities`
   grant. A module that imports `kilasflow_v1.http_request` without `http` is
   refused **at boot**, naming the pack and the missing capability — not at run
   time, where the failure would be a workflow error.
4. Registers the node bound to the pack executor. A pack cannot name its own
   executor, and `Register` refuses a module pack outside the loader, because
   the bytes live beside the manifest and only the loader reads a directory.

Validation at save time is the declarative one — required parameters, kinds,
options, credential requirements — and the module is **not invoked**: a save
never runs third-party code. What a run may do is bounded by the capabilities,
the limits and the credential list above, and the host applies the credential
and the egress policy itself rather than handing either to the module.
