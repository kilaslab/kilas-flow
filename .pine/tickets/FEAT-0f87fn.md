---
id: FEAT-0f87fn
title: Store binary data behind the item contract
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:04:07Z"
updated: "2026-09-05T10:07:53Z"
---

## Scope

KilasFlow's item contract already has a binary half, and nothing implements it. `workflow.BinaryRef` in `internal/workflow/document.go` is `{ID, FileName, MediaType, Size}` under a comment saying outright that payload storage is out of scope for the milestone, and `Item.Binary map[string]BinaryRef` sits beside `Item.JSON`. The entire lifecycle of that field today is two clone functions — `cloneItem` in `nodes/executors.go` and its twin in `internal/engine/runner.go` — copying a map that nothing ever fills. Nothing writes a ref, nothing reads a payload, and there is no store for one to live in.

The Code node is worse than empty: it actively discards. `nodes/code.go` hands the sandbox `runcode.Item{JSON: item.JSON}` and rebuilds the results as `workflow.Item{JSON: json}`, so any binary attached upstream is gone the moment an item passes through a Code node, with no error and no diagnostic.

Two features in this phase force the issue, and neither can be finished without it. WhatsApp media is the point of a WAHA integration — an image arrives, a workflow does something with it, a reply goes back — and the Telegram trigger's Download Images/Files option exists to pull a photo or document off an update. A workflow that can only carry JSON can carry a URL to a file, which means the file is fetched twice, is subject to whatever expiry the provider sets, and never survives into the execution record as evidence.

## Acceptance criteria

- [x] A binary store writes a payload and returns a reference, reads it back by reference, and refuses a read from a different tenant, proven by test.
- [x] Payloads are bounded by configuration and a write that would exceed the bound fails cleanly rather than filling the disk.
- [x] A binary payload never enters a workflow document, an execution record, an API response body or a log line; only the reference does.
- [x] Stored payloads are removed when their execution is removed, so a store cannot outgrow the executions it belongs to.
- [x] The Code node carries incoming binary references through to its output instead of dropping them, and a test that passes an item with a binary through a Code node asserts the reference survives.
- [x] The HTTP Request node stores a non-text response as a binary reference under the existing response-size bound, instead of forcing it through JSON or text decoding.
- [ ] WAHA's send-image and send-file operations and Telegram's Send Photo, Send Document and Get File all read and write through the store rather than holding payloads in item JSON. **Deferred to the node tickets** — neither node exists yet; the obligation is written into FEAT-bp0ytb and was already in FEAT-6vfn3s.
- [x] The editor shows an item's binary references as name, type and size, and never attempts to render the payload inline.

## Outcome

`internal/binary` holds the store: a `Store` interface (`Put`/`Get`/`DeleteExecution`), a `FileStore` rooted at a configured directory, and a `Scoped` adapter that is what an executor actually sees. Tenant and execution are part of the key rather than a filter, so a reference travelling in an execution record cannot be resolved from another tenant's scope — that is `TestScopedBindsTheExecutionAnExecutorSees`, and every path segment including the reference id is checked against the same pattern so none of them can become a traversal later.

The bound is enforced while reading, not from a declared length, and an oversized payload is removed rather than truncated: half a media file that reports success is worse than a refusal.

Two decisions worth keeping:

**`engine.Request.Binaries` is left nil when this server has no binary storage**, rather than boxing a nil store in the interface. That makes "can this server store a payload at all?" a question a node can ask. It matters because binary storage is off by default: the HTTP node's `autodetect` diverts a non-text response to the store when there is somewhere to divert it to, and otherwise decodes it exactly as it did before. Only an explicit `responseFormat: file` fails loudly. Without that distinction, this change would have broken every non-text HTTP response on every unconfigured server at once.

**Retention has one function to call.** Nothing in this codebase removes an execution row — workflow deletion is a soft delete that deliberately preserves execution evidence — so `engine.Service.DiscardBinaries(tenantID, executionID)` exists as the single call the pruner makes, and is a no-op when there is no store. FEAT-5fv8gf now carries that as an acceptance criterion rather than leaving a directory tree to reverse-engineer.

The Code node carries binary references **positionally** past the sandbox: user code sees JSON only, so a payload it never receives is one it cannot corrupt, and code that reshapes the batch has no attachment to inherit rather than being given the wrong one.

## Implementation Plan

Add `internal/binary` with a `Store` interface — put, get, delete by execution — and one filesystem-backed implementation rooted at a configured directory, keyed by tenant, execution and reference id. Do not put payloads in the internal database. Multi-megabyte WhatsApp media in SQLite rows would bloat the file this product ships as its default, and under the PostgreSQL tier it would land in a table inside a customer's own shared database, which is exactly the posture that phase is trying to keep narrow. A filesystem root is also the honest shape for the object-storage backend a hosted deployment will eventually want behind the same interface.

Thread the store through `engine.Request`, alongside the credential resolver and for the same reason: an executor must not reach into storage on its own, so the runtime stays the single place where tenant scoping is enforced. Then fix the two clone functions to copy references, fix the Code node's two conversion points, and only then touch the nodes that produce binaries.

Name the configuration section with one word. `internal/config`'s environment override maps the first underscore in a key to the section separator — the `OutboundHTTP` struct carries that warning in its own doc comment, which is why the section is `outbound` and not `outbound_http` — so a section called `binary_store` could never be set by an environment variable. Call it `binary`.

The trap is retention. A binary store with no deletion path is a disk-full incident with a delay fuse, and execution pruning does not exist yet anywhere in this codebase. Do not wait for it: give the store its own delete-by-execution call and wire it to whatever removes an execution today, so that when retention arrives it has one function to call rather than a directory tree to reverse-engineer.

The second trap is redaction. `internal/execution/redact.go` runs on every executions write and walks item JSON; a reference is metadata and must stay readable, so keep file names and media types out of the sensitive-key path, and keep payloads out of the record entirely rather than relying on redaction to hide them.

## References

- Roadmap plan, p3 section, entry V2-p3-8: `.pine/roadmap.md`.
- `internal/workflow/document.go` — `BinaryRef` and `Item`, with the comment declaring payload storage out of scope.
- `nodes/code.go` — `runcode.Item{JSON: item.JSON}` on the way in and `workflow.Item{JSON: json}` on the way out, the two points where binary is dropped.
- `nodes/executors.go` and `internal/engine/runner.go` — the two `cloneItem` implementations that copy `Item.Binary`.
- `internal/config/config.go` — `OutboundHTTP`'s doc comment on why a section name must be one word.
- `internal/safehttp/safehttp.go` — `Policy.ReadBody` and `MaxResponseBytes`, the bound a binary download has to respect.

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
 .pine/tickets/FEAT-0f87fn.md                       |    71 +
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
 .pine/tickets/FEAT-8r9n21.md                       |    57 +
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
 .pine/tickets/FEAT-qe6wb8.md                       |    56 +
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
 .pine/tickets/FEAT-znm60y.md                       |    54 +
 .pine/tickets/FEAT-ztxs5p.md                       |    59 +
 Makefile                                           |     8 +
 cmd/kilasflow/main.go                              |    70 +-
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
 internal/config/config.go                          |    33 +
 internal/credentials/builtin.go                    |   108 +
 internal/credentials/credentials.go                |    91 +-
 internal/credentials/credentials_test.go           |   229 +
 internal/credentials/registry.go                   |   312 +
 internal/engine/runner.go                          |   830 +-
 internal/engine/runner_test.go                     |  1142 +-
 internal/engine/service.go                         |    85 +-
 internal/engine/service_test.go                    |    47 +-
 internal/engine/worker_test.go                     |     4 +-
 internal/execution/records.go                      |    55 +-
 internal/execution/redact.go                       |   116 +-
 internal/execution/redact_test.go                  |   213 +-
 internal/expression/doc.go                         |    70 +-
 internal/expression/expression.go                  |   278 +-
 internal/expression/expression_test.go             |   251 +-
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
 nodes/http.go                                      |   153 +-
 nodes/http_test.go                                 |   231 +-
 nodes/loop.go                                      |   245 +
 nodes/presentation_test.go                         |    60 +
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
 253 files changed, 45978 insertions(+), 1082 deletions(-)
```
