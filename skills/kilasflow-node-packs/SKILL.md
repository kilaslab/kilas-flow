---
name: kilasflow-node-packs
description: Use when authoring, validating or installing a node pack, whether a declarative manifest, a WebAssembly module pack or a JavaScript sidecar community node, or when a pack's node type must be visible to one customer only. Triggers on "pack", "pack.json", "WASM", "sidecar", "visibleTo".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow pack validate
  - kilasflow node list
  - kilasflow node describe
  - kilasflow node options
  - kilasflow api
kilasflow_operations:
  - list-node-types
  - load-node-property-options
  - get-node-icon
kilasflow_nodes:
  - kilasflow.unsupported
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No pack install verb: a pack directory is loaded from the configured paths at boot, and pack validate is the only pack verb'
---

## Non-negotiables

1. A pack arrives as files on disk, and the boot is what installs it: each immediate subdirectory of the packs directory is one pack, loaded at composition before the registry is shared, and anything that goes wrong names the pack and refuses the whole start (internal/nodepack/loaddir.go, `LoadDir`; internal/config/config.go, `Packs`). `kilasflow pack validate <dir|pack.json>` is the only pack verb, and it is local — no server, no credential, no request (internal/cli/verbs_pack.go).
2. The checksum sidecar is what makes an installed manifest approved rather than merely present. `pack.json` sits beside `pack.sha256`, the digest is checked before the manifest is decoded, and a mismatch refuses the boot (`checkPackDigest` in internal/nodepack/loaddir.go). Any edit to the manifest's bytes means regenerating the sidecar in the same change.
3. A pack never claims the `kilasflow.` namespace: only built-in registration may, because a pack registering into it could shadow — or be mistaken for — a node this project ships (internal/node/registry.go, `BuiltinPrefix`). That namespace is where the import placeholder `kilasflow.unsupported` lives: an imported node with no mapping becomes it, registered at arities 1, 2, 4 and 8 (nodes/unsupported.go; nodes/core.go). Never treat the placeholder as a shape a pack may reuse.
4. A pack names a credential *type* and nothing more. `credentialType` (or a module's `credentials` list) becomes a credential requirement on the definition, the operator binds a real credential afterwards, and the host — never the pack — applies it to the request (internal/nodepack/nodepack.go; internal/wasmpack/caps.go).

## Strong defaults

- Decide the kind first, because a pack is exactly one of them: an action pack with `resources`, a `trigger` pack with events, or a `module` pack whose behaviour is code. A manifest that declares two, or none, is refused at load (internal/nodepack/nodepack.go, `Load`; internal/nodepack/module.go, `checkModule`).
- Author a hand-written pack from the reference shape in `packs/telegram/pack.json`, or scaffold the smallest pack that still loads and runs with the pack generator under `cmd/nodepackgen` — it also writes the `sha256sum`-compatible sidecar, so nobody computes a digest by hand (internal/nodepack/author.go, `Scaffold`, `WritePackDir`, `WriteChecksum`).
- Validate before installing: `kilasflow pack validate ./packs/acme` checks a directory the way an install reads it — sidecar first, then manifest — and reports every problem at once, each with its file and JSON path, because one round trip per mistake is the failure it exists to prevent (internal/nodepack/validate.go, `ValidateDir`, `ValidateFile`). A refusal exits 1 with `error.code = "invalid_pack"` and the whole list under `error.detail.issues`.
- A module pack is a wasip1 module beside the manifest plus a declaration of what it may do: `file` (a bare `.wasm` name), `sha256`, `abi` (which must equal the SDK's `v1`), `mode` (`item` per input item, or `batch`), `outputs`, `capabilities`, `credentials`, `limits` (internal/nodepack/module.go; internal/wasmpack/spec.go).
- The guest side of that path is `pkg/sdk`: import it, write a run function over items, and build with `GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o pack.wasm ./...`. The host module a module imports from is `kilasflow_v1`, so a pack built against another ABI fails at instantiation rather than being silently satisfied (pkg/sdk/abi.go, `HostModule`, `ABIVersion`).
- Capabilities are the whole boundary, and they are enforced before anything runs: `http`, `binary.read` and `binary.write`, plus the credential types the module may name. The registry compiles the module once, reads its imports, and refuses any host function its capabilities do not grant at load time, naming the pack and the missing capability (internal/wasmpack/registry.go, `Add`; internal/wasmpack/audit.go). A credential a module declares must also sign an HTTP request, because an authenticated request is a module's only way out.
- Programmatic community nodes — the ones with a real `execute()` a declarative pack cannot replicate — run in the JavaScript sidecar: `sidecar.enabled` (`KILASFLOW_SIDECAR_ENABLED`, off by default, and a default install never looks for Node), with `packages_dir` and an explicit allowlist of `packages`. Those nodes appear in the catalogue tagged `sidecar`, distinct from `builtin` and `pack` (internal/config/config.go, `Sidecar`; cmd/kilasflow/sidecar.go; internal/sidecarnode/catalogue.go; internal/node/registry.go, `SourcePack`, `SourceSidecar`).
- Tenant-scope a pack with `visibleTo`, a list of tenant IDs, or override any registered type — embedded packs included — with the operator's `packs.visible_to` (`KILASFLOW_PACKS_VISIBLE_TO`, entries written `"<node type>=<tenant id>"`). The scope belongs to the node *type* rather than one version of it, the override *replaces* the manifest's set rather than merging with it, there is no wildcard, and an empty `visibleTo` is refused because it would read as nobody or as everybody (internal/nodepack/nodepack.go; internal/config/config.go, `Packs.VisibleTo`; internal/node/visibility.go).
- Give every process the same `packs.visible_to`. The API role narrows the catalogue, but the worker is the authority: a worker started without the key accepts and runs what the API would have refused (internal/config/config.go).
- Read back what the deployment actually serves rather than what the manifest says: `kilasflow node list` (`list-node-types`) is the catalogue narrowed to the caller's tenant, `kilasflow node describe pack.telegram` is one entry at the version the server would resolve, and `kilasflow node options <type> --property <key>` (`load-node-property-options`) resolves a property whose values the pack's own loader answers — a declarative pack registers one such loader for the operations of the chosen resource (internal/api/handlers/nodes.go; internal/nodepack/nodepack.go, `OperationsLoaderName`).
- A node reported `unavailable` in the catalogue is a deployment fact, not a manifest fault; the catalogue carries the reason, and for a sidecar node it is a missing or unusable Node binary on that process (cmd/kilasflow/sidecar.go, `Availability`). Node artwork is a route of its own, `get-node-icon`, and it answers a type the caller's tenant may not see exactly as it answers an unregistered one (internal/api/handlers/nodes.go, `Icon`).
- Anything without a verb is one call away: `kilasflow api <operation-id>` (`api`) reaches every operation the running server serves, with `--path name=value` for placeholders, `--body @file.json` for a body and `--list` to enumerate the surface.

## Decision tree

```
what are you doing?
|
+-- authoring a declarative pack
|     -> copy the shape of packs/telegram/pack.json, or scaffold with cmd/nodepackgen
|        kilasflow pack validate ./packs/acme
|        place the directory under packs.dir and restart: no reload, no remote fetch
|
+-- authoring a WebAssembly module pack
|     -> pkg/sdk: sdk.Main(run) over items, built with GOOS=wasip1 GOARCH=wasm
|        pin module.sha256, declare abi v1, capabilities and limits
|        validate the directory, then install it exactly like a declarative pack
|
+-- a node needs a real execute(), not a manifest
|     -> the JavaScript sidecar: sidecar.enabled, packages_dir, packages
|
+-- the boot is refused and names a pack
|     -> read the reason: a missing sidecar, a manifest that changed after approval,
|        a module whose bytes no longer match module.sha256, or a missing capability
|
+-- one customer cannot see a node that exists
|     -> visibility: the manifest's visibleTo and the operator's packs.visible_to
|
+-- nobody can see a node you expected
|     -> kilasflow node list, then kilasflow node describe <type>
|
+-- the node is there but its operation picker is empty
      -> the pack's own options loader needs the resource first:
         kilasflow node options <type> --property resource
```

## Not shipped yet

- No pack install verb: a pack directory is loaded from the configured paths at boot, and pack validate is the only pack verb — there is no runtime install, no hot reload and no boot-time fetch over the network. Put the directory under the configured packs directory (`packs.dir`, `KILASFLOW_PACKS_DIR`) or embed the pack in the binary, then restart the process; the registry is read-only once the server is serving.

## Anti-patterns

- "I'll add a step that installs the pack at runtime" → the registry is read-only once serving begins and no verb installs anything → place the directory under `packs.dir` and restart, or embed it in the binary.
- "I edited pack.json in place" → the digest no longer matches the bytes a human approved, so the boot refuses the pack → regenerate the sidecar in the same change (`WritePackDir`/`WriteChecksum`), never by hand.
- "The manifest decoded, so the pack is fine" → decoding is the first of several gates; registration against throwaway registries is the rest (known property kinds, visibility, subtitle templates, XSS-safe icons, routing hooks) → run `kilasflow pack validate` and read every issue, not the first.
- "A module can call whatever it needs at run time" → its imports are audited against its declared capabilities at load, so an undeclared host function refuses the pack instead of failing a workflow later → declare the capability and reinstall.
- "The pack carries the API key" → a pack names a credential *type*; the operator binds the credential and the host applies it → bind it in the deployment and let the workflow reference it.
- "An empty `visibleTo` means nobody sees it" → an empty list is refused at load, because it reads as nobody or as everybody → omit the field for every tenant, or name the tenants you mean.
- "The API hides the node, so the scope holds" → the API narrows the catalogue but the worker is the authoritative gate → give every process the same `packs.visible_to`.
- "I'll scope a built-in type too" → `ApplyVisibility` refuses a built-in, because the engine names some of them → scope only what a pack registered.
- "The sidecar node is there, so it works" → every process that boots with the sidecar on needs a Node binary and the allowlisted packages at startup → install both, and read the `unavailable` reason the catalogue reports when one is missing.
- "I'll trust what the manifest says about visibility" → the operator's override replaces it and every version of a type shares one scope → read the catalogue back with `kilasflow node list` as the tenant in question.

## Reference files

| File | Read when |
| --- | --- |
| PACK_FORMAT.md | you are writing or reviewing a `pack.json`, its checksum sidecar or its parameters, and need the manifest fields, the cascade rules and the loader's refusals |
| MODULE_AND_SIDECAR.md | you are writing a WebAssembly module pack, scoping a node type to tenants, or turning the JavaScript sidecar on and need its configuration keys |
