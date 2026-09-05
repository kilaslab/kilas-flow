---
id: FEAT-6vfn3s
title: Add the Telegram action node at n8n parity
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-8r9n21
    - FEAT-ztxs5p
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:03:33Z"
updated: "2026-09-05T11:40:50Z"
---

## Scope

The trigger gets a message in; this node is how the bot answers. Match `n8n-nodes-base.telegram`'s resource and operation surface so an imported workflow maps one to one instead of landing on `kilasflow.unsupported`.

**Message**: Send Message, Send Photo, Send Document, Send Animation, Send Audio, Send Video, Send Sticker, Send Media Group, Send Location, Send Chat Action, Edit Message Text, Delete Chat Message, Pin Chat Message, Unpin Chat Message. **Chat**: Get, Get Administrators, Get Member, Leave, Set Title, Set Description. **Callback**: Answer Query, Answer Inline Query. **File**: Get File.

Send Chat Action belongs in the first cut alongside Send Message, not in a later pass. It is what produces the typing indicator, and a bot that thinks for four seconds in total silence reads as broken to the person waiting — that difference is worth more than several of the other operations combined.

The Bot API is regular enough that almost all of this is metadata, not Go: a method name, a chat id, a handful of scalar parameters, a JSON response. It executes on the declarative routing interpreter, which means it also inherits the SSRF policy and the credential host scope for free. The exceptions are the file operations, which need multipart uploads and the binary store, and Send Media Group, whose `media` argument is a JSON array referencing attachments.

Both Telegram types also need importer entries — `n8n-nodes-base.telegram` and `n8n-nodes-base.telegramTrigger` — added to the `mappings` table in `internal/interop/n8n/n8n.go`, whose exact-string matching is what the whole subset claim rests on.

## Acceptance criteria

- [x] All four resources and every operation listed above are registered, with n8n's own resource and operation values, so an imported node selects the same operation it selected in n8n.
- [x] Send Message and Send Chat Action work end to end: a Telegram Trigger delivery produces a typing indicator and then a reply in the same chat. **Against a stub Bot API, not a real bot** — everything but the network is real, including the compiler, the runner and the credential path.
- [x] Every operation that the Bot API expresses as a plain JSON call is declarative routing metadata with no operation-specific Go.
- [x] Send Photo, Send Document, Send Audio, Send Video, Send Animation and Send Media Group send a binary from the item's binary references as multipart, and also accept a `file_id` or URL, matching what the Bot API accepts.
- [x] Get File resolves the file path and stores the downloaded content in the binary store, attached to the output item as a binary reference rather than inlined into the item JSON.
- [x] Both `n8n-nodes-base.telegram` and `n8n-nodes-base.telegramTrigger` import onto the native nodes, `SupportedMappings()` lists them, and export reproduces the original type strings.
- [x] An operation parameter n8n supports that KilasFlow does not carry produces a named import diagnostic rather than being dropped in silence.
- [x] Failures report Telegram's own `description` field, since it is the only part of a Bot API error a user can act on.

## Outcome

23 operations across 4 resources, all of them metadata: `packs/telegram/pack.json` runs on the routing interpreter and this package contains no Go beyond registration. Hand-written rather than generated, because Telegram publishes HTML documentation and the community specs that exist are unvetted third-party artifacts.

**Three things the interpreter had to grow, all of them general rather than Telegram-shaped:**

- *Multipart.* `send` gained a `binary` placement: the parameter names a binary property on the item, and the interpreter streams that attachment out of the store as a file part. A non-empty file set is what makes a request multipart — there is no flag, because a flag could be set on a request that ends up sending no file. It is what lets the *same* operation either upload the item's attachment or name a `file_id`, decided by which parameter the user filled rather than by two operations.
- *A second call for the bytes.* `postReceive: binaryData` downloads a URL built from the response and attaches it. getFile is exactly the shape it serves — an API answers with a path, and the bytes are behind a call needing the same credential — and leaving it to the caller would mean every such node grew its own Go. WAHA's media will use it too.
- *A credential in the path.* `credentials.PlacementPath` substitutes `{credential.accessToken}` into the request path inside the package that already holds the secret. The alternative was exposing the token through `$credentials`, which carries non-secret fields only and is meant to keep carrying only those. It is explicitly not the same as declaring no authentication, which still means "this credential cannot sign an HTTP request" and is still refused — the guard that stops a postgres credential reaching an HTTP node.

Failures now carry the service's own words: `sendMessage failed: Bad Request: chat not found` rather than a bare 400. The URL is deliberately not repeated in the message, because a path-placed credential puts the token in it and `Redacted()` hides userinfo, not a path segment.

### A defect this uncovered

`$('Name')` never worked. `engine.cloneRequest` — the request each executor is handed — did not copy `NodeItems`, `Workflow` or `TriggerNodeID`, so the form every imported n8n workflow uses to read an earlier node resolved to *"that node has not produced output in this run"* no matter what had run. The evaluator was right and the data never reached it, which is why `internal/expression`'s own tests passed throughout. Fixed, with `TestDollarNodeByNameReachesTheExecutor` as the regression.

### Found and filed, not fixed here

`executeSet`, `executeIF` and `executeMerge` never resolve expressions: they read `node.Parameters` directly and take `engine.Request` as `_`. An imported Set node whose value is an expression writes the marker object into the item verbatim. That is the two most-used nodes in the product, and it belongs to the p4 parity tickets that rewrite them — filed as its own ticket rather than folded in here.

### Honest about the values

`resource` and `operation` values are what an imported workflow matches by literal string, and these were **reconstructed** from n8n-nodes-base's naming convention plus the labels in `design-refs/n8n-v2/12` and `13`: the reference checkout does not contain the Telegram node. They are pinned by a test and recorded in `packs/telegram/README.md`, so correcting one is a one-line diff rather than an archaeology exercise.

## Implementation Plan

Hand-write the routing metadata rather than running the pack generator. Telegram publishes its API as HTML documentation, not an OpenAPI document, and the community-maintained specs that do exist are unvetted third-party artifacts; for roughly twenty-one operations with stable parameter names, hand-written metadata reviewed once is cheaper and safer than importing a conversion of someone else's transcription. Keep it declarative anyway, so it runs on the same interpreter and needs no bespoke executor.

Structure it as `resource` and `operation` select parameters gating everything else through visibility conditions, exactly as the generated packs do — which means this node is a good early consumer of the wider `displayOptions` semantics, since several parameters are shown for more than one operation and the current single-key equality condition cannot express that.

Do the JSON operations first and prove the whole resource matrix with them, then add multipart. Multipart is the one place the interpreter's request model has to grow: it currently assembles a JSON or form body, and an upload needs a streamed file part alongside scalar fields. Add it to the interpreter as a body kind rather than special-casing Telegram inside the node, or the Telegram node quietly becomes programmatic again and WAHA's own send-image operations will need the same thing built a second time.

The trap is `chat_id`. Telegram accepts a numeric chat id, a `@channelusername` string, and in some contexts a user id, and the value in an imported workflow is usually an expression reading the trigger item. Send it as the string the parameter resolves to and let Telegram decide; coercing to a number will break every channel username, and coercing to a string is harmless because the API accepts both.

## References

- Roadmap plan, p3 section, entry V2-p3-7: `.pine/roadmap.md`.
- Telegram Bot API, https://core.telegram.org/bots/api — the method surface, `getFile` and its file path, and multipart upload of attachments.
- `internal/interop/n8n/n8n.go` — the `mappings` table, `byN8NType`, `SupportedMappings()`.
- `internal/node/registry.go` — `PropertyDefinition` and `VisibilityCondition`, whose single-key equality form is the limit this node runs into.
- n8n 2.34.0 reference checkout, `packages/nodes-base/nodes` — read the Telegram node's own descriptions once the sparse checkout is widened; it currently contains only `HttpRequest`, `If`, `Schedule` and `Set`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 12, 13 — the 27 actions grouped by resource in the picker, and the Resource/Operation cascade with Chat ID, Text, Reply Markup and Additional Fields. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

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
 .pine/tickets/FEAT-6vfn3s.md                       |    85 +
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
 .pine/tickets/FEAT-bp0ytb.md                       |   376 +
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
 .pine/tickets/FEAT-qe6wb8.md                       |   378 +
 .pine/tickets/FEAT-qfr9xe.md                       |    39 +
 .pine/tickets/FEAT-r6xhnp.md                       |    54 +
 .pine/tickets/FEAT-rj17xj.md                       |    64 +
 .pine/tickets/FEAT-sar60r.md                       |   124 +
 .pine/tickets/FEAT-sbnejr.md                       |    51 +
 .pine/tickets/FEAT-sdjdh2.md                       |    33 +
 .pine/tickets/FEAT-snxxny.md                       |    68 +
 .pine/tickets/FEAT-sp8cfm.md                       |   396 +
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
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |   384 +
 Makefile                                           |    19 +
 README.md                                          |    33 +
 cmd/kilasflow/main.go                              |   117 +-
 cmd/nodepackgen/generate.go                        |   576 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   165 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
 config.example.yaml                                |    17 +
 internal/ai/ai_test.go                             |    47 +
 internal/api/handlers/credentials.go               |    73 +
 internal/api/handlers/executions.go                |     1 +
 internal/api/handlers/interop.go                   |    61 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   119 +-
 internal/api/middleware/embed.go                   |    10 +-
 internal/api/routes.go                             |    12 +-
 internal/api/server.go                             |    15 +-
 internal/api/workflows_test.go                     |    73 +-
 internal/binary/binary.go                          |   212 +
 internal/binary/binary_test.go                     |   210 +
 internal/config/config.go                          |    33 +
 internal/credentials/builtin.go                    |   153 +
 internal/credentials/credentials.go                |    98 +-
 internal/credentials/credentials_test.go           |   242 +
 internal/credentials/registry.go                   |   340 +
 internal/engine/authenticate.go                    |    95 +
 internal/engine/runner.go                          |   858 +-
 internal/engine/runner_test.go                     |  1229 +-
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
 internal/interop/n8n/corpus/baseline.json          |   436 +
 internal/interop/n8n/corpus/corpus.go              |   250 +
 internal/interop/n8n/corpus/doc.go                 |    19 +
 internal/interop/n8n/corpus/fixtures/README.md     |    12 +
 .../n8n/corpus/fixtures/control-manual-set.json    |    40 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   574 +
 internal/interop/n8n/export_test.go                |    10 +
 internal/interop/n8n/n8n.go                        |   922 +-
 internal/interop/n8n/n8n_test.go                   |  1365 ++-
 internal/interop/n8n/parameters.go                 |   124 +-
 internal/loadoptions/loadoptions.go                |   353 +
 internal/loadoptions/loadoptions_test.go           |   354 +
 internal/node/icon.go                              |    94 +
 internal/node/registry.go                          |   634 +-
 internal/node/registry_test.go                     |   610 +-
 internal/nodepack/nodepack.go                      |   424 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
 internal/nodepack/trigger.go                       |   433 +
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
 internal/routing/executor.go                       |   571 +
 internal/routing/request.go                        |   408 +
 internal/routing/response.go                       |   177 +
 internal/routing/routing.go                        |   373 +
 internal/routing/routing_test.go                   |   764 ++
 internal/scheduler/scheduler.go                    |     8 +-
 internal/scheduler/scheduler_test.go               |    41 +-
 internal/webhook/export_test.go                    |    14 +
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
 internal/webhook/shape.go                          |   248 +
 internal/webhook/shape_test.go                     |   211 +
 internal/webhook/webhook.go                        |   322 +-
 internal/webhook/webhook_test.go                   |   473 +-
 internal/workflow/compiler.go                      |   347 +-
 internal/workflow/document.go                      |   100 +-
 internal/workflow/document_test.go                 |   881 +-
 internal/workflow/typeversion.go                   |   159 +
 internal/workflow/typeversion_openapi.go           |    28 +
 internal/workflow/typeversion_test.go              |   125 +
 nodes/ai.go                                        |    64 +-
 nodes/ai_test.go                                   |    28 +-
 nodes/annotation.go                                |    62 +
 nodes/bindings_test.go                             |   129 +
 nodes/code.go                                      |    28 +-
 nodes/code_test.go                                 |    68 +-
 nodes/core.go                                      |    50 +-
 nodes/database.go                                  |    14 +-
 nodes/database_test.go                             |     8 +-
 nodes/executors.go                                 |    64 +-
 nodes/executors_test.go                            |   113 +
 nodes/http.go                                      |   193 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
 nodes/routing.go                                   |    23 +
 nodes/telegram.go                                  |   407 +
 nodes/telegram_download.go                         |   223 +
 nodes/telegram_lifecycle.go                        |   423 +
 nodes/telegram_test.go                             |   616 ++
 nodes/unsupported.go                               |   116 +-
 nodes/webhook.go                                   |    47 +-
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/pack-trigger-202409.json                |   124 +
 packs/waha/pack-trigger-202502.json                |   130 +
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1193 ++
 schemas/workflow-v1.schema.json                    |     2 +-
 scripts/corpus-sync.sh                             |   225 +
 sdk/src/generated/models.ts                        |   457 +-
 third_party/waha/LICENSE                           |    19 +
 third_party/waha/PROVENANCE.md                     |    57 +
 third_party/waha/openapi-202409.json               |  8129 ++++++++++++++
 third_party/waha/openapi-202502.json               | 11084 +++++++++++++++++++
 .../lib/api/generated/credentials/credentials.ts   |    96 +-
 .../models/{unsupported.ts => activationNotice.ts} |    10 +-
 .../lib/api/generated/models/activationResource.ts |    23 +
 web/src/lib/api/generated/models/condition.ts      |    14 +
 .../api/generated/models/credentialRequirement.ts  |    15 +
 web/src/lib/api/generated/models/definition.ts     |    17 +
 .../generated/models/executionNodeRunResource.ts   |     2 +
 .../lib/api/generated/models/executionResource.ts  |     2 +
 .../lib/api/generated/models/executionSummary.ts   |     2 +
 web/src/lib/api/generated/models/exportIssue.ts    |    16 +
 .../api/generated/models/exportIssueSeverity.ts    |    19 +
 .../generated/models/exportedWorkflowResource.ts   |     4 +-
 .../lib/api/generated/models/expressionGrammar.ts  |    16 +
 web/src/lib/api/generated/models/field.ts          |     1 +
 .../lib/api/generated/models/getNodeIconParams.ts  |    19 +
 .../lib/api/generated/models/getNodeIconTheme.ts   |    15 +
 web/src/lib/api/generated/models/importIssue.ts    |    20 +
 .../api/generated/models/importIssueSeverity.ts    |    19 +
 .../generated/models/importedWorkflowResource.ts   |     7 +-
 web/src/lib/api/generated/models/index.ts          |    27 +-
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
 .../workflow-lifecycle/workflow-lifecycle.ts       |     3 +-
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
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   208 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 297 files changed, 65661 insertions(+), 1179 deletions(-)
```
