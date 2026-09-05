---
id: FEAT-nch9dg
title: Map n8n Data Table workflows through the importer
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-3xqky1
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

The `mappings` table at `internal/interop/n8n/n8n.go:186-231` is the whole advertised interoperability subset, and its eleven entries contain no `n8n-nodes-base.dataTable`. `byN8NType` (233-240) returns false for it, so the import branch at `n8n.go:325-348` turns every Data Table node into `kilasflow.unsupported`. Once V2-p9-10 ships `kilasflow.datastore` the export direction is worse than blocked: `byKilasType` (242-249) also returns false, `Export` omits the node at `n8n.go:734-741` with the reason "n8n has no equivalent of the KilasFlow node …, so it was omitted from the export", and `n8n.go:773-781` then drops every connection that touched it under the generic "a connection referenced a node that was not exported and was dropped with it".

The referenced table is the second absence. n8n keys a table by `dataTableId` scoped to a `projectId` — `DataTable.id` and `DataTableColumn.dataTableId` in the reference checkout's `packages/workflow/src/data-table.types.ts` — and the node's table slot is a resource locator offering From list, By Name and By ID. An imported id names a row in somebody else's catalogue and has no local counterpart by construction. The adapter is not ready for it either: `fromN8NValue` at `internal/interop/n8n/parameters.go:27-36` unwraps only a leading `=` on a string, so a resource-locator object passes through as an opaque map, and the package holds no `__rl` handling at all.

The tool variant needs a decision, not a mechanism. `packages/workflow/src/constants.ts:54-55` declares both `DATA_TABLE_NODE_TYPE = 'n8n-nodes-base.dataTable'` and `DATA_TABLE_TOOL_NODE_TYPE = 'n8n-nodes-base.dataTableTool'`, so the tool is a real type string in exported JSON even though n8n derives it from `usableAsTool` rather than a second node file. KilasFlow derives nothing — `nodes/ai.go:22-30` registers `kilasflow.httpTool` as its own type, and V2-p9-11 ships a separate datastore tool. A `mapping` entry carries exactly one `n8nType`, so two type strings need two entries.

One roadmap correction. The reference checkout at `/Users/izzadev/projects/mitrachat/n8n` (commit `40dfa42`, 2.34.0) is sparse: `packages/nodes-base/nodes` holds only `HttpRequest`, `If`, `Schedule` and `Set`, and `packages/cli/src/modules` holds only `community-packages`. The roadmap's `packages/cli/src/modules/data-table/utils/sql-utils.ts:386` is unreadable from here, and so is the node's parameter description file. The operation vocabulary is verifiable — `insert`, `get`, `rowExists`, `rowNotExists`, `deleteRows`, `update`, `upsert` for rows, `create`, `delete`, `list`, `update`, `clear` for tables — but the parameter names are not.

This is how a customer arrives. `SupportedMappings()` is published on every export response (`internal/api/handlers/interop.go:44-52`), so a Datastore present in the product but absent from that list is a feature nobody can migrate into.

## Acceptance criteria

- [ ] An n8n workflow containing `n8n-nodes-base.dataTable` imports as `kilasflow.datastore` with its resource, operation and filter carried, proven by a committed authored fixture test.
- [ ] An imported node whose table reference has no local counterpart still imports, lands as an unresolved reference, and reports a blocking issue naming the n8n id and cached name, proven by a test.
- [ ] Compiling a workflow whose Datastore node holds an unresolved reference fails with an error naming that node, so the workflow can be edited but never activated, proven by a test.
- [ ] `n8n-nodes-base.dataTableTool` imports to the V2-p9-11 tool node with its `ai_tool` edge landing on the agent's tool port, proven by a connection test.
- [ ] Exporting a KilasFlow Datastore node emits `n8n-nodes-base.dataTable` with every connection that touched it preserved, proven by a round-trip test asserting the connection count.
- [ ] Export reports the datastore identifier as a KilasFlow-local reference meaningless in n8n, in the same terms the existing credential-reference issue uses, proven by a test.
- [ ] `SupportedMappings()` lists both new pairs and the export response advertises them, proven by the existing interop handler test.
- [ ] The corpus is rescored by hand with `make corpus-baseline` and the resulting movement in the imported and activatable counts is recorded on this ticket.

## Implementation Plan

Capture a fixture before writing any mapping code. The node's parameter descriptions are absent from the reference checkout, so the parameter names are unknown, and inferring them from the screenshots is how a mapping ships that matches nothing real. Author a Data Table workflow in the local n8n instance the `design-refs/n8n-v2` captures came from, export it, and commit it under `internal/interop/n8n/corpus/fixtures/` — whose README explains that fixtures there are committed precisely because this project wrote them. A workflow lifted from n8n's own test suite cannot be committed at all.

**An unresolved reference, not a silent rebind.** Two ways to handle an id with no counterpart: carry it as an explicitly unresolved reference the editor must fix, or match the locator's cached name against a local datastore and bind to it. Reject name-matching. The cached name is a display string n8n never guarantees is current, and a silent bind to a same-named datastore in the importing tenant is a write to the wrong table that succeeds — the worst possible outcome, because nothing errors. Carry the id and cached name into the node's parameters and mark the reference unresolved.

Let the compiler refuse it, not the importer. `Import`'s own documentation states that the result is a draft and the existing compiler is the single validation authority, so the refusal belongs in the Datastore node's configuration validator from V2-p9-10. Putting it in the adapter creates a second, weaker authority and leaves the same broken node acceptable when it arrives through the editor instead.

Give `dataTableTool` its own `mappings` entry rather than building a derivation mechanism. KilasFlow has no `usableAsTool` concept to derive from, `nodes/ai.go` already models the HTTP tool as a separate registered type, and inventing derivation for one node would leave `@n8n/n8n-nodes-langchain.toolHttpRequest` — which still imports as a placeholder — inconsistent with the node beside it.

The trap is the export direction's silence. An unmapped node is omitted with a lossy note, and every edge through it is then dropped with a generic reason that never names the node responsible. A workflow whose middle node is a Datastore exports as two disconnected halves that n8n opens without complaint, and the only signal is a lossy entry a caller may never read. The round-trip test must assert that the connection count survives, not merely that the node appears.

**Recommend** that an import never creates a datastore as a side effect. An import that provisions tenant-visible storage turns "look at this workflow" into a write, and the per-tenant datastore count from V2-p9-5 would be spent by an import the user then discards. List the column names the node's mapping references in the issue text instead, so the datastore can be created in one step. This reopens once V2-p9-14 lands a create-from-definition endpoint: the importer could then offer the same action behind a caller-supplied flag, which is a different thing from doing it silently.

## References

- Roadmap plan, p9 section, entry V2-p9-13: `.pine/roadmap.md`.
- `internal/interop/n8n/n8n.go` — the `mappings` table (186-231), `byN8NType` and `byKilasType` (233-249), the unsupported-placeholder import branch (325-348), and `Export`'s omit path (734-741) followed by the connection drop (773-781).
- `internal/interop/n8n/parameters.go:27-49` — `fromN8NValue` and `toN8NValue`, which handle only the `=` expression prefix; no resource-locator (`__rl`) handling exists in the package.
- `internal/api/handlers/interop.go` — `ImportedWorkflowResource`, `ExportedWorkflowResource` and `SupportedMappings()` returned on every export.
- `nodes/unsupported.go` — `UnsupportedNodeType` and the placeholder's declared tool port, where a `dataTableTool` edge lands today.
- `nodes/ai.go:22-30` — `kilasflow.httpTool` as a separately registered type, the precedent for the tool variant.
- `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/constants.ts:54-55` — `DATA_TABLE_NODE_TYPE` and `DATA_TABLE_TOOL_NODE_TYPE`, both real type strings in exported JSON.
- `/Users/izzadev/projects/mitrachat/n8n/packages/workflow/src/data-table.types.ts` — the row and table operation unions, the `DataTableFilter` shape and the system columns; the node's parameter descriptions are absent from this sparse checkout.
- `internal/interop/n8n/corpus/fixtures/README.md` and `corpus/MANIFEST.json` — why only an authored fixture may be committed, and the instruction to explain a baseline shift when rescoring.
- `design-refs/n8n-v2/26-datatable-node-actions.png`, `28-datatable-resource-locator-modes.png`, `29-datatable-mapping-column-mode.png` — the action list, the three locator modes and the two mapping modes. Captured from a local n8n 2.33.7 instance; gitignored, never vendored, and not yet listed in that directory's `INDEX.md`, whose table stops at entry 17.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 26 and 31 — the operation list to map against, and the node's condition parameter paths read straight from the DOM: `filters.conditions[0].keyName`, `.condition` (default `eq`) and `.keyValue`. These are the NODE parameter names and they differ from the service-layer `columnName`/`condition`/`value`, so the importer must translate between the two rather than assume one shape. Captured from a live local n8n 2.x instance; gitignored, never vendored.
