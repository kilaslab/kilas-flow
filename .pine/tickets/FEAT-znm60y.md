---
id: FEAT-znm60y
title: Generate node packs from an OpenAPI document
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-8r9n21
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:00:24Z"
updated: "2026-09-05T10:38:34Z"
---

## Scope

WAHA's n8n package is not hand-written. It is produced by `@devlikeapro/n8n-openapi-node` from WAHA's own MIT `openapi.json`, which is why it carries 124 operations and no `execute()`. This ticket builds the equivalent for KilasFlow: a build-time generator that turns an OpenAPI 3 document into a node pack of definitions plus the routing metadata the interpreter from the previous ticket executes. WAHA is the first customer; any other OpenAPI-described service becomes a pack for free.

The hard requirement is not "generate something usable" — it is "generate the same strings". An imported n8n workflow carries the literal `resource` and `operation` values the original package chose, and KilasFlow matches an imported node against its own parameter options. Get the naming wrong by one character and every imported WAHA workflow selects nothing. The rules `@devlikeapro/n8n-openapi-node` applies are: `resource` is `lodash.startCase` of the OpenAPI tag with non-alphanumeric characters stripped, and `operation` is `startCase` of the `operationId` minus its first `_`-separated segment. Both come out as human-readable strings with spaces — `"Chatting"`, `"Send Text"` — not slugs, and that is what sits in real workflow JSON.

The generator's second rule is about body shape: only first-level request-body properties become structured parameters. A nested object arrives as a single JSON-string parameter that a user fills with an expression. Reproducing that is not laziness — it is what makes the generated parameter set match the one the imported workflow was authored against.

KilasFlow has no code generation of any kind today; `nodes/core.go`'s `RegisterAll` is a hand-written list of seventeen definitions. This ticket introduces the first generated artifact and therefore also has to settle how a pack is shipped, loaded and reviewed.

## Acceptance criteria

- [x] A Go command under `cmd/` takes an OpenAPI 3 document plus a small pack manifest (node type, display name, credential type, version) and writes a node pack.
- [x] `resource` and `operation` values reproduce `lodash.startCase` semantics exactly, proven by a golden test whose fixture includes tags with punctuation, `operationId`s with and without a leading `_` segment, and identifiers containing digit runs.
- [x] Path, query and first-level request-body properties become parameters with the right kind, required flag and default; nested request-body objects become one JSON parameter each rather than being flattened or dropped.
- [x] Every generated operation carries routing metadata the interpreter executes with no node-specific Go, and each operation's parameters are gated by `displayOptions` on the owning resource.
- [x] Regenerating a pack from an unchanged document produces a byte-identical file: map iteration never reaches the output.
- [x] Constructs the generator cannot express — `oneOf`/`anyOf` request bodies, multipart uploads, non-JSON media types, security schemes with no credential mapping — are listed in a generation report and are absent from the pack, never emitted as a guess.
- [x] A generated pack registers into `internal/node.Registry` under its own type namespace and version, and the registry rejects a pack whose executor binding has no executor.

## Outcome

`cmd/nodepackgen` reads an OpenAPI 3 document and a manifest and writes a pack plus a report. Run against the vendored WAHA spec it produces **123 of 124 operations across 13 resources**, with one skipped and named: `GET /api/version` (`VersionController_get`) and `GET /api/server/version` (`ServerController_get`) both reduce to the operation name `Get` inside the resource `Observability`, so the second is refused rather than overwriting the first.

**The pack format is not a dump of the internal types.** It is shaped the way the thing it describes is shaped — resources, each a list of operations, each with its request and its sends — so one operation's whole story sits in one place and a review diff is local. `node.Definition` could not have been dumped anyway: `ExecutorID` and `Validate` are not serialisable, and that turns out to be the right constraint. A pack cannot name its own executor; `Load` sets it, so a pack cannot claim a binding with privileges no pack should reach.

**`StartCase` is in `internal/nodepack`, not in the command.** It decides whether every imported WAHA workflow matches or silently selects nothing, and the importer will need the same function. It is a faithful reimplementation of lodash's word splitter — case boundaries, non-alphanumeric separators, and the letter/digit boundary that makes `utf8` two words — with its own table test over the shapes real documents contain.

**Three things the format forced, all recorded in the package docs:**

1. *One property per key.* The node registry refuses duplicate parameter keys, deliberately. n8n keeps one property per operation and lets several share a name, disambiguated by displayOptions. So a parameter used by six operations becomes one property that says which resources and operations show it — and where the six disagree about kind or options it degrades to a string and says so in the report. It is required only when every operation that shows it requires it: the visibility union cannot say "required here but not there", and a field demanded where it is not used makes the node unsavable.

2. *Placement moved to the operation.* `chatId` is a path parameter in one operation and a query parameter in another. With one property per key that cannot live on the property, so `routing.Routing` gained `Sends`, a list naming the parameter it reads. A `sends` entry whose parameter is not currently shown sends nothing, so a leftover value from a resource the node is no longer set to cannot ride along.

3. *A resource/operation cascade.* Operation names repeat across resources — WAHA has four distinct `Get` operations — so an `operation` → routing map would have collapsed them onto whichever request was written last. `routing.Cascade` routes on both properties, and choosing a pair that has no operation is a clear error naming the pair rather than a request to the wrong endpoint.

Path parameters are also now substituted structurally rather than as templates, and percent-escaped one segment at a time. A chat id containing a slash written as `{{ $parameter.chatId }}` would silently change which endpoint is called, and the value comes from a workflow author or from item data.

Deferred, and worth naming: the `operation` picker lists every operation name once, across all resources, because a KilasFlow definition holds one `operation` property rather than one per resource. The cascade makes this correct at run time but noisy in the editor. `internal/loadoptions` already has the mechanism to fix it — an internal options loader with `DependsOn: ["resource"]` — and that belongs with FEAT-qe6wb8, where a real pack makes the difference visible.

## Implementation Plan

Write the generator as `cmd/nodepackgen`, reading the OpenAPI document and a manifest, and emitting the pack. Settle the shipping format first, because everything else follows from it: emit a JSON pack file committed to the repository and embedded with `go:embed`, not generated Go source and not a document parsed at server start. A JSON pack diffs readably in review, keeps the single binary intact, and means a bad spec change can never turn into a compile error at deploy time. Generated Go would be reviewable too, but 124 operations of it would drown every future diff in this repository; parsing OpenAPI at startup would put a third-party document on the boot path.

Implement `startCase` before anything else and test it in isolation. Lodash's word splitter breaks on case boundaries, on non-alphanumeric separators, and between letter runs and digit runs — so `sendText`, `send_text` and `send-text` all become `Send Text`, while an identifier containing digits does not survive a naive `strings.Title`. This one function decides whether every imported WAHA workflow matches or silently selects nothing, so it gets its own table-driven test with the real WAHA `operationId` list once the spec is vendored.

Then walk the document: group operations by first tag into resources, sort resources and operations by their generated display strings so output is stable, and build one `options` property for `resource`, one per resource for `operation`, and the parameter set for each operation. Map OpenAPI types onto the property kinds the registry has — which today are only `string`, `number`, `boolean`, `select`, `keyValue` and `conditions` (`internal/node/registry.go`), so a generator run before the property-kinds work lands will have to degrade `enum` to `select` and everything structured to `string`. Note that limitation in the generation report rather than hiding it.

The trap is the security scheme. An OpenAPI document describes how to authenticate but not which KilasFlow credential type carries it; the manifest supplies that mapping, and a scheme with no mapping must fail generation loudly. A pack that generates cleanly and then cannot authenticate is the worst outcome available here.

## References

- Roadmap plan, p3 section, entry V2-p3-2: `.pine/roadmap.md`.
- n8n 2.34.0 reference checkout, `packages/workflow/src/Interfaces.ts` — the routing and property metadata shapes a pack must fill in.
- `internal/node/registry.go` — `Definition`, `PropertyDefinition`, `PropertyKind` and `Register`'s immutability rule.
- `nodes/core.go` `RegisterAll` — the hand-written registration this generator's output has to join.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (1):
  - `71f15dab` — chore(pine): open the V2 n8n-first epic
- Files changed (base → working tree):

```
 .gitignore                                         |    10 +
 .pine/MEMORY.md                                    |     2 +
 .pine/memory/licensing.md                          |    12 +
 .pine/memory/n8n-reference.md                      |    11 +
 .pine/roadmap.md                                   |   909 ++
 .pine/tickets/EPIC-m42s3g.md                       |    73 +
 .pine/tickets/FEAT-096vs9.md                       |    53 +
 .pine/tickets/FEAT-0f87fn.md                       |   337 +
 .pine/tickets/FEAT-12s0e5.md                       |    65 +
 .pine/tickets/FEAT-1500sp.md                       |    58 +
 .pine/tickets/FEAT-1axhdn.md                       |    65 +
 .pine/tickets/FEAT-1br8at.md                       |   128 +
 .pine/tickets/FEAT-1c70nt.md                       |    68 +
 .pine/tickets/FEAT-2f68r8.md                       |   129 +
 .pine/tickets/FEAT-2phs15.md                       |    68 +
 .pine/tickets/FEAT-347egc.md                       |    54 +
 .pine/tickets/FEAT-3xqky1.md                       |    70 +
 .pine/tickets/FEAT-45tfmh.md                       |    68 +
 .pine/tickets/FEAT-48hreg.md                       |    61 +
 .pine/tickets/FEAT-4d0bje.md                       |    62 +
 .pine/tickets/FEAT-55v09k.md                       |   124 +
 .pine/tickets/FEAT-5fv8gf.md                       |    59 +
 .pine/tickets/FEAT-5kfctc.md                       |    66 +
 .pine/tickets/FEAT-5kv1jq.md                       |   118 +
 .pine/tickets/FEAT-5rvtzc.md                       |   135 +
 .pine/tickets/FEAT-5s1w0t.md                       |   124 +
 .pine/tickets/FEAT-68zzqs.md                       |    65 +
 .pine/tickets/FEAT-6vfn3s.md                       |    61 +
 .pine/tickets/FEAT-7cg0cd.md                       |    60 +
 .pine/tickets/FEAT-8qyfh1.md                       |    53 +
 .pine/tickets/FEAT-8r9n21.md                       |   343 +
 .pine/tickets/FEAT-91as16.md                       |   141 +
 .pine/tickets/FEAT-9555xz.md                       |    58 +
 .pine/tickets/FEAT-96p7m3.md                       |    52 +
 .pine/tickets/FEAT-9dqn7d.md                       |    66 +
 .pine/tickets/FEAT-9knk67.md                       |   121 +
 .pine/tickets/FEAT-a6yg3n.md                       |   126 +
 .pine/tickets/FEAT-a94c8y.md                       |    60 +
 .pine/tickets/FEAT-adzn0a.md                       |   112 +
 .pine/tickets/FEAT-afs850.md                       |   113 +
 .pine/tickets/FEAT-agj52c.md                       |    64 +
 .pine/tickets/FEAT-ajw7wt.md                       |    61 +
 .pine/tickets/FEAT-az620p.md                       |    54 +
 .pine/tickets/FEAT-bp0ytb.md                       |    59 +
 .pine/tickets/FEAT-c2a081.md                       |    55 +
 .pine/tickets/FEAT-cgm1y3.md                       |    50 +
 .pine/tickets/FEAT-cjpbe6.md                       |    70 +
 .pine/tickets/FEAT-csqgg5.md                       |   145 +
 .pine/tickets/FEAT-ddzk2k.md                       |    59 +
 .pine/tickets/FEAT-ej0468.md                       |    54 +
 .pine/tickets/FEAT-fw0m2q.md                       |   117 +
 .pine/tickets/FEAT-g6wrxm.md                       |    64 +
 .pine/tickets/FEAT-gjzgkd.md                       |    59 +
 .pine/tickets/FEAT-gvn62x.md                       |    57 +
 .pine/tickets/FEAT-gxppx1.md                       |    71 +
 .pine/tickets/FEAT-hv4q8e.md                       |   126 +
 .pine/tickets/FEAT-je4f4t.md                       |    56 +
 .pine/tickets/FEAT-jwhdsy.md                       |    51 +
 .pine/tickets/FEAT-k3fmj1.md                       |   141 +
 .pine/tickets/FEAT-k3grr5.md                       |   126 +
 .pine/tickets/FEAT-k65hqv.md                       |    60 +
 .pine/tickets/FEAT-k9dwgn.md                       |    65 +
 .pine/tickets/FEAT-knpfqf.md                       |    56 +
 .pine/tickets/FEAT-mvegj5.md                       |    56 +
 .pine/tickets/FEAT-n19dch.md                       |    66 +
 .pine/tickets/FEAT-n5fdz3.md                       |    69 +
 .pine/tickets/FEAT-nbqye0.md                       |   129 +
 .pine/tickets/FEAT-nch9dg.md                       |    67 +
 .pine/tickets/FEAT-nrfg6e.md                       |    69 +
 .pine/tickets/FEAT-nrfz6m.md                       |    64 +
 .pine/tickets/FEAT-pd3p6x.md                       |   129 +
 .pine/tickets/FEAT-ptyh9w.md                       |    65 +
 .pine/tickets/FEAT-q81bq4.md                       |    56 +
 .pine/tickets/FEAT-qcm5ec.md                       |   117 +
 .pine/tickets/FEAT-qe6wb8.md                       |    57 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-snxxny.md                       |    68 +
 .pine/tickets/FEAT-sp8cfm.md                       |    57 +
 .pine/tickets/FEAT-ss44d9.md                       |    67 +
 .pine/tickets/FEAT-t26rt7.md                       |    65 +
 .pine/tickets/FEAT-t5q318.md                       |   131 +
 .pine/tickets/FEAT-v8k1tc.md                       |   132 +
 .pine/tickets/FEAT-vvwpjw.md                       |    57 +
 .pine/tickets/FEAT-w9kqeg.md                       |    96 +
 .pine/tickets/FEAT-whn5vb.md                       |   143 +
 .pine/tickets/FEAT-wkmv5e.md                       |    67 +
 .pine/tickets/FEAT-xeq6st.md                       |    68 +
 .pine/tickets/FEAT-xqqjqv.md                       |    66 +
 .pine/tickets/FEAT-xx6p22.md                       |    62 +
 .pine/tickets/FEAT-ybm2pd.md                       |    55 +
 .pine/tickets/FEAT-yyjfjq.md                       |   123 +
 .pine/tickets/FEAT-znm60y.md                       |    74 +
 .pine/tickets/FEAT-ztxs5p.md                       |    59 +
 Makefile                                           |     8 +
 cmd/kilasflow/main.go                              |    78 +-
 config.example.yaml                                |    17 +
 internal/ai/ai_test.go                             |    47 +
 internal/api/handlers/credentials.go               |    73 +
 internal/api/handlers/executions.go                |     1 +
 internal/api/handlers/interop.go                   |    58 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |    78 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    12 +-
 internal/api/server.go                             |    15 +-
 internal/api/workflows_test.go                     |    73 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/config/config.go                          |    33 +
 internal/credentials/builtin.go                    |   108 +
 internal/credentials/credentials.go                |    91 +-
 internal/credentials/credentials_test.go           |   229 +
 internal/credentials/registry.go                   |   312 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   830 +-
 internal/engine/runner_test.go                     |  1142 +-
 internal/engine/service.go                         |    85 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/worker_test.go                     |     4 +-
 internal/execution/records.go                      |    55 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    82 +-
 internal/expression/expression.go                  |   328 +-
 internal/expression/expression_test.go             |   309 +-
 internal/expression/functions.go                   |   219 +
 internal/expression/roots.go                       |   125 +
 internal/expression/undefined.go                   |    22 +
 internal/guardrails/doc.go                         |     8 +
 internal/guardrails/licence_boundary_test.go       |   359 +
 internal/interop/n8n/corpus/BASELINE.md            |   104 +
 internal/interop/n8n/corpus/MANIFEST.json          |   169 +
 internal/interop/n8n/corpus/baseline.json          |   435 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   548 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |   639 +-
 internal/interop/n8n/n8n_test.go                   |  1026 +-
 internal/interop/n8n/parameters.go                 |    70 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   626 +-
 internal/node/registry_test.go                     |   610 +-
 internal/property/loader.go                        |    92 +
 internal/property/property.go                      |   180 +
 internal/property/testdata/visibility.json         |   163 +
 internal/property/visibility.go                    |   315 +
 internal/property/visibility_test.go               |    77 +
 internal/repository/executions.go                  |    48 +-
 internal/repository/models.go                      |    97 +-
 internal/repository/models_test.go                 |    16 +-
 internal/repository/webhooks.go                    |   257 +-
 internal/routing/doc.go                            |    54 +
 internal/routing/executor.go                       |   371 +
 internal/routing/request.go                        |   354 +
 internal/routing/response.go                       |   172 +
 internal/routing/routing.go                        |   354 +
 internal/routing/routing_test.go                   |   711 ++
 internal/scheduler/scheduler.go                    |     8 +-
 internal/scheduler/scheduler_test.go               |    41 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   219 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   158 +
 internal/webhook/shape.go                          |   219 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   258 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   339 +-
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   149 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/bindings_test.go                             |   126 +
 nodes/code.go                                      |    28 +-
 nodes/code_test.go                                 |    68 +-
 nodes/core.go                                      |    49 +-
 nodes/database.go                                  |    14 +-
 nodes/database_test.go                             |     8 +-
 nodes/executors.go                                 |    29 +-
 nodes/executors_test.go                            |   113 +
 nodes/http.go                                      |   193 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |    47 +-
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   457 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |    96 +-
 .../models/{unsupported.ts => condition.ts}        |    11 +-
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    17 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     2 +
 .../lib/api/generated/models/executionSummary.ts   |     2 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    25 +-
 .../api/generated/models/loadOptionsInputBody.ts   |    20 +
 .../models/loadOptionsInputBodyParameters.ts       |     9 +
 .../api/generated/models/loadOptionsResource.ts    |    17 +
 web/src/lib/api/generated/models/node.ts           |     1 +
 web/src/lib/api/generated/models/nodeCodex.ts      |    16 +
 .../api/generated/models/nodeCodexSubcategories.ts |     9 +
 .../api/generated/models/{lossy.ts => nodeIcon.ts} |     7 +-
 web/src/lib/api/generated/models/option.ts         |    12 +
 web/src/lib/api/generated/models/optionsLoader.ts  |    21 +
 web/src/lib/api/generated/models/port.ts           |     9 +-
 .../lib/api/generated/models/propertyDefinition.ts |    11 +
 web/src/lib/api/generated/models/propertyGroup.ts  |    15 +
 .../api/generated/models/testCredentialResource.ts |    14 +
 web/src/lib/api/generated/models/typeOptions.ts    |    17 +
 web/src/lib/api/generated/models/visibility.ts     |    15 +
 .../lib/api/generated/models/webhookDeclaration.ts |    15 +
 .../api/generated/models/webhookRouteResource.ts   |    14 +
 web/src/lib/api/generated/nodes/nodes.ts           |   342 +-
 .../components/workflow-editor/canvas-node.svelte  |    33 +-
 .../workflow-editor/execution-canvas-node.svelte   |    20 +-
 .../components/workflow-editor/node-icon.svelte    |    10 +-
 .../components/workflow-editor/node-picker.svelte  |     8 +-
 .../workflow-editor/properties-panel.svelte        |    46 +-
 .../workflow-editor/property-field.svelte          |   138 +-
 .../workflow-editor/workflow-editor.svelte         |     4 +-
 web/src/lib/workflow-editor/credentials.ts         |    41 +-
 web/src/lib/workflow-editor/document.test.ts       |    10 +-
 web/src/lib/workflow-editor/document.ts            |     4 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |     2 +-
 web/src/lib/workflow-editor/execution.test.ts      |    72 +-
 web/src/lib/workflow-editor/execution.ts           |    86 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |    43 +
 web/src/lib/workflow-editor/node-visual.test.ts    |   156 +-
 web/src/lib/workflow-editor/node-visual.ts         |   204 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 263 files changed, 49227 insertions(+), 1130 deletions(-)
```
