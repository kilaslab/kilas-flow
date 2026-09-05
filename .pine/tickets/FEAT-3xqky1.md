---
id: FEAT-3xqky1
title: Add the Datastore node
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-nrfg6e
    - FEAT-45tfmh
    - FEAT-a6yg3n
    - FEAT-9knk67
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`nodes/core.go`'s `RegisterAll` (lines 10-42) installs seventeen node definitions and `nodes/executors.go`'s `RegisterExecutors` (lines 23-49) binds eighteen executor IDs; neither carries a datastore entry, and a case-insensitive search for `datastore`, `data table` or `dataTable` across the working tree matches only `.pine/roadmap.md` and five ticket bodies — no Go, TypeScript or Svelte. This ticket adds `kilasflow.datastore` at n8n's operation surface: a Table resource of Create, List, Update and Delete, and a Row resource of Insert, Get, Update, Upsert, Delete, If Row Exists and If Row Does Not Exist, so V2-p9-13 can map `n8n-nodes-base.dataTable` operation-for-operation rather than degrade it to a placeholder.

The load-bearing constraint is that column names are literal-only. `internal/expression/expression.go`'s `Resolve` (line 58) and its `resolveValue` helper (line 69) walk the entire parameter tree — every map value, every array element — replacing any `{"mode":"expression","value":"…"}` marker they find, with no per-property opt-out anywhere in the package. A column slot that accepts a marker is an identifier the caller of a webhook controls.

The roadmap cites `internal/webhook/webhook.go:249` as the line admitting attacker-controlled JSON into `$json`; the mechanism is real, the citation one line off — 249 is the `query` entry of the payload map at lines 245-251 and the body lands at 250. Its neighbouring claim in V2-p9-12, that this function returns `Redact(payload)`, is stale against the file: only `headers` is redacted, at line 230, and the comment at 226-229 says the body is deliberately left intact. An inbound body reaches the first node verbatim, so an expression-capable column slot is identifier injection available to anyone who can `POST` to a webhook path.

Storage is reached through the Datastore repository from V2-p9-2, never through `internal/sqlnode`. That package's `Guard` (lines 53-59) exists to refuse a credential naming KilasFlow's own database files, and `NewDatabaseExecutor` (`nodes/database.go:166`) demands a user credential this node has no business holding.

Three hand-maintained maps in `web/src/lib/workflow-editor/node-visual.ts` fail silently: `ICONS` (lines 46-64) falls back to `Box` at line 94, `ACCENTS` (lines 66-72) to `var(--muted-foreground)` at line 95, and `nodeSubtitle` returns `null` from its `default` arm at line 173. A node added server-side reaches the picker unaided — `node-picker.svelte:43` derives categories from the registry — as an unlabelled grey box. Editing all three is what makes an imported n8n workflow look imported rather than broken, which is the posture this epic exists to hold.

## Acceptance criteria

- [ ] `kilasflow.datastore` resolves from the registry with its executor bound, proven by a registry test asserting the type, version and executor ID together.
- [ ] Every Table and Row operation n8n exposes runs end to end against a real datastore, proven by one executor test per operation named for that operation.
- [ ] A column slot holding an expression marker fails compilation with an `ErrorInvalidConfig` issue naming the parameter path, proven by a compiler test.
- [ ] A webhook body supplying a column name is refused after `expression.Resolve` and commits nothing, proven by a test and captured as evidence on this ticket.
- [ ] The node package imports no `internal/sqlnode` symbol, proven by an import-boundary test written in the manner of `internal/guardrails/licence_boundary_test.go`.
- [ ] The canvas draws the node with its own glyph, accent and subtitle rather than the `Box` and muted-foreground fallbacks, proven by a `node-visual.test.ts` case.
- [ ] A ten-row write failing on the seventh names the failing item through paired-item lineage and, under `continueOnFail`, still emits the other nine, proven by a test.
- [ ] Both drivers are exercised by hand through `make smoke-sqlite` and `make smoke-postgres`, the output recorded here rather than in any automated pipeline.

## Implementation Plan

Settle the parameter schema before writing an executor, because it is the security boundary and the operation matrix, the validator and the editor form all derive from it. Model it on `nodes/database.go`'s definition (lines 38-82): an `operation` select gating the rest through `VisibleWhen`, with a `resource` select above it. Column names take their own well-known keys, never a free-form `keyValue` bag, so the validator polices fixed paths rather than searching a tree.

Reject the shortcut of expressing the Row filter as the `PropertyConditions` kind the IF node already uses. `ifCondition` (`nodes/executors.go:142-166`) accepts exactly one condition over a four-operator vocabulary; V2-p9-2's filter is `{type: and|or, filters: [{columnName, condition, value}]}` over ten operators. Bending one kind to serve both leaves two structures sharing one editor control and drifting apart. Declare the filter as its own kind.

Do not build a free-text datastore picker to avoid waiting on V2-p2-10. `internal/node/registry.go` declares exactly six property kinds (lines 15-22) and `knownPropertyKind` (line 263) refuses registration of any other, so a datastore chosen today would be a raw string an operator copies by hand — and replacing it later renames a stored parameter in every saved workflow.

The trap is believing the compiler already covers this. `internal/workflow/compiler.go:197-204` runs a definition's `Validate` on save, on execution creation and on every run, so a marker rejected there looks comprehensively blocked. It is not: `Resolve` runs after compilation, inside the executor, and yields a plain string from `$json`. A re-validation that inspects `ir.Parameters` rather than the resolved map, or that runs before `Resolve`, passes every test anyone would think to write and admits a webhook body regardless. Re-check the resolved value immediately before it is quoted into an identifier.

Write per item, not per node run. `DatabaseExecutor.Execute` (`nodes/database.go:172-214`) resolves parameters inside the item loop and returns on the first error; that shape suits this node and is what makes the deps on V2-p1-5 and V2-p1-2 pay — a failure on the seventh of ten rows has an item to blame and a `continueOnFail` setting that means something. One transaction wrapping the whole run is the tempting alternative and is wrong: it turns a per-row data error into an all-or-nothing abort with no lineage.

**Node category.** The choice is reusing `Database`, which already maps to `var(--node-data)` in `ACCENTS`, or adding a `Datastore` category with a `--node-datastore` token beside the five at `web/src/app.css:133-137` and their light counterparts at 179-183. Recommend the new category: the Database group means SQL against a database an operator holds a credential for, and this node holds none and reaches KilasFlow's own storage, so grouping them teaches something false. Reopen it if the category list grows past what a reader can scan, where merging thin categories beats a seventh colour.

## References

- Roadmap plan, p9 section, entry V2-p9-10: `.pine/roadmap.md`.
- `nodes/core.go` — `RegisterAll` at lines 10-42, the registration path a new definition joins, and `sharedSettings` at 128-138.
- `nodes/executors.go` — `RegisterExecutors` at lines 23-49, and `ifCondition` at 142-166, the condition vocabulary that must not be reused.
- `nodes/database.go` — the definition at lines 38-82 and `DatabaseExecutor.Execute` at 172-214, the per-item resolve loop worth copying.
- `internal/sqlnode/sqlnode.go` — `Guard` at lines 53-59, the reason this node cannot travel the SQL-node path.
- `internal/webhook/webhook.go` — `requestPayload` at lines 211-256: headers redacted at 230, the body admitted intact at 250 under the comment at 226-229.
- `internal/expression/expression.go` — `Resolve` at line 58 and `resolveValue` at line 69, which rewrite the whole parameter tree with no per-property opt-out.
- `internal/workflow/compiler.go` — the `Validate` call at lines 197-204, the only place a definition's validator runs.
- `internal/node/registry.go` — `PropertyKind` at lines 15-22 and `knownPropertyKind` at line 263, the six kinds available until V2-p2-10 lands.
- `web/src/lib/workflow-editor/node-visual.ts` — `ICONS` at 46-64, `ACCENTS` at 66-72, `nodeSubtitle` at 135-176, and the fallbacks at lines 94-95.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 25-32 — the definitive operation list (Actions (12): seven Row Actions and five Table Actions, with the table `update` operation surfaced as **Rename a data table**), the NDV cascade `Resource` to `Operation` to the `dataTableId` resource locator to **Mapping Column Mode** to Options, and the fact that Triggers (2) are only the generic Schedule and Webhook — there is no data-table trigger. Captured from a live local n8n 2.x instance; gitignored, never vendored.
