---
id: FEAT-xqqjqv
title: Add the assignment collection property kind for Set v3
status: done
priority: high
labels:
    - nodes
    - parity
    - sql
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p4
created: "2026-09-05T08:28:30Z"
updated: "2026-09-05T11:52:21Z"
---

## Scope

`node.PropertyKind` at `internal/node/registry.go:16-21` has six values — `string`, `number`, `boolean`, `select`, `keyValue`, `conditions` — and `assignmentCollection` is not among them. `knownPropertyKind` at line 263 rejects anything else, so the kind cannot be registered at all until it is added there, and `PropertyDefinition` at line 38 carries nothing that could hold an assignment's declared type: `Options` is `[]PropertyOption{Label, Value}`, a flat list of selectable strings.

The node that needs it is already in the tree. `setNode()` at `nodes/core.go:57` declares one parameter, `assignments`, with `Kind: node.PropertyKeyValue`, and both `executeSet` at `nodes/executors.go:58` and `validateSetConfiguration` at line 111 read it as `map[string]any`. A Go map has no order and no per-entry type, so two assignments to the same name are impossible and the order the user typed is lost the first time the document is saved.

The importer already pays for that. `setToKilas` at `internal/interop/n8n/parameters.go:80` reads n8n's ordered `assignments.assignments` array and collapses each `{id, name, type, value}` entry to `assignments[name] = value`, discarding the type. `setToN8N` at line 135 rebuilds the array from `sortedKeys` and hardcodes `"type":  "string"` at line 143, so a boolean or number assignment returns to n8n as a string and the field order comes back alphabetical rather than as authored.

The deferral is resolved on paper but owned by nobody in code. FEAT-5s1w0t (V2-p2-2) explicitly leaves `assignmentCollection` out and its Implementation Plan now names this entry as the owner — the roadmap still describes it as deferred to nobody, which the p9 planning amendment has since fixed. FEAT-jwhdsy, the data-shaping parity ticket, requires typed ordered assignments and lists p2-2's `fixedCollection` as its dependency, which is a different control: `fixedCollection` is a repeatable group of arbitrary properties, while an assignment collection is a fixed `{name, type, value}` row whose value editor depends on the sibling type.

This matters because Set is the single most common node in the corpus after the trigger. Every imported workflow that shapes data at all lands on a control that cannot express what its author wrote, and the loss is invisible until the exported file is opened somewhere else.

## Acceptance criteria

- [x] `PropertyKind` gains `assignmentCollection` and `knownPropertyKind` accepts it while still rejecting an unknown kind, proven by a registry test covering both directions.
- [x] An `assignmentCollection` property carries an ordered list of `{id, name, type, value}` entries in its own typed field rather than overloaded onto `Options`, and registration rejects an entry with an empty name.
- [x] The accepted `type` values are exactly those n8n's Set v3 emits, and an entry carrying a type outside that set is refused at registration rather than silently treated as a string.
- [x] `cloneProperties` deep-copies an assignment collection's default; a test mutates the entries of a returned definition and re-reads the registry unchanged.
- [x] `property-field.svelte` renders the kind as repeatable typed rows and never falls through to the text input, proven by a component test asserting the value reaching `onChange` is still an array.
- [x] Importing an n8n Set v3 node and exporting it again preserves assignment order and each entry's declared type, proven by a fixture comparing the arrays element by element.
- [x] `/api/v1/node-types` carries the new kind and the regenerated clients match, proven by `pnpm generate:api:check` in `web/` and `pnpm generate:types:check` in `sdk/` run by hand and recorded here.
- [x] A Set node stored before this ticket, whose `assignments` is a plain object, still loads, still validates and still produces the same items, proven by a fixture using the old shape.

## Outcome

`assignmentCollection` is a kind of its own, with an `Assignments []Assignment` carrier on `PropertyDefinition` rather than an overload of `Options` — the same rule every other nested carrier follows, and for the same reason: a field whose meaning depends on the sibling kind produces a JSON schema the generated TypeScript cannot express as better than `unknown`.

**The five accepted types are verifiable, not guessed.** `string`, `number`, `boolean`, `array` and `object` are what the reference checkout's `Set/v2/manual.mode.ts` type picker lists verbatim, and the assignment collection replaced that control without widening it. n8n's `FieldType` union is much wider — `dateTime`, `url`, `jwt`, `form-fields` — but those belong to other controls, and accepting one here would accept a type this product has no editor for. A row declaring one is refused at registration, and the importer degrades it to a string rather than writing a document nothing can render.

**The node switched with the kind, against the plan's default recommendation.** The plan suggested leaving `setNode()` on `keyValue` until FEAT-jwhdsy, and said what would reopen it: the two being taken together. The round-trip criterion is what reopened it — order and type cannot survive an export unless the document actually carries them — so `setNode()`, `executeSet` and `validateSetConfiguration` moved in this change. What is *not* claimed is the rest of FEAT-jwhdsy: only Set moved, and only its assignment shape.

**Both shapes run, told apart structurally.** The ordered shape is n8n's `{assignments: [...]}`; the flat one is the `{name: value}` map KilasFlow stored before. They are distinguished by whether the `assignments` key holds a list of objects carrying a `name`, not by a version flag — a flag is a thing every hand-authored document has to remember to set. A legacy field genuinely called `assignments` holding named objects would be read as the ordered shape; that is documented where it is decided.

**The type is load-bearing at run time.** A row declared a number whose value arrives as text — which is exactly what an expression over a string field produces — becomes a number. A value that cannot be read as its declared type is named rather than coerced into something plausible: `assignment "count" is declared a number and "not a number" is not one`.

**Two rows may write the same field**, and the later one wins. That is the thing a map could not express at all, and it is asserted in both the executor test and the round-trip test.

### The panel

`property-field.svelte` renders repeatable rows with a per-row type select and a value editor chosen by that type. The fallthrough the plan warned about had already been hardened by a later ticket — it degrades to a named read-only JSON view rather than a text input — but the branch is what makes the kind usable rather than merely non-destructive. `readAssignments` also opens a legacy node as string rows in alphabetical order, so editing an old node upgrades it rather than refusing it.

### The corpus

| | Before | After |
| --- | ---: | ---: |
| Activatable | 12 / 39 | **13 / 39** |
| Runnable | 2 / 39 | **3 / 39** |

`Set.workflow.null_values` now compiles and runs. `Set.v3.workflow` moved from failing on Set to failing on `n8n-nodes-base.noOp` — a different backlog, and the report names it.

## Implementation Plan

Settle the Go shape before anything renders it. `node.Definition` is serialised directly as the `/api/v1/node-types` payload by `internal/api/handlers/nodes.go`, so the carrier's field names and JSON tags are an API contract from the moment they exist; every later adjustment is a client regeneration in two packages. Add the kind, the typed carrier and its validation as one change, then move outward.

Recommend a dedicated `Assignments` carrier on `PropertyDefinition` holding ordered entries, each with a name, a declared type and a default value. Reject overloading `Options` to hold them — the same overload FEAT-5s1w0t rejects for `collection` and `fixedCollection`, and for the same reason: a field whose meaning depends on the sibling `kind` produces a JSON schema the generated TypeScript client cannot express as anything better than `unknown`.

Recommend implementing it as its own kind rather than as a preset over `fixedCollection`. The two look alike from a distance and differ where it counts: an assignment's value control is chosen by its own `type` field, row by row, which a generic repeatable group cannot express without the panel special-casing it anyway. Building the special case into the kind keeps it in one place and lets `validateProperties` say something specific about a malformed entry.

The trap is the Svelte fallthrough. `web/src/lib/components/workflow-editor/property-field.svelte` ends its kind chain with a bare `{:else}` at line 149 that renders any unrecognised kind as a plain text input bound to `stringValue`, which is `String(value)`. An array of assignment objects therefore displays as `[object Object]`, and the first keystroke writes that literal string back through `onChange`. Nothing throws, nothing warns, and the node's parameters are destroyed on first edit. Adding the kind in Go without adding the branch is worse than not adding it at all.

Keep expression evaluation out of scope, but record what is found. `executeSet` at `nodes/executors.go:58` never calls `expression.Resolve`, unlike `nodes/database.go:204`, `nodes/http.go:181`, `nodes/webhook.go:235` and `nodes/ai.go:243` — so a Set assignment holding a template is written into the item literally. That is a runtime defect belonging to FEAT-jwhdsy, which owns Set's semantics; this ticket delivers the shape the panel and the document need, and must not quietly fix the executor on the way past.

**Whether `setNode()` switches in this ticket.** Recommend adding the kind and leaving `setNode()` on `PropertyKeyValue` until FEAT-jwhdsy changes `executeSet` with it. A definition advertising typed ordered assignments while the executor still reads `map[string]any` is a node whose editor and runtime disagree, and the disagreement is silent — the panel accepts a typed entry the runner then drops. What would reopen it is the two tickets being taken together or FEAT-jwhdsy landing first, in which case the kind and the switch belong in one change and the interim shape is never built.

## References

- Roadmap plan, p4 section, entry V2-p4-13: `.pine/roadmap.md`.
- `internal/node/registry.go` — `PropertyKind` at 16 to 21, `PropertyDefinition` at 38, `knownPropertyKind` at 263, `cloneProperties` at 296 and `requiredParameters` at 278.
- `nodes/core.go` — `setNode` at line 57 and its single `PropertyKeyValue` assignments parameter.
- `nodes/executors.go` — `executeSet` at 58 and `validateSetConfiguration` at 111, both reading `map[string]any`, and the absent `expression.Resolve` call.
- `internal/interop/n8n/parameters.go` — `setToKilas` at 80, `setToN8N` at 135 and the hardcoded `"type":  "string"` at 143.
- `web/src/lib/components/workflow-editor/property-field.svelte` — the kind chain, the `expressionCapable` guard at line 14, and the `{:else}` text-input fallthrough at 149.
- `internal/api/handlers/nodes.go` — `NodeTypesOutput`, which serialises `node.Definition` straight onto the wire.
- `.pine/tickets/FEAT-5s1w0t.md` — V2-p2-2, whose deferral paragraph names this entry as the owner.
- `.pine/tickets/FEAT-jwhdsy.md` — the data-shaping parity ticket that consumes the kind and owns Set's runtime semantics.
- n8n 2.34.0 reference checkout (read-only, outside this repo): `packages/nodes-base/nodes/Set/v2/SetV2.node.ts`, present in the sparse checkout, for the assignment entry shape and its type list.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `4816f2ed` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `cf72f8d0` — chore(pine): open phase p9 for the Datastore and route database parity to p4
- Files changed (base → working tree):

```
 .pine/tickets/FEAT-0f87fn.md                       |  300 +-
 .pine/tickets/FEAT-12s0e5.md                       |   65 +
 .pine/tickets/FEAT-1axhdn.md                       |   65 +
 .pine/tickets/FEAT-1c70nt.md                       |   68 +
 .pine/tickets/FEAT-2f68r8.md                       |   81 +-
 .pine/tickets/FEAT-2phs15.md                       |   68 +
 .pine/tickets/FEAT-3xqky1.md                       |   70 +
 .pine/tickets/FEAT-45tfmh.md                       |   68 +
 .pine/tickets/FEAT-4d0bje.md                       |   62 +
 .pine/tickets/FEAT-55v09k.md                       |   82 +-
 .pine/tickets/FEAT-5fv8gf.md                       |    3 +
 .pine/tickets/FEAT-5kfctc.md                       |   66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   81 +-
 .pine/tickets/FEAT-5rvtzc.md                       |   91 +-
 .pine/tickets/FEAT-5s1w0t.md                       |   83 +-
 .pine/tickets/FEAT-68zzqs.md                       |   65 +
 .pine/tickets/FEAT-6vfn3s.md                       |  354 +-
 .pine/tickets/FEAT-8r9n21.md                       |  304 +-
 .pine/tickets/FEAT-91as16.md                       |   93 +-
 .pine/tickets/FEAT-9dqn7d.md                       |   66 +
 .pine/tickets/FEAT-9knk67.md                       |   84 +-
 .pine/tickets/FEAT-adzn0a.md                       |   74 +-
 .pine/tickets/FEAT-agj52c.md                       |   64 +
 .pine/tickets/FEAT-bp0ytb.md                       |  338 +-
 .pine/tickets/FEAT-cjpbe6.md                       |   70 +
 .pine/tickets/FEAT-ddzk2k.md                       |    2 +-
 .pine/tickets/FEAT-g6wrxm.md                       |   64 +
 .pine/tickets/FEAT-gxppx1.md                       |   71 +
 .pine/tickets/FEAT-k9dwgn.md                       |   65 +
 .pine/tickets/FEAT-n19dch.md                       |   66 +
 .pine/tickets/FEAT-n5fdz3.md                       |   69 +
 .pine/tickets/FEAT-nch9dg.md                       |   67 +
 .pine/tickets/FEAT-nrfg6e.md                       |   69 +
 .pine/tickets/FEAT-nrfz6m.md                       |   64 +
 .pine/tickets/FEAT-pd3p6x.md                       |   85 +-
 .pine/tickets/FEAT-pnbt4z.md                       |   91 +
 .pine/tickets/FEAT-ptyh9w.md                       |   65 +
 .pine/tickets/FEAT-qcm5ec.md                       |   74 +-
 .pine/tickets/FEAT-qe6wb8.md                       |  342 +-
 .pine/tickets/FEAT-qfr9xe.md                       |   39 +
 .pine/tickets/FEAT-sar60r.md                       |   87 +-
 .pine/tickets/FEAT-sdjdh2.md                       |   33 +
 .pine/tickets/FEAT-snxxny.md                       |   68 +
 .pine/tickets/FEAT-sp8cfm.md                       |  359 +-
 .pine/tickets/FEAT-ss44d9.md                       |   67 +
 .pine/tickets/FEAT-t26rt7.md                       |   65 +
 .pine/tickets/FEAT-v8k1tc.md                       |   88 +-
 .pine/tickets/FEAT-whn5vb.md                       |  101 +-
 .pine/tickets/FEAT-wkmv5e.md                       |   67 +
 .pine/tickets/FEAT-xeq6st.md                       |   68 +
 .pine/tickets/FEAT-xqqjqv.md                       |   93 +
 .pine/tickets/FEAT-xx6p22.md                       |   62 +
 .pine/tickets/FEAT-znm60y.md                       |  314 +-
 .pine/tickets/FEAT-ztxs5p.md                       |  345 +-
 Makefile                                           |   11 +
 README.md                                          |   33 +
 cmd/kilasflow/main.go                              |  113 +-
 cmd/nodepackgen/generate.go                        |  576 +++
 cmd/nodepackgen/generate_test.go                   |  374 ++
 cmd/nodepackgen/main.go                            |  165 +
 cmd/nodepackgen/openapi.go                         |  168 +
 cmd/nodepackgen/testdata/manifest.json             |   12 +
 cmd/nodepackgen/testdata/pack.golden.json          |  238 ++
 cmd/nodepackgen/testdata/report.golden.md          |   20 +
 cmd/nodepackgen/testdata/spec.json                 |   98 +
 config.example.yaml                                |   17 +
 internal/api/handlers/credentials.go               |   73 +
 internal/api/handlers/interop.go                   |    5 +-
 internal/api/handlers/nodes.go                     |  262 +-
 internal/api/handlers/workflows.go                 |  113 +-
 internal/api/middleware/embed.go                   |   10 +-
 internal/api/routes.go                             |   10 +-
 internal/api/server.go                             |   15 +-
 internal/api/workflows_test.go                     |    4 +-
 internal/binary/binary.go                          |  212 ++
 internal/binary/binary_test.go                     |  210 ++
 internal/config/config.go                          |   33 +
 internal/credentials/builtin.go                    |  153 +
 internal/credentials/credentials.go                |   98 +-
 internal/credentials/credentials_test.go           |  242 ++
 internal/credentials/registry.go                   |  340 ++
 internal/engine/authenticate.go                    |   95 +
 internal/engine/runner.go                          |  418 ++-
 internal/engine/runner_test.go                     |  579 ++-
 internal/engine/service.go                         |   45 +-
 internal/engine/service_test.go                    |   33 +-
 internal/execution/records.go                      |   27 +-
 internal/expression/doc.go                         |   82 +-
 internal/expression/expression.go                  |  328 +-
 internal/expression/expression_test.go             |  309 +-
 internal/expression/functions.go                   |  219 ++
 internal/expression/roots.go                       |  125 +
 internal/expression/undefined.go                   |   22 +
 internal/interop/n8n/corpus/BASELINE.md            |   34 +-
 internal/interop/n8n/corpus/baseline.json          |  114 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   64 +-
 internal/interop/n8n/n8n.go                        |  289 +-
 internal/interop/n8n/n8n_test.go                   |  503 ++-
 internal/interop/n8n/parameters.go                 |  171 +-
 internal/loadoptions/loadoptions.go                |  353 ++
 internal/loadoptions/loadoptions_test.go           |  354 ++
 internal/node/icon.go                              |   94 +
 internal/node/registry.go                          |  598 ++-
 internal/node/registry_test.go                     |  573 ++-
 internal/nodepack/nodepack.go                      |  424 +++
 internal/nodepack/startcase.go                     |  136 +
 internal/nodepack/startcase_test.go                |   82 +
 internal/nodepack/trigger.go                       |  433 +++
 internal/property/loader.go                        |   92 +
 internal/property/property.go                      |  272 ++
 internal/property/testdata/visibility.json         |  163 +
 internal/property/visibility.go                    |  315 ++
 internal/property/visibility_test.go               |   77 +
 internal/repository/executions.go                  |    2 +
 internal/repository/models.go                      |   37 +-
 internal/repository/webhooks.go                    |   17 +-
 internal/routing/doc.go                            |   54 +
 internal/routing/executor.go                       |  571 +++
 internal/routing/request.go                        |  408 +++
 internal/routing/response.go                       |  177 +
 internal/routing/routing.go                        |  373 ++
 internal/routing/routing_test.go                   |  764 ++++
 internal/webhook/export_test.go                    |   14 +
 internal/webhook/lifecycle.go                      |  304 ++
 internal/webhook/lifecycle_test.go                 |  229 ++
 internal/webhook/request_lifecycle.go              |  207 ++
 internal/webhook/shape.go                          |  248 ++
 internal/webhook/shape_test.go                     |  211 ++
 internal/webhook/webhook.go                        |  187 +-
 internal/webhook/webhook_test.go                   |   48 +-
 internal/workflow/compiler.go                      |  225 +-
 internal/workflow/document.go                      |   81 +-
 internal/workflow/document_test.go                 |  360 +-
 internal/workflow/typeversion.go                   |   10 +
 nodes/ai.go                                        |   54 +-
 nodes/annotation.go                                |    3 +
 nodes/bindings_test.go                             |  129 +
 nodes/code.go                                      |   26 +-
 nodes/code_test.go                                 |   60 +
 nodes/core.go                                      |   30 +-
 nodes/database.go                                  |   12 +-
 nodes/executors.go                                 |  141 +-
 nodes/executors_test.go                            |  329 ++
 nodes/http.go                                      |  191 +-
 nodes/http_test.go                                 |  225 ++
 nodes/loop.go                                      |  245 ++
 nodes/presentation_test.go                         |   60 +
 nodes/routing.go                                   |   23 +
 nodes/telegram.go                                  |  407 +++
 nodes/telegram_download.go                         |  223 ++
 nodes/telegram_lifecycle.go                        |  423 +++
 nodes/telegram_test.go                             |  616 ++++
 nodes/unsupported.go                               |    4 +
 nodes/webhook.go                                   |   35 +-
 packs/telegram/README.md                           |   40 +
 packs/telegram/pack.json                           | 1119 ++++++
 packs/telegram/telegram.go                         |   58 +
 packs/telegram/telegram_test.go                    |  466 +++
 packs/waha/README.md                               |   32 +
 packs/waha/REPORT-202409.md                        |  100 +
 packs/waha/REPORT-202502.md                        |  128 +
 packs/waha/manifest-202409.json                    |  124 +
 packs/waha/manifest-202502.json                    |  124 +
 packs/waha/pack-202409.json                        | 2794 ++++++++++++++
 packs/waha/pack-202502.json                        | 3844 ++++++++++++++++++++
 packs/waha/pack-trigger-202409.json                |  124 +
 packs/waha/pack-trigger-202502.json                |  130 +
 packs/waha/waha.go                                 |  107 +
 packs/waha/waha_test.go                            | 1193 ++++++
 sdk/src/generated/models.ts                        |  532 ++-
 .../lib/api/generated/credentials/credentials.ts   |   96 +-
 .../lib/api/generated/models/activationNotice.ts   |   13 +
 .../lib/api/generated/models/activationResource.ts |   23 +
 web/src/lib/api/generated/models/condition.ts      |   14 +
 .../api/generated/models/credentialRequirement.ts  |   15 +
 web/src/lib/api/generated/models/definition.ts     |   16 +
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/expressionGrammar.ts  |   16 +
 web/src/lib/api/generated/models/field.ts          |    1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |   19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |   15 +
 web/src/lib/api/generated/models/index.ts          |   21 +
 .../api/generated/models/loadOptionsInputBody.ts   |   20 +
 .../models/loadOptionsInputBodyParameters.ts       |    9 +
 .../api/generated/models/loadOptionsResource.ts    |   17 +
 web/src/lib/api/generated/models/nodeCodex.ts      |   16 +
 .../api/generated/models/nodeCodexSubcategories.ts |    9 +
 web/src/lib/api/generated/models/nodeIcon.ts       |   12 +
 web/src/lib/api/generated/models/option.ts         |   12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |   21 +
 web/src/lib/api/generated/models/port.ts           |    9 +-
 .../lib/api/generated/models/propertyDefinition.ts |   14 +
 web/src/lib/api/generated/models/propertyGroup.ts  |   15 +
 .../api/generated/models/testCredentialResource.ts |   14 +
 web/src/lib/api/generated/models/typeOptions.ts    |   17 +
 web/src/lib/api/generated/models/visibility.ts     |   15 +
 .../lib/api/generated/models/webhookDeclaration.ts |   15 +
 web/src/lib/api/generated/nodes/nodes.ts           |  342 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    3 +-
 .../components/workflow-editor/canvas-node.svelte  |   33 +-
 .../workflow-editor/execution-canvas-node.svelte   |   20 +-
 .../components/workflow-editor/node-icon.svelte    |   10 +-
 .../components/workflow-editor/node-picker.svelte  |    8 +-
 .../workflow-editor/properties-panel.svelte        |   46 +-
 .../workflow-editor/property-field.svelte          |  203 +-
 .../workflow-editor/workflow-editor.svelte         |    4 +-
 web/src/lib/workflow-editor/credentials.ts         |   41 +-
 web/src/lib/workflow-editor/document.test.ts       |   10 +-
 web/src/lib/workflow-editor/document.ts            |    4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |    2 +-
 web/src/lib/workflow-editor/execution.test.ts      |   72 +-
 web/src/lib/workflow-editor/execution.ts           |   75 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |  163 +-
 web/src/lib/workflow-editor/node-visual.ts         |  208 +-
 web/src/lib/workflow-editor/ports.test.ts          |   98 +-
 web/src/lib/workflow-editor/ports.ts               |   34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |   35 +
 web/src/lib/workflow-editor/visibility.ts          |  179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |   26 +
 221 files changed, 36554 insertions(+), 1109 deletions(-)
```
