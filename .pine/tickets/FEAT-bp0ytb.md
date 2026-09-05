---
id: FEAT-bp0ytb
title: Add the WAHA trigger with per-event outputs
status: done
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-qe6wb8
    - FEAT-91as16
    - FEAT-5kv1jq
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:01:37Z"
updated: "2026-09-05T11:03:35Z"
---

## Scope

WAHA delivers every event of a session to one webhook URL and distinguishes them by a `body.event` field. The n8n WAHA Trigger turns that single stream into a fan-out node with one output per event type, so a workflow wires `message` to one branch and `message.ack` to another. Reproducing that shape is what lets an imported WAHA template keep its wiring: the connections in the JSON are output indexes, and an index means nothing unless the outputs are in the same order.

The orders differ per version. `202409` publishes 20 events and `202502` publishes 26, and the two orderings diverge from index 5 onward — so the index-to-event table is a property of the typeVersion, not of the node, and has to be generated from the vendored spec rather than typed by hand once. Register the trigger at both versions, exactly as the action pack is.

Two engine facts make this node the sharpest test of the p1 work. First, `dependenciesComplete` in `internal/engine/runner.go` returns true as soon as every upstream node has *run* — it never asks whether the connecting port carried any items — so a 20-output trigger that emits on one port today would still start every one of the other 19 branches. Second, `internal/webhook/webhook.go`'s `requestPayload` hardcodes one item shape, `{method, path, headers, query, body}`, for every trigger; WAHA templates read `$json.event`, `$json.session` and `$json.payload` at the top level, so the envelope has to be node-type aware before any of this routes correctly.

The n8n node also does two things badly that KilasFlow should not copy. It registers nothing with WAHA, so the URL has to be pasted into the session configuration by hand and an imported workflow silently receives nothing; and it verifies no signature, so anyone who learns the URL can inject events.

## Acceptance criteria

- [x] The trigger is registered at both `202409` and `202502`, with one named output per event, and the index-to-event table for each version is generated from the vendored spec.
- [x] An inbound delivery is routed to exactly the output matching its `body.event`, and only that branch executes; a delivery carrying an unknown event is routed to a catch-all output rather than dropped.
- [x] The trigger item carries WAHA's envelope at the top level, so `$json.event`, `$json.session` and `$json.payload` resolve without a wrapper path.
- [x] Activation registers a webhook binding for the trigger through registry-driven extraction, with no node type hardcoded at the composition root.
- [x] Optional auto-registration installs the workflow's own webhook URL into the WAHA session on activation and removes it on deactivation, using the `wahaApi` credential; it is off by default.
- [x] When auto-registration is off, activation returns a notice naming the exact URL to paste into WAHA's session configuration, so an imported workflow cannot look correct while receiving nothing. **The API returns it; nothing shows it yet** — the SPA has no activation control at all, so FEAT-sdjdh2 owns the other half.
- [x] When a webhook secret is configured, `X-Webhook-Hmac` is verified as SHA-512 over the raw request body and a failing delivery is rejected before an execution is created.
- [x] Repeated deliveries carrying the same `X-Webhook-Request-Id` produce one execution, since WAHA retries.
- [x] A media-bearing delivery attaches the media through the binary store (`engine.Request.Binaries`, from FEAT-0f87fn) as a `workflow.BinaryRef` on the item, never as bytes in item JSON.

## Outcome

The trigger ships at both versions, **20 events at `202409` and 26 at `202502`**, generated from each document's own `WAHAWebhookMessage.event` enum in the same run as the action pack. The orders diverge at index 5 — `message.revoked` against `message.waiting` — and a test asserts they do, because two versions whose event orders were identical would mean one of them had not been generated from its own document. A connection in an imported workflow is an output **index**, so that order is the contract and is never sorted.

**The fan-out is real, not decorative.** The executor emits on one port and writes an empty slot to every other, and `TestOnlyTheBranchWiredToTheDeliveredEventRuns` compiles a two-branch workflow, delivers `message`, and asserts the `message.ack` branch was recorded as skipped rather than run. Without the branch pruning from FEAT-k3grr5 this node would have started twenty-five branches on every delivery.

**A trigger pack is data.** `nodepack.Trigger` carries the event table, the delivery shape, the signature header, the retry header, the lifecycle descriptors and the media paths, and `RegisterTrigger` installs all four halves at once — event table, delivery shape, verifier, lifecycle hook. A trigger missing any one of them registers cleanly and then misbehaves in a way that looks like a different bug: a delivery shaped as the wrong envelope, a signature never checked, a workflow that receives nothing.

**Three decisions worth keeping:**

- *Verification is per node, not per node type.* A node with no secret configured is not verified at all. That is deliberate: the endpoint is an unguessable minted route, and every imported workflow arrives with no secret because the package it came from cannot verify one — refusing those would make importing a working workflow produce a broken one. A node that *does* carry a secret is then held to it strictly, and a missing or malformed signature is a refusal.
- *Auto-registration is opt-in, through `webhook.GatedLifecycle`.* Registering a webhook writes to a customer's own WAHA instance, and importing a workflow and pressing Activate is not a thing that should quietly reconfigure somebody's WhatsApp gateway. When it is off, `ActivationNotice` produces the notice instead.
- *Media is downloaded at the trigger, once, or not at all.* A webhook that links to media is a webhook whose payload expires behind the sender's own auth. The URL comes from whoever is delivering, so it is checked against the egress policy before the dial — `TestMediaFromAnInternalAddressIsRefused` covers the case where this node would otherwise be an SSRF gadget — and only the reference reaches the item.

**Two improvements the work forced:**

- `webhook.substitute` was replacing placeholders key by key over a map, so a credential value containing another field's placeholder would expand it: a bot token with the right seven characters could pull a second field of the same credential into the request. It is now one pass, and an unresolved placeholder stays visible rather than becoming an empty string.
- A lifecycle descriptor naming a credential type now gets that credential's own authentication applied, not only its fields substituted into templates. Both forms are needed — Telegram's setWebhook wants the token in the URL, WAHA's wants an `X-Api-Key` header — but a descriptor that had to name the header itself would be a second place to get it wrong, with the secret written into a template.

## Implementation Plan

Generate the event table alongside the action pack — same spec, same generator run, one more output artifact — so the two can never drift. The trigger definition then declares `len(events) + 1` output ports, and the executor is a few lines: read `event` from the trigger item, look up the port index for the node's own version, and emit `request.Input` on that index with every other port empty.

That executor is correct and still useless without branch pruning: with today's runner every branch downstream of an empty port runs anyway. Do not work around it inside the node — a node cannot stop a downstream node from running — and do not ship this ticket without a test that wires two events to two branches, delivers one, and asserts the other branch produced no node run.

Settle the two open questions here rather than deferring them. Take auto-registration, implemented as an opt-in parameter that is off by default: default-on would change the behaviour of an imported workflow relative to n8n and would write to a customer's WAHA instance as a side effect of activation, which is not a thing an import should do silently. It uses the activate and deactivate lifecycle hooks the trigger-lifecycle work introduces, calling `PUT /api/sessions/{session}` through the routing interpreter so the same SSRF policy and credential scope apply. Take HMAC verification too, active whenever a secret is configured on the node: it costs a comparison, it is the only thing standing between a leaked URL and injected WhatsApp events, and it needs the raw request bytes — which is precisely why the raw-body capture work has to land first, since `requestPayload` re-marshals the body through `map[string]any` and the original bytes are gone by the time any handler sees them.

The trap is the notice. An activation notice that only appears in an API response nobody reads is the same as no notice; it has to reach the import screen and the editor's activation path, or the failure mode this ticket exists to prevent survives intact.

## References

- Roadmap plan, p3 section, entry V2-p3-4: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `internal/engine/runner.go` — `dependenciesComplete` and `nodeInput`; `NodeOutput` is indexed by the definition's declared output-port order.
- `internal/webhook/webhook.go` — `requestPayload`'s fixed `{method, path, headers, query, body}` envelope, and `Extract`, which filters on one node type supplied at composition (`cmd/kilasflow/main.go:138`).
- `nodes/webhook.go` — `webhookTrigger`, `executeWebhook` and `WebhookPath` as the shape a new trigger follows.
- `nodes/core.go` `ifNode` — the existing two-output fan-out and how named ports map to output indexes.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-05.

- Base: `8d531d39` (last commit at or before ticket created 2026-09-05)
- Commits (2):
  - `cc61e991` — feat(engine): store binary payloads behind the item contract
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
 .pine/tickets/FEAT-bp0ytb.md                       |    78 +
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
 Makefile                                           |    19 +
 cmd/kilasflow/main.go                              |    96 +-
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
 internal/api/handlers/interop.go                   |    58 +-
 internal/api/handlers/nodes.go                     |   262 +-
 internal/api/handlers/workflows.go                 |   119 +-
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
 internal/nodepack/nodepack.go                      |   424 +
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
 internal/webhook/lifecycle.go                      |   304 +
 internal/webhook/lifecycle_test.go                 |   229 +
 internal/webhook/request_lifecycle.go              |   207 +
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
 packs/waha/REPORT-202409.md                        |   100 +
 packs/waha/REPORT-202502.md                        |   128 +
 packs/waha/manifest-202409.json                    |   124 +
 packs/waha/manifest-202502.json                    |   124 +
 packs/waha/pack-202409.json                        |  2794 +++++
 packs/waha/pack-202502.json                        |  3844 +++++++
 packs/waha/waha.go                                 |   107 +
 packs/waha/waha_test.go                            |  1130 ++
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
 web/src/lib/workflow-editor/node-visual.ts         |   206 +-
 web/src/lib/workflow-editor/ports.test.ts          |    98 +-
 web/src/lib/workflow-editor/ports.ts               |    34 +-
 web/src/lib/workflow-editor/visibility.test.ts     |    35 +
 web/src/lib/workflow-editor/visibility.ts          |   179 +
 .../(dashboard)/app/workflows/[id]/+page.svelte    |    17 +-
 .../(dashboard)/executions/[id]/+page.svelte       |    26 +
 284 files changed, 60857 insertions(+), 1131 deletions(-)
```
