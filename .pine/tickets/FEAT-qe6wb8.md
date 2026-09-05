---
id: FEAT-qe6wb8
title: Ship the WAHA node pack for both published versions
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-znm60y
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:01:00Z"
updated: "2026-09-05T10:48:18Z"
---

## Scope

Run the generator over WAHA's own MIT `openapi.json` and ship the result as a registered node pack, for both versions the n8n package publishes: `202409` and `202502`. This is the ticket where the whole native-first bet becomes visible — a customer's WAHA workflow finds a node with the same resources, the same operations and the same parameter names it was authored against, running in the Go binary with no npm package anywhere.

The pack needs a credential type it cannot have yet. WAHA authenticates with a base URL plus an `X-Api-Key` header, and `internal/credentials` is a closed package-level `map[string]Definition` holding exactly six types — `httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`, `postgres`, `mysql`, `sqlite` — with an `Apply` switch that returns `credential type %q cannot authenticate an HTTP request` for anything else. `wahaApi` has to be registerable from outside that map, and the pack has to declare that it requires it. The editor side is closed too: `web/src/lib/workflow-editor/credentials.ts` maps node type to credential types in a hardcoded `BY_NODE_TYPE` table and `credentialTypesFor` returns `[]` for anything absent, so an unlisted node renders with no credential picker at all.

Two hidden defaults decide whether real templates work. The generator that produced the n8n package injects custom defaults that are not in the OpenAPI document: `session` defaults to `={{ $json.session }}` and `chatId` to `={{ $json.payload.from }}`. Official WAHA templates omit both parameters entirely and rely on those defaults, so a pack that treats absent as empty produces workflows that look correct, activate, and send nothing anywhere. Both defaults have to survive generation into KilasFlow's own explicit expression marker.

There is a live defect waiting for the `session` default in particular. `internal/execution/redact.go` lists `session`, `sessionid` and `sessiontoken` among its sensitive keys, and redaction runs at webhook ingest and on every executions write, with the runner rehydrating the trigger item from the redacted record — so `$json.session` resolves to `[redacted]` and every WAHA call goes to a session that does not exist. That is fixed elsewhere in the roadmap; this ticket owns the test that proves it stays fixed.

## Acceptance criteria

- [x] Both `202409` and `202502` are registered as distinct versions of one node type, generated from the vendored spec, and the operation count for each is recorded in the ticket evidence from the spec itself rather than assumed.
- [x] `resource` and `operation` option values are byte-identical to the values in the WAHA workflow JSON fixtures in the import corpus, checked by a test that reads the fixtures rather than a hand-copied list.
- [x] A `wahaApi` credential type exists with a base URL and an API key field, the key is secret and never returned after storage, and it is applied as the `X-Api-Key` header on every request the pack makes.
- [x] The credential's base URL supplies the routing base URL, and the credential's `AllowedDomains` scope is enforced on the resolved host exactly as it is for the HTTP Request node.
- [x] The `session` and `chatId` defaults are present on every operation that takes them, expressed in KilasFlow's explicit expression marker, and an end-to-end test proves `session` reaches the outbound request as the real session name and not `[redacted]`.
- [x] A send-text operation runs against a stub WAHA server through `safehttp` and the credential store, and the recorded execution shows the request and the response without the API key.
- [x] Regenerating both packs from the unchanged spec produces no diff.
- [x] The WAHA node renders on the canvas with a credential picker and its own icon rather than the fallback grey box.
- [x] The `operation` picker narrows to the chosen resource, through an internal options loader with `DependsOn: ["resource"]` — a pack holds one `operation` property, so without it the picker lists every operation in the pack.

## Outcome

Both versions ship, generated from the vendored MIT documents and registered as distinct versions of one node type. The counts are read from the specs by the test, not typed:

| Version | API | Operations in spec | Generated | Resources | Parameters |
| --- | --- | --- | --- | --- | --- |
| `202409` | WAHA 2024.9.2 | 98 | **97** | 12 | 43 |
| `202502` | WAHA 2025.2.5 | 124 | **123** | 13 | 64 |

Each version drops exactly one operation, and `REPORT-<version>.md` names it: `VersionController_get` and `ServerController_get` both reduce to the operation name `Get` inside the resource `Observability`, so the second is refused rather than overwriting the first.

**The claim the epic rests on, checked against real workflows.** `TestEveryResourceAndOperationInTheRealTemplatesExists` reads the 13 WAHA templates in the corpus, pulls the `resource` and `operation` strings out of all 23 WAHA nodes in them, and asserts every pair exists in a generated pack. All 23 match — `Chatting/Send Text`, `Auth/Get QR`, `Observability/Stop` and the rest — with no hand-copied list anywhere in the test. It skips cleanly when the corpus is not materialised.

**A defect the tests found.** A property's declared default that is *itself an expression* was never resolved. `expression.Resolve` only sees parameters the node actually carries, and a default lives on the definition — so `session` reached WAHA as the literal object `{"mode":"expression","value":"{{ $json.session }}"}`. Official templates omit `session` and `chatId` entirely and rely on those defaults, so every one of them would have failed. Defaults are now resolved in the interpreter, and a default that resolves to nothing is treated as an absent parameter rather than a null one: sending `null` to a service that documents a default is worse than sending nothing.

**Deviations from the plan, both deliberate:**

- The node type is `pack.waha`, not `kilasflow.waha`. `node.BuiltinPrefix` reserves that namespace for nodes compiled into this binary and refuses a pack that claims it — a supply-chain rule that postdates this ticket's plan. Recorded in `packs/waha/README.md`.
- The specs are read from `third_party/waha/`, not from a checkout outside the repository. FEAT-w9kqeg vendored them under their own MIT licence, and `internal/guardrails` forbids any build input under the read-only reference checkout, so generating from there was never available.

**The base URL is a non-secret credential field, on purpose.** The pack builds every request from `{{ $credentials.baseUrl }}`, and `$credentials` exposes non-secret fields only — marking it secret would leave the pack with no address to call. The API key is secret, is never returned after storage, and is applied as `X-Api-Key` by the shared credential path, so it reaches the request as a header and never enters an item, an execution record or a log line.

The `session` regression test is the one this ticket owed: `session` was on the redaction sensitive-key list while redaction runs at webhook ingest and on every executions write, so every WAHA call would have gone to a session named `[redacted]`. The test redacts a real envelope, proves a credential-shaped sibling key is still destroyed, and then proves the session reaches the stub server intact.

## Implementation Plan

Vendor nothing into this repository: the spec lives in the reference checkout beside the n8n one, and only the generated pack is committed. Two spec files, two manifests, two pack files, one node type registered at two versions — the registry keys on `{type, version}`, so both coexist without special handling once versions are wider than the current `int`.

Take the credential work in the order that keeps the tree green: open credential registration first, register `wahaApi`, teach `Apply` a header-with-fixed-name form (it is `httpHeaderAuth` with the name pinned to `X-Api-Key`, so reuse rather than duplicate the header path), then generate. The base URL is the piece with no precedent — no existing credential type carries one, and the routing interpreter needs it before it can build a URL. Expose it to routing as a non-secret credential field, never as part of the applied auth.

Decide the node type string deliberately and write the decision down. It must be a KilasFlow type, not `@devlikeapro/n8n-nodes-waha.WAHA`; the importer maps the foreign string onto it. Use `kilasflow.waha`, keeping the namespace rule the rest of the catalogue follows, and leave the original type visible in the import diagnostics rather than in the node type itself.

The trap is the version pair. The two specs are not additive — operations move and parameters change between `202409` and `202502` — so generate both independently and never derive one from the other. A workflow imported at `202409` must keep resolving against the `202409` definition for the life of the process, which is exactly what the registry's immutability rule already guarantees.

## References

- Roadmap plan, p3 section, entry V2-p3-3: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `github.com/devlikeapro/n8n-nodes-waha` (MIT), cloned outside this repository — `openapi.json` for both `202409` and `202502`.
- `internal/credentials/credentials.go` — the closed `definitions` map, `Field`, `Definition`, `Apply`'s switch, and `Record.AllowsHost`.
- `internal/execution/redact.go` — `session`, `sessionid` and `sessiontoken` on the sensitive-key list.
- `web/src/lib/workflow-editor/credentials.ts` — `BY_NODE_TYPE` and `credentialTypesFor`.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (2):
  - `f3cb58e9` — feat(nodepack): generate node packs from an OpenAPI document
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
 .pine/tickets/FEAT-qe6wb8.md                       |    81 +
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
 .pine/tickets/FEAT-znm60y.md                       |   350 +
 .pine/tickets/FEAT-ztxs5p.md                       |    59 +
 Makefile                                           |    18 +
 cmd/kilasflow/main.go                              |    87 +-
 cmd/nodepackgen/generate.go                        |   509 +
 cmd/nodepackgen/generate_test.go                   |   374 +
 cmd/nodepackgen/main.go                            |   145 +
 cmd/nodepackgen/openapi.go                         |   168 +
 cmd/nodepackgen/testdata/manifest.json             |    12 +
 cmd/nodepackgen/testdata/pack.golden.json          |   238 +
 cmd/nodepackgen/testdata/report.golden.md          |    20 +
 cmd/nodepackgen/testdata/spec.json                 |    98 +
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
 internal/credentials/builtin.go                    |   130 +
 internal/credentials/credentials.go                |    91 +-
 internal/credentials/credentials_test.go           |   241 +
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
 internal/nodepack/nodepack.go                      |   369 +
 internal/nodepack/startcase.go                     |   136 +
 internal/nodepack/startcase_test.go                |    82 +
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
 internal/routing/request.go                        |   388 +
 internal/routing/response.go                       |   172 +
 internal/routing/routing.go                        |   354 +
 internal/routing/routing_test.go                   |   764 ++
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
 packs/waha/README.md                               |    32 +
 packs/waha/REPORT-202409.md                        |    99 +
 packs/waha/REPORT-202502.md                        |   127 +
 packs/waha/manifest-202409.json                    |    17 +
 packs/waha/manifest-202502.json                    |    17 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/waha.go                                 |    78 +
 packs/waha/waha_test.go                            |   540 +
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
 web/src/lib/workflow-editor/node-visual.test.ts    |   163 +-
 web/src/lib/workflow-editor/node-visual.ts         |   206 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 283 files changed, 59377 insertions(+), 1128 deletions(-)
```
