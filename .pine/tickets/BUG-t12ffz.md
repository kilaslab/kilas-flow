---
id: BUG-t12ffz
title: Data table Tool set to Insert (or any write) silently performs a read, and the agent reports success
status: done
priority: high
labels:
    - ai
    - ai-tools
    - datastore
    - data-integrity
parent: EPIC-8rbys7
created: "2026-09-23T01:56:13Z"
updated: "2026-09-23T05:07:05Z"
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

## Progress — Lane E, review fix round 1 (2026-09-23)

The review found that a model could reach the expression evaluator, or widen a write, through the tool. All of it is closed in `nodes/datastore.go`:

- **Model values are data.** `invokeWrite` checks each supplied argument against its `$fromAI` type before substitution (`checkDatastoreToolArgument`): a string must be a string, a number a number, a boolean a boolean. It refuses any value holding an `{"mode":"expression"}` marker or `{{`/`}}` at any depth. The error names the argument and never echoes its value.
- **No splicing into code.** Save and descriptor build refuse a string or json `$fromAI` that shares its `{{ }}` with other code, such as `{{ $fromAI('name').toUpperCase() }}`. Found with the new `ai.FromAICallsSplicedIntoCode`, because the model's text would run as code there. The structural fix in the shared `SubstituteFromAI` is a separate ticket.
- **No model-chosen filter shape.** `refuseDatastoreToolModelChoices` refuses `$fromAI` in a condition's operator, in `match`, in `keyName` and in `matchingColumns`. It uses `ai.ExtractFromAI`, so a plain string and a marker are both caught. Before this, a model could set `neq` or `any` and empty the table.
- **Auto-map writes only its columns.** The auto-mapped item is built from the calls under `columns` only, with their defaults, so a condition's key is never written onto the rows it matched.

Proof: `go test ./nodes/ ./internal/ai/ ./internal/interop/n8n/` → ok. The covering tests are:
- `TestDatastoreToolNeverEvaluatesAModelValueAsAnExpression`, covering both review probes plus type mismatches and nested markers;
- `TestDatastoreToolRefusesAModelChosenOperatorOrMatchWhenBuilt`;
- `TestDatastoreToolAutoMapsOnlyTheColumnsItDeclares`;
- `TestDatastoreToolValidation`, for the operator, match and splice cases;
- `TestFromAICallsSplicedIntoCodeFindsOnlyCallsSharingASegment`.

## Progress — Lane E, review fix round 2 (2026-09-23)

Structural fix: on the write path, the model's values reach expressions as data and never as source. It replaces round 1's heuristics, which the re-review broke with a lookup-table literal, a `JSON.parse` literal, adjacent segments, and one key declared with two types.

- **Evaluator** (`internal/expression`). The new, additive `Context.FromAIArguments` makes `$fromAI('key', …)` return the agent's argument as a value, or the call's default. A missing required key is an error naming it. With it nil, today's `AllowFromAI` / `FromAIRequest` behaviour is unchanged. `$fromAI` now takes n8n's fourth argument, the default.
- **Write path** (`nodes/datastore.go`):
  - `invokeWrite` no longer runs `SubstituteFromAI` into expression markers. They stay exactly as the author wrote them.
  - Only plain strings have their `$fromAI` filled (`datastoreToolPlainFromAI`); nothing evaluates a plain string.
  - The arguments reach the step executor through an unexported `DatastoreExecutor.fromAIArguments`, which is set on the expression context it resolves with. This is the least invasive route: no change to `engine.Request` or any other executor.
  - The declared-type check stays. So does the json check that refuses a `mode:"expression"` map.
  - The `{{`/`}}` refusal, `datastoreToolValueIsTemplate` and `ai.FromAICallsSplicedIntoCode` are removed.
- **Deterministic schema.** The new `ai.CheckFromAIConsistent` refuses a `$fromAI` key declared with a different type, description or default, at save and at descriptor build.
- **Scope of the rules.** The authority refusals (operator, match, keyName, matchingColumns) and every `$fromAI` rule now bind writes only. A get tool's parameters are never filled from the model.
- **Unchanged:** `SubstituteFromAI` and the HTTP and Workflow tools. Their structural fix is the separate ticket.

Proof:
- `go test ./nodes/ ./internal/ai/ ./internal/expression/ ./internal/interop/n8n/ ./internal/engine/` → ok.
- `TestDatastoreToolNeverEvaluatesAModelValueAsAnExpression` runs every probe from both reviews. Each stored value is the model's literal text, or the call is refused. None of them stores an evaluated result.
- `TestDatastoreToolRefusesAKeyDeclaredTwoWaysWhenBuilt`, `TestFromAIArgumentsAreDataTheExpressionComputesWith` and `TestCheckFromAIConsistentRefusesAKeyDeclaredTwoWays` also pass.

## Progress — Lane E, review fix round 3 (2026-09-23)

One rule instead of per-slot patches: on a write, the model supplies values, never structure.

- **Where `$fromAI` may appear** (`refuseDatastoreToolStructureFromAI`, which replaces `refuseDatastoreToolModelChoices`, at save and at descriptor build, writes only): only inside one mapped column's value (`columns.value.<column>`) or a condition's `keyValue`, in plain or expression form. It is refused anywhere else, including:
  - a whole condition row, which covers both review probes (a row choosing its operator or its column) and the sole-json row;
  - the conditions list or the whole filters panel;
  - `match`, `keyName`, `condition`;
  - the whole `columns.value`, the mapping mode, `matchingColumns`;
  - any other parameter.
- **Filled shape check** (`checkDatastoreToolFilled`, defence in depth): after plain strings are filled, the call is refused if a value became an expression marker where the author's template was not one, at any depth. It is also refused if a map's keys or a list's length changed, or if a whole-call json value holds a marker. Before this, the probe column value `{"mode":"$fromAI('m')","value":"$fromAI('v')"}` stored "exec-1".
- **Json column values:** a json argument alone as a column's value is stored as that column's data and never read back as a mapping or conditions. Confirmed by a test in both forms.
- **Null default:** a json `null` default resolves to null in an expression and in a plain string alike. The evaluator's `fromAIArgument` now treats a key present as null as null.

Proof: `go test ./nodes/ ./internal/ai/ ./internal/expression/ ./internal/interop/n8n/ ./internal/engine/` → ok. The covering tests are `TestDatastoreToolModelSuppliesValuesNeverStructure`, `TestDatastoreToolRefusesAFilledValueThatBecomesAnExpression`, `TestDatastoreToolStoresAJsonColumnValueAsData`, `TestDatastoreToolNullDefaultIsTheSameInBothForms`, and `TestFromAIArgumentsAreDataTheExpressionComputesWith` (null case).

## Progress — Lane E, review fix round 4 (2026-09-23)

On a tool that writes, the structure is written literally. `nodes.DatastoreToolStructureExpression` refuses any expression marker, at any depth, outside `columns.value.<column>`, a condition's `keyValue`, `toolName` and `toolDescription`, whether or not it mentions `$fromAI`. It runs at save and at descriptor build, and names the path. This closes the operator, row, match and mapping-mode choices reached through `$json`, `$input` and `($fromAI)('v')`, which previously updated or deleted every row. The plain-string `$fromAI` placement rule stays. `checkDatastoreToolFilled` walks keys in sorted order. The n8n importer blocks a write tool that would carry such an expression (in practice `name`); an n8n operator or match expression already arrives as a literal with its diagnostic. `SKILL.md` notes that the author's operator is final. Proof: `TestDatastoreToolWriteTakesItsStructureLiterally`, `TestDatastoreToolWithLiteralStructureStillWrites`, `TestDatastoreToolNamesTheSameFilledValueOnEveryRun`, `TestDataTableToolWriteNeverImportsAnUnreportedStructureExpression`; `go test ./...` → ok.

## Progress — final review fixes (2026-09-23)

- **An imported write tool's match expression narrows to "all" and blocks.** An n8n `match` expression on an imported update, upsert or delete tool fell back to the wider "any" with only a lossy diagnostic. It now imports as "all", the narrower, with a blocking diagnostic asking the author to set Must Match. Read tools and the step node keep the lossy "any". The operator expression → `eq` lossy fallback is unchanged.
- **The importer's structure safety net reports every slot.** It reported only the first structure path, and nothing when that path's field was already blocked, so a keyName expression plus a name expression reported only `filters`. `nodes.DatastoreToolStructureExpressions` (new; `DatastoreToolStructureExpression` is now its first entry, so the save refusal names the same path) lists every path, and `dataTableToolStructureIssues` reports one blocking diagnostic per path whose field the importer has not already blocked.
- **`name` is dropped from an imported tool.** The blocking `name` diagnostic pointed at a field that is hidden for row operations, and no tool reads it (only create and rename table do). An imported tool now drops `name`, literal or expression, with a Dropped diagnostic. The step node still carries it.
- **Skill:** `skills/kilasflow-datastore/SKILL.md` adds `neq` to the author-chosen operator warning (`delete` where `id` `neq` the model's value deletes every row but one). `make generate-skills-index-check` and `make generate-skills-command-reference-check` report no drift.
- Proof: `TestDataTableToolWriteNeverImportsAnUnreportedStructureExpression` now checks, for every fixture, that each structure path left in the imported tool has a blocking diagnostic on its field, and covers keyName + name, a whole filters expression + name, and match. The keyName + name case failed that check before the change. `TestDataTableToolWritesImportAMatchExpressionAsAll` covers update, upsert and deleteRows against the step node. `TestDataTableToolStructureNetReportsEverySlotOutsideABlockedField` drives the net directly with several unblocked slots and one blocked field. `TestDatastoreToolStructureExpressionsNamesEverySlot` (nodes) pins the list and its first entry. Each failed before and passes after.

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-t12ffz" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (7):
  - `61a79b8d` — BUG-t12ffz: an imported write tool narrows a match it cannot carry to all, reports every structure expression, and drops the table name no tool reads
  - `9bee300f` — BUG-t12ffz: a Data table Tool that writes takes its structure literally, so no expression lets the model choose which rows it touches
  - `01b9a4a9` — BUG-t12ffz: on a write, the model supplies a Data table Tool's values and never its structure
  - `36f1543c` — BUG-t12ffz: a Data table Tool hands the model's values to expressions as data, never as source
  - `ddd02016` — BUG-t12ffz: a Data table Tool writes the model's values as data, and never lets the model choose which rows a write touches
  - `a7165061` — chore(pine): close BUG-t12ffz with its landing evidence
  - `822f9227` — BUG-t12ffz: a Data table Tool performs the operation it is set to, and an Insert with nothing to write still reads
- Merged by (1):
  - `fa15f1ed` — merge: a data table's name is unique in its tenant, and a Data table Tool performs the write it is set to with the model supplying values only (BUG-e7dwpk, BUG-t12ffz)
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-t12ffz.md                      |  408 +++++++-
 internal/ai/fromai.go                            |  176 ++--
 internal/ai/fromai_test.go                       |   89 +-
 internal/expression/expression.go                |   12 +
 internal/expression/expression_test.go           |   63 +
 internal/expression/globals.go                   |    4 +-
 internal/expression/roots.go                     |   27 +-
 internal/interop/n8n/export_test.go              |    7 +
 internal/interop/n8n/n8n_test.go                 |  390 ++++++-
 internal/interop/n8n/parameters.go               |  152 +++-
 nodes/ai.go                                      |   18 +-
 nodes/datastore.go                               | 1007 +++++++++++++---
 nodes/datastore_tool_test.go                     | 1367 +++++++++++++++++++++-
 nodes/embedscope.go                              |   10 +-
 nodes/embedscope_test.go                         |   25 +-
 skills/kilasflow-datastore/SKILL.md              |   13 +-
 skills/kilasflow-datastore/references/FILTERS.md |    2 +-
 17 files changed, 3385 insertions(+), 385 deletions(-)
```
