---
id: FEAT-8r9n21
title: Interpret declarative node routing in Go
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-5s1w0t
    - FEAT-pd3p6x
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T04:59:38Z"
updated: "2026-09-05T10:22:56Z"
---

## Scope

n8n has two ways to write a node. A programmatic node ships an `execute()` function; a declarative node ships only metadata — `requestDefaults` on the description, and a `routing` object on each property that says how that property becomes part of an HTTP request. `@devlikeapro/n8n-nodes-waha`'s action node is entirely the second kind: it has no `execute()` at all, just 124 OpenAPI-derived operations of `routing: {request: {…}}`. That is the single fact that makes WAHA reachable without a JavaScript runtime, and this ticket is the interpreter that cashes it in.

KilasFlow has no such interpreter today. Every outbound call is hand-written Go against fixed parameter keys: `nodes/http.go`'s `HTTPExecutor` reads `method`, `url`, `sendQuery`, `queryParameters`, `sendHeaders`, `headers`, `sendBody`, `bodyType`, `body`, `responseFormat`, `neverError` and `timeoutSeconds`, and nothing else in the process can describe a request from data. Adding a node therefore means adding Go, which does not scale to 124 operations and does not scale at all to a generated pack.

Implementing the routing model in Go is also what preserves the security posture a JavaScript sidecar would have thrown away. Every request this interpreter makes goes through `internal/safehttp` — `safehttp.NewClient(policy)` re-checks the resolved IP at dial time and on every redirect hop, and `Policy.ReadBody` bounds the response — and through the same credential path `HTTPExecutor.authenticate` uses: resolve via `engine.Request.Credentials.ResolveCredential`, check the declared credential type against `resolved.Type`, check the target host against `credentials.Record{AllowedDomains}.AllowsHost`, then `credentials.Apply`. Third-party JavaScript executing its own `fetch` could not have been held to any of that.

The subset to implement is the subset the generated packs and the Telegram node actually emit: `requestDefaults` (baseURL, headers, `url` suffix), `routing.request` (method, url, qs, body, headers), `routing.send` (property placement into body or query, dot notation, `type`, `value`), `routing.output.postReceive` (`rootProperty`, `setKeyValue`, `limit`) and offset pagination. n8n's `preSend`/`postReceive` function forms are JavaScript closures and have no representation here; a pack that needs one must fail at registration rather than at run time.

## Acceptance criteria

- [x] A node definition can carry request defaults and per-property routing metadata, and one shared executor runs any node so described with no node-specific Go code.
- [x] `routing.request` supports method, URL (including a baseURL from `requestDefaults` and placeholders resolved from the node's own parameters), query string, headers and body; `routing.send` places a parameter into body or query, honouring `property` in dot notation, `type` and a literal `value`.
- [x] `postReceive` supports `rootProperty`, `setKeyValue` and `limit`, and offset pagination walks pages until the page is short or a bound is reached; a `postReceive` or pagination type the interpreter does not implement is rejected when the pack is registered, never ignored at run time.
- [x] Every request the interpreter makes passes the `safehttp` policy: a routed node aimed at a loopback or private address fails with the policy's own error, proven by test.
- [x] A routed node authenticates only with a credential of the type its definition declares, and only when the target host is inside that credential's `AllowedDomains`; both refusals are proven by test.
- [x] A failing routed call reports the node name, the resource and operation values in effect, and the HTTP status, so the error names what a user can actually change.
- [x] Response items are produced deterministically: one item per element of the extracted root property, or one item for a non-array response, with binary responses deferred to the binary-store ticket rather than silently discarded.

## Outcome

`internal/routing` is the interpreter. `Registry` holds a `Node` — request defaults, per-property routing, per-option routing — keyed by `{type, version}`; `Executor` runs any node so described, and there is no node-specific Go anywhere in the path.

**The metadata does not live on `node.Definition`.** That type is served by the node-types API and is a published contract; a `routing` field would put a node's outbound URLs, header names and credential template references into a JSON body handed to every browser that opens the editor, to describe something the editor has no use for. A pack ships two things that travel together instead: a definition, which is what the editor sees, and a routing description, which is what the interpreter sees. The decision is written into the package doc.

**Visibility is read from the definition rather than copied.** The interpreter walks `definition.Parameters` in declaration order and skips anything `property.VisibleProperty` says is hidden. A node keeps the parameters of every resource it has ever been set to, so without that check a node switched from Message to Chat would still send the message body it no longer shows. Copying the rule into routing metadata would have produced a second copy that disagrees with the editor's about whether a field exists.

**Two loops, kept apart.** A declarative node runs once per item and pagination walks pages inside that run. `TestOffsetPaginationWalksPagesPerInputItem` asserts the offsets are `0,2,0,2` — the offset resets for the second input item — because the failure mode here is one list silently counted twice.

**The expression dialect took the second option in the plan.** Routing templates are resolved by `internal/expression` with three extra roots — `$parameter`, `$value` and `$credentials` — rather than by a second private evaluator. A second evaluator over tenant-authored data would have been a second attack surface that drifted from the first within a release. The roots are gated on `AllowRouting` and absent from `Roots()`, so the editor never offers them; a user-authored expression that names one gets an error saying where it is valid. `$credentials` is filled from `credentials.Split`, so it carries the credential type's non-secret fields only, and a type this build does not know yields nothing rather than everything.

Two defects the tests caught while writing them. The effective request's method, base URL and URL were never template-resolved, so `/api/chats/{{ $parameter.chatId }}/messages` was sent literally — fixed, and templates are resolved only on the pack's own strings, never on a value a `routing.send` already placed, because re-evaluating the user's own message text would turn a message containing `{{` into a parse error or into an expression. And the policy is checked before the dial as well as at it: `TestTheURLIsCheckedBeforeTheServerIsContacted` asserts the server is never contacted.

The credential-and-host-scope check moved out of `nodes/http.go` into `engine.Request.Authenticate`, with `ResolveNodeCredential` split off because the interpreter needs the record before it has a request to authenticate — a routing template may read `$credentials.baseUrl`, which decides what URL is built at all. `engine.Request.ExpressionContext` moved for the same reason: one context builder, so a `{{ }}` in an HTTP URL and one in a pack's routing template see the same data.

Deferred deliberately: `postReceive: binaryData`. It is refused at registration, so a pack that needs it fails to load rather than losing bytes at run time. Wiring it to the binary store belongs with the pack generator in FEAT-znm60y, where the metadata that needs it is produced.

## Implementation Plan

Put the model in a new package — `internal/routing` — holding the metadata types and the interpreter, and add a `RoutingExecutorID` registered in `nodes/executors.go`'s `RegisterExecutors` map alongside `HTTPExecutorID`. Keep the metadata types plain data with JSON tags: a generated pack is data on disk, and the same types must survive a round trip through it. Do not put routing metadata on `node.Definition` itself yet if that forces an API change you do not want in this ticket — the executor can read it from a pack-side table keyed by `{type, version}` — but decide once and write the decision into the package doc.

Build the interpreter in the order a request is built: resolve the effective request (defaults merged with `routing.request`), then apply every property's `routing.send`, then execute through `safehttp`, then run `postReceive` over the decoded response, then paginate. Lift the credential block out of `nodes/http.go:299` (`HTTPExecutor.authenticate`) into a shared helper both executors call, rather than copying it — a second copy of that check is a second place for the host-scope test to be forgotten.

The trap is the expression dialect. n8n's routing metadata is full of strings like `={{ $parameter["chatId"] }}` and `={{ $credentials.baseUrl }}`; KilasFlow marks an expression explicitly as `{"mode":"expression","value":"…"}` and its grammar (`internal/expression`) is data access only, over the roots `$json`, `$input`, `$node`, `$env`, `$execution` and `$itemIndex` — there is no `$parameter` and no `$credentials`. Two options: teach the interpreter a second, private evaluator for routing templates, or translate routing strings into KilasFlow's marker at pack-generation time and add `$parameter` and `$credentials` roots to `expression.Context`. Take the second. A second evaluator is a second attack surface over tenant-authored data and would drift from the first within a release; adding two roots to the one evaluator keeps the "parameters can never become code" property that `TestEvaluateRejectsAnythingThatIsNotDataAccess` guards. `$credentials` must expose non-secret fields only — a base URL, never a token — and the test that proves it belongs in this ticket.

The second trap is quieter: a declarative node runs once per item, so an operation with pagination produces items per input item, and the interpreter must keep the two loops distinct or a two-item input silently doubles a paginated list.

## References

- Roadmap plan, p3 section, entry V2-p3-1: `.pine/roadmap.md`.
- n8n 2.34.0 reference checkout, `packages/workflow/src/Interfaces.ts`: `INodePropertyRouting`, `INodeRequestSend`, `INodeRequestOutput`, `PostReceiveAction` and its `IPostReceiveRootProperty` / `IPostReceiveSetKeyValue` / `IPostReceiveLimit` variants, `IN8nRequestOperations` with `IN8nRequestOperationPaginationOffset`, and `requestDefaults` on the node description.
- `nodes/http.go` — the hand-written executor this generalises, and `authenticate` at line 299 for the credential and host-scope path.
- `internal/safehttp/safehttp.go` — `Policy`, `NewClient`, `CheckURL`, `CheckAddress`, `ReadBody`.
- `internal/expression/expression.go` — `Context` and the roots it currently exposes.

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
 .pine/tickets/FEAT-8r9n21.md                       |    75 +
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
 255 files changed, 46804 insertions(+), 1130 deletions(-)
```
