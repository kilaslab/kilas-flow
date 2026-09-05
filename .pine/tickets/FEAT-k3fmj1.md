---
id: FEAT-k3fmj1
title: Represent fractional and date-style node type versions
status: todo
priority: high
labels:
    - engine
    - interop
    - correctness
parent: EPIC-m42s3g
phase: p1
created: "2026-09-05T05:05:37Z"
updated: "2026-09-05T05:05:37Z"
---

## Scope

Node versions are integers everywhere in KilasFlow. `node.Definition.Version` is `int` (internal/node/registry.go:54), the registry is keyed on `definitionKey{nodeType string, version int}` (registry.go:72-75), `Registry.Get` and `Registry.Lookup` take `version int` (registry.go:104, 131), `workflow.Catalog.Lookup` declares the same signature (internal/workflow/compiler.go:11), `workflow.NodeDefinition.Version` is `int` (compiler.go:24), `workflow.Node.TypeVersion` is `int` (internal/workflow/document.go:42), and the persisted contract pins it: `schemas/workflow-v1.schema.json:49` declares `"typeVersion": {"type": "integer", "minimum": 1}`.

n8n does not use integers. Core nodes are on `4.2`, `3.4`, `1.1`, `2.4` — the export side of the importer already writes exactly those values (`exportTypeVersion` in internal/interop/n8n/n8n.go:117-158), so the fractional form is already a fact this codebase records. WAHA goes further and uses YYYYMM integers, `202409` and `202502`, which fit in an `int` but not in a scheme where version 1 is the only registered version.

The import side throws all of it away. `n8n.Node.TypeVersion` is a `float64` (n8n.go:48), and every mapping entry sets `kilasVersion: 1`, so `converted.TypeVersion = entry.kilasVersion` (n8n.go:260) collapses every imported node to version 1 regardless of what the source said. The only surviving trace is `originalTypeVersion` on an unsupported placeholder, and the diagnostic truncates with `TypeVersion: int(node.TypeVersion)` (n8n.go:253), so an n8n node on version 4.2 is reported as version 4.

That is tolerable while exactly one version of each node exists. It stops being tolerable the moment a node has two versions with different parameter shapes, which is the entire premise of the node-pack work: Set v2 and Set v3.4 take different parameters, and WAHA's `202409` and `202502` have different event lists. Without a version that survives import, an imported node is silently configured against the wrong schema.

## Acceptance criteria

- [ ] A node type version is representable as a fractional value end to end — registry key, definition, catalog lookup, compiled IR, persisted document, JSON schema and API contract — with no truncation at any boundary.
- [ ] A YYYYMM version such as `202502` registers, saves, compiles and round-trips unchanged.
- [ ] Importing an n8n node preserves its source `typeVersion` rather than replacing it with 1, and the diagnostic reports the exact version, `4.2` not `4`.
- [ ] A node type can declare a default version, and a document that names a version the registry does not have resolves per a documented rule rather than failing with "version 1 is not registered".
- [ ] Two versions of one node type can be registered simultaneously and a document selects between them by version.
- [ ] Every workflow saved before this change loads and compiles unchanged, and the existing `ErrorUnknownVersion` diagnostic still distinguishes an unknown version from an unknown type.

## Implementation Plan

Choose the representation first, and do not choose `float64`. A float map key invites `4.2 != 4.2000000000000002` and makes registry lookups nondeterministic in a way that will surface once and be miserable to find. Two safe options: a normalized decimal string (`"4.2"`, `"202502"`) or a struct of major and minor integers. Recommendation: a small named type wrapping major and minor `int`, with `MarshalJSON`/`UnmarshalJSON` that read and write JSON numbers so the wire format stays `4.2` and stays compatible with both n8n and today's documents. It is comparable, it is a valid map key, and it makes "is this version at least 3.0?" a real question rather than a float comparison.

Then widen in dependency order: `internal/node/registry.go` (`Definition.Version`, `definitionKey`, `Register`, `Get`, `List`, `Lookup`, `HasType`, `validateDefinition`'s `Version < 1` check), then `internal/workflow/compiler.go` (`Catalog`, `TypeCatalog`, `NodeDefinition`, the lookup and its two error messages), then `internal/workflow/document.go` (`Node.TypeVersion`, the `TypeVersion < 1` validation), then `schemas/workflow-v1.schema.json`, then every `Version: 1` literal in `nodes/`. Compiling after each step keeps the change reviewable; doing it in one sweep produces a diff nobody can check.

Default version and dispatch are the new behaviour, not just a widening. Add `DefaultVersion` to the definition set so a document that omits `typeVersion`, or names one that does not exist, resolves to a declared default. The rule needs stating: recommend resolving to the highest registered version that is less than or equal to the requested one, and failing when none is, because it matches how n8n treats an older workflow against a newer node and it never silently upgrades a workflow into a parameter shape it was not written for.

Two traps. `Definition` is serialized directly as the `/api/v1/node-types` payload, so changing `Version`'s JSON representation is an API change even though the wire value looks the same — run `pnpm generate:api` in `web/` and `pnpm generate:types` in `sdk/`, and check the generated TypeScript still types it as a number. And `schemas/workflow-v1.schema.json` says `"type": "integer"`; every existing persisted document satisfies both the old and new schema, but a document written after this change will not satisfy the old one, so the schema version and any host validating against it need attention.

Finally the importer: replace `kilasVersion: 1` with the real target version per mapping, stop discarding `node.TypeVersion`, and fix the `int(node.TypeVersion)` truncation in the unsupported diagnostic.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, entry V2-p1-11, and V2-p3-3 for the WAHA YYYYMM versions that depend on it.
- `internal/node/registry.go` — `Definition.Version`, `definitionKey`, `Register`, `Get`, `List`, `Lookup`, `validateDefinition`.
- `internal/workflow/compiler.go` — `Catalog`, `TypeCatalog`, `NodeDefinition`, the version lookup and `ErrorUnknownVersion`.
- `internal/workflow/document.go`, `schemas/workflow-v1.schema.json` — the persisted contract.
- `internal/interop/n8n/n8n.go` — `mapping.kilasVersion`, `exportTypeVersion`, `int(node.TypeVersion)`.
- `internal/api/handlers/nodes.go` — the handler that serves `Definition` as the node-types payload.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 04 — the NDV footer states the resolved version in words: "AI Agent node version 3.1 (Latest)". Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
