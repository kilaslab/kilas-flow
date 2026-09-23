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

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `822f9227` — BUG-t12ffz: a Data table Tool performs the operation it is set to, and an Insert with nothing to write still reads
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 ++
 .pine/tickets/BUG-15st2k.md                        | 162 ++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 ++
 .pine/tickets/BUG-2mes2k.md                        |  54 ++
 .pine/tickets/BUG-2n4rfz.md                        |  51 ++
 .pine/tickets/BUG-2z8geh.md                        |  52 ++
 .pine/tickets/BUG-3k12ky.md                        | 163 ++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 ++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 +++++
 .pine/tickets/BUG-66fhea.md                        |  97 ++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 ++++
 .pine/tickets/BUG-6gkd12.md                        | 107 ++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 ++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  61 +++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 ++
 .pine/tickets/BUG-bcahaj.md                        | 100 ++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 ++
 .pine/tickets/BUG-dstsg9.md                        |  54 ++
 .pine/tickets/BUG-e7dwpk.md                        | 357 +++++++++++++
 .pine/tickets/BUG-ecbq28.md                        | 111 ++++
 .pine/tickets/BUG-epy2se.md                        | 122 +++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 ++
 .pine/tickets/BUG-hmp85t.md                        |  99 ++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 ++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 ++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 ++
 .pine/tickets/BUG-namghh.md                        |  48 ++
 .pine/tickets/BUG-nbymq4.md                        |  52 ++
 .pine/tickets/BUG-ngt25j.md                        |  52 ++
 .pine/tickets/BUG-nn74ph.md                        |  51 ++
 .pine/tickets/BUG-nzy3pa.md                        | 186 +++++++
 .pine/tickets/BUG-p334yw.md                        |  53 ++
 .pine/tickets/BUG-p3j233.md                        |  53 ++
 .pine/tickets/BUG-phv0r9.md                        |  56 ++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 ++
 .pine/tickets/BUG-q6b75c.md                        |  52 ++
 .pine/tickets/BUG-r1m83f.md                        |  52 ++
 .pine/tickets/BUG-rbask0.md                        | 140 +++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 ++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 ++
 .pine/tickets/BUG-sgrxhh.md                        |  53 ++
 .pine/tickets/BUG-t12ffz.md                        |  94 ++++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 ++
 .pine/tickets/BUG-v8ksv8.md                        |  49 ++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 +++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 ++
 .pine/tickets/BUG-xam6t8.md                        | 179 +++++++
 .pine/tickets/BUG-y38bss.md                        |  54 ++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 ++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 ++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 ++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 +++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 ++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 ++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 ++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 ++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 ++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 ++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 ++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 ++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 ++
 .pine/tickets/FEAT-5fx926.md                       |  57 ++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 ++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 ++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 ++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 ++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 ++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 +++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 +++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 ++
 .pine/tickets/FEAT-f045nj.md                       | 131 +++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 ++
 .pine/tickets/FEAT-fqmh01.md                       |  97 ++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 ++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 +++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 ++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 ++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 ++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 ++
 .pine/tickets/FEAT-m7aw75.md                       |  54 ++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 ++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 +++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 ++
 .pine/tickets/FEAT-p01rcw.md                       |  98 ++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 +++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 ++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 ++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 ++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 ++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 ++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 ++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 ++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 ++
 .pine/tickets/FEAT-tjcr13.md                       |  52 ++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 +++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 ++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 ++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 ++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 ++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 +
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++-----------
 cmd/kilasflow/main.go                              |  19 +-
 config.example.yaml                                |   8 +-
 docs/src/content/docs/concepts/architecture.md     |  84 +++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |   8 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 ++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/datastores_test.go                    |  29 ++
 internal/api/handlers/datastores.go                |   7 +
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/config/config.go                          |  10 +-
 internal/config/config_test.go                     |  22 +
 .../datastore_unique_names_migration_test.go       | 176 +++++++
 internal/database/workflow_actor_migration_test.go |   7 +-
 internal/datastore/catalogue.go                    |  46 +-
 internal/datastore/engine.go                       |  26 +-
 internal/datastore/engine_test.go                  |  13 +-
 internal/datastore/names.go                        | 132 +++++
 internal/datastore/names_test.go                   | 271 ++++++++++
 internal/embed/confinement.go                      |  12 -
 internal/embed/confinement_test.go                 |  14 +-
 internal/engine/approval.go                        |   2 +-
 internal/interop/n8n/n8n_test.go                   | 118 +++++
 internal/interop/n8n/parameters.go                 |  47 +-
 internal/loadoptions/datastores.go                 |  31 +-
 internal/loadoptions/datastores_test.go            |  35 ++
 .../000021_datastore_unique_names.down.sql         |   8 +
 .../postgres/000021_datastore_unique_names.up.sql  |  47 ++
 .../sqlite/000021_datastore_unique_names.down.sql  |   8 +
 .../sqlite/000021_datastore_unique_names.up.sql    |  43 ++
 nodes/ai.go                                        |  93 ++--
 nodes/ai_test.go                                   |  59 +++
 nodes/datastore.go                                 | 352 +++++++++++--
 nodes/datastore_byname_test.go                     |  76 +++
 nodes/datastore_tool_test.go                       | 579 ++++++++++++++++++++-
 nodes/embedscope.go                                |  36 +-
 nodes/embedscope_test.go                           |  56 +-
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 skills/kilasflow-datastore/SKILL.md                |   2 +-
 skills/kilasflow-datastore/references/FILTERS.md   |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 +
 .../api/generated/models/aIModelStartedEvent.ts    |  24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 +
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 +
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 +
 .../workflow-editor/canvas-chat-panel.svelte       | 381 ++++++++++++--
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 ++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 ++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 ++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 +-
 web/src/lib/workflow-editor/chat.ts                |  78 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 ++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 279 files changed, 17247 insertions(+), 650 deletions(-)
```
