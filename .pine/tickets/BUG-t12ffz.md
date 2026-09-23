---
id: BUG-t12ffz
title: Data table Tool set to Insert (or any write) silently performs a read, and the agent reports success
status: doing
priority: high
labels:
    - ai
    - ai-tools
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T04:43:18Z"
---

# Description

The agent says "successfully added" while the row count stays the same. BUG-6jvcs5 is marked done with this item unchecked.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ai-ollama; finding ids: AI-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** A Data Table node attached as a tool performs its configured operation (insert, update, upsert, delete, get), and `$fromAI()` fills the column values.

# Steps to Reproduce

1. Create datastore `[ai-ollama] customers` (name, city, plan) with 3 rows.
2. Agent + datastoreTool `add_customer` with `operation: insert` and columns `name/city/plan = {{ $fromAI(...) }}`, plus a second datastoreTool `find_customers` (get).
3. Ask "Add a new customer: Joko Widodo from Yogyakarta on the free plan."

# Expected

Write operations run with `$fromAI` columns, or the tool refuses every operation except get at save/import time and the UI hides them.

# Actual

The model is offered the read schema (`match/conditions/limit`), so it calls `add_customer` with `conditions:[name eq Joko…, city eq …, plan eq free]`. The default `match:"any"` returns Budi's row, and the agent says "The customer "Joko Widodo" … has been successfully added." The second run returned `{"rows":[]}`, and the agent still said "I've added a new customer row for Dian Sastro". Row count stays 3. Status: succeeded. The n8n importer carries `operation: insert` onto the tool with no warning.

# Acceptance Criteria
- [x] Insert, update, upsert and delete run with `$fromAI` column values and change the table
- [ ] ~~Or, until writes are supported, every operation except get is refused at save and import time and hidden in the UI~~ Not applicable: the user chose to implement the writes (the first criterion) instead.
- [x] A test asserts the row count changes after an agent insert

# Implementation Plan

Implement insert/update/upsert/delete in the tool, with a schema derived from the mapped columns or `$fromAI`. Until then, reject non-get operations in `validateDatastoreToolConfiguration` and flag them as blocking on import.

# Notes

Related tickets: BUG-6jvcs5

Related (from the audit): BUG-6jvcs5 (status done). Its checklist item "Data table Tool offers every operation … but always performs a filtered read" is still unchecked and was never addressed in its progress notes. It still reproduces.

# Related Files

`case-5-1.execution.json`, `case-5-2.execution.json`, `case-import.response.json` (no issue for "Add customer" operation insert). Code `nodes/datastore.go:1123-1265`: `Definition`/`Invoke` ignore `operation` and `columns`. The node UI is `toolVariantOf(datastoreNode())`, which inherits all 13 operations.

## Progress — Lane E (2026-09-23)

Fix: the Data table Tool performs its configured operation. `get` reads as before. `insert`, `update`, `upsert` and `delete` write with the model's `$fromAI` values, through the step node's own executor.

- **Node definition** (`nodes/datastore.go` `datastoreToolParameters`): the tool's own copy of `operation` offers only get, insert, update, upsert and delete, defaults to **get**, and restricts `resource` to Row. The copies are fresh values with their own option slices, and the step Data table node keeps its default `insert` and all thirteen operations (asserted in `TestDatastoreToolOperationsAreTheRowOperationsAndDefaultToGet`).
- **Descriptor** (`DatastoreToolExecutor.Execute`): carries `operation` (the one the tool performs) and a copy of `ir.Parameters`, as the workflow tool's does. `datastoreToolFrom` (`nodes/ai.go`) keeps `request` on the tool and refuses an operation a tool does not perform.
- **Definition**: get keeps the closed read schema. A write's schema is `ai.FromAISchema(ai.ExtractFromAI(params))`, closed with `additionalProperties: false`, and falls back to a closed empty schema when there are no `$fromAI` calls.
- **Invoke** (`invokeWrite`):
  1. Only the declared `$fromAI` keys cross into the item.
  2. `ai.SubstituteFromAI` substitutes into a copy of the parameters.
  3. The table is pinned by id to the one the descriptor resolved through `datastore.ResolveByName`.
  4. It runs `NewDatastoreExecutor(store).Execute`, so `runOne`, `runRow`, the mapper and the whole-table refusal are reused.
  5. It answers `{operation, affected, rows, truncated}`. Past the 256 KiB cap it drops the rows but keeps the count, because the write has already committed.
  6. A failure returns as a tool error, never as a success.
- **Validation** (`validateDatastoreToolConfiguration` → `checkDatastoreToolOperation`, which also runs as the backstop when the descriptor is built): refuses table-level operations, increment, the branches and a non-row resource. It runs the step node's `validateDatastoreConfiguration` against the resolved operation, so a matching write needs a condition. It also refuses `$fromAI` in column-name slots (a condition's `keyName`, `matchingColumns`), and runs `validateToolFromAI`.
- **Embed scope** (`nodes/embedscope.go`): the tool no longer skips `datastoreOperationIssues`, so a table operation on a tool is refused like one on the step node.
- **Importer** (`internal/interop/n8n/parameters.go`): insert, update, upsert and deleteRows map cleanly. rowExists, rowNotExists and every table operation are blocking and import as a get. An insert tool that maps no column is blocking on `columns`. The exporter writes the operation the tool performs.

**Compatibility rule, and why:**
- **Insert:** a tool whose stored operation is `insert` but that declares nothing to write (no non-empty manual `columns` value and no `$fromAI` call in `columns`) reads, with the read schema, exactly as the tool always did. The editor writes every parameter default into a new node, and the tool's default was `insert`, so every Data table Tool built in the editor before this change is stored with `operation: insert` and no columns, and was used as a read. Honouring that insert would add an empty row on every call and remove the read the agent was built around. An insert that writes nothing is never what an author meant.
- **Update and upsert:** these cannot be reached by default, so one that declares nothing to write is refused at save/compile and at descriptor build ("…to read rows, set the operation to Get").
- **Where the rule lives:** it is decided in one place, `nodes.DatastoreToolOperation`, with the reason in its comment. It is shown in the editor as a notice under Operation when Insert is chosen, and in the node's description: "An Insert with no column values reads rows instead; set Operation to Get to make that explicit, or map columns to write."
- **No compile warning:** node validation has no non-blocking warning channel (`ConfigValidator` returns an error, and `ValidationError` has no severity), so none was built.

Proof:
- `go test ./nodes/ -run 'TestDatastoreTool|TestAgentInsertsARow|TestAgentUpdatesAndDeletes|TestEmbedScopeIssues|TestAgentRunsDatastoreTool'` → ok. This covers:
  - an agent insert takes the row count from 3 to 4;
  - update, delete and upsert with conditions;
  - the write schema comes from `$fromAI`;
  - a failed write reaches the model as a failure;
  - the table and item are pinned;
  - a legacy insert with nothing to write still reads with the read schema;
  - update or upsert with nothing to write is refused;
  - validation refuses table operations and a matching write with no condition.
- `go test ./internal/interop/n8n/ -run 'TestDataTableTool'` → ok. This covers an insert import, blocked operations, the auto-mapped insert, and the export.
- `go test ./...` and `go vet ./...` → ok. `make generate-skills-index-check` and `make generate-skills-command-reference-check` → up to date.

# Attachments
